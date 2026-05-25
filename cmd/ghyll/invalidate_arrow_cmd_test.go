// End-to-end test for the /invalidate-arrow slash command
// (diamond v4 / integrator-pass I-C-1 closure).

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// makeInvalidateArrowRuntime opens an engine + attaches the journal
// and registers a single arrow in the grid so /invalidate-arrow has
// a target. Returns the runtime + a cleanup hook.
func makeInvalidateArrowRuntime(t *testing.T, arrowID string) *engineRuntime {
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
	def := runner.ArrowDefinition{
		ID:         arrowID,
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{{
			Concept:  "no-todo-marker",
			ClauseID: arrowID + "-c0",
		}},
	}
	if _, err := rt.Grid().Append(def); err != nil {
		t.Fatalf("grid append: %v", err)
	}
	return rt
}

// TestScenario_InvalidateArrow_SlashCommand_NoOpID asserts the
// command refuses without an op-id (I-C-1 closure: audit row needs
// operator identity).
func TestScenario_InvalidateArrow_SlashCommand_NoOpID(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	s := &Session{engine: rt}
	res := s.handleInvalidateArrowCommand("A1")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "no op-id set") {
		t.Fatalf("expected refusal mentioning op-id, got: %s", res.Output)
	}
}

// TestScenario_InvalidateArrow_SlashCommand_ArrowNotInGrid asserts the
// command refuses on unknown arrow-id with a clear message.
func TestScenario_InvalidateArrow_SlashCommand_ArrowNotInGrid(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleInvalidateArrowCommand("does-not-exist")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "not in grid") {
		t.Fatalf("expected `not in grid` refusal, got: %s", res.Output)
	}
}

// TestScenario_InvalidateArrow_SlashCommand_UsageOnEmpty asserts the
// command surfaces usage when no arrow-id is given (parser refusal).
func TestScenario_InvalidateArrow_SlashCommand_UsageOnEmpty(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleInvalidateArrowCommand("")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "usage:") {
		t.Fatalf("expected usage hint, got: %s", res.Output)
	}
}

// TestScenario_InvalidateArrow_SlashCommand_PublishesAndPersists is
// the END-TO-END test. With op-id + a real arrow, the command
// publishes OpEventArrowInvalidated; the observer registered in
// attachJournal writes a row to arrow_invalidations. The test reads
// the row back via Store().CountArrowInvalidations to prove the
// producer→bus→observer→sqlite chain is complete.
func TestScenario_InvalidateArrow_SlashCommand_PublishesAndPersists(t *testing.T) {
	t.Parallel()
	const arrowID = "A1"
	rt := makeInvalidateArrowRuntime(t, arrowID)

	// Capture bus events to prove the producer fires.
	var captured []runner.OperatorEvent
	unsubscribe := rt.Bus().Subscribe(func(e runner.OperatorEvent) {
		if e.Kind == runner.OpEventArrowInvalidated {
			captured = append(captured, e)
		}
	})
	defer unsubscribe()

	s := &Session{engine: rt, opID: "op-alice@example.com"}
	res := s.handleInvalidateArrowCommand(arrowID + " --reason stale-spec")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "invalidated") {
		t.Fatalf("expected `invalidated` in confirmation, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "arrow_invalidations") {
		t.Fatalf("expected confirmation to mention persistence target, got: %s", res.Output)
	}

	// Producer side: exactly one event on the bus, carrying the
	// typed payload per ADR-v4-005.
	if len(captured) != 1 {
		t.Fatalf("expected 1 OpEventArrowInvalidated, got %d", len(captured))
	}
	ev := captured[0]
	if ev.ArrowID != arrowID {
		t.Fatalf("ev.ArrowID = %q, want %q", ev.ArrowID, arrowID)
	}
	if ev.OpID != "op-alice@example.com" {
		t.Fatalf("ev.OpID = %q, want op-alice@example.com", ev.OpID)
	}
	if ev.Payload["reason"] != "stale-spec" {
		t.Fatalf("ev.Payload[reason] = %q, want stale-spec", ev.Payload["reason"])
	}
	if ev.Payload["source"] != "operator" {
		t.Fatalf("ev.Payload[source] = %q, want operator", ev.Payload["source"])
	}

	// Consumer side: arrow_invalidations row landed on disk.
	n, err := rt.Store().CountArrowInvalidations(context.Background(), arrowID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row in arrow_invalidations, got %d", n)
	}
}

