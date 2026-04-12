package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/vault"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("ghyll-vault %s\n", version)
		return
	}

	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghyll-vault: %v\n", err)
		os.Exit(1)
	}

	if cfg.Vault == nil {
		fmt.Fprintln(os.Stderr, "ghyll-vault: no [vault] section in config")
		os.Exit(1)
	}

	dbPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "vault.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghyll-vault: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	srv := vault.NewServer(store, cfg.Vault.Token)

	addr := ":9090"
	fmt.Printf("ghyll-vault listening on %s\n", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		fmt.Fprintf(os.Stderr, "ghyll-vault: %v\n", err)
		os.Exit(1)
	}
}
