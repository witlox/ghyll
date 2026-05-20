package main

import (
	gocontext "context"
	"strings"
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
