package engine

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/witlox/ghyll/runner"
)

// Journal subscribes to the runner-layer Observer hooks and writes
// every mutation to the Store. It bridges in-memory runtime state
// to persistent storage; it does NOT decide policy. Replay-on-
// startup (engine/replay.go) reads the same tables back.
//
// Observer constraints (validation-pass-4/6 reminders): callbacks
// fire under the runner store's WRITE lock. They must be fast and
// non-blocking. The Journal therefore uses non-cancellable
// background context plus a short-timeout DB call; long-running
// writes would stall the runner.
//
// Errors are logged via the supplied Logger (or log.Default) and
// do NOT propagate — a transient sqlite error must not corrupt the
// runner's in-memory state. Replay reconciles at next startup.
type Journal struct {
	store   *Store
	logger  *log.Logger
	timeout time.Duration

	// background ctx for non-blocking writes. Cancelling Close
	// signals in-flight writes to abandon.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewJournal constructs a Journal bound to store. If logger is nil,
// log.Default() is used.
func NewJournal(store *Store, logger *log.Logger) *Journal {
	if store == nil {
		panic("engine.NewJournal: nil store")
	}
	if logger == nil {
		logger = log.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Journal{
		store:   store,
		logger:  logger,
		timeout: 5 * time.Second,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Close signals in-flight observer writes to abandon their context.
// The Store itself is owned externally; the journal does not close it.
func (j *Journal) Close() {
	j.cancel()
}

// logErr writes an error with a labelled prefix. Centralized so
// future-routing-to-metrics is one-line.
func (j *Journal) logErr(label string, err error) {
	if err == nil {
		return
	}
	j.logger.Printf("engine.Journal: %s: %v", label, err)
}

// AttachFindings registers a FindingsObserver that journals every
// raise / transition / forget. Idempotent at the engine layer: an
// already-Raised finding upsert in sqlite overwrites the row with
// the same values.
func (j *Journal) AttachFindings(store *runner.FindingsStore) {
	store.Observe(func(e runner.FindingsEvent) {
		ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
		defer cancel()
		switch e.Kind {
		case runner.FindingsEventRaise:
			rec := findingToRecord(e.After, e.Version)
			j.logErr("UpsertFinding(raise)", j.store.UpsertFinding(ctx, rec))

		case runner.FindingsEventTransition:
			rec := findingToRecord(e.After, e.Version)
			j.logErr("UpsertFinding(transition)", j.store.UpsertFinding(ctx, rec))
			j.logErr("InsertTransition", j.store.InsertTransition(ctx, TransitionRecord{
				FindingID:    e.After.ID,
				FromStatus:   e.Before.Status.String(),
				ToStatus:     e.After.Status.String(),
				Role:         e.After.RaisedByRole,
				Reason:       "",
				StoreVersion: e.Version,
				At:           e.After.RaisedAt,
			}))

		case runner.FindingsEventForget, runner.FindingsEventForgetArrow:
			id := e.Before.ID
			if id == "" {
				return
			}
			j.logErr("DeleteFinding", j.store.DeleteFinding(ctx, id))
		}
	})
}

// findingToRecord projects runner.FindingRecord into the engine's
// persistence shape.
func findingToRecord(f runner.FindingRecord, storeVer uint64) FindingRecord {
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
		UpdatedAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// AttachClassifications registers a ClassificationsObserver that
// journals declarations, recordings, overwrites, and forgets.
func (j *Journal) AttachClassifications(store *runner.ClassificationsStore) {
	store.Observe(func(e runner.ClassificationsEvent) {
		ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
		defer cancel()
		switch e.Kind {
		case runner.ClassificationsEventDeclare:
			j.logErr("UpsertRequirement", j.store.UpsertRequirement(ctx, RequirementRecord{
				ArrowID:      e.ArrowID,
				ReqID:        e.RequirementID,
				MinDepth:     int(e.Requirement.MinDepth),
				Description:  e.Requirement.Description,
				StoreVersion: e.Version,
				DeclaredAt:   time.Now().UTC().Format(time.RFC3339Nano),
			}))

		case runner.ClassificationsEventRecord:
			j.logErr("UpsertClassification", j.store.UpsertClassification(ctx, ClassificationRecord{
				ArrowID:      e.ArrowID,
				ReqID:        e.RequirementID,
				Observed:     int(e.After.Observed),
				Evidence:     e.After.Evidence,
				StoreVersion: e.Version,
				ClassifiedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}))

		case runner.ClassificationsEventOverwrite:
			j.logErr("UpsertClassification(overwrite)",
				j.store.UpsertClassification(ctx, ClassificationRecord{
					ArrowID:        e.ArrowID,
					ReqID:          e.RequirementID,
					Observed:       int(e.After.Observed),
					Evidence:       e.After.Evidence,
					OverwriteCount: 0, // engine doesn't track in-row; see audit table
					StoreVersion:   e.Version,
					ClassifiedAt:   time.Now().UTC().Format(time.RFC3339Nano),
				}))
			j.logErr("InsertOverwrite",
				j.store.InsertOverwrite(ctx, OverwriteRecord{
					ArrowID:        e.ArrowID,
					ReqID:          e.RequirementID,
					BeforeObserved: int(e.Before.Observed),
					AfterObserved:  int(e.After.Observed),
					StoreVersion:   e.Version,
					At:             time.Now().UTC().Format(time.RFC3339Nano),
				}))

		case runner.ClassificationsEventForget:
			j.logErr("DeleteRequirement",
				j.store.DeleteRequirement(ctx, e.ArrowID, e.RequirementID))

		case runner.ClassificationsEventForgetArrow:
			j.logErr("DeleteArrow", j.store.DeleteArrow(ctx, e.ArrowID))
		}
	})
}

// AttachGrid registers a GridObserver that journals every append /
// on-the-spot append. Definitions are JSON-serialized for storage;
// queryable fields are mirrored as columns.
func (j *Journal) AttachGrid(g *runner.Grid) {
	g.Observe(func(e runner.GridEvent) {
		ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
		defer cancel()
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
			ClausesJSON:      MustJSON(e.Definition.Clauses),
			RequirementsJSON: MustJSON(e.Definition.Requirements),
			Kind:             kind,
			DeclaredAt:       time.Now().UTC().Format(time.RFC3339Nano),
		}
		j.logErr("InsertGridArrow", j.store.InsertGridArrow(ctx, rec))
	})
}

// AttachAmendments registers an AmendmentObserver that journals
// enqueues and (via UPDATE) drains. Reset events clear the
// drained_at marker for all rows so a fresh session sees no stale
// state — operator-explicit reset semantics.
func (j *Journal) AttachAmendments(q *runner.AmendmentQueue) {
	q.Observe(func(e runner.AmendmentEvent) {
		ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
		defer cancel()
		switch e.Kind {
		case runner.AmendmentEventEnqueue:
			j.logErr("UpsertAmendment(enqueue)",
				j.store.UpsertAmendment(ctx, AmendmentRecord{
					ID:             e.Request.ID,
					Reason:         string(e.Request.Reason),
					SourceArrow:    e.Request.SourceArrow,
					TargetRole:     e.Request.TargetRole,
					ContextsJSON:   MustJSON(e.Request.Contexts),
					Description:    e.Request.Description,
					FindingIDsJSON: MustJSON(e.Request.FindingIDs),
					CreatedAt:      e.Request.CreatedAt,
				}))

		case runner.AmendmentEventDrain:
			drainedAt := time.Now().UTC().Format(time.RFC3339Nano)
			for _, r := range e.Drained {
				j.logErr("UpsertAmendment(drain)",
					j.store.UpsertAmendment(ctx, AmendmentRecord{
						ID:             r.ID,
						Reason:         string(r.Reason),
						SourceArrow:    r.SourceArrow,
						TargetRole:     r.TargetRole,
						ContextsJSON:   MustJSON(r.Contexts),
						Description:    r.Description,
						FindingIDsJSON: MustJSON(r.FindingIDs),
						CreatedAt:      r.CreatedAt,
						DrainedAt:      newNullString(drainedAt),
					}))
			}

		case runner.AmendmentEventReset:
			// Operator-explicit reset: clear drained_at via direct
			// UPDATE so subsequent queries don't see stale state.
			// The amendments themselves stay in the table for audit.
			j.logErr("ResetDrained", j.resetAmendmentDrained(ctx))
		}
	})
}

// resetAmendmentDrained clears the drained_at column on all
// amendments. Used by AmendmentEventReset.
func (j *Journal) resetAmendmentDrained(ctx context.Context) error {
	_, err := j.store.db.ExecContext(ctx, `UPDATE amendments SET drained_at = NULL`)
	if err != nil {
		return fmt.Errorf("reset drained: %w", err)
	}
	return nil
}

// AttachRunner registers an EvaluationRun observer on the runner so
// every completed clause evaluation is journaled.
func (j *Journal) AttachRunner(r *runner.Runner) {
	r.OnEvaluationRun(func(run *runner.EvaluationRun) {
		if run == nil {
			return
		}
		ctx, cancel := context.WithTimeout(j.ctx, j.timeout)
		defer cancel()
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
			ResultJSON:              MustJSON(run.Result),
			RunError:                run.RunError,
		}
		j.logErr("InsertEvaluationRun", j.store.InsertEvaluationRun(ctx, rec))
	})
}

// newNullString wraps a non-empty string in sql.NullString.
func newNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
