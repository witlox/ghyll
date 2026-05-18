package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRouteArrow_EmptyClausesReturnsRoutedFastTier(t *testing.T) {
	req, err := RouteArrow(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Routed {
		t.Error("empty clauses must produce Routed=true (valid fast-tier verdict)")
	}
	if req.HasSensitive() {
		t.Error("empty clauses → no depth-sensitive")
	}
	if req.MinTier != DepthRankNone {
		t.Errorf("MinTier = %d; want 0", req.MinTier)
	}
}

func TestRouteArrow_AllRobustReturnsRoutedFastTier(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C1", Concept: "lint-clean", DepthType: DepthTypeRobust},
		{ClauseID: "C2", Concept: "compiles", DepthType: DepthTypeRobust},
	}
	req, err := RouteArrow(clauses)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Routed {
		t.Error("all-robust must produce Routed=true")
	}
	if req.HasSensitive() {
		t.Error("all-robust must not flag HasSensitive")
	}
}

func TestRouteArrow_MaxAcrossDepthSensitive(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C1", Concept: "x", DepthType: DepthTypeRobust},
		{ClauseID: "C2", Concept: "y", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankMocked},
		{ClauseID: "C3", Concept: "z", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankRealistic},
	}
	req, err := RouteArrow(clauses)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Routed {
		t.Error("Routed must be true on success")
	}
	if !req.HasSensitive() {
		t.Error("HasSensitive should be true")
	}
	if req.MinTier != DepthRankRealistic {
		t.Errorf("MinTier = %d; want REALISTIC", req.MinTier)
	}
	if req.MaxDepthClauseID != "C3" {
		t.Errorf("MaxDepthClauseID = %q; want C3", req.MaxDepthClauseID)
	}
}

func TestRouteArrow_RejectsUnknownDepthType(t *testing.T) {
	clauses := []Clause{{ClauseID: "C1", Concept: "x"}}
	_, err := RouteArrow(clauses)
	if !errors.Is(err, ErrClauseDepthTypeUnknown) {
		t.Errorf("unknown depth-type should error; got %v", err)
	}
}

func TestRouteArrow_RejectsSensitiveWithNoneTier(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C1", Concept: "x", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankNone},
	}
	_, err := RouteArrow(clauses)
	if !errors.Is(err, ErrClauseDepthRequirementInvalid) {
		t.Errorf("depth-sensitive with MinTier=NONE should error; got %v", err)
	}
}

func TestRouteArrow_RejectsOutOfRangeMinTier(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C1", Concept: "x", DepthType: DepthTypeSensitive, MinDepthTier: DepthRank(99)},
	}
	_, err := RouteArrow(clauses)
	if !errors.Is(err, ErrClauseDepthRequirementInvalid) {
		t.Errorf("out-of-range tier should error; got %v", err)
	}
}

