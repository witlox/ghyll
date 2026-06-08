package main

import (
	"strings"
	"testing"
)

// TestCapToolResult_DefaultPreservesSmall — outputs under the
// default cap pass through unchanged. No marker appended.
func TestCapToolResult_DefaultPreservesSmall(t *testing.T) {
	in := "hello world\n"
	got := capToolResult(in, 0)
	if got != in {
		t.Errorf("small content should pass through unchanged, got %q", got)
	}
}

// TestCapToolResult_TruncatesAtDefault — output above the default
// (8 KiB) is cut, marker appended, original size visible.
func TestCapToolResult_TruncatesAtDefault(t *testing.T) {
	in := strings.Repeat("a", 20000)
	got := capToolResult(in, 0)
	if !strings.HasPrefix(got, strings.Repeat("a", 8192)) {
		t.Errorf("truncation should keep first 8192 bytes verbatim")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("marker missing, got tail: %q", got[len(got)-100:])
	}
	if !strings.Contains(got, "20000 bytes") {
		t.Errorf("marker should include original size, got tail: %q", got[len(got)-200:])
	}
}

// TestCapToolResult_CustomCap — explicit positive cap is honored.
func TestCapToolResult_CustomCap(t *testing.T) {
	in := strings.Repeat("x", 1000)
	got := capToolResult(in, 100)
	if !strings.HasPrefix(got, strings.Repeat("x", 100)) {
		t.Errorf("should keep first 100 bytes")
	}
	if !strings.Contains(got, "kept 100 of 1000 bytes") {
		t.Errorf("marker should reflect custom cap, got tail: %q", got[len(got)-200:])
	}
}

// TestCapToolResult_DisabledWithMinusOne — operator opt-out.
// Full content rides through to context. For the rare case where
// the model genuinely needs the whole result and the operator
// accepts the gateway-413 risk.
func TestCapToolResult_DisabledWithMinusOne(t *testing.T) {
	in := strings.Repeat("y", 100000)
	got := capToolResult(in, -1)
	if got != in {
		t.Errorf("-1 should disable truncation, got %d bytes (want %d)", len(got), len(in))
	}
	if strings.Contains(got, "truncated") {
		t.Errorf("-1 should NOT append the marker")
	}
}

// TestCapToolResult_NegativeOtherDefaults — operators reaching for
// a custom value typed -1 by intent; -50 is a typo. Treat all
// negatives-other-than-minus-one as default to fail-safe.
func TestCapToolResult_NegativeOtherDefaults(t *testing.T) {
	in := strings.Repeat("z", 20000)
	got := capToolResult(in, -50)
	if len(got) == len(in) {
		t.Errorf("negative-other-than-minus-one should NOT disable truncation")
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("default truncation should still apply")
	}
}

// TestCapToolResult_EqualToCapNotTruncated — boundary: len == cap
// is exactly at the threshold, no truncation needed.
func TestCapToolResult_EqualToCapNotTruncated(t *testing.T) {
	in := strings.Repeat("=", 100)
	got := capToolResult(in, 100)
	if got != in {
		t.Errorf("len == cap should pass through, got %d bytes", len(got))
	}
}
