package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/config"
)

func main() {
	args := os.Args[1:]

	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ghyll run [dir] [--model <model>]")
		os.Exit(1)
	}

	switch args[0] {
	case "run":
		if err := runSession(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "ghyll: %v\n", err)
			os.Exit(1)
		}
	case "config":
		if len(args) > 1 && args[1] == "show" {
			if err := showConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "ghyll: %v\n", err)
				os.Exit(1)
			}
		}
	default:
		fmt.Fprintf(os.Stderr, "ghyll: unknown command %q\n", args[0])
		os.Exit(1)
	}
}

func runSession(args []string) error {
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

	// Load config
	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	// Resolve active model
	activeModel := cfg.Routing.DefaultModel
	modelLocked := false
	if modelFlag != "" {
		activeModel = modelFlag
		modelLocked = true
	}

	// Verify model exists in config
	if _, ok := cfg.Models[activeModel]; !ok {
		return fmt.Errorf("model %q not configured", activeModel)
	}

	fmt.Printf("ghyll [%s] %s ▸ ", activeModel, absDir)

	// Session loop will be wired here
	_ = modelLocked
	_ = cfg

	fmt.Println("session not yet implemented — packages ready, wiring pending")
	return nil
}

func showConfig() error {
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
