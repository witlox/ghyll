package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// Tier-0 wiring tests verify the production integration of the v2
// components: AttestationStore + JSONL writer + InsufficientBasisTracker
// + RoleContextLockTable + PassRegistry + dispatcher. Each test
// builds a real engineRuntime end-to-end and exercises one wiring
// invariant.

func newTier0Runtime(t *testing.T) (*engineRuntime, string) {
	t.Helper()
	workdir := t.TempDir()
	rt, err := openEngineWithOptions(workdir, nil, 3)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	return rt, workdir
}

func TestScenario_Tier0_AccessorsReturnConstructedComponents(t *testing.T) {
	rt, _ := newTier0Runtime(t)
	if rt.AttestationStore() == nil {
		t.Error("AttestationStore nil after openEngineWithOptions")
	}
	if rt.RoleLocks() == nil {
		t.Error("RoleLocks nil")
	}
	if rt.Bus() == nil {
		t.Error("Bus nil")
	}
	if rt.Passes() == nil {
		t.Error("Passes nil")
	}
	if rt.InsufficientBasisTracker() == nil {
		t.Error("InsufficientBasisTracker nil")
	}
	if rt.Findings() == nil || rt.Grid() == nil || rt.Amendments() == nil ||
		rt.Classifications() == nil || rt.Store() == nil {
		t.Error("legacy v1 accessors nil")
	}
}

func TestScenario_Tier0_JSONLWriterCreated(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	expected := filepath.Join(workdir, ".ghyll", "attestations.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("JSONL audit file not created at %s: %v", expected, err)
	}
	_ = rt // keep alive
}

func TestScenario_Tier0_AttestationFlow_PersistsAndJSONLsAudit(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	// Replay then attach so the JSONL observer is wired.
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatalf("replayEngine: %v", err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatalf("attachJournal: %v", err)
	}

	rec := runner.AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           runner.AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "op-test",
		AttestedByRole: "implementer",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
	}
	if err := rt.AttestationStore().Record(rec); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rt.journal != nil {
		rt.journal.Flush()
	}

	// Engine table received the row.
	n, err := rt.Store().CountAttestations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CountAttestations = %d; want 1", n)
	}

	// JSONL file received exactly one line.
	jsonl := filepath.Join(workdir, ".ghyll", "attestations.jsonl")
	contents, err := os.ReadFile(jsonl)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(strings.TrimRight(string(contents), "\n"), "\n") + 1
	if lines != 1 {
		t.Fatalf("JSONL line count = %d; want 1", lines)
	}
	if !strings.Contains(string(contents), "att-A1-C1-v1") {
		t.Fatalf("JSONL missing record id: %s", contents)
	}
}

// TestScenario_Tier0_Replay_DoesNotDoubleAppendJSONL pins the
// adversary-fix: replayed attestations must NOT re-append to the
// JSONL file (the JSONL observer is subscribed POST-replay).
func TestScenario_Tier0_Replay_DoesNotDoubleAppendJSONL(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	jsonl := filepath.Join(workdir, ".ghyll", "attestations.jsonl")

	// Wire fully, record once, close.
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatalf("replayEngine: %v", err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatalf("attachJournal: %v", err)
	}
	rec := runner.AttestationRecord{
		ID: "att-A1-C1-v1", Kind: runner.AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "op",
		AttestedByRole: "implementer", SourceRole: "analyst", TargetRole: "architect",
		Verdict: runner.AttestationPass, Timestamp: 1, GridVersion: 1,
	}
	if err := rt.AttestationStore().Record(rec); err != nil {
		t.Fatal(err)
	}
	rt.journal.Flush()
	rt.closeEngine()

	// First session wrote one JSONL line. Verify.
	contents1, _ := os.ReadFile(jsonl)
	lines1 := strings.Count(strings.TrimRight(string(contents1), "\n"), "\n") + 1
	if lines1 != 1 {
		t.Fatalf("after first session: lines = %d; want 1", lines1)
	}

	// Second session — same workdir, same DB. Replay should
	// re-populate the in-memory store, but the JSONL observer
	// (subscribed post-replay) must NOT fire for replayed records.
	rt2, err := openEngineWithOptions(workdir, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt2.closeEngine)
	if _, err := rt2.replayEngine(context.Background()); err != nil {
		t.Fatalf("session 2 replayEngine: %v", err)
	}
	if err := rt2.attachJournal(nil); err != nil {
		t.Fatalf("session 2 attachJournal: %v", err)
	}
	rt2.journal.Flush()

	contents2, _ := os.ReadFile(jsonl)
	lines2 := strings.Count(strings.TrimRight(string(contents2), "\n"), "\n") + 1
	if lines2 != 1 {
		t.Fatalf("after replay session: lines = %d; want still 1 (replay must not double-append)", lines2)
	}
}

