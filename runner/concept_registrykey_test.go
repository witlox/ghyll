package runner

import "testing"

func TestScenario_ConceptRegistryKey_UniversalReturnsBare(t *testing.T) {
	t.Parallel()
	c := Clause{Concept: "no-todo-marker", Args: map[string]any{"scope": "*.go"}}
	if got, want := ConceptRegistryKey(c), "no-todo-marker"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestScenario_ConceptRegistryKey_LanguageBoundReturnsCompound(t *testing.T) {
	t.Parallel()
	c := Clause{Concept: "compiles", Args: map[string]any{"language": "go"}}
	if got, want := ConceptRegistryKey(c), "compiles.go"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestScenario_ConceptRegistryKey_LanguageBoundMissingLanguageArg(t *testing.T) {
	t.Parallel()
	c := Clause{Concept: "compiles", Args: map[string]any{}}
	if got, want := ConceptRegistryKey(c), "compiles."; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestScenario_ConceptRegistryKey_LanguageBoundMalformedLanguageArg(t *testing.T) {
	t.Parallel()
	c := Clause{Concept: "compiles", Args: map[string]any{"language": 42}}
	if got, want := ConceptRegistryKey(c), "compiles."; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestScenario_Registry_SnapshotIsolation(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	RegisterBuiltins(r)
	snap := r.Snapshot()
	// Mutate the source: a Replace should leave the snapshot
	// untouched.
	if err := r.Replace("no-todo-marker", EvaluateNoTodoMarker); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	_, srcIdent, _ := r.Lookup("no-todo-marker")
	_, snapIdent, _ := snap.Lookup("no-todo-marker")
	if srcIdent.Generation == snapIdent.Generation {
		t.Fatalf("expected snapshot to retain pre-Replace generation, src=%d snap=%d",
			srcIdent.Generation, snapIdent.Generation)
	}
}

func TestScenario_Registry_SwapIntoAtomicity(t *testing.T) {
	t.Parallel()
	live := NewRegistry()
	RegisterBuiltins(live)

	snap := live.Snapshot()
	// Add a fresh binding to the snapshot.
	if err := snap.Register("compiles.go", EvaluateNoTodoMarker); err != nil {
		t.Fatalf("snap.Register: %v", err)
	}
	// Live registry should not yet have compiles.go.
	if _, _, ok := live.Lookup("compiles.go"); ok {
		t.Fatalf("live should not have compiles.go before swap")
	}
	snap.SwapInto(live)
	if _, _, ok := live.Lookup("compiles.go"); !ok {
		t.Fatalf("live should have compiles.go after swap")
	}
}
