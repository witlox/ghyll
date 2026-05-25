// Contract tests for the typed OperatorEvent.Payload contract
// (ADR-v4-005). The ADR enumerates required Payload keys per
// event kind; each producer must populate those keys. The
// `TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency`
// test below is the across-event-kind contract enforcer the ADR
// claims exists (line 45).
//
// F-C-1 / F-M-1 / F-M-2 closure (2026-05-25 diamond final
// adversarial): the producers were shipped against per-consumer
// schemas rather than ADR-v4-005's central typed-Payload contract.
// This test pins the contract so the next event kind's producer
// has to satisfy it BEFORE its consumer test passes.

package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// canonicalPayloadKeys enumerates the required Payload keys per
// event kind, per ADR-v4-005 lines 31-41.
//
// Adding a new event kind: add an entry here. Any producer in the
// codebase that publishes without all of its required keys will
// trip the consistency assertion in
// TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency.
var canonicalPayloadKeys = map[runner.OperatorEventKind][]string{
	runner.OpEventAdversarialRoundStart: {
		"arrow_id", "pass_id", "round", "rounds_max",
		"open_findings", "tier_label",
	},
	runner.OpEventRemediationConverged: {
		"arrow_id", "pass_id", "outcome", "rounds_used",
	},
	runner.OpEventRemediationEscalated: {
		"arrow_id", "pass_id", "outcome", "rounds_used", "reason",
	},
	runner.OpEventAmendmentDrained: {
		"amendment_id", "source_arrow", "grid_version_before",
		"grid_version_after", "arrows_added", "passes_aborted",
		"outcome",
	},
	runner.OpEventArrowInvalidated: {
		"arrow_id", "op_id", "reason", "timestamp",
	},
}

// TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency
// drives each in-production producer and asserts the resulting
// OperatorEvent.Payload contains all keys required by ADR-v4-005.
//
// This test is the one ADR-v4-005 line 45 claims exists; F-C-1
// flagged it as missing.
func TestScenario_OperatorBus_PayloadContract_OutcomeKeyConsistency(t *testing.T) {
	t.Parallel()

	t.Run("ArrowInvalidated", func(t *testing.T) {
		t.Parallel()
		const arrowID = "A1"
		rt := makeInvalidateArrowRuntime(t, arrowID)
		captured := captureEvent(t, rt.Bus(), runner.OpEventArrowInvalidated)
		s := &Session{engine: rt, opID: "op-test"}
		res := s.handleInvalidateArrowCommand(arrowID + " --reason contract-test")
		if !res.Handled {
			t.Fatal("expected Handled=true")
		}
		evs := captured()
		if len(evs) != 1 {
			t.Fatalf("expected 1 event, got %d", len(evs))
		}
		assertCanonicalPayload(t, runner.OpEventArrowInvalidated, evs[0])
	})

	t.Run("AdversarialRoundStart_Converged", func(t *testing.T) {
		t.Parallel()
		// Drive runDispatcherAdversarialPhase with a stub bundle whose
		// OpenSweep returns no findings → converges in round 0
		// (openCount==0 path). Exercises both the round-start publish
		// and the converged publish from one dispatch.
		rt, bundle, req := makePayloadContractAdversarialFixtures(t, false)
		capturedStart := captureEvent(t, rt.Bus(), runner.OpEventAdversarialRoundStart)
		capturedTerm := captureEvent(t, rt.Bus(), runner.OpEventRemediationConverged)
		_, _, err := rt.runDispatcherAdversarialPhase(context.Background(),
			req, "P1", req.Arrow.Clauses, bundle)
		if err != nil {
			t.Fatalf("runDispatcherAdversarialPhase: %v", err)
		}
		startEvs := capturedStart()
		if len(startEvs) != 1 {
			t.Fatalf("expected 1 round-start, got %d", len(startEvs))
		}
		assertCanonicalPayload(t, runner.OpEventAdversarialRoundStart, startEvs[0])
		termEvs := capturedTerm()
		if len(termEvs) != 1 {
			t.Fatalf("expected 1 converged, got %d", len(termEvs))
		}
		assertCanonicalPayload(t, runner.OpEventRemediationConverged, termEvs[0])
	})

	t.Run("RemediationEscalated", func(t *testing.T) {
		t.Parallel()
		// Drive an escalated outcome: OpenSweep raises one
		// high-severity finding, FixAttempt returns madeProgress=false
		// → RemediationEscalatedNoProgress in round 0.
		rt, bundle, req := makePayloadContractAdversarialFixtures(t, true)
		capturedEsc := captureEvent(t, rt.Bus(), runner.OpEventRemediationEscalated)
		_, _, err := rt.runDispatcherAdversarialPhase(context.Background(),
			req, "P2", req.Arrow.Clauses, bundle)
		if err != nil {
			t.Fatalf("runDispatcherAdversarialPhase: %v", err)
		}
		escEvs := capturedEsc()
		if len(escEvs) != 1 {
			t.Fatalf("expected 1 escalated, got %d", len(escEvs))
		}
		assertCanonicalPayload(t, runner.OpEventRemediationEscalated, escEvs[0])
	})
}

