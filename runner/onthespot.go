package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// On-the-spot arrow creation. Per gates.md §12: when ghyll hits a
// transition with no declared arrow, the runner suspends and an
// LLM-backed definer hook produces a new ArrowDefinition. The
// definition is gated by operator attestation (§12.2: producing
// role MAY NOT self-certify).
//
// Hardenings (validation-pass-6):
//   - F2: ArrowDefinition.Validate requires non-empty Stratum/Context.
//   - F1: Grid Append/Lookup deep-copy slices/maps.
//   - F5: self-cert check covers BOTH SourceRole and TargetRole
//     per ADR-009. The target role is a co-author of the on-the-spot
//     contract; allowing it to attest leaves a self-cert channel.
//   - F6: DetectUndeclared returns an explicit error on malformed
//     Transition instead of silently swallowing.
//   - F7: arrow / role IDs trimmed in Suspension so identity
//     compares are single-policy.
//   - F8: UpstreamArtifactRef and Attestation.Reason capped +
//     sanitized at entry.
//   - F9: ctx.Err() checked before and after definer call.
//   - F11: definer-produced invalid ArrowDefinition wrapped in
//     ErrDefinerProducedInvalid.

const (
	// maxUpstreamRefLen bounds the operator-supplied artifact ref.
	maxUpstreamRefLen = maxFreeTextLen

	// maxAttestationReasonLen bounds the operator-supplied reason.
	maxAttestationReasonLen = maxFreeTextLen
)

// Transition is the runner's lookup key for "which arrow should
// carry this hand-off."
type Transition struct {
	ArrowID             string
	SourceRole          string
	TargetRole          string
	Stratum             string
	Context             string
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
	if strings.TrimSpace(t.Stratum) == "" {
		return errors.New("transition-stratum-empty")
	}
	if strings.TrimSpace(t.Context) == "" {
		return errors.New("transition-context-empty")
	}
	if len(t.UpstreamArtifactRef) > maxUpstreamRefLen {
		return fmt.Errorf("transition-upstream-artifact-ref-too-long: %d bytes exceeds max %d",
			len(t.UpstreamArtifactRef), maxUpstreamRefLen)
	}
	return nil
}

// normalize returns a copy of t with ID + role fields trimmed and
// UpstreamArtifactRef sanitized. Called from DetectUndeclared so
// Suspension fields are in canonical form.
func (t Transition) normalize() Transition {
	out := t
	out.ArrowID = strings.TrimSpace(t.ArrowID)
	out.SourceRole = strings.TrimSpace(t.SourceRole)
	out.TargetRole = strings.TrimSpace(t.TargetRole)
	out.Stratum = strings.TrimSpace(t.Stratum)
	out.Context = strings.TrimSpace(t.Context)
	out.UpstreamArtifactRef = sanitizeOneLine(t.UpstreamArtifactRef)
	return out
}

// Suspension carries the on-the-spot context.
type Suspension struct {
	Transition          Transition
	AttestationRequired bool
}

// DetectUndeclared returns (suspension, ok=true, nil) when the grid
// has no declared arrow for the transition. ok=false, nil means the
// arrow is declared. A non-nil error indicates a malformed
// Transition; the caller MUST handle the error (validation-pass-6 F6).
func DetectUndeclared(g *Grid, t Transition) (Suspension, bool, error) {
	if g == nil {
		return Suspension{}, false, errors.New("DetectUndeclared: nil grid")
	}
	if err := t.Validate(); err != nil {
		return Suspension{}, false, err
	}
	if g.Has(t.ArrowID) {
		return Suspension{}, false, nil
	}
	return Suspension{
		Transition:          t.normalize(),
		AttestationRequired: true,
	}, true, nil
}

// DefinerFn is the hook signature for the on-the-spot definition.
type DefinerFn func(ctx context.Context, susp Suspension) (ArrowDefinition, error)

// Attestation records the operator's attestation of an on-the-spot
// definition. Both SourceRole AND TargetRole are forbidden from
// self-cert (validation-pass-6 F5 conservative reading of §12.2).
type Attestation struct {
	AttestedByRole string
	AttestedByOpID string
	Reason         string
}

