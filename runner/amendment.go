package runner

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
//     and the analyst's runner reads from. Bounded by MaxLen (F38).
//     Remembers drained IDs in seenIDs to dedup across drain
//     boundaries (F44).
//   - PendingAmendments(store, arrowID): scans a FindingsStore for
//     open missing-cross-context-spec findings on the given arrow
//     and returns the implied amendment requests. Returns an error
//     if contexts is too short (F39).

// AmendmentReason is the typed enum for why an amendment was
// triggered. Validated at Validate-time (F36): unknown reasons are
// rejected.
type AmendmentReason string

const (
	// AmendmentReasonMissingCrossContextSpec — a finding of type
	// missing-cross-context-spec is open and the analyst must
	// re-specify the cross-context contract.
	AmendmentReasonMissingCrossContextSpec AmendmentReason = "missing-cross-context-spec"
)

// knownAmendmentReasons enumerates the valid AmendmentReason values
// at Validate time. Adding a new reason requires touching this set
// (deliberate friction so the validation boundary stays narrow).
var knownAmendmentReasons = map[AmendmentReason]struct{}{
	AmendmentReasonMissingCrossContextSpec: {},
}

// AmendmentRequest is the structured payload of an amendment-trigger.
// Carries the operator-facing fields the analyst arrow needs to
// re-specify the cross-context gap.
type AmendmentRequest struct {
	ID          string
	Reason      AmendmentReason
	SourceArrow string
	TargetRole  string
	Contexts    []string
	Description string
	FindingIDs  []string
	CreatedAt   string
}

// Validate checks that the AmendmentRequest is well-formed.
// F36: unknown reasons are rejected. Whitespace-only string fields
// are rejected as empty.
func (r AmendmentRequest) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("amendment-id-empty")
	}
	if r.Reason == "" {
		return errors.New("amendment-reason-empty")
	}
	if _, ok := knownAmendmentReasons[r.Reason]; !ok {
		return fmt.Errorf("amendment-reason-unknown: %q", r.Reason)
	}
	if strings.TrimSpace(r.SourceArrow) == "" {
		return errors.New("amendment-source-arrow-empty")
	}
	if strings.TrimSpace(r.TargetRole) == "" {
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

// AmendmentQueue is a thread-safe bounded queue of pending
// amendments. Construct via NewAmendmentQueue. Multiple producers
// (one per integrator arrow) may write; one consumer (the analyst-
// arrow scheduler) drains.
//
// F38: bounded by MaxLen. Default DefaultAmendmentQueueMaxLen;
// override via NewAmendmentQueueWithMax. Overflow returns
// ErrAmendmentQueueFull.
//
// F44: drained IDs are remembered in seenIDs so a second
// PendingAmendments on the same still-open finding yields the same
// amendment ID and the Enqueue dedups. Call Reset to clear seenIDs
// at session-end.
type AmendmentQueue struct {
	mu      sync.Mutex
	pending []AmendmentRequest
	byID    map[string]struct{}
	seenIDs map[string]struct{}
	maxLen  int
}

// DefaultAmendmentQueueMaxLen is the per-queue cap when no override
// is supplied. Tuned to surface backpressure without forcing the
// operator to plumb a config knob in v1.
const DefaultAmendmentQueueMaxLen = 1024

// NewAmendmentQueue returns an empty queue with the default cap.
func NewAmendmentQueue() *AmendmentQueue {
	return NewAmendmentQueueWithMax(DefaultAmendmentQueueMaxLen)
}

// NewAmendmentQueueWithMax returns an empty queue with the given
// MaxLen. Zero or negative max means "unbounded" (test-only).
func NewAmendmentQueueWithMax(maxLen int) *AmendmentQueue {
	return &AmendmentQueue{
		byID:    make(map[string]struct{}),
		seenIDs: make(map[string]struct{}),
		maxLen:  maxLen,
	}
}

// Amendment queue errors.
var (
	ErrAmendmentDuplicateID = errors.New("amendment-duplicate-id")
	ErrAmendmentQueueFull   = errors.New("amendment-queue-full")
)

// Enqueue adds an amendment to the queue. Idempotent on ID across
// drain boundaries (F44): if the ID has ever been seen by this
// queue, Enqueue returns ErrAmendmentDuplicateID.
func (q *AmendmentQueue) Enqueue(r AmendmentRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, dup := q.byID[r.ID]; dup {
		return fmt.Errorf("%w: %s (pending)", ErrAmendmentDuplicateID, r.ID)
	}
	if _, dup := q.seenIDs[r.ID]; dup {
		return fmt.Errorf("%w: %s (already drained)", ErrAmendmentDuplicateID, r.ID)
	}
	if q.maxLen > 0 && len(q.pending) >= q.maxLen {
		return fmt.Errorf("%w: %d pending (max %d)", ErrAmendmentQueueFull, len(q.pending), q.maxLen)
	}
	q.byID[r.ID] = struct{}{}
	q.seenIDs[r.ID] = struct{}{}
	q.pending = append(q.pending, deepCopyAmendment(r))
	return nil
}

// Drain returns a deep-copy snapshot of pending amendments and
// clears the pending slice. seenIDs is RETAINED so drained IDs
// can't be re-emitted (F44). Call Reset to clear seenIDs too.
func (q *AmendmentQueue) Drain() []AmendmentRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]AmendmentRequest, len(q.pending))
	for i, r := range q.pending {
		out[i] = deepCopyAmendment(r)
	}
	q.pending = nil
	q.byID = make(map[string]struct{})
	return out
}

