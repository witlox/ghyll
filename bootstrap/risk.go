package bootstrap

import (
	"errors"
	"fmt"
	"strings"
)

// Refusal flow (init.feature 25, 66, 77).
//
// ghyll's stated position (CLAUDE.md): it is correct for novel
// architecture, correctness-critical systems, long-horizon projects.
// It is wrong for CRUD, migrations, glue code, rapid prototyping. The
// refusal flow is how that position is enforced at init: if every
// signal in the project profile says low-stakes, init proposes
// refusal rather than running the auto-propose loop. The operator
// may accept (ghyll exits) or override (with a residue note recording
// the override).

// Recommendation is init's verdict on whether the project should
// proceed through auto-propose or be refused.
type Recommendation int

const (
	// RecommendationProceed: at least one risk signal warrants ghyll's
	// machinery. Auto-propose runs.
	RecommendationProceed Recommendation = iota
	// RecommendationRefuse: every signal indicates a project ghyll is
	// wrong for. Operator may override but should not silently.
	RecommendationRefuse
)

// String returns a stable wire-form for the recommendation, used in
// attestation records and operator UI.
func (r Recommendation) String() string {
	switch r {
	case RecommendationProceed:
		return "proceed"
	case RecommendationRefuse:
		return "refuse"
	default:
		return "unknown"
	}
}

// RiskAssessment is the structured input to the refusal decision.
// Each field is an operator-attested or operator-declared fact — none
// is machine-derivable in v2 (the harness cannot inspect a project
// and decide whether the architecture is "novel"; that judgement
// belongs to the operator). Init collects these as part of sub-phase A
// and feeds them to Evaluate.
type RiskAssessment struct {
	// BoundedContextCount is the number of bounded contexts the
	// project will have. 0 or 1 indicates a single-context project
	// (no cross-context coordination overhead).
	BoundedContextCount int

	// CrossContextSeams is the operator's estimate of the number of
	// integration points between bounded contexts (queues, RPC
	// boundaries, shared databases, etc.). 0 means contexts run
	// independently and no integration test surface exists.
	CrossContextSeams int

	// NovelArchitecture is true when the project's structure is not a
	// well-trodden pattern (event sourcing on top of a graph database,
	// custom consensus protocol, etc.). False for plain CRUD over a
	// relational store, standard layered web apps, scripts.
	NovelArchitecture bool

	// CorrectnessCritical is true when a defect reaching production
	// carries non-trivial cost (financial systems, safety-critical,
	// medical, legal-compliance, etc.). False for prototypes, internal
	// tools where bugs are recoverable.
	CorrectnessCritical bool
}

// validate rejects RiskAssessment values that don't make sense:
// negative counts (validation-pass-2 F31). The harness has no way to
// have "minus two bounded contexts"; treating such input as a
// silent low-stakes signal misleads the rationale.
func (a RiskAssessment) validate() error {
	if a.BoundedContextCount < 0 {
		return fmt.Errorf("RiskAssessment: BoundedContextCount must be >= 0; got %d",
			a.BoundedContextCount)
	}
	if a.CrossContextSeams < 0 {
		return fmt.Errorf("RiskAssessment: CrossContextSeams must be >= 0; got %d",
			a.CrossContextSeams)
	}
	return nil
}

// Evaluate returns Refuse iff every signal says low-stakes
// (≤1 context AND 0 seams AND not novel AND not critical). A single
// strong signal in any axis flips the recommendation to Proceed.
//
// This is intentionally asymmetric: refusal requires unanimous
// low-stakes evidence. The asymmetry encodes the project's
// correctness-over-velocity position — when in doubt, ghyll runs.
func (a RiskAssessment) Evaluate() Recommendation {
	lowStakes := a.BoundedContextCount <= 1 &&
		a.CrossContextSeams == 0 &&
		!a.NovelArchitecture &&
		!a.CorrectnessCritical
	if lowStakes {
		return RecommendationRefuse
	}
	return RecommendationProceed
}

// Rationale returns a human-readable explanation for the
// recommendation. Includes which signals drove the decision so the
// operator can decide whether to accept or override informed.
func (a RiskAssessment) Rationale() string {
	if a.Evaluate() == RecommendationRefuse {
		return "Every signal indicates a project ghyll is wrong for: " +
			"single bounded context, no cross-context seams, no novel " +
			"architecture, and a defect reaching production is not " +
			"expensive. ghyll's correctness-via-friction model adds " +
			"more cost than it prevents on projects like this."
	}
	var reasons []string
	if a.BoundedContextCount >= 2 {
		reasons = append(reasons, fmt.Sprintf("%d bounded contexts", a.BoundedContextCount))
	}
	if a.CrossContextSeams >= 1 {
		reasons = append(reasons, fmt.Sprintf("%d cross-context seams", a.CrossContextSeams))
	}
	if a.NovelArchitecture {
		reasons = append(reasons, "novel architecture")
	}
	if a.CorrectnessCritical {
		reasons = append(reasons, "correctness-critical")
	}
	if len(reasons) == 0 {
		// Shouldn't happen given Evaluate's logic, but defended.
		return "Proceeding."
	}
	return "Proceeding: " + strings.Join(reasons, ", ") +
		" indicate(s) work ghyll's gate model is designed for."
}

