package runner

import "testing"

func TestArrowStatus_String(t *testing.T) {
	cases := map[ArrowStatus]string{
		ArrowStatusUnset:       "unset",
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
		ArrowStatusUnset,
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

func TestArrowStatus_IsPersisted(t *testing.T) {
	// validation-pass-3 F24: only the 5 spec'd statuses are
	// persisted; Unset and InProgress are runner-internal.
	for _, s := range []ArrowStatus{
		ArrowStatusComplete, ArrowStatusBlocked, ArrowStatusUnevaluated,
		ArrowStatusProvisional, ArrowStatusInvalidated,
	} {
		if !s.IsPersisted() {
			t.Errorf("%s should be persisted", s)
		}
	}
	for _, s := range []ArrowStatus{ArrowStatusUnset, ArrowStatusInProgress} {
		if s.IsPersisted() {
			t.Errorf("%s should NOT be persisted (runner-internal)", s)
		}
	}
}

func TestDeriveArrowStatus_AllPass(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusPass},
	}
	got, c, f := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete", got)
	}
	if c != 0 || f != 0 {
		t.Errorf("(clauses, findings) = (%d, %d); want (0, 0)", c, f)
	}
}

func TestDeriveArrowStatus_OneFail(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusFail},
	}
	got, c, f := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked", got)
	}
	if c != 1 || f != 0 {
		t.Errorf("(clauses, findings) = (%d, %d); want (1, 0)", c, f)
	}
}

func TestDeriveArrowStatus_OneUnevaluatedNoFails(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusPass}, {Status: StatusUnevaluated},
	}
	got, c, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated", got)
	}
	if c != 1 {
		t.Errorf("uneval clauses = %d; want 1", c)
	}
	if got.SatisfiesNextRole() {
		t.Error("unevaluated must not satisfy next role's input")
	}
}

func TestDeriveArrowStatus_FailAndUnevaluatedCoexist(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusFail}, {Status: StatusUnevaluated},
	}
	got, _, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked (fail trumps unevaluated)", got)
	}
}

func TestDeriveArrowStatus_AwaitingAttestationProvisional(t *testing.T) {
	// validation-pass-3 F4: provisional requires all non-attested
	// clauses pass.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusRunning, AwaitingAttestation: true},
		{Status: StatusRunning, AwaitingAttestation: true},
	}
	got, c, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusProvisional {
		t.Errorf("got %s; want provisional", got)
	}
	if c != 2 {
		t.Errorf("awaiting count = %d; want 2", c)
	}
}

func TestDeriveArrowStatus_ProvisionalRequiresAllPass(t *testing.T) {
	// validation-pass-3 F4: pending non-attested clauses block
	// provisional (the spec demands all evaluated clauses pass).
	clauses := []ClauseDeriveInput{
		{Status: StatusPending}, // not yet evaluated
		{Status: StatusRunning, AwaitingAttestation: true},
	}
	got, _, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got == ArrowStatusProvisional {
		t.Errorf("got provisional; want in-progress (pending non-attested clause blocks provisional)")
	}
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress", got)
	}
}

func TestDeriveArrowStatus_InsufficientBasisProvisional(t *testing.T) {
	// validation-pass-3 F5: insufficient-basis is a peer of
	// awaiting-attestation.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass},
		{Status: StatusRunning, InsufficientBasis: true},
	}
	got, c, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusProvisional {
		t.Errorf("got %s; want provisional (insufficient-basis)", got)
	}
	if c != 1 {
		t.Errorf("pending-attestation count = %d; want 1", c)
	}
}

func TestDeriveArrowStatus_UnevaluatedTrumpsProvisional(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusPass},
		{Status: StatusUnevaluated},
		{Status: StatusRunning, AwaitingAttestation: true},
	}
	got, _, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated (trumps provisional)", got)
	}
}

func TestDeriveArrowStatus_AwaitingFlagIgnoredOnNonRunning(t *testing.T) {
	// validation-pass-3 F7: AwaitingAttestation valid only when
	// Status==StatusRunning. Pending+Awaiting is reinterpreted as
	// plain pending.
	clauses := []ClauseDeriveInput{
		{Status: StatusPass},
		{Status: StatusPending, AwaitingAttestation: true},
	}
	got, _, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress (awaiting on pending is invalid → plain pending)", got)
	}
}

