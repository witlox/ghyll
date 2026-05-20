package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestScenario_NewGrid_PopulatesTier2Defaults verifies NewGrid
// fills ResidueNoteMaxBytes + ModalPendingMaxLen so a freshly-
// constructed grid has concrete numbers, not zeros.
func TestScenario_NewGrid_PopulatesTier2Defaults(t *testing.T) {
	g := NewGrid("alice")
	if g.ResidueNoteMaxBytes != DefaultResidueNoteMaxBytes {
		t.Errorf("ResidueNoteMaxBytes = %d; want %d", g.ResidueNoteMaxBytes, DefaultResidueNoteMaxBytes)
	}
	if g.ModalPendingMaxLen != DefaultModalPendingMaxLen {
		t.Errorf("ModalPendingMaxLen = %d; want %d", g.ModalPendingMaxLen, DefaultModalPendingMaxLen)
	}
}

// TestScenario_Grid_RoundTrip_PreservesTier2Fields verifies a
// non-default ResidueNoteMaxBytes / ModalPendingMaxLen survives
// Write -> Read intact.
func TestScenario_Grid_RoundTrip_PreservesTier2Fields(t *testing.T) {
	dir := t.TempDir()
	g := NewGrid("alice")
	g.ResidueNoteMaxBytes = 32 * 1024
	g.ModalPendingMaxLen = 16
	if err := g.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ResidueNoteMaxBytes != 32*1024 {
		t.Errorf("ResidueNoteMaxBytes = %d; want 32768", got.ResidueNoteMaxBytes)
	}
	if got.ModalPendingMaxLen != 16 {
		t.Errorf("ModalPendingMaxLen = %d; want 16", got.ModalPendingMaxLen)
	}
}

// TestScenario_Grid_ZeroFields_NormalizeToDefaults verifies a
// grid YAML that omits the tier-2 fields gets normalized to the
// built-in defaults at Read time (backwards-compat for pre-tier-2
// projects).
func TestScenario_Grid_ZeroFields_NormalizeToDefaults(t *testing.T) {
	// Hand-craft a grid.v1.yaml without the tier-2 keys.
	dir := t.TempDir()
	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyYAML := []byte(`grid-version: 1
created-at: 2026-01-01T00:00:00Z
created-by-op-id: alice
bounded-contexts: []
depth-ladder:
  - tier: 0
    label: NONE
  - tier: 1
    label: SHALLOW
severity-threshold: medium
insufficient-basis-rounds-max: 3
remediation-rounds-max: 5
`)
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.v1.yaml"), legacyYAML, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ghyllDir, "grid.current"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ResidueNoteMaxBytes != DefaultResidueNoteMaxBytes {
		t.Errorf("ResidueNoteMaxBytes = %d; want %d (normalized)", got.ResidueNoteMaxBytes, DefaultResidueNoteMaxBytes)
	}
	if got.ModalPendingMaxLen != DefaultModalPendingMaxLen {
		t.Errorf("ModalPendingMaxLen = %d; want %d (normalized)", got.ModalPendingMaxLen, DefaultModalPendingMaxLen)
	}
}

// TestScenario_NormalizeTier2Defaults_NilSafe verifies the
// receiver is nil-tolerant (defense-in-depth for callers that
// hold a *Grid that's been zeroed).
func TestScenario_NormalizeTier2Defaults_NilSafe(t *testing.T) {
	var g *Grid
	g.NormalizeTier2Defaults() // must not panic
}

// TestScenario_DefaultGridDefaults_IncludesTier2 verifies
// DefaultGridDefaults populates the new fields.
func TestScenario_DefaultGridDefaults_IncludesTier2(t *testing.T) {
	d := DefaultGridDefaults()
	if d.ResidueNoteMaxBytes != DefaultResidueNoteMaxBytes {
		t.Errorf("ResidueNoteMaxBytes = %d", d.ResidueNoteMaxBytes)
	}
	if d.ModalPendingMaxLen != DefaultModalPendingMaxLen {
		t.Errorf("ModalPendingMaxLen = %d", d.ModalPendingMaxLen)
	}
}

// TestScenario_GridDefaults_Validate_RejectsZeroTier2 verifies
// the validator catches a GridDefaults with zero tier-2 fields
// (which would silently disable the residue cap / backpressure).
func TestScenario_GridDefaults_Validate_RejectsZeroTier2(t *testing.T) {
	d := DefaultGridDefaults()
	d.ResidueNoteMaxBytes = 0
	if err := d.validate(); !errors.Is(err, ErrResidueNoteMaxBytesNonPositive) {
		t.Errorf("err = %v; want ErrResidueNoteMaxBytesNonPositive", err)
	}
	d = DefaultGridDefaults()
	d.ModalPendingMaxLen = 0
	if err := d.validate(); !errors.Is(err, ErrModalPendingMaxLenNonPositive) {
		t.Errorf("err = %v; want ErrModalPendingMaxLenNonPositive", err)
	}
}

// TestScenario_Grid_YAMLKey_KebabCase verifies the YAML field
// names use the project's kebab-case convention.
func TestScenario_Grid_YAMLKey_KebabCase(t *testing.T) {
	g := NewGrid("alice")
	g.ResidueNoteMaxBytes = 12345
	g.ModalPendingMaxLen = 7
	data, err := yaml.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	wire := string(data)
	for _, k := range []string{"residue-note-max-bytes: 12345", "modal-pending-max-len: 7"} {
		if !strings.Contains(wire, k) {
			t.Errorf("wire does not contain %q; wire:\n%s", k, wire)
		}
	}
}
