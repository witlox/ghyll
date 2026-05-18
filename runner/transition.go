package runner

import (
	"errors"
	"fmt"
)

// Transition refusal. Per gates.md §7.2 + runner.md F-?: when a role
// attempts to enter a stratum that depends on an upstream arrow,
// the runner refuses if the upstream's derived status is not
// `complete` (and not invalidated). Refusal is structural, not
// advisory — there is no soft-refuse / warning mode (runner.md
// invariant 2 — total refusal).
//
// The refusal carries enough structured detail that tooling can
// render an actionable message (which arrow blocks, what status it
// holds, how many clauses are still pending) without parsing the
// error string.

// TransitionRefusalKind names the structural category of a
// refusal. Used as the `kind` field in TransitionRefusal errors so
// callers (UI, attestation log, tests) can switch on it without
// substring-matching error messages.
type TransitionRefusalKind string

const (
	// KindTransitionRefused: the upstream arrow's status is one of
	// {blocked, in-progress, unevaluated, provisional}. The
	// downstream role's transition must wait until the upstream
	// reaches `complete`.
	KindTransitionRefused TransitionRefusalKind = "transition-refused"

	// KindTransitionRefusedInvalidated: the upstream arrow was
	// invalidated by a grid amendment (D22). The downstream is
	// refused with the invalidating grid version surfaced so the
	// operator knows which amendment to inspect.
	KindTransitionRefusedInvalidated TransitionRefusalKind = "transition-refused-invalidated"
)

// TransitionRefusal is the structured error returned when the
// runner refuses a downstream role transition. Implements `error`
// for caller convenience; the typed fields are intended for
// programmatic inspection (UI, attestation records, tooling).
type TransitionRefusal struct {
	// Kind is the structural category of the refusal.
	Kind TransitionRefusalKind

	// UpstreamArrowID identifies the blocking arrow.
	UpstreamArrowID string

	// DownstreamArrowID is the arrow the caller was trying to
	// enter (for context; helps operators trace which transition).
	DownstreamArrowID string

	// UpstreamStatus is the upstream arrow's derived status at
	// refusal time. ArrowStatusComplete would not refuse; the
	// other values populate this for diagnostic output.
	UpstreamStatus ArrowStatus

	// BlockingClauses is the count of clauses contributing to the
	// upstream's non-complete status (per DeriveArrowStatus's
	// second return). Zero for invalidated refusals.
	BlockingClauses int

	// InvalidatingGridVersion is the grid-version that invalidated
	// the upstream. Zero unless Kind ==
	// KindTransitionRefusedInvalidated.
	InvalidatingGridVersion int
}

// Error formats a human-readable refusal message. The structured
// fields above are the API; the error string is for log lines and
// fall-back display.
//
// Wire format (validation-pass-3 F18): both Kind and UpstreamStatus
// serialize via their String() method (Kind is a string-typed alias;
// UpstreamStatus's String() returns the wire form). The format
// `%s: upstream %q has status %s (%d blocking)` is the documented
// shape attestation-log consumers may rely on; if it changes, that
// is a breaking change to the wire contract.
func (r *TransitionRefusal) Error() string {
	switch r.Kind {
	case KindTransitionRefusedInvalidated:
		return fmt.Sprintf("%s: upstream arrow %q invalidated by grid-version v%d; downstream %q refused",
			string(r.Kind), r.UpstreamArrowID, r.InvalidatingGridVersion, r.DownstreamArrowID)
	default:
		return fmt.Sprintf("%s: upstream arrow %q has status %s (%d blocking clauses); downstream %q refused",
			string(r.Kind), r.UpstreamArrowID, r.UpstreamStatus, r.BlockingClauses, r.DownstreamArrowID)
	}
}

// Is supports errors.Is comparison against ErrTransitionRefused so
// callers can match the broader category without depending on the
// concrete type.
func (r *TransitionRefusal) Is(target error) bool {
	return target == ErrTransitionRefused
}

