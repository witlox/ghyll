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
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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
// pass log. Out-of-range values surface as "invalid-clause-status"
// (loud, unambiguous corruption signal — validation-pass-2 F56) so
// they don't look like a legitimate state to downstream readers.
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
		return fmt.Sprintf("invalid-clause-status(%d)", int(s))
	}
}

// validTransitions lists the legal next states for each ClauseStatus.
// The runner only drives transitions through this map; an attempt to
// move along an unlisted edge returns ErrInvalidTransition.
//
// Validation-pass-3 F29: the pending→unevaluated edge previously
// allowed for a depth-below-required short-circuit was dead code
// (Evaluate always transitions to running first). Removed; if a
// depth-gate short-circuit is added later, the edge comes back
// with the wiring that drives it.
var validTransitions = map[ClauseStatus]map[ClauseStatus]struct{}{
	StatusPending: {
		StatusRunning: {},
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
	ErrEvaluatorContract  = errors.New("runner-evaluator-contract-violation")
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
//
// ClauseID and PassID identify the clause's containing record so
// subprocess evaluators (future) can tag their logs with the same
// IDs the runner uses in EvaluationRun. Per validation-pass-2 F59.
type Clause struct {
	Concept    string
	Args       map[string]any
	ProjectDir string
	ClauseID   string
	PassID     string

	// ArrowID identifies the arrow this clause belongs to per
	// gates.md §7.1a. Optional in v1 (zero string acceptable); the
	// phase-5 engine populates it so persisted EvaluationRun records
	// are self-keyed without a retrofit.
	ArrowID string

	// GridVersion is the bump-counter for the engine's grid at the
	// time of evaluation. Zero when no engine is attached.
	GridVersion uint64

	// DepthType declares the model-tier requirement per gates.md §6:
	// depth-robust (any tier) or depth-sensitive (at or above
	// MinDepthTier). Authoring-time field; the runner's router
	// reads it at arrow traversal time (see runner/routing.go).
	DepthType ClauseDepthType

	// MinDepthTier is the minimum depth tier required for a
	// depth-sensitive clause to be honestly evaluated. Ignored when
	// DepthType is depth-robust. Uses the DepthRank scale (0..3).
	MinDepthTier DepthRank
}

// Evaluator decides one machine clause. Returns the verdict + details
// or an error if the evaluator could not run at all. Errors from the
// evaluator propagate to the runner, which records the clause as
// failed-to-evaluate (an operational error, not a clause-status
// transition).
type Evaluator func(ctx context.Context, c Clause) (*Result, error)

// EvaluatorIdentity is a stable token identifying a specific evaluator
// registration. Carried in EvaluationRun so attestation can pin the
// exact binding that produced a result, even across hot-swaps. Per
// validation-pass-2 F14.
type EvaluatorIdentity struct {
	Concept    string // the catalogue concept name
	Generation int64  // increments on each Register call for this concept
}

// String returns "concept@gen" for log lines and audit records.
func (e EvaluatorIdentity) String() string {
	return fmt.Sprintf("%s@%d", e.Concept, e.Generation)
}

// registered holds an Evaluator plus its identity for attestation.
type registered struct {
	fn       Evaluator
	identity EvaluatorIdentity
}

// Registry is the dispatcher from concept name to Evaluator.
//
// Built-in evaluators (universal-base concepts like no-todo-marker,
// every-step-bound) register themselves via RegisterBuiltins from
// the caller. Project-declared evaluators (language bindings:
// lint-clean.go, tests-pass.python, etc.) are registered explicitly
// from the grid's LanguageBindings.
//
// Registry distinguishes Register (one-shot; refuses if already
// registered) from Replace (explicit hot-swap; bumps Generation).
// validation-pass-2 F14 — silent overwrite was a no-audit gap.
type Registry struct {
	mu  sync.RWMutex
	by  map[string]registered
	gen map[string]int64 // monotonic generation counter per concept
}

// Registry errors.
var (
	ErrConceptAlreadyRegistered = errors.New("runner-concept-already-registered")
	ErrConceptNotRegistered     = errors.New("runner-concept-not-registered")
)

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		by:  make(map[string]registered),
		gen: make(map[string]int64),
	}
}

// Register associates an evaluator with a concept name. Refuses with
// ErrConceptAlreadyRegistered if the concept is already registered;
// use Replace for an explicit hot-swap.
func (r *Registry) Register(concept string, e Evaluator) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.by[concept]; exists {
		return fmt.Errorf("%w: %s", ErrConceptAlreadyRegistered, concept)
	}
	r.gen[concept] = 1
	r.by[concept] = registered{
		fn: e,
		identity: EvaluatorIdentity{
			Concept:    concept,
			Generation: 1,
		},
	}
	return nil
}

