package runner

import (
	"errors"
	"testing"
)

// Tier 3 coverage push — ClassificationsStore.Forget +
// ForgetArrow + small helper paths.

func TestScenario_ClassificationsStore_Forget_RemovesRequirement(t *testing.T) {
	s := NewClassificationsStore()
	if err := s.DeclareRequirement("A", Requirement{
		ID: "R1", MinDepth: DepthRankShallow, Description: "shallow scan",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("A", "R1"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if reqs := s.RequirementsForArrow("A"); len(reqs) != 0 {
		t.Errorf("requirements after Forget: %d; want 0", len(reqs))
	}
}

func TestScenario_ClassificationsStore_Forget_UnknownErrors(t *testing.T) {
	s := NewClassificationsStore()
	err := s.Forget("A", "missing")
	if !errors.Is(err, ErrRequirementUnknown) {
		t.Errorf("err = %v; want ErrRequirementUnknown", err)
	}
}

func TestScenario_ClassificationsStore_ForgetArrow_ReturnsCount(t *testing.T) {
	s := NewClassificationsStore()
	for _, id := range []string{"R1", "R2", "R3"} {
		_ = s.DeclareRequirement("A", Requirement{
			ID: id, MinDepth: DepthRankShallow, Description: "x",
		})
	}
	if n := s.ForgetArrow("A"); n != 3 {
		t.Errorf("ForgetArrow = %d; want 3", n)
	}
	// Idempotent: ForgetArrow on empty arrow returns 0.
	if n := s.ForgetArrow("A"); n != 0 {
		t.Errorf("second ForgetArrow = %d; want 0", n)
	}
}

func TestScenario_ClassificationsStore_ForgetArrow_TrimsWhitespace(t *testing.T) {
	s := NewClassificationsStore()
	_ = s.DeclareRequirement("A", Requirement{
		ID: "R1", MinDepth: DepthRankShallow, Description: "x",
	})
	// Whitespace-padded arrow id normalizes.
	if n := s.ForgetArrow("  A  "); n != 1 {
		t.Errorf("ForgetArrow(\"  A  \") = %d; want 1", n)
	}
}
