package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AmendmentCommitter drives a drained AmendmentRequest through to
// grid v(N+1) per gates.md §3.7. The amendment fires when the
// integrator emits a `missing-cross-context-spec` finding; the
// analyst responds with one or more new ArrowDefinitions; the
// committer:
//
//  1. Appends each new arrow to the Grid. Grid.Append bumps the
//     internal version counter — that is the grid v(N+1) marker.
//  2. Aborts in-flight passes whose ArrowID matches the
//     amendment's SourceArrow. The PassRegistry surfaces them;
//     the lock token releases on Abort.
//  3. Publishes typed OperatorEvents (amendment-drained,
//     pass-closed via the abort path).
//  4. Marks the AmendmentRequest as drained in the queue (the
//     queue's Drain method already provides the
//     "drained-at" mechanic; the committer feeds that through).
//
// Concurrency: AmendmentCommitter serializes commits via its own
// mutex so two integrator threads can't race two amendments on
// the same source arrow into the grid out of order.
type AmendmentCommitter struct {
	mu sync.Mutex

	Grid   *Grid
	Passes *PassRegistry
	Bus    *OperatorBus
	Queue  *AmendmentQueue
	Now    func() time.Time
}

// CommitRequest is the input to AmendmentCommitter.Commit.
type CommitRequest struct {
	// Amendment is the drained request that triggered this
	// commit. CommittedAmendment.Request returns this verbatim.
	Amendment AmendmentRequest

	// NewArrows are the ArrowDefinitions the analyst produced in
	// response to the amendment. Each is appended to the Grid via
	// Grid.Append. Order matters — the appends bump the version
	// counter in declaration order. Empty slice is permitted (the
	// analyst may decide the original arrow is still valid; the
	// amendment then just aborts the in-flight passes so they
	// re-traverse).
	NewArrows []ArrowDefinition
}

// CommitResult bundles the per-commit summary.
type CommitResult struct {
	GridVersionBefore uint64
	GridVersionAfter  uint64
	AppendedArrows    []string
	AbortedPasses     []string
	CommittedAt       time.Time
}

// AmendmentCommitter errors.
var (
	ErrAmendmentCommitNoGrid   = errors.New("amendment-commit: nil Grid")
	ErrAmendmentCommitNoPasses = errors.New("amendment-commit: nil Passes")
	ErrAmendmentCommitInvalid  = errors.New("amendment-commit: invalid request")
)

// Commit applies one amendment. Returns CommitResult on success or
// an error if validation fails / Grid.Append errors / any new arrow
// already exists (Grid.Append already enforces this via
// ErrArrowAlreadyDeclared).
//
// Ordering: pass-abort FIRST (stop in-flight work that runs against
// the now-invalidated contract), THEN grid append (introduce the new
// contract), THEN queue MarkDrained (persist the drained_at marker
// via the journal), THEN bus event (operator-facing signal).
//
// If grid append fails part-way, the committer:
//   - Has already aborted the affected in-flight passes (the
//     source-arrow contract IS invalidated; passes can't continue).
//   - Persists the drained_at for the amendment regardless (the
//     analyst's decision to commit IS final; the partial append is a
//     mechanical error to surface separately).
//   - Surfaces the partial state via res.AppendedArrows and the
//     wrapped error.
//
// Empty req.NewArrows is valid per gates.md §3.7: the analyst may
// re-assess and decide the original contract holds. The grid
// version stays put; affected passes still abort to re-traverse
// from a clean state.
func (c *AmendmentCommitter) Commit(ctx context.Context, req CommitRequest) (*CommitResult, error) {
	if c == nil {
		return nil, errors.New("amendment-commit: nil receiver")
	}
	if c.Grid == nil {
		return nil, ErrAmendmentCommitNoGrid
	}
	if c.Passes == nil {
		return nil, ErrAmendmentCommitNoPasses
	}
	if err := req.Amendment.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAmendmentCommitInvalid, err)
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	res := &CommitResult{
		GridVersionBefore: c.Grid.Version(),
		CommittedAt:       now(),
	}

	// 1. Abort in-flight passes on the SourceArrow FIRST. The
	//    amendment invalidates the contract those passes are running
	//    under; they must stop before the new grid arrives. Doing this
	//    before Append means a partial append still aborts the
	//    affected passes — they don't continue running against a
	//    half-amended grid.
	for _, p := range c.Passes.All() {
		if p.ArrowID() == req.Amendment.SourceArrow && p.State() == PassStateOpen {
			p.Abort(fmt.Sprintf("amendment %s drained: %s",
				req.Amendment.ID, req.Amendment.Reason))
			res.AbortedPasses = append(res.AbortedPasses, p.ID())
		}
	}

	// 2. Append new arrows to the grid. Each Append bumps the
	//    version counter; the final version is the grid v(N+1)
	//    semantic. If any append fails, the partial state is
	//    surfaced via res.AppendedArrows + the wrapped error.
	var appendErr error
	for _, def := range req.NewArrows {
		if _, err := c.Grid.Append(def); err != nil {
			appendErr = fmt.Errorf("amendment-commit: append arrow %s: %w",
				strings.TrimSpace(def.ID), err)
			break
		}
		res.AppendedArrows = append(res.AppendedArrows, def.ID)
	}
	res.GridVersionAfter = c.Grid.Version()

	// 3. Persist drained_at. The committer marks the amendment as
	//    drained on the queue, which emits AmendmentEventDrain. The
	//    engine.Journal's existing AmendmentObserver handler at
	//    engine/journal.go:handleAmendment then writes the drained_at
	//    column. Without this step, the amendment re-replays as
	//    pending on next session start.
	if c.Queue != nil {
		c.Queue.MarkDrained(req.Amendment.ID)
	}

	// 4. Publish the amendment-drained operator event so
	//    subscribers (JSONL writer, status CLI, future operator UI)
	//    see the commit's outcome.
	if c.Bus != nil {
		status := "complete"
		if appendErr != nil {
			status = "partial-append-error"
		}
		c.Bus.Publish(OperatorEvent{
			Kind:    OpEventAmendmentDrained,
			ArrowID: req.Amendment.SourceArrow,
			Detail: fmt.Sprintf("amendment=%s reason=%s status=%s arrows-added=%d passes-aborted=%d v=%d->%d",
				req.Amendment.ID, req.Amendment.Reason, status,
				len(res.AppendedArrows), len(res.AbortedPasses),
				res.GridVersionBefore, res.GridVersionAfter),
		})
	}

	if appendErr != nil {
		return res, appendErr
	}
	return res, nil
}