// On-the-spot errors.
var (
	ErrSelfCertification      = errors.New("on-the-spot-self-certification-refused")
	ErrAttestationRoleEmpty   = errors.New("attestation-role-empty")
	ErrOnTheSpotMismatch      = errors.New("on-the-spot-definition-mismatch")
	ErrDefinerProducedInvalid = errors.New("definer-produced-invalid-definition")
)

// ResolveOnTheSpot drives the on-the-spot definition flow. Returns
// the new grid version on success; 0 on error.
//
// The flow:
//  1. ctx.Err() check (validation-pass-6 F9).
//  2. Validate attestation: role non-empty, NOT == SourceRole and
//     NOT == TargetRole (F5 conservative reading of §12.2).
//  3. Invoke definer hook (with panic recovery).
//  4. Cross-check the returned definition matches the suspension's
//     identity fields.
//  5. Wrap a definer-produced-invalid definition error in
//     ErrDefinerProducedInvalid (F11).
//  6. Append to grid.
//
// On ErrArrowAlreadyDeclared (concurrent race): the caller SHOULD
// Lookup the now-declared arrow and proceed (validation-pass-6 F14).
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := susp.Transition.Validate(); err != nil {
		return 0, fmt.Errorf("ResolveOnTheSpot: %w", err)
	}
	if len(att.Reason) > maxAttestationReasonLen {
		return 0, fmt.Errorf("attestation-reason-too-long: %d bytes exceeds max %d",
			len(att.Reason), maxAttestationReasonLen)
	}
	if strings.TrimSpace(att.AttestedByRole) == "" {
		return 0, ErrAttestationRoleEmpty
	}
	// §12.2 (ADR-009): neither source nor target role may attest the
	// on-the-spot arrow definition. Both are co-authors of the
	// contract.
	role := strings.TrimSpace(att.AttestedByRole)
	if strings.EqualFold(role, susp.Transition.SourceRole) {
		return 0, fmt.Errorf("%w: producer role %q cannot attest its own definition",
			ErrSelfCertification, susp.Transition.SourceRole)
	}
	if strings.EqualFold(role, susp.Transition.TargetRole) {
		return 0, fmt.Errorf("%w: target role %q cannot attest the contract it consumes",
			ErrSelfCertification, susp.Transition.TargetRole)
	}

	def, err := safeInvokeDefiner(ctx, definer, susp)
	if err != nil {
		return 0, fmt.Errorf("definer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// Identity cross-check (compare trimmed ID; the suspension is
	// already normalized — F7).
	defID := strings.TrimSpace(def.ID)
	if defID != susp.Transition.ArrowID {
		return 0, fmt.Errorf("%w: ID %q != suspension %q",
			ErrOnTheSpotMismatch, def.ID, susp.Transition.ArrowID)
	}
	if !strings.EqualFold(strings.TrimSpace(def.SourceRole), susp.Transition.SourceRole) {
		return 0, fmt.Errorf("%w: source-role %q != suspension %q",
			ErrOnTheSpotMismatch, def.SourceRole, susp.Transition.SourceRole)
	}
	if !strings.EqualFold(strings.TrimSpace(def.TargetRole), susp.Transition.TargetRole) {
		return 0, fmt.Errorf("%w: target-role %q != suspension %q",
			ErrOnTheSpotMismatch, def.TargetRole, susp.Transition.TargetRole)
	}

	// F11: pre-validate so a definer-produced invalid definition is
	// distinguishable from a grid-internal error.
	if err := def.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrDefinerProducedInvalid, err)
	}

	return g.AppendOnTheSpot(def)
}

// safeInvokeDefiner wraps the definer hook with panic recovery.
// Returns a wrapped error so tests can match via errors.Is.
func safeInvokeDefiner(ctx context.Context, fn DefinerFn, susp Suspension) (def ArrowDefinition, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrDefinerPanicked, r)
		}
	}()
	return fn(ctx, susp)
}

// ErrDefinerPanicked is returned (wrapped) when the definer hook
// panics. Tests can match via errors.Is.
var ErrDefinerPanicked = errors.New("definer-hook-panicked")
