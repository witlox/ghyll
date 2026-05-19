package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

func TestScenario_ArrowShow_MissingDB_EmitsStructuredMarker(t *testing.T) {
	dir := t.TempDir()
	withUICaptured(t, func(out, _ *bytes.Buffer) {
		if err := cmdArrowShow([]string{"A1", "--dir", dir}); err != nil {
			t.Fatalf("expected clean exit on missing DB; got %v", err)
		}
		got := out.String()
		if !strings.HasPrefix(got, "ghyll-engine-status: missing\n") {
			t.Fatalf("output missing marker; got %q", got)
		}
	})
}

func TestScenario_ArrowShow_HappyPath_RendersArrow(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	// Seed an arrow with two clauses.
	if _, err := rt.Grid().Append(runner.ArrowDefinition{
		ID:         "A-checkout",
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L1",
		Context:    "checkout",
		Clauses: []runner.Clause{
			{Concept: "no-todo-marker", ClauseID: "C1"},
			{Concept: "lint-clean", ClauseID: "C2", DepthType: runner.DepthTypeSensitive, MinDepthTier: runner.DepthRankRealistic},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Drain so closeEngine can run without blocking.
	rt.journal.Flush()
	// CRITICAL: we must close BEFORE invoking the CLI because the
	// CLI opens its own read-only handle and replays through a
	// fresh set of caches.
	rt.closeEngine()

	withUICaptured(t, func(out, _ *bytes.Buffer) {
		if err := cmdArrowShow([]string{"A-checkout", "--dir", workdir}); err != nil {
			t.Fatalf("cmdArrowShow: %v", err)
		}
		got := out.String()
		for _, want := range []string{
			"arrow: A-checkout",
			"source-role:  analyst",
			"target-role:  architect",
			"clauses:      2",
			"no-todo-marker",
			"lint-clean",
			"findings:     0",
			"attestations: 0",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output missing %q\n--- got ---\n%s", want, got)
			}
		}
	})
}

func TestScenario_ArrowShow_UnknownArrowErrors(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	rt.closeEngine()

	withUICaptured(t, func(_, _ *bytes.Buffer) {
		err := cmdArrowShow([]string{"A-nonexistent", "--dir", workdir})
		if err == nil {
			t.Fatal("expected unknown-arrow error")
		}
		if !strings.Contains(err.Error(), "not in grid") {
			t.Fatalf("error should mention 'not in grid'; got %v", err)
		}
	})
}

func TestScenario_ArrowShow_RejectsMissingArrowID(t *testing.T) {
	err := cmdArrowShow(nil)
	if err == nil {
		t.Fatal("expected error on missing arrow-id")
	}
	if !strings.Contains(err.Error(), "arrow-id required") {
		t.Fatalf("error should mention arrow-id; got %v", err)
	}
}

func TestScenario_ArrowShow_RejectsFlagAsArrowID(t *testing.T) {
	err := cmdArrowShow([]string{"--dir", "."})
	if err == nil {
		t.Fatal("expected error when first arg is a flag")
	}
}

func TestScenario_ArrowMain_UnknownSubcommand(t *testing.T) {
	err := cmdArrowMain([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown-subcommand error; got %v", err)
	}
}

func TestScenario_ArrowMain_UsageOnEmpty(t *testing.T) {
	err := cmdArrowMain(nil)
	if err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("expected usage line; got %v", err)
	}
}
