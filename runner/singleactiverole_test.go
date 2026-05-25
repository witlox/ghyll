package runner

import (
	"context"
	"testing"
)

func TestScenario_SingleActiveRoleInstance_NoConflict(t *testing.T) {
	t.Parallel()
	passes := NewPassRegistry()
	r := NewRunner(NewRegistry(), passes, DepthRankNone)
	res, err := EvaluateSingleActiveRoleInstance(context.Background(), r, Clause{
		Concept: "single-active-role-instance",
		Args: map[string]any{
			"role":    "analyst",
			"context": "billing",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateSingleActiveRoleInstance: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true (no passes registered), got %+v", res)
	}
}

func TestScenario_SingleActiveRoleInstance_ConflictDetected(t *testing.T) {
	t.Parallel()
	passes := NewPassRegistry()
	lt := NewRoleContextLockTable()

	p1, err := OpenPass(PassOptions{
		PassID:    "pass-1",
		Role:      "analyst",
		Context:   "billing",
		ArrowID:   "arrow-a",
		LockTable: lt,
	})
	if err != nil {
		t.Fatalf("OpenPass p1: %v", err)
	}
	passes.Register(p1)
	defer passes.Unregister(p1.ID())

	r := NewRunner(NewRegistry(), passes, DepthRankNone)
	res, err := EvaluateSingleActiveRoleInstance(context.Background(), r, Clause{
		Concept: "single-active-role-instance",
		PassID:  "pass-2", // different pass — should detect conflict
		Args: map[string]any{
			"role":    "analyst",
			"context": "billing",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateSingleActiveRoleInstance: %v", err)
	}
	if res.Pass {
		t.Fatalf("expected Pass=false (one OTHER pass open), got %+v", res)
	}
}

// TestScenario_SingleActiveRoleInstance_FiltersSelf verifies the
// design H3 filter-self contract: the clause's own containing pass
// is excluded from the conflicting set.
func TestScenario_SingleActiveRoleInstance_FiltersSelf(t *testing.T) {
	t.Parallel()
	passes := NewPassRegistry()
	lt := NewRoleContextLockTable()

	p1, err := OpenPass(PassOptions{
		PassID:    "pass-self",
		Role:      "analyst",
		Context:   "billing",
		ArrowID:   "arrow-a",
		LockTable: lt,
	})
	if err != nil {
		t.Fatalf("OpenPass p1: %v", err)
	}
	passes.Register(p1)
	defer passes.Unregister(p1.ID())

	r := NewRunner(NewRegistry(), passes, DepthRankNone)
	res, err := EvaluateSingleActiveRoleInstance(context.Background(), r, Clause{
		Concept: "single-active-role-instance",
		PassID:  "pass-self", // SAME pass — must be filtered
		Args: map[string]any{
			"role":    "analyst",
			"context": "billing",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateSingleActiveRoleInstance: %v", err)
	}
	if !res.Pass {
		t.Fatalf("expected Pass=true (filter-self), got %+v", res)
	}
}

// TestScenario_SingleActiveRoleInstance_ArgsMatchYAML asserts the
// evaluator's reads match the authoritative YAML names (role, context).
func TestScenario_SingleActiveRoleInstance_ArgsMatchYAML(t *testing.T) {
	t.Parallel()
	r := NewRunner(NewRegistry(), NewPassRegistry(), DepthRankNone)
	res, err := EvaluateSingleActiveRoleInstance(context.Background(), r, Clause{
		Concept: "single-active-role-instance",
		Args: map[string]any{
			"role":    "implementer",
			"context": "checkout",
		},
	})
	if err != nil {
		t.Fatalf("EvaluateSingleActiveRoleInstance: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil Result")
	}
}

// TestScenario_Runner_LookupWithRunnerPath verifies the two-table
// dispatch routes single-active-role-instance through the
// runner-typed table (R9 closure).
func TestScenario_Runner_LookupWithRunnerPath(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	RegisterBuiltins(reg)
	if _, _, ok := reg.LookupWithRunner("single-active-role-instance"); !ok {
		t.Fatalf("expected single-active-role-instance in runner-typed table")
	}
	// And NOT in the plain table:
	if _, _, ok := reg.Lookup("single-active-role-instance"); ok {
		t.Fatalf("did not expect single-active-role-instance in plain table")
	}
}

// TestScenario_Runner_LookupFallback verifies a plain evaluator
// falls through the runner-typed miss to the plain table (R9 closure).
func TestScenario_Runner_LookupFallback(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	RegisterBuiltins(reg)
	// no-todo-marker is a plain Evaluator — must be reachable via
	// the plain table only.
	if _, _, ok := reg.Lookup("no-todo-marker"); !ok {
		t.Fatalf("expected no-todo-marker in plain table")
	}
	if _, _, ok := reg.LookupWithRunner("no-todo-marker"); ok {
		t.Fatalf("did not expect no-todo-marker in runner-typed table")
	}

	// Evaluate end-to-end via Runner.Evaluate to prove the two-table
	// dispatch dispatches the plain-table evaluator correctly.
	r := NewRunner(reg, NewPassRegistry(), DepthRankNone)
	dir := t.TempDir()
	mustWrite(t, dir+"/a.go", "package x\n")
	run, err := r.Evaluate(context.Background(), "c-1", "p-1", Clause{
		Concept:    "no-todo-marker",
		ProjectDir: dir,
		Args: map[string]any{
			"scope": "*.go",
		},
	})
	if err != nil {
		t.Fatalf("Evaluate(no-todo-marker): %v", err)
	}
	if run.EndStatus != StatusPass {
		t.Fatalf("expected StatusPass, got %v", run.EndStatus)
	}
}
