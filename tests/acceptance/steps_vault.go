package acceptance

import "github.com/cucumber/godog"

func registerVaultSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^vault is configured at "([^"]*)"$`, func(url string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the vault contains checkpoints from developers (.+)$`, func(devs string) error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll searches for "([^"]*)"$`, func(query string) error {
		return godog.ErrPending
	})
	ctx.Step(`^vault\.token = "([^"]*)" in config$`, func(token string) error {
		return godog.ErrPending
	})
	ctx.Step(`^vault\.url = "([^"]*)" in config$`, func(url string) error {
		return godog.ErrPending
	})
	ctx.Step(`^vault is configured but the server is not responding$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll needs team memory search$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the vault returns a checkpoint from developer ([a-z]+)$`, func(dev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^([a-z]+)\'s public key is not in devices\/([a-z]+)\.pub$`, func(dev, keydev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the vault returns checkpoint ([a-z0-9]+) from ([a-z]+)$`, func(cp, dev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^vault is configured and reachable$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^auto_push is enabled in config$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll-vault is running with a checkpoint store$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll-vault is running$`, func() error {
		return godog.ErrPending
	})
}
