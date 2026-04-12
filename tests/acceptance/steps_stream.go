package acceptance

import "github.com/cucumber/godog"

func registerStreamSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^ghyll is configured with endpoint "([^"]*)"$`, func(endpoint string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the active dialect is "([^"]*)"$`, func(dialect string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the context window contains a user prompt$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the stream client sends a request$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^it receives SSE events with delta content$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the model responds with a tool call for "([^"]*)" with command "([^"]*)"$`, func(tool, cmd string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the model responds with (\d+) tool calls in sequence$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the stream is delivering a response$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^(\d+) tokens have been received$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the connection drops mid-stream$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the endpoint returns HTTP (\d+)$`, func(code int) error {
		return godog.ErrPending
	})
	ctx.Step(`^the endpoint returns HTTP (\d+) with Retry-After: (\d+)$`, func(code, after int) error {
		return godog.ErrPending
	})
	ctx.Step(`^auto-routing is active \(no --model flag\)$`, func() error {
		state.AutoRouting = true
		return nil
	})
	ctx.Step(`^the ([a-z0-9]+) endpoint has failed (\d+) consecutive requests$`, func(model string, n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^both ([a-z0-9]+) and ([a-z0-9]+) endpoints have failed (\d+) consecutive requests each$`, func(m1, m2 string, n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^tier fallback triggers$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the endpoint returns an SSE frame with invalid JSON in the data field$`, func() error {
		return godog.ErrPending
	})
}
