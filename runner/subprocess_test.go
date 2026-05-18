package runner

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBindingEvaluator_SuccessfulRun(t *testing.T) {
	// A binding that echoes a valid {pass, details} JSON.
	eval := NewBindingEvaluator(`cat <<'EOF'
{"pass": true, "details": {"scanned-files": ["src/foo.go"]}}
EOF
`)
	res, err := eval(context.Background(), Clause{
		Concept: "test", Args: map[string]any{"scope": "src/**"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !res.Pass {
		t.Errorf("Pass = false; want true")
	}
	files, ok := res.Details["scanned-files"].([]any)
	if !ok || len(files) != 1 || files[0] != "src/foo.go" {
		t.Errorf("scanned-files = %v", res.Details["scanned-files"])
	}
}

func TestBindingEvaluator_FailRun(t *testing.T) {
	eval := NewBindingEvaluator(`cat <<'EOF'
{"pass": false, "details": {"hits": [{"file": "src/foo.go", "line": 42, "marker": "TODO"}]}}
EOF
exit 1
`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false")
	}
	hits, _ := res.Details["hits"].([]any)
	if len(hits) != 1 {
		t.Errorf("hits = %v", res.Details["hits"])
	}
}

func TestBindingEvaluator_Timeout(t *testing.T) {
	// Sleep longer than the timeout. The runner SIGTERMs after the
	// deadline, SIGKILLs after grace.
	eval := NewBindingEvaluator(`sleep 5`, WithTimeout(200*time.Millisecond), WithGrace(50*time.Millisecond))
	start := time.Now()
	res, err := eval(context.Background(), Clause{Concept: "test"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (timeout)")
	}
	if got := res.Details["error"]; got != ReasonTimeout {
		t.Errorf("details.error = %v; want %s", got, ReasonTimeout)
	}
	// Should have killed within timeout + grace + a bit of margin.
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v; expected timeout kill to be prompt", elapsed)
	}
}

func TestBindingEvaluator_MalformedJSON(t *testing.T) {
	eval := NewBindingEvaluator(`echo "this is not json"`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (malformed)")
	}
	if got := res.Details["error"]; got != ReasonMalformedOutput {
		t.Errorf("details.error = %v; want %s", got, ReasonMalformedOutput)
	}
	// Raw stdout is preserved for forensics.
	if got := res.Details["stdout"]; got == nil {
		t.Error("details.stdout should be set on malformed output")
	}
}

func TestBindingEvaluator_EmptyStdout(t *testing.T) {
	eval := NewBindingEvaluator(`true`) // exits 0, no stdout
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (empty stdout)")
	}
	if got := res.Details["error"]; got != ReasonMalformedOutput {
		t.Errorf("error = %v; want %s", got, ReasonMalformedOutput)
	}
}

func TestBindingEvaluator_OversizedStdout(t *testing.T) {
	// Emit > MaxOutputBytes; runner caps the read and fails fast.
	eval := NewBindingEvaluator(
		`yes "x" | head -c 200000`, // ~200KB
		WithMaxOutputBytes(1024),   // 1KB cap
		WithTimeout(5*time.Second),
	)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (oversized)")
	}
	if got := res.Details["error"]; got != ReasonOversizedOutput {
		t.Errorf("details.error = %v; want %s", got, ReasonOversizedOutput)
	}
	if got := res.Details["stdout-oversize"]; got != true {
		t.Errorf("stdout-oversize flag missing")
	}
}

func TestBindingEvaluator_StderrAsMetadata(t *testing.T) {
	// Spurious stderr alongside valid JSON stdout — stderr captured
	// as metadata, not as failure signal.
	eval := NewBindingEvaluator(`
echo "warning: deprecated flag --foo" >&2
echo '{"pass": true, "details": {"scanned": 5}}'
`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("Pass = false; want true (stderr is metadata, not failure)")
	}
	stderr, _ := res.Details["stderr"].(string)
	if !strings.Contains(stderr, "deprecated flag") {
		t.Errorf("stderr metadata missing: %v", res.Details["stderr"])
	}
}

func TestBindingEvaluator_StdinReceivesClauseInput(t *testing.T) {
	// Echo stdin as the details payload so we can verify it.
	eval := NewBindingEvaluator(`
input=$(cat)
echo "{\"pass\": true, \"details\": {\"echo\": ${input}}}"
`)
	res, err := eval(context.Background(), Clause{
		Concept: "test",
		Args:    map[string]any{"scope": "src/**", "threshold": 0.85},
	})
	if err != nil {
		t.Fatal(err)
	}
	echo, _ := res.Details["echo"].(map[string]any)
	if echo == nil {
		t.Fatalf("echo missing: %v", res.Details)
	}
	args, _ := echo["args"].(map[string]any)
	if args["scope"] != "src/**" {
		t.Errorf("stdin didn't carry args.scope: %v", args)
	}
}

func TestBindingEvaluator_SpawnFailedNoSuchCommand(t *testing.T) {
	// `sh -c` itself runs; the inner command is not found. sh exits
	// 127, no stdout — that's a malformed-output failure (the
	// binding produced nothing parseable).
	eval := NewBindingEvaluator(`no-such-command-12345-xyz`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false")
	}
	// Could be malformed-output (empty stdout) — verify we got SOME
	// failure marker.
	if got := res.Details["error"]; got == nil {
		t.Errorf("expected details.error to be set; got %v", res.Details)
	}
}

func TestBindingEvaluator_OOMKilled(t *testing.T) {
	// Simulate OOM by sending SIGKILL to ourselves. The runner
	// observes signal 9 and reports ReasonOOMKilled.
	eval := NewBindingEvaluator(`kill -KILL $$`, WithTimeout(2*time.Second))
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (killed)")
	}
	if got := res.Details["error"]; got != ReasonOOMKilled {
		t.Errorf("details.error = %v; want %s", got, ReasonOOMKilled)
	}
}

