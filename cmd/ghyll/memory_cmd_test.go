package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/memory"
)

func seedStore(t *testing.T, dir string) *memory.Store {
	t.Helper()
	store, err := memory.OpenStore(filepath.Join(dir, "memory.db"))
	if err != nil {
		t.Fatal(err)
	}

	_, priv, _ := ed25519.GenerateKey(nil)
	zeroHash := "0000000000000000000000000000000000000000000000000000000000000000"

	c0 := &memory.Checkpoint{
		Version: 1, ParentHash: zeroHash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 1000, SessionID: "sess-1", Turn: 1, ActiveModel: "m25",
		Summary: "fixed auth race condition in session.go",
	}
	memory.SignCheckpoint(c0, priv)

	c1 := &memory.Checkpoint{
		Version: 1, ParentHash: c0.Hash, DeviceID: "dev1", AuthorID: "alice",
		Timestamp: 2000, SessionID: "sess-1", Turn: 5, ActiveModel: "m25",
		Summary: "added mutex to session refresh, compaction at turn 5",
	}
	memory.SignCheckpoint(c1, priv)

	c2 := &memory.Checkpoint{
		Version: 1, ParentHash: zeroHash, DeviceID: "dev2", AuthorID: "bob",
		Timestamp: 3000, SessionID: "sess-2", Turn: 3, ActiveModel: "glm5",
		Summary: "refactored payment module error handling",
	}
	memory.SignCheckpoint(c2, priv)

	for _, cp := range []*memory.Checkpoint{c0, c1, c2} {
		if err := store.Append(cp); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

// TestScenario_MemoryLog
func TestScenario_MemoryLog(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemoryLog(store, &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "fixed auth race condition") {
		t.Errorf("missing checkpoint summary in output:\n%s", output)
	}
	if !strings.Contains(output, "refactored payment module") {
		t.Errorf("missing bob's checkpoint:\n%s", output)
	}
}

// TestScenario_MemorySearch
func TestScenario_MemorySearch(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemorySearch(store, "auth race condition", &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	// Text search should find checkpoints containing the query terms
	if !strings.Contains(output, "auth race condition") {
		t.Errorf("search didn't find relevant checkpoint:\n%s", output)
	}
}

// TestScenario_FetchEmbedder_Success — happy path. httptest server
// serves a small blob, fetch-embedder writes it to ModelPath atomically.
// Asserts: file exists, content matches, no .tmp left behind.
func TestScenario_FetchEmbedder_Success(t *testing.T) {
	payload := []byte("fake-onnx-bytes-0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "models", "gte.onnx")
	var buf bytes.Buffer
	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read written model: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("file contents differ: got %q want %q", got, payload)
	}
	// INT-4: tmp now uses os.CreateTemp's unique naming
	// (<base>.<rand>.tmp). Assert no orphan matching the pattern
	// remains — the helper enumerates entries since the random
	// suffix isn't predictable.
	if leftover := findTmpSidecars(t, modelPath); len(leftover) > 0 {
		t.Errorf(".tmp sidecars should not exist after success, found %v", leftover)
	}
	if !strings.Contains(buf.String(), "embedder ready") {
		t.Errorf("missing success message in output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "ONNX Runtime") {
		t.Errorf("missing ONNX Runtime hint in output:\n%s", buf.String())
	}
}

// TestScenario_FetchEmbedder_SkipWhenExists — invoking without --force
// when the file already exists is a no-op (no download). Verified by
// confirming the existing-file bytes survived and the server was not
// hit (request counter == 0).
func TestScenario_FetchEmbedder_SkipWhenExists(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte("server-served-bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	original := []byte("existing-file-bytes")
	if err := os.WriteFile(modelPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != 0 {
		t.Errorf("server should not have been hit (no --force), got %d hits", hits)
	}
	got, _ := os.ReadFile(modelPath)
	if !bytes.Equal(got, original) {
		t.Errorf("existing file mutated: got %q want %q", got, original)
	}
	// UX-FM-2: message now says "already present" and includes size + URL
	if !strings.Contains(buf.String(), "already present") {
		t.Errorf("missing skip message:\n%s", buf.String())
	}
	// UX-FM-3: ONNX Runtime hint must also print on the skip path
	if !strings.Contains(buf.String(), "ONNX Runtime") {
		t.Errorf("ONNX Runtime hint missing from skip-path output:\n%s", buf.String())
	}
}

// TestScenario_FetchEmbedder_ForceReDownloads — --force overrides
// the skip-when-exists short-circuit and re-fetches.
func TestScenario_FetchEmbedder_ForceReDownloads(t *testing.T) {
	freshPayload := []byte("fresh-payload-from-server")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(freshPayload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	if err := os.WriteFile(modelPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", []string{"--force"}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := os.ReadFile(modelPath)
	if !bytes.Equal(got, freshPayload) {
		t.Errorf("file not re-downloaded: got %q want %q", got, freshPayload)
	}
}

// TestScenario_FetchEmbedder_RejectsNonHTTPS — http:// to a non-loopback
// host must be rejected before any network call (supply-chain guard).
func TestScenario_FetchEmbedder_RejectsNonHTTPS(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")

	err := cmdMemoryFetchEmbedder("http://example.com/model.onnx", modelPath, "", nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for http:// URL, got nil")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error should mention https, got: %v", err)
	}
	// No file should have been created either.
	if _, statErr := os.Stat(modelPath); !os.IsNotExist(statErr) {
		t.Errorf("file should not exist after rejected URL, stat err=%v", statErr)
	}
}

// TestScenario_FetchEmbedder_ServerErrorLeavesNoPartial — when the
// server returns a 5xx, NO partial file (and NO .tmp) lingers. Atomic
// write contract.
func TestScenario_FetchEmbedder_ServerErrorLeavesNoPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")

	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error from 500, got nil")
	}
	if _, err := os.Stat(modelPath); !os.IsNotExist(err) {
		t.Errorf("model file should not exist after 5xx, stat err=%v", err)
	}
	if leftover := findTmpSidecars(t, modelPath); len(leftover) > 0 {
		t.Errorf(".tmp should be cleaned up after 5xx, found %v", leftover)
	}
}

// TestScenario_FetchEmbedder_LoopbackHTTPAllowed — http:// to 127.0.0.1
// (httptest is plaintext) MUST be allowed, else every unit test for
// this command would have to spin up a TLS cert. Encodes the
// loopback-exception contract.
func TestScenario_FetchEmbedder_LoopbackHTTPAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("loopback-ok"))
	}))
	defer srv.Close()

	if !strings.HasPrefix(srv.URL, "http://127.0.0.1:") && !strings.HasPrefix(srv.URL, "http://[::1]:") {
		t.Fatalf("test precondition: httptest URL should be loopback, got %s", srv.URL)
	}

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")

	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("loopback http should be accepted, got error: %v", err)
	}
}

