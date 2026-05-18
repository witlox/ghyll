package runner

import (
	"context"
	"errors"
	"strings"
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
	_, ok, err := DetectUndeclared(g, sampleTransition("A1"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("declared arrow should NOT trigger suspension")
	}
}

func TestDetectUndeclared_UndeclaredReturnsTrue(t *testing.T) {
	g := NewGrid()
	susp, ok, err := DetectUndeclared(g, sampleTransition("A-new"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("undeclared arrow should trigger suspension")
	}
	if !susp.AttestationRequired {
		t.Error("suspension must require attestation per §12.2")
	}
}

func TestDetectUndeclared_NilGridReturnsError(t *testing.T) {
	_, _, err := DetectUndeclared(nil, sampleTransition("A1"))
	if err == nil {
		t.Error("nil grid should error (F6)")
	}
}

func TestDetectUndeclared_MalformedTransitionReturnsError(t *testing.T) {
	// F6: malformed Transition must surface an error, not silently
	// return ok=false.
	g := NewGrid()
	_, _, err := DetectUndeclared(g, Transition{ArrowID: ""})
	if err == nil {
		t.Error("malformed transition should error")
	}
}

func TestDetectUndeclared_NormalizesIDs(t *testing.T) {
	// F7: Suspension.Transition fields should be trimmed.
	g := NewGrid()
	susp, ok, err := DetectUndeclared(g, Transition{
		ArrowID:    "  A-new  ",
		SourceRole: " analyst ",
		TargetRole: " architect ",
		Stratum:    " L4 ",
		Context:    " checkout ",
	})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if susp.Transition.ArrowID != "A-new" {
		t.Errorf("ArrowID = %q; want A-new", susp.Transition.ArrowID)
	}
	if susp.Transition.SourceRole != "analyst" {
		t.Errorf("SourceRole = %q", susp.Transition.SourceRole)
	}
}

func TestTransition_ValidateRejectsBlankStratumContext(t *testing.T) {
	good := sampleTransition("A1")
	cases := []struct {
		name string
		mut  func(*Transition)
	}{
		{"empty Stratum", func(t *Transition) { t.Stratum = "" }},
		{"whitespace Stratum", func(t *Transition) { t.Stratum = "   " }},
		{"empty Context", func(t *Transition) { t.Context = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx := good
			c.mut(&tx)
			if err := tx.Validate(); err == nil {
				t.Errorf("%s should fail", c.name)
			}
		})
	}
}

func TestTransition_ValidateRejectsOversizedUpstreamRef(t *testing.T) {
	tx := sampleTransition("A1")
	tx.UpstreamArtifactRef = strings.Repeat("a", maxUpstreamRefLen+1)
	if err := tx.Validate(); err == nil {
		t.Error("oversized UpstreamArtifactRef should fail")
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
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
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

func TestResolveOnTheSpot_SourceRoleSelfCertRefused(t *testing.T) {
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "analyst"},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrSelfCertification) {
		t.Errorf("source self-cert should be refused; got %v", err)
	}
}

func TestResolveOnTheSpot_TargetRoleSelfCertRefused(t *testing.T) {
	// F5: target role is also forbidden (conservative §12.2).
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "architect"},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrSelfCertification) {
		t.Errorf("target self-cert should be refused; got %v", err)
	}
}

func TestResolveOnTheSpot_SelfCertCaseInsensitive(t *testing.T) {
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
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
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{},
		defaultDefiner("A-new"),
	)
	if !errors.Is(err, ErrAttestationRoleEmpty) {
		t.Errorf("empty attestation role should error; got %v", err)
	}
}

func TestResolveOnTheSpot_AttestationReasonOversized(t *testing.T) {
	// F8: cap on operator-supplied free text.
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{
			AttestedByRole: "operator",
			Reason:         strings.Repeat("x", maxAttestationReasonLen+1),
		},
		defaultDefiner("A-new"),
	)
	if err == nil {
		t.Error("oversized reason should error")
	}
}

func TestResolveOnTheSpot_DefinerIDMismatch(t *testing.T) {
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
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
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		return ArrowDefinition{
			ID:         "A-new",
			SourceRole: "WRONG",
			TargetRole: "architect",
			Stratum:    "L4",
			Context:    "checkout",
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

func TestResolveOnTheSpot_DefinerInvalidDefinition(t *testing.T) {
	// F11: definer produces an invalid ArrowDefinition (e.g., no
	// clauses). The error is wrapped in ErrDefinerProducedInvalid.
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		return ArrowDefinition{
			ID:         "A-new",
			SourceRole: "analyst",
			TargetRole: "architect",
			Stratum:    "L4",
			Context:    "checkout",
			Clauses:    nil, // invalid
		}, nil
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, definer)
	if !errors.Is(err, ErrDefinerProducedInvalid) {
		t.Errorf("invalid definition should wrap ErrDefinerProducedInvalid; got %v", err)
	}
}

func TestResolveOnTheSpot_DefinerErrorPropagates(t *testing.T) {
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
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
	// F13: assert against the real sentinel ErrDefinerPanicked.
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		panic("LLM ate stack")
	}
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, definer,
	)
	if !errors.Is(err, ErrDefinerPanicked) {
		t.Errorf("panic should be wrapped in ErrDefinerPanicked; got %v", err)
	}
}

func TestResolveOnTheSpot_NilDefiner(t *testing.T) {
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	_, err := ResolveOnTheSpot(context.Background(), g, susp,
		Attestation{AttestedByRole: "operator"}, nil)
	if err == nil {
		t.Error("nil definer should error")
	}
}

func TestResolveOnTheSpot_NilGrid(t *testing.T) {
	susp, _, _ := DetectUndeclared(NewGrid(), sampleTransition("A-new"))
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

func TestResolveOnTheSpot_ContextCancelledUpfront(t *testing.T) {
	// F9: pre-call ctx.Err() check prevents wasted definer work.
	g := NewGrid()
	susp, _, _ := DetectUndeclared(g, sampleTransition("A-new"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	definer := func(_ context.Context, _ Suspension) (ArrowDefinition, error) {
		called = true
		return defaultDefiner("A-new")(context.Background(), susp)
	}
	_, err := ResolveOnTheSpot(ctx, g, susp,
		Attestation{AttestedByRole: "operator"}, definer)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled; got %v", err)
	}
	if called {
		t.Error("definer should NOT be invoked with cancelled ctx")
	}
}
