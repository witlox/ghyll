package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClauseStatus_String(t *testing.T) {
	cases := map[ClauseStatus]string{
		StatusPending:     "pending",
		StatusRunning:     "running",
		StatusPass:        "pass",
		StatusFail:        "fail",
		StatusUnevaluated: "unevaluated",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("ClauseStatus(%d).String() = %q; want %q", s, got, want)
		}
	}
}

func TestCanTransition(t *testing.T) {
	legal := []struct {
		from, to ClauseStatus
	}{
		{StatusPending, StatusRunning},
		{StatusRunning, StatusPass},
		{StatusRunning, StatusFail},
		{StatusRunning, StatusUnevaluated},
	}
	for _, c := range legal {
		if !CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s → %s) = false; want true", c.from, c.to)
		}
	}
	illegal := []struct {
		from, to ClauseStatus
	}{
		{StatusPending, StatusPass},        // must go through running
		{StatusPending, StatusFail},        // same
		{StatusPending, StatusUnevaluated}, // F29: dead edge removed
		{StatusPass, StatusFail},           // terminal
		{StatusFail, StatusPass},           // terminal
		{StatusUnevaluated, StatusPass},    // terminal
		{StatusRunning, StatusPending},     // no backwards
	}
	for _, c := range illegal {
		if CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s → %s) = true; want false", c.from, c.to)
		}
	}
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Errorf("Count() = %d; want 0 on empty registry", r.Count())
	}
	called := false
	stub := func(ctx context.Context, c Clause) (*Result, error) {
		called = true
		return &Result{Pass: true}, nil
	}
	if err := r.Register("test-concept", stub); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if r.Count() != 1 {
		t.Errorf("Count() = %d; want 1", r.Count())
	}
	got, identity, ok := r.Lookup("test-concept")
	if !ok || got == nil {
		t.Fatalf("Lookup returned %v, ok=%v", got, ok)
	}
	if identity.Concept != "test-concept" || identity.Generation != 1 {
		t.Errorf("identity = %+v; want {test-concept, 1}", identity)
	}
	_, _ = got(context.Background(), Clause{})
	if !called {
		t.Error("looked-up evaluator did not run")
	}
}

func TestRegistry_LookupMissing(t *testing.T) {
	r := NewRegistry()
	if _, _, ok := r.Lookup("nothing"); ok {
		t.Error("Lookup of missing concept should return false")
	}
}

func TestRegistry_RegisterRefusesDuplicate(t *testing.T) {
	// Per validation-pass-2 F14: Register refuses silent overwrite;
	// callers use Replace explicitly to amend.
	r := NewRegistry()
	if err := r.Register("x", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	err := r.Register("x", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: false}, nil
	})
	if !errors.Is(err, ErrConceptAlreadyRegistered) {
		t.Errorf("second Register: got %v; want ErrConceptAlreadyRegistered", err)
	}
}

func TestRegistry_ReplaceBumpsGeneration(t *testing.T) {
	r := NewRegistry()
	if err := r.Register("x", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, idV1, _ := r.Lookup("x")
	if err := r.Replace("x", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: false}, nil
	}); err != nil {
		t.Fatal(err)
	}
	e, idV2, _ := r.Lookup("x")
	if idV2.Generation != idV1.Generation+1 {
		t.Errorf("Replace did not bump Generation: %d → %d", idV1.Generation, idV2.Generation)
	}
	res, _ := e(context.Background(), Clause{})
	if res.Pass {
		t.Error("Replace did not install new evaluator")
	}
}

func TestRegistry_ReplaceMissing(t *testing.T) {
	r := NewRegistry()
	err := r.Replace("nope", func(ctx context.Context, c Clause) (*Result, error) {
		return nil, nil
	})
	if err == nil {
		t.Error("Replace on unregistered concept should error")
	}
}

func TestRunner_EvaluatePass(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("trivial-pass", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: true, Details: map[string]any{"hits": []map[string]any{}}}, nil
	})
	r := NewRunner(reg, nil, DepthRankNone).
		WithClock(fixedClock(t)).
		WithIDGen(func() string { return "ev-fixed-id" })
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "trivial-pass"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if run.ClauseID != "C1" || run.PassID != "P1" {
		t.Errorf("run identity wrong: %+v", run)
	}
	if run.ID != "ev-fixed-id" {
		t.Errorf("ID = %q; want ev-fixed-id (id-gen override)", run.ID)
	}
	if run.StartStatus != StatusPending {
		t.Errorf("StartStatus = %s; want pending", run.StartStatus)
	}
	if run.EndStatus != StatusPass {
		t.Errorf("EndStatus = %s; want pass", run.EndStatus)
	}
	if run.Result == nil || !run.Result.Pass {
		t.Errorf("Result = %+v", run.Result)
	}
	if !run.CompletedAt.After(run.StartedAt) && !run.CompletedAt.Equal(run.StartedAt) {
		t.Error("CompletedAt must be >= StartedAt")
	}
}

