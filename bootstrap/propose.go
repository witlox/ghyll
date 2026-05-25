package bootstrap

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/witlox/ghyll/catalogue"
)

// Auto-propose + operator-confirm loop. Per ADR-011 §B.2:
//
//   1. Harness drafts each clause from a role file's exit-gate table.
//   2. Operator returns one of four verdicts per clause:
//      - confirm: accept as-is.
//      - modify:  raise costs / tighten thresholds (CheckModification
//                 enforces raise-only).
//      - extend:  add a per-context clause not in the role file.
//      - skip:    drop the clause; REQUIRES a residue entry.
//   3. The grid is recorded only after every proposed clause has
//      received a verdict (AllVerdictsReceived).

// ProposedClause is a clause draft emitted by auto-propose.
//
// For machine clauses, ConceptName is the catalogue concept name and
// DefaultArgs / DefaultCost come from the catalogue concept schema.
// RoleArgsHint preserves the literal text inside the role-file's
// concept-call parens (e.g., "`ubiquitous-language.md`") so the
// operator sees what the role author had in mind. Mapping the raw
// hint onto named arguments is deferred to a later refinement (the
// scenarios this slice greens do not need the mapping).
//
// For attested clauses, ConceptName is empty and DefaultArgs is nil
// (the operator's attestation, not a machine evaluator, decides the
// result).
type ProposedClause struct {
	// Identification (from role file).
	ID          string // e.g., "G1"
	Description string // clause text

	// Eval / depth (from role file).
	EvalType  string // "machine" or "attested"
	DepthType string // "depth-robust" or "depth-sensitive"

	// Machine-clause fields (zero values for attested).
	ConceptName  string
	DefaultArgs  map[string]any
	DefaultCost  int
	RoleArgsHint string

	// Source role (e.g., "analyst") — recorded for traceability.
	RoleSource string
}

// IsMachine reports whether the clause is machine-evaluated.
func (p ProposedClause) IsMachine() bool { return p.EvalType == "machine" }

// IsAttested reports whether the clause requires attested judgement.
func (p ProposedClause) IsAttested() bool { return p.EvalType == "attested" }

// VerdictKind is the operator's response to a proposed clause.
//
// Extend is not a verdict — it adds a new clause beyond the role-file
// defaults rather than deciding the fate of a proposed one. See the
// Extend method.
type VerdictKind int

const (
	VerdictConfirm VerdictKind = iota
	VerdictModify
	VerdictSkip
)

// Verdict is the operator's per-clause decision.
//
// For Modify: ModifiedArgs is a diff against the proposed clause's
// DefaultArgs; only the changed keys appear. CheckModification
// enforces raise-only.
//
// For Skip: Residue is the operator-supplied reason. Empty Residue
// triggers ErrResidueRequiredForSkip.
type Verdict struct {
	Kind         VerdictKind
	ModifiedArgs map[string]any
	Residue      string
}

// ClauseSource records how a recorded clause entered the grid.
type ClauseSource int

const (
	SourceRoleDefault       ClauseSource = iota // confirm or modify against role file
	SourceOperatorExtension                     // extend
)

// RecordedClause is a clause that has been confirmed, modified, or
// extended into the arrow's exit gate.
type RecordedClause struct {
	ID          string
	Description string
	EvalType    string
	DepthType   string
	ConceptName string
	Args        map[string]any // merged: defaults + any modify diff
	Cost        int
	Source      ClauseSource
}

// ResidueEntry records a skip-with-reason. The clause is NOT in the
// arrow's exit gate; the residue lives in the grid's residue list so
// the adversarial phase can attack the gap.
type ResidueEntry struct {
	ClauseID     string
	Description  string
	ConceptName  string
	RoleArgsHint string // verbatim arg hint from the role file (validation-pass-2 F28)
	Reason       string
}

// ArrowProposal is one (role-pair, context) arrow being populated by
// auto-propose. Construct via BuildProposal; drive via Apply.
//
// ArrowProposal is safe for concurrent use; all public methods take
// the internal mutex (validation-pass-2 F27). Callers may observe
// snapshots via Recorded/Residue/Extensions while another goroutine
// applies verdicts — readers see a consistent point-in-time view.
type ArrowProposal struct {
	Upstream   string
	Downstream string
	Context    string
	Proposed   []ProposedClause

	mu         sync.Mutex
	verdicts   map[string]Verdict
	recorded   []RecordedClause
	residue    []ResidueEntry
	extensions []RecordedClause
}

