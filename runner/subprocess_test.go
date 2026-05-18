package runner

import (
	"context"
	"fmt"
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
	// observes signal 9 and reports ReasonKilledBySignal.
	eval := NewBindingEvaluator(`kill -KILL $$`, WithTimeout(2*time.Second))
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass {
		t.Errorf("Pass = true; want false (killed)")
	}
	if got := res.Details["error"]; got != ReasonKilledBySignal {
		t.Errorf("details.error = %v; want %s", got, ReasonKilledBySignal)
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

func TestBindingEvaluator_EmptyCommandReportsSpawnFailed(t *testing.T) {
	// validation-pass-3 F38: empty/whitespace-only command is
	// operator misconfiguration; surfaces as ReasonSpawnFailed
	// Result (not a runner-level error) so the EvaluationRun
	// captures the issue with attribution.
	for _, cmd := range []string{"", "   ", "\n", "\t"} {
		eval := NewBindingEvaluator(cmd)
		res, err := eval(context.Background(), Clause{Concept: "test"})
		if err != nil {
			t.Errorf("%q: unexpected runner-level error %v", cmd, err)
			continue
		}
		if res.Pass {
			t.Errorf("%q: should not pass", cmd)
		}
		if got := res.Details["error"]; got != ReasonSpawnFailed {
			t.Errorf("%q: error = %v; want %s", cmd, got, ReasonSpawnFailed)
		}
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
	if !c.overflowed() {
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
	if c.overflowed() {
		t.Error("overflow set on under-cap write")
	}
	if string(c.bytes()) != "hello" {
		t.Errorf("captured = %q", c.bytes())
	}
}

func TestBindingEvaluator_EnvAllowlistByDefault(t *testing.T) {
	// validation-pass-3 F1: secrets in parent env NOT inherited
	// unless explicitly opted-in via WithInheritEnv.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret-do-not-leak")
	t.Setenv("PATH", "/usr/local/bin:/usr/bin:/bin")
	eval := NewBindingEvaluator(`
echo "{\"pass\": true, \"details\": {\"env_seen_key\": \"${ANTHROPIC_API_KEY:-MISSING}\", \"path\": \"${PATH:-MISSING}\"}}"
`)
	res, err := eval(context.Background(), Clause{Concept: "test"})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Details["env_seen_key"]
	if got != "MISSING" {
		t.Errorf("ANTHROPIC_API_KEY leaked to binding: %v", got)
	}
	if res.Details["path"] == "MISSING" {
		t.Error("PATH should be in the default allowlist")
	}
}

func TestBindingEvaluator_WithInheritEnv(t *testing.T) {
	t.Setenv("CARGO_HOME", "/tmp/cargo-test")
	eval := NewBindingEvaluator(
		`echo "{\"pass\": true, \"details\": {\"cargo\": \"${CARGO_HOME:-MISSING}\"}}"`,
		WithInheritEnv("CARGO_HOME"),
	)
	res, _ := eval(context.Background(), Clause{Concept: "test"})
	if res.Details["cargo"] != "/tmp/cargo-test" {
		t.Errorf("WithInheritEnv didn't pass CARGO_HOME: %v", res.Details)
	}
}

func TestBindingEvaluator_StdoutCapKillsPromptly(t *testing.T) {
	// validation-pass-3 F2: stdout cap overflow must kill the
	// subprocess promptly, NOT wait for Timeout. Test: emit way
	// more than the cap and verify elapsed time is small.
	eval := NewBindingEvaluator(
		`yes "x" | head -c 1000000`, // 1MB
		WithMaxOutputBytes(1024),    // 1KB cap
		WithTimeout(30*time.Second),
	)
	start := time.Now()
	res, _ := eval(context.Background(), Clause{Concept: "test"})
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("cap-overflow took %v; should kill within ~grace", elapsed)
	}
	if res.Details["error"] != ReasonOversizedOutput {
		t.Errorf("error = %v; want %s", res.Details["error"], ReasonOversizedOutput)
	}
}

func TestBindingEvaluator_CancelledVsTimeoutDistinct(t *testing.T) {
	// validation-pass-3 F40: caller cancellation and deadline
	// expiry surface as different reasons.
	eval := NewBindingEvaluator(`sleep 5`, WithTimeout(5*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res, _ := eval(ctx, Clause{Concept: "test"})
	if res.Details["error"] != ReasonCancelled {
		t.Errorf("error = %v; want %s (caller cancel)", res.Details["error"], ReasonCancelled)
	}
}

func TestBindingEvaluator_DetailsDepthLimit(t *testing.T) {
	// validation-pass-3 F35: deeply nested details payload refused.
	// Build a JSON with 12 levels of nesting (> maxDetailsJSONDepth=8).
	deep := `{"pass": true, "details": {"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a":{"a": 1}}}}}}}}}}}`
	eval := NewBindingEvaluator(fmt.Sprintf(`echo '%s'`, deep))
	res, _ := eval(context.Background(), Clause{Concept: "test"})
	if res.Pass {
		t.Errorf("deeply-nested details should fail; got Pass=true")
	}
	if res.Details["error"] != ReasonMalformedOutput {
		t.Errorf("error = %v; want %s", res.Details["error"], ReasonMalformedOutput)
	}
}

func TestBindingEvaluator_SecretsRedactedInForensics(t *testing.T) {
	// validation-pass-3 F33: secrets in stderr redacted before
	// persistence to attestation records.
	eval := NewBindingEvaluator(`
echo "Bearer sk-ant-abcdef1234567890abcdef" >&2
echo "API_KEY=sk-leaked-secret-pattern-12345" >&2
echo "not json"
`)
	res, _ := eval(context.Background(), Clause{Concept: "test"})
	stderr, _ := res.Details["stderr"].(string)
	if strings.Contains(stderr, "sk-ant-abcdef") {
		t.Errorf("Bearer token not redacted: %q", stderr)
	}
	if strings.Contains(stderr, "sk-leaked-secret") {
		t.Errorf("API_KEY value not redacted: %q", stderr)
	}
	if !strings.Contains(stderr, "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker; got %q", stderr)
	}
}

func TestKillProcessGroup_GraceClampedFromZero(t *testing.T) {
	// validation-pass-3 F34: a direct-struct &BindingEvaluator{Grace:0}
	// must not skip SIGTERM. ensureDefaults fills grace; if a future
	// caller bypasses defaults entirely, killProcessGroup's clamp
	// still applies.
	b := &BindingEvaluator{Command: "true", Grace: 0}
	b.ensureDefaults()
	if b.Grace < minBindingGrace {
		t.Errorf("Grace = %v; want >= %v after ensureDefaults", b.Grace, minBindingGrace)
	}
}

func TestRedactSecrets_Patterns(t *testing.T) {
	cases := map[string]string{
		"Bearer abc-123-xyz":                "[REDACTED]",
		"API_KEY=hush":                      "[REDACTED]",
		"GHYLL_TOKEN=sneaky":                "[REDACTED]",
		"sk-ant-1234567890abcdef1234567890": "[REDACTED]",
	}
	for in, marker := range cases {
		out := redactSecrets(in)
		if !strings.Contains(out, marker) {
			t.Errorf("redactSecrets(%q) = %q; want [REDACTED]", in, out)
		}
	}
	// Negative: doesn't redact innocuous strings.
	if redactSecrets("normal text") != "normal text" {
		t.Errorf("over-redacted innocuous text")
	}
}
