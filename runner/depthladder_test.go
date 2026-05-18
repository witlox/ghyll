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
	if got := l.Label(DepthRank(99)); got != "" {
		t.Errorf("Label(99) should return empty; got %q", got)
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

func TestRequirement_Validate(t *testing.T) {
	good := Requirement{ID: "R1", MinDepth: DepthRankShallow}
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
