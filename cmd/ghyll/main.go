package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/ui"
)

var version = "dev"

func main() {
	initLogger()
	args := os.Args[1:]

	if len(args) < 1 {
		ui.Usage(
			"usage: ghyll run [dir] [--model <model>]",
			"       ghyll config show",
			"       ghyll memory search <query>",
			"       ghyll memory log",
			"       ghyll engine status [--dir <path>]",
			"       ghyll engine replay [--dir <path>]",
			"       ghyll engine verify-attestations [--dir <path>]",
			"       ghyll arrow show <arrow-id> [--dir <path>]",
			"       ghyll version",
		)
		os.Exit(1)
	}

	if args[0] == "version" {
		ui.Info("ghyll %s", version)
		return
	}

	switch args[0] {
	case "run":
		if err := cmdRun(args[1:]); err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}
	case "config":
		if len(args) > 1 && args[1] == "show" {
			if err := cmdConfigShow(); err != nil {
				ui.Errorf("%v", err)
				os.Exit(1)
			}
		}
	case "memory":
		if err := cmdMemoryMain(args[1:]); err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}
	case "engine":
		if err := cmdEngineMain(args[1:]); err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}
	case "arrow":
		if err := cmdArrowMain(args[1:]); err != nil {
			ui.Errorf("%v", err)
			os.Exit(1)
		}
	default:
		ui.Errorf("unknown command %q", args[0])
		os.Exit(1)
	}
}

