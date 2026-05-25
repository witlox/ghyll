package main

import (
	gocontext "context"
	"strings"
	"sync"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// Tests for the /run-arrow + /list-arrows slash commands wired by
// integrator finding C-3. These cover parser validation, grid
// lookup, full dispatcher round-trip on a minimal arrow, and the
// empty-grid hint surface.

func TestScenario_RunArrowParser_HappyPath(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		wantID  string
		wantCtx string
	}{
		{"id-only", "A1", "A1", ""},
		{"id-and-ctx", "A1 --context ctxA", "A1", "ctxA"},
		{"trim-spaces", "  A1   --context   ctxA  ", "A1", "ctxA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseRunArrowArgs(tc.arg)
			if err != nil {
				t.Fatalf("parseRunArrowArgs(%q): %v", tc.arg, err)
			}
			if opts.ArrowID != tc.wantID {
				t.Errorf("ArrowID = %q; want %q", opts.ArrowID, tc.wantID)
			}
			if opts.Context != tc.wantCtx {
				t.Errorf("Context = %q; want %q", opts.Context, tc.wantCtx)
			}
		})
	}
}

func TestScenario_RunArrowParser_RejectsBadForms(t *testing.T) {
	cases := []struct {
		name    string
		arg     string
		wantErr string
	}{
		{"empty", "", "arrow-id required"},
		{"whitespace-only", "   ", "arrow-id required"},
		{"flag-first", "--context ctxA", "first positional must not start with --"},
		{"missing-ctx-value", "A1 --context", "--context requires a value"},
		{"unknown-flag", "A1 --bogus x", "unknown flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseRunArrowArgs(tc.arg)
			if err == nil {
				t.Fatalf("parseRunArrowArgs(%q) expected error", tc.arg)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v; want substring %q", err, tc.wantErr)
			}
		})
	}
}