// ErrTransitionRefused is the sentinel callers compare against via
// errors.Is. The concrete error is always *TransitionRefusal; this
// sentinel lets callers match the category without importing the
// kind constants.
//
// Note (validation-pass-3 F37): comparison is pointer-identity, NOT
// message equality. `errors.New("transition-refused")` (a fresh
// errorString) will NOT match via errors.Is — use this exported
// sentinel.
var ErrTransitionRefused = errors.New("transition-refused")

// ErrTransitionInvalidInput is returned when CheckTransition is
// called with malformed arguments (empty arrow IDs, negative
// blocking counts, etc.). Distinct from ErrTransitionRefused so
// callers can tell a programmer bug apart from a legitimate
// refusal. Validation-pass-3 F19.
var ErrTransitionInvalidInput = errors.New("transition-invalid-input")

// CheckTransition reports whether a downstream role transition is
// permitted given the upstream arrow's current state. Returns nil
// (permitted) only when the upstream is ArrowStatusComplete and
// not invalidated. Every other state produces a *TransitionRefusal.
//
// upstreamArrowID and downstreamArrowID are caller-supplied opaque
// identifiers; they appear in the refusal message and structured
// error for operator triage.
//
// If the upstream is ArrowStatusInvalidated, the caller supplies
// invalidatingGridVersion (the version of the amendment that
// invalidated). For non-invalidated refusals, callers pass 0.
func CheckTransition(
	upstreamArrowID, downstreamArrowID string,
	upstreamStatus ArrowStatus,
	blockingClauses int,
	invalidatingGridVersion int,
) error {
	// Validation-pass-3 F19: refuse empty IDs and negative counts.
	// These are programmer bugs (callers should never produce
	// them), surfaced via a distinct error sentinel.
	if upstreamArrowID == "" || downstreamArrowID == "" {
		return fmt.Errorf("%w: arrow IDs must be non-empty (upstream=%q downstream=%q)",
			ErrTransitionInvalidInput, upstreamArrowID, downstreamArrowID)
	}
	if blockingClauses < 0 {
		return fmt.Errorf("%w: blockingClauses must be >= 0; got %d",
			ErrTransitionInvalidInput, blockingClauses)
	}
	if invalidatingGridVersion < 0 {
		return fmt.Errorf("%w: invalidatingGridVersion must be >= 0; got %d",
			ErrTransitionInvalidInput, invalidatingGridVersion)
	}

	if upstreamStatus == ArrowStatusInvalidated {
		// Validation-pass-3 F20: grid versions start at v1; v0 is
		// structurally meaningless for an invalidated arrow.
		if invalidatingGridVersion < 1 {
			return fmt.Errorf("%w: invalidated status requires invalidatingGridVersion >= 1; got %d",
				ErrTransitionInvalidInput, invalidatingGridVersion)
		}
		// F21: preserve blockingClauses even for invalidated —
		// findings raised before invalidation are retained tagged
		// with grid-version per gates.md §7.2.
		return &TransitionRefusal{
			Kind:                    KindTransitionRefusedInvalidated,
			UpstreamArrowID:         upstreamArrowID,
			DownstreamArrowID:       downstreamArrowID,
			UpstreamStatus:          upstreamStatus,
			BlockingClauses:         blockingClauses,
			InvalidatingGridVersion: invalidatingGridVersion,
		}
	}
	if upstreamStatus.SatisfiesNextRole() {
		return nil
	}
	return &TransitionRefusal{
		Kind:              KindTransitionRefused,
		UpstreamArrowID:   upstreamArrowID,
		DownstreamArrowID: downstreamArrowID,
		UpstreamStatus:    upstreamStatus,
		BlockingClauses:   blockingClauses,
	}
}

// AsTransitionRefusal unwraps an error to its concrete
// *TransitionRefusal, or returns nil if err is not one. Convenience
// for callers that need the typed fields (UI rendering, attestation
// records) without repeating the type assertion at every call site.
func AsTransitionRefusal(err error) *TransitionRefusal {
	var tr *TransitionRefusal
	if errors.As(err, &tr) {
		return tr
	}
	return nil
}
