// Adversarial-cycle wiring on the dispatcher (diamond v4 / Gap 1).
//
// This file is the production-side entry to the §11 adversarial
// cycle. Pre-v4, the cycle was reachable only from tests because the
// dispatcher's Dispatch path ran verification clauses and returned;
// the spec's intent (clause partitioning, cycle invocation, status
// derivation) was unenforced.
//
// The integration boundary lives in `cmd/ghyll` (ADR-v4-007) which
// constructs the AdversarialHooks bundle from the active dialect; the
// runner-side surface is the helpers below.

package runner

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
)

// AdversarialHooks groups the four LLM-backed hooks the cycle needs
// plus the remediation defaults. Stored on the dispatcher as an
// atomic.Pointer so /adversary enable/disable swaps the bundle
// race-cleanly (design-M3 closure).
type AdversarialHooks struct {
	Factory                   func(round int) *Adversary
	OpenSweep                 OpenSweepFn
	Classify                  DepthClassifyFn
	ProducerFix               ProducerFn
	RemediationConfigDefaults RemediationConfig
}

// Validate reports whether the bundle has all four hooks wired. A
// nil receiver or missing hook returns false.
func (h *AdversarialHooks) Validate() bool {
	if h == nil {
		return false
	}
	return h.Factory != nil && h.OpenSweep != nil && h.Classify != nil && h.ProducerFix != nil
}

// Dispatcher errors introduced by the diamond v4 wiring.
var (
	// ErrAdversaryHooksNotWired is returned when a depth-sensitive
	// dispatch is attempted but the dispatcher's adversarial-hooks
	// bundle is nil (or incomplete). Operators see this when
	// /adversary disable was typed AND a depth-sensitive arrow is
	// then run.
	ErrAdversaryHooksNotWired = errors.New("dispatcher: adversarial-hooks-not-wired")

	// ErrDispatchNoAuditSubscriber is returned at the top of Dispatch
	// when the bus has no audit-tagged subscriber (R6 closure). A
	// dispatch whose lifecycle would not be persisted to the JSONL
	// audit log is refused before the counter spins.
	ErrDispatchNoAuditSubscriber = errors.New("dispatcher: no-audit-subscriber")

	// ErrDispatchRecursionExceeded is returned when the dispatcher's
	// recursion depth (carried in ctx) exceeds MaxRecursiveDispatch.
	// Defense-in-depth (R11 closure): today no codepath exceeds
	// depth 1; the budget guards future surfaces (operator-wired
	// OpenSweep hooks that recursively dispatch sub-arrows).
	ErrDispatchRecursionExceeded = errors.New("dispatcher: dispatch-recursion-exceeded")
)

// dispatcherRecursionDepthKey is the ctx key under which the
// dispatcher carries the current recursion depth. Local type avoids
// collisions per the context.Context convention.
type dispatcherRecursionDepthKeyT struct{}

var dispatcherRecursionDepthKey = dispatcherRecursionDepthKeyT{}

// PartitionClauses splits a clause set into the depth-sensitive
// partition and the depth-robust partition. The partition predicate
// is `c.DepthType == DepthTypeSensitive` per gates.md §11 and
// runner/routing.go:36-37 (C1 closure: this is NOT
// `MinDepthTier > DepthRankNone` — depth-robust may carry
// MinDepthTier == None and a mis-declared depth-sensitive must
// still go through the cycle).
func PartitionClauses(clauses []Clause) (sensitive, robust []Clause) {
	for _, c := range clauses {
		if c.DepthType == DepthTypeSensitive {
			sensitive = append(sensitive, c)
		} else {
			robust = append(robust, c)
		}
	}
	return sensitive, robust
}

// RequireAuditSubscriber returns ErrDispatchNoAuditSubscriber if the
// bus is non-nil and has no audit-tagged subscriber. Nil bus is
// permitted (some test paths skip the floor).
func RequireAuditSubscriber(bus *OperatorBus) error {
	if bus == nil {
		return nil
	}
	if !bus.HasAuditSubscriber() {
		return ErrDispatchNoAuditSubscriber
	}
	return nil
}

// CheckRecursionBudget returns ErrDispatchRecursionExceeded if
// ctx-carried depth >= max. Depth defaults to 0.
func CheckRecursionBudget(ctx context.Context, maxDepth int) error {
	depth, _ := ctx.Value(dispatcherRecursionDepthKey).(int)
	if maxDepth > 0 && depth >= maxDepth {
		return fmt.Errorf("%w: depth=%d", ErrDispatchRecursionExceeded, depth)
	}
	return nil
}

// IncrementRecursionDepth returns a new ctx with the depth
// incremented by 1. The dispatcher calls this before any nested
// Dispatch (e.g., a sub-arrow created via the open-sweep hook).
func IncrementRecursionDepth(ctx context.Context) context.Context {
	depth, _ := ctx.Value(dispatcherRecursionDepthKey).(int)
	return context.WithValue(ctx, dispatcherRecursionDepthKey, depth+1)
}

// DefaultMaxRecursiveDispatch is the dispatcher's default recursion
// budget (defense-in-depth per R11 closure). Today no codepath
// exceeds depth 1; the budget guards future surfaces.
const DefaultMaxRecursiveDispatch = 4

// AtomicAdversarialHooks is an atomic.Pointer wrapper so the
// dispatcher can swap hook bundles race-cleanly when the operator
// types /adversary enable/disable. Zero value is "no hooks bundle"
// (Load returns nil).
type AtomicAdversarialHooks struct {
	p atomic.Pointer[AdversarialHooks]
}

// Load returns the current hooks bundle (nil if unset).
func (a *AtomicAdversarialHooks) Load() *AdversarialHooks {
	if a == nil {
		return nil
	}
	return a.p.Load()
}

// Store atomically swaps the hooks bundle. Pass nil to clear.
func (a *AtomicAdversarialHooks) Store(h *AdversarialHooks) {
	if a == nil {
		return
	}
	a.p.Store(h)
}
