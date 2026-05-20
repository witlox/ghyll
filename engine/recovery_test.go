package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/witlox/ghyll/runner"
)

// freshStore opens a brand-new engine.db for one test.
func freshStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fixedNow returns a clock that always reports t.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestRecovery_NoOpenPasses(t *testing.T) {
	s := freshStore(t)
	deps := RecoveryDeps{Store: s, Now: fixedNow(time.Now())}
	rep, err := Recovery(context.Background(), deps, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery: %v", err)
	}
	if rep.OrphansAborted != 0 || rep.OrphansPreserved != 0 ||
		rep.EvaluationRunsFlipped != 0 {
		t.Errorf("counts non-zero on empty restart: %+v", rep)
	}
	if len(rep.Events) != 0 {
		t.Errorf("events = %v; want none on empty restart", rep.Events)
	}
}

func TestRecovery_OrphanAbort(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	if err := s.UpsertPass(ctx, PassRecord{
		PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
		State: "open", OpenedAt: "2026-05-20T11:00:00Z",
	}); err != nil {
		t.Fatalf("seed pass: %v", err)
	}

	lockTable := runner.NewRoleContextLockTable()
	rep, err := Recovery(ctx, RecoveryDeps{
		Store:     s,
		Passes:    runner.NewPassRegistry(),
		LockTable: lockTable,
		Now:       fixedNow(now),
	}, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery: %v", err)
	}
	if rep.OrphansAborted != 1 || rep.OrphansPreserved != 0 {
		t.Errorf("counts = %+v; want aborted=1 preserved=0", rep)
	}
	if len(rep.Events) != 1 || rep.Events[0].Kind != runner.OpEventRecoveryPassAbortedCrash {
		t.Errorf("events = %+v; want one crash event", rep.Events)
	}

	got, ok, _ := s.GetPass(ctx, "P1")
	if !ok || got.State != "aborted" || got.CloseReason != "crash" {
		t.Errorf("post-recovery row: state=%q reason=%q", got.State, got.CloseReason)
	}
}

