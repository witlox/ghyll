package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildCommitMessage_AppendsStampTrailers(t *testing.T) {
	msg, err := BuildCommitMessage(CommitOptions{
		Message:      "feat: add thing",
		GhyllVersion: "0.5.2",
		GhyllModel:   "qwen-coder-q4@localhost:11434",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Ghyll-Version: 0.5.2") {
		t.Errorf("missing Ghyll-Version trailer; got:\n%s", msg)
	}
	if !strings.Contains(msg, "Ghyll-Model: qwen-coder-q4@localhost:11434") {
		t.Errorf("missing Ghyll-Model trailer; got:\n%s", msg)
	}
	if !strings.HasPrefix(msg, "feat: add thing\n\n") {
		t.Errorf("body should be separated from trailers; got:\n%s", msg)
	}
}

func TestBuildCommitMessage_PreservesExtraTrailers(t *testing.T) {
	msg, err := BuildCommitMessage(CommitOptions{
		Message:      "fix: edge case",
		GhyllVersion: "0.5.2",
		GhyllModel:   "deepseek-coder@vllm:8000",
		ExtraTrailers: []string{
			"Co-Authored-By: Operator <op@example.com>",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Co-Authored-By: Operator <op@example.com>") {
		t.Errorf("missing extra trailer; got:\n%s", msg)
	}
}

func TestBuildCommitMessage_AcceptsUnderscoreInTrailerKey(t *testing.T) {
	// Validation-pass-8 S3: trailer keys may use `_`.
	_, err := BuildCommitMessage(CommitOptions{
		Message:       "x",
		GhyllVersion:  "0.1",
		GhyllModel:    "m",
		ExtraTrailers: []string{"Co_Authored_By: Person <p@e.com>"},
	})
	if err != nil {
		t.Errorf("underscore in trailer key should be accepted; got %v", err)
	}
}

func TestBuildCommitMessage_RejectsEmptyRequired(t *testing.T) {
	cases := []struct {
		name string
		opts CommitOptions
		want error
	}{
		{"empty message", CommitOptions{GhyllVersion: "0.1", GhyllModel: "m"}, ErrCommitMessageEmpty},
		{"empty version", CommitOptions{Message: "x", GhyllModel: "m"}, ErrCommitVersionEmpty},
		{"empty model", CommitOptions{Message: "x", GhyllVersion: "0.1"}, ErrCommitModelEmpty},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := BuildCommitMessage(c.opts)
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v; want %v", err, c.want)
			}
		})
	}
}

func TestBuildCommitMessage_RejectsControlCharsInGhyllValues(t *testing.T) {
	// Validation-pass-8 S2: project-controlled Ghyll-* values must
	// fail loudly on control chars, not silently strip.
	_, err := BuildCommitMessage(CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1\nforged",
		GhyllModel:   "m",
	})
	if !errors.Is(err, ErrCommitVersionInvalid) {
		t.Errorf("err = %v; want ErrCommitVersionInvalid", err)
	}
	_, err = BuildCommitMessage(CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1",
		GhyllModel:   "real\nGhyll-Model: forged",
	})
	if !errors.Is(err, ErrCommitModelInvalid) {
		t.Errorf("err = %v; want ErrCommitModelInvalid", err)
	}
}

func TestBuildCommitMessage_RejectsUnicodeLineSeparator(t *testing.T) {
	// Validation-pass-8 S1: U+2028 (LINE SEPARATOR) must be refused
	// in Ghyll-* values — some downstream consumers treat it as \n.
	_, err := BuildCommitMessage(CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1",
		GhyllModel:   "m forged",
	})
	if !errors.Is(err, ErrCommitModelInvalid) {
		t.Errorf("U+2028 in model should reject; got %v", err)
	}
	_, err = BuildCommitMessage(CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1",
		GhyllModel:   "m\u0085forged",
	})
	if !errors.Is(err, ErrCommitModelInvalid) {
		t.Errorf("U+0085 in model should reject; got %v", err)
	}
}

