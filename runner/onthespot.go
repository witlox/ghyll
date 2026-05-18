package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// On-the-spot arrow creation. Per gates.md §12: when ghyll hits a
// transition with no declared arrow, it suspends and triggers the
// on-the-spot definition flow.
//
// Phase-6 mechanism (runner-layer):
//
//  1. DetectUndeclared(grid, transition) returns a Suspension when
//     the transition's arrow isn't in the grid.
//  2. ResolveOnTheSpot drives the definition: it invokes the
//     DefinerFn hook (LLM-backed in production), enforces that the
//     producing role does not self-certify, and writes the new
//     ArrowDefinition back to the grid as version N+1.
//  3. The interruption is logged via Grid.AppendOnTheSpot which
//     bumps OnTheSpotInterruptions per §12.5.

// Transition is the runner's lookup key for "which arrow should
// carry this hand-off." Constructed by the engine when a producer
// finishes one role's work and the next role's role-pair traversal
// is about to begin.
type Transition struct {
	// ArrowID is the lookup key into the Grid.
	ArrowID string

	// SourceRole / TargetRole are the role-pair endpoints. Used to
	// stamp the synthesized ArrowDefinition AND to detect
	// producer-self-certification (the producer role is the SOURCE
	// role; the operator-attesting role MUST NOT match).
	SourceRole string
	TargetRole string

	// Stratum + Context are propagated into the synthesized arrow.
	Stratum string
	Context string

	// UpstreamArtifactRef points at the upstream artifact the
	// new arrow's clauses would gate. Operator-supplied free-text;
	// no semantics enforced by the runner.
	UpstreamArtifactRef string
}

// Validate checks transition fields.
func (t Transition) Validate() error {
	if strings.TrimSpace(t.ArrowID) == "" {
		return errors.New("transition-arrow-id-empty")
	}
	if strings.TrimSpace(t.SourceRole) == "" {
		return errors.New("transition-source-role-empty")
	}
	if strings.TrimSpace(t.TargetRole) == "" {
		return errors.New("transition-target-role-empty")
	}
	return nil
}

// Suspension carries the on-the-spot context. Returned by
// DetectUndeclared when no arrow is declared for the transition.
// The engine layer drives the operator-attestation step and the
// definition hook off this struct.
type Suspension struct {
	Transition Transition

	// AttestationRequired records the policy per gates.md §12.2:
	// the producing role MAY NOT self-certify. The attesting party
	// MUST be a different role from Transition.SourceRole. Set
	// true unconditionally — only the operator can clear it via
	// the post-attestation step.
	AttestationRequired bool
}

// DetectUndeclared returns (Suspension, ok=true) when the grid has
// no declared arrow for the transition. ok=false means the arrow is
// declared and traversal proceeds normally.
func DetectUndeclared(g *Grid, t Transition) (Suspension, bool) {
	if g == nil {
		return Suspension{}, false
	}
	if err := t.Validate(); err != nil {
		// Malformed transition is its own problem; the engine should
		// surface the error before calling. We return ok=false rather
		// than panic.
		return Suspension{}, false
	}
	if g.Has(t.ArrowID) {
		return Suspension{}, false
	}
	return Suspension{
		Transition:          t,
		AttestationRequired: true,
	}, true
}

// DefinerFn is the hook signature for the on-the-spot definition
// flow. The hook receives the suspension context and returns a
// fully-formed ArrowDefinition (clauses, requirements, etc.). In
// production this is LLM-backed at an escalated model tier per
// gates.md §12.3.
type DefinerFn func(ctx context.Context, susp Suspension) (ArrowDefinition, error)

// Attestation records the operator's attestation of an on-the-spot
// definition. The producing role MUST NOT self-certify
// (gates.md §12.2).
type Attestation struct {
	// AttestedByRole is the role that approved the new definition.
	// MUST differ from Transition.SourceRole.
	AttestedByRole string

	// AttestedByOpID is the operator-id (per D14) that performed
	// the attestation. Empty when attestation came from an
	// automated role-instance (test path only — production
	// attestation requires a human op-id).
	AttestedByOpID string

	// Reason is the operator-facing rationale (operator-supplied
	// free text). Sanitized at write time.
	Reason string
}