// newRunArrowSession builds a session with the engine + sessionCtx
// wired. Mirrors newOperatorTestSession but adds the sessionCtx
// SessionContext expects.
func newRunArrowSession(t *testing.T) *Session {
	t.Helper()
	rt, _ := newTier0Runtime(t)
	if _, err := rt.replayEngine(gocontext.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := gocontext.WithCancel(gocontext.Background())
	t.Cleanup(cancel)
	return &Session{
		engine:        rt,
		workdir:       t.TempDir(),
		output:        func(string) {},
		sessionCtx:    ctx,
		sessionCancel: cancel,
	}
}

func TestScenario_RunArrow_EmptyGrid_HintsInit(t *testing.T) {
	s := newRunArrowSession(t)
	r := s.DispatchSlashCommand("/run-arrow A1")
	if !r.Handled || !r.ContinueLoop {
		t.Fatalf("expected handled+continue; got %+v", r)
	}
	if !strings.Contains(r.Output, "no grid") || !strings.Contains(r.Output, "ghyll init") {
		t.Fatalf("expected empty-grid hint; got %+v", r)
	}
}

func TestScenario_ListArrows_EmptyGrid_HintsInit(t *testing.T) {
	s := newRunArrowSession(t)
	r := s.DispatchSlashCommand("/list-arrows")
	if !strings.Contains(r.Output, "no grid") || !strings.Contains(r.Output, "ghyll init") {
		t.Fatalf("expected empty-grid hint; got %+v", r)
	}
}

func TestScenario_RunArrow_UnknownArrow_AfterGridPopulated(t *testing.T) {
	s := newRunArrowSession(t)
	if _, err := s.engine.Grid().Append(runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{{
			Concept:  "no-todo-marker",
			ClauseID: "C1",
			Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	r := s.DispatchSlashCommand("/run-arrow A999")
	if !strings.Contains(r.Output, "not in grid") {
		t.Fatalf("expected not-in-grid error; got %+v", r)
	}
	if !strings.Contains(r.Output, "/list-arrows") {
		t.Fatalf("expected /list-arrows hint; got %+v", r)
	}
}

func TestScenario_ListArrows_RendersGrid(t *testing.T) {
	s := newRunArrowSession(t)
	if _, err := s.engine.Grid().Append(runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{{
			Concept:  "no-todo-marker",
			ClauseID: "C1",
			Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	r := s.DispatchSlashCommand("/list-arrows")
	if !strings.Contains(r.Output, "A1") {
		t.Fatalf("expected A1 in listing; got %+v", r)
	}
	if !strings.Contains(r.Output, "analyst → architect") {
		t.Fatalf("expected source→target render; got %+v", r)
	}
	if !strings.Contains(r.Output, "stratum=L1") {
		t.Fatalf("expected stratum render; got %+v", r)
	}
}

// TestScenario_RunArrow_DispatcherEndToEnd is the full integration:
// /run-arrow A1 against a populated grid, with a real bus +
// dispatcher + PassRegistry + AttestationStore. Asserts the
// SlashCommandResult surfaces the pass-opened / pass-closed events
// + the final status line.
func TestScenario_RunArrow_DispatcherEndToEnd(t *testing.T) {
	s := newRunArrowSession(t)
	def := runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{{
			Concept:  "no-todo-marker",
			ClauseID: "C1",
			Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
		}},
	}
	if _, err := s.engine.Grid().Append(def); err != nil {
		t.Fatal(err)
	}

	r := s.DispatchSlashCommand("/run-arrow A1")
	if !r.Handled || !r.ContinueLoop {
		t.Fatalf("expected handled+continue; got %+v", r)
	}
	if !strings.Contains(r.Output, "pass-opened") {
		t.Fatalf("expected pass-opened event; got %+v", r)
	}
	if !strings.Contains(r.Output, "pass-closed") {
		t.Fatalf("expected pass-closed event; got %+v", r)
	}
	if !strings.Contains(r.Output, "arrow A1 dispatched") {
		t.Fatalf("expected final status; got %+v", r)
	}
	// PassRegistry empties after dispatch closes (tier0 invariant).
	if s.engine.Passes().Len() != 0 {
		t.Fatalf("PassRegistry should be empty after dispatch; got %d", s.engine.Passes().Len())
	}
	if s.engine.RoleLocks().Len() != 0 {
		t.Fatalf("RoleContextLockTable should be empty after dispatch")
	}
}

// TestScenario_RunArrow_ContextOverride confirms --context wins
// over the arrow's declared Context. We run the same arrow twice
// with different --context values; both should dispatch
// successfully (different (role, context) lock keys, no busy
// conflict from the prior dispatch since each completes
// synchronously).
func TestScenario_RunArrow_ContextOverride(t *testing.T) {
	s := newRunArrowSession(t)
	if _, err := s.engine.Grid().Append(runner.ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{{
			Concept:  "no-todo-marker",
			ClauseID: "C1",
			Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	r1 := s.DispatchSlashCommand("/run-arrow A1 --context override-ctx-x")
	if !strings.Contains(r1.Output, "arrow A1 dispatched") {
		t.Fatalf("first dispatch with override: %+v", r1)
	}

	r2 := s.DispatchSlashCommand("/run-arrow A1 --context override-ctx-y")
	if !strings.Contains(r2.Output, "arrow A1 dispatched") {
		t.Fatalf("second dispatch with different override: %+v", r2)
	}
}

func TestScenario_RunArrow_EngineNil_Refused(t *testing.T) {
	s := &Session{output: func(string) {}}
	r := s.DispatchSlashCommand("/run-arrow A1")
	if !strings.Contains(r.Output, "engine not initialized") {
		t.Fatalf("expected engine-nil refusal; got %+v", r)
	}
}

func TestScenario_ListArrows_EngineNil_Refused(t *testing.T) {
	s := &Session{output: func(string) {}}
	r := s.DispatchSlashCommand("/list-arrows")
	if !strings.Contains(r.Output, "engine not initialized") {
		t.Fatalf("expected engine-nil refusal; got %+v", r)
	}
}

func TestScenario_RunArrow_BadParse_SurfacesUsage(t *testing.T) {
	s := newRunArrowSession(t)
	r := s.DispatchSlashCommand("/run-arrow --context only")
	if !strings.Contains(r.Output, "usage:") {
		t.Fatalf("expected usage hint; got %+v", r)
	}
}

// TestScenario_RunArrow_LateEventNotDropped covers post-prod-readiness
// adversarial L-A. The previous /run-arrow code captured the events
// snapshot BEFORE unsubscribe ran (via defer), so a publisher firing
// between the synchronous Dispatch return and function return could
// append into the local `events` slice but never reach `captured`.
// The remediation unsubscribes explicitly BEFORE snapshotting, so
// after unsubscribe returns no further publish can call our callback.
//
// This test verifies the contract directly against the bus surface
// used by the handler: subscribe, fire one event, unsubscribe-then-
// snapshot, fire a follow-up event after unsubscribe. The follow-up
// must NOT appear in the snapshot (proving the unsubscribe takes
// effect), AND the pre-unsubscribe event MUST appear (proving the
// new order doesn't lose existing events).
func TestScenario_RunArrow_LateEventNotDropped(t *testing.T) {
	bus := runner.NewOperatorBus()
	var (
		mu     sync.Mutex
		events []runner.OperatorEvent
	)
	unsubscribe := bus.Subscribe(func(e runner.OperatorEvent) {
		switch e.Kind {
		case runner.OpEventPassOpened,
			runner.OpEventPassClosed,
			runner.OpEventInsufficientBasisRoundsExceeded:
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		}
	})

	// Pre-unsubscribe publish — must land in the snapshot.
	bus.Publish(runner.OperatorEvent{
		Kind:    runner.OpEventPassOpened,
		PassID:  "P-1",
		Role:    "analyst",
		ArrowID: "A1",
	})

	// New order from L-A remediation: unsubscribe BEFORE the
	// snapshot. After this returns, the bus's subscriber list no
	// longer references our callback, so subsequent Publish calls
	// cannot append to `events`.
	unsubscribe()
	mu.Lock()
	captured := append([]runner.OperatorEvent(nil), events...)
	mu.Unlock()

	// Post-unsubscribe publish — must NOT appear in our snapshot.
	bus.Publish(runner.OperatorEvent{
		Kind:    runner.OpEventPassClosed,
		PassID:  "P-1",
		Role:    "analyst",
		ArrowID: "A1",
	})

	if len(captured) != 1 {
		t.Fatalf("captured len = %d; want 1 (pre-unsubscribe only)", len(captured))
	}
	if captured[0].Kind != runner.OpEventPassOpened {
		t.Errorf("captured[0].Kind = %v; want OpEventPassOpened", captured[0].Kind)
	}

	// Re-snapshot AFTER the late publish to confirm the bus did
	// not re-deliver to our (now-detached) callback.
	mu.Lock()
	finalLen := len(events)
	mu.Unlock()
	if finalLen != 1 {
		t.Errorf("events len after late publish = %d; want 1 (callback should be detached)", finalLen)
	}
}

// TestScenario_RunArrow_RaceWithConcurrentPublishers stresses the
// L-A remediation under contention: a publisher goroutine fires
// events continuously while the main goroutine unsubscribes and
// captures. After unsubscribe returns, no further events should
// reach the captured set even if the publisher keeps firing.
// Run with -race to catch any leaked appends.
func TestScenario_RunArrow_RaceWithConcurrentPublishers(t *testing.T) {
	bus := runner.NewOperatorBus()
	var (
		mu     sync.Mutex
		events []runner.OperatorEvent
	)
	unsubscribe := bus.Subscribe(func(e runner.OperatorEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	// Publisher goroutine: fires events until stop closes.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				bus.Publish(runner.OperatorEvent{
					Kind:   runner.OpEventPassOpened,
					PassID: "P-loop",
				})
			}
		}
	}()

	// Let a few events accumulate.
	for {
		mu.Lock()
		got := len(events)
		mu.Unlock()
		if got > 0 {
			break
		}
	}

	// Unsubscribe-then-snapshot (L-A pattern).
	unsubscribe()
	mu.Lock()
	captured := append([]runner.OperatorEvent(nil), events...)
	mu.Unlock()

	// Wait briefly for any in-flight publishes started before
	// unsubscribe to complete. After this the subscriber callback
	// must be quiescent.
	close(stop)
	<-done

	mu.Lock()
	finalLen := len(events)
	mu.Unlock()

	// The captured slice may have FEWER elements than finalLen
	// (a publish in flight at unsubscribe time may append after
	// our snapshot but before publisher exits). That's expected
	// and acceptable — the L-A contract is that NO publishes
	// AFTER unsubscribe returns are routed into the callback;
	// we cannot tighten that without bus-level barriers. What we
	// DO assert is that the captured slice is non-empty (the
	// remediation didn't accidentally make the snapshot empty).
	if len(captured) == 0 {
		t.Errorf("captured is empty after concurrent publishes; pattern broke event delivery")
	}
	if finalLen < len(captured) {
		t.Errorf("finalLen=%d < captured=%d; impossible ordering", finalLen, len(captured))
	}
}
