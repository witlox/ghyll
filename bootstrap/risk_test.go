package bootstrap

import (
	"errors"
	"strings"
	"testing"
)

func TestRiskAssessment_Evaluate(t *testing.T) {
	cases := []struct {
		label string
		risk  RiskAssessment
		want  Recommendation
	}{
		// Refuse: every signal weak.
		{
			"all low",
			RiskAssessment{BoundedContextCount: 0, CrossContextSeams: 0, NovelArchitecture: false, CorrectnessCritical: false},
			RecommendationRefuse,
		},
		{
			"single context, nothing else",
			RiskAssessment{BoundedContextCount: 1, CrossContextSeams: 0, NovelArchitecture: false, CorrectnessCritical: false},
			RecommendationRefuse,
		},
		// Proceed: any single strong signal.
		{
			"two contexts",
			RiskAssessment{BoundedContextCount: 2, CrossContextSeams: 0, NovelArchitecture: false, CorrectnessCritical: false},
			RecommendationProceed,
		},
		{
			"one seam",
			RiskAssessment{BoundedContextCount: 1, CrossContextSeams: 1, NovelArchitecture: false, CorrectnessCritical: false},
			RecommendationProceed,
		},
		{
			"novel architecture",
			RiskAssessment{BoundedContextCount: 1, CrossContextSeams: 0, NovelArchitecture: true, CorrectnessCritical: false},
			RecommendationProceed,
		},
		{
			"correctness-critical",
			RiskAssessment{BoundedContextCount: 1, CrossContextSeams: 0, NovelArchitecture: false, CorrectnessCritical: true},
			RecommendationProceed,
		},
		// High-risk profile from scenario 77.
		{
			"high-risk: 4 contexts, 6 seams, critical",
			RiskAssessment{BoundedContextCount: 4, CrossContextSeams: 6, NovelArchitecture: false, CorrectnessCritical: true},
			RecommendationProceed,
		},
		// Low-risk profile from scenario 66.
		{
			"low-risk table",
			RiskAssessment{BoundedContextCount: 1, CrossContextSeams: 0, NovelArchitecture: false, CorrectnessCritical: false},
			RecommendationRefuse,
		},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			got := c.risk.Evaluate()
			if got != c.want {
				t.Errorf("Evaluate() = %v; want %v", got, c.want)
			}
		})
	}
}

func TestRecommendationString(t *testing.T) {
	if RecommendationProceed.String() != "proceed" {
		t.Errorf("Proceed.String() = %q; want proceed", RecommendationProceed.String())
	}
	if RecommendationRefuse.String() != "refuse" {
		t.Errorf("Refuse.String() = %q; want refuse", RecommendationRefuse.String())
	}
}

func TestRationale_RefusalMentionsCorrectness(t *testing.T) {
	r := RiskAssessment{} // all low-stakes
	text := r.Rationale()
	if !strings.Contains(text, "ghyll is wrong for") && !strings.Contains(text, "wrong for") {
		t.Errorf("refusal rationale should explain ghyll is wrong for this; got %q", text)
	}
}

func TestRationale_ProceedListsStrongSignals(t *testing.T) {
	r := RiskAssessment{
		BoundedContextCount: 4,
		CrossContextSeams:   6,
		CorrectnessCritical: true,
	}
	text := r.Rationale()
	for _, want := range []string{"4 bounded contexts", "6 cross-context seams", "correctness-critical"} {
		if !strings.Contains(text, want) {
			t.Errorf("proceed rationale should mention %q; got %q", want, text)
		}
	}
}

func TestProposeRefusal_RecordsRefusalWhenLowRisk(t *testing.T) {
	p := &ProjectProfile{}
	rec, err := p.ProposeRefusal(RiskAssessment{})
	if err != nil {
		t.Fatalf("ProposeRefusal: %v", err)
	}
	if rec != RecommendationRefuse {
		t.Errorf("got %v; want RecommendationRefuse", rec)
	}
	if !p.RefusalProposed() {
		t.Error("RefusalProposed should be true")
	}
	if p.RefusalAccepted() {
		t.Error("RefusalAccepted should be false until AcceptRefusal called")
	}
}

func TestProposeRefusal_NoRefusalWhenHighRisk(t *testing.T) {
	p := &ProjectProfile{}
	rec, err := p.ProposeRefusal(RiskAssessment{CorrectnessCritical: true})
	if err != nil {
		t.Fatalf("ProposeRefusal: %v", err)
	}
	if rec != RecommendationProceed {
		t.Errorf("got %v; want RecommendationProceed", rec)
	}
	if p.RefusalProposed() {
		t.Error("RefusalProposed should be false on proceed")
	}
	if p.Refusal() != nil {
		t.Error("Refusal() should be nil on proceed")
	}
}

func TestAcceptRefusal_FlipsAcceptedFlag(t *testing.T) {
	p := &ProjectProfile{}
	if _, err := p.ProposeRefusal(RiskAssessment{}); err != nil {
		t.Fatal(err)
	}
	if err := p.AcceptRefusal(); err != nil {
		t.Fatalf("AcceptRefusal: %v", err)
	}
	if !p.RefusalAccepted() {
		t.Error("RefusalAccepted should be true after Accept")
	}
}

func TestAcceptRefusal_NoProposalRejects(t *testing.T) {
	p := &ProjectProfile{}
	err := p.AcceptRefusal()
	if !errors.Is(err, ErrRefusalNotProposed) {
		t.Errorf("AcceptRefusal without proposal: got %v; want ErrRefusalNotProposed", err)
	}
}

func TestOverrideRefusal_RequiresResidue(t *testing.T) {
	p := &ProjectProfile{}
	if _, err := p.ProposeRefusal(RiskAssessment{}); err != nil {
		t.Fatal(err)
	}
	err := p.OverrideRefusal("")
	if !errors.Is(err, ErrRefusalOverrideEmpty) {
		t.Errorf("empty override: got %v; want ErrRefusalOverrideEmpty", err)
	}
	err = p.OverrideRefusal("   ")
	if !errors.Is(err, ErrRefusalOverrideEmpty) {
		t.Errorf("whitespace override: got %v; want ErrRefusalOverrideEmpty", err)
	}
	if err := p.OverrideRefusal("we know it's low-risk but want machinery for training"); err != nil {
		t.Errorf("non-empty override should succeed; got %v", err)
	}
	if p.RefusalAccepted() {
		t.Error("RefusalAccepted should be false after override")
	}
	if got := p.Refusal().OverrideResidue; got == "" {
		t.Error("OverrideResidue should be set after override")
	}
}

func TestProposeRefusal_AlreadyResolved(t *testing.T) {
	p := &ProjectProfile{}
	if _, err := p.ProposeRefusal(RiskAssessment{}); err != nil {
		t.Fatal(err)
	}
	_, err := p.ProposeRefusal(RiskAssessment{})
	if !errors.Is(err, ErrRefusalAlreadyResolved) {
		t.Errorf("second ProposeRefusal: got %v; want ErrRefusalAlreadyResolved", err)
	}
}

func TestProposeRefusal_NilReceiver(t *testing.T) {
	var p *ProjectProfile
	if _, err := p.ProposeRefusal(RiskAssessment{}); err == nil {
		t.Error("nil receiver should error")
	}
	if err := p.AcceptRefusal(); err == nil {
		t.Error("nil AcceptRefusal should error")
	}
	if err := p.OverrideRefusal("x"); err == nil {
		t.Error("nil OverrideRefusal should error")
	}
	if r := p.Refusal(); r != nil {
		t.Error("nil Refusal() should be nil")
	}
}