// Auto-propose errors.
var (
	ErrResidueRequiredForSkip = errors.New("residue-required-for-skip")
	ErrUnknownClauseID        = errors.New("unknown-clause-id")
	ErrVerdictAlreadyApplied  = errors.New("verdict-already-applied")
	ErrModifiedArgsMissing    = errors.New("modified-args-missing")
	ErrExtensionInvalid       = errors.New("extension-clause-invalid")
	ErrProposalEmpty          = errors.New("proposal-zero-clauses")
	ErrDuplicateClauseID      = errors.New("proposal-duplicate-clause-id")
	ErrClauseArgsIncomplete   = errors.New("clause-required-args-missing")
)

// NewArrowProposal constructs an ArrowProposal with the given framing
// and proposed clauses. Used for direct programmatic construction
// (tests, integrators that build proposals outside the role-file
// path). For the role-file path, use BuildProposal.
func NewArrowProposal(upstream, downstream, context string, proposed []ProposedClause) *ArrowProposal {
	return &ArrowProposal{
		Upstream:   upstream,
		Downstream: downstream,
		Context:    context,
		Proposed:   append([]ProposedClause(nil), proposed...),
		verdicts:   make(map[string]Verdict, len(proposed)),
	}
}

// BuildProposal expands a role file into per-(role-pair, context) arrow
// proposals. For each machine clause, DefaultArgs are pulled from the
// catalogue concept's schema (arg defaults); DefaultCost is the concept's
// DefaultCost.
//
// upstream / downstream are role names that frame the arrow (e.g.,
// "analyst" → "architect"); context is the bounded-context id.
//
// Returns an error if any role clause references a concept missing
// from the catalogue (a load-time error the catalogue itself should
// have caught, but defended here as well).
func BuildProposal(rf *RoleFile, cat *catalogue.Catalogue, upstream, downstream, context string) (*ArrowProposal, error) {
	if rf == nil {
		return nil, errors.New("BuildProposal: nil RoleFile")
	}
	if cat == nil {
		return nil, errors.New("BuildProposal: nil Catalogue")
	}

	proposed := make([]ProposedClause, 0, len(rf.Clauses))
	seenIDs := make(map[string]struct{}, len(rf.Clauses))
	for _, rc := range rf.Clauses {
		// validation-pass-2 F4: refuse duplicate clause IDs at
		// BuildProposal time. If the role file has duplicates,
		// AllVerdictsReceived's counting check is fragile.
		if _, dup := seenIDs[rc.ID]; dup {
			return nil, fmt.Errorf("%w: %s in role %q", ErrDuplicateClauseID, rc.ID, rf.Role)
		}
		seenIDs[rc.ID] = struct{}{}
		p := ProposedClause{
			ID:          rc.ID,
			Description: rc.ClauseText,
			EvalType:    rc.EvalType,
			DepthType:   rc.DepthType,
			RoleSource:  rf.Role,
		}
		if rc.IsMachine() {
			concept, ok := cat.Get(rc.ConceptName)
			if !ok {
				return nil, fmt.Errorf("BuildProposal: clause %s references unknown concept %q", rc.ID, rc.ConceptName)
			}
			p.ConceptName = rc.ConceptName
			p.DefaultArgs = extractDefaultArgs(concept)
			p.DefaultCost = concept.DefaultCost
			p.RoleArgsHint = rc.ConceptArgsRaw
		}
		proposed = append(proposed, p)
	}
	// validation-pass-2 F26: a zero-clause exit gate trivially
	// satisfies AllVerdictsReceived, recording an arrow with no exit
	// conditions. Refuse outright at construction time.
	if len(proposed) == 0 {
		return nil, fmt.Errorf("%w: role %q has no exit-gate clauses", ErrProposalEmpty, rf.Role)
	}

	return &ArrowProposal{
		Upstream:   upstream,
		Downstream: downstream,
		Context:    context,
		Proposed:   proposed,
		verdicts:   make(map[string]Verdict, len(proposed)),
	}, nil
}

