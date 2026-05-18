// Package runner is the v2 enforcement spine. It invokes machine
// evaluators, tracks per-clause status through the lifecycle, and
// records evaluation runs.
//
// This file ships the evaluator dispatch surface and the clause
// state machine. Built-in evaluators for universal-base concepts
// live in sibling files (e.g., notodo.go for no-todo-marker).
//
// Per build-notes.md step 3, the runner sits behind the bootstrap
// init component: a grid + bindings declared at init is the input;
// evaluation runs are the output.
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ClauseStatus is the lifecycle state of a single clause within one
// pass execution. Per gates.md §7.1. The state machine is:
//
//	pending ──► running ──► pass | fail | unevaluated
//
// `awaiting-attestation` (for attested clauses) and the post-finding
// transitions (`blocked` etc.) live on the higher state-machine
// component; the runner emits the post-evaluation status and lets
// arrow-status derivation derive the rest.
type ClauseStatus int

const (
	StatusPending ClauseStatus = iota
	StatusRunning
	StatusPass
	StatusFail
	StatusUnevaluated
)

// String returns the wire form used in attestation records and the
// pass log.
func (s ClauseStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusPass:
		return "pass"
	case StatusFail:
		return "fail"
	case StatusUnevaluated:
		return "unevaluated"
	default:
		return "unknown"
	}
}

// validTransitions lists the legal next states for each ClauseStatus.
// The runner only drives transitions through this map; an attempt to
// move along an unlisted edge returns ErrInvalidTransition.
var validTransitions = map[ClauseStatus]map[ClauseStatus]struct{}{
	StatusPending: {
		StatusRunning:     {},
		StatusUnevaluated: {}, // skipped before run (depth-below-required)
	},
	StatusRunning: {
		StatusPass:        {},
		StatusFail:        {},
		StatusUnevaluated: {}, // evaluator returned unevaluated
	},
}

// CanTransition reports whether a status->status transition is legal.
func CanTransition(from, to ClauseStatus) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	_, ok = allowed[to]
	return ok
}

// Runner errors.
var (
	ErrInvalidTransition  = errors.New("runner-invalid-transition")
	ErrEvaluatorUnknown   = errors.New("runner-evaluator-unknown")
	ErrEvaluatorPanicked  = errors.New("runner-evaluator-panicked")
	ErrEvaluatorReturnNil = errors.New("runner-evaluator-returned-nil")
)

// Result is one evaluator's return value. Pass is the boolean verdict;
// Details is concept-specific and may be nil for evaluators that
// produce no additional payload. Unevaluated takes precedence over
// Pass: when the evaluator cannot decide the clause (e.g., the
// underlying tool produced no usable signal), it returns Unevaluated
// = true with a Reason; Pass is ignored in that case.
type Result struct {
	Pass        bool
	Details     map[string]any
	Unevaluated bool
	Reason      string // non-empty when Unevaluated is true
}

// Clause is the input to an evaluator: a concept name plus the
// operator-confirmed arguments. ProjectDir scopes filesystem-bound
// evaluators (e.g., no-todo-marker walks projectDir/<scope>).
type Clause struct {
	Concept    string
	Args       map[string]any
	ProjectDir string
}

// Evaluator decides one machine clause. Returns the verdict + details
// or an error if the evaluator could not run at all. Errors from the
// evaluator propagate to the runner, which records the clause as
// failed-to-evaluate (an operational error, not a clause-status
// transition).
type Evaluator func(ctx context.Context, c Clause) (*Result, error)

// Registry is the dispatcher from concept name to Evaluator.
//
// Built-in evaluators (universal-base concepts like no-todo-marker,
// every-step-bound) register themselves via init() in their
// respective files. Project-declared evaluators (language bindings:
// lint-clean.go, tests-pass.python, etc.) are registered explicitly
// from the grid's LanguageBindings.
type Registry struct {
	mu sync.RWMutex
	by map[string]Evaluator
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{by: make(map[string]Evaluator)}
}

// Register associates an evaluator with a concept name. Re-registering
// overwrites silently — bindings declared late in init may amend an
// earlier registration.
func (r *Registry) Register(concept string, e Evaluator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.by[concept] = e
}

// Lookup returns the evaluator for the named concept, or nil + false.
func (r *Registry) Lookup(concept string) (Evaluator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.by[concept]
	return e, ok
}

// Count returns the number of registered evaluators.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.by)
}

// EvaluationRun is the persistent record of one evaluator invocation.
// Per gates.md §7.1a + the runner.md design: identifies the clause,
// the pass, when it started/finished, and the result.
type EvaluationRun struct {
	ID          string
	ClauseID    string
	PassID      string
	StartedAt   time.Time
	CompletedAt time.Time
	StartStatus ClauseStatus
	EndStatus   ClauseStatus
	Result      *Result
}