// RefusalOutcome records the operator's response when init has
// proposed refusal. Captured on the ProjectProfile.
type RefusalOutcome struct {
	// Recommended is what init proposed. Always RecommendationRefuse
	// for a non-zero RefusalOutcome (Proceed never sets one).
	Recommended Recommendation
	// Accepted is true when the operator agreed with refusal (ghyll
	// exits); false when the operator overrode.
	Accepted bool
	// OverrideResidue is the non-empty rationale recorded when
	// Accepted is false. ADR-011 sub-phase B's residue-or-skip rule
	// extends to refusal overrides: no silent override.
	OverrideResidue string
}

// Refusal errors.
var (
	ErrRefusalNotProposed     = errors.New("refusal-not-proposed")
	ErrRefusalOverrideEmpty   = errors.New("refusal-override-residue-required")
	ErrRefusalAlreadyResolved = errors.New("refusal-already-resolved")
)

// ProposeRefusal records init's recommendation. Per validation-pass-2
// F54: NOT idempotent — a second call returns ErrRefusalAlreadyResolved
// regardless of whether the risk values match. On the error path,
// returns RecommendationRefuse (the actual recorded recommendation)
// not Proceed, so a caller that ignores the error still gets the
// safer answer.
//
// F31: rejects negative counts (BoundedContextCount, CrossContextSeams).
func (p *ProjectProfile) ProposeRefusal(risk RiskAssessment) (Recommendation, error) {
	if p == nil {
		return RecommendationProceed, errors.New("ProposeRefusal: nil ProjectProfile")
	}
	if err := risk.validate(); err != nil {
		return RecommendationProceed, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal != nil {
		// Return the existing recommendation so a caller that misses
		// the error doesn't proceed.
		return p.refusal.Recommended, fmt.Errorf("%w: previously recommended %s",
			ErrRefusalAlreadyResolved, p.refusal.Recommended)
	}
	p.risk = risk
	rec := risk.Evaluate()
	if rec == RecommendationRefuse {
		p.refusal = &RefusalOutcome{Recommended: RecommendationRefuse}
	}
	return rec, nil
}

// AcceptRefusal records that the operator accepted init's refusal
// recommendation. After AcceptRefusal, RefusalAccepted is true and
// the caller should terminate.
//
// Validation-pass-2 F10: refuses with ErrRefusalAlreadyResolved if
// the refusal has already been Accepted or Overridden — the state
// machine never silently flips a resolved verdict.
func (p *ProjectProfile) AcceptRefusal() error {
	if p == nil {
		return errors.New("AcceptRefusal: nil ProjectProfile")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal == nil {
		return ErrRefusalNotProposed
	}
	if p.refusal.Accepted || p.refusal.OverrideResidue != "" {
		return ErrRefusalAlreadyResolved
	}
	p.refusal.Accepted = true
	return nil
}

// OverrideRefusal records that the operator overrode init's refusal
// recommendation. The residue argument is the operator-supplied
// rationale; empty residue is refused so the override cannot be
// silent. Validation-pass-2 F10: refuses with ErrRefusalAlreadyResolved
// if Accept or Override has already landed.
func (p *ProjectProfile) OverrideRefusal(residue string) error {
	if p == nil {
		return errors.New("OverrideRefusal: nil ProjectProfile")
	}
	if strings.TrimSpace(residue) == "" {
		return ErrRefusalOverrideEmpty
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal == nil {
		return ErrRefusalNotProposed
	}
	if p.refusal.Accepted || p.refusal.OverrideResidue != "" {
		return ErrRefusalAlreadyResolved
	}
	p.refusal.OverrideResidue = residue
	return nil
}

// Refusal returns a copy of the refusal outcome, or nil if init has
// not proposed refusal.
func (p *ProjectProfile) Refusal() *RefusalOutcome {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal == nil {
		return nil
	}
	cp := *p.refusal
	return &cp
}

// RefusalProposed reports whether init has proposed refusal (i.e.,
// ProposeRefusal returned RecommendationRefuse). False both when
// ProposeRefusal hasn't been called and when it returned Proceed.
func (p *ProjectProfile) RefusalProposed() bool {
	return p != nil && p.refusal != nil
}

// RefusalAccepted reports whether the operator accepted init's
// refusal recommendation. False both when refusal wasn't proposed and
// when the operator overrode.
func (p *ProjectProfile) RefusalAccepted() bool {
	return p != nil && p.refusal != nil && p.refusal.Accepted
}
