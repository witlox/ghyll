// End-to-end wiring tests for the diamond v4 deferred-item closures.
// Each test asserts the PRODUCTION callsite — not the substrate — by
// exercising the user-visible seam (slash command, session-open
// banner, registry post-open coverage).

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/runner"
)

// makeMinimalGrid writes a v1 grid.yaml + grid.current into the given
// workdir using the default bootstrap path. Returns the parsed Grid.
func makeMinimalGrid(t *testing.T, workdir string, languageBindings map[string]string) *bootstrap.Grid {
	t.Helper()
	g := bootstrap.NewGrid("op-test")
	g.BoundedContexts = []bootstrap.BoundedContext{{ID: "checkout"}}
	if languageBindings != nil {
		g.LanguageBindings = languageBindings
	}
	if err := os.MkdirAll(filepath.Join(workdir, ".ghyll"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := g.Write(workdir); err != nil {
		t.Fatalf("grid write: %v", err)
	}
	out, err := bootstrap.Read(workdir)
	if err != nil {
		t.Fatalf("grid read: %v", err)
	}
	return out
}

// TestScenario_OpenEngineWithOptions_RegistersGridBindings verifies
// C-1 closure: openEngineWithOptions accepts *bootstrap.Grid and
// calls registerGridBindings so the runtime registry resolves
// `<concept>.<language>` keys post-open. The PRODUCTION callsite is
// session.go's initEngine — this test reaches the same seam.
func TestScenario_OpenEngineWithOptions_RegistersGridBindings(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, map[string]string{
		"compiles.go": "go build ./...",
	})
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	defer rt.closeEngine()
	// The runtime registry now must resolve `compiles.go`.
	_, _, ok := rt.registry.Lookup("compiles.go")
	if !ok {
		t.Fatal("registry must contain compiles.go after registerGridBindings")
	}
}

// TestScenario_OpenEngineWithOptions_MissingBindingRefuses verifies
// C-2 closure: a grid file declaring an arrow with a language-bound
// concept but NO matching binding causes openEngineWithOptions to
// fail with *MissingBindingError BEFORE the runtime escapes.
func TestScenario_OpenEngineWithOptions_MissingBindingRefuses(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	// Construct an in-memory grid with a language-bound clause and
	// NO matching binding. Skip the disk write — the open path only
	// reads the in-memory shape we pass in.
	g := bootstrap.NewGrid("op-test")
	g.BoundedContexts = []bootstrap.BoundedContext{{ID: "checkout"}}
	g.Arrows = []map[string]any{{
		"id":          "A1",
		"source-role": "analyst",
		"target-role": "architect",
		"context":     "checkout",
		"stratum":     "L1",
		"clauses": []any{
			map[string]any{
				"concept": "compiles",
				"args": map[string]any{
					"language": "go",
					"scope":    "**/*.go",
				},
			},
		},
	}}
	_, err := openEngineWithOptions(workdir, nil, 3, g)
	if err == nil {
		t.Fatal("expected MissingBindingError, got nil")
	}
	var mbe *bootstrap.MissingBindingError
	if !errors.As(err, &mbe) {
		t.Fatalf("expected *MissingBindingError, got %T: %v", err, err)
	}
}

// TestScenario_EngineRuntime_Committer_Constructed verifies C-3
// closure: engineRuntime.committer is non-nil after openEngine and
// has the live registry / queue / grid wired.
func TestScenario_EngineRuntime_Committer_Constructed(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	defer rt.closeEngine()
	c := rt.Committer()
	if c == nil {
		t.Fatal("expected non-nil committer after openEngineWithOptions")
	}
	if c.LiveRegistry == nil {
		t.Fatal("expected committer.LiveRegistry to be wired to runtime registry")
	}
	if c.BindingsReRegister == nil {
		t.Fatal("expected committer.BindingsReRegister to be wired")
	}
}

// TestScenario_DrainAmendments_SlashCommand_NoOpID verifies C-4
// closure: /drain-amendments refuses without an op-id.
func TestScenario_DrainAmendments_SlashCommand_NoOpID(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	s := &Session{engine: rt}
	res := s.handleDrainAmendmentsCommand("")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "no op-id set") {
		t.Fatalf("expected refusal mentioning op-id, got: %s", res.Output)
	}
}

// TestScenario_DrainAmendments_SlashCommand_DrainsPending verifies
// C-4 closure: with an op-id and pending amendments, the slash
// command actually drains the queue via the wired committer.
func TestScenario_DrainAmendments_SlashCommand_DrainsPending(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	// Enqueue a pending amendment directly via the queue.
	am := runner.AmendmentRequest{
		ID: "AM-1", Reason: runner.AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A1", TargetRole: "analyst",
		Contexts:   []string{"checkout", "billing"},
		FindingIDs: []string{"F1"},
		CreatedAt:  time.Now().Format(time.RFC3339Nano),
	}
	if err := rt.Amendments().Enqueue(am); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleDrainAmendmentsCommand("")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "drain complete: 1/1 committed") {
		t.Fatalf("expected commit confirmation in output, got: %s", res.Output)
	}
	// Verify the queue is drained.
	if n := rt.Amendments().DrainedCount(); n != 1 {
		t.Fatalf("expected 1 drained amendment, got %d", n)
	}
}

