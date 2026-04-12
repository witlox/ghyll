package acceptance

import "github.com/cucumber/godog"

func registerKeySteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^no key pair exists at (.+)$`, func(path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll starts a session for the first time$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^an ed25519 key pair is generated$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a key pair exists locally$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a key pair exists at (.+)$`, func(path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the device\'s public key is not yet on the memory branch$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^developer ([a-z]+) has pushed her public key to ghyll\/memory$`, func(dev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^developer ([a-z]+) runs ghyll on the same repo$`, func(dev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a private key exists at (.+) with mode (\d+)$`, func(path string, mode int) error {
		return godog.ErrPending
	})
	ctx.Step(`^a checkpoint from device "([^"]*)"$`, func(device string) error {
		return godog.ErrPending
	})
	ctx.Step(`^devices\/([a-z-]+)\.pub exists on the memory branch$`, func(device string) error {
		return godog.ErrPending
	})
	ctx.Step(`^no public key exists for "([^"]*)"$`, func(device string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a machine with hostname "([^"]*)"$`, func(hostname string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the device ID is computed$`, func() error {
		return godog.ErrPending
	})
}
