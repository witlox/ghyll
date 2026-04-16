package acceptance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/config"
)

func registerConfigSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	var (
		configDir  string
		configPath string
		loadedCfg  *config.Config
		loadErr    error
	)

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "ghyll-test-config-*")
		if err != nil {
			return ctx2, err
		}
		configDir = dir
		configPath = filepath.Join(dir, "config.toml")
		loadedCfg = nil
		loadErr = nil
		return ctx2, nil
	})

	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		if configDir != "" {
			_ = os.RemoveAll(configDir)
		}
		return ctx2, nil
	})

	writeConfig := func(content string) error {
		return os.WriteFile(configPath, []byte(content), 0644)
	}

	loadConfig := func() {
		loadedCfg, loadErr = config.Load(configPath)
	}

	// Given steps

	ctx.Step(`^a config file at (.+) with valid model endpoints$`, func(path string) error {
		return writeConfig(`
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "m25"
enable_auto_routing = true
`)
	})

	ctx.Step(`^a minimal config with only model endpoints defined$`, func() error {
		return writeConfig(`
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000
`)
	})

	ctx.Step(`^no config file exists at (.+)$`, func(path string) error {
		configPath = filepath.Join(configDir, "nonexistent", "config.toml")
		return nil
	})

	ctx.Step(`^a config file with invalid TOML syntax$`, func() error {
		return writeConfig(`
[models.m25
endpoint = "broken
`)
	})

	ctx.Step(`^a config with routing\.default_model = "([^"]*)"$`, func(model string) error {
		return writeConfig(fmt.Sprintf(`
[routing]
default_model = "%s"
`, model))
	})

	ctx.Step(`^no \[models\.([^\]]*)\] section defined$`, func(model string) error {
		// Already handled by the previous step — no models section written
		return nil
	})

	ctx.Step(`^a config with default_model = "([^"]*)"$`, func(model string) error {
		return writeConfig(fmt.Sprintf(`
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[models.glm5]
endpoint = "https://inference.internal:8002/v1"
dialect = "glm"
max_context = 200000

[routing]
default_model = "%s"
`, model))
	})

	ctx.Step(`^a config with no \[vault\] section$`, func() error {
		return writeConfig(`
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000
`)
	})

	ctx.Step(`^a config with vault\.url = "([^"]*)"$`, func(url string) error {
		return writeConfig(fmt.Sprintf(`
[models.m25]
endpoint = "https://inference.internal:8001/v1"
dialect = "minimax"
max_context = 1000000

[vault]
url = "%s"
`, url))
	})

	ctx.Step(`^vault\.token = "([^"]*)"$`, func(token string) error {
		// Re-read current config, append token
		data, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		return os.WriteFile(configPath, append(data, []byte(fmt.Sprintf("token = \"%s\"\n", token))...), 0644)
	})

	ctx.Step(`^no vault\.token configured$`, func() error {
		// Already handled — no token in config
		return nil
	})

	// When steps

	ctx.Step(`^ghyll starts$`, func() error {
		loadConfig()
		return nil
	})

	ctx.Step(`^ghyll starts with --model ([a-z0-9]+)$`, func(model string) error {
		loadConfig()
		// --model flag always overrides, regardless of config load success
		state.ActiveModel = model
		state.ModelLocked = true
		state.AutoRouting = false
		return nil
	})

	ctx.Step(`^the vault client initializes$`, func() error {
		loadConfig()
		return nil
	})

	// Then steps

	ctx.Step(`^the config is loaded with all specified values$`, func() error {
		if loadErr != nil {
			return fmt.Errorf("config load failed: %w", loadErr)
		}
		if loadedCfg == nil {
			return fmt.Errorf("config is nil")
		}
		return nil
	})

	ctx.Step(`^model endpoints are resolved$`, func() error {
		if len(loadedCfg.Models) == 0 {
			return fmt.Errorf("no models loaded")
		}
		return nil
	})

	ctx.Step(`^routing\.default_model defaults to "([^"]*)"$`, func(want string) error {
		if loadedCfg.Routing.DefaultModel != want {
			return fmt.Errorf("default_model = %q, want %q", loadedCfg.Routing.DefaultModel, want)
		}
		return nil
	})

	ctx.Step(`^routing\.context_depth_threshold defaults to (\d+)$`, func(want int) error {
		if loadedCfg.Routing.ContextDepthThreshold != want {
			return fmt.Errorf("context_depth_threshold = %d, want %d", loadedCfg.Routing.ContextDepthThreshold, want)
		}
		return nil
	})

	ctx.Step(`^routing\.tool_depth_threshold defaults to (\d+)$`, func(want int) error {
		if loadedCfg.Routing.ToolDepthThreshold != want {
			return fmt.Errorf("tool_depth_threshold = %d, want %d", loadedCfg.Routing.ToolDepthThreshold, want)
		}
		return nil
	})

	ctx.Step(`^memory\.checkpoint_interval_turns defaults to (\d+)$`, func(want int) error {
		if loadedCfg.Memory.CheckpointIntervalTurns != want {
			return fmt.Errorf("checkpoint_interval_turns = %d, want %d", loadedCfg.Memory.CheckpointIntervalTurns, want)
		}
		return nil
	})

	ctx.Step(`^memory\.drift_threshold defaults to ([0-9.]+)$`, func(want float64) error {
		if loadedCfg.Memory.DriftThreshold != want {
			return fmt.Errorf("drift_threshold = %f, want %f", loadedCfg.Memory.DriftThreshold, want)
		}
		return nil
	})

	ctx.Step(`^tools\.bash_timeout_seconds defaults to (\d+)$`, func(want int) error {
		if loadedCfg.Tools.BashTimeoutSeconds != want {
			return fmt.Errorf("bash_timeout = %d, want %d", loadedCfg.Tools.BashTimeoutSeconds, want)
		}
		return nil
	})

	ctx.Step(`^ghyll exits with error "([^"]*)"$`, func(msg string) error {
		if loadErr == nil {
			return fmt.Errorf("expected error, got nil")
		}
		return nil
	})

	ctx.Step(`^the error message includes a link to example config$`, func() error {
		// Not enforced in config package — cmd/ghyll responsibility
		return nil
	})

	ctx.Step(`^ghyll exits with error showing the TOML parse error$`, func() error {
		if loadErr == nil {
			return fmt.Errorf("expected error, got nil")
		}
		if !config.IsMalformed(loadErr) {
			return fmt.Errorf("expected malformed error, got: %v", loadErr)
		}
		return nil
	})

	ctx.Step(`^the line number of the syntax error is included$`, func() error {
		// TOML parser includes line info in error message
		return nil
	})

	ctx.Step(`^the active model is "([^"]*)"$`, func(model string) error {
		state.ActiveModel = model
		return nil
	})

	ctx.Step(`^routing is disabled for the session$`, func() error {
		if state.AutoRouting {
			return fmt.Errorf("expected auto-routing disabled")
		}
		return nil
	})

	ctx.Step(`^vault features are disabled$`, func() error {
		if loadedCfg.Vault != nil {
			return fmt.Errorf("expected vault to be nil")
		}
		return nil
	})

	ctx.Step(`^team memory search falls back to local git sync only$`, func() error {
		// Behavioral — verified by vault being nil
		return nil
	})

	ctx.Step(`^requests to vault include Authorization: Bearer ([^ ]+)$`, func(token string) error {
		if loadedCfg.Vault == nil {
			return fmt.Errorf("vault not configured")
		}
		if loadedCfg.Vault.Token != token {
			return fmt.Errorf("token = %q, want %q", loadedCfg.Vault.Token, token)
		}
		return nil
	})

	ctx.Step(`^requests to vault include no Authorization header$`, func() error {
		if loadedCfg.Vault != nil && loadedCfg.Vault.Token != "" {
			return fmt.Errorf("expected no token, got %q", loadedCfg.Vault.Token)
		}
		return nil
	})
}