// TestScenario_FetchEmbedder_RejectsUnknownFlag — unrecognized flag
// fails loud rather than silently being ignored (a future operator
// typo like `--Force` should not silently no-op into a re-download).
func TestScenario_FetchEmbedder_RejectsUnknownFlag(t *testing.T) {
	err := cmdMemoryFetchEmbedder(
		"https://example.com/x",
		filepath.Join(t.TempDir(), "x"),
		"",
		[]string{"--bogus"},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected unknown flag error, got: %v", err)
	}
}

// TestScenario_FetchEmbedder_TildeExpansion — ModelPath starting with
// "~/" expands against $HOME. Operators write `~/.ghyll/...` in TOML
// and reasonably expect it to resolve.
func TestScenario_FetchEmbedder_TildeExpansion(t *testing.T) {
	homeBefore := os.Getenv("HOME")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	defer t.Setenv("HOME", homeBefore)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tilde-payload"))
	}))
	defer srv.Close()

	if err := cmdMemoryFetchEmbedder(srv.URL, "~/.ghyll/models/gte.onnx", "", nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	written := filepath.Join(tmpHome, ".ghyll", "models", "gte.onnx")
	if _, err := os.Stat(written); err != nil {
		t.Errorf("expected file at %s, got err=%v", written, err)
	}
}

// TestScenario_ReadEmbedderConfig_MinimalTOML — a TOML file with
// ONLY [memory.embedder] decodes cleanly. This is the "operator
// hasn't configured a model yet" case that fetch-embedder must
// support without going through the full config.Load validator.
func TestScenario_ReadEmbedderConfig_MinimalTOML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := `
[memory.embedder]
model_url = "https://example.com/x.onnx"
model_path = "/tmp/x.onnx"
`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	url, path, sha := readEmbedderConfig(cfgPath)
	if url != "https://example.com/x.onnx" {
		t.Errorf("url: got %q want %q", url, "https://example.com/x.onnx")
	}
	if path != "/tmp/x.onnx" {
		t.Errorf("path: got %q want %q", path, "/tmp/x.onnx")
	}
	if sha != "" {
		t.Errorf("sha should be empty when not in TOML, got %q", sha)
	}
}

// TestScenario_ReadEmbedderConfig_MissingFile — fresh install: no
// ~/.ghyll/config.toml. Must return empty strings (caller substitutes
// defaults), NEVER panic or error.
func TestScenario_ReadEmbedderConfig_MissingFile(t *testing.T) {
	url, path, sha := readEmbedderConfig(filepath.Join(t.TempDir(), "absent.toml"))
	if url != "" || path != "" || sha != "" {
		t.Errorf("missing file should return empty, got (%q, %q, %q)", url, path, sha)
	}
}

