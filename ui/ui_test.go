package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// withCaptured swaps stdout/stderr for the duration of fn and restores
// the originals when it returns.
func withCaptured(t *testing.T, fn func(out, err *bytes.Buffer)) {
	t.Helper()
	var out, err bytes.Buffer
	prevOut, prevErr := stdout, stderr
	SetOutput(&out, &err)
	defer SetOutput(prevOut, prevErr)
	fn(&out, &err)
}

func TestScenario_Info_WritesNewlineToStdout(t *testing.T) {
	withCaptured(t, func(out, errBuf *bytes.Buffer) {
		Info("hello %s", "world")
		if got := out.String(); got != "hello world\n" {
			t.Fatalf("Info wrote %q; want %q", got, "hello world\n")
		}
		if errBuf.Len() != 0 {
			t.Fatalf("Info should not touch stderr; got %q", errBuf.String())
		}
	})
}

func TestScenario_Status_PrefixesSymbol(t *testing.T) {
	withCaptured(t, func(out, _ *bytes.Buffer) {
		Status("ℹ", "device: %s", "alice")
		if got := out.String(); got != "ℹ device: alice\n" {
			t.Fatalf("Status wrote %q; want %q", got, "ℹ device: alice\n")
		}
	})
}

func TestScenario_Print_NoTrailingNewline(t *testing.T) {
	withCaptured(t, func(out, _ *bytes.Buffer) {
		Print("ghyll> ")
		if got := out.String(); got != "ghyll> " {
			t.Fatalf("Print wrote %q; want %q", got, "ghyll> ")
		}
	})
}

func TestScenario_Errorf_PrefixesGhyllAndUsesStderr(t *testing.T) {
	withCaptured(t, func(out, errBuf *bytes.Buffer) {
		Errorf("open store: %v", os.ErrPermission)
		if errBuf.Len() == 0 {
			t.Fatal("Errorf wrote nothing to stderr")
		}
		got := errBuf.String()
		if !strings.HasPrefix(got, "ghyll: open store: ") {
			t.Fatalf("Errorf missing prefix; got %q", got)
		}
		if !strings.HasSuffix(got, "\n") {
			t.Fatalf("Errorf missing trailing newline; got %q", got)
		}
		if out.Len() != 0 {
			t.Fatalf("Errorf should not touch stdout; got %q", out.String())
		}
	})
}

func TestScenario_Usage_OneLinePerEntry_NoPrefix(t *testing.T) {
	withCaptured(t, func(_, errBuf *bytes.Buffer) {
		Usage(
			"usage: ghyll run [dir]",
			"       ghyll memory log",
		)
		got := errBuf.String()
		want := "usage: ghyll run [dir]\n       ghyll memory log\n"
		if got != want {
			t.Fatalf("Usage wrote %q; want %q", got, want)
		}
	})
}

func TestScenario_StdoutStderr_ReturnCurrentWriters(t *testing.T) {
	prevOut, prevErr := stdout, stderr
	defer SetOutput(prevOut, prevErr)
	var out, errBuf bytes.Buffer
	SetOutput(&out, &errBuf)
	if Stdout() != &out {
		t.Fatal("Stdout did not return the override")
	}
	if Stderr() != &errBuf {
		t.Fatal("Stderr did not return the override")
	}
}
