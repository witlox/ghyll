// Tests for the cmd/ghyll-side adversarial-cycle driver wired into
// PassDispatcher.AdversarialPhase. The runner side already verifies
// PartitionClauses + ErrAdversaryHooksNotWired; these tests close
// the production seam.

package main

import (
	"context"
	"testing"

	"github.com/witlox/ghyll/runner"
)

func newAdvRuntime(t *testing.T) *engineRuntime {
	t.Helper()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatalf("replayEngine: %v", err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatalf("attachJournal: %v", err)
	}
	return rt
}

// TestScenario_RunDispatcherAdversarialPhase_UnwiredRefuses verifies
// the driver refuses when no hooks bundle is loaded.
func TestScenario_RunDispatcherAdversarialPhase_UnwiredRefuses(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	req := &runner.DispatchRequest{
		Arrow: runner.ArrowDefinition{ID: "A1"},
	}
	// I-H-3 closure: dispatcher passes its loaded hooks snapshot;
	// here we pass nil to exercise the defensive fallback that
	// re-loads from the atomic pointer (unwired -> refuses).
	_, _, err := rt.runDispatcherAdversarialPhase(context.Background(), req, "P1", nil, nil)
	if err == nil {
		t.Fatal("expected ErrAdversaryHooksNotWired with no bundle")
	}
}

// TestScenario_RunDispatcherAdversarialPhase_HooksParamPreferred
// verifies I-H-3 closure: when the dispatcher passes its already-
// loaded hooks snapshot, the phase fn drives the cycle through that
// snapshot rather than re-loading from the atomic pointer. A
// concurrent `/adversary disable` swap of the AtomicAdversarialHooks
// to nil between the dispatcher gate and this call no longer races
// — the cycle runs against the snapshot.
func TestScenario_RunDispatcherAdversarialPhase_HooksParamPreferred(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	bundle := &runner.AdversarialHooks{
		Factory: func(int) *runner.Adversary {
			return runner.NewAdversary(nil, nil, nil)
		},
		OpenSweep: func(context.Context, runner.AdversaryAttack) ([]runner.FindingRecord, error) {
			return nil, nil
		},
		Classify: func(context.Context, runner.AdversaryAttack) ([]runner.Classification, error) {
			return nil, nil
		},
		ProducerFix: func(context.Context, []runner.FindingRecord, int) ([]byte, error) {
			return []byte("ok"), nil
		},
		RemediationConfigDefaults: runner.RemediationConfig{RoundsMax: 1},
	}
	// Simulate the I-H-3 race: the atomic pointer is nil (operator
	// just typed `/adversary disable`) BUT the dispatcher captured
	// `bundle` BEFORE the swap. The phase fn must drive through
	// `bundle`, not the now-nil pointer.
	if rt.AdversarialHooks().Load() != nil {
		t.Fatal("setup: expected AtomicAdversarialHooks to be nil")
	}
	req := &runner.DispatchRequest{
		Arrow: runner.ArrowDefinition{
			ID:      "A1",
			Clauses: []runner.Clause{{Concept: "no-todo-marker", DepthType: runner.DepthTypeSensitive}},
		},
		ActualTier: runner.DepthRankShallow,
		ProjectDir: t.TempDir(),
	}
	sensitive, _ := runner.PartitionClauses(req.Arrow.Clauses)
	report, _, err := rt.runDispatcherAdversarialPhase(
		context.Background(), req, "P1", sensitive, bundle,
	)
	if err != nil {
		t.Fatalf("I-H-3: phase fn should drive through param bundle; got err=%v", err)
	}
	if report == nil || report.Outcome != runner.RemediationConverged {
		t.Fatalf("I-H-3: expected converged via param bundle; got %v", report)
	}
}

