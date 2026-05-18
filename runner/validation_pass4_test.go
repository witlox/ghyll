package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression tests for validation-pass-4 remediations. One test per
// load-bearing finding so the fix is encoded in the suite, not just
// the prose doc.

// F1: byID must remain coherent across slice growth in byArrow.
// Pre-fix: byID stored *FindingRecord aliasing into byArrow; after
// any append-induced realloc, Transition wrote to a stale array.
func TestF1_FindingsStore_ByIDStableAcrossAppendGrowth(t *testing.T) {
	s := NewFindingsStore()
	// Raise enough findings to cross multiple slice-growth boundaries
	// (cap=1, 2, 4, 8, 16, 32, 64).
	for i := 0; i < 100; i++ {
		if err := s.Raise(FindingRecord{
			ID:       fmt.Sprintf("F%d", i),
			ArrowID:  "A1",
			Type:     FindingTypeLocalBug,
			Severity: SeverityInfo,
			Status:   FindingStatusOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Transition the FIRST finding (which was raised before any
	// growth occurred). Pre-fix: this write went to a stale backing
	// array; Get("F0") would still report Open.
	if err := s.Transition("F0", FindingStatusResolved); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("F0")
	if !ok || got.Status != FindingStatusResolved {
		t.Errorf("F0 status = %v; want resolved (F1 byID staleness regression)", got.Status)
	}
	// ForArrow must see the transitioned state too.
	for _, r := range s.ForArrow("A1") {
		if r.ID == "F0" && r.Status != FindingStatusResolved {
			t.Errorf("ForArrow F0 status = %v; want resolved (F1)", r.Status)
		}
	}
}

// F3: operator-supplied artifact-path must be refused when it
// escapes the project tree.
func TestF3_ArrowArtifact_PathTraversalRefused(t *testing.T) {
	dir := t.TempDir()
	// Construct a real out-of-tree file so containment-bypass would
	// observably attest the wrong thing if not refused.
	parent := filepath.Dir(dir)
	leak := filepath.Join(parent, "leaked-secret-"+filepath.Base(dir))
	_ = os.WriteFile(leak, []byte("secret"), 0644)
	defer func() { _ = os.Remove(leak) }()

	relTraversal := "../leaked-secret-" + filepath.Base(dir)
	res, err := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"artifact-path": relTraversal},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("path-traversal should fail; got pass with details %+v (F3)", res.Details)
	}
}

// F4: a symlink in an intermediate parent directory must block the
// final artifact resolution.
func TestF4_ArrowArtifact_ParentSymlinkBypassRefused(t *testing.T) {
	dir := t.TempDir()
	// Real out-of-project target.
	parent := filepath.Dir(dir)
	outside := filepath.Join(parent, "outside-"+filepath.Base(dir))
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(outside) }()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	// Symlink inside the project that points at the outside dir.
	linkPath := filepath.Join(dir, "linkdir")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res, err := EvaluateArrowArtifactPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args:       map[string]any{"artifact-path": "linkdir/secret.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("parent-symlink should refuse; got pass with details %+v (F4)", res.Details)
	}
}

// F9: Severity outside 0..4 must be rejected at Raise time.
func TestF9_FindingsStore_RejectsOutOfRangeSeverity(t *testing.T) {
	s := NewFindingsStore()
	err := s.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug, Severity: 99})
	if !errors.Is(err, ErrFindingInvalidSeverity) {
		t.Errorf("severity=99 should error; got %v", err)
	}
	err = s.Raise(FindingRecord{ID: "F2", ArrowID: "A1", Type: FindingTypeLocalBug, Severity: -1})
	if !errors.Is(err, ErrFindingInvalidSeverity) {
		t.Errorf("severity=-1 should error; got %v", err)
	}
}

// F22: invalid Type shapes must be rejected at Raise.
func TestF22_FindingsStore_RejectsInvalidType(t *testing.T) {
	s := NewFindingsStore()
	cases := []FindingType{
		"   ",          // whitespace
		"local-bug\n",  // trailing newline
		"LOCAL-BUG",    // uppercase
		"local_bug",    // underscore
		"1-starts-num", // leading digit
	}
	for _, ty := range cases {
		err := s.Raise(FindingRecord{ID: "F", ArrowID: "A", Type: ty})
		if !errors.Is(err, ErrFindingTypeInvalid) && !errors.Is(err, ErrFindingTypeEmpty) {
			t.Errorf("Type=%q should reject; got %v", ty, err)
		}
	}
}

