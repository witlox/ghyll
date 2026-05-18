package runner

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Grid is the runner's in-memory model of declared arrows. Per
// gates.md §3 it is the structured catalogue of role-pair transitions
// the harness expects to traverse. Each Append bumps the version
// counter so persistent engines can append-only journal changes.
//
// The runner's Grid is the runtime cache; the persistent disk grid
// (bootstrap/grid.go) is its source of truth at session boundaries.
//
// Validation pattern: structurally invalid arrow definitions are
// rejected at Append; downstream consumers can trust everything in
// the grid validates.

const (
	// maxArrowClauses bounds the per-arrow clause count (validation-
	// pass-6 F4). An LLM-backed definer can otherwise OOM the runner.
	maxArrowClauses = 256

	// maxArrowRequirements bounds the per-arrow requirement count.
	maxArrowRequirements = 256
)

// ArrowDefinition is one arrow in the grid. Captures the structural
// shape the runner needs at evaluation time.
type ArrowDefinition struct {
	ID           string
	SourceRole   string
	TargetRole   string
	Stratum      string
	Context      string
	Clauses      []Clause
	Requirements []Requirement
}

// Validate checks the definition is structurally well-formed.
// Per validation-pass-5/6 patterns: trim+nonempty for IDs and
// stratum/context (F2), per-clause concept presence, per-requirement
// Requirement.Validate, max-count caps (F4).
func (a ArrowDefinition) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("arrow-id-empty")
	}
	if strings.TrimSpace(a.SourceRole) == "" {
		return errors.New("arrow-source-role-empty")
	}
	if strings.TrimSpace(a.TargetRole) == "" {
		return errors.New("arrow-target-role-empty")
	}
	if strings.TrimSpace(a.Stratum) == "" {
		return errors.New("arrow-stratum-empty")
	}
	if strings.TrimSpace(a.Context) == "" {
		return errors.New("arrow-context-empty")
	}
	if len(a.Clauses) == 0 {
		return fmt.Errorf("arrow %q: no clauses declared", a.ID)
	}
	if len(a.Clauses) > maxArrowClauses {
		return fmt.Errorf("arrow %q: %d clauses exceeds max %d", a.ID, len(a.Clauses), maxArrowClauses)
	}
	if len(a.Requirements) > maxArrowRequirements {
		return fmt.Errorf("arrow %q: %d requirements exceeds max %d", a.ID, len(a.Requirements), maxArrowRequirements)
	}
	for i, c := range a.Clauses {
		if strings.TrimSpace(c.Concept) == "" {
			return fmt.Errorf("arrow %q clause[%d]: concept empty", a.ID, i)
		}
	}
	for i, r := range a.Requirements {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("arrow %q requirement[%d]: %w", a.ID, i, err)
		}
	}
	return nil
}

// deepCopyArrow returns an ArrowDefinition whose Clauses, each
// Clause.Args map, and Requirements share no storage with the
// input. Used at Append (so caller-side mutation can't poison
// stored state) and at Lookup (so consumer mutation can't poison
// the canonical copy).
func deepCopyArrow(a ArrowDefinition) ArrowDefinition {
	out := a
	if a.Clauses != nil {
		out.Clauses = make([]Clause, len(a.Clauses))
		for i, c := range a.Clauses {
			out.Clauses[i] = deepCopyClause(c)
		}
	}
	if a.Requirements != nil {
		out.Requirements = make([]Requirement, len(a.Requirements))
		copy(out.Requirements, a.Requirements)
	}
	return out
}

// deepCopyClause returns a Clause whose Args map shares no storage
// with the input. Other Clause fields are scalars (no aliasing).
func deepCopyClause(c Clause) Clause {
	out := c
	if c.Args != nil {
		out.Args = make(map[string]any, len(c.Args))
		for k, v := range c.Args {
			out.Args[k] = v
		}
	}
	return out
}

// Grid is the thread-safe in-memory arrow registry.
type Grid struct {
	mu                     sync.RWMutex
	arrows                 map[string]ArrowDefinition
	version                uint64
	onTheSpotInterruptions int
	observers              []GridObserver
}

