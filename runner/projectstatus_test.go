package runner

import (
	"testing"
	"time"
)

func TestScenario_ProjectStatus_EmptySources_ReturnsZeroes(t *testing.T) {
	st := CaptureProjectStatus(StatusSources{})
	if st.ArrowCount != 0 || st.AmendmentBacklog != 0 || st.AttestationCount != 0 {
		t.Fatalf("empty sources should yield zeroes; got %+v", st)
	}
	if st.CapturedAt.IsZero() {
		t.Fatal("CapturedAt should be stamped even with empty sources")
	}
}

func TestScenario_ProjectStatus_CountsGridArrows(t *testing.T) {
	grid := NewGrid()
	stub := []Clause{{Concept: "lint-clean", ClauseID: "C1"}}
	_, err := grid.Append(ArrowDefinition{
		ID: "A1", SourceRole: "analyst", TargetRole: "architect",
		Stratum: "L1", Context: "checkout", Clauses: stub,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = grid.Append(ArrowDefinition{
		ID: "A2", SourceRole: "architect", TargetRole: "implementer",
		Stratum: "L1", Context: "checkout", Clauses: stub,
	})
	if err != nil {
		t.Fatal(err)
	}

	st := CaptureProjectStatus(StatusSources{Grid: grid})
	if st.ArrowCount != 2 {
		t.Fatalf("ArrowCount = %d; want 2", st.ArrowCount)
	}
}

func TestScenario_ProjectStatus_CountsFindingsByStatus(t *testing.T) {
	fs := NewFindingsStore()
	_ = fs.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug, Severity: SeverityHigh, Status: FindingStatusOpen})
	_ = fs.Raise(FindingRecord{ID: "F2", ArrowID: "A1", Type: FindingTypeLocalBug, Severity: SeverityHigh, Status: FindingStatusOpen})
	_ = fs.Raise(FindingRecord{ID: "F3", ArrowID: "A2", Type: FindingTypeLocalBug, Severity: SeverityLow, Status: FindingStatusOpen})
	// Transition F1 to running, F3 to accepted-risk via the allowed paths.
	_ = fs.Transition("F1", FindingStatusRunning)
	_ = fs.TransitionWithReason("F3", FindingStatusAcceptedRisk, "operator", "documented exception")

	st := CaptureProjectStatus(StatusSources{Findings: fs})
	if st.FindingCounts.Open != 1 {
		t.Errorf("Open = %d; want 1", st.FindingCounts.Open)
	}
	if st.FindingCounts.Running != 1 {
		t.Errorf("Running = %d; want 1", st.FindingCounts.Running)
	}
	if st.FindingCounts.AcceptedRisk != 1 {
		t.Errorf("AcceptedRisk = %d; want 1", st.FindingCounts.AcceptedRisk)
	}
}

func TestScenario_ProjectStatus_BlockingArrowIDs_Sorted(t *testing.T) {
	fs := NewFindingsStore()
	_ = fs.Raise(FindingRecord{ID: "F1", ArrowID: "A-zeta", Type: FindingTypeLocalBug, Severity: SeverityHigh, Status: FindingStatusOpen})
	_ = fs.Raise(FindingRecord{ID: "F2", ArrowID: "A-alpha", Type: FindingTypeLocalBug, Severity: SeverityHigh, Status: FindingStatusOpen})
	_ = fs.Raise(FindingRecord{ID: "F3", ArrowID: "A-beta", Type: FindingTypeLocalBug, Severity: SeverityHigh, Status: FindingStatusOpen})

	st := CaptureProjectStatus(StatusSources{Findings: fs})
	if len(st.BlockingArrowIDs) != 3 {
		t.Fatalf("BlockingArrowIDs len = %d; want 3", len(st.BlockingArrowIDs))
	}
	want := []string{"A-alpha", "A-beta", "A-zeta"}
	for i, id := range want {
		if st.BlockingArrowIDs[i] != id {
			t.Fatalf("BlockingArrowIDs[%d] = %q; want %q (sorted)", i, st.BlockingArrowIDs[i], id)
		}
	}
}

func TestScenario_ProjectStatus_OpenPassesSortedByID(t *testing.T) {
	tbl := NewRoleContextLockTable()
	reg := NewPassRegistry()

	p1, _ := OpenPass(PassOptions{PassID: "P-zeta", Role: "analyst", Context: "c1", ArrowID: "A1", LockTable: tbl})
	reg.Register(p1)
	p2, _ := OpenPass(PassOptions{PassID: "P-alpha", Role: "architect", Context: "c2", ArrowID: "A2", LockTable: tbl})
	reg.Register(p2)
	defer p1.Close("done")
	defer p2.Close("done")

	st := CaptureProjectStatus(StatusSources{Passes: reg})
	if len(st.OpenPasses) != 2 {
		t.Fatalf("OpenPasses len = %d; want 2", len(st.OpenPasses))
	}
	if st.OpenPasses[0].PassID != "P-alpha" {
		t.Fatalf("OpenPasses[0] = %q; want P-alpha (sorted)", st.OpenPasses[0].PassID)
	}
}

func TestScenario_ProjectStatus_AttestationCounts_GroupedByKind(t *testing.T) {
	as := NewAttestationStore()
	_ = as.Record(AttestationRecord{
		ID: "att-A1-C1-v1", Kind: AttestationKindDepthType, ArrowID: "A1", ClauseID: "C1",
		OpID: "op", AttestedByRole: "implementer", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 1, GridVersion: 1,
	})
	_ = as.Record(AttestationRecord{
		ID: "att-A1-C2-v1", Kind: AttestationKindDepthType, ArrowID: "A1", ClauseID: "C2",
		OpID: "op", AttestedByRole: "implementer", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 2, GridVersion: 1,
	})
	_ = as.Record(AttestationRecord{
		ID: "att-A2-v1", Kind: AttestationKindOnTheSpot, ArrowID: "A2",
		OpID: "op", AttestedByRole: "integrator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: AttestationPass, Timestamp: 3, GridVersion: 1,
	})

	st := CaptureProjectStatus(StatusSources{Attestations: as})
	if st.AttestationCount != 3 {
		t.Fatalf("AttestationCount = %d; want 3", st.AttestationCount)
	}
	if st.AttestationsByKind[AttestationKindDepthType] != 2 {
		t.Errorf("depth-type count = %d; want 2", st.AttestationsByKind[AttestationKindDepthType])
	}
	if st.AttestationsByKind[AttestationKindOnTheSpot] != 1 {
		t.Errorf("on-the-spot count = %d; want 1", st.AttestationsByKind[AttestationKindOnTheSpot])
	}
}

func TestScenario_PassRegistry_RegisterUnregisterRoundtrip(t *testing.T) {
	tbl := NewRoleContextLockTable()
	reg := NewPassRegistry()
	p, _ := OpenPass(PassOptions{PassID: "P1", Role: "analyst", Context: "c", ArrowID: "A1", LockTable: tbl})
	reg.Register(p)
	if reg.Len() != 1 {
		t.Fatalf("Len after Register = %d; want 1", reg.Len())
	}
	reg.Unregister(p.ID())
	if reg.Len() != 0 {
		t.Fatalf("Len after Unregister = %d; want 0", reg.Len())
	}
	p.Close("done")
}

func TestScenario_ProjectStatus_WithCustomClock(t *testing.T) {
	pinned := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	st := CaptureProjectStatus(StatusSources{Now: func() time.Time { return pinned }})
	if !st.CapturedAt.Equal(pinned) {
		t.Fatalf("CapturedAt = %v; want %v", st.CapturedAt, pinned)
	}
}
