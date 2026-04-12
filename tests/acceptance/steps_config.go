package acceptance

import "github.com/cucumber/godog"

func registerConfigSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// Config feature steps — stubs for now.
	// Each step will be implemented when the config package is built.
	// Pending steps are reported by godog in non-strict mode.

	ctx.Step(`^a config file at (.+) with valid model endpoints$`, func(path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a minimal config with only model endpoints defined$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^no config file exists at (.+)$`, func(path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a config file with invalid TOML syntax$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a config with routing\.default_model = "([^"]*)"$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^no \[models\.([^\]]*)\] section defined$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a config with default_model = "([^"]*)"$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a config with no \[vault\] section$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a config with vault\.url = "([^"]*)"$`, func(url string) error {
		return godog.ErrPending
	})
	ctx.Step(`^vault\.token = "([^"]*)"$`, func(token string) error {
		return godog.ErrPending
	})
	ctx.Step(`^no vault\.token configured$`, func() error {
		return godog.ErrPending
	})
}
