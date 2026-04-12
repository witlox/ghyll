package acceptance

import "github.com/cucumber/godog"

func registerSyncSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^a project repo at (.+) with remote "([^"]*)"$`, func(path, remote string) error {
		return godog.ErrPending
	})
	ctx.Step(`^the ghyll\/memory branch does not exist$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll starts a session$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^an orphan branch "([^"]*)" is created locally$`, func(branch string) error {
		return godog.ErrPending
	})
	ctx.Step(`^auto_sync is enabled$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^a checkpoint is created$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the checkpoint JSON is written to ghyll\/memory branch worktree$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll\/memory branch exists on origin with remote checkpoints$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll starts a new session$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^developer ([a-z]+) and ([a-z]+) both push to ghyll\/memory simultaneously$`, func(a, b string) error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll\/memory branch exists$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^the git remote is unreachable$`, func() error {
		return godog.ErrPending
	})
	ctx.Step(`^ghyll\/memory has (\d+) checkpoint files over (\d+) months$`, func(files, months int) error {
		return godog.ErrPending
	})
	ctx.Step(`^developer ([a-z]+) has checkpoints \[([^\]]*)\] on remote$`, func(dev, list string) error {
		return godog.ErrPending
	})
	ctx.Step(`^local sqlite already has \[([^\]]*)\]$`, func(list string) error {
		return godog.ErrPending
	})
}
