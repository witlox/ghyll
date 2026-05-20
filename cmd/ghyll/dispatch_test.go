package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/witlox/ghyll/runner"
)

// Tests for the top-level subcommand dispatchers (cmdEngineMain,
// cmdMemoryMain, cmdArrowMain). Each exercises the routing
// branches without invoking the underlying subcommand bodies
// (those have their own tests).

func TestScenario_Dispatch_EngineMain_Status(t *testing.T) {
	// Empty args is the usage error.
	if err := cmdEngineMain(nil); err == nil {
		t.Fatal("empty args should error")
	}
	if err := cmdEngineMain([]string{"unknown"}); err == nil ||
		!strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("unknown subcommand: %v", err)
	}
	// `status --dir <tmp>` exercises the dispatch -> cmdEngineStatus
	// happy path. The missing-DB path returns nil; we just verify
	// the dispatcher routes to it.
	dir := t.TempDir()
	if err := cmdEngineMain([]string{"status", "--dir", dir}); err != nil {
		t.Fatalf("status dispatch: %v", err)
	}
	if err := cmdEngineMain([]string{"replay", "--dir", dir}); err != nil {
		t.Fatalf("replay dispatch: %v", err)
	}
	if err := cmdEngineMain([]string{"verify-attestations", "--dir", dir}); err != nil {
		t.Fatalf("verify-attestations dispatch: %v", err)
	}
}

// memoryHome stages a $HOME with .ghyll/ pre-created so
// memory.OpenStore in cmdMemoryMain can build its sqlite db.
func memoryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ghyll"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestScenario_Dispatch_MemoryMain_UnknownSubcommand(t *testing.T) {
	memoryHome(t)
	if err := cmdMemoryMain(nil); err == nil {
		t.Fatal("empty args should error")
	}
	if err := cmdMemoryMain([]string{"unknown"}); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown subcommand: %v", err)
	}
}

func TestScenario_Dispatch_MemoryMain_SearchMissingQuery(t *testing.T) {
	memoryHome(t)
	if err := cmdMemoryMain([]string{"search"}); err == nil ||
		!strings.Contains(err.Error(), "usage:") {
		t.Fatalf("search without query should error with usage: got %v", err)
	}
}

// TestScenario_Dispatch_FindingStatusName covers every branch of
// the findingStatusName helper so arrow_cmd's status rendering is
// exercised across all valid status values.
func TestScenario_Dispatch_FindingStatusName(t *testing.T) {
	cases := map[runner.FindingStatus]string{
		runner.FindingStatusOpen:         "open",
		runner.FindingStatusRunning:      "running",
		runner.FindingStatusResolved:     "resolved",
		runner.FindingStatusAcceptedRisk: "accepted-risk",
		runner.FindingStatusUnevaluated:  "unevaluated",
	}
	for in, want := range cases {
		if got := findingStatusName(in); got != want {
			t.Errorf("findingStatusName(%v) = %q; want %q", in, got, want)
		}
	}
	// Out-of-range fallback.
	got := findingStatusName(runner.FindingStatus(99))
	if !strings.HasPrefix(got, "status-") {
		t.Errorf("out-of-range status should produce status-N; got %q", got)
	}
}

// TestScenario_Dispatch_RedirectSlogToFile_HappyPath verifies that
// the diagnostic-routing helper does not error when the workdir
// is writable. Reads back the file existence as a proxy for
// successful redirection.
func TestScenario_Dispatch_RedirectSlogToFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	// redirectSlogToFile mutates the package-level slog default
	// — we restore it via initLogger afterward so other tests see
	// the stderr handler.
	t.Cleanup(initLogger)
	redirectSlogToFile(dir)
	logPath := filepath.Join(dir, ".ghyll", "ghyll.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file not created at %s: %v", logPath, err)
	}
}

