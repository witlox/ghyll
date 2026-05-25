package bootstrap

import (
	"errors"
	"testing"

	"github.com/witlox/ghyll/catalogue"
)

// loadTestCatalogue is a test helper for loading the shipped catalogue
// from the bootstrap/ working dir.
func loadTestCatalogue(t *testing.T) *catalogue.Catalogue {
	t.Helper()
	cat, err := catalogue.Load("../gates/concepts")
	if err != nil {
		t.Fatalf("catalogue.Load: %v", err)
	}
	return cat
}

func TestBuildProposal_Analyst(t *testing.T) {
	rf, err := ParseRoleFile("../specs/architecture/roles/analyst.md")
	if err != nil {
		t.Fatalf("ParseRoleFile: %v", err)
	}
	cat := loadTestCatalogue(t)

	ap, err := BuildProposal(rf, cat, "analyst", "architect", "context-A")
	if err != nil {
		t.Fatalf("BuildProposal: %v", err)
	}
	if ap.Upstream != "analyst" || ap.Downstream != "architect" || ap.Context != "context-A" {
		t.Errorf("arrow framing wrong: %+v", ap)
	}
	if len(ap.Proposed) != len(rf.Clauses) {
		t.Errorf("len(Proposed) = %d; want %d (one per role clause)", len(ap.Proposed), len(rf.Clauses))
	}
	// G1 is a machine clause referencing unique-definition.
	g1 := ap.Proposed[0]
	if g1.ID != "G1" {
		t.Errorf("Proposed[0].ID = %q; want G1", g1.ID)
	}
	if !g1.IsMachine() {
		t.Error("G1 should be machine")
	}
	if g1.ConceptName != "unique-definition" {
		t.Errorf("G1.ConceptName = %q; want unique-definition", g1.ConceptName)
	}
	// unique-definition has one defaultable arg: case-sensitive=true.
	if got, ok := g1.DefaultArgs["case-sensitive"]; !ok || got != true {
		t.Errorf("G1.DefaultArgs[case-sensitive] = %v, ok=%v; want true, true", got, ok)
	}
	if g1.RoleArgsHint == "" {
		t.Error("G1.RoleArgsHint should preserve the role-file arg hint")
	}
	if g1.RoleSource != "analyst" {
		t.Errorf("RoleSource = %q; want analyst", g1.RoleSource)
	}
	// First attested clause: ConceptName empty, DefaultArgs nil, DefaultCost 0.
	for _, p := range ap.Proposed {
		if p.IsAttested() {
			if p.ConceptName != "" {
				t.Errorf("attested clause %s has ConceptName %q; want empty", p.ID, p.ConceptName)
			}
			if p.DefaultArgs != nil {
				t.Errorf("attested clause %s has DefaultArgs %v; want nil", p.ID, p.DefaultArgs)
			}
			break
		}
	}
}

func TestBuildProposal_AllRoles(t *testing.T) {
	cat := loadTestCatalogue(t)
	roles := []string{"analyst", "architect", "implementer", "integrator"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			rf, err := ParseRoleFile("../specs/architecture/roles/" + role + ".md")
			if err != nil {
				t.Fatalf("ParseRoleFile(%s): %v", role, err)
			}
			ap, err := BuildProposal(rf, cat, role, "downstream", "ctx")
			if err != nil {
				t.Fatalf("BuildProposal(%s): %v", role, err)
			}
			if len(ap.Proposed) == 0 {
				t.Errorf("%s: 0 proposed clauses", role)
			}
			// Every machine clause's concept must exist in the catalogue.
			for _, p := range ap.Proposed {
				if !p.IsMachine() {
					continue
				}
				if _, ok := cat.Get(p.ConceptName); !ok {
					t.Errorf("%s clause %s: concept %q not in catalogue", role, p.ID, p.ConceptName)
				}
			}
		})
	}
}

func TestBuildProposal_NilInputs(t *testing.T) {
	cat := loadTestCatalogue(t)
	if _, err := BuildProposal(nil, cat, "u", "d", "c"); err == nil {
		t.Error("nil RoleFile should fail")
	}
	rf := &RoleFile{Role: "test"}
	if _, err := BuildProposal(rf, nil, "u", "d", "c"); err == nil {
		t.Error("nil Catalogue should fail")
	}
}