// NewGrid returns an empty grid at version 0.
func NewGrid() *Grid {
	return &Grid{arrows: make(map[string]ArrowDefinition)}
}

// GridEventKind names a grid mutation.
type GridEventKind string

const (
	GridEventAppend          GridEventKind = "append"
	GridEventOnTheSpotAppend GridEventKind = "on-the-spot-append"
)

// GridEvent is the payload delivered to a GridObserver.
type GridEvent struct {
	Kind       GridEventKind
	ArrowID    string
	Definition ArrowDefinition
	Version    uint64
}

// GridObserver fires under the write lock on every mutation. Must be
// fast and non-blocking — a slow observer stalls all concurrent
// Lookups (mirrors FindingsObserver constraint).
type GridObserver func(event GridEvent)

// Observe registers an observer for grid mutations.
func (g *Grid) Observe(fn GridObserver) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.observers = append(g.observers, fn)
}

func (g *Grid) emit(e GridEvent) {
	for _, ob := range g.observers {
		ob(e)
	}
}

// Grid errors.
var ErrArrowAlreadyDeclared = errors.New("arrow-already-declared")

// Append registers a new arrow definition.
// Returns ErrArrowAlreadyDeclared if the ID is already present.
//
// On ErrArrowAlreadyDeclared, the caller SHOULD Lookup the
// now-declared arrow and proceed with traversal — the race winner's
// definition is canonical (validation-pass-6 F14).
func (g *Grid) Append(def ArrowDefinition) (uint64, error) {
	return g.appendInternal(def, GridEventAppend, false)
}

// AppendOnTheSpot registers a new arrow definition produced by the
// on-the-spot definition flow (gates.md §12). Same body as Append
// but bumps OnTheSpotInterruptions per §12.5.
func (g *Grid) AppendOnTheSpot(def ArrowDefinition) (uint64, error) {
	return g.appendInternal(def, GridEventOnTheSpotAppend, true)
}

// appendInternal is the shared append body (validation-pass-6 F3).
func (g *Grid) appendInternal(def ArrowDefinition, kind GridEventKind, bumpInterruption bool) (uint64, error) {
	if err := def.Validate(); err != nil {
		return 0, err
	}
	stored := deepCopyArrow(def) // F1: insulate from caller-side mutation
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, dup := g.arrows[stored.ID]; dup {
		return 0, fmt.Errorf("%w: %s", ErrArrowAlreadyDeclared, stored.ID)
	}
	g.arrows[stored.ID] = stored
	g.version++
	if bumpInterruption {
		g.onTheSpotInterruptions++
	}
	g.emit(GridEvent{
		Kind:       kind,
		ArrowID:    stored.ID,
		Definition: deepCopyArrow(stored), // observers can't poison via event payload
		Version:    g.version,
	})
	return g.version, nil
}

// Lookup returns a deep-copy of the arrow definition. Mutating the
// returned Clauses / Requirements / Args has no effect on stored
// state (validation-pass-6 F1).
func (g *Grid) Lookup(arrowID string) (ArrowDefinition, bool) {
	arrowID = strings.TrimSpace(arrowID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	def, ok := g.arrows[arrowID]
	if !ok {
		return ArrowDefinition{}, false
	}
	return deepCopyArrow(def), true
}

// Has reports whether an arrow is declared.
func (g *Grid) Has(arrowID string) bool {
	arrowID = strings.TrimSpace(arrowID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.arrows[arrowID]
	return ok
}

// Version returns the current grid version.
func (g *Grid) Version() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.version
}

// OnTheSpotInterruptions returns the count of on-the-spot arrow
// additions for this session. Per gates.md §12.5 a high count signals
// weak initialization.
func (g *Grid) OnTheSpotInterruptions() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.onTheSpotInterruptions
}

// Arrows returns a sorted snapshot of all declared arrow IDs.
func (g *Grid) Arrows() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.arrows))
	for id := range g.arrows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