func TestDeriveArrowStatus_OpenFindingAtThresholdBlocks(t *testing.T) {
	// validation-pass-3 F6: threshold inclusive. Finding at exactly
	// threshold blocks.
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusOpen, SeverityRank: SeverityMedium},
	}
	got, _, f := DeriveArrowStatus(clauses, findings, SeverityMedium)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked (at-threshold inclusive)", got)
	}
	if f != 1 {
		t.Errorf("blocking findings = %d; want 1", f)
	}
}

func TestDeriveArrowStatus_OpenFindingBelowThresholdIgnored(t *testing.T) {
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusOpen, SeverityRank: SeverityLow},
	}
	got, _, _ := DeriveArrowStatus(clauses, findings, SeverityMedium)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete (low finding below threshold doesn't block)", got)
	}
}

func TestDeriveArrowStatus_ResolvedFindingIgnored(t *testing.T) {
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusResolved, SeverityRank: SeverityCritical},
		{Status: FindingStatusAcceptedRisk, SeverityRank: SeverityHigh},
	}
	got, _, _ := DeriveArrowStatus(clauses, findings, SeverityMedium)
	if got != ArrowStatusComplete {
		t.Errorf("got %s; want complete (resolved/accepted-risk don't block)", got)
	}
}

func TestDeriveArrowStatus_UnevaluatedFindingPropagates(t *testing.T) {
	// validation-pass-3 F25: SeverityRank meaningless when Status
	// is FindingStatusUnevaluated.
	clauses := []ClauseDeriveInput{{Status: StatusPass}}
	findings := []Finding{
		{Status: FindingStatusUnevaluated, SeverityRank: SeverityHigh},
	}
	got, _, f := DeriveArrowStatus(clauses, findings, SeverityMedium)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated (unevaluated finding propagates)", got)
	}
	if f != 1 {
		t.Errorf("uneval findings = %d; want 1", f)
	}
}

func TestDeriveArrowStatus_OutOfRangeClauseStatus(t *testing.T) {
	// validation-pass-3 F8: out-of-range status → unevaluated, not
	// silently complete.
	clauses := []ClauseDeriveInput{{Status: ClauseStatus(99)}}
	got, _, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusUnevaluated {
		t.Errorf("got %s; want unevaluated (garbage status corrupts to safe state)", got)
	}
}

func TestDeriveArrowStatus_InProgress(t *testing.T) {
	clauses := []ClauseDeriveInput{
		{Status: StatusPass},
		{Status: StatusPending},
		{Status: StatusRunning},
	}
	got, c, _ := DeriveArrowStatus(clauses, nil, SeverityMedium)
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress", got)
	}
	if c != 2 {
		t.Errorf("count = %d; want 2", c)
	}
}

func TestDeriveArrowStatus_EmptyClausesIsInProgress(t *testing.T) {
	got, _, _ := DeriveArrowStatus(nil, nil, SeverityMedium)
	if got != ArrowStatusInProgress {
		t.Errorf("got %s; want in-progress (degenerate empty)", got)
	}
}

func TestDeriveArrowStatus_SeparateClauseAndFindingCounts(t *testing.T) {
	// validation-pass-3 F22: counts split into clauses + findings.
	clauses := []ClauseDeriveInput{
		{Status: StatusFail}, {Status: StatusFail},
	}
	findings := []Finding{
		{Status: FindingStatusOpen, SeverityRank: SeverityCritical},
		{Status: FindingStatusOpen, SeverityRank: SeverityCritical},
		{Status: FindingStatusOpen, SeverityRank: SeverityCritical},
	}
	got, c, f := DeriveArrowStatus(clauses, findings, SeverityMedium)
	if got != ArrowStatusBlocked {
		t.Errorf("got %s; want blocked", got)
	}
	if c != 2 {
		t.Errorf("blocking clauses = %d; want 2", c)
	}
	if f != 3 {
		t.Errorf("blocking findings = %d; want 3", f)
	}
}
