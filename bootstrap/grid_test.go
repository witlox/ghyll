package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrid_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	g := NewGrid("alice@example.com")
	g.BoundedContexts = []BoundedContext{
		{ID: "contextA", Description: "Payment processing"},
		{ID: "contextB"},
	}
	g.LanguageBindings = map[string]string{
		"lint-clean.go":    "staticcheck && go vet",
		"compiles.go":      "go build ./...",
		"mutation-score.go": "go-mutesting",
	}

	if err := g.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Both files must exist.
	if _, err := os.Stat(filepath.Join(dir, ".ghyll", "grid.v1.yaml")); err != nil {
		t.Errorf("grid.v1.yaml not on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ghyll", "grid.current")); err != nil {
		t.Errorf("grid.current not on disk: %v", err)
	}

	// Read should round-trip.
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.GridVersion != 1 {
		t.Errorf("GridVersion = %d; want 1", got.GridVersion)
	}
	if got.CreatedByOpID != "alice@example.com" {
		t.Errorf("CreatedByOpID = %q; want %q", got.CreatedByOpID, "alice@example.com")
	}
	if len(got.BoundedContexts) != 2 {
		t.Errorf("BoundedContexts count = %d; want 2", len(got.BoundedContexts))
	}
	if got.SeverityThreshold != "medium" {
		t.Errorf("SeverityThreshold = %q; want \"medium\" (harness default)", got.SeverityThreshold)
	}
	if got.InsufficientBasisRoundsMax != 3 {
		t.Errorf("InsufficientBasisRoundsMax = %d; want 3", got.InsufficientBasisRoundsMax)
	}
	if got.RemediationRoundsMax != 5 {
		t.Errorf("RemediationRoundsMax = %d; want 5", got.RemediationRoundsMax)
	}
	if len(got.DepthLadder) != 4 {
		t.Errorf("DepthLadder count = %d; want 4 (the fixed tier count)", len(got.DepthLadder))
	}
}

func TestGrid_CurrentPointerFormat(t *testing.T) {
	dir := t.TempDir()
	g := NewGrid("alice")
	g.GridVersion = 7

	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".ghyll", "grid.current"))
	if err != nil {
		t.Fatal(err)
	}
	// Single line, exactly "v7\n".
	want := "v7\n"
	if got := string(data); got != want {
		t.Errorf("grid.current content = %q; want %q", got, want)
	}
}

func TestGrid_ReadCurrent_Absent(t *testing.T) {
	dir := t.TempDir()
	// No .ghyll/ at all.
	_, err := ReadCurrent(dir)
	if !errors.Is(err, ErrGridCurrentAbsent) {
		t.Errorf("ReadCurrent without grid.current: got %v; want ErrGridCurrentAbsent", err)
	}
}

func TestGrid_ReadCurrent_Malformed(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"empty":        "",
		"whitespace":   "   \n  \n",
		"no v prefix":  "1\n",
		"bad number":   "vfoo\n",
		"negative":     "v-1\n",
		"zero":         "v0\n",
		"multi-line":   "v1\nv2\n",
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(ghyllDir, "grid.current"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ReadCurrent(dir)
			if !errors.Is(err, ErrGridCurrentMalformed) {
				t.Errorf("ReadCurrent(%s): got %v; want ErrGridCurrentMalformed", label, err)
			}
		})
	}
}

func TestGrid_Read_PointerToMissingVersion(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// grid.current names v3 but the file doesn't exist.
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.current"), []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if !errors.Is(err, ErrGridCurrentPointsToMissing) {
		t.Errorf("Read with pointer to missing: got %v; want ErrGridCurrentPointsToMissing", err)
	}
}

func TestGrid_ReadVersion_NotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ghyll"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ReadVersion(dir, 5)
	if !errors.Is(err, ErrGridCurrentPointsToMissing) {
		t.Errorf("ReadVersion(missing): got %v; want ErrGridCurrentPointsToMissing", err)
	}
}

func TestGrid_ReadVersion_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// File is named grid.v2.yaml but contains grid-version: 1.
	content := []byte("grid-version: 1\ncreated-at: 2026-01-01T00:00:00Z\ncreated-by-op-id: alice\nbounded-contexts: []\ndepth-ladder: []\nseverity-threshold: medium\ninsufficient-basis-rounds-max: 3\nremediation-rounds-max: 5\n")
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v2.yaml"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadVersion(dir, 2)
	if err == nil {
		t.Error("ReadVersion should reject filename/grid-version mismatch")
	}
	if !strings.Contains(err.Error(), "declares grid-version") {
		t.Errorf("error should mention version mismatch; got %v", err)
	}
}

func TestGrid_Write_NilGrid(t *testing.T) {
	var g *Grid
	if err := g.Write(t.TempDir()); err == nil {
		t.Error("Write on nil Grid should fail")
	}
}

func TestGrid_Write_InvalidVersion(t *testing.T) {
	dir := t.TempDir()
	g := NewGrid("alice")
	g.GridVersion = 0
	if err := g.Write(dir); err == nil {
		t.Error("Write with GridVersion=0 should fail")
	}
}

func TestGrid_Write_MultipleVersions(t *testing.T) {
	dir := t.TempDir()

	// Write v1.
	g1 := NewGrid("alice")
	if err := g1.Write(dir); err != nil {
		t.Fatal(err)
	}
	if v, _ := ReadCurrent(dir); v != 1 {
		t.Errorf("after v1 write, ReadCurrent = %d; want 1", v)
	}

	// Write v2 — both files coexist; pointer moves to v2.
	g2 := NewGrid("alice")
	g2.GridVersion = 2
	if err := g2.Write(dir); err != nil {
		t.Fatal(err)
	}
	if v, _ := ReadCurrent(dir); v != 2 {
		t.Errorf("after v2 write, ReadCurrent = %d; want 2", v)
	}
	// v1.yaml still on disk (immutable history).
	if _, err := os.Stat(filepath.Join(dir, ".ghyll", "grid.v1.yaml")); err != nil {
		t.Errorf("grid.v1.yaml should still exist after v2 write: %v", err)
	}

	// ReadVersion can fetch v1 explicitly.
	got, err := ReadVersion(dir, 1)
	if err != nil {
		t.Fatalf("ReadVersion(1) after v2 write: %v", err)
	}
	if got.GridVersion != 1 {
		t.Errorf("got.GridVersion = %d; want 1", got.GridVersion)
	}
}

func TestDefaultDepthLadder(t *testing.T) {
	ladder := DefaultDepthLadder()
	if len(ladder) != 4 {
		t.Fatalf("DefaultDepthLadder has %d tiers; want 4 (fixed per ADR-011)", len(ladder))
	}
	want := []string{"NONE", "SHALLOW", "MOCKED", "REALISTIC"}
	for i, tier := range ladder {
		if tier.Tier != i {
			t.Errorf("tier[%d].Tier = %d; want %d", i, tier.Tier, i)
		}
		if tier.Label != want[i] {
			t.Errorf("tier[%d].Label = %q; want %q", i, tier.Label, want[i])
		}
	}
}
