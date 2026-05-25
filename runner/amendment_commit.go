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

	// BindingsReRegister is an optional callback that builds an
	// in-memory snapshot of the evaluator registry from the
	// amendment's NewLanguageBindings (Gap 2 mid-drain ordering,
	// ADR-v4-003). Returns the snapshot registry the committer
	// will atomically swap into the live runtime AFTER the disk
	// write succeeds. Nil = skip the snapshot+swap step (used by
	// tests and pre-v4 call sites).
	//
	// The integration site (cmd/ghyll, per ADR-v4-007) provides
	// the implementation since it spans bootstrap.Grid +
	// runner.Registry.
	BindingsReRegister func(req CommitRequest) (snapshot *Registry, swap func(), err error)

	// LiveRegistry is the live evaluator registry the committer
	// swaps the snapshot into on commit. Nil = no swap performed
	// even if BindingsReRegister returns a snapshot. Set at runtime
	// construction; immutable post-construct per design-M11.
	LiveRegistry *Registry

	// Workdir is the working directory the committer hands to the
	// grid Write path (per Gap 2 step 6 disk write). Empty = no
	// disk persistence; the in-memory grid mutates and the
	// version bumps, but the on-disk grid.v<N+1>.yaml is not
	// produced. cmd/ghyll wires this with the session workdir.
	Workdir string
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

	// NewLanguageBindings is the optional overlay of new
	// `<concept>.<language>: <command>` bindings the amendment
	// introduces. Lives on CommitRequest (runtime), NOT
	// AmendmentRequest (persisted) per R21 closure — old
	// amendments replay cleanly because the field isn't in the
	// persistence record. Nil / empty = no binding overlay.
	NewLanguageBindings map[string]string
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
	// ErrAmendmentCommitFIFO is returned when the queue head shifts
	// mid-drain (R22 closure). The queue retains the un-drained
	// amendments; the caller may retry after diagnosing.
	ErrAmendmentCommitFIFO = errors.New("amendment-commit: FIFO violation")
	// ErrAmendmentCommitBindings is returned when
	// BindingsReRegister fails to build the snapshot. The commit
	// aborts BEFORE the grid version bumps (per ADR-v4-003).
	ErrAmendmentCommitBindings = errors.New("amendment-commit: bindings re-register failed")
)

