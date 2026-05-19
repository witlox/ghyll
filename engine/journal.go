package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/witlox/ghyll/runner"
)

// Journal subscribes to the runner-layer Observer hooks and writes
// every mutation to the Store. It bridges in-memory runtime state
// to persistent storage; it does NOT decide policy. Replay-on-
// startup (engine/replay.go) reads the same tables back.
//
// Concurrency design (validation-pass-9 J5/J6/J7):
//   - Observers fire under the runner store's WRITE lock. The
//     observer body MUST NOT do synchronous sqlite writes — that
//     would stall the runner under disk pressure. Instead, each
//     observer body pushes a typed event onto a bounded channel
//     and returns immediately.
//   - A single consumer goroutine drains the channel and writes
//     synchronously off the hot path. Single goroutine preserves
//     total order; channel buffer absorbs bursts.
//   - On channel-full the journal LOGS and drops the event (a
//     metric counter increments) rather than block the runner.
//     Replay reconciles at next startup.
//   - Close signals the consumer goroutine to drain remaining
//     events and exit. After Close, observers no longer enqueue
//     (the atomic flag flips so the closure returns immediately).
//   - clock is injectable (J11) so tests can pin timestamps.
type Journal struct {
	store   *Store
	logger  *slog.Logger
	clock   func() time.Time
	timeout time.Duration

	events   chan journalEvent
	consumer sync.WaitGroup
	closed   atomic.Bool
	dropped  atomic.Uint64
}

// journalEvent is the type-erased payload pushed onto the consumer
// channel. Each handler fills the relevant fields; the consumer
// dispatches by Kind.
type journalEvent struct {
	kind           string
	finding        runner.FindingsEvent
	classification runner.ClassificationsEvent
	grid           runner.GridEvent
	amendment      runner.AmendmentEvent
	run            *runner.EvaluationRun
	attestation    runner.AttestationEvent
	flushDone      chan struct{} // populated for jKindFlush events
}

const (
	jKindFinding        = "finding"
	jKindClassification = "classification"
	jKindGrid           = "grid"
	jKindAmendment      = "amendment"
	jKindRun            = "run"
	jKindAttestation    = "attestation"
	jKindFlush          = "flush"

	defaultJournalBuffer  = 1024
	defaultJournalTimeout = 5 * time.Second
)

// NewJournal constructs a Journal bound to store. If logger is nil,
// slog.Default() is used. The consumer goroutine starts immediately.
func NewJournal(store *Store, logger *slog.Logger) *Journal {
	return NewJournalWithClock(store, logger, time.Now, defaultJournalBuffer)
}

