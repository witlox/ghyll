package runner

import (
	"errors"
	"sort"
	"strings"
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
// Tier 1 (ADR-015): the in-memory registry is the runtime
// surface; the engine `passes` table is the persistence shadow.
// Open passes from a crashed previous process are loaded back at
// session start via engine.Recovery + PassRegistry.Resume for
// the attestation-pending subset; everything else gets marked
// `aborted:crash` at the engine row.
type PassRegistry struct {
	mu     sync.RWMutex
	passes map[string]*Pass

	// observers is the one-shot subscriber list registered at
	// session start. Emit runs WITHOUT acquiring r.mu (F-4):
	// the slice is never mutated after Observe completes its
	// session-start phase, so a no-lock fanout is safe and
	// breaks the AB/BA cycle with All() → p.State().
	observers []PassObserver
}

// PassEventKind names the lifecycle moment producing the event.
// Per ADR-015 Part A: emit fires from Register / closeWith /
// Resume to the unlocked observer list.
type PassEventKind string

const (
	PassEventOpen    PassEventKind = "open"
	PassEventClose   PassEventKind = "close"
	PassEventAbort   PassEventKind = "abort"
	PassEventRecover PassEventKind = "recover"
)

// PassEvent is the observer payload. Mirrors PassRecord but
// stays in runner types (no engine import — preserves the
// runner-as-leaf invariant).
type PassEvent struct {
	Kind        PassEventKind
	PassID      string
	Role        string
	Context     string
	ArrowID     string
	GridVersion uint64
	State       PassState
	OpenedAt    time.Time
	ClosedAt    time.Time
	CloseReason string
	RecoveredAt time.Time
	At          time.Time
}

// PassObserver fires on every mutation. Per F-4: emit runs
// WITHOUT acquiring any registry lock. Observers MUST be fast
// (chan-send only); long work hands off to a goroutine.
type PassObserver func(event PassEvent)

// NewPassRegistry returns an empty registry.
func NewPassRegistry() *PassRegistry {
	return &PassRegistry{passes: make(map[string]*Pass)}
}

// Observe registers an observer. One-shot semantics: register
// all observers at session start before any emit happens.
// Concurrent Observe + emit is a caller bug.
func (r *PassRegistry) Observe(fn PassObserver) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.observers = append(r.observers, fn)
	r.mu.Unlock()
}

// emit fans out an event to every observer + bridges to the
// pass's OperatorBus (if any) for the legacy OpEventPass* kinds.
// No registry lock held during fanout (F-4): the slice is
// one-shot at registration and never mutates after that.
//
// The bus bridge is a single SOURCE → two SINKS pattern: emit is
// the canonical audit entry point, but the legacy bus subscribers
// (engine status CLI, future operator UI) keep working without
// duplicate publish calls scattered through OpenPass / closeWith.
func (r *PassRegistry) emit(e PassEvent) {
	r.mu.RLock()
	obs := r.observers
	pass := r.passes[e.PassID]
	r.mu.RUnlock()
	for _, fn := range obs {
		fn(e)
	}
	if pass == nil || pass.bus == nil {
		return
	}
	switch e.Kind {
	case PassEventOpen:
		pass.bus.Publish(OperatorEvent{
			Kind:    OpEventPassOpened,
			ArrowID: e.ArrowID,
			PassID:  e.PassID,
			Role:    e.Role,
			Detail:  e.Context,
		})
	case PassEventClose, PassEventAbort:
		pass.bus.Publish(OperatorEvent{
			Kind:    OpEventPassClosed,
			ArrowID: e.ArrowID,
			PassID:  e.PassID,
			Role:    e.Role,
			Detail:  string(e.State) + ":" + e.CloseReason,
		})
	}
}

// Register adds a pass + emits PassEventOpen. Pass-id is the key;
// double-registration overwrites silently (the dispatcher is the
// only caller; this is defensive).
//
// The emit is the single audit path for "pass opened" per N-1.
// OpenPass's bus.Publish(OpEventPassOpened, ...) is removed.
func (r *PassRegistry) Register(p *Pass) {
	if p == nil {
		return
	}
	r.mu.Lock()
	r.passes[p.ID()] = p
	p.registry = r
	r.mu.Unlock()
	r.emit(PassEvent{
		Kind:        PassEventOpen,
		PassID:      p.ID(),
		Role:        p.Role(),
		Context:     p.Context(),
		ArrowID:     p.ArrowID(),
		GridVersion: p.GridVersion(),
		State:       p.State(),
		OpenedAt:    p.OpenedAt(),
		At:          time.Now(),
	})
}

// ResumeOptions carries the persisted-pass fields PassRegistry.Resume
// needs to rebuild an in-memory *Pass. The runner package does NOT
// import engine, so the caller (engine.Recovery) translates from
// engine.PassRecord at the call site.
type ResumeOptions struct {
	PassID      string
	Role        string
	Context     string
	ArrowID     string
	GridVersion uint64
	OpenedAt    time.Time
	Now         func() time.Time // injected for testability; defaults to time.Now
}

// Resume reconstitutes a *Pass from a persisted record and
// re-acquires the per-(role, context) lock token via lockTable.
// Called by engine.Recovery for every attestation-pending pass
// preserved across a restart (F-3). On success the *Pass is
// registered and PassEventRecover fires; the pass's
// `recoveredAt` is stamped to deps.Now().
//
// Errors:
//   - *ErrRoleContextBusy if the (role, context) tuple is already
//     held in this process. Should not happen for orphan recovery
//     (the process is fresh), but the call is defensive.
//   - ErrPassResumeInvalidState if opts validation fails.
func (r *PassRegistry) Resume(
	opts ResumeOptions,
	lockTable *RoleContextLockTable,
) (*Pass, error) {
	if r == nil {
		return nil, errors.New("PassRegistry.Resume: nil receiver")
	}
	if lockTable == nil {
		return nil, ErrPassLockTableNil
	}
	if strings.TrimSpace(opts.PassID) == "" {
		return nil, ErrPassIDEmpty
	}
	if strings.TrimSpace(opts.Role) == "" {
		return nil, ErrPassRoleEmpty
	}
	if strings.TrimSpace(opts.Context) == "" {
		return nil, ErrPassContextEmpty
	}
	if strings.TrimSpace(opts.ArrowID) == "" {
		return nil, ErrPassArrowEmpty
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	tok, err := lockTable.TryAcquire(opts.Role, opts.Context, opts.PassID, 0)
	if err != nil {
		return nil, err
	}
	p := &Pass{
		id:          opts.PassID,
		role:        opts.Role,
		context:     opts.Context,
		arrowID:     opts.ArrowID,
		gridVersion: opts.GridVersion,
		openedAt:    opts.OpenedAt,
		state:       PassStateOpen,
		lockToken:   tok,
	}
	// Register before stamping recoveredAt so the registry's emit
	// for PassEventOpen sees a clean state, then markRecovered
	// emits PassEventRecover with the stamp.
	r.Register(p)
	p.markRecovered(now())
	return p, nil
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