// initLogger installs the default slog handler used by library code
// (engine.Journal, memory.SyncLoop, runner diagnostics). Honors
// GHYLL_LOG_LEVEL (debug|info|warn|error; case-insensitive; default
// warn so background sync hiccups stay visible without spamming the
// REPL) and GHYLL_LOG_FORMAT (text|json; default text).
//
// Diagnostics go to stderr by default. The interactive `ghyll run`
// flow swaps to a file handler (see redirectSlogToFile) once it has
// resolved the workdir, so background goroutines like memory.SyncLoop
// do not interrupt the REPL prompt with stderr writes.
func initLogger() {
	level := parseLogLevel(os.Getenv("GHYLL_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("GHYLL_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// parseLogLevel maps a case-insensitive level string to slog levels.
// Empty string and unknown values map to LevelWarn; unknown values
// also surface a one-line warning to stderr so misconfiguration is
// noticed (rather than silently degrading to warn).
func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "warn", "warning":
		return slog.LevelWarn
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		fmt.Fprintf(os.Stderr, "ghyll: GHYLL_LOG_LEVEL=%q not recognized; using warn\n", s)
		return slog.LevelWarn
	}
}

// redirectSlogToFile rebuilds the default slog handler to write into
// `<dir>/.ghyll/ghyll.log` (append mode). Called from cmdRun once
// absDir is known, so memory.SyncLoop / engine.Journal diagnostics
// land in a tail-able log instead of corrupting the REPL prompt on
// stderr. If the file cannot be opened the previous (stderr) handler
// is retained and the failure is surfaced to stderr once.
func redirectSlogToFile(dir string) {
	logPath := filepath.Join(dir, ".ghyll", "ghyll.log")
	if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o755); mkErr != nil {
		fmt.Fprintf(os.Stderr, "ghyll: cannot create log dir %s: %v (diagnostics stay on stderr)\n", filepath.Dir(logPath), mkErr)
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghyll: cannot open %s: %v (diagnostics stay on stderr)\n", logPath, err)
		return
	}
	level := parseLogLevel(os.Getenv("GHYLL_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("GHYLL_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(f, opts)
	} else {
		h = slog.NewTextHandler(f, opts)
	}
	slog.SetDefault(slog.New(h))
}

func cmdRun(args []string) error {
	workdir := "."
	var modelFlag string
	var resumeFlag bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				modelFlag = args[i+1]
				i++
			}
		case "--resume":
			resumeFlag = true
		default:
			workdir = args[i]
		}
	}

	absDir, err := filepath.Abs(workdir)
	if err != nil {
		return fmt.Errorf("resolve workdir: %w", err)
	}

	// 1. Load config
	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// 2. Acquire lockfile (invariant 31)
	lock, err := AcquireLock(absDir)
	if err != nil {
		return err
	}
	defer lock.Release()

	// 2a. Route slog diagnostics to a file so background goroutines
	// (memory.SyncLoop, engine.Journal) do not interrupt the REPL
	// prompt on stderr. CLI subcommands (engine status, memory log,
	// etc.) keep the stderr handler installed by initLogger.
	redirectSlogToFile(absDir)

	// 2b. Sandbox policy: warn (or refuse to start, if
	// GHYLL_REQUIRE_SANDBOX is set) when no sandbox is detected.
	// ghyll executes tool calls from the model directly; running
	// unsandboxed exposes the user to a compromised endpoint.
	// Operators with custom sandbox setups can set
	// GHYLL_SANDBOX_ASSUME_SAFE=<reason> to bypass.
	if err := EnforceSandboxPolicy(func(msg string) { ui.Info("%s", msg) }); err != nil {
		return err
	}

	// 3. Load or generate device key (invariant 29)
	keysDir := filepath.Join(os.Getenv("HOME"), ".ghyll", "keys")
	hostname, _ := os.Hostname()
	deviceID := hostname
	if deviceID == "" {
		deviceID = "default"
	}
	deviceKey, err := memory.LoadOrGenerateKey(keysDir, deviceID)
	if err != nil {
		return fmt.Errorf("key setup: %w", err)
	}
	ui.Status("ℹ", "device: %s", deviceKey.DeviceID)

	// 4. Open sqlite store
	dbPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "memory.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// 5. Initialize embedder (invariant 17: graceful if unavailable)
	embedderPath := cfg.Memory.Embedder.ModelPath
	if embedderPath == "" {
		embedderPath = filepath.Join(os.Getenv("HOME"), ".ghyll", "models", "gte-micro.onnx")
	}
	embedder, _ := memory.NewEmbedder(embedderPath, cfg.Memory.Embedder.Dimensions)
	defer embedder.Close()
	if !embedder.IsAvailable() {
		ui.Status("ℹ", "embedding model not available, drift detection disabled")
	}

	// 6. Setup git syncer
	var syncer *memory.Syncer
	syncer, err = memory.NewSyncer(absDir, cfg.Memory.Branch, deviceKey.DeviceID)
	if err != nil {
		ui.Status("⚠", "sync setup failed: %v", err)
	} else {
		if initErr := syncer.InitBranch(); initErr != nil {
			ui.Status("⚠", "memory branch init failed: %v", initErr)
			syncer = nil
		} else {
			pubPEM, _ := memory.MarshalPublicKey(deviceKey.PublicKey)
			_ = syncer.WritePublicKey(deviceKey.DeviceID, pubPEM)
			if fetchErr := syncer.Fetch(); fetchErr != nil {
				ui.Status("⚠", "initial sync failed: %v", fetchErr)
			}
		}
	}

	// 7. Start background sync
	var syncCancel context.CancelFunc
	if syncer != nil && cfg.Memory.AutoSync {
		var syncCtx context.Context
		syncCtx, syncCancel = context.WithCancel(context.Background())
		interval := time.Duration(cfg.Memory.SyncIntervalSeconds) * time.Second
		if interval == 0 {
			interval = 60 * time.Second
		}
		go memory.SyncLoop(syncCtx, syncer, interval)
	}
	defer func() {
		if syncCancel != nil {
			syncCancel()
		}
	}()

	// 8. Setup vault client
	var vaultClient *memory.VaultClient
	if cfg.Vault != nil {
		vaultClient = memory.NewVaultClient(cfg.Vault.URL, cfg.Vault.Token)
	}

	// 9. Generate session ID
	sessionID := fmt.Sprintf("%s-%d", deviceKey.DeviceID, time.Now().UnixNano())

	// 10. Determine repo remote for resume
	var repoRemote string
	if resumeFlag {
		remoteResult := memory.GitRemoteURL(absDir)
		if remoteResult != "" {
			repoRemote = remoteResult
		} else {
			repoRemote = absDir // fallback to path
		}
	}

	// 11. Create session
	output := func(msg string) { ui.Info("%s", msg) }
	sess, err := NewSession(SessionConfig{
		Cfg:         cfg,
		Store:       store,
		Syncer:      syncer,
		VaultClient: vaultClient,
		DeviceKey:   deviceKey,
		Embedder:    embedder,
		ModelFlag:   modelFlag,
		Resume:      resumeFlag,
		RepoRemote:  repoRemote,
		Workdir:     absDir,
		SessionID:   sessionID,
		Output:      output,
		Version:     version,
	})
	if err != nil {
		return err
	}
	defer sess.Close()

	ui.Info("ghyll [%s] %s", sess.ActiveModel(), absDir)

	// 11. Run interactive REPL
	REPL(sess, os.Stdin)

	// 12. Shutdown: final sync
	if syncer != nil {
		_ = syncer.CommitAndPush(context.Background())
	}

	return nil
}

func cmdConfigShow() error {
	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	ui.Info("Models: %d configured", len(cfg.Models))
	for name, m := range cfg.Models {
		ui.Info("  %s: %s (max %d tokens)", name, m.Endpoint, m.MaxContext)
	}
	ui.Info("Routing: default=%s, depth_threshold=%d, tool_threshold=%d",
		cfg.Routing.DefaultModel, cfg.Routing.ContextDepthThreshold, cfg.Routing.ToolDepthThreshold)
	if cfg.Vault != nil {
		ui.Info("Vault: %s", cfg.Vault.URL)
	} else {
		ui.Info("Vault: not configured")
	}
	return nil
}