// F23: out-of-range Status must be rejected at Raise.
func TestF23_FindingsStore_RejectsUnknownStatus(t *testing.T) {
	s := NewFindingsStore()
	err := s.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug,
		Status: FindingStatus(99),
	})
	if !errors.Is(err, ErrFindingInvalidStatus) {
		t.Errorf("unknown status should error; got %v", err)
	}
}

// F25: Transition above maxFindingTransitions returns
// ErrFindingTransitionChurn.
func TestF25_FindingsStore_TransitionChurnGuard(t *testing.T) {
	s := NewFindingsStore()
	if err := s.Raise(FindingRecord{
		ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug, Status: FindingStatusOpen,
	}); err != nil {
		t.Fatal(err)
	}
	// Ping-pong open → running → open repeatedly until churn fires.
	for i := 0; i < maxFindingTransitions+5; i++ {
		err := s.Transition("F1", FindingStatusRunning)
		if errors.Is(err, ErrFindingTransitionChurn) {
			return // success
		}
		if err != nil {
			t.Fatalf("transition iter %d: %v", i, err)
		}
		err = s.Transition("F1", FindingStatusOpen)
		if errors.Is(err, ErrFindingTransitionChurn) {
			return // success
		}
		if err != nil {
			t.Fatalf("transition back iter %d: %v", i, err)
		}
	}
	t.Errorf("expected ErrFindingTransitionChurn within %d cycles", maxFindingTransitions)
}

// F24: Forget removes a finding and ForgetArrow removes all on an
// arrow.
func TestF24_FindingsStore_ForgetAndForgetArrow(t *testing.T) {
	s := NewFindingsStore()
	for i := 0; i < 5; i++ {
		_ = s.Raise(FindingRecord{
			ID: fmt.Sprintf("F%d", i), ArrowID: "A1",
			Type: FindingTypeLocalBug, Status: FindingStatusOpen,
		})
	}
	if err := s.Forget("F2"); err != nil {
		t.Fatalf("Forget F2: %v", err)
	}
	if _, ok := s.Get("F2"); ok {
		t.Error("F2 should be gone after Forget")
	}
	// Indices must stay coherent for remaining entries.
	got, _ := s.Get("F4")
	if got.ID != "F4" {
		t.Errorf("F4 lost after Forget(F2); got %+v", got)
	}
	n := s.ForgetArrow("A1")
	if n != 4 {
		t.Errorf("ForgetArrow returned %d; want 4", n)
	}
	if len(s.ForArrow("A1")) != 0 {
		t.Error("ForArrow should be empty after ForgetArrow")
	}
}

// F26: nested WithFindingsStore must panic (silent shadowing was the
// original hazard).
func TestF26_WithFindingsStore_NestedPanics(t *testing.T) {
	ctx := WithFindingsStore(context.Background(), NewFindingsStore())
	defer func() {
		if r := recover(); r == nil {
			t.Error("nested WithFindingsStore should panic")
		}
	}()
	_ = WithFindingsStore(ctx, NewFindingsStore())
}

// F36: unknown AmendmentReason rejected.
func TestF36_AmendmentRequest_UnknownReasonRefused(t *testing.T) {
	r := AmendmentRequest{
		ID: "amend-1", Reason: AmendmentReason("missing-cross-conetxt-spec"), // typo
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	}
	err := r.Validate()
	if err == nil || !strings.Contains(err.Error(), "amendment-reason-unknown") {
		t.Errorf("unknown reason should error; got %v", err)
	}
}

// F38: AmendmentQueue overflow.
func TestF38_AmendmentQueue_MaxLenOverflow(t *testing.T) {
	q := NewAmendmentQueueWithMax(2)
	mk := func(id string) AmendmentRequest {
		return AmendmentRequest{
			ID: id, Reason: AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "A", TargetRole: "analyst",
			Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F"},
		}
	}
	if err := q.Enqueue(mk("a1")); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(mk("a2")); err != nil {
		t.Fatal(err)
	}
	err := q.Enqueue(mk("a3"))
	if !errors.Is(err, ErrAmendmentQueueFull) {
		t.Errorf("overflow should error with ErrAmendmentQueueFull; got %v", err)
	}
}

