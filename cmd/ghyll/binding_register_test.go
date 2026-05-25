package main

import (
	"errors"
	"testing"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/runner"
)

func TestScenario_RegisterGridBindings_RegistersCompilesGo(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	g := &bootstrap.Grid{
		LanguageBindings: map[string]string{
			"compiles.go": "go build ./...",
		},
	}
	if err := registerGridBindings(reg, g, t.TempDir()); err != nil {
		t.Fatalf("registerGridBindings: %v", err)
	}
	if _, _, ok := reg.Lookup("compiles.go"); !ok {
		t.Fatalf("expected compiles.go to be registered")
	}
}

func TestScenario_RegisterGridBindings_RejectsInvalidKey(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	g := &bootstrap.Grid{
		LanguageBindings: map[string]string{
			"foo.go": "echo nope",
		},
	}
	err := registerGridBindings(reg, g, t.TempDir())
	if err == nil {
		t.Fatalf("expected refusal for unknown concept, got nil")
	}
	if !errors.Is(err, ErrLanguageBindingInvalid) {
		t.Fatalf("expected ErrLanguageBindingInvalid, got %v", err)
	}
}

func TestScenario_RegisterGridBindings_RejectsMalformedKey(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	g := &bootstrap.Grid{
		LanguageBindings: map[string]string{
			"compiles": "echo no-dot",
		},
	}
	err := registerGridBindings(reg, g, t.TempDir())
	if err == nil {
		t.Fatalf("expected refusal for malformed key, got nil")
	}
}

func TestScenario_RegisterGridBindings_RejectsEmptyCommand(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	g := &bootstrap.Grid{
		LanguageBindings: map[string]string{
			"compiles.go": "",
		},
	}
	err := registerGridBindings(reg, g, t.TempDir())
	if err == nil {
		t.Fatalf("expected refusal for empty command, got nil")
	}
	if !errors.Is(err, bootstrap.ErrBindingCommandEmpty) {
		t.Fatalf("expected ErrBindingCommandEmpty, got %v", err)
	}
}

func TestScenario_RegisterGridBindings_AllRequiredBindingsPresent(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	g := &bootstrap.Grid{
		LanguageBindings: map[string]string{
			"compiles.go":   "go build ./...",
			"lint-clean.go": "go vet ./...",
			"tests-pass.go": "go test ./...",
		},
	}
	if err := registerGridBindings(reg, g, t.TempDir()); err != nil {
		t.Fatalf("registerGridBindings: %v", err)
	}
	for _, k := range []string{"compiles.go", "lint-clean.go", "tests-pass.go"} {
		if _, _, ok := reg.Lookup(k); !ok {
			t.Errorf("%q not registered", k)
		}
	}
}

func TestScenario_RequiredBindingsFromUntypedGrid_ArrowsParsed(t *testing.T) {
	t.Parallel()
	g := &bootstrap.Grid{
		Arrows: []map[string]any{
			{
				"id": "arrow-1",
				"clauses": []any{
					map[string]any{
						"concept": "compiles",
						"args":    map[string]any{"language": "go"},
					},
					map[string]any{
						"concept": "no-todo-marker",
						"args":    map[string]any{"scope": "*.go"},
					},
				},
			},
		},
	}
	keys, validations, err := requiredBindingsFromUntypedGrid(g)
	if err != nil {
		t.Fatalf("requiredBindingsFromUntypedGrid: %v", err)
	}
	if len(validations) != 0 {
		t.Errorf("unexpected validations: %+v", validations)
	}
	if len(keys) != 1 || keys[0].String() != "compiles.go" {
		t.Fatalf("expected [compiles.go], got %+v", keys)
	}
}

func TestScenario_RequiredBindingsFromUntypedGrid_DedupAcrossSources(t *testing.T) {
	t.Parallel()
	g := &bootstrap.Grid{
		Arrows: []map[string]any{
			{
				"clauses": []any{
					map[string]any{"concept": "compiles", "args": map[string]any{"language": "go"}},
					map[string]any{"concept": "compiles", "args": map[string]any{"language": "go"}},
				},
			},
			{
				"clauses": []any{
					map[string]any{"concept": "compiles", "args": map[string]any{"language": "go"}},
				},
			},
		},
	}
	keys, _, err := requiredBindingsFromUntypedGrid(g)
	if err != nil {
		t.Fatalf("requiredBindingsFromUntypedGrid: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected dedup to one key, got %d: %+v", len(keys), keys)
	}
}

func TestScenario_RegisterGridBindings_MissingBindingForArrowClause(t *testing.T) {
	t.Parallel()
	reg := runner.NewRegistry()
	runner.RegisterBuiltins(reg)
	untyped := &bootstrap.Grid{
		Arrows: []map[string]any{
			{
				"clauses": []any{
					map[string]any{"concept": "compiles", "args": map[string]any{"language": "go"}},
				},
			},
		},
	}
	err := verifyBindingsCoverage(reg, nil, untyped)
	if err == nil {
		t.Fatalf("expected *MissingBindingError, got nil")
	}
	if mbe := bootstrap.AsMissingBindingError(err); mbe == nil {
		t.Fatalf("expected MissingBindingError, got %v", err)
	}
}
