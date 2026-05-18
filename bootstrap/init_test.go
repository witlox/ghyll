package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/witlox/ghyll/catalogue"
)

// Helper: a fully-resolved single-arrow proposal for tests. All
// proposed clauses receive Confirm verdicts.
func builtArrowProposal(t *testing.T, cat *catalogue.Catalogue, upstream, downstream, context string) *ArrowProposal {
	t.Helper()
	concept, _ := cat.Get("compiles")
	ap := NewArrowProposal(upstream, downstream, context, []ProposedClause{{
		ID:          "G1",
		Description: "compiles for testing",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: extractDefaultArgs(concept),
		DefaultCost: concept.DefaultCost,
		RoleSource:  "test",
	}})
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	return ap
}

func TestBuildInitGrid_AssemblesGridFromInputs(t *testing.T) {
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ProjectProfile{
		Mode: ModeGreenfield,
		BoundedContexts: []BoundedContext{
			{ID: "contextA", Description: "Payments"},
			{ID: "contextB", Description: "Identity"},
		},
		DiamondRoles: FixedDiamondRoles,
	}
	proposals := []*ArrowProposal{
		builtArrowProposal(t, cat, "analyst", "architect", "contextA"),
		builtArrowProposal(t, cat, "analyst", "architect", "contextB"),
	}

	g, err := BuildInitGrid("alice@example.com", profile, proposals)
	if err != nil {
		t.Fatalf("BuildInitGrid: %v", err)
	}
	if g.GridVersion != 1 {
		t.Errorf("GridVersion = %d; want 1 (first grid)", g.GridVersion)
	}
	if g.CreatedByOpID != "alice@example.com" {
		t.Errorf("CreatedByOpID = %q", g.CreatedByOpID)
	}
	if len(g.BoundedContexts) != 2 {
		t.Errorf("BoundedContexts len = %d; want 2", len(g.BoundedContexts))
	}
	if len(g.Arrows) != 2 {
		t.Errorf("Arrows len = %d; want 2", len(g.Arrows))
	}
	// Spot-check the first arrow's shape.
	a0 := g.Arrows[0]
	if a0["upstream"] != "analyst" {
		t.Errorf("Arrows[0].upstream = %v; want analyst", a0["upstream"])
	}
	if a0["context"] != "contextA" {
		t.Errorf("Arrows[0].context = %v; want contextA", a0["context"])
	}
	clauses, ok := a0["clauses"].([]map[string]any)
	if !ok {
		t.Fatalf("Arrows[0].clauses wrong type %T", a0["clauses"])
	}
	if len(clauses) != 1 {
		t.Fatalf("clauses len = %d; want 1", len(clauses))
	}
	if clauses[0]["id"] != "G1" {
		t.Errorf("clauses[0].id = %v; want G1", clauses[0]["id"])
	}
	if clauses[0]["concept"] != "compiles" {
		t.Errorf("clauses[0].concept = %v; want compiles", clauses[0]["concept"])
	}
	if clauses[0]["source"] != "role-default" {
		t.Errorf("clauses[0].source = %v; want role-default", clauses[0]["source"])
	}
}

func TestBuildInitGrid_SerializesResidue(t *testing.T) {
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ProjectProfile{
		BoundedContexts: []BoundedContext{{ID: "contextA"}},
		DiamondRoles:    FixedDiamondRoles,
	}
	// Build a proposal with one clause skipped + residue.
	concept, _ := cat.Get("compiles")
	ap := NewArrowProposal("analyst", "architect", "contextA", []ProposedClause{{
		ID:          "G1",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: extractDefaultArgs(concept),
	}})
	if err := ap.Apply("G1", Verdict{
		Kind:    VerdictSkip,
		Residue: "binding not available for this language in this context",
	}, cat); err != nil {
		t.Fatal(err)
	}

	g, err := BuildInitGrid("alice", profile, []*ArrowProposal{ap})
	if err != nil {
		t.Fatalf("BuildInitGrid: %v", err)
	}
	if len(g.Residue) != 1 {
		t.Fatalf("Residue len = %d; want 1", len(g.Residue))
	}
	r := g.Residue[0]
	if r["clause-id"] != "G1" {
		t.Errorf("residue clause-id = %v; want G1", r["clause-id"])
	}
	if r["arrow"] != "analyst→architect/contextA" {
		t.Errorf("residue arrow = %v", r["arrow"])
	}
}

func TestBuildInitGrid_RejectsEmptyOpID(t *testing.T) {
	profile := &ProjectProfile{BoundedContexts: []BoundedContext{{ID: "contextA"}}}
	_, err := BuildInitGrid("", profile, []*ArrowProposal{})
	if !errors.Is(err, ErrInitOpIDEmpty) {
		t.Errorf("err = %v; want ErrInitOpIDEmpty", err)
	}
}

func TestBuildInitGrid_RejectsNilProfile(t *testing.T) {
	_, err := BuildInitGrid("alice", nil, []*ArrowProposal{})
	if !errors.Is(err, ErrInitProfileNil) {
		t.Errorf("err = %v; want ErrInitProfileNil", err)
	}
}