func TestApply_ConfirmRecordsClauseUnchanged(t *testing.T) {
	// Scenario 94: "Operator confirms a clause unchanged" — recorded
	// with the proposed arguments.
	ap, cat := buildSingleClauseProposal(t, "mutation-score(scope, threshold, language)")
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatalf("Apply(confirm): %v", err)
	}
	rec := ap.Recorded()
	if len(rec) != 1 {
		t.Fatalf("Recorded() len = %d; want 1", len(rec))
	}
	if rec[0].Source != SourceRoleDefault {
		t.Errorf("Source = %v; want SourceRoleDefault", rec[0].Source)
	}
	// Args should equal the proposed DefaultArgs (deep equality not
	// needed here since they're identical maps).
	if len(rec[0].Args) != len(ap.Proposed[0].DefaultArgs) {
		t.Errorf("Args len = %d; want %d", len(rec[0].Args), len(ap.Proposed[0].DefaultArgs))
	}
	// Cost should match the catalogue's DefaultCost for mutation-score (3).
	if rec[0].Cost != 3 {
		t.Errorf("Cost = %d; want 3 (mutation-score DefaultCost)", rec[0].Cost)
	}
}

func TestApply_ModifyRaisingThresholdAllowed(t *testing.T) {
	// Scenario 99: Operator returns "modify" with {threshold: 0.85}
	// against a default {threshold: 0.7}. Raise-only.
	ap, cat := buildSingleClauseProposal(t, "mutation-score(scope, threshold, language)")
	// Override threshold while keeping the helper's required-arg
	// fill (scope, test-scope, language) so F29 validation passes.
	ap.Proposed[0].DefaultArgs["threshold"] = 0.7
	v := Verdict{
		Kind:         VerdictModify,
		ModifiedArgs: map[string]any{"threshold": 0.85},
	}
	if err := ap.Apply("G1", v, cat); err != nil {
		t.Fatalf("Apply(modify-raise): %v", err)
	}
	rec := ap.Recorded()
	if len(rec) != 1 {
		t.Fatalf("Recorded len = %d; want 1", len(rec))
	}
	got, ok := rec[0].Args["threshold"].(float64)
	if !ok || got != 0.85 {
		t.Errorf("recorded threshold = %v; want 0.85", rec[0].Args["threshold"])
	}
}

func TestApply_ModifyLoweringThresholdRefused(t *testing.T) {
	// Scenario 105: operator returns "modify" with {threshold: 0.5}
	// against default {threshold: 0.7}. Must refuse with the
	// "cannot-weaken-default" sentinel.
	ap, cat := buildSingleClauseProposal(t, "mutation-score(scope, threshold, language)")
	ap.Proposed[0].DefaultArgs = map[string]any{"threshold": 0.7}
	v := Verdict{
		Kind:         VerdictModify,
		ModifiedArgs: map[string]any{"threshold": 0.5},
	}
	err := ap.Apply("G1", v, cat)
	if err == nil {
		t.Fatal("Apply(modify-lower) should have failed; got nil")
	}
	if !errors.Is(err, ErrModifyWeakening) {
		t.Errorf("err = %v; want ErrModifyWeakening", err)
	}
	if len(ap.Recorded()) != 0 {
		t.Error("a refused modify must not record a clause")
	}
}

func TestExtend_AddsClauseAlongsideDefaults(t *testing.T) {
	// Scenario 110: operator extends with a new clause not in the
	// role file. The new clause is recorded alongside role-file
	// defaults.
	ap, cat := buildSingleClauseProposal(t, "compiles")
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatalf("Apply(confirm): %v", err)
	}
	ext := ProposedClause{
		ID:          "X-no-todo",
		Description: "Per-context: no TODO markers in src/auth",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "no-todo-marker",
		DefaultArgs: map[string]any{"scope": "src/auth/**"},
	}
	if err := ap.Extend(ext, cat); err != nil {
		t.Fatalf("Extend: %v", err)
	}
	rec := ap.Recorded()
	if len(rec) != 2 {
		t.Fatalf("Recorded len = %d; want 2 (confirm + extend)", len(rec))
	}
	if rec[1].Source != SourceOperatorExtension {
		t.Errorf("extended clause Source = %v; want SourceOperatorExtension", rec[1].Source)
	}
	if rec[1].ConceptName != "no-todo-marker" {
		t.Errorf("extended ConceptName = %q; want no-todo-marker", rec[1].ConceptName)
	}
	if len(ap.Extensions()) != 1 {
		t.Errorf("Extensions() len = %d; want 1", len(ap.Extensions()))
	}
	// Extending doesn't satisfy a proposed clause's verdict — the
	// pending confirm/skip on remaining clauses still has to land.
}

