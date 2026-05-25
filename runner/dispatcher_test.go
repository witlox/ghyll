package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// dispatcherFixture wires up a PassDispatcher with all its
// dependencies. Each test composes a fresh fixture so state
// doesn't leak.
type dispatcherFixture struct {
	locks    *RoleContextLockTable
	passes   *PassRegistry
	bus      *OperatorBus
	registry *Registry
	runner   *Runner
}

func newDispatcherFixture(t *testing.T) *dispatcherFixture {
	t.Helper()
	reg := NewRegistry()
	RegisterBuiltins(reg)
	bus := NewOperatorBus()
	// Diamond v4 / R6: RequireAuditSubscriber refuses when the bus
	// has no audit-tagged subscriber. Production wires the JSONL
	// writer via SubscribeTagged(_, "audit"); the dispatcher
	// fixture mirrors that membership marker so existing
	// pre-diamond-v4 tests still pass.
	bus.SubscribeTagged(func(OperatorEvent) {}, "audit")
	return &dispatcherFixture{
		locks:    NewRoleContextLockTable(),
		passes:   NewPassRegistry(),
		bus:      bus,
		registry: reg,
		runner:   NewRunner(reg, nil, DepthRankNone).WithActualTier(DepthRankRealistic),
	}
}

func (f *dispatcherFixture) dispatcher() *PassDispatcher {
	counter := 0
	return &PassDispatcher{
		LockTable: f.locks,
		Passes:    f.passes,
		Bus:       f.bus,
		RunnerFactory: func(_ DepthRank) *Runner {
			return f.runner
		},
		SeverityThreshold: SeverityMedium,
		PassIDGen: func() string {
			counter++
			return "P-test-" + itoa(counter)
		},
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('A'+(n-10)/10)) + string(rune('0'+n%10))
}

// happyArrow returns a one-clause arrow that passes the
// no-todo-marker built-in (the project-dir is /tmp; markers list
// is empty so any scan trivially passes).
func happyArrow() ArrowDefinition {
	return ArrowDefinition{
		ID:         "A1",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []Clause{
			{
				Concept:  "no-todo-marker",
				ClauseID: "C1",
				Args:     map[string]any{"scope": "**", "markers": []any{"TODO"}},
			},
		},
	}
}

func TestScenario_Dispatcher_HappyPath_RunsClausesAndClosesPass(t *testing.T) {
	f := newDispatcherFixture(t)
	d := f.dispatcher()

	res, err := d.Dispatch(context.Background(), DispatchRequest{
		Role:       "analyst",
		Context:    "checkout",
		Arrow:      happyArrow(),
		ActualTier: DepthRankRealistic,
		ProjectDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("Runs len = %d; want 1", len(res.Runs))
	}
	if res.PassID == "" {
		t.Fatal("PassID should be set")
	}
	if f.passes.Len() != 0 {
		t.Fatalf("PassRegistry should be empty after Dispatch; got %d", f.passes.Len())
	}
	if f.locks.Len() != 0 {
		t.Fatalf("LockTable should be empty after Dispatch; got %d", f.locks.Len())
	}
}

func TestScenario_Dispatcher_PassRegisteredDuringEvaluate(t *testing.T) {
	f := newDispatcherFixture(t)
	d := f.dispatcher()
	// Wire a custom builtin that asserts the pass is registered
	// at the moment Evaluate fires.
	called := false
	f.registry.Replace("no-todo-marker", Evaluator(func(_ context.Context, c Clause) (*Result, error) {
		called = true
		if f.passes.Len() != 1 {
			t.Errorf("PassRegistry.Len at Evaluate time = %d; want 1",
				f.passes.Len())
		}
		if holder, ok := f.locks.InspectHolder("analyst", "checkout"); !ok || holder == "" {
			t.Errorf("LockTable.InspectHolder = (%q, %v); want held", holder, ok)
		}
		return &Result{Pass: true, Details: map[string]any{}}, nil
	}))
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("custom evaluator was not invoked")
	}
}

func TestScenario_Dispatcher_BusyReturnsRoleContextBusy(t *testing.T) {
	f := newDispatcherFixture(t)
	// Pre-hold the lock on (analyst, checkout).
	_, _ = f.locks.TryAcquire("analyst", "checkout", "P-pre", 0)

	d := f.dispatcher()
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
	})
	var busy *ErrRoleContextBusy
	if !errors.As(err, &busy) {
		t.Fatalf("expected *ErrRoleContextBusy; got %v", err)
	}
	if busy.HoldingPass != "P-pre" {
		t.Fatalf("HoldingPass = %q; want P-pre", busy.HoldingPass)
	}
}