// Commit applies one amendment. Returns CommitResult on success or
// an error if validation fails / Grid.Append errors / any new arrow
// already exists (Grid.Append already enforces this via
// ErrArrowAlreadyDeclared).
//
// Ordering (integrator-pass I-M-1 closure): grid append FIRST
// (introduce the new contract), THEN binding swap (live registry
// follows the grid), THEN pass-abort (stop in-flight work; close
// events now correlate to the NEW bindings — subscribers that
// re-read the live registry on close see the contract that
// supersedes the aborted pass, not the one that just died), THEN
// queue MarkDrained (persist the drained_at marker via the
// journal), THEN bus event (operator-facing signal).
//
// The prior ordering (abort → append → swap) had OpEventPassClosed
// firing while bindings still pointed at the old contract; a
// status-CLI subscriber that re-rendered the affected arrow on
// close would have seen pre-amendment bindings. The reordered
// invariant: any subscriber correlating a close event to a live-
// registry lookup observes the new contract that superseded the
// aborted pass.
//
// If grid append fails part-way, the committer:
//   - Has NOT yet aborted in-flight passes (the new contract did
//     not fully arrive; the old contract is still the authority).
//     Aborts still run AFTER the partial append so the passes
//     observe the partial-amended grid, not a clean rollback.
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

	// Diamond v4 / R22 closure: FIFO head check. If the queue has
	// pending amendments, the head MUST match this amendment's ID.
	// An empty queue is permitted (the test path that constructs a
	// committer with an empty queue and calls Commit directly, or
	// the drain path AFTER the head was popped); only a non-empty
	// queue whose head DOESN'T match is a FIFO violation.
	if c.Queue != nil {
		pending := c.Queue.Pending()
		if len(pending) > 0 && pending[0].ID != req.Amendment.ID {
			return nil, fmt.Errorf("%w: expected %q at head, got %q",
				ErrAmendmentCommitFIFO, req.Amendment.ID, pending[0].ID)
		}
	}

	// Diamond v4 / ADR-v4-003: build the registry snapshot BEFORE
	// any mutation. If the snapshot construction fails (a malformed
	// binding overlay, unregistered concept), abort here — the live
	// registry, grid, and queue are all unchanged.
	var snapshot *Registry
	var swap func()
	if c.BindingsReRegister != nil {
		var err error
		snapshot, swap, err = c.BindingsReRegister(req)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAmendmentCommitBindings, err)
		}
	}
	_ = snapshot // referenced by `swap` closure when invoked below

	res := &CommitResult{
		GridVersionBefore: c.Grid.Version(),
		CommittedAt:       now(),
	}

	// 1. Append new arrows to the grid. Each Append bumps the
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

	// 2. Atomically swap the snapshot registry into the live
	//    runtime (ADR-v4-003 step 6a). The snapshot was built
	//    pre-mutation; swapping AFTER append means concurrent
	//    dispatchers see OLD or NEW bindings, never partial.
	if swap != nil && appendErr == nil {
		swap()
	}

	// 3. Abort in-flight passes on the SourceArrow. The amendment
	//    invalidates the contract those passes are running under;
	//    they must stop now that the new grid + bindings are live.
	//    Doing this AFTER the swap (I-M-1 closure) means
	//    OpEventPassClosed fires while subscribers' live-registry
	//    lookups for the affected arrow see the NEW contract — a
	//    subscriber correlating the close event to a registry
	//    lookup no longer races against a half-applied amendment.
	for _, p := range c.Passes.All() {
		if p.ArrowID() == req.Amendment.SourceArrow && p.State() == PassStateOpen {
			p.Abort(fmt.Sprintf("amendment %s drained: %s",
				req.Amendment.ID, req.Amendment.Reason))
			res.AbortedPasses = append(res.AbortedPasses, p.ID())
		}
	}

	// 4. Persist drained_at. The committer marks the amendment as
	//    drained on the queue, which emits AmendmentEventDrain. The
	//    engine.Journal's existing AmendmentObserver handler at
	//    engine/journal.go:handleAmendment then writes the drained_at
	//    column. Without this step, the amendment re-replays as
	//    pending on next session start.
	//
	// I-M-3 (advisory) is documented at lines 126-142: drained_at is
	// persisted even on partial-append failure so the analyst's
	// commit decision is final; the integrator-pass review noted the
	// runtime-vs-persisted-grid drift but treats it as deliberate.
	if c.Queue != nil {
		c.Queue.MarkDrained(req.Amendment.ID)
	}

	// 5. Publish the amendment-drained operator event so
	//    subscribers (JSONL writer, status CLI, future operator UI)
	//    see the commit's outcome.
	//
	// I-M-3 closure: populate the typed Payload per ADR-v4-005 so
	// the runtime-vs-persisted-grid drift on partial-append-error is
	// MACHINE-observable, not just embedded in the human-readable
	// Detail. Subscribers comparing outcome=partial-append-error
	// against the live grid version can detect the desync without
	// parsing free text.
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
			Payload: map[string]string{
				"amendment_id":        req.Amendment.ID,
				"source_arrow":        req.Amendment.SourceArrow,
				"grid_version_before": fmt.Sprintf("%d", res.GridVersionBefore),
				"grid_version_after":  fmt.Sprintf("%d", res.GridVersionAfter),
				"arrows_added":        fmt.Sprintf("%d", len(res.AppendedArrows)),
				"passes_aborted":      fmt.Sprintf("%d", len(res.AbortedPasses)),
				"outcome":             status,
			},
		})
	}

	if appendErr != nil {
		return res, appendErr
	}
	return res, nil
}