func TestRecovery_AttestationPendingPreserved(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	// Seed pass + evaluation_run with depth_type_attestation_ref
	// but NO attestation row.
	if err := s.UpsertPass(ctx, PassRecord{
		PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
		State: "open", OpenedAt: "2026-05-20T11:00:00Z",
	}); err != nil {
		t.Fatalf("seed pass: %v", err)
	}
	if err := s.InsertEvaluationRun(ctx, EvaluationRunRecord{
		ID: "R1", ClauseID: "C5", PassID: "P1", ArrowID: "A1",
		DepthTypeAttestationRef: "att-X-C5-v1",
		StartStatus:             "pending",
		EndStatus:               "running",
		ResultJSON:              "{}",
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	lockTable := runner.NewRoleContextLockTable()
	passes := runner.NewPassRegistry()
	atts := runner.NewAttestationStore()
	rep, err := Recovery(ctx, RecoveryDeps{
		Store:        s,
		Passes:       passes,
		Attestations: atts,
		LockTable:    lockTable,
		Now:          fixedNow(now),
	}, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery: %v", err)
	}
	if rep.OrphansAborted != 0 || rep.OrphansPreserved != 1 {
		t.Errorf("counts = %+v; want aborted=0 preserved=1", rep)
	}
	if len(rep.Events) != 1 || rep.Events[0].Kind != runner.OpEventRecoveryAttestationRepublished {
		t.Errorf("events = %+v; want one republish event", rep.Events)
	}

	got, ok, _ := s.GetPass(ctx, "P1")
	if !ok || got.State != "open" {
		t.Errorf("post-recovery row state = %q; want open (preserved)", got.State)
	}
	if got.RecoveredAt == "" {
		t.Error("RecoveredAt unstamped after preservation")
	}

	// Registry contains the resumed pass + lock is held.
	if passes.Len() != 1 {
		t.Errorf("registry len = %d; want 1 (resumed)", passes.Len())
	}
	if holder, _ := lockTable.InspectHolder("analyst", "A"); holder != "P1" {
		t.Errorf("lock holder = %q; want P1 (resume re-acquired)", holder)
	}
}

func TestRecovery_EvaluationRunReconcile(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	if err := s.InsertEvaluationRun(ctx, EvaluationRunRecord{
		ID: "R1", ClauseID: "C5", PassID: "P1", ArrowID: "A1",
		DepthTypeAttestationRef: "att-X",
		StartStatus:             "pending",
		EndStatus:               "running",
		ResultJSON:              "{}",
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	atts := runner.NewAttestationStore()
	if err := atts.Record(runner.AttestationRecord{
		ID: "att-X", Kind: runner.AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C5", OpID: "alice", AttestedByRole: "operator",
		Verdict: runner.AttestationPass, Timestamp: 1, GridVersion: 1,
	}); err != nil {
		t.Fatalf("seed attestation: %v", err)
	}

	rep, err := Recovery(ctx, RecoveryDeps{
		Store:        s,
		Attestations: atts,
		Now:          fixedNow(now),
	}, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery: %v", err)
	}
	if rep.EvaluationRunsFlipped != 1 {
		t.Errorf("EvaluationRunsFlipped = %d; want 1", rep.EvaluationRunsFlipped)
	}
	if len(rep.Events) != 1 || rep.Events[0].Kind != runner.OpEventRecoveryAttestationReplay {
		t.Errorf("events = %+v; want one replay event", rep.Events)
	}

	// Verify the row got the new end_status + provenance.
	var endStatus, recoverySrc string
	if err := s.DB().QueryRowContext(ctx,
		`SELECT end_status, recovery_source FROM evaluation_runs WHERE id = ?`, "R1",
	).Scan(&endStatus, &recoverySrc); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if endStatus != "pass" {
		t.Errorf("end_status = %q; want pass", endStatus)
	}
	if recoverySrc != recoverySource {
		t.Errorf("recovery_source = %q", recoverySrc)
	}
}

func TestRecovery_Idempotent(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertPass(ctx, PassRecord{
		PassID: "P1", Role: "r", Context: "c", ArrowID: "A1",
		State: "open", OpenedAt: "2026-05-20T11:00:00Z",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	deps := RecoveryDeps{Store: s, Passes: runner.NewPassRegistry(),
		LockTable: runner.NewRoleContextLockTable(), Now: fixedNow(now)}
	rep1, err := Recovery(ctx, deps, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery 1: %v", err)
	}
	if rep1.OrphansAborted != 1 {
		t.Fatalf("first call did not abort the orphan: %+v", rep1)
	}
	// Second call must be a no-op.
	deps.Passes = runner.NewPassRegistry()
	deps.LockTable = runner.NewRoleContextLockTable()
	rep2, err := Recovery(ctx, deps, ReplayCounts{})
	if err != nil {
		t.Fatalf("Recovery 2: %v", err)
	}
	if rep2.OrphansAborted != 0 || rep2.OrphansPreserved != 0 ||
		rep2.EvaluationRunsFlipped != 0 || len(rep2.Events) != 0 {
		t.Errorf("second call non-empty: %+v", rep2)
	}
}

func TestRecovery_ReplayCountsErrorsRefuses(t *testing.T) {
	s := freshStore(t)
	deps := RecoveryDeps{Store: s, Now: fixedNow(time.Now())}
	_, err := Recovery(context.Background(), deps, ReplayCounts{
		Errors: []string{"row 1 corrupt"},
	})
	if !errors.Is(err, ErrRecoveryReplayDirty) {
		t.Errorf("err = %v; want ErrRecoveryReplayDirty", err)
	}
}

func TestRecovery_JSONLMissingFreshProject(t *testing.T) {
	dir := t.TempDir()
	atts := runner.NewAttestationStore()
	loaded, truncated, err := atts.LoadFromJSONL(
		filepath.Join(dir, "attestations.jsonl"), false)
	if err != nil {
		t.Fatalf("LoadFromJSONL: %v", err)
	}
	if loaded != 0 || truncated {
		t.Errorf("loaded=%d truncated=%v; want 0,false", loaded, truncated)
	}
}

func TestRecovery_JSONLMissingWithRows(t *testing.T) {
	dir := t.TempDir()
	atts := runner.NewAttestationStore()
	_, _, err := atts.LoadFromJSONL(
		filepath.Join(dir, "attestations.jsonl"), true)
	if !errors.Is(err, runner.ErrAttestationAuditLost) {
		t.Errorf("err = %v; want ErrAttestationAuditLost", err)
	}
}

func TestRecovery_VerdictToClauseStatus(t *testing.T) {
	cases := []struct {
		v    runner.AttestationVerdict
		want string
	}{
		{runner.AttestationPass, "pass"},
		{runner.AttestationFail, "fail"},
		{runner.AttestationInsufficientBasis, "running"},
	}
	for _, tc := range cases {
		if got := verdictToClauseStatus(tc.v); got != tc.want {
			t.Errorf("%q → %q; want %q", tc.v, got, tc.want)
		}
	}
}