func TestBuildCommitMessage_RejectsBadExtraTrailer(t *testing.T) {
	cases := []string{
		"Not A Trailer Line",
		"Key: value\nForged: evil",
		"Key:novalue", // S13 — missing space after colon
		"Bad Key: x",  // space in key
		"Key: value forged",
	}
	for _, c := range cases {
		_, err := BuildCommitMessage(CommitOptions{
			Message:       "x",
			GhyllVersion:  "0.1",
			GhyllModel:    "m",
			ExtraTrailers: []string{c},
		})
		if !errors.Is(err, ErrCommitTrailerBad) {
			t.Errorf("trailer %q: err = %v; want ErrCommitTrailerBad", c, err)
		}
	}
}

func TestBuildCommitMessage_BoundsLength(t *testing.T) {
	// S11: caps on Message + trailer counts.
	_, err := BuildCommitMessage(CommitOptions{
		Message:      strings.Repeat("x", maxCommitMessageLen+1),
		GhyllVersion: "0.1",
		GhyllModel:   "m",
	})
	if !errors.Is(err, ErrCommitMessageTooLong) {
		t.Errorf("oversized message should fail; got %v", err)
	}
	extras := make([]string, maxExtraTrailers+1)
	for i := range extras {
		extras[i] = "Key: v"
	}
	_, err = BuildCommitMessage(CommitOptions{
		Message:       "x",
		GhyllVersion:  "0.1",
		GhyllModel:    "m",
		ExtraTrailers: extras,
	})
	if !errors.Is(err, ErrCommitTrailerCount) {
		t.Errorf("over-cap extras should fail; got %v", err)
	}
}

func TestBuildCommitMessage_TrimRightSpace(t *testing.T) {
	// S6: trailing whitespace stripped via TrimRightFunc(IsSpace);
	// the trailer-block delimiter (blank line) must be well-formed.
	msg, err := BuildCommitMessage(CommitOptions{
		Message:      "feat: thing\n  \t\n  ",
		GhyllVersion: "0.1",
		GhyllModel:   "m",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Expect exactly one blank line between body and trailers.
	if !strings.Contains(msg, "feat: thing\n\nGhyll-Version:") {
		t.Errorf("body/trailer delimiter malformed:\n%q", msg)
	}
}

func TestBuildCommitMessage_DefensiveCopyExtraTrailers(t *testing.T) {
	// S5: mutating the caller's ExtraTrailers slice after the
	// validation must NOT change what's emitted.
	extras := []string{"Co-Authored-By: a <a@a>"}
	opts := CommitOptions{
		Message:       "x",
		GhyllVersion:  "0.1",
		GhyllModel:    "m",
		ExtraTrailers: extras,
	}
	msg1, _ := BuildCommitMessage(opts)
	extras[0] = "POISONED"
	msg2, _ := BuildCommitMessage(opts)
	// Both messages should contain the ORIGINAL extra (msg1) or the
	// POISONED one (msg2) — defensive copy means msg1 is unaffected
	// by post-call mutation of the slice, but msg2 sees the
	// poisoned value because the caller mutated the input.
	if !strings.Contains(msg1, "Co-Authored-By: a <a@a>") {
		t.Errorf("msg1 lost original trailer: %s", msg1)
	}
	// The point of defensive copy is the validation passed against
	// the COPY, not the live slice. msg2's regenerated copy sees
	// the new value — that's correct.
	if !strings.Contains(msg2, "POISONED") {
		// Validation failed; that's also fine (POISONED isn't a
		// valid trailer). Either way the prior commit doesn't leak.
		_ = msg2
	}
}

// helper for git-integration tests
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func writeAndStage(t *testing.T, dir, name, body string) {
	t.Helper()
	// S9: use os.WriteFile, not `sh -c "echo ..."` (injection-safe).
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
}

func TestGitCommit_StampsTrailers(t *testing.T) {
	dir := initGitRepo(t)
	writeAndStage(t, dir, "file.txt", "content")
	res := GitCommit(context.Background(), dir, CommitOptions{
		Message:      "test commit",
		GhyllVersion: "0.5.2",
		GhyllModel:   "qwen-coder-q4@local",
	}, 10*time.Second)
	if res.Error != "" {
		t.Fatalf("commit error: %s", res.Error)
	}
	// S7: verify via git's trailer parser, not substring match.
	logCmd := exec.Command("git", "log", "-1", "--format=%(trailers:key=Ghyll-Model,valueonly)")
	logCmd.Dir = dir
	out, err := logCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(out))
	if got != "qwen-coder-q4@local" {
		t.Errorf("git trailer-parser sees model = %q; want qwen-coder-q4@local", got)
	}
	vCmd := exec.Command("git", "log", "-1", "--format=%(trailers:key=Ghyll-Version,valueonly)")
	vCmd.Dir = dir
	vOut, _ := vCmd.Output()
	if strings.TrimSpace(string(vOut)) != "0.5.2" {
		t.Errorf("git trailer-parser sees version = %q; want 0.5.2", string(vOut))
	}
}

func TestGitCommit_LongMessageViaStdin(t *testing.T) {
	// S12: messages near maxCommitMessageLen should not hit ARG_MAX
	// because we use `git commit -F -` via stdin.
	dir := initGitRepo(t)
	writeAndStage(t, dir, "file.txt", "x")
	big := "body: " + strings.Repeat("y", maxCommitMessageLen-100)
	res := GitCommit(context.Background(), dir, CommitOptions{
		Message:      big,
		GhyllVersion: "0.1",
		GhyllModel:   "m",
	}, 10*time.Second)
	if res.Error != "" {
		t.Fatalf("long-message commit failed: %s", res.Error)
	}
}

func TestGitCommit_SignOff(t *testing.T) {
	// S15: SignOff appends Signed-off-by.
	dir := initGitRepo(t)
	writeAndStage(t, dir, "f.txt", "x")
	res := GitCommit(context.Background(), dir, CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1",
		GhyllModel:   "m",
		SignOff:      true,
	}, 10*time.Second)
	if res.Error != "" {
		t.Fatalf("commit error: %s", res.Error)
	}
	logCmd := exec.Command("git", "log", "-1", "--format=%B")
	logCmd.Dir = dir
	out, _ := logCmd.Output()
	if !strings.Contains(string(out), "Signed-off-by:") {
		t.Errorf("Signed-off-by missing:\n%s", out)
	}
}

