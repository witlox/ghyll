package acceptance

import "github.com/cucumber/godog"

func registerDriftSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^a session working on "([^"]*)"$`, func(task string) error {
		return godog.ErrPending
	})
	ctx.Step(`^(\d+) turns have completed, all related to (.+)$`, func(n int, topic string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the most recent checkpoint is checkpoint (\d+)$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^drift is measured at turn (\d+)$`, func(turn int) error {
		return godog.ErrPending
	})
	ctx.Step(`^cosine similarity is computed against checkpoint (\d+)\'s embedding$`, func(n int) error {
		return godog.ErrPending
	})
	ctx.Step(`^similarity is ([0-9.]+)$`, func(sim float64) error {
		state.Similarity = sim
		return nil
	})
	ctx.Step(`^no backfill is triggered$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a session that started on "([^"]*)"$`, func(task string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the conversation has drifted to discussing (.+)$`, func(topic string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the conversation has since drifted to discussing (.+)$`, func(topic string) error {
		return godog.ErrPending
	})
	ctx.Step(`^this is below the threshold of ([0-9.]+)$`, func(t float64) error {
		return godog.ErrPending
	})
	ctx.Step(`^the ONNX embedding model has not been downloaded$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^drift measurement is attempted$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^drift_check_interval is set to (\d+) turns$`, func(n int) error {
		return godog.ErrPending
	})
}
