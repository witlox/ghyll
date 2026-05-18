package runner

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Amendment trigger flow. Per gates.md §3.7 + the integrator role
// file: when integrator findings of type `missing-cross-context-spec`
// remain open, the integrator emits an amendment request that
// creates an integrator → analyst arrow carrying the cross-context
// gap. The analyst arrow re-runs and produces grid vN+1.
//
// This file ships:
//   - AmendmentRequest: the structured shape of an amendment.
//   - AmendmentQueue: a thread-safe queue the integrator writes to
//     and the analyst's runner reads from.
//   - PendingAmendments(store, arrowID): scans a FindingsStore for
//     open missing-cross-context-spec findings on the given arrow
//     and returns the implied amendment requests.

// AmendmentReason is the typed enum for why an amendment was
// triggered. Today only one value; the analyst may add more in
// future amendments to the spec.
type AmendmentReason string

const (
	// AmendmentReasonMissingCrossContextSpec — a finding of type
	// missing-cross-context-spec is open and the analyst must
	// re-specify the cross-context contract.
	AmendmentReasonMissingCrossContextSpec AmendmentReason = "missing-cross-context-spec"
)

// AmendmentRequest is the structured payload of an amendment-trigger.
// Carries the operator-facing fields the analyst arrow needs to
// re-specify the cross-context gap.
type AmendmentRequest struct {
	// ID identifies this request uniquely (typically a generated
	// UUID-like string; the runner doesn't require any particular
	// format).
	ID string

	// Reason is the typed reason this amendment was triggered.
	Reason AmendmentReason

	// SourceArrow is the arrow that detected the gap (e.g., the
	// integrator arrow). The amendment runs as a NEW arrow whose
	// upstream is SourceArrow's downstream role.
	SourceArrow string

	// TargetRole is the role responsible for handling the
	// amendment — typically "analyst" for missing-cross-context-
	// spec.
	TargetRole string

	// Contexts names the bounded contexts implicated by the gap.
	// At least two (the cross-context interaction).
	Contexts []string

	// Description is the operator-supplied narrative explaining
	// the gap.
	Description string

	// FindingIDs links back to the FindingRecord(s) that
	// triggered the amendment.
	FindingIDs []string

	// CreatedAt is the RFC3339 timestamp at which the amendment
	// was queued.
	CreatedAt string
}

// Validate checks that the AmendmentRequest is well-formed.
func (r AmendmentRequest) Validate() error {
	if r.ID == "" {
		return errors.New("amendment-id-empty")
	}
	if r.Reason == "" {
		return errors.New("amendment-reason-empty")
	}
	if r.SourceArrow == "" {
		return errors.New("amendment-source-arrow-empty")
	}
	if r.TargetRole == "" {
		return errors.New("amendment-target-role-empty")
	}
	if len(r.FindingIDs) == 0 {
		return errors.New("amendment-finding-ids-empty")
	}
	if r.Reason == AmendmentReasonMissingCrossContextSpec && len(r.Contexts) < 2 {
		return fmt.Errorf("amendment-contexts-too-few: missing-cross-context-spec requires >= 2 contexts (got %d)", len(r.Contexts))
	}
	return nil
}

// AmendmentQueue is a thread-safe queue of pending amendments.
// Construct via NewAmendmentQueue. Multiple producers (one per
// integrator arrow) may write; one consumer (the analyst-arrow
// scheduler) drains.
type AmendmentQueue struct {
	mu      sync.Mutex
	pending []AmendmentRequest
	byID    map[string]struct{}
}

// NewAmendmentQueue returns an empty queue.
func NewAmendmentQueue() *AmendmentQueue {
	return &AmendmentQueue{byID: make(map[string]struct{})}
}

// Amendment queue errors.
var (
	ErrAmendmentDuplicateID = errors.New("amendment-duplicate-id")
)