// F37: Pending and Drain return deep-copied slice fields; mutation
// of the snapshot must not poison the queue.
func TestF37_AmendmentQueue_PendingDeepCopy(t *testing.T) {
	q := NewAmendmentQueue()
	r := AmendmentRequest{
		ID: "a1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	}
	if err := q.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	snap := q.Pending()
	snap[0].Contexts[0] = "POISONED"
	snap[0].FindingIDs[0] = "POISONED"
	// Queue's internal state must be untouched.
	live := q.Pending()
	if live[0].Contexts[0] != "c1" {
		t.Errorf("Pending must deep-copy Contexts; got %v", live[0].Contexts)
	}
	if live[0].FindingIDs[0] != "F1" {
		t.Errorf("Pending must deep-copy FindingIDs; got %v", live[0].FindingIDs)
	}
}

// F40: defaultAmendmentIDGen must yield unique IDs in a tight loop
// (no nano-collision).
func TestF40_DefaultAmendmentIDGen_UniqueInLoop(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		id := defaultAmendmentIDGen()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID at iter %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// F43: FormatAmendmentSummary escapes embedded newlines.
func TestF43_FormatAmendmentSummary_EscapesNewlines(t *testing.T) {
	r := AmendmentRequest{
		ID:          "a1",
		Reason:      AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A",
		TargetRole:  "analyst",
		Contexts:    []string{"c1", "c2"},
		FindingIDs:  []string{"F1"},
		Description: "real\n  source-arrow: forged",
	}
	out := FormatAmendmentSummary(r)
	if strings.Contains(out, "real\n  source-arrow: forged") {
		t.Errorf("embedded newline forged a field; output:\n%s", out)
	}
	if !strings.Contains(out, `\n`) {
		t.Errorf("expected `\\n` escape in output; got:\n%s", out)
	}
}

// F44: drained IDs remain refused until Reset.
func TestF44_AmendmentQueue_DrainedIDsRememberedUntilReset(t *testing.T) {
	q := NewAmendmentQueue()
	r := AmendmentRequest{
		ID: "a1", Reason: AmendmentReasonMissingCrossContextSpec,
		SourceArrow: "A", TargetRole: "analyst",
		Contexts: []string{"c1", "c2"}, FindingIDs: []string{"F1"},
	}
	if err := q.Enqueue(r); err != nil {
		t.Fatal(err)
	}
	_ = q.Drain()
	if err := q.Enqueue(r); !errors.Is(err, ErrAmendmentDuplicateID) {
		t.Errorf("re-enqueue after Drain should refuse; got %v", err)
	}
	q.Reset()
	if err := q.Enqueue(r); err != nil {
		t.Errorf("re-enqueue after Reset must succeed; got %v", err)
	}
}

// F5: store.Version increments on every mutation.
func TestF5_FindingsStore_VersionIncrements(t *testing.T) {
	s := NewFindingsStore()
	v0 := s.Version()
	_ = s.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug})
	v1 := s.Version()
	if v1 <= v0 {
		t.Errorf("Version did not advance on Raise: %d -> %d", v0, v1)
	}
	_ = s.Transition("F1", FindingStatusResolved)
	v2 := s.Version()
	if v2 <= v1 {
		t.Errorf("Version did not advance on Transition: %d -> %d", v1, v2)
	}
	_ = s.Forget("F1")
	v3 := s.Version()
	if v3 <= v2 {
		t.Errorf("Version did not advance on Forget: %d -> %d", v2, v3)
	}
}

// F8: corrupt FindingStatus defaults to blocking in isBlockingFinding.
func TestF8_IsBlockingFinding_CorruptStatusBlocks(t *testing.T) {
	if !isBlockingFinding(FindingRecord{Status: FindingStatus(99), Severity: 0}, SeverityMedium) {
		t.Error("corrupt FindingStatus should default to blocking")
	}
}