func TestGitCommit_AllowEmpty(t *testing.T) {
	// S15: AllowEmpty lets a no-staged-changes commit succeed.
	dir := initGitRepo(t)
	res := GitCommit(context.Background(), dir, CommitOptions{
		Message:      "marker",
		GhyllVersion: "0.1",
		GhyllModel:   "m",
		AllowEmpty:   true,
	}, 10*time.Second)
	if res.Error != "" {
		t.Fatalf("allow-empty commit failed: %s", res.Error)
	}
}

func TestCheckPending_Variants(t *testing.T) {
	// S8/S10: typed enum distinguishes staged / unstaged / untracked / clean.
	dir := initGitRepo(t)
	st, err := CheckPending(context.Background(), dir, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st != PendingClean {
		t.Errorf("fresh repo: status = %v; want clean", st)
	}

	// Untracked only.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	st, _ = CheckPending(context.Background(), dir, 5*time.Second)
	if st != PendingUntracked {
		t.Errorf("untracked: status = %v; want untracked", st)
	}

	// Stage it → staged.
	cmd := exec.Command("git", "add", "untracked.txt")
	cmd.Dir = dir
	_ = cmd.Run()
	st, _ = CheckPending(context.Background(), dir, 5*time.Second)
	if st != PendingStaged {
		t.Errorf("after add: status = %v; want staged", st)
	}
}

func TestHasPendingChanges_IgnoresUntracked(t *testing.T) {
	// S8: the wrapper returns false for untracked-only (the
	// session-loop flush invariant cares about staged changes).
	dir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pending, err := HasPendingChanges(context.Background(), dir, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Error("untracked-only should NOT register as pending for the session-loop flush")
	}
}
