package acceptance

import "github.com/cucumber/godog"

func registerRoutingSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^ghyll is configured with endpoints for "([^"]*)" and "([^"]*)"$`, func(m1, m2 string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the routing thresholds are context_depth=(\d+) and tool_depth=(\d+)$`, func(cd, td int) error {
		return godog.ErrPending
	})
	ctx.Step(`^a new session starts$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the active model is "([^"]*)"$`, func(model string) error {
		state.ActiveModel = model
		return nil
	})
	ctx.Step(`^the terminal prompt shows "\[([^\]]*)\]"$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the context window contains (\d+) tokens$`, func(tokens int) error {
		state.ContextTokens = tokens
		return nil
	})
	ctx.Step(`^the next turn begins$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a checkpoint is created before the switch$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the current chain has (\d+) sequential tool calls without user input$`, func(depth int) error {
		state.ToolDepth = depth
		return nil
	})
	ctx.Step(`^the user types "\/deep"$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^auto-routing continues to evaluate$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll starts with --model ([a-z0-9]+)$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll was started with --model ([a-z0-9]+)$`, func(model string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the dialect router does not change models$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^tier fallback does not apply$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^context was auto-escalated due to depth$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^compaction reduces context to (\d+) tokens$`, func(tokens int) error {
		return godog.ErrPending
	})
	ctx.Step(`^drift detection triggers backfill$`, func() error {
		return godog.ErrPending
	})
}
