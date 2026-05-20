package runner

import (
	"sync"
	"time"
)

// OperatorEvent is a typed event published on the operator event
// bus. The bus is the load-bearing channel for attestation flow,
// amendment R/C reporting, multi-round adversarial orchestration,
// and the @deferred surface that previously had no observable hook
// (gates.md attestation event bus).
//
// Subscribers see events in publish order. The bus retains no
// history; subscribers that join late see only events published
// after they subscribed.
//
// Pattern matches FindingsStore/Grid/AmendmentQueue Observer
// fanout — publishers hold the bus mutex during fanout, so
// subscribers MUST be fast (the typical subscriber writes to its
// own channel and returns immediately).
type OperatorEvent struct {
	Kind      OperatorEventKind
	Timestamp time.Time
	ArrowID   string
	ClauseID  string // optional
	FindingID string // optional
	PassID    string // optional
	Role      string // who emitted (or who the event concerns)
	OpID      string // operator identity, if relevant
	Detail    string // free-text payload; sanitized at emit time
	Payload   map[string]string
}

// OperatorEventKind is the wire-stable event-type discriminator.
// Add cases for new event types; do NOT renumber.
type OperatorEventKind string

const (
	// Attestation events.
	OpEventAttestationRecorded  OperatorEventKind = "attestation-recorded"
	OpEventAttestationRequested OperatorEventKind = "attestation-requested"
	OpEventSelfCertRefused      OperatorEventKind = "self-cert-refused"

	// Amendment events.
	OpEventAmendmentEnqueued     OperatorEventKind = "amendment-enqueued"
	OpEventAmendmentDrained      OperatorEventKind = "amendment-drained"
	OpEventAmendmentQueueGrowing OperatorEventKind = "amendment-queue-growing"

	// Adversarial / multi-round events.
	OpEventAdversarialRoundStart OperatorEventKind = "adversarial-round-start"
	OpEventProducerFixSignal     OperatorEventKind = "producer-fix-signal"
	OpEventRemediationConverged  OperatorEventKind = "remediation-converged"
	OpEventRemediationEscalated  OperatorEventKind = "remediation-escalated"

	// Pass lifecycle events.
	OpEventPassOpened OperatorEventKind = "pass-opened"
	OpEventPassClosed OperatorEventKind = "pass-closed"

	// State-machine events.
	OpEventInsufficientBasisRoundsExceeded OperatorEventKind = "insufficient-basis-rounds-exceeded"

	// Audit-trail events.
	OpEventAttestationAuditDurabilityFailed OperatorEventKind = "attestation-audit-durability-failed"

	// Recovery events (Tier 1, ADR-015 Part D). Emitted by
	// engine.Recovery into RecoveryReport.Events, NOT to the bus
	// (per F-18: the bus has zero subscribers at recovery time).
	// session.Open is responsible for surfacing these on the
	// chat-loop's first iteration.
	OpEventRecoveryPassAbortedCrash       OperatorEventKind = "recovery-pass-aborted-crash"
	OpEventRecoveryAttestationRepublished OperatorEventKind = "recovery-attestation-republished"
	OpEventRecoveryAttestationReplay      OperatorEventKind = "recovery-attestation-replay"
	OpEventRecoveryJSONLTruncated         OperatorEventKind = "recovery-jsonl-truncated"

	// Tier 2 events (ADR-016 + gate-1 remediation).

	// OpEventClauseFailVerdict — operator submitted verdict=fail
	// on an attested clause. Subscribed by the producer-fix
	// signal path so the upstream role gets a remediation
	// trigger.
	OpEventClauseFailVerdict OperatorEventKind = "clause-fail-verdict"

	// OpEventEscalationPresented — the modal showed an
	// escalation prompt to the operator. Audit-trail only.
	OpEventEscalationPresented OperatorEventKind = "escalation-presented"

	// OpEventEscalationResolved — operator chose option 1
	// (accepted-risk) or option 2 (route-upstream) on the
	// escalation prompt. Detail carries the choice.
	OpEventEscalationResolved OperatorEventKind = "escalation-resolved"

	// OpEventModalSkipped — operator typed `skip` on the
	// verdict modal; the clause stays pending. Lock token
	// released so the dispatcher can move on; the next REPL
	// turn re-presents on OpEventAttestationRequested
	// republish.
	OpEventModalSkipped OperatorEventKind = "modal-skipped"

	// OpEventModalBackpressure — modal driver dropped an
	// OnEvent because its pending queue is at
	// ModalPendingMaxLen (gate-1 F-8).
	OpEventModalBackpressure OperatorEventKind = "modal-backpressure"

	// OpEventPathTruncated — EncodeAttestationPath produced
	// a path with one or more hash-substituted segments
	// (gate-1 F-17). Audit-trail; the write still succeeded.
	OpEventPathTruncated OperatorEventKind = "attestation-path-truncated"
)

