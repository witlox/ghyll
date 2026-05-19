package runner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

// ProducerFixHarness is a concrete implementation of the
// AdversarialOrchestrator's ProducerRemediate hook. It wraps a
// caller-supplied producer function with two safety mechanisms
// the spec requires:
//
//  1. Loop-bomb detection: tracks the digest of the producer's
//     response artifact across rounds and detects an unchanged
//     artifact between rounds. An unchanged artifact + still-
//     open findings means the producer is signaling "fixed" but
//     hasn't actually changed anything — a loop bomb. The
//     harness aborts with ErrProducerLoopBomb.
//
//  2. Convergence accounting: every round the producer takes
//     action on, the harness publishes a producer-fix-signal
//     OperatorEvent through the bus so observers can render
//     the cycle's progress.
//
// One harness instance per arrow per remediation cycle.
type ProducerFixHarness struct {
	// Producer is the caller-provided remediation function. It
	// receives the open findings raised in the previous round
	// and the round number. It should:
	//   - Transition findings the producer believes it has
	//     resolved (Findings.TransitionWithReason).
	//   - Return a digest of the response artifact (any
	//     stable byte sequence — typically the produced code
	//     diff or the response text) so the harness can detect
	//     loop bombs.
	//   - Return an error to abort the cycle.
	Producer ProducerFn

	// Bus optional — nil disables event publication.
	Bus *OperatorBus

	// ArrowID stamps the published events.
	ArrowID string

	mu              sync.Mutex
	round           int
	lastArtifactDgt [32]byte
	lastArtifactSet bool
}

// ProducerFn is the caller-supplied remediation function. Returns
// the artifact digest (any stable bytes) the harness can compare
// across rounds, plus an error if the producer aborted.
type ProducerFn func(ctx context.Context, openFindings []FindingRecord, round int) (artifactDigest []byte, err error)

// ProducerFix is a function value matching
// AdversarialOrchestrator's ProducerRemediateFn signature.
// Returning the harness via this method lets the orchestrator's
// ProducerRemediate hook invoke the harness cleanly.
//
// Calling the harness directly via h.ProducerRemediate(...) also
// works and is the form used in tests.
func (h *ProducerFixHarness) ProducerRemediate() ProducerRemediateFn {
	return func(ctx context.Context, openFindings []FindingRecord) error {
		return h.runOneRound(ctx, openFindings)
	}
}

// runOneRound drives one producer call, detects loop bombs, and
// publishes the producer-fix-signal event.
func (h *ProducerFixHarness) runOneRound(ctx context.Context, openFindings []FindingRecord) error {
	if h == nil {
		return errors.New("producer-fix: nil harness")
	}
	if h.Producer == nil {
		return errors.New("producer-fix: nil Producer")
	}

	h.mu.Lock()
	h.round++
	round := h.round
	prev := h.lastArtifactDgt
	prevSet := h.lastArtifactSet
	h.mu.Unlock()

	if h.Bus != nil {
		h.Bus.Publish(OperatorEvent{
			Kind:    OpEventProducerFixSignal,
			ArrowID: h.ArrowID,
			Detail:  fmt.Sprintf("round=%d open=%d", round, len(openFindings)),
		})
	}

	artifact, err := h.Producer(ctx, openFindings, round)
	if err != nil {
		return err
	}

	// Loop-bomb detection: hash the artifact and compare with the
	// previous round's hash. If unchanged AND findings remain
	// open, the producer is going through the motions without
	// actually remediating.
	dgt := sha256.Sum256(artifact)
	h.mu.Lock()
	h.lastArtifactDgt = dgt
	h.lastArtifactSet = true
	h.mu.Unlock()

	if prevSet && dgt == prev && len(openFindings) > 0 {
		return fmt.Errorf("%w: round %d artifact byte-identical to round %d",
			ErrProducerLoopBomb, round, round-1)
	}
	return nil
}

// Round returns the most recent round counter. Useful for tests
// and for telemetry callers.
func (h *ProducerFixHarness) Round() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.round
}

// ErrProducerLoopBomb is returned when the producer's response
// artifact is byte-identical to a prior round's. The cycle is
// aborted; the operator must intervene because the producer is
// no longer making progress.
var ErrProducerLoopBomb = errors.New("producer-fix: loop-bomb (unchanged artifact across rounds)")
