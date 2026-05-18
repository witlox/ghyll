package runner

import (
	"context"
	"testing"
)

func TestCardinalityCheck_ExactMatch(t *testing.T) {
	// Scenario: integrator G4 — "no value outside enum" → expected
	// count is 0.
	dir := t.TempDir()
	writeFile(t, dir, "findings.md", `
- type: local-bug
- type: missing-cross-context-spec
`)
	res, err := EvaluateCardinalityCheck(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"query":        `type: unauthorized-type`,
			"query-target": "findings.md",
			"expected":     0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pass {
		t.Errorf("expected pass (0 matches); got %+v", res.Details)
	}
	if res.Details["actual"] != 0 {
		t.Errorf("actual = %v; want 0", res.Details["actual"])
	}
}

func TestCardinalityCheck_FailsOnMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "findings.md", `
- type: local-bug
- type: not-in-enum
`)
	res, _ := EvaluateCardinalityCheck(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"query":        `type: not-in-enum`,
			"query-target": "findings.md",
			"expected":     0,
		},
	})
	if res.Pass {
		t.Errorf("expected fail (1 match found); got %+v", res.Details)
	}
}

func TestCardinalityCheck_RangeMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "data.md", "a\nb\nc\nd\ne\n")
	res, _ := EvaluateCardinalityCheck(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"query":        `^[a-z]$`,
			"query-target": "data.md",
			"expected":     []any{3, 7}, // 5 in [3, 7] → pass
		},
	})
	if !res.Pass {
		t.Errorf("expected pass (5 in [3,7]); got %+v", res.Details)
	}
}

func TestCardinalityCheck_RangeOutOfBounds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "data.md", "a\nb\n")
	res, _ := EvaluateCardinalityCheck(context.Background(), Clause{
		ProjectDir: dir,
		Args: map[string]any{
			"query":        `^[a-z]$`,
			"query-target": "data.md",
			"expected":     []any{5, 10}, // 2 < 5
		},
	})
	if res.Pass {
		t.Error("expected fail (2 outside [5,10])")
	}
}

func TestCardinalityCheck_ProjectStateNotSupported(t *testing.T) {
	_, err := EvaluateCardinalityCheck(context.Background(), Clause{
		Args: map[string]any{
			"query":        "anything",
			"query-target": "project-state",
			"expected":     0,
		},
	})
	if err == nil {
		t.Error("project-state target should error (v1 supports path-glob only)")
	}
}

func TestCardinalityCheck_InvalidRegex(t *testing.T) {
	_, err := EvaluateCardinalityCheck(context.Background(), Clause{
		Args: map[string]any{
			"query":        "[unclosed",
			"query-target": "**",
			"expected":     0,
		},
	})
	if err == nil {
		t.Error("invalid regex should error")
	}
}

func TestCardinalityCheck_MissingExpected(t *testing.T) {
	_, err := EvaluateCardinalityCheck(context.Background(), Clause{
		Args: map[string]any{
			"query":        "x",
			"query-target": "**",
		},
	})
	if err == nil {
		t.Error("missing expected arg should error")
	}
}

func TestMatchesExpected(t *testing.T) {
	cases := []struct {
		actual   int
		expected any
		want     bool
	}{
		{5, 5, true},
		{5, 4, false},
		{5, []any{3, 7}, true},
		{5, []any{6, 10}, false},
		{5, []any{0, 5}, true},  // inclusive upper
		{5, []any{5, 10}, true}, // inclusive lower
	}
	for _, c := range cases {
		ok, _, err := matchesExpected(c.actual, c.expected)
		if err != nil {
			t.Errorf("err on (%v, %v): %v", c.actual, c.expected, err)
			continue
		}
		if ok != c.want {
			t.Errorf("matchesExpected(%v, %v) = %v; want %v", c.actual, c.expected, ok, c.want)
		}
	}
	// Inverted range errors.
	if _, _, err := matchesExpected(3, []any{10, 5}); err == nil {
		t.Error("inverted range should error")
	}
}
