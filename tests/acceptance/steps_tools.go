package acceptance

import "github.com/cucumber/godog"

func registerToolSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^the model requests tool call ([a-z_]+) with command "([^"]*)"$`, func(tool, cmd string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model requests tool call ([a-z_]+) with path "([^"]*)"$`, func(tool, path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model requests tool call ([a-z_]+) with path "([^"]*)" and content$`, func(tool, path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model requests tool call ([a-z_]+) with args "([^"]*)"$`, func(tool, args string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model requests tool call ([a-z_]+) with pattern "([^"]*)" and path "([^"]*)"$`, func(tool, pattern, path string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the tool executes$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the bash timeout is (\d+) seconds$`, func(timeout int) error {
		return godog.ErrPending
	})
	ctx.Step(`^(\d+) seconds elapse$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the process is killed$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ripgrep is available in PATH$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ripgrep is not available in PATH$`, func() error {
		return godog.ErrPending
	})
}