// NewJournalWithClock is the test-friendly constructor. Pass nil
// clock for time.Now. Buffer of 0 uses the default.
func NewJournalWithClock(store *Store, logger *slog.Logger, clock func() time.Time, buffer int) *Journal {
	if store == nil {
		panic("engine.NewJournal: nil store")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	if buffer <= 0 {
		buffer = defaultJournalBuffer
	}
	j := &Journal{
		store:   store,
		logger:  logger,
		clock:   clock,
		timeout: defaultJournalTimeout,
		events:  make(chan journalEvent, buffer),
	}
	j.consumer.Add(1)
	go j.runConsumer()
	return j
}

// Close stops the consumer goroutine. Observers fired after Close
// drop their events with a log message (and a dropped counter
// increment) rather than block. Close blocks until the consumer
// has drained the channel.
func (j *Journal) Close() {
	if !j.closed.CompareAndSwap(false, true) {
		return
	}
	close(j.events)
	j.consumer.Wait()
}

// Dropped returns the count of events dropped due to full channel
// or post-Close arrival. Useful for the engine's health metrics.
func (j *Journal) Dropped() uint64 {
	return j.dropped.Load()
}

// enqueue pushes an event onto the consumer channel. Per
// integrator-pass C1: a full channel is treated as backpressure
// rather than silent loss — the call blocks up to
// enqueueBackpressureBudget so the runner experiences a short stall
// rather than the engine.db diverging from in-memory state.
//
// After Close or after the budget elapses, the event drops and the
// counter increments so the operator sees the divergence at
// session shutdown (see Session.Close in cmd/ghyll).
func (j *Journal) enqueue(e journalEvent) {
	if j.closed.Load() {
		j.dropped.Add(1)
		return
	}
	// Fast path — non-blocking send when the consumer is keeping up.
	select {
	case j.events <- e:
		return
	default:
	}
	// Backpressure path — bounded block. Observers fire under the
	// runner's WRITE lock so a long block here stalls the runner;
	// the budget keeps the worst case to enqueueBackpressureBudget.
	t := time.NewTimer(enqueueBackpressureBudget)
	defer t.Stop()
	select {
	case j.events <- e:
		return
	case <-t.C:
		j.dropped.Add(1)
		j.logger.Warn("engine.Journal: events channel full; dropping event",
			"after", enqueueBackpressureBudget, "kind", e.kind)
	}
}

// enqueueBackpressureBudget caps the per-event block when the
// consumer goroutine is behind. Tuned for batch-burst durability:
// short enough that a slow disk doesn't deadlock the runner, long
// enough that a transient consumer stall doesn't trigger drops.
const enqueueBackpressureBudget = 100 * time.Millisecond

// runConsumer drains the channel and writes to sqlite synchronously.
// Single goroutine preserves total order.
func (j *Journal) runConsumer() {
	defer j.consumer.Done()
	for e := range j.events {
		j.handle(e)
	}
}

// handle dispatches a single event to the appropriate write path.
// Errors log and continue — a single sqlite glitch must not stop
// the consumer.
func (j *Journal) handle(e journalEvent) {
	if e.kind == jKindFlush {
		close(e.flushDone)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	defer cancel()
	switch e.kind {
	case jKindFinding:
		j.handleFinding(ctx, e.finding)
	case jKindClassification:
		j.handleClassification(ctx, e.classification)
	case jKindGrid:
		j.handleGrid(ctx, e.grid)
	case jKindAmendment:
		j.handleAmendment(ctx, e.amendment)
	case jKindRun:
		j.handleRun(ctx, e.run)
	case jKindAttestation:
		j.handleAttestation(ctx, e.attestation)
	}
}

// Flush blocks until the consumer has processed every event that
// was already on the channel when Flush was called. Used by tests
// and by code paths that need to read sqlite immediately after a
// mutation (e.g., session-end checkpoint).
//
// Returns immediately if the journal is closed.
func (j *Journal) Flush() {
	if j.closed.Load() {
		return
	}
	done := make(chan struct{})
	select {
	case j.events <- journalEvent{kind: jKindFlush, flushDone: done}:
	default:
		// Channel full — fall back to blocking send so Flush still
		// signals correctly.
		j.events <- journalEvent{kind: jKindFlush, flushDone: done}
	}
	<-done
}

// logErr writes an error with a labelled prefix.
func (j *Journal) logErr(label string, err error) {
	if err == nil {
		return
	}
	j.logger.Warn("engine.Journal: error", "label", label, "err", err)
}

// AttachFindings registers a FindingsObserver that journals every
// raise / transition / forget. Per J5: the observer body is a
// constant-time chan send and returns immediately.
func (j *Journal) AttachFindings(store *runner.FindingsStore) {
	store.Observe(func(e runner.FindingsEvent) {
		j.enqueue(journalEvent{kind: jKindFinding, finding: e})
	})
}

func (j *Journal) handleFinding(ctx context.Context, e runner.FindingsEvent) {
	switch e.Kind {
	case runner.FindingsEventRaise:
		rec := findingToRecord(e.After, e.Version, j.clock)
		j.logErr("UpsertFinding(raise)", j.store.UpsertFinding(ctx, rec))

	case runner.FindingsEventTransition:
		rec := findingToRecord(e.After, e.Version, j.clock)
		j.logErr("UpsertFinding(transition)", j.store.UpsertFinding(ctx, rec))
		// J3 + J4: use the transition's own timestamp / role /
		// reason rather than the original raise's.
		j.logErr("InsertTransition", j.store.InsertTransition(ctx, TransitionRecord{
			FindingID:    e.After.ID,
			FromStatus:   e.Before.Status.String(),
			ToStatus:     e.After.Status.String(),
			Role:         e.Role,
			Reason:       e.Reason,
			StoreVersion: e.Version,
			At:           e.At,
		}))

	case runner.FindingsEventForget, runner.FindingsEventForgetArrow:
		id := e.Before.ID
		if id == "" {
			return
		}
		j.logErr("DeleteFinding", j.store.DeleteFinding(ctx, id))
	}
}

// findingToRecord projects runner.FindingRecord into the engine's
// persistence shape. clock injection (J11) lets tests pin timestamps.
func findingToRecord(f runner.FindingRecord, storeVer uint64, clock func() time.Time) FindingRecord {
	return FindingRecord{
		ID:              f.ID,
		ArrowID:         f.ArrowID,
		Type:            string(f.Type),
		Severity:        f.Severity,
		Status:          f.Status.String(),
		Description:     f.Description,
		RaisedAt:        f.RaisedAt,
		RaisedByRole:    f.RaisedByRole,
		TransitionCount: f.TransitionCount,
		StoreVersion:    storeVer,
		UpdatedAt:       clock().UTC().Format(time.RFC3339Nano),
	}
}

// AttachClassifications registers a ClassificationsObserver.
func (j *Journal) AttachClassifications(store *runner.ClassificationsStore) {
	store.Observe(func(e runner.ClassificationsEvent) {
		j.enqueue(journalEvent{kind: jKindClassification, classification: e})
	})
}

func (j *Journal) handleClassification(ctx context.Context, e runner.ClassificationsEvent) {
	now := j.clock().UTC().Format(time.RFC3339Nano)
	switch e.Kind {
	case runner.ClassificationsEventDeclare:
		j.logErr("UpsertRequirement", j.store.UpsertRequirement(ctx, RequirementRecord{
			ArrowID:      e.ArrowID,
			ReqID:        e.RequirementID,
			MinDepth:     int(e.Requirement.MinDepth),
			Description:  e.Requirement.Description,
			StoreVersion: e.Version,
			DeclaredAt:   now,
		}))

	case runner.ClassificationsEventRecord:
		j.logErr("UpsertClassification", j.store.UpsertClassification(ctx, ClassificationRecord{
			ArrowID:      e.ArrowID,
			ReqID:        e.RequirementID,
			Observed:     int(e.After.Observed),
			Evidence:     e.After.Evidence,
			StoreVersion: e.Version,
			ClassifiedAt: now,
		}))

	case runner.ClassificationsEventOverwrite:
		// J14: UpsertClassification's conflict-update path now
		// increments overwrite_count automatically.
		j.logErr("UpsertClassification(overwrite)",
			j.store.UpsertClassification(ctx, ClassificationRecord{
				ArrowID:      e.ArrowID,
				ReqID:        e.RequirementID,
				Observed:     int(e.After.Observed),
				Evidence:     e.After.Evidence,
				StoreVersion: e.Version,
				ClassifiedAt: now,
			}))
		j.logErr("InsertOverwrite",
			j.store.InsertOverwrite(ctx, OverwriteRecord{
				ArrowID:        e.ArrowID,
				ReqID:          e.RequirementID,
				BeforeObserved: int(e.Before.Observed),
				AfterObserved:  int(e.After.Observed),
				StoreVersion:   e.Version,
				At:             now,
			}))

	case runner.ClassificationsEventForget:
		j.logErr("DeleteRequirement",
			j.store.DeleteRequirement(ctx, e.ArrowID, e.RequirementID))

	case runner.ClassificationsEventForgetArrow:
		j.logErr("DeleteArrow", j.store.DeleteArrow(ctx, e.ArrowID))
	}
}

// AttachGrid registers a GridObserver.
func (j *Journal) AttachGrid(g *runner.Grid) {
	g.Observe(func(e runner.GridEvent) {
		j.enqueue(journalEvent{kind: jKindGrid, grid: e})
	})
}

func (j *Journal) handleGrid(ctx context.Context, e runner.GridEvent) {
	kind := "append"
	if e.Kind == runner.GridEventOnTheSpotAppend {
		kind = "on-the-spot"
	}
	rec := GridArrowRecord{
		ID:               e.ArrowID,
		GridVersion:      e.Version,
		SourceRole:       e.Definition.SourceRole,
		TargetRole:       e.Definition.TargetRole,
		Stratum:          e.Definition.Stratum,
		Context:          e.Definition.Context,
		ClausesJSON:      JSONSlice(e.Definition.Clauses),
		RequirementsJSON: JSONSlice(e.Definition.Requirements),
		Kind:             kind,
		DeclaredAt:       j.clock().UTC().Format(time.RFC3339Nano),
	}
	j.logErr("InsertGridArrow", j.store.InsertGridArrow(ctx, rec))
}

// AttachAmendments registers an AmendmentObserver.
func (j *Journal) AttachAmendments(q *runner.AmendmentQueue) {
	q.Observe(func(e runner.AmendmentEvent) {
		j.enqueue(journalEvent{kind: jKindAmendment, amendment: e})
	})
}

// AttachAttestations registers an AttestationObserver per ADR-010.
// Every Record on the in-memory store gets persisted by the
// consumer goroutine.
func (j *Journal) AttachAttestations(store *runner.AttestationStore) {
	store.Observe(func(e runner.AttestationEvent) {
		j.enqueue(journalEvent{kind: jKindAttestation, attestation: e})
	})
}

// handleAttestation persists one attestation record.
func (j *Journal) handleAttestation(ctx context.Context, e runner.AttestationEvent) {
	switch e.Kind {
	case runner.AttestationEventRecord:
		j.logErr("insertAttestation", j.store.insertAttestation(ctx, e.Record))
	}
}

func (j *Journal) handleAmendment(ctx context.Context, e runner.AmendmentEvent) {
	switch e.Kind {
	case runner.AmendmentEventEnqueue:
		j.logErr("UpsertAmendment(enqueue)",
			j.store.UpsertAmendment(ctx, AmendmentRecord{
				ID:             e.Request.ID,
				Reason:         string(e.Request.Reason),
				SourceArrow:    e.Request.SourceArrow,
				TargetRole:     e.Request.TargetRole,
				ContextsJSON:   JSONSlice(e.Request.Contexts),
				Description:    e.Request.Description,
				FindingIDsJSON: JSONSlice(e.Request.FindingIDs),
				CreatedAt:      e.Request.CreatedAt,
			}))

	case runner.AmendmentEventDrain:
		// J7: wrap the drain in a transaction so concurrent
		// ListAmendments(drained=true) readers see all rows or none.
		drainedAt := j.clock().UTC().Format(time.RFC3339Nano)
		j.logErr("DrainAmendments", j.drainAmendments(ctx, e.Drained, drainedAt))

	case runner.AmendmentEventReset:
		// J2: Reset is in-memory ONLY. Wiping drained_at would
		// destroy F44 dedup across process restart. The journal
		// log here is the only persistent signal of the operator's
		// Reset intent.
		j.logger.Info("engine.Journal: AmendmentEventReset is in-memory only; persistence retained")
	}
}

// drainAmendments writes all drained rows in a single transaction
// so readers see atomic drain semantics (J7).
func (j *Journal) drainAmendments(ctx context.Context, drained []runner.AmendmentRequest, at string) error {
	if len(drained) == 0 {
		return nil
	}
	tx, err := j.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range drained {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO amendments (
				id, reason, source_arrow, target_role,
				contexts_json, description, finding_ids_json,
				created_at, drained_at
			) VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET drained_at = excluded.drained_at
		`, r.ID, string(r.Reason), r.SourceArrow, r.TargetRole,
			JSONSlice(r.Contexts), r.Description, JSONSlice(r.FindingIDs),
			r.CreatedAt, at)
		if err != nil {
			return fmt.Errorf("upsert %s: %w", safeID(r.ID), err)
		}
	}
	return tx.Commit()
}

// AttachRunner registers an EvaluationRun observer.
func (j *Journal) AttachRunner(r *runner.Runner) {
	r.OnEvaluationRun(func(run *runner.EvaluationRun) {
		if run == nil {
			return
		}
		j.enqueue(journalEvent{kind: jKindRun, run: run})
	})
}

func (j *Journal) handleRun(ctx context.Context, run *runner.EvaluationRun) {
	rec := EvaluationRunRecord{
		ID:                      run.ID,
		ClauseID:                run.ClauseID,
		PassID:                  run.PassID,
		ArrowID:                 run.ArrowID,
		GridVersion:             run.GridVersion,
		DepthTypeAttestationRef: run.DepthTypeAttestationRef,
		ActualTier:              int(run.ActualTier),
		MinDepthTier:            int(run.MinDepthTier),
		EvaluatorConcept:        run.Evaluator.Concept,
		EvaluatorGeneration:     run.Evaluator.Generation,
		StartedAt:               run.StartedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt:             run.CompletedAt.UTC().Format(time.RFC3339Nano),
		StartStatus:             run.StartStatus.String(),
		EndStatus:               run.EndStatus.String(),
		ResultJSON:              JSONObject(run.Result),
		RunError:                run.RunError,
	}
	j.logErr("InsertEvaluationRun", j.store.InsertEvaluationRun(ctx, rec))
}

// newNullString wraps a non-empty string in sql.NullString.
func newNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Unused but retained for potential future engine APIs that need
// to mark a row drained via the typed nullable.
var _ = newNullString