func TestBindingEvaluator_CallerCancellation(t *testing.T) {
	// Caller cancels via ctx. Subprocess receives SIGTERM, then
	// SIGKILL. We report as a timeout-class failure.
	eval := NewBindingEvaluator(`sleep 5`, WithTimeout(5*time.Second), WithGrace(50*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	res, err := eval(ctx, Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (cancelled)")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("elapsed = %v; cancellation should be prompt", elapsed)
	}
}

func TestBindingEvaluator_EmptyCommandRejected(t *testing.T) {
	eval := NewBindingEvaluator("")
	_, err := eval(context.Background(), Clause{Concept: "test"})
	if err == nil {
		t.Error("empty command should error")
	}
}

func TestBindingEvaluator_NonZeroExitWithValidJSON(t *testing.T) {
	// A binding that exits non-zero AND emits valid pass=false
	// JSON. The runner trusts the JSON (pass=false), exit code
	// alone doesn't override.
	eval := NewBindingEvaluator(`
echo '{"pass": false, "details": {"hits": 3}}'
exit 1
`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (binding said so)")
	}
	if got := res.Details["hits"]; got != float64(3) {
		t.Errorf("hits = %v; want 3", got)
	}
}

func TestCaptureBuf_BoundedAndOverflows(t *testing.T) {
	c := &captureBuf{max: 5}
	n, err := c.Write([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("Write returned %d; want 11 (full slice 'accepted')", n)
	}
	if !c.overflow {
		t.Error("overflow flag should be set")
	}
	if string(c.bytes()) != "hello" {
		t.Errorf("captured = %q; want 'hello'", c.bytes())
	}
	// Subsequent writes accepted (no error) but don't grow buffer.
	_, _ = c.Write([]byte("more"))
	if string(c.bytes()) != "hello" {
		t.Errorf("buffer grew after overflow: %q", c.bytes())
	}
}

func TestCaptureBuf_UnderMaxRetainsAll(t *testing.T) {
	c := &captureBuf{max: 100}
	_, _ = c.Write([]byte("hello"))
	if c.overflow {
		t.Error("overflow set on under-cap write")
	}
	if string(c.bytes()) != "hello" {
		t.Errorf("captured = %q", c.bytes())
	}
}
