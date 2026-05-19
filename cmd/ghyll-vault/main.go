package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/memory"
	"github.com/witlox/ghyll/ui"
	"github.com/witlox/ghyll/vault"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		ui.Info("ghyll-vault %s", version)
		return
	}

	configPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}

	if cfg.Vault == nil {
		ui.Errorf("no [vault] section in config")
		os.Exit(1)
	}

	dbPath := filepath.Join(os.Getenv("HOME"), ".ghyll", "vault.db")
	store, err := memory.OpenStore(dbPath)
	if err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
	defer func() { _ = store.Close() }()

	srv := vault.NewServer(store, cfg.Vault.Token)

	addr := ":9090"
	ui.Info("ghyll-vault listening on %s", addr)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		ui.Errorf("%v", err)
		os.Exit(1)
	}
}