// F16: zero-width regex refused by both tracelink and cardinality.
func TestF16_ZeroWidthRegexRefused(t *testing.T) {
	dir := t.TempDir()
	_, errTrace := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":      "**",
			"to":        "**",
			"link-rule": "(.*)", // matches empty string
		},
	})
	if errTrace == nil {
		t.Error("tracelink should refuse zero-width regex")
	}
	_, errCard := EvaluateCardinalityCheck(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"query":        "(.*)",
			"query-target": "**",
			"expected":     0,
		},
	})
	if errCard == nil {
		t.Error("cardinality should refuse zero-width regex")
	}
}

// F27: empty `to` glob → Unevaluated.
func TestF27_TraceLink_EmptyToGlobIsUnevaluated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "specs/x.md", "ref `nonexistent_test`\n")
	res, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":      "specs/*.md",
			"to":        "tests/no-such-dir/*.go",
			"link-rule": "`(\\w+_test)`",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("empty to glob should produce Unevaluated; got %+v", res)
	}
}

// F13: link-rule with alternation across capture groups should pick
// the first non-empty capture per match.
func TestF13_TraceLink_AlternationPicksFirstNonEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "specs/x.md", "fixes #42 or relates-to FEAT-7\n")
	writeFile(t, dir, "tickets/42.md", "")
	writeFile(t, dir, "tickets/FEAT-7.md", "")
	res, err := EvaluateTraceLinkPresent(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"from":             "specs/*.md",
			"to":               "tickets/*.md",
			"link-rule":        `fixes #(\d+)|relates-to ([A-Z]+-\d+)`,
			"min-multiplicity": 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass (2 distinct targets, one per branch); got %+v", res.Details)
	}
}

// F12 + F31: filteredParentEnv must not leak ANTHROPIC_API_KEY etc.
func TestF31_FilteredParentEnv_DoesNotLeakSecrets(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "should-not-leak")
	t.Setenv("PATH", "/usr/bin")
	env := filteredParentEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") {
			t.Error("ANTHROPIC_API_KEY leaked to schema-check env")
		}
	}
}

// Phase-5 preflight: FindingsStore.Observe fires for every mutation
// kind under the write lock.
func TestPhase5Preflight_FindingsStoreObserve(t *testing.T) {
	s := NewFindingsStore()
	var events []FindingsEvent
	s.Observe(func(e FindingsEvent) { events = append(events, e) })
	_ = s.Raise(FindingRecord{ID: "F1", ArrowID: "A1", Type: FindingTypeLocalBug})
	_ = s.Transition("F1", FindingStatusResolved)
	_ = s.Forget("F1")
	_ = s.Raise(FindingRecord{ID: "F2", ArrowID: "A2", Type: FindingTypeLocalBug})
	_ = s.ForgetArrow("A2")
	wantKinds := []FindingsEventKind{
		FindingsEventRaise,
		FindingsEventTransition,
		FindingsEventForget,
		FindingsEventRaise,
		FindingsEventForgetArrow,
	}
	if len(events) != len(wantKinds) {
		t.Fatalf("event count = %d; want %d (events: %+v)", len(events), len(wantKinds), events)
	}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Errorf("event[%d].Kind = %v; want %v", i, events[i].Kind, want)
		}
	}
	for i := 1; i < len(events); i++ {
		if events[i].Version <= events[i-1].Version {
			t.Errorf("event[%d].Version = %d; want > %d", i, events[i].Version, events[i-1].Version)
		}
	}
}

// Phase-5 preflight: Clause.ArrowID + GridVersion propagate into the
// persisted EvaluationRun.
func TestPhase5Preflight_EvaluationRunCarriesArrowIDAndGridVersion(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register("test-pass", func(_ context.Context, _ Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(reg)
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{
		Concept:     "test-pass",
		ArrowID:     "analyst→arch/L4/checkout",
		GridVersion: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ArrowID != "analyst→arch/L4/checkout" {
		t.Errorf("ArrowID = %q; want analyst→arch/L4/checkout", run.ArrowID)
	}
	if run.GridVersion != 42 {
		t.Errorf("GridVersion = %d; want 42", run.GridVersion)
	}
}