func TestExtend_RejectsInvalidShape(t *testing.T) {
	_, cat := buildSingleClauseProposal(t, "compiles")
	cases := map[string]ProposedClause{
		"empty ID":        {ID: "", EvalType: "machine", DepthType: "depth-robust", ConceptName: "compiles"},
		"bad eval":        {ID: "X", EvalType: "wat", DepthType: "depth-robust", ConceptName: "compiles"},
		"bad depth":       {ID: "X", EvalType: "machine", DepthType: "wat", ConceptName: "compiles"},
		"machine no name": {ID: "X", EvalType: "machine", DepthType: "depth-robust", ConceptName: ""},
		"unknown concept": {ID: "X", EvalType: "machine", DepthType: "depth-robust", ConceptName: "made-up-concept"},
	}
	for label, ext := range cases {
		t.Run(label, func(t *testing.T) {
			ap, _ := buildSingleClauseProposal(t, "compiles")
			err := ap.Extend(ext, cat)
			if !errors.Is(err, ErrExtensionInvalid) {
				t.Errorf("expected ErrExtensionInvalid for %s; got %v", label, err)
			}
		})
	}
}

func TestExtend_RejectsDuplicateID(t *testing.T) {
	// Extension cannot duplicate a proposed clause's ID nor a
	// previously-recorded clause's ID.
	ap, cat := buildSingleClauseProposal(t, "compiles")
	compilesArgs := map[string]any{"scope": "src/**", "language": "go"}
	noTodoArgs := map[string]any{"scope": "src/**"}
	// Duplicate of proposed G1.
	err := ap.Extend(ProposedClause{
		ID: "G1", EvalType: "machine", DepthType: "depth-robust",
		ConceptName: "compiles", DefaultArgs: compilesArgs,
	}, cat)
	if !errors.Is(err, ErrExtensionInvalid) {
		t.Errorf("duplicate of proposed: got %v; want ErrExtensionInvalid", err)
	}
	// Successful extend.
	if err := ap.Extend(ProposedClause{
		ID: "X-new", EvalType: "machine", DepthType: "depth-robust",
		ConceptName: "no-todo-marker", DefaultArgs: noTodoArgs,
	}, cat); err != nil {
		t.Fatal(err)
	}
	// Now duplicate of recorded.
	err = ap.Extend(ProposedClause{
		ID: "X-new", EvalType: "machine", DepthType: "depth-robust",
		ConceptName: "no-todo-marker", DefaultArgs: noTodoArgs,
	}, cat)
	if !errors.Is(err, ErrExtensionInvalid) {
		t.Errorf("duplicate of recorded: got %v; want ErrExtensionInvalid", err)
	}
}

func TestApply_SkipWithResidueRecorded(t *testing.T) {
	// Scenario 115: operator returns "skip" with a residue entry. The
	// clause is dropped from the exit gate; the residue is recorded.
	ap, cat := buildSingleClauseProposal(t, "compiles")
	v := Verdict{
		Kind:    VerdictSkip,
		Residue: "binding not yet implemented for this language",
	}
	if err := ap.Apply("G1", v, cat); err != nil {
		t.Fatalf("Apply(skip-with-residue): %v", err)
	}
	if len(ap.Recorded()) != 0 {
		t.Error("skipped clause should not be recorded in exit gate")
	}
	res := ap.Residue()
	if len(res) != 1 {
		t.Fatalf("Residue len = %d; want 1", len(res))
	}
	if res[0].Reason != "binding not yet implemented for this language" {
		t.Errorf("residue reason = %q", res[0].Reason)
	}
	if res[0].ClauseID != "G1" {
		t.Errorf("residue ClauseID = %q; want G1", res[0].ClauseID)
	}
}

func TestApply_SkipWithoutResidueRefused(t *testing.T) {
	// Scenario 121: operator returns "skip" without residue. Init
	// refuses with "residue-required-for-skip" and re-prompts.
	ap, cat := buildSingleClauseProposal(t, "compiles")
	v := Verdict{Kind: VerdictSkip, Residue: ""}
	err := ap.Apply("G1", v, cat)
	if err == nil {
		t.Fatal("Apply(skip without residue) should fail; got nil")
	}
	if !errors.Is(err, ErrResidueRequiredForSkip) {
		t.Errorf("err = %v; want ErrResidueRequiredForSkip", err)
	}
	if len(ap.Recorded()) != 0 || len(ap.Residue()) != 0 {
		t.Error("refused skip must not record clause or residue")
	}
	// Operator can retry with residue.
	v2 := Verdict{Kind: VerdictSkip, Residue: "operator-supplied reason"}
	if err := ap.Apply("G1", v2, cat); err != nil {
		t.Errorf("retry with residue should succeed; got %v", err)
	}
}

