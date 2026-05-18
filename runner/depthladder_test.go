package runner

import (
	"strings"
	"testing"
)

func TestDepthLadder_DefaultLabels(t *testing.T) {
	l := NewDefaultDepthLadder()
	got := l.Labels()
	want := []string{"NONE", "SHALLOW", "MOCKED", "REALISTIC"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("default[%d] = %q; want %q", i, got[i], w)
		}
	}
}

func TestDepthLadder_Label(t *testing.T) {
	l := NewDefaultDepthLadder()
	if got := l.Label(DepthRankRealistic); got != "REALISTIC" {
		t.Errorf("Label(realistic) = %q; want REALISTIC", got)
	}
}

func TestDepthLadder_ParseDepthRank_CaseInsensitive(t *testing.T) {
	l := NewDefaultDepthLadder()
	cases := map[string]DepthRank{
		"NONE":      DepthRankNone,
		"none":      DepthRankNone,
		"  shallow": DepthRankShallow,
		"MOCKED":    DepthRankMocked,
		"realistic": DepthRankRealistic,
	}
	for in, want := range cases {
		got, err := l.ParseDepthRank(in)
		if err != nil {
			t.Errorf("ParseDepthRank(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDepthRank(%q) = %d; want %d", in, got, want)
		}
	}
}

func TestDepthLadder_ParseDepthRank_UnknownLabel(t *testing.T) {
	l := NewDefaultDepthLadder()
	_, err := l.ParseDepthRank("BOGUS")
	if err == nil {
		t.Error("BOGUS should not parse")
	}
}

func TestNewDepthLadder_ProjectOverride(t *testing.T) {
	l, err := NewDepthLadder([4]string{
		"absent", "defensive", "offensive", "red-teamed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.Label(DepthRankRealistic) != "red-teamed" {
		t.Errorf("override failed; got %q", l.Label(DepthRankRealistic))
	}
	// Parser must match the overridden labels.
	r, err := l.ParseDepthRank("RED-TEAMED")
	if err != nil || r != DepthRankRealistic {
		t.Errorf("parse override: r=%d err=%v", r, err)
	}
}

func TestNewDepthLadder_RejectsEmptyLabel(t *testing.T) {
	_, err := NewDepthLadder([4]string{"NONE", "", "MOCKED", "REAL"})
	if err == nil {
		t.Error("empty label should error")
	}
}

func TestNewDepthLadder_RejectsDuplicate(t *testing.T) {
	_, err := NewDepthLadder([4]string{"A", "B", "B", "C"})
	if err == nil {
		t.Error("duplicate label should error")
	}
	if !strings.Contains(err.Error(), "tiers") {
		t.Errorf("err should mention tiers; got %v", err)
	}
}

func TestNewDepthLadder_RejectsNonASCII(t *testing.T) {
	// F23: non-ASCII labels survive ToLower case-folding edges
	// (Turkish dotless-I etc.); restrict to ASCII alphanumeric.
	_, err := NewDepthLadder([4]string{"NONE", "İ-tier", "MOCKED", "REALISTIC"})
	if err == nil {
		t.Error("non-ASCII label should error")
	}
}

func TestDefaultDepthLabels_ReturnsFreshArray(t *testing.T) {
	// F36: mutating the returned array must NOT affect the
	// package's source of truth.
	a := DefaultDepthLabels()
	a[0] = "PWNED"
	b := DefaultDepthLabels()
	if b[0] == "PWNED" {
		t.Error("DefaultDepthLabels returned a shared mutable array")
	}
}

func TestDepthLadder_Label_OutOfRange(t *testing.T) {
	// F35: out-of-range rank should return a clearly invalid label,
	// not empty string.
	l := NewDefaultDepthLadder()
	got := l.Label(DepthRank(99))
	if got == "" || !strings.Contains(got, "invalid") {
		t.Errorf("Label(99) = %q; want a clearly-invalid marker", got)
	}
}

func TestRequirement_Validate(t *testing.T) {
	good := Requirement{ID: "R1", MinDepth: DepthRankShallow, Description: "checkout works end-to-end"}
	if err := good.Validate(); err != nil {
		t.Errorf("good requirement should validate: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Requirement)
	}{
		{"empty ID", func(r *Requirement) { r.ID = "" }},
		{"whitespace ID", func(r *Requirement) { r.ID = "   " }},
		{"out-of-range min", func(r *Requirement) { r.MinDepth = DepthRank(99) }},
		{"negative min", func(r *Requirement) { r.MinDepth = DepthRank(-1) }},
		{"min-NONE rejected (F12)", func(r *Requirement) { r.MinDepth = DepthRankNone }},
		{"empty description (F22)", func(r *Requirement) { r.Description = "" }},
		{"whitespace description", func(r *Requirement) { r.Description = "   " }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := good
			c.mut(&r)
			if err := r.Validate(); err == nil {
				t.Errorf("%s: should fail", c.name)
			}
		})
	}
}

func TestRequirement_Validate_DescriptionTooLong(t *testing.T) {
	r := Requirement{
		ID: "R1", MinDepth: DepthRankShallow,
		Description: string(make([]byte, maxRequirementDescLen+1)),
	}
	if err := r.Validate(); err == nil {
		t.Error("oversized description should fail")
	}
}

func TestClassification_Validate(t *testing.T) {
	good := Classification{RequirementID: "R1", Observed: DepthRankMocked}
	if err := good.Validate(); err != nil {
		t.Errorf("good classification should validate: %v", err)
	}
	bad := Classification{RequirementID: "", Observed: DepthRankMocked}
	if err := bad.Validate(); err == nil {
		t.Error("empty req ID should fail")
	}
	bad2 := Classification{RequirementID: "R", Observed: DepthRank(99)}
	if err := bad2.Validate(); err == nil {
		t.Error("out-of-range observed should fail")
	}
}

func TestIsKnownDepthRank(t *testing.T) {
	for r := DepthRank(0); r <= DepthRankRealistic; r++ {
		if !IsKnownDepthRank(r) {
			t.Errorf("rank %d should be known", r)
		}
	}
	if IsKnownDepthRank(DepthRank(-1)) || IsKnownDepthRank(DepthRank(4)) {
		t.Error("out-of-range ranks should not be known")
	}
}
