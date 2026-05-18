package runner

import (
	"errors"
	"testing"
)

func sampleArrow(id string) ArrowDefinition {
	return ArrowDefinition{
		ID:         id,
		SourceRole: "analyst",
		TargetRole: "architect",
		Stratum:    "L4",
		Context:    "checkout",
		Clauses: []Clause{
			{Concept: "lint-clean", ClauseID: "C1"},
		},
		Requirements: []Requirement{
			{ID: "R1", MinDepth: DepthRankShallow, Description: "test req"},
		},
	}
}

func TestGrid_AppendAndLookup(t *testing.T) {
	g := NewGrid()
	v, err := g.Append(sampleArrow("A1"))
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Errorf("version = %d; want 1", v)
	}
	def, ok := g.Lookup("A1")
	if !ok {
		t.Fatal("Lookup returned !ok")
	}
	if def.SourceRole != "analyst" {
		t.Errorf("SourceRole = %q", def.SourceRole)
	}
}

func TestGrid_AppendDuplicateRefused(t *testing.T) {
	g := NewGrid()
	_, _ = g.Append(sampleArrow("A1"))
	_, err := g.Append(sampleArrow("A1"))
	if !errors.Is(err, ErrArrowAlreadyDeclared) {
		t.Errorf("dup append should error with ErrArrowAlreadyDeclared; got %v", err)
	}
}

func TestGrid_VersionIncrements(t *testing.T) {
	g := NewGrid()
	v1, _ := g.Append(sampleArrow("A1"))
	v2, _ := g.Append(sampleArrow("A2"))
	if v2 <= v1 {
		t.Errorf("version did not bump: %d -> %d", v1, v2)
	}
}

func TestGrid_AppendOnTheSpotBumpsCounter(t *testing.T) {
	g := NewGrid()
	_, _ = g.Append(sampleArrow("A0"))
	if g.OnTheSpotInterruptions() != 0 {
		t.Error("regular Append should NOT bump on-the-spot counter")
	}
	_, _ = g.AppendOnTheSpot(sampleArrow("A1"))
	_, _ = g.AppendOnTheSpot(sampleArrow("A2"))
	if got := g.OnTheSpotInterruptions(); got != 2 {
		t.Errorf("OnTheSpotInterruptions = %d; want 2", got)
	}
}

func TestGrid_ObserverFires(t *testing.T) {
	g := NewGrid()
	var events []GridEvent
	g.Observe(func(e GridEvent) { events = append(events, e) })
	_, _ = g.Append(sampleArrow("A1"))
	_, _ = g.AppendOnTheSpot(sampleArrow("A2"))
	if len(events) != 2 {
		t.Fatalf("events = %d; want 2", len(events))
	}
	if events[0].Kind != GridEventAppend || events[1].Kind != GridEventOnTheSpotAppend {
		t.Errorf("event kinds = %v, %v", events[0].Kind, events[1].Kind)
	}
}

func TestGrid_LookupUndeclared(t *testing.T) {
	g := NewGrid()
	_, ok := g.Lookup("nope")
	if ok {
		t.Error("undeclared arrow should return ok=false")
	}
}

func TestGrid_ArrowsSortedSnapshot(t *testing.T) {
	g := NewGrid()
	_, _ = g.Append(sampleArrow("Z"))
	_, _ = g.Append(sampleArrow("A"))
	_, _ = g.Append(sampleArrow("M"))
	got := g.Arrows()
	want := []string{"A", "M", "Z"}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q; want %q", i, got[i], w)
		}
	}
}

func TestArrowDefinition_Validate(t *testing.T) {
	good := sampleArrow("A1")
	if err := good.Validate(); err != nil {
		t.Errorf("good arrow should validate: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*ArrowDefinition)
	}{
		{"empty ID", func(d *ArrowDefinition) { d.ID = "" }},
		{"whitespace ID", func(d *ArrowDefinition) { d.ID = "   " }},
		{"empty source role", func(d *ArrowDefinition) { d.SourceRole = "" }},
		{"empty target role", func(d *ArrowDefinition) { d.TargetRole = "" }},
		{"no clauses", func(d *ArrowDefinition) { d.Clauses = nil }},
		{"clause concept empty", func(d *ArrowDefinition) {
			d.Clauses = []Clause{{Concept: ""}}
		}},
		{"bad requirement", func(d *ArrowDefinition) {
			d.Requirements = []Requirement{{ID: "", MinDepth: DepthRankShallow, Description: "x"}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := good
			d.Clauses = append([]Clause{}, good.Clauses...)
			d.Requirements = append([]Requirement{}, good.Requirements...)
			c.mut(&d)
			if err := d.Validate(); err == nil {
				t.Errorf("%s: should fail", c.name)
			}
		})
	}
}
