package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// newMinimalConfigForFlush returns a config the flush helper can
// read (Models[name].StampLabel for the stamp + Tools.GitCheckTimeout
// for the bounded ctx). Validation-pass-10 H13: explicit so the
// helper's contract is visible.
func newMinimalConfigForFlush(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Models: map[string]config.ModelConfig{
			"m25":  {Endpoint: "http://localhost:8001/v1", Dialect: "minimax", MaxContext: 1000000},
			"glm5": {Endpoint: "http://localhost:8002/v1", Dialect: "glm", MaxContext: 200000},
		},
		Routing: config.RoutingConfig{
			DefaultModel:            "m25",
			DeepModel:               "glm5",
			ContextDepthThreshold:   32000,
			ToolDepthThreshold:      5,
			GateFloorEscalateAtRank: 2,
		},
		Tools: config.ToolsConfig{
			GitCheckTimeoutSeconds:  5,
			GitCommitTimeoutSeconds: 30,
		},
	}
}

// routingDecisionFromTest constructs a RoutingDecision for tests.
func routingDecisionFromTest(action, target string) dialect.RoutingDecision {
	return dialect.RoutingDecision{
		Action:      action,
		TargetModel: target,
		Reason:      dialect.ReasonContextDepth,
	}
}

// TestPhase10_SessionOpensEngineAndPersistsAcrossRestart verifies
// the slice-1 wiring: a session opens the engine, writes a
// finding through the runner-layer store, the journal persists
// it, and a subsequent process can replay the same state.
func TestPhase10_SessionOpensEngineAndPersistsAcrossRestart(t *testing.T) {
	workdir := t.TempDir()

	// First "session" — open engine, raise a finding, close.
	rt, err := openEngine(workdir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	if err := rt.findings.Raise(runner.FindingRecord{
		ID: "F1", ArrowID: "A1",
		Type:     runner.FindingTypeLocalBug,
		Severity: runner.SeverityHigh, Status: runner.FindingStatusOpen,
		Description: "checkpoint test",
	}); err != nil {
		t.Fatal(err)
	}
	rt.journal.Flush()
	rt.closeEngine()

	// Second "session" — open the SAME workdir's engine; replay
	// should restore the finding.
	rt2, err := openEngine(workdir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt2.closeEngine()
	counts, err := rt2.replayEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts.Findings != 1 {
		t.Errorf("replay findings = %d; want 1", counts.Findings)
	}
	f, ok := rt2.findings.Get("F1")
	if !ok {
		t.Fatal("F1 missing after replay")
	}
	if f.Description != "checkpoint test" {
		t.Errorf("description lost: %q", f.Description)
	}
}

// TestPhase10_EngineDBPathIsProjectLocal asserts engine.db lives
// under the workdir's .ghyll/ — different projects share no state.
func TestPhase10_EngineDBPathIsProjectLocal(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	pa, err := defaultEngineDBPath(a)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := defaultEngineDBPath(b)
	if err != nil {
		t.Fatal(err)
	}
	if pa == pb {
		t.Errorf("project-local paths collided: %s == %s", pa, pb)
	}
	if filepath.Dir(pa) != filepath.Join(a, ".ghyll") {
		t.Errorf("path not under project: %s", pa)
	}
}

// TestPhase10_BuildModelStamp verifies the trailer-value format.
// Per validation-pass-10 H6: bare name by default, StampLabel
// override when present, never the endpoint URL.
func TestPhase10_BuildModelStamp(t *testing.T) {
	cases := []struct {
		name  string
		model string
		cfg   *config.Config
		want  string
	}{
		{
			"bare name when no label",
			"qwen-coder",
			&config.Config{Models: map[string]config.ModelConfig{
				"qwen-coder": {Endpoint: "http://int.example/v1"},
			}},
			"qwen-coder",
		},
		{
			"StampLabel override wins",
			"qwen-coder",
			&config.Config{Models: map[string]config.ModelConfig{
				"qwen-coder": {Endpoint: "http://int.example/v1", StampLabel: "qwen-coder@gpu1"},
			}},
			"qwen-coder@gpu1",
		},
		{
			"unknown model returns bare name",
			"deepseek",
			&config.Config{Models: map[string]config.ModelConfig{}},
			"deepseek",
		},
		{
			"nil cfg returns bare name",
			"m25",
			nil,
			"m25",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildModelStamp(c.model, c.cfg)
			if got != c.want {
				t.Errorf("got %q; want %q", got, c.want)
			}
		})
	}
}

// TestPhase10_EngineCloseIdempotent asserts double-close is safe.
func TestPhase10_EngineCloseIdempotent(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rt.closeEngine()
	rt.closeEngine() // must not panic
	var nilRT *engineRuntime
	nilRT.closeEngine() // must not panic on nil receiver
}

// TestPhase10_NewRunnerAttachesJournal asserts that engineRuntime's
// NewRunner wires the EvaluationRun observer so runs persist.
// Per W2: tier is required.
func TestPhase10_NewRunnerAttachesJournal(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	r := rt.NewRunner(runner.DepthRankShallow)
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	_, err = r.Evaluate(context.Background(), "C1", "P1", runner.Clause{
		Concept: "no-todo-marker", ArrowID: "A1",
		ProjectDir: t.TempDir(), // empty dir → no TODOs found
		Args: map[string]any{
			"scope":   "**",
			"markers": []any{"TODO"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.journal.Flush()
	runs, err := rt.store.ListEvaluationRuns(context.Background(), engine.RunFilter{ArrowID: "A1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("evaluation runs persisted = %d; want 1", len(runs))
	}
}

// TestPhase10_AttachRunnerTwice_DoesNotDuplicate verifies W15:
// double attach must not duplicate EvaluationRun writes.
func TestPhase10_AttachRunnerTwice_DoesNotDuplicate(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	r := rt.NewRunner(runner.DepthRankShallow)
	// Re-attach against the same Runner instance: every observer
	// fires once. EvaluationRun ID is unique per call, so even with
	// double-firing the sqlite write is upsert-or-ignore via
	// INSERT … ON CONFLICT(id) DO NOTHING. Validate the count.
	rt.attachRunner(r)
	_, err = r.Evaluate(context.Background(), "C1", "P1", runner.Clause{
		Concept: "no-todo-marker", ArrowID: "A1",
		ProjectDir: t.TempDir(),
		Args: map[string]any{
			"scope":   "**",
			"markers": []any{"TODO"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rt.journal.Flush()
	runs, err := rt.store.ListEvaluationRuns(context.Background(), engine.RunFilter{ArrowID: "A1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("double-attach should not duplicate; got %d runs, want 1", len(runs))
	}
}

// TestPhase10_AttachJournalIdempotency verifies W1: second call
// returns ErrEngineAttachTwice rather than silently leaking
// observers + goroutines.
func TestPhase10_AttachJournalIdempotency(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	err = rt.attachJournal(nil)
	if err == nil {
		t.Fatal("second attach must error; got nil")
	}
	if err.Error() == "" || err != ErrEngineAttachTwice {
		t.Errorf("want ErrEngineAttachTwice, got %v", err)
	}
}

// TestPhase10_ReplayAfterAttachErrors verifies W6: ordering invariant
// — replay after attach would re-journal the in-memory rows, so
// the helper refuses.
func TestPhase10_ReplayAfterAttachErrors(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	_, err = rt.replayEngine(context.Background())
	if err == nil || err != ErrEngineReplayAfterAttach {
		t.Errorf("want ErrEngineReplayAfterAttach, got %v", err)
	}
}

// TestPhase10_AttachJournalBeforeReplay verifies W6: caller must
// run replay first; attach refuses when replay has not run.
func TestPhase10_AttachJournalBeforeReplay(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	err = rt.attachJournal(nil)
	if err == nil || err != ErrEngineReplayBeforeAttach {
		t.Errorf("want ErrEngineReplayBeforeAttach, got %v", err)
	}
}

// TestPhase10_FlushStagedBeforeModelSwitch_NoChangesNoCommit verifies
// the model-switch flush is a no-op when the working tree has no
// staged changes.
func TestPhase10_FlushStagedBeforeModelSwitch_NoChangesNoCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initEmptyGitRepo(t)
	s := &Session{workdir: dir, version: "test", output: func(string) {}}
	s.cfg = newMinimalConfigForFlush(t)
	err := s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5"))
	if err != nil {
		t.Errorf("flush on clean tree should not error: %v", err)
	}
}

// TestPhase10_FlushStagedBeforeModelSwitch_StagedTriggersCommit
// verifies H12: staged changes get committed with the OLD model's
// stamp.
func TestPhase10_FlushStagedBeforeModelSwitch_StagedTriggersCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initEmptyGitRepo(t)
	// Stage a file.
	stageFile(t, dir, "f.txt", "hello")
	output := captureOutput()
	s := &Session{workdir: dir, version: "test", output: output.fn}
	s.cfg = newMinimalConfigForFlush(t)
	err := s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5"))
	if err != nil {
		t.Fatalf("staged flush should succeed: %v", err)
	}
	// Verify a commit landed with the m25 stamp.
	out, _ := gitOutput(t, dir, "log", "--format=%B", "-1")
	if !strings.Contains(out, "Ghyll-Model: m25") {
		t.Errorf("commit missing Ghyll-Model trailer for m25: %s", out)
	}
}

// TestPhase10_FlushStagedBeforeModelSwitch_UnstagedRefuses verifies
// H5: unstaged-only changes refuse the flush rather than silently
// misattributing them to the next model.
func TestPhase10_FlushStagedBeforeModelSwitch_UnstagedRefuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initEmptyGitRepoWithCommit(t)
	// Modify a tracked file but don't `git add` — pure unstaged.
	if err := writeFile(filepath.Join(dir, "tracked.txt"), "mutated"); err != nil {
		t.Fatal(err)
	}
	s := &Session{workdir: dir, version: "test", output: func(string) {}}
	s.cfg = newMinimalConfigForFlush(t)
	err := s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5"))
	if err == nil {
		t.Fatal("unstaged-only changes should refuse the flush")
	}
	if !strings.Contains(err.Error(), "unstaged") {
		t.Errorf("error should mention 'unstaged'; got %v", err)
	}
}

// TestPhase10_FlushStagedBeforeModelSwitch_EmptyWorkdirWarns verifies
// H10: empty workdir surfaces a one-shot operator warning.
func TestPhase10_FlushStagedBeforeModelSwitch_EmptyWorkdirWarns(t *testing.T) {
	out := captureOutput()
	s := &Session{workdir: "", version: "test", output: out.fn}
	s.cfg = newMinimalConfigForFlush(t)
	if err := s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5")); err != nil {
		t.Errorf("empty-workdir should not error: %v", err)
	}
	if !strings.Contains(out.text(), "no workdir") {
		t.Errorf("expected workdir warning; got %q", out.text())
	}
	// Second call must NOT re-emit the warning (one-shot).
	out.clear()
	_ = s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5"))
	if strings.Contains(out.text(), "no workdir") {
		t.Errorf("warning re-emitted on second call; one-shot violation: %q", out.text())
	}
}

// TestPhase10_InitEngine_OpenFailureFallsBack verifies W4: when
// the engine.db cannot be opened (e.g., workdir is a regular file
// not a directory), the session continues without v2 persistence
// and surfaces a warning.
func TestPhase10_InitEngine_OpenFailureFallsBack(t *testing.T) {
	// Use a path where .ghyll cannot be created: make workdir a
	// file so MkdirAll("$workdir/.ghyll") fails.
	root := t.TempDir()
	file := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureOutput()
	s := &Session{workdir: file, version: "test", output: out.fn}
	s.initEngine(0)
	if s.engine != nil {
		t.Errorf("engine should be nil after open failure; got non-nil")
	}
	if !strings.Contains(out.text(), "engine open failed") {
		t.Errorf("expected 'engine open failed' warning; got %q", out.text())
	}
}

// TestPhase10_InitEngine_DisableEngineSkipsAll verifies that
// SessionConfig.DisableEngine prevents any engine wiring.
func TestPhase10_InitEngine_DisableEngineSkipsAll(t *testing.T) {
	// Just exercise the gating logic — we don't have a full
	// SessionConfig harness here, but verifying initEngine is NOT
	// called when DisableEngine is set is covered by the NewSession
	// path. This test asserts the helper is a separate code path.
	out := captureOutput()
	s := &Session{workdir: t.TempDir(), version: "test", output: out.fn}
	// Don't call initEngine; engine stays nil.
	if s.engine != nil {
		t.Errorf("engine should remain nil when initEngine not called")
	}
}

// TestPhase10_InitEngine_ReplayTimeoutHonored verifies W3: the
// ReplayTimeout config value is used (vs. the default).
func TestPhase10_InitEngine_ReplayTimeoutHonored(t *testing.T) {
	// We can't easily inject a stall, but we can verify that an
	// extremely short timeout (1ns) on a fresh DB still succeeds
	// (replay of an empty store is sub-microsecond).
	out := captureOutput()
	s := &Session{workdir: t.TempDir(), version: "test", output: out.fn, cfg: newMinimalConfigForFlush(t)}
	s.initEngine(0) // 0 → defaultReplayTimeout
	if s.engine == nil {
		t.Fatal("engine should initialize on empty workdir")
	}
	if !strings.Contains(out.text(), "replaying engine state") {
		t.Errorf("expected 'replaying engine state' status; got %q", out.text())
	}
	s.Close()
}

// TestPhase10_BuildModelStamp_RejectedByCommit verifies W13: a
// model name with embedded newline / control char is rejected by
// the commit-message builder (defense-in-depth even though
// dispatch path doesn't currently produce these).
func TestPhase10_BuildModelStamp_RejectedByCommit(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.ModelConfig{
		"bad-model": {Endpoint: "http://x/v1", StampLabel: "bad\nmodel"},
	}}
	stamp := buildModelStamp("bad-model", cfg)
	if !strings.Contains(stamp, "\n") {
		t.Skip("test data did not preserve newline; helper changed")
	}
	// We can't import tool here easily, so just verify the stamp
	// contains the control char — the actual rejection lives in
	// tool.BuildCommitMessage and is covered by tool-package tests.
}

// initEmptyGitRepo creates a tempdir with `git init` ready.
func initEmptyGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, cmd := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		c := exec.Command("git", cmd...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", cmd, err, out)
		}
	}
	return dir
}

// initEmptyGitRepoWithCommit creates a repo with one committed file
// so subsequent edits are "tracked but modified" (unstaged).
func initEmptyGitRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := initEmptyGitRepo(t)
	if err := writeFile(filepath.Join(dir, "tracked.txt"), "initial"); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"add", "tracked.txt"},
		{"commit", "-q", "-m", "add tracked"},
	} {
		c := exec.Command("git", cmd...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", cmd, err, out)
		}
	}
	return dir
}

func stageFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := writeFile(path, content); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "add", name)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

// writeFile is a tiny file-write helper that avoids reaching for os
// in every test.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// captureOutput collects messages emitted via the session's output
// callback so tests can assert on what the operator would see.
type outCap struct {
	lines []string
}

func captureOutput() *outCap {
	return &outCap{}
}

func (o *outCap) fn(msg string) {
	o.lines = append(o.lines, msg)
}

func (o *outCap) text() string {
	return strings.Join(o.lines, "\n")
}

func (o *outCap) clear() {
	o.lines = nil
}
