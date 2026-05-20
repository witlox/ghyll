package modal

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestScenario_LineReader_ReadsLinesInOrder(t *testing.T) {
	r := NewLineReader(strings.NewReader("a\nb\nc\n"))
	defer r.Close()
	for _, want := range []string{"a", "b", "c"} {
		line, err := r.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if line != want {
			t.Errorf("line = %q; want %q", line, want)
		}
	}
}

func TestScenario_LineReader_EOFAfterDrain(t *testing.T) {
	r := NewLineReader(strings.NewReader("only\n"))
	defer r.Close()
	_, _ = r.Next(context.Background())
	_, err := r.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("err = %v; want io.EOF", err)
	}
}

func TestScenario_LineReader_CtxCancelDoesNotLoseFutureLine(t *testing.T) {
	// Pipe so the reader blocks waiting on more input. Cancel,
	// then send a line, then re-read on a fresh ctx — the line
	// must still arrive (gate-2 CONC-C-2: no orphan goroutine
	// consuming on a different scanner).
	pr, pw := io.Pipe()
	r := NewLineReader(pr)
	defer r.Close()

	// First Next call cancels mid-wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("first Next err = %v; want context.Canceled", err)
	}
	// Send a line.
	go func() {
		_, _ = pw.Write([]byte("delayed\n"))
		_ = pw.Close()
	}()
	// New ctx — should receive the line.
	deadline, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	line, err := r.Next(deadline)
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if line != "delayed" {
		t.Errorf("line = %q; want delayed", line)
	}
}

func TestScenario_LineReader_CloseIsIdempotent(t *testing.T) {
	r := NewLineReader(strings.NewReader(""))
	r.Close()
	r.Close() // must not panic
}

func TestScenario_LineReader_NoGoroutineLeakAfterClose(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		r := NewLineReader(strings.NewReader("x\n"))
		_, _ = r.Next(context.Background())
		r.Close()
	}
	// Give scheduler a moment to clean up.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > before+2 { // small slack for runtime jitters
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}