// TestScenario_RunDispatcherAdversarialPhase_StubBundleConverges
// verifies the driver runs the cycle end-to-end with a stub bundle
// that produces zero findings (immediate convergence).
func TestScenario_RunDispatcherAdversarialPhase_StubBundleConverges(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	bundle := &runner.AdversarialHooks{
		Factory: func(int) *runner.Adversary {
			return runner.NewAdversary(nil, nil, nil)
		},
		OpenSweep: func(context.Context, runner.AdversaryAttack) ([]runner.FindingRecord, error) {
			return nil, nil
		},
		Classify: func(context.Context, runner.AdversaryAttack) ([]runner.Classification, error) {
			return nil, nil
		},
		ProducerFix: func(context.Context, []runner.FindingRecord, int) ([]byte, error) {
			return []byte("ok"), nil
		},
		RemediationConfigDefaults: runner.RemediationConfig{RoundsMax: 1},
	}
	rt.AdversarialHooks().Store(bundle)
	req := &runner.DispatchRequest{
		Arrow: runner.ArrowDefinition{
			ID:      "A1",
			Clauses: []runner.Clause{{Concept: "no-todo-marker", DepthType: runner.DepthTypeSensitive}},
		},
		ActualTier: runner.DepthRankShallow,
		ProjectDir: t.TempDir(),
	}
	sensitive, _ := runner.PartitionClauses(req.Arrow.Clauses)
	report, verifyClauses, err := rt.runDispatcherAdversarialPhase(
		context.Background(), req, "P1", sensitive, bundle,
	)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.Outcome != runner.RemediationConverged {
		t.Fatalf("expected converged outcome, got %s", report.Outcome)
	}
	// Verify clauses should contain at least the auto-inserts.
	if len(verifyClauses) == 0 {
		t.Fatal("expected verifyClauses to include auto-inserts")
	}
}

// TestScenario_AdversaryCommand_StatusReportsMalformed verifies the
// driver reports "wired-but-malformed" when Validate() fails.
func TestScenario_AdversaryCommand_StatusReportsMalformed(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	rt.AdversarialHooks().Store(&runner.AdversarialHooks{
		// Only Factory wired — Validate must return false.
		Factory: func(int) *runner.Adversary { return &runner.Adversary{} },
	})
	s := &Session{engine: rt}
	res := s.handleAdversaryCommand("status")
	if !contains(res.Output, "wired-but-malformed") {
		t.Fatalf("expected wired-but-malformed, got: %s", res.Output)
	}
}

// TestScenario_DrainAmendments_NoPending_Reports verifies the empty-
// queue path is handled cleanly.
func TestScenario_DrainAmendments_NoPending_Reports(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleDrainAmendmentsCommand("")
	if !contains(res.Output, "no pending amendments") {
		t.Fatalf("expected empty-queue notice, got: %s", res.Output)
	}
}

// TestScenario_AdversaryCommand_UnknownArgRejects verifies the usage
// hint shows for unknown sub-commands.
func TestScenario_AdversaryCommand_UnknownArgRejects(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	s := &Session{engine: rt}
	res := s.handleAdversaryCommand("explode")
	if !contains(res.Output, "usage:") {
		t.Fatalf("expected usage hint, got: %s", res.Output)
	}
}

// TestScenario_AdversaryCommand_EngineNil verifies the engine-unset
// branch returns the expected refusal.
func TestScenario_AdversaryCommand_EngineNil(t *testing.T) {
	t.Parallel()
	s := &Session{}
	res := s.handleAdversaryCommand("status")
	if !contains(res.Output, "engine not initialized") {
		t.Fatalf("expected engine-not-init message, got: %s", res.Output)
	}
}

// TestScenario_DrainAmendments_EngineNil verifies the same path on
// the drain command.
func TestScenario_DrainAmendments_EngineNil(t *testing.T) {
	t.Parallel()
	s := &Session{}
	res := s.handleDrainAmendmentsCommand("")
	if !contains(res.Output, "engine not initialized") {
		t.Fatalf("expected engine-not-init message, got: %s", res.Output)
	}
}

// TestScenario_EngineRuntime_GridFile_Accessor verifies the read
// path through gridFileMu.
func TestScenario_EngineRuntime_GridFile_Accessor(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	gf := rt.GridFile()
	if gf == nil {
		t.Fatal("expected non-nil grid file after openEngineWithOptions with a grid")
	}
}

// TestScenario_BuildRegistryOverlay_InvalidKey verifies the overlay
// builder refuses malformed binding keys.
func TestScenario_BuildRegistryOverlay_InvalidKey(t *testing.T) {
	t.Parallel()
	rt := newAdvRuntime(t)
	_, _, err := rt.buildRegistryOverlay(runner.CommitRequest{
		NewLanguageBindings: map[string]string{"": "go build"},
	})
	if err == nil {
		t.Fatal("expected error on empty key, got nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
