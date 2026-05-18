package runner

import (
	"errors"
	"testing"
)

func TestRouteArrow_EmptyClausesReturnsNoneTier(t *testing.T) {
	req, err := RouteArrow(nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.AnyDepthSensitive {
		t.Error("empty clauses → no depth-sensitive")
	}
	if req.MinTier != DepthRankNone {
		t.Errorf("MinTier = %d; want 0", req.MinTier)
	}
}

func TestRouteArrow_AllRobustReturnsNoneTier(t *testing.T) {
	clauses := []Clause{
		{ClauseID: "C1", Concept: "lint-clean", DepthType: DepthTypeRobust},
		{ClauseID: "C2", Concept: "compiles", DepthType: DepthTypeRobust},
	}
	req, err := RouteArrow(clauses)
	if err != nil {
		t.Fatal(err)
	}
	if req.AnyDepthSensitive {
		t.Error("all-robust must not flag AnyDepthSensitive")
	}
	if req.MinTier != DepthRankNone {
		t.Errorf("MinTier = %d; want 0", req.MinTier)
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
	if !req.AnyDepthSensitive {
		t.Error("AnyDepthSensitive should be true")
	}
	if req.MinTier != DepthRankRealistic {
		t.Errorf("MinTier = %d; want REALISTIC", req.MinTier)
	}
	if req.MaxDepthClauseID != "C3" {
		t.Errorf("MaxDepthClauseID = %q; want C3", req.MaxDepthClauseID)
	}
}

func TestRouteArrow_RejectsUnknownDepthType(t *testing.T) {
	// gates.md §6: depth-type MUST NOT default. Unset/unknown
	// declarations are operator misconfiguration.
	clauses := []Clause{
		{ClauseID: "C1", Concept: "x"}, // DepthType unset
	}
	_, err := RouteArrow(clauses)
	if !errors.Is(err, ErrClauseDepthTypeUnknown) {
		t.Errorf("unknown depth-type should error; got %v", err)
	}
}

func TestRouteArrow_RejectsSensitiveWithNoneTier(t *testing.T) {
	// A depth-sensitive clause requesting NONE is inert (mirrors
	// validation-pass-5 F12 for Requirement).
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

func TestParseClauseDepthType(t *testing.T) {
	cases := map[string]ClauseDepthType{
		"depth-robust":    DepthTypeRobust,
		"Depth-Robust":    DepthTypeRobust,
		"  depth-robust ": DepthTypeRobust,
		"depth_sensitive": DepthTypeSensitive, // underscore normalization
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

func TestIsKnownDepthType(t *testing.T) {
	if !IsKnownDepthType(DepthTypeRobust) || !IsKnownDepthType(DepthTypeSensitive) {
		t.Error("known types should validate")
	}
	if IsKnownDepthType("bogus") {
		t.Error("bogus should not validate")
	}
}

func TestRoutingRequirement_Validate(t *testing.T) {
	good := RoutingRequirement{
		MinTier: DepthRankMocked, AnyDepthSensitive: true, MaxDepthClauseID: "C1",
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good req should validate: %v", err)
	}
	bad := RoutingRequirement{MinTier: DepthRank(99)}
	if err := bad.Validate(); err == nil {
		t.Error("out-of-range MinTier should fail")
	}
	bad2 := RoutingRequirement{MinTier: DepthRankMocked, AnyDepthSensitive: true}
	if err := bad2.Validate(); err == nil {
		t.Error("sensitive-without-clause-id should fail")
	}
}

func TestRouteArrow_MaxClauseIDIsHighestTierClause(t *testing.T) {
	// MaxDepthClauseID should track the clause that drove MinTier,
	// not just the FIRST depth-sensitive clause.
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