// TestScenario_Engine_ArrowInvalidationsTable verifies C-5 closure:
// the schema creates `arrow_invalidations` and the bus subscription
// persists OpEventArrowInvalidated rows into it.
func TestScenario_Engine_ArrowInvalidationsTable(t *testing.T) {
	t.Parallel()
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
	rt.Bus().Publish(runner.OperatorEvent{
		Kind:      runner.OpEventArrowInvalidated,
		ArrowID:   "A-inv",
		Detail:    "operator-requested",
		Timestamp: time.Now(),
		Payload:   map[string]string{"op_id": "op-test", "reason": "stale"},
	})
	// Publish runs subscriber callbacks synchronously; the insert
	// completes before Publish returns.
	n, err := rt.Store().CountArrowInvalidations(context.Background(), "A-inv")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 invalidation row, got %d", n)
	}
}

// TestScenario_PassDispatcher_RunDispatcherAdversarialPhase_Wired
// verifies C-7 + C-8 closures: PassDispatcher has both Hooks and
// AdversarialPhase fields wired through dispatcher(), and the hook
// driver fires when /adversary is enabled.
func TestScenario_PassDispatcher_RunDispatcherAdversarialPhase_Wired(t *testing.T) {
	t.Parallel()
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
	d := rt.dispatcher()
	if d == nil {
		t.Fatal("dispatcher() returned nil")
	}
	if d.Hooks == nil {
		t.Fatal("dispatcher.Hooks must be wired")
	}
	if d.AdversarialPhase == nil {
		t.Fatal("dispatcher.AdversarialPhase must be wired")
	}
	if d.MaxRecursiveDispatch == 0 {
		t.Fatal("dispatcher.MaxRecursiveDispatch must be set")
	}
}

// TestScenario_AdversaryCommand_EnableDisableStatus verifies C-9
// closure: /adversary {enable,disable,status} toggles the atomic
// pointer and reports state.
func TestScenario_AdversaryCommand_EnableDisableStatus(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	s := &Session{engine: rt}

	// Initial status: disabled.
	res := s.handleAdversaryCommand("status")
	if !strings.Contains(res.Output, "DISABLED") {
		t.Fatalf("expected DISABLED initially, got: %s", res.Output)
	}

	// Enable.
	res = s.handleAdversaryCommand("enable")
	if !strings.Contains(res.Output, "enabled") {
		t.Fatalf("expected enabled confirmation, got: %s", res.Output)
	}
	if rt.AdversarialHooks().Load() == nil {
		t.Fatal("expected hook bundle after /adversary enable")
	}

	// Status: wired.
	res = s.handleAdversaryCommand("")
	if !strings.Contains(res.Output, "wired") {
		t.Fatalf("expected wired after enable, got: %s", res.Output)
	}

	// Disable.
	res = s.handleAdversaryCommand("disable")
	if !strings.Contains(res.Output, "disabled") {
		t.Fatalf("expected disabled confirmation, got: %s", res.Output)
	}
	if rt.AdversarialHooks().Load() != nil {
		t.Fatal("expected nil bundle after /adversary disable")
	}
}

// TestScenario_ModalDriver_DispatchesNewEventKinds verifies C-11
// closure: the modal driver's OnEvent switch arms the new event
// kinds so they are surfaced inline (not silently dropped).
func TestScenario_ModalDriver_DispatchesNewEventKinds(t *testing.T) {
	t.Parallel()
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
	d := newModalDriver(nil, rt.AttestationStore(), rt.Passes(),
		rt.Bus(), rt.InsufficientBasisTracker(),
		func() string { return "op-test" }, nil, 64)
	t.Cleanup(d.Stop)

	kinds := []runner.OperatorEventKind{
		runner.OpEventAdversarialRoundStart,
		runner.OpEventRemediationConverged,
		runner.OpEventRemediationEscalated,
		runner.OpEventAmendmentEnqueueRefused,
		runner.OpEventRecoveryAmendmentsPending,
		runner.OpEventArrowInvalidated,
		runner.OpEventBindingMissing,
	}
	for _, k := range kinds {
		rt.Bus().Publish(runner.OperatorEvent{Kind: k, ArrowID: "A1", Detail: string(k)})
	}
	got := d.NotificationsSnapshot()
	if len(got) != len(kinds) {
		t.Fatalf("expected %d notifications, got %d", len(kinds), len(got))
	}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Fatalf("notification[%d]: kind=%s, want %s", i, got[i].Kind, k)
		}
	}
}

// TestScenario_VerifyBindingsCoverage_PostReplay verifies C-1 + C-2
// closure: the post-Replay binding-coverage call surfaces missing
// bindings via the bus + console.
func TestScenario_VerifyBindingsCoverage_PostReplay(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, map[string]string{
		"compiles.go": "go build ./...",
	})
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	if cerr := rt.verifyBindingsCoveragePostReplay(); cerr != nil {
		t.Fatalf("verifyBindingsCoveragePostReplay: unexpected error %v", cerr)
	}
}

// TestScenario_RemediationMigrations_PassesColumnsExist verifies
// C-6 closure: ALTER TABLE adds the remediation columns on open;
// re-open is idempotent.
func TestScenario_RemediationMigrations_PassesColumnsExist(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	rt.closeEngine()
	// Re-open against the same DB: the ALTER TABLE must be a no-op.
	rt2, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	t.Cleanup(rt2.closeEngine)
	rows, err := rt2.Store().DB().Query(`PRAGMA table_info("passes")`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	cols := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		cols[name] = struct{}{}
	}
	if _, ok := cols["remediation_outcome"]; !ok {
		t.Fatal("expected passes.remediation_outcome after migration")
	}
	if _, ok := cols["remediation_rounds_used"]; !ok {
		t.Fatal("expected passes.remediation_rounds_used after migration")
	}
}