// Pending returns a deep-copy snapshot without clearing. F37: each
// returned AmendmentRequest's slice fields are independent of the
// queue's view.
func (q *AmendmentQueue) Pending() []AmendmentRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]AmendmentRequest, len(q.pending))
	for i, r := range q.pending {
		out[i] = deepCopyAmendment(r)
	}
	return out
}

// Len returns the number of pending amendments.
func (q *AmendmentQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// Reset clears both the pending list AND the seenIDs dedup set.
// Typically called at session boundary; per-arrow loops should NOT
// call Reset between iterations or F44's dedup is lost.
func (q *AmendmentQueue) Reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = nil
	q.byID = make(map[string]struct{})
	q.seenIDs = make(map[string]struct{})
}

// deepCopyAmendment returns an AmendmentRequest whose slice fields
// share no storage with r. Used by Enqueue/Drain/Pending so callers
// can't poison queue state by mutating returned slices (F37).
func deepCopyAmendment(r AmendmentRequest) AmendmentRequest {
	out := r
	if r.Contexts != nil {
		out.Contexts = append([]string(nil), r.Contexts...)
	}
	if r.FindingIDs != nil {
		out.FindingIDs = append([]string(nil), r.FindingIDs...)
	}
	return out
}

// ErrAmendmentContextsTooFew is returned by PendingAmendments when
// the caller-supplied contexts list is too short to build a valid
// missing-cross-context-spec amendment (F39).
var ErrAmendmentContextsTooFew = errors.New("amendment-contexts-too-few")

// PendingAmendments scans a FindingsStore for findings on the named
// arrow that match AmendmentReasonMissingCrossContextSpec and are
// open, returning the implied AmendmentRequest(s). One request per
// finding; the integrator's session loop typically calls this at
// arrow-end and enqueues each result on the project's
// AmendmentQueue.
//
// contexts is the operator-supplied list of bounded contexts the
// finding implicates. Must have >= 2 entries for
// missing-cross-context-spec (F39). Validated upfront, not lazily
// at Enqueue time.
//
// idGen produces unique amendment IDs; if nil, uses
// defaultAmendmentIDGen.
func PendingAmendments(
	store *FindingsStore,
	arrowID string,
	contexts []string,
	idGen func() string,
) ([]AmendmentRequest, error) {
	if store == nil || arrowID == "" {
		return nil, nil
	}
	// F39: validate contexts upfront so the caller sees the gap at
	// the source, not lazily at Enqueue time.
	if len(contexts) < 2 {
		return nil, fmt.Errorf("%w: missing-cross-context-spec requires >= 2 contexts (got %d)",
			ErrAmendmentContextsTooFew, len(contexts))
	}
	if idGen == nil {
		idGen = defaultAmendmentIDGen
	}
	findings := store.ForArrow(arrowID)
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	// F40: capture time.Now once for stable CreatedAt across the
	// batch, and use the seq-counter ID generator to avoid same-
	// nano collisions.
	now := time.Now().UTC().Format(time.RFC3339)
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
			CreatedAt:   now,
		})
	}
	return out, nil
}

// amendmentSeq is a monotonic counter appended to the default ID
// generator (F40). Survives concurrent goroutines via atomic.
var amendmentSeq atomic.Uint64

// defaultAmendmentIDGen produces an amendment ID of the form
// "amend-<nano>-<seq>-<processIDPrefix>". The atomic seq counter
// guarantees uniqueness even on coarse-clock platforms where two
// Now() calls return identical nanoseconds (F40).
func defaultAmendmentIDGen() string {
	seq := amendmentSeq.Add(1)
	return fmt.Sprintf("amend-%s-%d-%s",
		time.Now().UTC().Format("20060102-150405.000000000"),
		seq,
		processIDPrefix)
}

// FormatAmendmentSummary renders a multi-line summary of an
// amendment request suitable for operator display or log lines.
// Wire-stable: downstream tooling may parse the output. Operator-
// supplied text is sanitized (F43) so embedded newlines / control
// characters can't forge structured fields.
func FormatAmendmentSummary(r AmendmentRequest) string {
	var b strings.Builder
	b.WriteString("amendment ")
	b.WriteString(sanitizeOneLine(r.ID))
	b.WriteString(" (")
	b.WriteString(sanitizeOneLine(string(r.Reason)))
	b.WriteString(")\n")
	fmt.Fprintf(&b, "  source-arrow: %s\n", sanitizeOneLine(r.SourceArrow))
	fmt.Fprintf(&b, "  target-role:  %s\n", sanitizeOneLine(r.TargetRole))
	if len(r.Contexts) > 0 {
		sanitized := make([]string, len(r.Contexts))
		for i, c := range r.Contexts {
			sanitized[i] = sanitizeOneLine(c)
		}
		fmt.Fprintf(&b, "  contexts:     %s\n", strings.Join(sanitized, ", "))
	}
	if len(r.FindingIDs) > 0 {
		sanitized := make([]string, len(r.FindingIDs))
		for i, id := range r.FindingIDs {
			sanitized[i] = sanitizeOneLine(id)
		}
		fmt.Fprintf(&b, "  findings:     %s\n", strings.Join(sanitized, ", "))
	}
	if r.Description != "" {
		fmt.Fprintf(&b, "  description:  %s\n", sanitizeOneLine(r.Description))
	}
	if r.CreatedAt != "" {
		fmt.Fprintf(&b, "  created-at:   %s\n", sanitizeOneLine(r.CreatedAt))
	}
	return b.String()
}

// sanitizeOneLine is defined in sanitize.go (shared with other
// evaluators emitting operator-supplied free-text).