// captureEvent registers a transient bus subscriber and returns a
// snapshotter the caller invokes to get the captured events.
func captureEvent(t *testing.T, bus *runner.OperatorBus, kind runner.OperatorEventKind) func() []runner.OperatorEvent {
	t.Helper()
	var (
		mu  sync.Mutex
		evs []runner.OperatorEvent
	)
	uns := bus.Subscribe(func(e runner.OperatorEvent) {
		if e.Kind != kind {
			return
		}
		mu.Lock()
		evs = append(evs, e)
		mu.Unlock()
	})
	t.Cleanup(uns)
	return func() []runner.OperatorEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]runner.OperatorEvent, len(evs))
		copy(out, evs)
		return out
	}
}

// assertCanonicalPayload fails the test if any required key per
// ADR-v4-005 is missing or empty.
func assertCanonicalPayload(t *testing.T, kind runner.OperatorEventKind, ev runner.OperatorEvent) {
	t.Helper()
	required, ok := canonicalPayloadKeys[kind]
	if !ok {
		t.Fatalf("canonicalPayloadKeys missing entry for %s", kind)
	}
	if ev.Payload == nil {
		t.Fatalf("%s: Payload is nil (ADR-v4-005 requires %v)", kind, required)
	}
	var missing []string
	for _, k := range required {
		if v, ok := ev.Payload[k]; !ok || v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s: Payload missing canonical keys per ADR-v4-005: %s\n  got Payload=%v",
			kind, strings.Join(missing, ", "), ev.Payload)
	}
}

// makePayloadContractAdversarialFixtures builds the smallest
// possible bundle + DispatchRequest such that the adversarial
// cycle reaches its terminal publish deterministically.
//
//	escalate=false: OpenSweep returns no findings; the loop
//	  converges in round 0.
//	escalate=true:  OpenSweep raises one high-severity finding;
//	  ProducerFix returns nil bytes ("no progress") → harness
//	  reports madeProgress=false → RemediationEscalatedNoProgress.
func makePayloadContractAdversarialFixtures(t *testing.T, escalate bool) (*engineRuntime, *runner.AdversarialHooks, *runner.DispatchRequest) {
	t.Helper()
	workdir := t.TempDir()
	g := makeMinimalGrid(t, workdir, nil)
	rt, err := openEngineWithOptions(workdir, nil, 3, g)
	if err != nil {
		t.Fatalf("openEngineWithOptions: %v", err)
	}
	t.Cleanup(rt.closeEngine)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatalf("replayEngine: %v", err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatalf("attachJournal: %v", err)
	}
	bundle := &runner.AdversarialHooks{
		Factory: func(int) *runner.Adversary {
			return &runner.Adversary{
				AdversaryRole: "adversary",
			}
		},
		OpenSweep: func(context.Context, runner.AdversaryAttack) ([]runner.FindingRecord, error) {
			return nil, nil
		},
		Classify: func(context.Context, runner.AdversaryAttack) ([]runner.Classification, error) {
			return nil, nil
		},
		ProducerFix: func(context.Context, []runner.FindingRecord, int) ([]byte, error) {
			return []byte("ok"), nil
		},
		RemediationConfigDefaults: runner.RemediationConfig{RoundsMax: 1},
	}
	if escalate {
		// Drive an escalated-rounds-max outcome. Seed an open
		// high-severity finding so openFindingsByThreshold reports
		// >0 across rounds; ProducerFix returns a (varying-byte)
		// artifact each round so the loop-bomb detector doesn't
		// trip; FixAttempt reports madeProgress=true → loop runs
		// round 0 then exits via the for-bound (RoundsMax=1) →
		// outcome=escalated-rounds-max.
		if err := rt.findings.Raise(runner.FindingRecord{
			ID: "F-pre", ArrowID: "A-contract", Type: "contract-test",
			Severity: runner.SeverityHigh, Status: runner.FindingStatusOpen,
		}); err != nil {
			t.Fatalf("Raise: %v", err)
		}
		var counter int
		bundle.ProducerFix = func(context.Context, []runner.FindingRecord, int) ([]byte, error) {
			counter++
			// Distinct artifact bytes per round avoids the
			// loop-bomb trip from byte-identical artifacts.
			return []byte{byte(counter), 0xff}, nil
		}
	}
	rt.AdversarialHooks().Store(bundle)
	req := &runner.DispatchRequest{
		Arrow: runner.ArrowDefinition{
			ID: "A-contract",
			Clauses: []runner.Clause{
				{Concept: "no-todo-marker", DepthType: runner.DepthTypeSensitive},
			},
		},
		ActualTier: runner.DepthRankShallow,
	}
	return rt, bundle, req
}
