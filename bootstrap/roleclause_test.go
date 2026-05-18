package bootstrap

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRoleFile_Analyst(t *testing.T) {
	rf, err := ParseRoleFile("../specs/direction/roles/analyst.md")
	if err != nil {
		t.Fatalf("ParseRoleFile(analyst.md): %v", err)
	}
	if rf.Role != "analyst" {
		t.Errorf("Role = %q; want %q", rf.Role, "analyst")
	}
	// analyst.md exit gate has 13 clauses: G1–G13.
	if got := len(rf.Clauses); got != 13 {
		t.Errorf("len(Clauses) = %d; want 13", got)
	}
	// Spot-check G1 (machine clause).
	if len(rf.Clauses) >= 1 {
		g1 := rf.Clauses[0]
		if g1.ID != "G1" {
			t.Errorf("Clauses[0].ID = %q; want G1", g1.ID)
		}
		if g1.ConceptName != "unique-definition" {
			t.Errorf("G1.ConceptName = %q; want unique-definition", g1.ConceptName)
		}
		if !strings.Contains(g1.ConceptArgsRaw, "ubiquitous-language.md") {
			t.Errorf("G1.ConceptArgsRaw = %q; should reference ubiquitous-language.md", g1.ConceptArgsRaw)
		}
		if g1.EvalType != "machine" {
			t.Errorf("G1.EvalType = %q; want machine", g1.EvalType)
		}
		if g1.DepthType != "depth-robust" {
			t.Errorf("G1.DepthType = %q; want depth-robust", g1.DepthType)
		}
		if !g1.IsMachine() {
			t.Error("G1.IsMachine() = false; want true")
		}
		if g1.IsAttested() {
			t.Error("G1.IsAttested() = true; want false")
		}
	}
	// Spot-check an attested clause (G7 in analyst.md is the first
	// attested judgement: "Every feature has Gherkin scenarios for
	// failure paths...").
	var sawAttested bool
	for _, c := range rf.Clauses {
		if c.IsAttested() {
			sawAttested = true
			if c.ConceptName != "" {
				t.Errorf("attested clause %s has non-empty ConceptName %q", c.ID, c.ConceptName)
			}
			if c.DepthType != "depth-sensitive" {
				// Analyst attested clauses are all depth-sensitive per the
				// table; if this fails, the file changed shape.
				t.Errorf("attested clause %s DepthType = %q; want depth-sensitive", c.ID, c.DepthType)
			}
		}
	}
	if !sawAttested {
		t.Error("expected at least one attested clause in analyst.md exit gate")
	}
}

func TestParseRoleFile_AllRoles(t *testing.T) {
	// Every role file ships with an exit-gate table; parsing all four
	// must succeed.
	roles := []string{"analyst", "architect", "implementer", "integrator"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			path := "../specs/direction/roles/" + role + ".md"
			rf, err := ParseRoleFile(path)
			if err != nil {
				t.Fatalf("ParseRoleFile(%s): %v", role, err)
			}
			if rf.Role != role {
				t.Errorf("Role = %q; want %q", rf.Role, role)
			}
			if len(rf.Clauses) == 0 {
				t.Errorf("%s.md: parsed 0 clauses; expected non-empty exit gate", role)
			}
			// Every clause must have a non-empty ID and clause text.
			for i, c := range rf.Clauses {
				if c.ID == "" {
					t.Errorf("%s clause[%d] has empty ID", role, i)
				}
				if c.ClauseText == "" {
					t.Errorf("%s clause[%d] %q has empty ClauseText", role, i, c.ID)
				}
				if c.IsMachine() == c.IsAttested() {
					t.Errorf("%s clause %s: exactly one of IsMachine/IsAttested must be true", role, c.ID)
				}
			}
		})
	}
}

