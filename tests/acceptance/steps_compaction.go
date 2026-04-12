package acceptance

import "github.com/cucumber/godog"

func registerCompactionSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^the active model is "([^"]*)" with max_context (\d+)$`, func(model string, max int) error {
		state.ActiveModel = model
		state.MaxContext = max
		return nil
	})
	ctx.Step(`^compaction preserves the last (\d+) turns$`, func(n int) error {
		state.TurnsPreserved = n
		return nil
	})
	ctx.Step(`^the context window contains (\d+) tokens \((\d+)% of max\)$`, func(tokens, pct int) error {
		state.ContextTokens = tokens
		return nil
	})
	ctx.Step(`^the next turn is about to begin$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^compaction triggers before sending the request$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a separate API call is made with only the turns to summarize$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the proactive check estimated (\d+) tokens \(below (\d+)% threshold\)$`, func(tokens, pct int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model rejects with context_length_exceeded$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^compaction triggers immediately$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the context has (\d+) turns$`, func(n int) error {
		state.Messages = n
		return nil
	})
	ctx.Step(`^compaction triggers$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the context window exceeds the routing escalation threshold \((\d+) tokens\)$`, func(threshold int) error {
		return godog.ErrPending
	})
	ctx.Step(`^compaction has already run once at turn (\d+)$`, func(turn int) error {
		return godog.ErrPending
	})
}