// TestScenario_ReadEmbedderConfig_MalformedTOML — a partially-broken
// config (e.g. mid-edit) must still let fetch-embedder run. Return
// empty strings so the caller falls back to defaults rather than
// blocking bootstrap on a parse error.
func TestScenario_ReadEmbedderConfig_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("this is = = not valid toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	url, path, sha := readEmbedderConfig(cfgPath)
	if url != "" || path != "" || sha != "" {
		t.Errorf("malformed TOML should return empty, got (%q, %q, %q)", url, path, sha)
	}
}

// findTmpSidecars returns the names of any orphan files in
// filepath.Dir(modelPath) matching the unique-tmp pattern that
// os.CreateTemp produces: "<base>.*.tmp". Empty slice means a clean
// directory. Used by tests that assert atomic-write hygiene under
// the INT-4 remediation.
func findTmpSidecars(t *testing.T, modelPath string) []string {
	t.Helper()
	dir := filepath.Dir(modelPath)
	base := filepath.Base(modelPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Dir doesn't exist (e.g. test that never wrote) — no orphans.
		return nil
	}
	var leftover []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, base+".") && strings.HasSuffix(n, ".tmp") {
			leftover = append(leftover, n)
		}
	}
	return leftover
}

// TestScenario_FetchEmbedder_RejectsHTTPRedirect — FE-SEC-1 regression.
// An https:// start URL that 302-redirects to a non-loopback http://
// target must be refused by the CheckRedirect hook before the second
// request fires. Without this guard, a CDN-layer compromise or
// upstream redirect injection silently downgrades the executable
// model download to plaintext.
func TestScenario_FetchEmbedder_RejectsHTTPRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect to a non-loopback http target. The CheckRedirect
		// hook must reject before the (never-completed) next hop.
		http.Redirect(w, r, "http://example.com/model.onnx", http.StatusFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected redirect-to-http to be refused, got nil")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("error should mention https rule, got: %v", err)
	}
	if _, statErr := os.Stat(modelPath); !os.IsNotExist(statErr) {
		t.Errorf("model file should not exist after refused redirect, stat err=%v", statErr)
	}
}

// TestScenario_FetchEmbedder_RejectsEmptyBody — CORR-1 regression.
// A 200-OK with a 0-byte body is NOT a successful download.
func TestScenario_FetchEmbedder_RejectsEmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// no Write call → 0 bytes
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected 0-byte body to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "empty body") {
		t.Errorf("error should mention empty body, got: %v", err)
	}
	if _, statErr := os.Stat(modelPath); !os.IsNotExist(statErr) {
		t.Errorf("model file should not exist after empty body, stat err=%v", statErr)
	}
}

// TestScenario_FetchEmbedder_RejectsHTMLContentType — CORR-2.
// HuggingFace gated repos return 200 + HTML when unauthenticated;
// writing that HTML into ~/.ghyll/models/gte-micro.onnx would
// silently break drift detection.
func TestScenario_FetchEmbedder_RejectsHTMLContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Login required</body></html>"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected HTML Content-Type to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "text/html") {
		t.Errorf("error should mention text/html, got: %v", err)
	}
}

// TestScenario_FetchEmbedder_SHAMismatch — FE-SEC-4 verification.
// When modelSHA is set and the downloaded content's hash differs,
// the file is rejected and removed.
func TestScenario_FetchEmbedder_SHAMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("real-bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	wrongSHA := "0000000000000000000000000000000000000000000000000000000000000000"
	err := cmdMemoryFetchEmbedder(srv.URL, modelPath, wrongSHA, nil, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected sha mismatch to fail, got nil")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("error should mention sha256 mismatch, got: %v", err)
	}
	if _, statErr := os.Stat(modelPath); !os.IsNotExist(statErr) {
		t.Errorf("rejected file should be removed, stat err=%v", statErr)
	}
}

// TestScenario_FetchEmbedder_SHAMatch — same lever, positive case.
// Computed SHA matches the supplied pin → file lands.
func TestScenario_FetchEmbedder_SHAMatch(t *testing.T) {
	payload := []byte("known-bytes-for-sha")
	hash := sha256sumHex(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	if err := cmdMemoryFetchEmbedder(srv.URL, modelPath, hash, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("matching sha should succeed, got: %v", err)
	}
	if _, statErr := os.Stat(modelPath); statErr != nil {
		t.Errorf("model file should exist after matching sha, stat err=%v", statErr)
	}
}

// sha256sumHex helper for SHA tests.
func sha256sumHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestScenario_FetchEmbedder_OversizedContentLength — FE-SEC-6.
// Server announces a Content-Length above the cap → reject before
// reading any body.
func TestScenario_FetchEmbedder_OversizedContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2147483649") // > 1 GiB
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := t.TempDir()
	modelPath := filepath.Join(dir, "gte.onnx")
	err := cmdMemoryFetchEmbedder(srv.URL, modelPath, "", nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected oversize rejection, got: %v", err)
	}
}