// Replace overwrites an existing registration and bumps the
// Generation counter so subsequent EvaluationRun records carry the
// new identity. Used during init re-entry when an operator amends a
// binding (D18). Returns ErrConceptNotRegistered (validation-pass-3
// F10 — was misnamed ErrConceptAlreadyRegistered in the docstring)
// if the concept is not registered yet; use Register first.
func (r *Registry) Replace(concept string, e Evaluator) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.by[concept]; !exists {
		return fmt.Errorf("%w: %s", ErrConceptNotRegistered, concept)
	}
	r.gen[concept]++
	r.by[concept] = registered{
		fn: e,
		identity: EvaluatorIdentity{
			Concept:    concept,
			Generation: r.gen[concept],
		},
	}
	return nil
}

// Lookup returns the evaluator and its identity for the named concept.
// The identity captures which Generation of the registration was
// observed, so a concurrent Replace does not invalidate the
// EvaluationRun's attestation.
func (r *Registry) Lookup(concept string) (Evaluator, EvaluatorIdentity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reg, ok := r.by[concept]
	if !ok {
		return nil, EvaluatorIdentity{}, false
	}
	return reg.fn, reg.identity, true
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
//
// Fields are exported for serialization. Callers MUST NOT mutate
// fields after Evaluate returns (validation-pass-2 F58); the
// EvaluationRun is a snapshot intended for attestation. To preserve
// chain-of-custody, hash the record at the boundary before persisting
// — any later mutation is then detectable.
//
// EvaluatorIdentity captures which Generation of the registry's
// binding produced this result (F14). A concurrent Replace between
// the Lookup and the evaluator return does not invalidate this — the
// identity is captured under the read lock with the function.
//
// RunError carries the operational error (broken binding, evaluator
// panic, etc.) so an EndStatus of Fail can be distinguished from a
// real clause-fail in persisted records (F15). It is also a string
// (not error) so the record serializes deterministically.
type EvaluationRun struct {
	ID          string
	ClauseID    string
	PassID      string
	ArrowID     string // gates.md §7.1a arrow identity — populated from Clause.ArrowID
	GridVersion uint64 // bump-counter from the engine's grid; 0 if unset
	Evaluator   EvaluatorIdentity
	StartedAt   time.Time
	CompletedAt time.Time
	StartStatus ClauseStatus
	EndStatus   ClauseStatus
	Result      *Result
	RunError    string
}

// Duration returns the wall-clock time the evaluator ran.
func (e *EvaluationRun) Duration() time.Duration {
	if e == nil {
		return 0
	}
	return e.CompletedAt.Sub(e.StartedAt)
}

// runIDCounter is a process-wide atomic counter that guarantees
// unique EvaluationRun.IDs even when two Evaluate calls observe the
// same nanosecond from time.Now() (validation-pass-2 F43).
var runIDCounter atomic.Uint64

// Runner orchestrates a single-clause evaluation. The runner type
// holds the evaluator registry; instances are cheap and intended to
// be created per arrow-pass.
type Runner struct {
	Registry *Registry

	// now is the runner's clock; abstracted so tests can pin
	// timestamps. Defaults to time.Now when zero.
	now func() time.Time

	// idgen returns a fresh evaluation-run-id. Defaults to
	// defaultIDGen (timestamp + monotonic counter).
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
// Evaluator panics are caught via deferred recover() and reported as
// ErrEvaluatorPanicked with a fail-status run record. CAVEATS
// (validation-pass-2 F16 + validation-pass-3 F9):
//   - recover() only catches panics on the goroutine it's deferred
//     in. An evaluator that spawns its own goroutines and panics
//     there will crash the process. Evaluators MUST NOT spawn
//     goroutines that outlive the call.
//   - A stack-overflow panic may be unrecoverable depending on
//     remaining stack budget. Evaluators MUST NOT recurse unbounded.
//   - runtime.Goexit is NOT caught — recover() returns nil during
//     Goexit, the named returns stay at zero, and Evaluate's
//     goroutine vanishes without producing an EvaluationRun.
//     Evaluators MUST NOT call runtime.Goexit (or trigger it via
//     testing.T.FailNow / similar).
//
// Future subprocess-based evaluators (language bindings) get
// stronger isolation than the in-process built-ins.
//
// Clause's ClauseID and PassID are populated from the function
// arguments if the caller passed zero values, so subprocess
// evaluators (future) see them in c.ClauseID / c.PassID.
func (r *Runner) Evaluate(ctx context.Context, clauseID, passID string, c Clause) (*EvaluationRun, error) {
	if r.Registry == nil {
		return nil, errors.New("Evaluate: runner has no Registry")
	}
	if clauseID == "" || passID == "" {
		return nil, errors.New("Evaluate: clauseID and passID must be non-empty")
	}
	evaluator, identity, ok := r.Registry.Lookup(c.Concept)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrEvaluatorUnknown, c.Concept)
	}
	// Populate the clause's IDs so the evaluator sees them.
	if c.ClauseID == "" {
		c.ClauseID = clauseID
	}
	if c.PassID == "" {
		c.PassID = passID
	}
	// Validation-pass-3 F27: shallow-clone the Args map so a
	// mutating evaluator can't leak state across invocations of
	// the same clause. The caller's map is preserved verbatim.
	if c.Args != nil {
		cloned := make(map[string]any, len(c.Args))
		for k, v := range c.Args {
			cloned[k] = v
		}
		c.Args = cloned
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

	var runErrText string
	if runErr != nil {
		runErrText = runErr.Error()
	}

	// Validation-pass-3 F26: snapshot Result.Details into a fresh
	// map so post-return mutation by the evaluator can't corrupt
	// the persisted EvaluationRun.
	if result != nil && result.Details != nil {
		result = &Result{
			Pass:        result.Pass,
			Details:     snapshotDetails(result.Details),
			Unevaluated: result.Unevaluated,
			Reason:      result.Reason,
		}
	}

	return &EvaluationRun{
		ID:          r.idgen(),
		ClauseID:    clauseID,
		PassID:      passID,
		ArrowID:     c.ArrowID,
		GridVersion: c.GridVersion,
		Evaluator:   identity,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		StartStatus: startStatus, // observable lifecycle starts at pending
		EndStatus:   endStatus,
		Result:      result,
		RunError:    runErrText,
	}, runErr
}

// snapshotDetails returns a deep copy of the Details map so that a
// retaining evaluator (or future binding) cannot mutate the
// persisted EvaluationRun. Recursive over map[string]any and []any.
// Validation-pass-3 F26.
func snapshotDetails(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = snapshotValue(v)
	}
	return out
}

func snapshotValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return snapshotDetails(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = snapshotValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(x))
		for i, item := range x {
			out[i] = snapshotDetails(item)
		}
		return out
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	}
	return v // scalars: string, int, float, bool, nil
}

// deriveEndStatus maps the evaluator's (result, err) pair to the
// terminal ClauseStatus. The transition (running → end) is validated;
// unreachable edges shouldn't happen given validTransitions but are
// defended.
//
// Validation-pass-3 F11: enforces Result invariants. An evaluator
// returning {Unevaluated:true, Reason:""} is treated as a contract
// violation (the operator cannot triage an unevaluated clause with
// no justification). {Unevaluated:true, Pass:true} is also a
// violation (the two are exclusive per Result's docstring).
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
	// F11: enforce invariants documented on Result.
	if result.Unevaluated {
		if result.Reason == "" {
			return StatusFail, fmt.Errorf("%w: Unevaluated=true requires non-empty Reason",
				ErrEvaluatorContract)
		}
		if result.Pass {
			return StatusFail, fmt.Errorf("%w: Unevaluated and Pass are mutually exclusive",
				ErrEvaluatorContract)
		}
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

// processIDPrefix is a per-process random hex string seeded once at
// package init. Appended to every EvaluationRun.ID so two concurrent
// processes (runner + vault replay, two ghyll sessions in different
// repos) cannot produce identical IDs even when their wall clocks
// and counters align. Validation-pass-3 F28.
var processIDPrefix = func() string {
	var b [4]byte
	_, _ = cryptorand.Read(b[:])
	return hex.EncodeToString(b[:])
}()

// defaultIDGen returns a sortable timestamp-derived id with the
// process prefix and a monotonic counter appended: "ev-YYYYMMDD-
// HHMMSS-nanos-<procprefix>-<counter>". Two Evaluate calls in the
// same nanosecond on the same process produce different IDs via
// the counter; concurrent processes don't collide via the random
// prefix.
func defaultIDGen() string {
	t := time.Now().UTC()
	n := runIDCounter.Add(1)
	return fmt.Sprintf("ev-%04d%02d%02d-%02d%02d%02d-%09d-%s-%d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(),
		t.Nanosecond(), processIDPrefix, n)
}
