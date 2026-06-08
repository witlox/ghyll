package acceptance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/cucumber/godog"
)

// INT-2 remediation: hoist the ghyll-binary cache to package scope
// behind a sync.Once so multiple BDD scenarios (or future
// ghyll-memory-* features wired to this seam) share a single build.
// The original closure-scoped cache rebuilt the ~10 MB binary every
// scenario because registerMemoryFetchSteps runs once per scenario,
// resetting ghyllBin to "" before any cache hit could fire.
var (
	ghyllBinOnce sync.Once
	ghyllBinPath string
	ghyllBinErr  error
)

// registerMemoryFetchSteps wires the BDD steps for
// `ghyll memory fetch-embedder`. Each scenario gets a fresh
// httptest server + tempdir + config TOML; the operator step
// builds the `ghyll` binary once per scenario (cached) and shells
// out, so we exercise the real main.go dispatch path, not a direct
// function call. Per [[bdd-needs-test-depth]]: assertions key on
// the file artifact + the request counter — a stub stepping over
// the real subcommand could not produce both.
func registerMemoryFetchSteps(ctx *godog.ScenarioContext, _ *ScenarioState) {
	var (
		srv       *httptest.Server
		hits      atomic.Int64
		tmpDir    string
		modelPath string
		homeOrig  string
		payload   []byte
	)

	cleanup := func() {
		if srv != nil {
			srv.Close()
			srv = nil
		}
		if tmpDir != "" {
			_ = os.RemoveAll(tmpDir)
			tmpDir = ""
		}
		if homeOrig != "" {
			_ = os.Setenv("HOME", homeOrig)
			homeOrig = ""
		}
		hits.Store(0)
		modelPath = ""
		payload = nil
	}

	ctx.Before(func(ctx2 context.Context, _ *godog.Scenario) (context.Context, error) {
		cleanup()
		dir, err := os.MkdirTemp("", "ghyll-fetch-embedder-*")
		if err != nil {
			return ctx2, err
		}
		tmpDir = dir
		homeOrig = os.Getenv("HOME")
		// Isolate HOME so the subcommand's config.Load resolves
		// to OUR tmp config, not whatever real ~/.ghyll the dev
		// box has — otherwise a CI runner with a stale config
		// would non-determ this test.
		_ = os.Setenv("HOME", tmpDir)
		return ctx2, nil
	})
	ctx.After(func(ctx2 context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		cleanup()
		return ctx2, nil
	})

	ctx.Step(`^the embedder host serves a (\d+)-byte ONNX-shaped payload$`, func(n int) error {
		payload = make([]byte, n)
		for i := range payload {
			payload[i] = byte('A' + (i % 26))
		}
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(payload)
		}))
		return nil
	})

	ctx.Step(`^the config points ModelURL at that host and ModelPath into a temp dir$`, func() error {
		modelPath = filepath.Join(tmpDir, "models", "gte-micro.onnx")
		return writeTempConfigForFetch(tmpDir, srv.URL, modelPath)
	})

	ctx.Step(`^an existing embedder file at ModelPath with (\d+) bytes$`, func(n int) error {
		// Server is set up to fail the test if it gets hit.
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = w.Write([]byte("should-not-be-served"))
		}))
		modelPath = filepath.Join(tmpDir, "gte-micro.onnx")
		existing := make([]byte, n)
		for i := range existing {
			existing[i] = byte('X')
		}
		if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(modelPath, existing, 0o644); err != nil {
			return err
		}
		return writeTempConfigForFetch(tmpDir, srv.URL, modelPath)
	})

	ctx.Step(`^the operator runs "ghyll memory fetch-embedder"$`, func() error {
		bin, err := ensureGhyllBinary()
		if err != nil {
			return err
		}
		cmd := exec.Command(bin, "memory", "fetch-embedder")
		cmd.Env = append(os.Environ(), "HOME="+tmpDir)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			return fmt.Errorf("subcommand failed: %v\noutput:\n%s", runErr, out)
		}
		return nil
	})

	ctx.Step(`^the model file exists at ModelPath$`, func() error {
		if _, err := os.Stat(modelPath); err != nil {
			return fmt.Errorf("expected model at %s: %w", modelPath, err)
		}
		return nil
	})

	ctx.Step(`^the model file is (\d+) bytes$`, func(want int) error {
		info, err := os.Stat(modelPath)
		if err != nil {
			return err
		}
		if int(info.Size()) != want {
			return fmt.Errorf("model size: want %d got %d", want, info.Size())
		}
		return nil
	})

	ctx.Step(`^no "\.tmp" sidecar remains in the target directory$`, func() error {
		if _, err := os.Stat(modelPath + ".tmp"); !os.IsNotExist(err) {
			return fmt.Errorf("unexpected .tmp sidecar at %s.tmp (err=%v)", modelPath, err)
		}
		return nil
	})

	ctx.Step(`^the model file at ModelPath is still (\d+) bytes$`, func(want int) error {
		info, err := os.Stat(modelPath)
		if err != nil {
			return err
		}
		if int(info.Size()) != want {
			return fmt.Errorf("model size mutated: want %d got %d", want, info.Size())
		}
		return nil
	})

	ctx.Step(`^no HTTP request reached the embedder host$`, func() error {
		if got := hits.Load(); got != 0 {
			return fmt.Errorf("expected 0 server hits, got %d", got)
		}
		return nil
	})
}

// writeTempConfigForFetch writes a minimal ~/.ghyll/config.toml
// under the test's isolated HOME pointing the embedder at the given
// URL/path. Only the keys fetch-embedder reads are populated — the
// rest of config.Load applies defaults.
func writeTempConfigForFetch(home, url, modelPath string) error {
	cfgDir := filepath.Join(home, ".ghyll")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf(`
[memory.embedder]
model_url = %q
model_path = %q
dimensions = 384
`, url, modelPath)
	return os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0o600)
}

// ensureGhyllBinary builds the ghyll binary at most once per test
// process via sync.Once (INT-2). Subsequent scenarios reuse the
// already-built binary.
func ensureGhyllBinary() (string, error) {
	ghyllBinOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "ghyll-bin-*")
		if err != nil {
			ghyllBinErr = err
			return
		}
		bin := filepath.Join(tmp, "ghyll")
		// `go build` from the repo root — godog runs tests with
		// the package dir as cwd, so reach the cmd/ghyll target by
		// module path (works regardless of cwd).
		cmd := exec.Command("go", "build", "-o", bin, "github.com/witlox/ghyll/cmd/ghyll")
		if out, err := cmd.CombinedOutput(); err != nil {
			ghyllBinErr = fmt.Errorf("go build ghyll: %v\n%s", err, out)
			return
		}
		ghyllBinPath = bin
	})
	return ghyllBinPath, ghyllBinErr
}
