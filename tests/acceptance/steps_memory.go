package acceptance

import "github.com/cucumber/godog"

func registerMemorySteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^a session with (\d+) completed turns$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the checkpoint interval is reached$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a checkpoint is created with:$`, func(table *godog.Table) error {
		return godog.ErrPending
	})
	ctx.Step(`^the checkpoint is appended to sqlite store$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^checkpoints \[([^\]]*)\] exist in the store$`, func(list string) error {
		return godog.ErrPending
	})
	ctx.Step(`^checkpoints \[([^\]]*)\] from a remote sync$`, func(list string) error {
		return godog.ErrPending
	})
	ctx.Step(`^([a-z0-9]+)\.summary has been modified after creation$`, func(cp string) error {
		return godog.ErrPending
	})
	ctx.Step(`^hash chain verification runs$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^verification fails at ([a-z0-9]+)$`, func(cp string) error {
		return godog.ErrPending
	})
	ctx.Step(`^a checkpoint from developer "([^"]*)"$`, func(dev string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the first checkpoint of a session is created$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the dialect router decides to switch from "([^"]*)" to "([^"]*)"$`, func(from, to string) error {
		return godog.ErrPending
	})
	ctx.Step(`^turn (\d+) contains the text "([^"]*)"$`, func(turn int, text string) error {
		return godog.ErrPending
	})
}