func TestApply_UnknownClauseID(t *testing.T) {
	ap, cat := buildSingleClauseProposal(t, "compiles")
	err := ap.Apply("G99", Verdict{Kind: VerdictConfirm}, cat)
	if !errors.Is(err, ErrUnknownClauseID) {
		t.Errorf("err = %v; want ErrUnknownClauseID", err)
	}
}

func TestApply_VerdictAlreadyApplied(t *testing.T) {
	ap, cat := buildSingleClauseProposal(t, "compiles")
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat)
	if !errors.Is(err, ErrVerdictAlreadyApplied) {
		t.Errorf("err = %v; want ErrVerdictAlreadyApplied", err)
	}
}

func TestApply_ModifyAttestedRefused(t *testing.T) {
	// Attested clauses have no schema; modify is meaningless on them.
	ap, cat := buildSingleClauseProposal(t, "compiles")
	ap.Proposed[0].EvalType = "attested"
	ap.Proposed[0].ConceptName = ""
	ap.Proposed[0].DefaultArgs = nil
	v := Verdict{Kind: VerdictModify, ModifiedArgs: map[string]any{"x": 1}}
	err := ap.Apply("G1", v, cat)
	if err == nil {
		t.Fatal("modify on attested clause should fail")
	}
}

func TestAllVerdictsReceived(t *testing.T) {
	ap, cat := buildSingleClauseProposal(t, "compiles")
	ap.Proposed = append(ap.Proposed, ProposedClause{
		ID:          "G2",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: "compiles",
		DefaultArgs: map[string]any{"scope": "src/**", "language": "go"},
	})
	if ap.AllVerdictsReceived() {
		t.Error("AllVerdictsReceived true before any verdict applied")
	}
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	if ap.AllVerdictsReceived() {
		t.Error("AllVerdictsReceived true after only one of two verdicts")
	}
	if err := ap.Apply("G2", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	if !ap.AllVerdictsReceived() {
		t.Error("AllVerdictsReceived false after both verdicts applied")
	}
}

func TestVerdictFor(t *testing.T) {
	ap, cat := buildSingleClauseProposal(t, "compiles")
	if _, ok := ap.VerdictFor("G1"); ok {
		t.Error("VerdictFor before Apply returned ok=true")
	}
	if err := ap.Apply("G1", Verdict{Kind: VerdictConfirm}, cat); err != nil {
		t.Fatal(err)
	}
	got, ok := ap.VerdictFor("G1")
	if !ok || got.Kind != VerdictConfirm {
		t.Errorf("VerdictFor after Apply: ok=%v kind=%v; want true, VerdictConfirm", ok, got.Kind)
	}
}

// buildSingleClauseProposal returns an ArrowProposal whose single
// proposed clause references the given concept (passed as
// "concept-name" or "concept-name(hint)"). The default args are
// populated from the catalogue schema.
func buildSingleClauseProposal(t *testing.T, conceptHint string) (*ArrowProposal, *catalogue.Catalogue) {
	t.Helper()
	cat := loadTestCatalogue(t)
	// Strip any "(...)" hint to get the bare concept name.
	conceptName := conceptHint
	if i := indexRune(conceptHint, '('); i > 0 {
		conceptName = conceptHint[:i]
	}
	concept, ok := cat.Get(conceptName)
	if !ok {
		t.Fatalf("buildSingleClauseProposal: catalogue missing %q", conceptName)
	}
	// validation-pass-2 F29: BuildProposal/Apply now validate that
	// every required arg is present. extractDefaultArgs only pulls
	// defaultable args; for tests we populate required-no-default
	// args with synthetic placeholder values.
	args := extractDefaultArgs(concept)
	for argName, schema := range concept.Arguments {
		if !schema.Required {
			continue
		}
		if _, present := args[argName]; present {
			continue
		}
		args[argName] = syntheticArgValue(schema.Type)
	}
	ap := &ArrowProposal{
		Upstream:   "analyst",
		Downstream: "architect",
		Context:    "context-A",
		Proposed: []ProposedClause{{
			ID:           "G1",
			Description:  "test clause",
			EvalType:     "machine",
			DepthType:    "depth-robust",
			ConceptName:  conceptName,
			DefaultArgs:  args,
			DefaultCost:  concept.DefaultCost,
			RoleArgsHint: conceptHint,
			RoleSource:   "test",
		}},
		verdicts: make(map[string]Verdict),
	}
	return ap, cat
}

// syntheticArgValue returns a placeholder value of the given catalogue
// argument type. Used by test helpers that build proposed clauses
// for concepts whose required args have no default — F29 now
// requires those args to be present at Apply time.
func syntheticArgValue(argType string) any {
	switch argType {
	case "string", "artifact-ref", "command", "duration", "enum-or-path":
		return "test-value"
	case "path-glob":
		return "src/**"
	case "regex":
		return "^test"
	case "language-id":
		return "go"
	case "role-id":
		return "analyst"
	case "bounded-context-id":
		return "test-context"
	case "pass-id", "arrow-id", "dependency-id":
		return "test-id"
	case "int":
		return 0
	case "number":
		return 0.5
	case "boolean":
		return false
	case "list":
		return []any{}
	case "severity":
		return "medium"
	case "finding-status":
		return "open"
	case "depth-tier":
		return 0
	case "enum":
		return ""
	case "int-or-range":
		return 0
	}
	return ""
}

// indexRune is a tiny strings.IndexRune to avoid importing strings just
// for this test helper.
func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

// TestScenario_MapKeys_DeterministicOrder covers post-prod-readiness
// adversarial L-C: mapKeys must return keys in a stable order so the
// residue reason text formatted from ErrClauseArgsIncomplete is
// byte-identical across runs. The bug was that Go's randomized map
// iteration produced different reason strings on re-runs of the same
// inputs, making the audit-facing residue field flap.
func TestScenario_MapKeys_DeterministicOrder(t *testing.T) {
	args := map[string]any{
		"zeta":  1,
		"alpha": 2,
		"mu":    3,
		"beta":  4,
		"omega": 5,
	}
	var first []string
	for i := 0; i < 50; i++ {
		got := mapKeys(args)
		if i == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("iter %d: len = %d; want %d", i, len(got), len(first))
		}
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("iter %d: mapKeys returned non-deterministic order: %v vs %v",
					i, got, first)
			}
		}
	}
	// Confirm the order is sorted (the chosen canonicalization).
	want := []string{"alpha", "beta", "mu", "omega", "zeta"}
	for i, k := range want {
		if first[i] != k {
			t.Errorf("first[%d] = %q; want %q (sorted order)", i, first[i], k)
		}
	}
}

