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
	subscribers []OperatorEventSubscriber
	now         func() time.Time
}

// NewOperatorBus returns an empty bus with time.Now as the
// timestamp source.
func NewOperatorBus() *OperatorBus {
	return &OperatorBus{now: time.Now}
}

// WithClock overrides the timestamp source for tests.
func (b *OperatorBus) WithClock(clock func() time.Time) *OperatorBus {
	b.now = clock
	return b
}

// Subscribe registers a subscriber. The bus retains the function;
// there is no Unsubscribe. Typical use: the session attaches one
// subscriber per consumer (JSONL writer, status surface, future
// HTTP forwarder) at startup.
func (b *OperatorBus) Subscribe(fn OperatorEventSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers = append(b.subscribers, fn)
}

// Publish fans out the event to every subscriber under the bus
// mutex. If Timestamp is zero, the bus stamps it with now(). The
// publisher does not see subscriber errors — subscribers that
// fail silently swallow.
func (b *OperatorBus) Publish(e OperatorEvent) {
	if e.Timestamp.IsZero() {
		e.Timestamp = b.now()
	}
	b.mu.RLock()
	subs := make([]OperatorEventSubscriber, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.RUnlock()
	// Fan out OUTSIDE the lock so a slow subscriber doesn't block
	// Subscribe / other Publishes. The slice copy above gives a
	// stable snapshot.
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
