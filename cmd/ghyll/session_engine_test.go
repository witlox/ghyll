package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/dialect"
	"github.com/witlox/ghyll/engine"
	"github.com/witlox/ghyll/runner"
)

// newMinimalConfigForFlush returns a config the flush helper can
// read (just needs Models[name].Endpoint for the stamp).
func newMinimalConfigForFlush(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Models: map[string]config.ModelConfig{
			"m25":  {Endpoint: "http://localhost:8001/v1", Dialect: "minimax", MaxContext: 1000000},
			"glm5": {Endpoint: "http://localhost:8002/v1", Dialect: "glm", MaxContext: 200000},
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
	rt.attachJournal(nil)
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
func TestPhase10_BuildModelStamp(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		endpoint string
		want     string
	}{
		{"with endpoint", "qwen-coder", "http://localhost:11434/v1", "qwen-coder@http://localhost:11434/v1"},
		{"no endpoint", "deepseek", "", "deepseek"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildModelStamp(c.model, c.endpoint)
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
func TestPhase10_NewRunnerAttachesJournal(t *testing.T) {
	rt, err := openEngine(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.closeEngine()
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.attachJournal(nil)
	r := rt.NewRunner()
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

// TestPhase10_FlushStagedBeforeModelSwitch_NoChangesNoCommit verifies
// the model-switch flush is a no-op when the working tree has no
// staged changes.
func TestPhase10_FlushStagedBeforeModelSwitch_NoChangesNoCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
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
	// Build a minimal Session with just the fields the flush helper
	// touches.
	s := &Session{workdir: dir, version: "test", output: func(string) {}}
	// Stub a config that the flush helper reads (only ModelName +
	// Endpoint for the stamp).
	s.cfg = newMinimalConfigForFlush(t)
	err := s.flushStagedBeforeModelSwitch("m25", routingDecisionFromTest("escalate", "glm5"))
	if err != nil {
		t.Errorf("flush on clean tree should not error: %v", err)
	}
}