// TestScenario_InvalidateArrow_DefaultReason asserts that omitting
// --reason populates a non-empty default ("operator-requested") so
// the audit row never lands with reason="".
func TestScenario_InvalidateArrow_DefaultReason(t *testing.T) {
	t.Parallel()
	const arrowID = "A1"
	rt := makeInvalidateArrowRuntime(t, arrowID)
	var captured []runner.OperatorEvent
	unsubscribe := rt.Bus().Subscribe(func(e runner.OperatorEvent) {
		if e.Kind == runner.OpEventArrowInvalidated {
			captured = append(captured, e)
		}
	})
	defer unsubscribe()
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleInvalidateArrowCommand(arrowID)
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if len(captured) != 1 {
		t.Fatalf("expected 1 event, got %d", len(captured))
	}
	if captured[0].Payload["reason"] != "operator-requested" {
		t.Fatalf("default reason = %q, want operator-requested",
			captured[0].Payload["reason"])
	}
}

// TestScenario_InvalidateArrow_DispatchWiredThroughSession asserts
// that DispatchSlashCommand routes `/invalidate-arrow` to the
// handler (the seam the REPL exercises).
func TestScenario_InvalidateArrow_DispatchWiredThroughSession(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	s := &Session{engine: rt, opID: "op-test"}
	res := s.DispatchSlashCommand("/invalidate-arrow A1 --reason wired")
	if !res.Handled {
		t.Fatal("expected DispatchSlashCommand to handle /invalidate-arrow")
	}
	if !strings.Contains(res.Output, "invalidated") {
		t.Fatalf("expected invalidation confirmation, got: %s", res.Output)
	}
}

// TestScenario_InvalidateArrow_ParseArgs_UnknownFlag asserts the
// parser rejects unknown flags rather than silently dropping them.
func TestScenario_InvalidateArrow_ParseArgs_UnknownFlag(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	s := &Session{engine: rt, opID: "op-test"}
	res := s.handleInvalidateArrowCommand("A1 --bogus value")
	if !res.Handled {
		t.Fatal("expected Handled=true")
	}
	if !strings.Contains(res.Output, "usage:") {
		t.Fatalf("expected usage refusal, got: %s", res.Output)
	}
}

// TestScenario_ArrowInvalidations_SubscribeUnsubscribeCaptured
// asserts I-H-1 closure: the arrow_invalidations bus subscriber's
// closer is captured on engineRuntime so closeEngine drops the
// callback before r.store closes. Without the handle, a late
// publish after the store closes would call InsertArrowInvalidation
// on a dead store handle.
func TestScenario_ArrowInvalidations_SubscribeUnsubscribeCaptured(t *testing.T) {
	t.Parallel()
	rt := makeInvalidateArrowRuntime(t, "A1")
	if rt.arrowInvalidationsUnsubscribe == nil {
		t.Fatal("I-H-1: expected arrowInvalidationsUnsubscribe captured after attachJournal")
	}
	// Manually invoke the unsubscribe; the closer must be
	// idempotent and the subscriber must be removed from the bus.
	beforeCount := rt.Bus().SubscriberCount()
	rt.arrowInvalidationsUnsubscribe()
	if got := rt.Bus().SubscriberCount(); got != beforeCount-1 {
		t.Fatalf("subscriber count after unsubscribe: got=%d want=%d",
			got, beforeCount-1)
	}
	// Re-publish: the observer is unwired; no row should land.
	rt.Bus().Publish(runner.OperatorEvent{
		Kind: runner.OpEventArrowInvalidated, ArrowID: "A1",
	})
	n, err := rt.Store().CountArrowInvalidations(context.Background(), "A1")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 rows post-unsubscribe, got %d", n)
	}
	// Defensive: closeEngine must not double-free; the field is
	// nil-checked inside closeEngine. Manually nil it so the
	// cleanup hook's closeEngine call does not re-invoke our local
	// closer.
	rt.arrowInvalidationsUnsubscribe = nil
}

// TestScenario_InvalidateArrow_ParseArgs_ReasonWithSpaces asserts
// the parser captures multi-word --reason values.
func TestScenario_InvalidateArrow_ParseArgs_ReasonWithSpaces(t *testing.T) {
	t.Parallel()
	opts, err := parseInvalidateArrowArgs("A1 --reason superseded by analyst")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.ArrowID != "A1" {
		t.Fatalf("ArrowID = %q, want A1", opts.ArrowID)
	}
	if opts.Reason != "superseded by analyst" {
		t.Fatalf("Reason = %q, want `superseded by analyst`", opts.Reason)
	}
}