// TestScenario_ClauseArgsIncomplete_ReasonReproducible exercises the
// end-to-end path: validateClauseArgs must produce a byte-identical
// error string on every invocation with the same arg set so the
// residue reason recorded by autoAcceptProposal is reproducible
// across ghyll init runs. Pairs with L-C.
func TestScenario_ClauseArgsIncomplete_ReasonReproducible(t *testing.T) {
	cat := loadTestCatalogue(t)
	// Pick a concept with at least one required-without-default arg.
	// path-scope-coverage has scope (required, no default) and other
	// args. We feed a superset of args (some required missing, some
	// non-schema present) to exercise both `missing` and `mapKeys`
	// branches of the formatted error.
	var p ProposedClause
	var conceptName string
	for _, name := range cat.List() {
		c, ok := cat.Get(name)
		if !ok {
			continue
		}
		hasRequiredNoDefault := false
		for _, schema := range c.Arguments {
			if schema.Required && schema.Default == nil {
				hasRequiredNoDefault = true
				break
			}
		}
		if hasRequiredNoDefault {
			conceptName = name
			break
		}
	}
	if conceptName == "" {
		t.Skip("no catalogue concept with required-without-default args; nothing to exercise")
	}
	p = ProposedClause{
		ID:          "C1",
		EvalType:    "machine",
		DepthType:   "depth-robust",
		ConceptName: conceptName,
	}
	// Provide several non-required-key args to force the mapKeys
	// branch to format multiple keys.
	args := map[string]any{
		"zeta":  "z",
		"alpha": "a",
		"mu":    "m",
		"beta":  "b",
	}
	var first string
	for i := 0; i < 10; i++ {
		err := validateClauseArgs(p, args, cat)
		if err == nil {
			t.Fatalf("iter %d: validateClauseArgs returned nil; expected ErrClauseArgsIncomplete", i)
		}
		if !errors.Is(err, ErrClauseArgsIncomplete) {
			t.Fatalf("iter %d: err = %v; want ErrClauseArgsIncomplete", i, err)
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("iter %d: error string drifted across runs:\n  first: %s\n  this:  %s",
				i, first, err.Error())
		}
	}
}
