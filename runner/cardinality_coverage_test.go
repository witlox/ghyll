package runner

import (
	"strings"
	"testing"
)

// Tier 3 coverage push — matchesExpected branches.

func TestScenario_matchesExpected_ExactInt(t *testing.T) {
	ok, desc, err := matchesExpected(5, 5)
	if err != nil || !ok {
		t.Errorf("(5,5) = (%v, %q, %v)", ok, desc, err)
	}
	if !strings.Contains(desc, "exactly 5") {
		t.Errorf("desc = %q", desc)
	}
}

func TestScenario_matchesExpected_ExactIntMismatch(t *testing.T) {
	ok, _, err := matchesExpected(3, 5)
	if err != nil || ok {
		t.Errorf("(3,5) = (%v, %v); want (false, nil)", ok, err)
	}
}

func TestScenario_matchesExpected_RangeAny(t *testing.T) {
	ok, desc, err := matchesExpected(3, []any{1, 5})
	if err != nil || !ok {
		t.Errorf("3 in [1,5] = (%v, %q, %v)", ok, desc, err)
	}
}

func TestScenario_matchesExpected_RangeAnyOutOfBounds(t *testing.T) {
	ok, _, err := matchesExpected(10, []any{1, 5})
	if err != nil || ok {
		t.Errorf("10 in [1,5] = (%v, %v); want (false, nil)", ok, err)
	}
}

func TestScenario_matchesExpected_RangeWrongLen(t *testing.T) {
	_, _, err := matchesExpected(3, []any{1, 5, 10})
	if err == nil {
		t.Error("3-element range: want error")
	}
}

func TestScenario_matchesExpected_RangeInverted(t *testing.T) {
	_, _, err := matchesExpected(3, []any{10, 1})
	if err == nil {
		t.Error("inverted range: want error")
	}
}

func TestScenario_matchesExpected_RangeIntsForm(t *testing.T) {
	ok, desc, err := matchesExpected(3, []int{1, 5})
	if err != nil || !ok {
		t.Errorf("[]int form = (%v, %q, %v)", ok, desc, err)
	}
}

func TestScenario_matchesExpected_RangeBadElement(t *testing.T) {
	_, _, err := matchesExpected(3, []any{"oops", 5})
	if err == nil {
		t.Error("non-numeric range min: want error")
	}
}
