package runner

import (
	"context"
	"errors"
	"testing"
)

func sampleTransition(arrowID string) Transition {
	return Transition{
		ArrowID:    arrowID,
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L4",
		Context:    "checkout",
	}
}

func TestDetectUndeclared_DeclaredArrowReturnsFalse(t *testing.T) {
	g := NewGrid()
	_, _ = g.Append(sampleArrow("A1"))
	_, ok := DetectUndeclared(g, sampleTransition("A1"))
	if ok {
		t.Error("declared arrow should NOT trigger suspension")
	}
}

func TestDetectUndeclared_UndeclaredReturnsTrue(t *testing.T) {
	g := NewGrid()
	susp, ok := DetectUndeclared(g, sampleTransition("A-new"))
	if !ok {
		t.Fatal("undeclared arrow should trigger suspension")
	}
	if !susp.AttestationRequired {
		t.Error("suspension must require attestation per §12.2")
	}
	if susp.Transition.ArrowID != "A-new" {
		t.Errorf("ArrowID = %q", susp.Transition.ArrowID)
	}
}

func TestDetectUndeclared_NilGridReturnsFalse(t *testing.T) {
	_, ok := DetectUndeclared(nil, sampleTransition("A1"))
	if ok {
		t.Error("nil grid should return ok=false")
	}
}

func TestDetectUndeclared_MalformedTransitionReturnsFalse(t *testing.T) {
	g := NewGrid()
	_, ok := DetectUndeclared(g, Transition{ArrowID: ""})
	if ok {
		t.Error("malformed transition should return ok=false")
	}
}

func defaultDefiner(arrowID string) DefinerFn {
	return func(_ context.Context, susp Suspension) (ArrowDefinition, error) {
		return ArrowDefinition{
			ID:         arrowID,
			SourceRole: susp.Transition.SourceRole,
			TargetRole: susp.Transition.TargetRole,
			Stratum:    susp.Transition.Stratum,
			Context:    susp.Transition.Context,
			Clauses:    []Clause{{Concept: "lint-clean", ClauseID: "C1"}},
		}, nil
	}
}

func TestResolveOnTheSpot_SuccessAppendsAndBumpsCounter(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	v, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator", AttestedByOpID: "op-1"},
		defaultDefiner("A-new"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Errorf("version = %d; want 1", v)
	}
	if !g.Has("A-new") {
		t.Error("arrow should be in grid after resolve")
	}
	if g.OnTheSpotInterruptions() != 1 {
		t.Errorf("interruptions = %d; want 1", g.OnTheSpotInterruptions())
	}
}

func TestResolveOnTheSpot_SelfCertificationRefused(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	// SourceRole = "analyst" — operator attempts to certify as analyst.
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "analyst"},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrSelfCertification) {
		t.Errorf("self-cert should be refused; got %v", err)
	}
}

func TestResolveOnTheSpot_SelfCertificationCaseInsensitive(t *testing.T) {
	// "Analyst" should also be refused (the producer role match
	// must be case-insensitive — operators may capitalize).
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "Analyst"},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrSelfCertification) {
		t.Errorf("case-insensitive self-cert should be refused; got %v", err)
	}
}

func TestResolveOnTheSpot_AttestationRoleRequired(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrAttestationRoleEmpty) {
		t.Errorf("empty attestation role should error; got %v", err)
	}
}

func TestResolveOnTheSpot_DefinerIDMismatch(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	// Definer returns a different arrowID — must be rejected.
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"},
		defaultDefiner("A-different"),
	)
	if !errors.Is(err, ErrOnTheSpotMismatch) {
		t.Errorf("ID mismatch should error; got %v", err)
	}
}

func TestResolveOnTheSpot_DefinerRoleMismatch(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		return ArrowDefinition{
			ID:         "A-new",
			SourceRole: "WRONG", // not analyst
			TargetRole: "architect",
			Clauses:    []Clause{{Concept: "lint-clean"}},
		}, nil
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, definer,
	)
	if !errors.Is(err, ErrOnTheSpotMismatch) {
		t.Errorf("source-role mismatch should error; got %v", err)
	}
}

func TestResolveOnTheSpot_DefinerErrorPropagates(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		return ArrowDefinition{}, errors.New("LLM blew up")
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, definer,
	)
	if err == nil {
		t.Error("definer error should propagate")
	}
}

func TestResolveOnTheSpot_DefinerPanicRecovered(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		panic("LLM ate stack")
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, definer,
	)
	if err == nil || !errors.Is(err, errPanicSentinel) {
		// We expect a non-nil error mentioning the panic; the
		// errPanicSentinel check is a soft test (the wrap doesn't
		// use a sentinel error type).
		if err == nil {
			t.Error("panicking definer should produce an error")
		}
	}
}

// Test-only sentinel — never matched, just keeps the compile-time
// check tidy.
var errPanicSentinel = errors.New("not-used")

func TestResolveOnTheSpot_NilDefiner(t *testing.T) {
	g := NewGrid()
	susp, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, nil)
	if err == nil {
		t.Error("nil definer should error")
	}
}

func TestResolveOnTheSpot_NilGrid(t *testing.T) {
	susp, _ := DetectUndeclared(NewGrid(), sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), nil, susp,
		Attestation{AttestedByRole: "operator"},
		defaultDefiner("A-new"))
	if err == nil {
		t.Error("nil grid should error")
	}
}

func TestResolveOnTheSpot_DuplicateAppendRefused(t *testing.T) {
	g := NewGrid()
	_, _ = g.Append(sampleArrow("A-new"))
	susp := Suspension{
		Transition:          sampleTransition("A-new"),
		AttestationRequired: true,
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrArrowAlreadyDeclared) {
		t.Errorf("expected ErrArrowAlreadyDeclared; got %v", err)
	}
}