func TestScenario_Tier0_InsufficientBasisTracker_WiredToAttestationStore(t *testing.T) {
	rt, _ := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}

	// Three insufficient-basis verdicts on the same clause cross
	// the configured max (3); the bus should publish an
	// insufficient-basis-rounds-exceeded event.
	var saw bool
	rt.Bus().Subscribe(func(e runner.OperatorEvent) {
		if e.Kind == runner.OpEventInsufficientBasisRoundsExceeded && e.ClauseID == "C5" {
			saw = true
		}
	})
	for i := 1; i <= 3; i++ {
		rec := runner.AttestationRecord{
			ID:             "att-A1-C5-v" + string(rune('0'+i)),
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A1",
			ClauseID:       "C5",
			OpID:           "op",
			AttestedByRole: "implementer",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			Verdict:        runner.AttestationInsufficientBasis,
			Timestamp:      int64(i),
			GridVersion:    uint64(i),
		}
		if err := rt.AttestationStore().Record(rec); err != nil {
			t.Fatal(err)
		}
	}
	if !saw {
		t.Fatal("InsufficientBasisTracker did not publish at max-rounds (wiring broken)")
	}
}

func TestScenario_Tier0_TreeWriter_WritesPerPassFile(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	rec := runner.AttestationRecord{
		ID:             "att-A1-C1-v1",
		Kind:           runner.AttestationKindDepthType,
		ArrowID:        "A1",
		ClauseID:       "C1",
		OpID:           "alice",
		AttestedByRole: "operator",
		SourceRole:     "analyst",
		TargetRole:     "architect",
		Verdict:        runner.AttestationPass,
		Timestamp:      1747663200_000000000,
		GridVersion:    1,
	}
	if err := rt.AttestationStore().Record(rec); err != nil {
		t.Fatal(err)
	}
	rt.journal.Flush()

	expected := filepath.Join(workdir, ".ghyll", "attestations",
		"v1", "default", "stratum-default",
		"analyst__architect", "att-A1-C1-v1.jsonl")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("per-pass file not created at %s: %v", expected, err)
	}
}

func TestScenario_Tier0_RunArrow_DispatcherEndToEnd(t *testing.T) {
	rt, _ := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}

	arrow := runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{
			{
				Concept:  "no-todo-marker",
				ClauseID: "C1",
				Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
			},
		},
	}
	res, err := rt.RunArrow(context.Background(),
		"analyst", "checkout", arrow, runner.DepthRankRealistic)
	if err != nil {
		t.Fatalf("RunArrow: %v", err)
	}
	if res == nil || len(res.Runs) != 1 {
		t.Fatalf("RunArrow returned unexpected result: %+v", res)
	}
	if rt.Passes().Len() != 0 {
		t.Fatalf("PassRegistry should be empty after RunArrow; got %d", rt.Passes().Len())
	}
	if rt.RoleLocks().Len() != 0 {
		t.Fatal("RoleContextLockTable should be empty after RunArrow")
	}
}