// Duration returns the wall-clock time the evaluator ran.
func (e *EvaluationRun) Duration() time.Duration {
	if e == nil {
		return 0
	}
	return e.CompletedAt.Sub(e.StartedAt)
}

// Runner orchestrates a single-clause evaluation. The runner type
// holds the evaluator registry; instances are cheap and intended to
// be created per arrow-pass.
type Runner struct {
	Registry *Registry

	// now is the runner's clock; abstracted so tests can pin
	// timestamps. Defaults to time.Now when zero.
	now func() time.Time

	// idgen returns a fresh evaluation-run-id. Defaults to a
	// timestamp-derived string when nil.
	idgen func() string
}

// NewRunner returns a Runner backed by the given registry. now and
// idgen default to time.Now and a timestamp-derived id generator.
func NewRunner(reg *Registry) *Runner {
	return &Runner{
		Registry: reg,
		now:      time.Now,
		idgen:    defaultIDGen,
	}
}

// WithClock overrides the runner's clock for tests.
func (r *Runner) WithClock(now func() time.Time) *Runner {
	r.now = now
	return r
}

// WithIDGen overrides the runner's evaluation-run-id generator.
func (r *Runner) WithIDGen(g func() string) *Runner {
	r.idgen = g
	return r
}

// Evaluate runs one clause through the lifecycle. Returns the
// EvaluationRun record (which carries the final status + result).
//
// Status transitions are driven through CanTransition so an
// invalid edge produces ErrInvalidTransition rather than a silent
// status corruption.
//
// Evaluator panics are caught and reported as ErrEvaluatorPanicked
// with a fail-status run record (panicking evaluators are binding
// bugs the operator must fix, not silent clause failures).
func (r *Runner) Evaluate(ctx context.Context, clauseID, passID string, c Clause) (*EvaluationRun, error) {
	if r.Registry == nil {
		return nil, errors.New("Evaluate: runner has no Registry")
	}
	if clauseID == "" || passID == "" {
		return nil, errors.New("Evaluate: clauseID and passID must be non-empty")
	}
	evaluator, ok := r.Registry.Lookup(c.Concept)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrEvaluatorUnknown, c.Concept)
	}

	startedAt := r.now()
	startStatus := StatusPending
	if !CanTransition(startStatus, StatusRunning) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, startStatus, StatusRunning)
	}
	runStatus := StatusRunning

	result, err := safeInvoke(ctx, evaluator, c)
	endStatus, runErr := r.deriveEndStatus(runStatus, result, err)
	completedAt := r.now()

	return &EvaluationRun{
		ID:          r.idgen(),
		ClauseID:    clauseID,
		PassID:      passID,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		StartStatus: startStatus, // observable lifecycle starts at pending
		EndStatus:   endStatus,
		Result:      result,
	}, runErr
}

// deriveEndStatus maps the evaluator's (result, err) pair to the
// terminal ClauseStatus. The transition (running → end) is validated;
// unreachable edges shouldn't happen given validTransitions but are
// defended.
func (r *Runner) deriveEndStatus(from ClauseStatus, result *Result, err error) (ClauseStatus, error) {
	if err != nil {
		// Evaluator-side error: not a clause-status transition;
		// surface it as a fail with the error attached on the result
		// payload? For now, surface as a runner-level error and a
		// fail status. The caller can decide whether to retry.
		return StatusFail, err
	}
	if result == nil {
		return StatusFail, ErrEvaluatorReturnNil
	}
	var to ClauseStatus
	switch {
	case result.Unevaluated:
		to = StatusUnevaluated
	case result.Pass:
		to = StatusPass
	default:
		to = StatusFail
	}
	if !CanTransition(from, to) {
		return from, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	return to, nil
}

// safeInvoke calls the evaluator with a panic guard. Panics become
// ErrEvaluatorPanicked errors so a broken binding doesn't crash the
// runner.
func safeInvoke(ctx context.Context, e Evaluator, c Clause) (result *Result, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%w: %v", ErrEvaluatorPanicked, rec)
			result = nil
		}
	}()
	return e(ctx, c)
}

// defaultIDGen returns a sortable timestamp-derived id. Format:
// "ev-YYYYMMDD-HHMMSS-nanos" so log readers can group by pass and
// time at a glance. Not cryptographically random; the runner does
// not need that property.
func defaultIDGen() string {
	t := time.Now().UTC()
	return fmt.Sprintf("ev-%04d%02d%02d-%02d%02d%02d-%09d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(),
		t.Nanosecond())
}