func TestRunner_EvaluateFail(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("trivial-fail", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: false, Details: map[string]any{"hits": []any{"some-hit"}}}, nil
	})
	r := NewRunner(reg, nil, DepthRankNone)
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "trivial-fail"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if run.EndStatus != StatusFail {
		t.Errorf("EndStatus = %s; want fail", run.EndStatus)
	}
}

func TestRunner_EvaluateUnevaluated(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("returns-unevaluated", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Unevaluated: true, Reason: "no signal available"}, nil
	})
	r := NewRunner(reg, nil, DepthRankNone)
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "returns-unevaluated"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if run.EndStatus != StatusUnevaluated {
		t.Errorf("EndStatus = %s; want unevaluated", run.EndStatus)
	}
}

func TestRunner_EvaluateUnknownConcept(t *testing.T) {
	reg := NewRegistry()
	r := NewRunner(reg, nil, DepthRankNone)
	_, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "no-such"})
	if !errors.Is(err, ErrEvaluatorUnknown) {
		t.Errorf("err = %v; want ErrEvaluatorUnknown", err)
	}
}

func TestRunner_EvaluateEvaluatorError(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("errors", func(ctx context.Context, c Clause) (*Result, error) {
		return nil, errors.New("binding broke")
	})
	r := NewRunner(reg, nil, DepthRankNone)
	run, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "errors"})
	if err == nil {
		t.Fatal("expected evaluator error to propagate")
	}
	if run.EndStatus != StatusFail {
		t.Errorf("EndStatus = %s; want fail (evaluator errored)", run.EndStatus)
	}
}

func TestRunner_EvaluateNilResult(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("returns-nil", func(ctx context.Context, c Clause) (*Result, error) {
		return nil, nil
	})
	r := NewRunner(reg, nil, DepthRankNone)
	_, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "returns-nil"})
	if !errors.Is(err, ErrEvaluatorReturnNil) {
		t.Errorf("err = %v; want ErrEvaluatorReturnNil", err)
	}
}

func TestRunner_EvaluatorPanicCaught(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("panics", func(ctx context.Context, c Clause) (*Result, error) {
		panic("evaluator went boom")
	})
	r := NewRunner(reg, nil, DepthRankNone)
	_, err := r.Evaluate(context.Background(), "C1", "P1", Clause{Concept: "panics"})
	if !errors.Is(err, ErrEvaluatorPanicked) {
		t.Errorf("err = %v; want ErrEvaluatorPanicked", err)
	}
}

func TestRunner_EvaluateRequiresIDs(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("x", func(ctx context.Context, c Clause) (*Result, error) {
		return &Result{Pass: true}, nil
	})
	r := NewRunner(reg, nil, DepthRankNone)
	if _, err := r.Evaluate(context.Background(), "", "P", Clause{Concept: "x"}); err == nil {
		t.Error("empty clauseID should error")
	}
	if _, err := r.Evaluate(context.Background(), "C", "", Clause{Concept: "x"}); err == nil {
		t.Error("empty passID should error")
	}
}

func TestRunner_NilRegistry(t *testing.T) {
	r := &Runner{}
	_, err := r.Evaluate(context.Background(), "C", "P", Clause{Concept: "x"})
	if err == nil {
		t.Error("nil registry should error")
	}
}

func TestEvaluationRun_Duration(t *testing.T) {
	start := time.Now()
	end := start.Add(50 * time.Millisecond)
	run := &EvaluationRun{StartedAt: start, CompletedAt: end}
	if run.Duration() != 50*time.Millisecond {
		t.Errorf("Duration = %v; want 50ms", run.Duration())
	}
	var nilRun *EvaluationRun
	if nilRun.Duration() != 0 {
		t.Error("nil receiver Duration should be 0")
	}
}

// fixedClock returns a clock function that advances by 1ns on each
// call. Lets tests verify CompletedAt > StartedAt deterministically.
func fixedClock(t *testing.T) func() time.Time {
	t.Helper()
	start := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	calls := 0
	return func() time.Time {
		calls++
		return start.Add(time.Duration(calls))
	}
}
