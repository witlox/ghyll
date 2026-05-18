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
// counter so persistent engines (phase-N work) can append-only journal
// changes.
//
// The runner's Grid is the runtime cache; the persistent disk grid
// (bootstrap/grid.go) is its source of truth at session boundaries.
// Phase-6 keeps the two decoupled — the engine layer (later phase)
// will bridge them.
//
// Validation pattern: structurally invalid arrow definitions are
// rejected at Append; downstream consumers can trust everything in
// the grid validates.

// ArrowDefinition is one arrow in the grid. Captures the structural
// shape the runner needs at evaluation time: the arrow's identity,
// the clauses it carries, and the requirements depth-classification
// will compare against.
type ArrowDefinition struct {
	// ID is the operator-stable arrow identifier per gates.md §7.1a
	// (typically "role→role/stratum/context").
	ID string

	// SourceRole is the role producing the upstream artifact.
	SourceRole string

	// TargetRole is the role consuming the artifact.
	TargetRole string

	// Stratum is the depth stratum per gates.md §3.
	Stratum string

	// Context is the bounded context per gates.md §3.
	Context string

	// Clauses are the gate clauses the arrow's verification step
	// must evaluate.
	Clauses []Clause

	// Requirements are the per-requirement minimum-depth declarations
	// the depth-classification sub-activity compares against.
	Requirements []Requirement
}

// Validate checks the definition is structurally well-formed.
// Per validation-pass-5 patterns: trim+nonempty for IDs, validate
// per-clause concept presence, require each Requirement validates
// via Requirement.Validate.
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
	if len(a.Clauses) == 0 {
		// An arrow with no clauses is degenerate; DeriveArrowStatus
		// would treat it as in-progress forever. Reject at the
		// boundary.
		return fmt.Errorf("arrow %q: no clauses declared", a.ID)
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

// Grid is the thread-safe in-memory arrow registry.
type Grid struct {
	mu                     sync.RWMutex
	arrows                 map[string]ArrowDefinition // arrowID → def
	version                uint64
	onTheSpotInterruptions int
	observers              []GridObserver
}

// NewGrid returns an empty grid at version 0.
func NewGrid() *Grid {
	return &Grid{arrows: make(map[string]ArrowDefinition)}
}

// Grid event types.
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

// GridObserver fires under the write lock on every mutation. Same
// constraints as FindingsObserver: must be fast and non-blocking.
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
var (
	ErrArrowAlreadyDeclared = errors.New("arrow-already-declared")
	ErrArrowUndeclared      = errors.New("arrow-undeclared")
)

// Append registers a new arrow definition. Returns
// ErrArrowAlreadyDeclared if the ID is already present (the engine
// must explicitly Replace to amend; phase-6 doesn't expose Replace).
// On success the grid version bumps and a GridEventAppend fires.
func (g *Grid) Append(def ArrowDefinition) (uint64, error) {
	if err := def.Validate(); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, dup := g.arrows[def.ID]; dup {
		return 0, fmt.Errorf("%w: %s", ErrArrowAlreadyDeclared, def.ID)
	}
	g.arrows[def.ID] = def
	g.version++
	g.emit(GridEvent{
		Kind:       GridEventAppend,
		ArrowID:    def.ID,
		Definition: def,
		Version:    g.version,
	})
	return g.version, nil
}

// AppendOnTheSpot registers a new arrow definition produced by the
// on-the-spot definition flow (gates.md §12). Identical to Append
// except for the event kind (so the engine can distinguish
// init-time arrows from runtime additions) and the interruption
// counter that bumps for the operator-visible quality signal per
// §12.5.
func (g *Grid) AppendOnTheSpot(def ArrowDefinition) (uint64, error) {
	if err := def.Validate(); err != nil {
		return 0, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, dup := g.arrows[def.ID]; dup {
		return 0, fmt.Errorf("%w: %s", ErrArrowAlreadyDeclared, def.ID)
	}
	g.arrows[def.ID] = def
	g.version++
	g.onTheSpotInterruptions++
	g.emit(GridEvent{
		Kind:       GridEventOnTheSpotAppend,
		ArrowID:    def.ID,
		Definition: def,
		Version:    g.version,
	})
	return g.version, nil
}

// Lookup returns the arrow definition for the given ID. ok=false if
// the arrow is undeclared (the on-the-spot creation trigger).
func (g *Grid) Lookup(arrowID string) (ArrowDefinition, bool) {
	arrowID = strings.TrimSpace(arrowID)
	g.mu.RLock()
	defer g.mu.RUnlock()
	def, ok := g.arrows[arrowID]
	return def, ok
}

// Has reports whether an arrow is declared.
func (g *Grid) Has(arrowID string) bool {
	_, ok := g.Lookup(arrowID)
	return ok
}

// Version returns the current grid version (bump-counter).
func (g *Grid) Version() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.version
}

// OnTheSpotInterruptions returns the count of on-the-spot arrow
// additions for this session. Per gates.md §12.5 this is a quality
// signal — a high count signals weak initialization.
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
