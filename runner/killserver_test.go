package runner

import (
	"context"
	"path/filepath"
	"testing"
)

// statefulTestSetup writes a sentinel file that the "test"
// command reads to decide pass/fail. The "kill" command removes
// the sentinel; "unkill" recreates it. So:
//
//	test passes when sentinel exists
//	kill removes sentinel
//	test after kill fails (sentinel missing)
//
// Models a real dependency: the test depends on the sentinel.
// A "mocked" test ignores the sentinel and always passes.
func statefulTestSetup(t *testing.T) (dir, testCmd, killCmd, unkillCmd string) {
	t.Helper()
	dir = t.TempDir()
	sentinel := filepath.Join(dir, "dep-live")
	writeFile(t, dir, "dep-live", "running\n")
	// Test command: exit 0 if sentinel exists, else exit 1.
	testCmd = "test -f " + shellQuote(sentinel)
	killCmd = "rm -f " + shellQuote(sentinel)
	unkillCmd = "echo running > " + shellQuote(sentinel)
	return
}

func TestKillServerFailsIntegration_RealDep(t *testing.T) {
	dir, testCmd, killCmd, unkillCmd := statefulTestSetup(t)
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"test-command":        testCmd,
			"critical-deps":       []any{"postgres"},
			"kill-cmd.postgres":   killCmd,
			"unkill-cmd.postgres": unkillCmd,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass (kill caused fail); got %+v", res.Details)
	}
	if got := res.Details["deps-mocked"]; got != 0 {
		t.Errorf("deps-mocked = %v; want 0", got)
	}
}

func TestKillServerFailsIntegration_MockedDep(t *testing.T) {
	// "test" always passes (mocked). kill of postgres should NOT
	// cause test to fail → clause fails.
	dir := t.TempDir()
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"test-command":      "true", // always passes — mocked test
			"critical-deps":     []any{"postgres"},
			"kill-cmd.postgres": "true", // kill succeeds (no-op)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("expected fail (kill didn't cause test failure → suite is mocking); got %+v", res.Details)
	}
	mocked, _ := res.Details["mocked-deps"].([]string)
	if len(mocked) != 1 || mocked[0] != "postgres" {
		t.Errorf("mocked-deps = %v; want [postgres]", mocked)
	}
}

func TestKillServerFailsIntegration_MissingTestCommand(t *testing.T) {
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		Args: map[string]any{
			"critical-deps": []any{"db"},
			"kill-cmd.db":   "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("expected Unevaluated (no test-command); got %+v", res)
	}
}

func TestKillServerFailsIntegration_MissingKillCmd(t *testing.T) {
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		Args: map[string]any{
			"test-command":  "true",
			"critical-deps": []any{"db1", "db2"},
			"kill-cmd.db1":  "true",
			// kill-cmd.db2 missing
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("expected Unevaluated (missing kill-cmd); got %+v", res)
	}
	missing, _ := res.Details["missing-kill-cmds"].([]string)
	if len(missing) != 1 || missing[0] != "db2" {
		t.Errorf("missing = %v; want [db2]", missing)
	}
}

func TestKillServerFailsIntegration_BaselineFails(t *testing.T) {
	// test command itself fails → Unevaluated (can't measure
	// kill-causes-fail without a passing baseline).
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		Args: map[string]any{
			"test-command":  "false", // always exit 1
			"critical-deps": []any{"db"},
			"kill-cmd.db":   "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("expected Unevaluated (baseline fails); got %+v", res)
	}
}

func TestKillServerFailsIntegration_EmptyCriticalDeps(t *testing.T) {
	res, err := EvaluateKillServerFailsIntegration(context.Background(), Clause{
		Args: map[string]any{
			"test-command":  "true",
			"critical-deps": []any{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unevaluated {
		t.Errorf("expected Unevaluated (empty critical-deps); got %+v", res)
	}
}
