package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/memory"
)

var version = "dev"

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ghyll run [dir] [--model <model>]")
		fmt.Fprintln(os.Stderr, "       ghyll config show")
		fmt.Fprintln(os.Stderr, "       ghyll memory search <query>")
		fmt.Fprintln(os.Stderr, "       ghyll memory log")
		fmt.Fprintln(os.Stderr, "       ghyll version")
		os.Exit(1)
	}

	if args[0] == "version" {
		fmt.Printf("ghyll %s\n", version)
		return
	}

	switch args[0] {
	case "run":
		if err := cmdRun(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "ghyll: %v\n", err)
			os.Exit(1)
		}
	case "config":
		if len(args) > 1 && args[1] == "show" {
			if err := cmdConfigShow(); err != nil {
				fmt.Fprintf(os.Stderr, "ghyll: %v\n", err)
				os.Exit(1)
			}
		}
	case "memory":
		if err := cmdMemoryMain(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "ghyll: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "ghyll: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

func cmdRun(args []string) error {
	workdir := "."
	var modelFlag string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				modelFlag = args[i+1]
				i++
			}
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
	fmt.Printf("ℹ device: %s\n", deviceKey.DeviceID)

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
		fmt.Println("ℹ embedding model not available, drift detection disabled")
	}

	// 6. Setup git syncer
	var syncer *memory.Syncer
	syncer, err = memory.NewSyncer(absDir, cfg.Memory.Branch, deviceKey.DeviceID)
	if err != nil {
		fmt.Printf("⚠ sync setup failed: %v\n", err)
	} else {
		if initErr := syncer.InitBranch(); initErr != nil {
			fmt.Printf("⚠ memory branch init failed: %v\n", initErr)
			syncer = nil
		} else {
			pubPEM, _ := memory.MarshalPublicKey(deviceKey.PublicKey)
			_ = syncer.WritePublicKey(deviceKey.DeviceID, pubPEM)
			if fetchErr := syncer.Fetch(); fetchErr != nil {
				fmt.Printf("⚠ initial sync failed: %v\n", fetchErr)
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

	// 10. Create session
	output := func(msg string) { fmt.Println(msg) }
	sess, err := NewSession(SessionConfig{
		Cfg:         cfg,
		Store:       store,
		Syncer:      syncer,
		VaultClient: vaultClient,
		DeviceKey:   deviceKey,
		Embedder:    embedder,
		ModelFlag:   modelFlag,
		Workdir:     absDir,
		SessionID:   sessionID,
		Output:      output,
	})
	if err != nil {
		return err
	}

	fmt.Printf("ghyll [%s] %s\n", sess.ActiveModel(), absDir)

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
	fmt.Printf("Models: %d configured\n", len(cfg.Models))
	for name, m := range cfg.Models {
		fmt.Printf("  %s: %s (max %d tokens)\n", name, m.Endpoint, m.MaxContext)
	}
	fmt.Printf("Routing: default=%s, depth_threshold=%d, tool_threshold=%d\n",
		cfg.Routing.DefaultModel, cfg.Routing.ContextDepthThreshold, cfg.Routing.ToolDepthThreshold)
	if cfg.Vault != nil {
		fmt.Printf("Vault: %s\n", cfg.Vault.URL)
	} else {
		fmt.Println("Vault: not configured")
	}
	return nil
}