// OperatorEventSubscriber receives events in publish order. Slow
// subscribers block the publisher; if your subscriber does I/O or
// network calls, hand off to a goroutine and return immediately.
type OperatorEventSubscriber func(event OperatorEvent)

// OperatorBus is the shared in-process event bus. One bus per
// session. Subscribers register at session start; publishers are
// the runner / engine / dispatch layers.
//
// The bus is in-memory and unsynced across sessions. A future
// "operator HTTP endpoint" can attach its own subscriber and
// forward events; until then, the bus is internal observability +
// the substrate for the JSONL audit writer.
type OperatorBus struct {
	mu          sync.RWMutex
	subscribers []subscriberEntry
	nextID      uint64
	now         func() time.Time
}

// subscriberEntry pairs an id with its callback so Unsubscribe
// can remove by handle. Gate-2 CONC-H-4.
type subscriberEntry struct {
	id uint64
	fn OperatorEventSubscriber
}

// NewOperatorBus returns an empty bus with time.Now as the
// timestamp source.
func NewOperatorBus() *OperatorBus {
	return &OperatorBus{now: time.Now}
}

// WithClock overrides the timestamp source for tests.
// Gate-2 CONC-L-1: holds the bus mutex during the swap so a
// concurrent Publish sees a consistent now-function.
func (b *OperatorBus) WithClock(clock func() time.Time) *OperatorBus {
	b.mu.Lock()
	b.now = clock
	b.mu.Unlock()
	return b
}

// Subscribe registers a subscriber. Returns a closer that removes
// the subscriber when called (idempotent). Gate-2 CONC-H-4: the
// session's closeEngine MUST call the returned closer for every
// subscriber it registers so the bus doesn't outlive the engine
// runtime through dangling callbacks.
func (b *OperatorBus) Subscribe(fn OperatorEventSubscriber) func() {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	b.subscribers = append(b.subscribers, subscriberEntry{id: id, fn: fn})
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		for i, e := range b.subscribers {
			if e.id == id {
				b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

// Publish fans out the event to every subscriber. If Timestamp is
// zero, the bus stamps it with now(). The publisher does not see
// subscriber errors — subscribers that fail silently swallow.
//
// Fan-out runs OUTSIDE the bus mutex so a slow subscriber doesn't
// block Subscribe / other Publishes. The slice copy below gives a
// stable snapshot.
func (b *OperatorBus) Publish(e OperatorEvent) {
	b.mu.RLock()
	if e.Timestamp.IsZero() {
		e.Timestamp = b.now()
	}
	subs := make([]OperatorEventSubscriber, len(b.subscribers))
	for i, entry := range b.subscribers {
		subs[i] = entry.fn
	}
	b.mu.RUnlock()
	for _, sub := range subs {
		sub(e)
	}
}

// SubscriberCount returns the registered subscriber count. Useful
// for tests asserting wire-up correctness.
func (b *OperatorBus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Gate-2 CONC-H-4: docstring drift correction. The original
// comment claimed "publishers hold the bus mutex during fanout"
// which is and was false (Publish snapshots then releases). The
// implementation is intentional — recursive Publish is safe.