// extractDefaultArgs returns a map of arg-name → default-value for
// every argument in the concept that declares a non-nil default.
// Required args without defaults are not included; the operator (or
// the role-args hint) is expected to supply them.
//
// Values are deep-copied (validation-pass-2 F47) so that slice/map
// defaults are not aliased across proposed clauses — a Modify
// mutating an arg's slice in place would otherwise leak into other
// proposals and into the catalogue's in-memory schema.
func extractDefaultArgs(c catalogue.Concept) map[string]any {
	out := make(map[string]any)
	for name, schema := range c.Arguments {
		if schema.Default != nil {
			out[name] = deepCopyValue(schema.Default)
		}
	}
	return out
}

// deepCopyValue returns a structural copy of v. Slices and maps are
// recursively copied; scalars (strings, numbers, bools) are returned
// as-is. Any other type (channels, funcs, pointers) is returned
// as-is — catalogue schema defaults should not contain such types,
// and accidentally introducing one is a separate bug.
func deepCopyValue(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = deepCopyValue(item)
		}
		return out
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = deepCopyValue(item)
		}
		return out
	}
	return v
}

// Apply records the operator's verdict for the named clause.
//
// Verdict semantics (validation-pass-2 F3):
//   - Confirm: the clause is recorded with its proposed default args.
//     Required args without defaults must already be in
//     DefaultArgs; otherwise ErrClauseArgsIncomplete.
//   - Modify:  ModifiedArgs is checked against the proposed defaults
//     via CheckModification (raise-only enforced); on success
//     the clause is recorded with merged args. After merge,
//     required args must all be present.
//   - Skip:    Residue must be non-empty; the clause is added to the
//     residue list and NOT to the exit gate.
//
// Extend is NOT a verdict — see the Extend method for adding clauses
// beyond the role file.
//
// Returns:
//   - ErrUnknownClauseID if no proposed clause has the given ID.
//   - ErrVerdictAlreadyApplied if a verdict was already recorded for
//     the clause (callers should not re-apply).
//   - ErrResidueRequiredForSkip if Kind==VerdictSkip and Residue=="".
//   - ErrModifiedArgsMissing if Kind==VerdictModify and ModifiedArgs==nil.
//   - ErrClauseArgsIncomplete if the recorded args don't satisfy the
//     concept's required-arg schema (machine clauses only).
//   - Whatever CheckModification returns for a refused modify (wraps
//     ErrModifyWeakening, ErrModifyNonFinite, ErrModifyRegexUnsupported,
//     etc.).
func (a *ArrowProposal) Apply(clauseID string, v Verdict, cat *catalogue.Catalogue) error {
	if a == nil {
		return errors.New("Apply: nil ArrowProposal")
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i, p := range a.Proposed {
		if p.ID == clauseID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: %s", ErrUnknownClauseID, clauseID)
	}
	if _, exists := a.verdicts[clauseID]; exists {
		return fmt.Errorf("%w: %s", ErrVerdictAlreadyApplied, clauseID)
	}
	proposed := a.Proposed[idx]

	switch v.Kind {
	case VerdictConfirm:
		args := cloneArgs(proposed.DefaultArgs)
		if err := validateClauseArgs(proposed, args, cat); err != nil {
			return err
		}
		a.recorded = append(a.recorded, RecordedClause{
			ID:          proposed.ID,
			Description: proposed.Description,
			EvalType:    proposed.EvalType,
			DepthType:   proposed.DepthType,
			ConceptName: proposed.ConceptName,
			Args:        args,
			Cost:        proposed.DefaultCost,
			Source:      SourceRoleDefault,
		})

	case VerdictModify:
		if v.ModifiedArgs == nil {
			return fmt.Errorf("%w: clause %s", ErrModifiedArgsMissing, clauseID)
		}
		// Attested clauses have no schema to modify against; reject.
		if proposed.IsAttested() {
			return fmt.Errorf("modify on attested clause %s is not meaningful (no schema)", clauseID)
		}
		if err := CheckModification(proposed.ConceptName, proposed.DefaultArgs, v.ModifiedArgs, cat); err != nil {
			return err
		}
		merged := mergeArgs(proposed.DefaultArgs, v.ModifiedArgs)
		if err := validateClauseArgs(proposed, merged, cat); err != nil {
			return err
		}
		a.recorded = append(a.recorded, RecordedClause{
			ID:          proposed.ID,
			Description: proposed.Description,
			EvalType:    proposed.EvalType,
			DepthType:   proposed.DepthType,
			ConceptName: proposed.ConceptName,
			Args:        merged,
			Cost:        proposed.DefaultCost,
			Source:      SourceRoleDefault,
		})

	case VerdictSkip:
		if v.Residue == "" {
			return fmt.Errorf("%w: clause %s", ErrResidueRequiredForSkip, clauseID)
		}
		a.residue = append(a.residue, ResidueEntry{
			ClauseID:     proposed.ID,
			Description:  proposed.Description,
			ConceptName:  proposed.ConceptName,
			RoleArgsHint: proposed.RoleArgsHint,
			Reason:       v.Residue,
		})

	default:
		return fmt.Errorf("Apply: unknown verdict kind %d", v.Kind)
	}

	a.verdicts[clauseID] = v
	return nil
}

// validateClauseArgs reports whether the recorded args satisfy the
// concept's required-arg schema. Attested clauses have no schema and
// always validate. Returns ErrClauseArgsIncomplete naming the missing
// arg(s) on failure. Per validation-pass-2 F29.
func validateClauseArgs(p ProposedClause, args map[string]any, cat *catalogue.Catalogue) error {
	if p.IsAttested() {
		return nil
	}
	if cat == nil {
		return errors.New("validateClauseArgs: nil catalogue")
	}
	concept, ok := cat.Get(p.ConceptName)
	if !ok {
		return fmt.Errorf("validateClauseArgs: concept %q not in catalogue", p.ConceptName)
	}
	var missing []string
	for argName, schema := range concept.Arguments {
		if !schema.Required {
			continue
		}
		if _, present := args[argName]; !present {
			missing = append(missing, argName)
		}
	}
	if len(missing) > 0 {
		// Post-prod-readiness adversarial L-C: sort both the
		// missing-arg list and the observed-arg list so the
		// formatted reason is byte-identical across runs. Go map
		// iteration is randomized per-run; without these sorts the
		// audit-facing residue field flapped between invocations
		// on the same input.
		sort.Strings(missing)
		return fmt.Errorf("%w: %s requires args %v (got %v)",
			ErrClauseArgsIncomplete, p.ConceptName, missing, mapKeys(args))
	}
	return nil
}

// mapKeys returns the keys of m as a sorted slice for error
// reporting. Post-prod-readiness adversarial L-C: sorted output so
// the residue reason text recorded for ErrClauseArgsIncomplete is
// byte-identical across runs (Go map iteration order is randomized
// per-run, which previously made the audit-facing residue strings
// flap between invocations on the same input).
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Extend adds an operator-supplied clause to the arrow's exit gate
// beyond what the role file proposes. The new clause must declare a
// non-empty ID, a valid eval type, and (for machine eval) a catalogue
// concept name.
//
// Extend is intentionally NOT a verdict on a Proposed clause: the
// operator may invoke Extend at any point during the auto-propose
// flow, independently of the per-clause verdict sequence. ADR-011
// §B.2 verdict #3.
//
// Returns ErrExtensionInvalid if the extension is missing required
// fields, references an unknown concept, or duplicates a recorded
// clause's ID.
func (a *ArrowProposal) Extend(ext ProposedClause, cat *catalogue.Catalogue) error {
	if a == nil {
		return errors.New("Extend: nil ArrowProposal")
	}
	if ext.ID == "" {
		return fmt.Errorf("%w: extension has empty ID", ErrExtensionInvalid)
	}
	if ext.EvalType != "machine" && ext.EvalType != "attested" {
		return fmt.Errorf("%w: EvalType %q not in {machine, attested}", ErrExtensionInvalid, ext.EvalType)
	}
	if ext.DepthType != "depth-robust" && ext.DepthType != "depth-sensitive" {
		return fmt.Errorf("%w: DepthType %q not in {depth-robust, depth-sensitive}", ErrExtensionInvalid, ext.DepthType)
	}
	cost := ext.DefaultCost
	if ext.IsMachine() {
		if ext.ConceptName == "" {
			return fmt.Errorf("%w: machine extension missing ConceptName", ErrExtensionInvalid)
		}
		concept, ok := cat.Get(ext.ConceptName)
		if !ok {
			return fmt.Errorf("%w: concept %q not in catalogue", ErrExtensionInvalid, ext.ConceptName)
		}
		if cost == 0 {
			cost = concept.DefaultCost
		}
	}
	args := cloneArgs(ext.DefaultArgs)
	if err := validateClauseArgs(ext, args, cat); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Refuse duplicate clause IDs across the gate (proposed + recorded).
	for _, r := range a.recorded {
		if r.ID == ext.ID {
			return fmt.Errorf("%w: clause ID %q already recorded", ErrExtensionInvalid, ext.ID)
		}
	}
	for _, p := range a.Proposed {
		if p.ID == ext.ID {
			return fmt.Errorf("%w: clause ID %q collides with a proposed clause", ErrExtensionInvalid, ext.ID)
		}
	}
	rc := RecordedClause{
		ID:          ext.ID,
		Description: ext.Description,
		EvalType:    ext.EvalType,
		DepthType:   ext.DepthType,
		ConceptName: ext.ConceptName,
		Args:        args,
		Cost:        cost,
		Source:      SourceOperatorExtension,
	}
	a.recorded = append(a.recorded, rc)
	a.extensions = append(a.extensions, rc)
	return nil
}

// Recorded returns the clauses confirmed / modified / extended into
// the arrow's exit gate, in application order.
func (a *ArrowProposal) Recorded() []RecordedClause {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]RecordedClause, len(a.recorded))
	copy(out, a.recorded)
	return out
}

