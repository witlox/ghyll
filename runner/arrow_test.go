package runner

import "testing"

func TestArrowStatus_String(t *testing.T) {
	cases := map[ArrowStatus]string{
		ArrowStatusInProgress:  "in-progress",
		ArrowStatusComplete:    "complete",
		ArrowStatusBlocked:     "blocked",
		ArrowStatusUnevaluated: "unevaluated",
		ArrowStatusProvisional: "provisional",
		ArrowStatusInvalidated: "invalidated",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ArrowStatus(%d).String() = %q; want %q", s, got, want)
		}
	}
	if got := ArrowStatus(99).String(); got != "invalid-arrow-status(99)" {
		t.Errorf("out-of-range: got %q", got)
	}
}

func TestArrowStatus_SatisfiesNextRole(t *testing.T) {
	for _, s := range []ArrowStatus{
		ArrowStatusInProgress,
		ArrowStatusBlocked,
		ArrowStatusUnevaluated,
		ArrowStatusProvisional,
		ArrowStatusInvalidated,
	} {
		if s.SatisfiesNextRole() {
			t.Errorf("%s.SatisfiesNextRole() = true; want false", s)
		}
	}
	if !ArrowStatusComplete.SatisfiesNextRole() {
		t.Error("Complete must satisfy next role")
	}
}

func TestDeriveArrowStatus_AllPass(t *testing.T) {
	// Scenario: All clauses pass — derived status complete.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusPass},
	}
	got, n := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete", got)
	}
	if n != 0 {
		t.Errorf("count = %d; want 0", n)
	}
}

func TestDeriveArrowStatus_OneFail(t *testing.T) {
	// Scenario: One clause failed — blocked.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusFail},
	}
	got, n := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked", got)
	}
	if n != 1 {
		t.Errorf("count = %d; want 1", n)
	}
}

func TestDeriveArrowStatus_OneUnevaluatedNoFails(t *testing.T) {
	// Scenario: One clause unevaluated with no fails — unevaluated.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusUnevaluated},
	}
	got, n := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated", got)
	}
	if n != 1 {
		t.Errorf("count = %d; want 1", n)
	}
	if got.SatisfiesNextRole() {
		t.Error("unevaluated must not satisfy next role's input")
	}
}

func TestDeriveArrowStatus_FailAndUnevaluatedCoexist(t *testing.T) {
	// Scenario: Fail and unevaluated coexist — fail wins, blocked.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusFail}, {Status: StatusUnevaluated},
	}
	got, _ := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked (fail trumps unevaluated)", got)
	}
}

func TestDeriveArrowStatus_AwaitingAttestation(t *testing.T) {
	// Scenario: Awaiting attestation produces provisional.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusRunning, AwaitingAttestation: true},
		{Status: StatusRunning, AwaitingAttestation: true},
	}
	got, n := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusProvisional {
		t.Errorf("got %s; want provisional", got)
	}
	if n != 2 {
		t.Errorf("count = %d; want 2 (awaiting count)", n)
	}
}

func TestDeriveArrowStatus_UnevaluatedTrumpsProvisional(t *testing.T) {
	// Scenario: Unevaluated trumps provisional.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusUnevaluated},
		{Status: StatusRunning, AwaitingAttestation: true},
	}
	got, _ := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated (trumps provisional)", got)
	}
}

func TestDeriveArrowStatus_OpenFindingAboveThresholdBlocks(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass},
	}
	findings := []Finding{
		{Status: FindingStatusOpen, SeverityRank: 4}, // high, threshold=3 (medium)
	}
	got, n := DeriveArrowStatus(clauses, findings, 3)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked", got)
	}
	if n != 1 {
		t.Errorf("count = %d; want 1", n)
	}
}

func TestDeriveArrowStatus_OpenFindingBelowThresholdIgnored(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass},
	}
	findings := []Finding{
		{Status: FindingStatusOpen, SeverityRank: 1}, // low, threshold=3 (medium)
	}
	got, _ := DeriveArrowStatus(clauses, findings, 3)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete (low finding below threshold doesn't block)", got)
	}
}

func TestDeriveArrowStatus_ResolvedFindingIgnored(t *testing.T) {
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusResolved, SeverityRank: 5},
		{Status: FindingStatusAcceptedRisk, SeverityRank: 4},
	}
	got, _ := DeriveArrowStatus(clauses, findings, 3)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete (resolved/accepted-risk don't block)", got)
	}
}

func TestDeriveArrowStatus_UnevaluatedFindingPropagates(t *testing.T) {
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusUnevaluated, SeverityRank: 4},
	}
	got, _ := DeriveArrowStatus(clauses, findings, 3)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated (unevaluated finding propagates)", got)
	}
}

func TestDeriveArrowStatus_InProgress(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass},
		{Status: StatusPending},
		{Status: StatusRunning},
	}
	got, n := DeriveArrowStatus(clauses, nil, 3)
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress", got)
	}
	if n != 2 {
		t.Errorf("count = %d; want 2", n)
	}
}

func TestDeriveArrowStatus_EmptyClausesIsInProgress(t *testing.T) {
	got, _ := DeriveArrowStatus(nil, nil, 3)
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress (degenerate empty)", got)
	}
}