func TestBuildInitGrid_RejectsEmptyProposals(t *testing.T) {
	profile := &ProjectProfile{BoundedContexts: []BoundedContext{{ID: "contextA"}}}
	_, err := BuildInitGrid("alice", profile, nil)
	if !errors.Is(err, ErrInitProposalsEmpty) {
		t.Errorf("err = %v; want ErrInitProposalsEmpty", err)
	}
}

func TestBuildInitGrid_RejectsRefusalAccepted(t *testing.T) {
	profile := &ProjectProfile{BoundedContexts: []BoundedContext{{ID: "contextA"}}}
	if _, err := profile.ProposeRefusal(RiskAssessment{}); err != nil {
		t.Fatal(err)
	}
	if err := profile.AcceptRefusal(); err != nil {
		t.Fatal(err)
	}
	_, err := BuildInitGrid("alice", profile, []*ArrowProposal{})
	if !errors.Is(err, ErrInitRefusalAccepted) {
		t.Errorf("err = %v; want ErrInitRefusalAccepted", err)
	}
}

func TestBuildInitGrid_RejectsIncompleteVerdicts(t *testing.T) {
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ProjectProfile{BoundedContexts: []BoundedContext{{ID: "contextA"}}}
	concept, _ := cat.Get("compiles")
	// Two proposed clauses; only one gets a verdict.
	ap := NewArrowProposal("analyst", "architect", "contextA", []ProposedClause{
		{ID: "G1", EvalType: "machine", DepthType: "depth-robust", ConceptName: "compiles", DefaultArgs: extractDefaultArgs(concept)},
		{ID: "G2", EvalType: "machine", DepthType: "depth-robust", ConceptName: "compiles", DefaultArgs: extractDefaultArgs(concept)},
	})
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	_, err = BuildInitGrid("alice", profile, []*ArrowProposal{ap})
	if !errors.Is(err, ErrInitVerdictsIncomplete) {
		t.Errorf("err = %v; want ErrInitVerdictsIncomplete", err)
	}
}

func TestBuildInitGrid_RejectsArrowForUndeclaredContext(t *testing.T) {
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ProjectProfile{
		BoundedContexts: []BoundedContext{{ID: "contextA"}},
	}
	// Proposal references contextZ which isn't in the profile.
	ap := builtArrowProposal(t, cat, "analyst", "architect", "contextZ")
	_, err = BuildInitGrid("alice", profile, []*ArrowProposal{ap})
	if !errors.Is(err, ErrInitContextNotInProfile) {
		t.Errorf("err = %v; want ErrInitContextNotInProfile", err)
	}
}

func TestBuildInitGrid_NilProposalInSliceErrors(t *testing.T) {
	profile := &ProjectProfile{BoundedContexts: []BoundedContext{{ID: "contextA"}}}
	_, err := BuildInitGrid("alice", profile, []*ArrowProposal{nil})
	if err == nil {
		t.Error("nil proposal in slice should error")
	}
}

func TestBuildInitGrid_EndToEndWrite(t *testing.T) {
	// Full pipeline: build + write + re-read.
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatal(err)
	}
	profile := &ProjectProfile{
		Mode:            ModeGreenfield,
		BoundedContexts: []BoundedContext{{ID: "contextA"}},
		DiamondRoles:    FixedDiamondRoles,
	}
	proposals := []*ArrowProposal{
		builtArrowProposal(t, cat, "analyst", "architect", "contextA"),
	}
	g, err := BuildInitGrid("alice@example.com", profile, proposals)
	if err != nil {
		t.Fatalf("BuildInitGrid: %v", err)
	}

	dir := t.TempDir()
	if err := g.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Versioned file exists.
	if _, err := os.Stat(filepath.Join(dir, ".ghyll", "grid.v1.yaml")); err != nil {
		t.Errorf("grid.v1.yaml absent: %v", err)
	}
	// Pointer exists and reads "v1".
	pointer, err := os.ReadFile(filepath.Join(dir, ".ghyll", "grid.current"))
	if err != nil {
		t.Fatalf("read grid.current: %v", err)
	}
	if string(pointer) != "v1\n" {
		t.Errorf("grid.current = %q; want \"v1\\n\"", string(pointer))
	}
	// No stale temp files.
	entries, _ := os.ReadDir(filepath.Join(dir, ".ghyll"))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || filepath.Ext(e.Name()) == ".partial" {
			t.Errorf(".ghyll has stale temp file: %s", e.Name())
		}
	}
	// Round-trip read.
	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.CreatedByOpID != "alice@example.com" {
		t.Errorf("round-trip CreatedByOpID = %q", got.CreatedByOpID)
	}
	if len(got.Arrows) != 1 {
		t.Errorf("round-trip Arrows len = %d; want 1", len(got.Arrows))
	}
}

func TestClauseSourceString(t *testing.T) {
	cases := map[ClauseSource]string{
		SourceRoleDefault:       "role-default",
		SourceOperatorExtension: "operator-extension",
	}
	for src, want := range cases {
		if got := clauseSourceString(src); got != want {
			t.Errorf("clauseSourceString(%v) = %q; want %q", src, got, want)
		}
	}
}
