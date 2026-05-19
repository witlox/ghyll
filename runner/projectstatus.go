package runner

import (
	"sort"
	"sync"
	"time"
)

// ProjectStatus is the composite project-level view assembled from
// the four runner-layer stores (Findings, Classifications, Grid,
// AmendmentQueue) plus pass-table state.
//
// One snapshot is computed on demand by Capture; the result is
// immutable. Designed for the engine status CLI, the operator
// HTTP endpoint, and the future ghyll arrow show <id> surface.
//
// The aggregator is a pure read function: it does NOT cache
// across calls. Each Capture walks the stores anew. This trades
// CPU for staleness: callers always see fresh state.
type ProjectStatus struct {
	CapturedAt         time.Time
	GridVersion        uint64
	ArrowCount         int
	OpenPasses         []PassSnapshot
	FindingCounts      FindingStatusCounts
	BlockingArrowIDs   []string // arrows whose derived status is "blocked"
	AmendmentBacklog   int
	AmendmentDrained   int
	AttestationCount   int
	AttestationsByKind map[AttestationKind]int
}

// PassSnapshot is one row in ProjectStatus.OpenPasses. Reflects
// the state of a Pass at capture time.
type PassSnapshot struct {
	PassID   string
	Role     string
	Context  string
	ArrowID  string
	State    PassState
	OpenedAt time.Time
	ClosedAt time.Time
	Reason   string
}

// FindingStatusCounts groups findings by status for the summary.
// Mirrors the runner.FindingStatus enum: Open, Running, Resolved,
// AcceptedRisk, Unevaluated.
type FindingStatusCounts struct {
	Open         int
	Running      int
	Resolved     int
	AcceptedRisk int
	Unevaluated  int
}

// PassRegistry tracks live passes for ProjectStatus. The
// dispatcher registers each Pass at Open and unregisters at
// Close/Abort. Crash-recovery does NOT persist passes — open
// passes from a crashed previous process are gone on restart
// (the in-memory lock table is empty; same for this registry).
type PassRegistry struct {
	mu     sync.RWMutex
	passes map[string]*Pass
}

// NewPassRegistry returns an empty registry.
func NewPassRegistry() *PassRegistry {
	return &PassRegistry{passes: make(map[string]*Pass)}
}

// Register adds a pass. Pass-id is the key; double-registration
// overwrites silently (the dispatcher is the only caller; this is
// defensive).
func (r *PassRegistry) Register(p *Pass) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passes[p.ID()] = p
}

// Unregister removes a pass by ID. Idempotent (missing IDs are
// silent).
func (r *PassRegistry) Unregister(passID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.passes, passID)
}

// All returns a snapshot of every live pass.
func (r *PassRegistry) All() []*Pass {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Pass, 0, len(r.passes))
	for _, p := range r.passes {
		out = append(out, p)
	}
	return out
}

// Len returns the registered pass count.
func (r *PassRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.passes)
}

// StatusSources bundles the runner-layer sources Capture needs.
// All fields except AttestationStore are required; AttestationStore
// is optional (a project that hasn't yet recorded any attestations
// still has a meaningful status).
type StatusSources struct {
	Findings        *FindingsStore
	Classifications *ClassificationsStore
	Grid            *Grid
	Amendments      *AmendmentQueue
	Attestations    *AttestationStore // optional
	Passes          *PassRegistry
	Now             func() time.Time // optional
}

// CaptureProjectStatus walks the sources and assembles a snapshot.
// The result is a value type — safe to hold past the call.
func CaptureProjectStatus(src StatusSources) ProjectStatus {
	now := src.Now
	if now == nil {
		now = time.Now
	}
	status := ProjectStatus{
		CapturedAt:         now(),
		AttestationsByKind: make(map[AttestationKind]int),
	}

	if src.Grid != nil {
		status.GridVersion = src.Grid.Version()
		status.ArrowCount = len(src.Grid.Arrows())
	}

	if src.Passes != nil {
		for _, p := range src.Passes.All() {
			status.OpenPasses = append(status.OpenPasses, PassSnapshot{
				PassID:   p.ID(),
				Role:     p.Role(),
				Context:  p.Context(),
				ArrowID:  p.ArrowID(),
				State:    p.State(),
				OpenedAt: p.OpenedAt(),
				ClosedAt: p.ClosedAt(),
				Reason:   p.CloseReason(),
			})
		}
		sort.Slice(status.OpenPasses, func(i, j int) bool {
			return status.OpenPasses[i].PassID < status.OpenPasses[j].PassID
		})
	}

	if src.Findings != nil {
		status.FindingCounts = countFindings(src.Findings)
		status.BlockingArrowIDs = blockingArrows(src.Findings)
	}

	if src.Amendments != nil {
		status.AmendmentBacklog = src.Amendments.Len()
		status.AmendmentDrained = src.Amendments.DrainedCount()
	}

	if src.Attestations != nil {
		all := src.Attestations.All()
		status.AttestationCount = len(all)
		for _, a := range all {
			status.AttestationsByKind[a.Kind]++
		}
	}

	return status
}

func countFindings(fs *FindingsStore) FindingStatusCounts {
	var c FindingStatusCounts
	for _, arrowID := range fs.ArrowIDs() {
		for _, f := range fs.ForArrow(arrowID) {
			switch f.Status {
			case FindingStatusOpen:
				c.Open++
			case FindingStatusRunning:
				c.Running++
			case FindingStatusResolved:
				c.Resolved++
			case FindingStatusAcceptedRisk:
				c.AcceptedRisk++
			case FindingStatusUnevaluated:
				c.Unevaluated++
			}
		}
	}
	return c
}

func blockingArrows(fs *FindingsStore) []string {
	// An arrow is "blocking" if it has at least one finding with
	// status Open or Running (in-flight remediation). Sorted for
	// deterministic output.
	blocking := make(map[string]struct{})
	for _, arrowID := range fs.ArrowIDs() {
		for _, f := range fs.ForArrow(arrowID) {
			if f.Status == FindingStatusOpen || f.Status == FindingStatusRunning {
				blocking[arrowID] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(blocking))
	for id := range blocking {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