// On-the-spot errors.
var (
	// ErrSelfCertification — the producing role attempted to attest
	// its own on-the-spot definition. Per gates.md §12.2.
	ErrSelfCertification = errors.New("on-the-spot-self-certification-refused")

	// ErrAttestationRoleEmpty — the attestation didn't name a role.
	ErrAttestationRoleEmpty = errors.New("attestation-role-empty")

	// ErrOnTheSpotMismatch — the definer returned an arrow whose
	// fields don't match the suspension (different ID, source role,
	// etc.). Catches the case where an LLM hallucinates a different
	// arrow than the one the runner asked it to define.
	ErrOnTheSpotMismatch = errors.New("on-the-spot-definition-mismatch")
)

// ResolveOnTheSpot drives the on-the-spot definition flow. Returns
// the new grid version on success.
//
// The flow:
//  1. Validate attestation (role non-empty, NOT == SourceRole).
//  2. Invoke definer hook (with panic recovery).
//  3. Cross-check the returned definition matches the suspension's
//     identity fields.
//  4. Append the definition to the grid via Grid.AppendOnTheSpot.
//
// The hook is invoked with the suspension's ctx. The engine layer
// is responsible for escalating the model tier per §12.3 (this
// runner doesn't drive routing).
func ResolveOnTheSpot(
	ctx context.Context,
	g *Grid,
	susp Suspension,
	att Attestation,
	definer DefinerFn,
) (uint64, error) {
	if g == nil {
		return 0, errors.New("ResolveOnTheSpot: nil grid")
	}
	if definer == nil {
		return 0, errors.New("ResolveOnTheSpot: nil definer")
	}
	if err := susp.Transition.Validate(); err != nil {
		return 0, fmt.Errorf("ResolveOnTheSpot: %w", err)
	}
	if strings.TrimSpace(att.AttestedByRole) == "" {
		return 0, ErrAttestationRoleEmpty
	}
	// §12.2: producing role MAY NOT self-certify.
	if strings.EqualFold(att.AttestedByRole, susp.Transition.SourceRole) {
		return 0, fmt.Errorf("%w: producer role %q cannot attest its own definition",
			ErrSelfCertification, susp.Transition.SourceRole)
	}

	def, err := safeInvokeDefiner(ctx, definer, susp)
	if err != nil {
		return 0, fmt.Errorf("definer: %w", err)
	}

	// Identity cross-check: the definer is asked to define THIS
	// arrow, not some other. Catches the LLM-hallucinates-different-
	// arrow failure mode.
	if def.ID != susp.Transition.ArrowID {
		return 0, fmt.Errorf("%w: ID %q != suspension %q",
			ErrOnTheSpotMismatch, def.ID, susp.Transition.ArrowID)
	}
	if !strings.EqualFold(def.SourceRole, susp.Transition.SourceRole) {
		return 0, fmt.Errorf("%w: source-role %q != suspension %q",
			ErrOnTheSpotMismatch, def.SourceRole, susp.Transition.SourceRole)
	}
	if !strings.EqualFold(def.TargetRole, susp.Transition.TargetRole) {
		return 0, fmt.Errorf("%w: target-role %q != suspension %q",
			ErrOnTheSpotMismatch, def.TargetRole, susp.Transition.TargetRole)
	}

	return g.AppendOnTheSpot(def)
}

// safeInvokeDefiner wraps the definer hook with panic recovery.
// Same pattern as safeInvokeOpenSweep (adversarial.go).
func safeInvokeDefiner(ctx context.Context, fn DefinerFn, susp Suspension) (def ArrowDefinition, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("definer hook panicked: %v", r)
		}
	}()
	return fn(ctx, susp)
}
