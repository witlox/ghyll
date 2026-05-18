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

func TestGrid_LookupDeepCopiesAgainstPoisoning(t *testing.T) {
	// Validation-pass-6 F1: mutating the returned ArrowDefinition's
	// slices/maps must NOT poison stored state.
	g := NewGrid()
	def := sampleArrow("A1")
	def.Clauses[0].Args = map[string]any{"k": "original"}
	_, _ = g.Append(def)

	out, _ := g.Lookup("A1")
	out.Clauses[0].Concept = "POISONED"
	out.Clauses[0].Args["k"] = "POISONED"
	out.Requirements[0].Description = "POISONED"

	again, _ := g.Lookup("A1")
	if again.Clauses[0].Concept != "lint-clean" {
		t.Errorf("Lookup poisoned via Clauses; got %q", again.Clauses[0].Concept)
	}
	if again.Clauses[0].Args["k"] != "original" {
		t.Errorf("Lookup poisoned via Args; got %v", again.Clauses[0].Args["k"])
	}
	if again.Requirements[0].Description != "test req" {
		t.Errorf("Lookup poisoned via Requirements; got %q", again.Requirements[0].Description)
	}
}

func TestGrid_AppendDeepCopiesAgainstPoisoning(t *testing.T) {
	// F1 (caller side): mutating the ArrowDefinition AFTER Append
	// must NOT poison stored state.
	g := NewGrid()
	def := sampleArrow("A1")
	def.Clauses[0].Concept = "original"
	_, _ = g.Append(def)
	def.Clauses[0].Concept = "POISONED"
	stored, _ := g.Lookup("A1")
	if stored.Clauses[0].Concept != "original" {
		t.Errorf("post-Append mutation poisoned store; got %q", stored.Clauses[0].Concept)
	}
}

func TestGrid_RejectsBlankStratumContext(t *testing.T) {
	// F2: Validate must require non-empty Stratum/Context.
	g := NewGrid()
	def := sampleArrow("A1")
	def.Stratum = ""
	_, err := g.Append(def)
	if err == nil {
		t.Error("empty Stratum should fail Validate")
	}
	def.Stratum = "L4"
	def.Context = "  "
	_, err = g.Append(def)
	if err == nil {
		t.Error("whitespace Context should fail Validate")
	}
}

func TestGrid_RejectsOverManyClauses(t *testing.T) {
	// F4: cap on clauses per arrow.
	g := NewGrid()
	def := sampleArrow("A1")
	def.Clauses = make([]Clause, maxArrowClauses+1)
	for i := range def.Clauses {
		def.Clauses[i] = Clause{Concept: "x"}
	}
	_, err := g.Append(def)
	if err == nil {
		t.Error("over-cap clauses should fail Validate")
	}
}

func TestGrid_ConcurrentAppendLookupObserve(t *testing.T) {
	// F15: race-detector test. Run under `go test -race`.
	t.Parallel()
	g := NewGrid()
	var observed int
	g.Observe(func(GridEvent) { observed++ })
	done := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		i := i
		go func() {
			def := sampleArrow("A" + string(rune('a'+i)))
			_, _ = g.Append(def)
			_ = g.Has("A" + string(rune('a'+i)))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
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