// Enqueue adds an amendment to the queue. Idempotent on ID:
// re-enqueueing the same ID returns ErrAmendmentDuplicateID so the
// caller knows the amendment was already submitted (and can move
// on without double-action).
func (q *AmendmentQueue) Enqueue(r AmendmentRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, dup := q.byID[r.ID]; dup {
		return fmt.Errorf("%w: %s", ErrAmendmentDuplicateID, r.ID)
	}
	q.byID[r.ID] = struct{}{}
	q.pending = append(q.pending, r)
	return nil
}

// Drain returns a snapshot of pending amendments and clears the
// queue. Used by the analyst-arrow scheduler at session boundaries.
func (q *AmendmentQueue) Drain() []AmendmentRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.pending
	q.pending = nil
	q.byID = make(map[string]struct{})
	return out
}

// Pending returns a snapshot without clearing. Useful for tests
// and for the runner's "is there work to do" check.
func (q *AmendmentQueue) Pending() []AmendmentRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]AmendmentRequest, len(q.pending))
	copy(out, q.pending)
	return out
}

// Len returns the number of pending amendments.
func (q *AmendmentQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// PendingAmendments scans a FindingsStore for findings on the named
// arrow that match AmendmentReasonMissingCrossContextSpec and are
// open, returning the implied AmendmentRequest(s). One request per
// finding; the integrator's session loop typically calls this at
// arrow-end and enqueues each result on the project's
// AmendmentQueue.
//
// contexts is the operator-supplied list of bounded contexts the
// finding implicates (the integrator role's session step knows
// which contexts the test exercised). Each returned request
// carries the same contexts and the source arrow's ID.
//
// idGen produces unique amendment IDs; if nil, uses a default
// time-based generator.
func PendingAmendments(
	store *FindingsStore,
	arrowID string,
	contexts []string,
	idGen func() string,
) []AmendmentRequest {
	if store == nil || arrowID == "" {
		return nil
	}
	if idGen == nil {
		idGen = defaultAmendmentIDGen
	}
	findings := store.ForArrow(arrowID)
	// Sort by ID for deterministic output.
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	var out []AmendmentRequest
	for _, f := range findings {
		if f.Type != FindingTypeMissingCrossContextSpec {
			continue
		}
		if f.Status != FindingStatusOpen {
			continue
		}
		ctxCopy := append([]string(nil), contexts...)
		out = append(out, AmendmentRequest{
			ID:          idGen(),
			Reason:      AmendmentReasonMissingCrossContextSpec,
			SourceArrow: arrowID,
			TargetRole:  "analyst",
			Contexts:    ctxCopy,
			Description: f.Description,
			FindingIDs:  []string{f.ID},
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}
	return out
}

// defaultAmendmentIDGen produces an amendment ID of the form
// "amend-<rfc3339-nano>". Includes the process prefix used for
// evaluation-run IDs so concurrent processes don't collide.
func defaultAmendmentIDGen() string {
	return "amend-" + time.Now().UTC().Format("20060102-150405.000000000") + "-" + processIDPrefix
}

// FormatAmendmentSummary renders a multi-line summary of an
// amendment request suitable for operator display or log lines.
// Wire-stable: downstream tooling may parse the output.
func FormatAmendmentSummary(r AmendmentRequest) string {
	var b strings.Builder
	b.WriteString("amendment ")
	b.WriteString(r.ID)
	b.WriteString(" (")
	b.WriteString(string(r.Reason))
	b.WriteString(")\n")
	fmt.Fprintf(&b, "  source-arrow: %s\n", r.SourceArrow)
	fmt.Fprintf(&b, "  target-role:  %s\n", r.TargetRole)
	if len(r.Contexts) > 0 {
		fmt.Fprintf(&b, "  contexts:     %s\n", strings.Join(r.Contexts, ", "))
	}
	if len(r.FindingIDs) > 0 {
		fmt.Fprintf(&b, "  findings:     %s\n", strings.Join(r.FindingIDs, ", "))
	}
	if r.Description != "" {
		fmt.Fprintf(&b, "  description:  %s\n", r.Description)
	}
	if r.CreatedAt != "" {
		fmt.Fprintf(&b, "  created-at:   %s\n", r.CreatedAt)
	}
	return b.String()
}