func TestParseExitGateTable_NoHeader(t *testing.T) {
	content := "# Role: Test\n\nNo exit gate here.\n"
	_, err := parseExitGateTable(content)
	if !errors.Is(err, ErrRoleFileNoTable) {
		t.Errorf("expected ErrRoleFileNoTable; got %v", err)
	}
}

func TestParseExitGateTable_HeaderWhitespaceTolerant(t *testing.T) {
	// Extra spaces in the header should not block parsing.
	content := "" +
		"|  #  |  Clause  |  Concept (machine) or attested judgement  |  Eval  |  Depth  |\n" +
		"|---|---|---|---|---|\n" +
		"| G1 | desc | `compiles`() | machine | depth-robust |\n"
	clauses, err := parseExitGateTable(content)
	if err != nil {
		t.Fatalf("parseExitGateTable: %v", err)
	}
	if len(clauses) != 1 {
		t.Fatalf("len(clauses) = %d; want 1", len(clauses))
	}
	if clauses[0].ConceptName != "compiles" {
		t.Errorf("ConceptName = %q; want compiles", clauses[0].ConceptName)
	}
	if clauses[0].ConceptArgsRaw != "" {
		t.Errorf("ConceptArgsRaw = %q; want empty (arg-less concept)", clauses[0].ConceptArgsRaw)
	}
}

func TestParseExitGateTable_MalformedRow(t *testing.T) {
	cases := map[string]string{
		"missing cell": "| G1 | desc | machine | depth-robust |", // 4 cells
		"unknown eval": "" +
			"| # | Clause | Concept (machine) or attested judgement | Eval | Depth |\n" +
			"|---|---|---|---|---|\n" +
			"| G1 | desc | `compiles`() | bogus | depth-robust |\n",
		"unknown depth": "" +
			"| # | Clause | Concept (machine) or attested judgement | Eval | Depth |\n" +
			"|---|---|---|---|---|\n" +
			"| G1 | desc | `compiles`() | machine | bogus |\n",
		"attested with concept": "" +
			"| # | Clause | Concept (machine) or attested judgement | Eval | Depth |\n" +
			"|---|---|---|---|---|\n" +
			"| G1 | desc | `compiles`() | attested | depth-sensitive |\n",
		"machine with judgement": "" +
			"| # | Clause | Concept (machine) or attested judgement | Eval | Depth |\n" +
			"|---|---|---|---|---|\n" +
			"| G1 | desc | (judgement) | machine | depth-robust |\n",
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			_, err := parseExitGateTable(content)
			if err == nil {
				t.Errorf("%s: expected error; got nil", label)
			}
		})
	}
}

func TestParseConceptCell(t *testing.T) {
	cases := []struct {
		cell        string
		wantConcept string
		wantArgs    string
		wantErr     bool
	}{
		{"(judgement)", "", "", false},
		{"`unique-definition`(`ubiquitous-language.md`)", "unique-definition", "`ubiquitous-language.md`", false},
		{"`compiles`()", "compiles", "", false},
		{"`arrow-artifact-present`(analyst→architect coverage-claim)", "arrow-artifact-present", "analyst→architect coverage-claim", false},
		{"`no-orphan-symbol`(exported-behaviours)", "no-orphan-symbol", "exported-behaviours", false},
		// Unrecognized cell — error.
		{"not a call", "", "", true},
		{"`bad", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.cell, func(t *testing.T) {
			concept, args, err := parseConceptCell(c.cell)
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error for %q; got nil", c.cell)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for %q: %v", c.cell, err)
				return
			}
			if concept != c.wantConcept {
				t.Errorf("concept = %q; want %q", concept, c.wantConcept)
			}
			if args != c.wantArgs {
				t.Errorf("args = %q; want %q", args, c.wantArgs)
			}
		})
	}
}

func TestParseRoleFile_MissingFile(t *testing.T) {
	_, err := ParseRoleFile("/no/such/path.md")
	if err == nil {
		t.Error("expected error for missing file; got nil")
	}
}
