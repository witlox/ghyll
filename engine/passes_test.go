package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// TestScenario_UpsertPass_RoundTrips verifies the insert + on-conflict
// update + read path for the new passes table.
func TestScenario_UpsertPass_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	rec := PassRecord{
		PassID:      "P1",
		Role:        "analyst",
		Context:     "contextA",
		ArrowID:     "A1",
		GridVersion: 1,
		State:       "open",
		OpenedAt:    "2026-05-20T10:00:00Z",
	}
	if err := s.UpsertPass(ctx, rec); err != nil {
		t.Fatalf("UpsertPass(open): %v", err)
	}

	got, ok, err := s.GetPass(ctx, "P1")
	if err != nil {
		t.Fatalf("GetPass: %v", err)
	}
	if !ok {
		t.Fatalf("GetPass: not found after Upsert")
	}
	if got.State != "open" || got.OpenedAt != rec.OpenedAt {
		t.Errorf("state = %q opened = %q; want open / %q", got.State, got.OpenedAt, rec.OpenedAt)
	}

	// Update to closed via the same upsert.
	rec.State = "closed"
	rec.ClosedAt = "2026-05-20T10:05:00Z"
	rec.CloseReason = "derived-complete"
	if err := s.UpsertPass(ctx, rec); err != nil {
		t.Fatalf("UpsertPass(closed): %v", err)
	}
	got, _, _ = s.GetPass(ctx, "P1")
	if got.State != "closed" || got.CloseReason != "derived-complete" {
		t.Errorf("after close: state=%q reason=%q", got.State, got.CloseReason)
	}
}

func TestScenario_UpsertPass_RecoveredAtSetOnce(t *testing.T) {
	// F-12 invariant: recovered_at is set-once. A second UpsertPass
	// carrying a different recovered_at must not overwrite the first.
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	rec := PassRecord{
		PassID: "P1", Role: "analyst", Context: "A", ArrowID: "A1",
		State: "open", RecoveredAt: "2026-05-20T11:00:00Z",
	}
	if err := s.UpsertPass(ctx, rec); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	rec.RecoveredAt = "2026-05-21T00:00:00Z"
	if err := s.UpsertPass(ctx, rec); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, _, _ := s.GetPass(ctx, "P1")
	if got.RecoveredAt != "2026-05-20T11:00:00Z" {
		t.Errorf("RecoveredAt = %q; want frozen at first value", got.RecoveredAt)
	}
}

func TestScenario_UpsertPass_ValidationRejects(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	cases := []struct {
		name string
		rec  PassRecord
		want error
	}{
		{"empty pass_id", PassRecord{Role: "r", Context: "c", ArrowID: "a", State: "open"}, ErrEnginePassIDEmpty},
		{"empty role", PassRecord{PassID: "P1", Context: "c", ArrowID: "a", State: "open"}, ErrEnginePassRoleEmpty},
		{"empty context", PassRecord{PassID: "P1", Role: "r", ArrowID: "a", State: "open"}, ErrEnginePassContextEmpty},
		{"empty arrow_id", PassRecord{PassID: "P1", Role: "r", Context: "c", State: "open"}, ErrEnginePassArrowEmpty},
		{"bad state", PassRecord{PassID: "P1", Role: "r", Context: "c", ArrowID: "a", State: "running"}, ErrEnginePassStateInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.UpsertPass(ctx, tc.rec)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v; want %v", err, tc.want)
			}
		})
	}
}

func TestScenario_ListPasses_FilterAndOrder(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	// Three passes: P1 open, P2 closed, P3 aborted. Distinct opened_at
	// to pin the chronological order.
	for _, rec := range []PassRecord{
		{PassID: "P1", Role: "r", Context: "c", ArrowID: "A1", State: "open", OpenedAt: "2026-05-20T10:00:00Z"},
		{PassID: "P2", Role: "r", Context: "c", ArrowID: "A2", State: "closed", OpenedAt: "2026-05-20T10:01:00Z", ClosedAt: "2026-05-20T10:02:00Z"},
		{PassID: "P3", Role: "r", Context: "c", ArrowID: "A3", State: "aborted", OpenedAt: "2026-05-20T10:03:00Z", ClosedAt: "2026-05-20T10:04:00Z", CloseReason: "crash"},
	} {
		if err := s.UpsertPass(ctx, rec); err != nil {
			t.Fatalf("Upsert %s: %v", rec.PassID, err)
		}
	}

	all, err := s.ListPasses(ctx, PassListFilter{})
	if err != nil {
		t.Fatalf("ListPasses(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("len(all) = %d; want 3", len(all))
	}
	if all[0].PassID != "P1" || all[2].PassID != "P3" {
		t.Errorf("order = %s,%s,%s; want chronological", all[0].PassID, all[1].PassID, all[2].PassID)
	}

	open, err := s.ListPasses(ctx, PassListFilter{State: "open"})
	if err != nil {
		t.Fatalf("ListPasses(open): %v", err)
	}
	if len(open) != 1 || open[0].PassID != "P1" {
		t.Errorf("open filter = %+v; want [P1]", open)
	}

	count, err := s.CountPasses(ctx)
	if err != nil {
		t.Fatalf("CountPasses: %v", err)
	}
	if count != 3 {
		t.Errorf("CountPasses = %d; want 3", count)
	}
}

func TestScenario_UpdateEvaluationRunReconciled(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	// Seed a run row in running state.
	run := EvaluationRunRecord{
		ID: "R1", ClauseID: "C1", PassID: "P1", ArrowID: "A1",
		DepthTypeAttestationRef: "att-1",
		StartStatus:             "pending",
		EndStatus:               "running",
		ResultJSON:              "{}",
	}
	if err := s.InsertEvaluationRun(ctx, run); err != nil {
		t.Fatalf("InsertEvaluationRun: %v", err)
	}

	// Reconcile to pass with provenance.
	if err := s.UpdateEvaluationRunReconciled(ctx, "R1", "pass",
		"recovery-attestation-replay", "2026-05-20T12:00:00Z"); err != nil {
		t.Fatalf("UpdateEvaluationRunReconciled: %v", err)
	}

	// Verify via direct DB read (no public scan API for full row).
	var endStatus, recovery, completed string
	if err := s.DB().QueryRowContext(ctx,
		`SELECT end_status, recovery_source, completed_at FROM evaluation_runs WHERE id = ?`, "R1",
	).Scan(&endStatus, &recovery, &completed); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if endStatus != "pass" {
		t.Errorf("end_status = %q; want pass", endStatus)
	}
	if recovery != "recovery-attestation-replay" {
		t.Errorf("recovery_source = %q", recovery)
	}
	if completed != "2026-05-20T12:00:00Z" {
		t.Errorf("completed_at = %q; want filled by reconcile", completed)
	}

	// Update on missing row → not-found.
	err = s.UpdateEvaluationRunReconciled(ctx, "Rmissing", "pass", "x", "t")
	if !errors.Is(err, ErrEngineEvaluationRunNotFound) {
		t.Errorf("missing row err = %v; want ErrEngineEvaluationRunNotFound", err)
	}
}