func TestRouteArrow_ErrorReturnsUnroutedZero(t *testing.T) {
	// F2: error path must return Routed=false so the caller can't
	// confuse it with a valid fast-tier verdict.
	_, err := RouteArrow([]Clause{{ClauseID: "C1", Concept: "x"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Also confirm explicit non-success guard.
	req, _ := RouteArrow([]Clause{{ClauseID: "C1", Concept: "x"}})
	if req.Routed {
		t.Error("erroring RouteArrow must return Routed=false")
	}
}

func TestParseClauseDepthType(t *testing.T) {
	cases := map[string]ClauseDepthType{
		"depth-robust":    DepthTypeRobust,
		"Depth-Robust":    DepthTypeRobust,
		"  depth-robust ": DepthTypeRobust,
		"depth_sensitive": DepthTypeSensitive,
		"depth-sensitive": DepthTypeSensitive,
	}
	for in, want := range cases {
		got, err := ParseClauseDepthType(in)
		if err != nil {
			t.Errorf("ParseClauseDepthType(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseClauseDepthType(%q) = %v; want %v", in, got, want)
		}
	}
	if _, err := ParseClauseDepthType("nonsense"); err == nil {
		t.Error("unknown depth-type should error")
	}
}

func TestIsKnownDepthType_MirrorsParser(t *testing.T) {
	// F4: parser-accepted forms must validate via IsKnownDepthType.
	for _, s := range []string{"depth-robust", "Depth-Robust", "DEPTH_SENSITIVE", "depth_sensitive"} {
		if !IsKnownDepthType(ClauseDepthType(s)) {
			t.Errorf("IsKnownDepthType(%q) should be true (parser accepts it)", s)
		}
	}
	if IsKnownDepthType("bogus") {
		t.Error("bogus should not validate")
	}
}

func TestRoutingRequirement_Validate(t *testing.T) {
	good := RoutingRequirement{Routed: true, MinTier: DepthRankMocked, MaxDepthClauseID: "C1"}
	if err := good.Validate(); err != nil {
		t.Errorf("good req should validate: %v", err)
	}
	bad := RoutingRequirement{Routed: true, MinTier: DepthRank(99)}
	if err := bad.Validate(); err == nil {
		t.Error("out-of-range MinTier should fail")
	}
	// Unrouted-with-data is malformed.
	bad2 := RoutingRequirement{Routed: false, MinTier: DepthRankMocked}
	if err := bad2.Validate(); err == nil {
		t.Error("unrouted with non-zero MinTier should fail")
	}
	// Zero value validates as the legitimate "no decision" state.
	zero := RoutingRequirement{}
	if err := zero.Validate(); err != nil {
		t.Errorf("zero value should validate: %v", err)
	}
}

func TestRouteArrow_MaxClauseIDIsHighestTierClause(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C-low", Concept: "x", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankShallow},
		{ClauseID: "C-high", Concept: "y", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankRealistic},
		{ClauseID: "C-mid", Concept: "z", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankMocked},
	}
	req, _ := RouteArrow(clauses)
	if req.MaxDepthClauseID != "C-high" {
		t.Errorf("MaxDepthClauseID = %q; want C-high", req.MaxDepthClauseID)
	}
}

func TestRouteArrow_TieBreakFirstEncountered(t *testing.T) {
	// F5: deterministic tie-break — first depth-sensitive at the
	// maximum tier wins.
	clauses := []Clause{
		{ClauseID: "C-first", Concept: "x", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankRealistic},
		{ClauseID: "C-second", Concept: "y", DepthType: DepthTypeSensitive, MinDepthTier: DepthRankRealistic},
	}
	req, _ := RouteArrow(clauses)
	if req.MaxDepthClauseID != "C-first" {
		t.Errorf("tie-break should pick first; got %q", req.MaxDepthClauseID)
	}
}

func TestValidateClauseDepthDeclaration_ErrorIncludesClauseDescriptor(t *testing.T) {
	// F6: error messages should include a triage-friendly clause
	// label even when ClauseID is empty.
	c := Clause{Concept: "lint-clean"} // DepthType unset, ClauseID empty
	err := ValidateClauseDepthDeclaration(c)
	if err == nil {
		t.Fatal("unset DepthType should error")
	}
	if !strings.Contains(err.Error(), "lint-clean") {
		t.Errorf("error should fall back to Concept for triage; got %v", err)
	}
}

func TestEvaluate_DepthBelowRequiredShortCircuits(t *testing.T) {
	// F1 + F8 integration test: a depth-sensitive clause whose
	// MinDepthTier exceeds the runner's actualTier short-circuits
	// to Unevaluated with Reason=depth-below-required.
	reg := NewRegistry()
	called := false
	_ = reg.Register("test", func(_ context.Context, _ Clause) (*Result, error) {
		called = true
		return &Result{Pass: true}, nil
	})
	r := NewRunner(reg).WithActualTier(DepthRankShallow)
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{
		Concept:      "test",
		DepthType:    DepthTypeSensitive,
		MinDepthTier: DepthRankRealistic, // 3 > Shallow (1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("evaluator should NOT be invoked when depth-below-required")
	}
	if run.EndStatus != StatusUnevaluated {
		t.Errorf("EndStatus = %v; want Unevaluated", run.EndStatus)
	}
	if run.Result == nil || run.Result.Reason != string(ReasonDepthBelowRequired) {
		t.Errorf("Result.Reason = %v; want %s", run.Result, ReasonDepthBelowRequired)
	}
}

func TestEvaluate_DepthMetRunsEvaluator(t *testing.T) {
	reg := NewRegistry()
	called := false
	_ = reg.Register("test", func(_ context.Context, _ Clause) (*Result, error) {
		called = true
		return &Result{Pass: true}, nil
	})
	r := NewRunner(reg).WithActualTier(DepthRankRealistic)
	_, err := r.Evaluate(context.Background(), "C1", "P1", Clause{
		Concept:      "test",
		DepthType:    DepthTypeSensitive,
		MinDepthTier: DepthRankMocked,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("evaluator should be invoked when actualTier >= MinDepthTier")
	}
}

func TestEvaluate_LegacyPathSkipsDepthCheck(t *testing.T) {
	// WithActualTier not called → legacy callers behave as before
	// (no depth enforcement).
	reg := NewRegistry()
	called := false
	_ = reg.Register("test", func(_ context.Context, _ Clause) (*Result, error) {
		called = true
		return &Result{Pass: true}, nil
	})
	r := NewRunner(reg)
	_, err := r.Evaluate(context.Background(), "C1", "P1", Clause{
		Concept:      "test",
		DepthType:    DepthTypeSensitive,
		MinDepthTier: DepthRankRealistic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("legacy runner (no actualTier) should NOT short-circuit")
	}
}

func TestIsKnownUnevaluatedReason(t *testing.T) {
	for _, r := range []UnevaluatedReason{
		ReasonDepthBelowRequired,
		ReasonNoRuleSelectableHints,
		ReasonProducerNoResponse,
	} {
		if !IsKnownUnevaluatedReason(r) {
			t.Errorf("known reason %q should validate", r)
		}
	}
	if IsKnownUnevaluatedReason("bogus") {
		t.Error("bogus should not validate")
	}
}