// TestScenario_Dispatch_InitLogger_Idempotent verifies initLogger
// can be called multiple times safely (e.g., after
// redirectSlogToFile's t.Cleanup restoration).
func TestScenario_Dispatch_InitLogger_Idempotent(t *testing.T) {
	for i := 0; i < 3; i++ {
		initLogger()
	}
}

// TestScenario_Dispatch_EngineStatus_PopulatedDB exercises the
// happy-path of cmdEngineStatus + cmdEngineReplay against a real
// (but empty-ish) engine.db. Lifts cmd/ghyll coverage on the
// status / replay branches.
func TestScenario_Dispatch_EngineStatus_PopulatedDB(t *testing.T) {
	rt, workdir := newTier0Runtime(t)
	// Replay + attach + flush + close so the engine.db exists on
	// disk for the CLI to open read-only.
	if _, err := rt.replayEngine(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rt.attachJournal(nil); err != nil {
		t.Fatal(err)
	}
	rec := runner.AttestationRecord{
		ID: "att-A1-C1-v1", Kind: runner.AttestationKindDepthType,
		ArrowID: "A1", ClauseID: "C1", OpID: "test",
		AttestedByRole: "operator", SourceRole: "analyst", TargetRole: "architect",
		Verdict: runner.AttestationPass, Timestamp: 1, GridVersion: 1,
		PassID: "P-dispatch", Context: "default", Stratum: "L1",
	}
	_ = rt.AttestationStore().Record(rec)
	rt.journal.Flush()
	rt.closeEngine()

	// `status` against a populated DB.
	withUICaptured(t, func(out, _ *bytes.Buffer) {
		if err := cmdEngineStatus([]string{"--dir", workdir}); err != nil {
			t.Fatalf("status on populated DB: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "ghyll-engine-status: present") {
			t.Fatalf("status output missing present marker; got %q", got)
		}
		if !strings.Contains(got, "attestations:") {
			t.Fatalf("status output missing attestations line; got %q", got)
		}
	})

	// `replay` against the same DB.
	withUICaptured(t, func(out, _ *bytes.Buffer) {
		if err := cmdEngineReplay([]string{"--dir", workdir}); err != nil {
			t.Fatalf("replay on populated DB: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "replay:") {
			t.Fatalf("replay output missing header; got %q", got)
		}
	})
}

// TestScenario_Dispatch_MemoryMain_LogAndSearchDispatch covers
// the log + search dispatch branches of cmdMemoryMain (the
// underlying cmdMemoryLog / cmdMemorySearch already have direct
// tests in memory_cmd_test.go; this exercises the dispatcher
// routing).
func TestScenario_Dispatch_MemoryMain_LogAndSearchDispatch(t *testing.T) {
	memoryHome(t)
	// Empty store → "no checkpoints" is the expected output.
	if err := cmdMemoryMain([]string{"log"}); err != nil {
		t.Fatalf("log dispatch on empty store: %v", err)
	}
	if err := cmdMemoryMain([]string{"search", "anything"}); err != nil {
		t.Fatalf("search dispatch on empty store: %v", err)
	}
}

// TestScenario_Dispatch_ArrowMain_DispatchBranches covers cmdArrowMain's
// dispatch table.
func TestScenario_Dispatch_ArrowMain_DispatchBranches(t *testing.T) {
	if err := cmdArrowMain(nil); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("empty args should yield usage: got %v", err)
	}
	if err := cmdArrowMain([]string{"bogus"}); err == nil ||
		!strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("bogus subcommand: got %v", err)
	}
}

// TestScenario_Dispatch_HandoffFlushError exercises the
// handoffFlushError type's Error / Unwrap methods that the
// handleHandoff path uses for the M3 blocking-flush check.
func TestScenario_Dispatch_HandoffFlushError(t *testing.T) {
	inner := errors.New("disk full")
	e := &handoffFlushError{stage: "commit", inner: inner}
	if !strings.Contains(e.Error(), "commit") || !strings.Contains(e.Error(), "disk full") {
		t.Errorf("Error() should include stage + inner; got %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is should reach the wrapped error")
	}
}