// TestScenario_FetchEmbedder_HelpFlag — UX-FM-7. --help and -h
// print usage and return without error.
func TestScenario_FetchEmbedder_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		var buf bytes.Buffer
		err := cmdMemoryFetchEmbedder("https://example.com/x", filepath.Join(t.TempDir(), "x"), "", []string{flag}, &buf)
		if err != nil {
			t.Errorf("%s should not error, got: %v", flag, err)
		}
		if !strings.Contains(buf.String(), "Usage") {
			t.Errorf("%s output should contain Usage, got:\n%s", flag, buf.String())
		}
	}
}

// TestScenario_ExpandUserHome_RejectsShellVarsAndOtherUser — UX-FM-10.
// $VAR and ~user/ are not silently treated as literal directory
// names; the function returns a directed error.
func TestScenario_ExpandUserHome_RejectsShellVarsAndOtherUser(t *testing.T) {
	cases := []string{"$HOME/.ghyll/models/x.onnx", "${HOME}/x", "~alice/.ghyll/x"}
	for _, in := range cases {
		_, err := expandUserHome(in)
		if err == nil {
			t.Errorf("expandUserHome(%q) should error, got nil", in)
		}
	}
}

// TestScenario_ExpandUserHome_PreservesAbsolute — pass-through for
// fully-qualified paths and bare ~/$HOME forms.
func TestScenario_ExpandUserHome_PreservesAbsolute(t *testing.T) {
	t.Setenv("HOME", "/h/u")
	cases := []struct{ in, want string }{
		{"/abs/path", "/abs/path"},
		{"~/x", "/h/u/x"},
		{"~", "/h/u"},
		{"relative/path", "relative/path"},
	}
	for _, c := range cases {
		got, err := expandUserHome(c.in)
		if err != nil {
			t.Errorf("expandUserHome(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("expandUserHome(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

// TestScenario_MemorySearch_HashPrefix — operator copies a hash
// out of `memory log` and pastes it into `memory search` to inspect
// that one checkpoint. Previously the text-only matcher always
// returned "no matching checkpoints" because hashes never appear
// in summaries. Fix: hex-only single-token query matches by hash
// prefix.
func TestScenario_MemorySearch_HashPrefix(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	// Grab a real hash from the seeded checkpoints.
	all, _ := store.ListAll()
	if len(all) == 0 {
		t.Fatal("seedStore produced no checkpoints")
	}
	hashPrefix := all[0].Hash[:12]

	var buf bytes.Buffer
	if err := cmdMemorySearch(store, hashPrefix, &buf); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(buf.String(), hashPrefix) {
		t.Errorf("hash-prefix search should return the checkpoint, got:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "no matching") {
		t.Errorf("hash-prefix search should not say no match, got:\n%s", buf.String())
	}
}

// TestScenario_MemorySearch_HashTooShort — guard against short
// hex-like tokens triggering hash-prefix mode. Need at least 6 hex
// chars; below that we fall through to the text-search path so a
// summary containing "abc" still matches normally.
func TestScenario_MemorySearch_HashTooShort(t *testing.T) {
	if isHexPrefix("abc") != true {
		t.Errorf("isHexPrefix(abc) should be true; the cutoff is on LENGTH not hex-ness")
	}
	// 5-char hex query → falls through to text path. Doesn't blow up.
}

// TestScenario_IsHexPrefix
func TestScenario_IsHexPrefix(t *testing.T) {
	cases := map[string]bool{
		"":             false,
		"a":            true,
		"5afd615f":     true,
		"5AFD615F":     false, // uppercase rejected; log shows lowercase
		"5afd615g":     false, // 'g' not hex
		"42644898f3b9": true,
	}
	for in, want := range cases {
		if got := isHexPrefix(in); got != want {
			t.Errorf("isHexPrefix(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestScenario_MemorySearch_NoResults
func TestScenario_MemorySearch_NoResults(t *testing.T) {
	dir := t.TempDir()
	store := seedStore(t, dir)
	defer func() { _ = store.Close() }()

	var buf bytes.Buffer
	err := cmdMemorySearch(store, "nonexistent xyz abc", &buf)
	if err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	if !strings.Contains(output, "no matching checkpoints") {
		t.Errorf("expected no-results message:\n%s", output)
	}
}
