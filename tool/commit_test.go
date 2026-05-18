package tool

import (
	"context"
	"errors"
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
	// Body precedes trailers separated by blank line.
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

func TestBuildCommitMessage_RejectsBadExtraTrailer(t *testing.T) {
	_, err := BuildCommitMessage(CommitOptions{
		Message:       "x",
		GhyllVersion:  "0.1",
		GhyllModel:    "m",
		ExtraTrailers: []string{"Not A Trailer Line"},
	})
	if !errors.Is(err, ErrCommitTrailerBad) {
		t.Errorf("err = %v; want ErrCommitTrailerBad", err)
	}
}

func TestBuildCommitMessage_RejectsEmbeddedNewlineInExtraTrailer(t *testing.T) {
	_, err := BuildCommitMessage(CommitOptions{
		Message:       "x",
		GhyllVersion:  "0.1",
		GhyllModel:    "m",
		ExtraTrailers: []string{"Key: value\nForged-Trailer: evil"},
	})
	if !errors.Is(err, ErrCommitTrailerBad) {
		t.Errorf("err = %v; want ErrCommitTrailerBad", err)
	}
}

func TestBuildCommitMessage_SanitizesEmbeddedControlCharsInValues(t *testing.T) {
	// A hostile model identifier with embedded newlines must not
	// forge a multi-line trailer block. git's trailer parser splits
	// on lines; we defeat the forgery by stripping newlines so the
	// value collapses to one line — even though the literal substring
	// survives as text within the value.
	msg, err := BuildCommitMessage(CommitOptions{
		Message:      "x",
		GhyllVersion: "0.1",
		GhyllModel:   "real\nGhyll-Version: 9.9.9\nGhyll-Model: forged",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Structural check: exactly one TRAILER LINE per key (line
	// starts with `Ghyll-Version:` or `Ghyll-Model:`).
	gotVersionLines := 0
	gotModelLines := 0
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, "Ghyll-Version:") {
			gotVersionLines++
		}
		if strings.HasPrefix(line, "Ghyll-Model:") {
			gotModelLines++
		}
	}
	if gotVersionLines != 1 {
		t.Errorf("forged trailer leaked: %d Ghyll-Version trailer lines; want 1\n%s", gotVersionLines, msg)
	}
	if gotModelLines != 1 {
		t.Errorf("forged trailer leaked: %d Ghyll-Model trailer lines; want 1\n%s", gotModelLines, msg)
	}
	// Defense: ensure no raw newline survived inside the value.
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, "Ghyll-Model:") && strings.Contains(line, "\n") {
			t.Error("raw newline survived in Ghyll-Model value")
		}
	}
}

// initGitRepo creates a throwaway repo for integration-style tests of
// GitCommit / HasPendingChanges. Skips on machines without git.
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
	path := filepath.Join(dir, name)
	if err := exec.Command("sh", "-c", "echo "+body+" > "+path).Run(); err != nil {
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
	// Verify trailer landed in git log.
	logCmd := exec.Command("git", "log", "-1", "--format=%B")
	logCmd.Dir = dir
	out, err := logCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if !strings.Contains(body, "Ghyll-Version: 0.5.2") {
		t.Errorf("trailer missing in commit message:\n%s", body)
	}
}

func TestHasPendingChanges(t *testing.T) {
	dir := initGitRepo(t)
	pending, err := HasPendingChanges(context.Background(), dir, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Error("fresh repo should have no pending changes")
	}
	writeAndStage(t, dir, "file.txt", "content")
	pending, _ = HasPendingChanges(context.Background(), dir, 5*time.Second)
	if !pending {
		t.Error("staged file should register as pending")
	}
}