// Residue returns the skip-with-reason entries, in application order.
func (a *ArrowProposal) Residue() []ResidueEntry {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ResidueEntry, len(a.residue))
	copy(out, a.residue)
	return out
}

// Extensions returns just the operator-extended clauses (subset of
// Recorded). Useful for reports that distinguish role-default vs
// operator-added.
func (a *ArrowProposal) Extensions() []RecordedClause {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]RecordedClause, len(a.extensions))
	copy(out, a.extensions)
	return out
}

// VerdictFor returns the verdict applied to the named proposed clause,
// or (Verdict{}, false) if no verdict has been applied yet.
func (a *ArrowProposal) VerdictFor(clauseID string) (Verdict, bool) {
	if a == nil {
		return Verdict{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.verdicts[clauseID]
	return v, ok
}

// AllVerdictsReceived reports whether every proposed clause has had
// a verdict applied. Per ADR-011 step 3, the grid must not be
// recorded until this returns true.
//
// validation-pass-2 F4: iterates Proposed and checks each ID is in
// the verdicts map rather than relying on count equality, so a
// duplicate-ID role file (now refused at BuildProposal time per F4
// fix) or a future code path that adds Proposed entries after
// construction can't fool the check by happenstance.
func (a *ArrowProposal) AllVerdictsReceived() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.Proposed) == 0 {
		return false
	}
	for _, p := range a.Proposed {
		if _, ok := a.verdicts[p.ID]; !ok {
			return false
		}
	}
	return true
}

// cloneArgs returns a deep copy of m. Returns nil for a nil input.
// Deep copy so slice/map values are not aliased — see validation-
// pass-2 F47. A Modify that mutates a slice arg in place would
// otherwise leak into other proposals (and into the catalogue's
// in-memory schema default).
func cloneArgs(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

// mergeArgs returns a new map containing every key from base, overridden
// by any key present in diff. Inputs are not mutated; diff values are
// deep-copied so the caller's map can be safely reused or freed.
func mergeArgs(base, diff map[string]any) map[string]any {
	out := cloneArgs(base)
	if out == nil {
		out = make(map[string]any, len(diff))
	}
	for k, v := range diff {
		out[k] = deepCopyValue(v)
	}
	return out
}