func TestScenario_Dispatcher_ClauseErrorAbortsPass(t *testing.T) {
	f := newDispatcherFixture(t)
	// Custom evaluator that errors.
	f.registry.Replace("no-todo-marker", Evaluator(func(_ context.Context, _ Clause) (*Result, error) {
		return nil, errors.New("evaluator-blew-up")
	}))
	d := f.dispatcher()
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
	})
	if !errors.Is(err, ErrDispatcherClauseEval) {
		t.Fatalf("got %v; want ErrDispatcherClauseEval", err)
	}
	if f.locks.Len() != 0 {
		t.Fatal("LockTable not released after abort")
	}
	if f.passes.Len() != 0 {
		t.Fatal("PassRegistry not cleaned after abort")
	}
}

func TestScenario_Dispatcher_ContextCancelAbortsPass(t *testing.T) {
	f := newDispatcherFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := f.dispatcher()
	_, err := d.Dispatch(ctx, DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
	})
	if err == nil {
		t.Fatal("expected context-cancel error")
	}
	if f.locks.Len() != 0 {
		t.Fatal("LockTable not released after cancel")
	}
}

func TestScenario_Dispatcher_PublishesPassLifecycleEvents(t *testing.T) {
	f := newDispatcherFixture(t)
	var events []OperatorEventKind
	f.bus.Subscribe(func(e OperatorEvent) { events = append(events, e.Kind) })

	d := f.dispatcher()
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
		ProjectDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	saw := func(k OperatorEventKind) bool {
		for _, e := range events {
			if e == k {
				return true
			}
		}
		return false
	}
	if !saw(OpEventPassOpened) {
		t.Errorf("missing pass-opened; got %v", events)
	}
	if !saw(OpEventPassClosed) {
		t.Errorf("missing pass-closed; got %v", events)
	}
}

func TestScenario_Dispatcher_ValidationErrors(t *testing.T) {
	f := newDispatcherFixture(t)
	cases := []struct {
		name string
		mod  func(d *PassDispatcher)
		want error
	}{
		{"nil LockTable", func(d *PassDispatcher) { d.LockTable = nil }, ErrDispatcherNoLockTable},
		{"nil Passes", func(d *PassDispatcher) { d.Passes = nil }, ErrDispatcherNoPasses},
		{"nil RunnerFactory", func(d *PassDispatcher) { d.RunnerFactory = nil }, ErrDispatcherNoFactory},
		{"nil PassIDGen", func(d *PassDispatcher) { d.PassIDGen = nil }, ErrDispatcherNoPassIDGen},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := f.dispatcher()
			c.mod(d)
			_, err := d.Dispatch(context.Background(), DispatchRequest{
				Role: "analyst", Context: "checkout",
				Arrow: happyArrow(), ActualTier: DepthRankRealistic,
			})
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v; want %v", err, c.want)
			}
		})
	}
}

func TestScenario_Dispatcher_PassIDStableAcrossAllClauses(t *testing.T) {
	f := newDispatcherFixture(t)
	seen := make(map[string]struct{})
	f.registry.Replace("no-todo-marker", Evaluator(func(_ context.Context, c Clause) (*Result, error) {
		seen[c.PassID] = struct{}{}
		return &Result{Pass: true, Details: map[string]any{}}, nil
	}))
	arrow := happyArrow()
	arrow.Clauses = []Clause{
		{Concept: "no-todo-marker", ClauseID: "C1", Args: map[string]any{"scope": "**", "markers": []any{}}},
		{Concept: "no-todo-marker", ClauseID: "C2", Args: map[string]any{"scope": "**", "markers": []any{}}},
		{Concept: "no-todo-marker", ClauseID: "C3", Args: map[string]any{"scope": "**", "markers": []any{}}},
	}
	d := f.dispatcher()
	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: arrow, ActualTier: DepthRankRealistic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("PassID set across clauses = %v; want size 1 (stable)", seen)
	}
}

func TestScenario_Dispatcher_LockTTL_AutoExpiresAfterDeadline(t *testing.T) {
	f := newDispatcherFixture(t)
	clock := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	f.locks = NewRoleContextLockTable().WithClock(func() time.Time { return clock })

	d := f.dispatcher()
	d.LockTable = f.locks
	d.DefaultLockTTL = 100 * time.Millisecond
	d.Now = func() time.Time { return clock }

	// Pre-hold with a stale token whose TTL is in the past.
	_, _ = f.locks.TryAcquire("analyst", "checkout", "P-stale", 50*time.Millisecond)
	clock = clock.Add(200 * time.Millisecond)

	_, err := d.Dispatch(context.Background(), DispatchRequest{
		Role: "analyst", Context: "checkout",
		Arrow: happyArrow(), ActualTier: DepthRankRealistic,
		ProjectDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("expected dispatcher to sweep stale TTL and proceed; got %v", err)
	}
}
