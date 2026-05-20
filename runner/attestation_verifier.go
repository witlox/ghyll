package runner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// AttestationVerifier walks an `.ghyll/attestations.jsonl` audit
// file (or any io.Reader emitting one JSON object per line) and
// validates each record against the schema invariants from
// ADR-009 / ADR-010:
//
//   - Required fields present: attestation_id, kind, arrow_id,
//     op_id, attested_by_role, verdict, timestamp, grid_version.
//   - kind ∈ {depth-type, on-the-spot}.
//   - clause_id presence matches kind (depth-type requires; on-
//     the-spot forbids).
//   - verdict ∈ {pass, fail, insufficient-basis}.
//   - §12.2 self-cert: attested_by_role MUST NOT equal source_role
//     or target_role when those are recorded.
//   - timestamp > 0.
//
// Verifier output is per-record so the operator can pinpoint the
// failing line without re-walking the file. Used by a future
// `ghyll attestations verify` CLI subcommand and by the test
// harness that round-trips Record -> JSONL -> Verify.
type AttestationVerifier struct{}

// VerifyIssue describes one validation failure.
type VerifyIssue struct {
	Line   int    // 1-based line number in the input
	Record string // the raw line content (truncated for log safety)
	Reason string
}

// VerifyResult bundles the per-file summary.
type VerifyResult struct {
	Lines  int // total non-blank lines read
	OK     int // lines that validated cleanly
	Failed int // lines that produced one or more issues
	Issues []VerifyIssue
}

// VerifyFile opens path and runs Verify against it. Returns
// (zero, error) if path can't be opened. A clean file returns a
// VerifyResult with Failed=0.
func (v *AttestationVerifier) VerifyFile(path string) (VerifyResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("attestation-verify: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return v.Verify(f)
}

// Verify reads JSONL records line-by-line, validating each.
// Blank lines and lines starting with `#` are skipped. The
// caller's reader is consumed to EOF.
//
// Verify does not stop at the first failure — operators want a
// full audit summary, not an interruption. Every line is checked
// and the issues are collected in order.
func (v *AttestationVerifier) Verify(r io.Reader) (VerifyResult, error) {
	res := VerifyResult{}
	scanner := bufio.NewScanner(r)
	// Bump the per-line buffer to handle large reason fields.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		res.Lines++
		ok, issues := v.checkLine(lineNo, trimmed)
		if ok {
			res.OK++
		} else {
			res.Failed++
			res.Issues = append(res.Issues, issues...)
		}
	}
	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("attestation-verify: scan: %w", err)
	}
	return res, nil
}

// checkLine parses one line and returns (ok, issues). A line with
// multiple problems may produce multiple issues; callers see the
// full picture per line.
func (v *AttestationVerifier) checkLine(lineNo int, raw string) (bool, []VerifyIssue) {
	var rec jsonlRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return false, []VerifyIssue{{
			Line:   lineNo,
			Record: truncateForVerify(raw, 200),
			Reason: fmt.Sprintf("json-unmarshal: %v", err),
		}}
	}
	var issues []VerifyIssue
	add := func(reason string) {
		issues = append(issues, VerifyIssue{
			Line:   lineNo,
			Record: truncateForVerify(raw, 200),
			Reason: reason,
		})
	}

	if strings.TrimSpace(rec.ID) == "" {
		add("attestation_id is empty")
	}
	if strings.TrimSpace(rec.ArrowID) == "" {
		add("arrow_id is empty")
	}
	if strings.TrimSpace(rec.OpID) == "" {
		add("op_id is empty")
	}
	if strings.TrimSpace(rec.AttestedByRole) == "" {
		add("attested_by_role is empty")
	}
	if rec.Timestamp <= 0 {
		add(fmt.Sprintf("timestamp must be > 0; got %d", rec.Timestamp))
	}

	kind := AttestationKind(rec.Kind)
	switch kind {
	case AttestationKindDepthType:
		if strings.TrimSpace(rec.ClauseID) == "" {
			add("depth-type kind requires clause_id")
		}
	case AttestationKindOnTheSpot:
		if strings.TrimSpace(rec.ClauseID) != "" {
			add("on-the-spot kind must not carry clause_id")
		}
	default:
		add(fmt.Sprintf("kind %q not in {depth-type, on-the-spot}", rec.Kind))
	}

	switch AttestationVerdict(rec.Verdict) {
	case AttestationPass, AttestationFail, AttestationInsufficientBasis:
		// OK
	default:
		add(fmt.Sprintf("verdict %q not in {pass, fail, insufficient-basis}", rec.Verdict))
	}

	// §12.2 self-cert. Only fires when source/target are recorded
	// (older JSONL rows may have empty strings — those skip the
	// check, same semantic as the engine schema CHECK).
	att := strings.TrimSpace(rec.AttestedByRole)
	src := strings.TrimSpace(rec.SourceRole)
	tgt := strings.TrimSpace(rec.TargetRole)
	if src != "" && strings.EqualFold(att, src) {
		add(fmt.Sprintf("self-cert: attested_by_role %q equals source_role", att))
	}
	if tgt != "" && strings.EqualFold(att, tgt) {
		add(fmt.Sprintf("self-cert: attested_by_role %q equals target_role", att))
	}

	// Tier 2 self-cert extension (gate-1 F-3): adversary_role
	// MUST NOT equal source/target. Empty adversary_role skips —
	// only adversary-phase records carry it.
	adv := strings.TrimSpace(rec.AdversaryRole)
	if adv != "" {
		if src != "" && strings.EqualFold(adv, src) {
			add(fmt.Sprintf("self-cert: adversary_role %q equals source_role", adv))
		}
		if tgt != "" && strings.EqualFold(adv, tgt) {
			add(fmt.Sprintf("self-cert: adversary_role %q equals target_role", adv))
		}
		if strings.Contains(adv, "__") {
			add(`adversary_role must not contain "__" (reserved separator)`)
		}
	}

	// Tier 2 per-unit payload validation (gate-1 F-25). Empty
	// unit field tolerated (pre-Tier-2 rows); when present, the
	// payload JSON MUST parse to a valid shape for the unit.
	if rec.Unit != "" {
		switch VerdictUnit(rec.Unit) {
		case VerdictUnitConfirm, VerdictUnitRecordLocationsInspected, VerdictUnitWriteResidueNote:
			// OK
		default:
			add(fmt.Sprintf("unit %q not in {confirm, record-locations-inspected, write-residue-note}", rec.Unit))
		}
		if rec.UnitPayloadJSON != "" {
			var payload VerdictUnitPayload
			if err := json.Unmarshal([]byte(rec.UnitPayloadJSON), &payload); err != nil {
				add(fmt.Sprintf("unit_payload_json malformed: %v", err))
			} else if pverr := ValidateUnitPayload(VerdictUnit(rec.Unit), payload, 0); pverr != nil {
				add(fmt.Sprintf("unit_payload_json fails ValidateUnitPayload: %v", pverr))
			}
		}
	}

	// Tier 2 hint_json must parse as a JSON object when present
	// (gate-1 F-25). Empty or "{}" both accepted.
	if rec.HintJSON != "" && rec.HintJSON != "{}" {
		var anyJSON map[string]any
		if err := json.Unmarshal([]byte(rec.HintJSON), &anyJSON); err != nil {
			add(fmt.Sprintf("hint_json malformed: %v", err))
		}
	}

	if len(issues) == 0 {
		return true, nil
	}
	return false, issues
}

// truncateForVerify caps the record snippet so a corrupted line
// doesn't blow up operator output.
func truncateForVerify(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// String renders a VerifyResult as a human-readable summary.
func (r VerifyResult) String() string {
	if r.Failed == 0 {
		return fmt.Sprintf("attestation-verify: %d/%d records OK", r.OK, r.Lines)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "attestation-verify: %d/%d records OK, %d failed\n",
		r.OK, r.Lines, r.Failed)
	for _, i := range r.Issues {
		fmt.Fprintf(&b, "  line %d: %s\n", i.Line, i.Reason)
	}
	return strings.TrimRight(b.String(), "\n")
}

// ErrVerifyFileEmpty signals a verify against an empty (or
// header-only) file. Operators triaging an "empty audit trail"
// see a typed error instead of a meaningless OK=0/Failed=0.
var ErrVerifyFileEmpty = errors.New("attestation-verify: file has no records")

// AggregateResult is the per-ID consistency report from
// VerifyAggregateConsistency.
type AggregateResult struct {
	FlatLoaded    int
	TreeLoaded    int
	OnlyInFlat    []string // attestation IDs present in flat but not tree
	OnlyInTree    []string // attestation IDs present in tree but not flat
	DivergentByID []string // attestation IDs present in both but with non-equal records
}

// VerifyAggregateConsistency loads both the flat JSONL audit file
// and the tree-structured per-pass JSONL collection, then reports
// ID-level divergence between the two surfaces (gate-1 F-12: the
// tree is the primary writer; flat is a forward-only Observer).
// Any divergence is a wire bug, not normal drift.
//
// Returns ErrAttestationAggregateDivergence wrapping the diff
// summary when there is at least one OnlyInFlat / OnlyInTree /
// DivergentByID. Nil if both surfaces agree.
//
// Either path may be missing (fresh project) — VerifyAggregate-
// Consistency tolerates that case and returns a zero AggregateResult
// + nil error.
func (v *AttestationVerifier) VerifyAggregateConsistency(flatPath, treeRoot string) (AggregateResult, error) {
	res := AggregateResult{}
	flatStore := NewAttestationStore()
	if flatPath != "" {
		loaded, _, lerr := flatStore.LoadFromJSONL(flatPath, false)
		if lerr != nil && !errors.Is(lerr, os.ErrNotExist) {
			return res, fmt.Errorf("verify-aggregate: load flat: %w", lerr)
		}
		res.FlatLoaded = loaded
	}
	treeStore := NewAttestationStore()
	if treeRoot != "" {
		loaded, _, terr := treeStore.LoadFromTree(treeRoot, false)
		if terr != nil && !errors.Is(terr, os.ErrNotExist) {
			return res, fmt.Errorf("verify-aggregate: load tree: %w", terr)
		}
		res.TreeLoaded = loaded
	}
	// Walk each store to build ID sets.
	flatByID := map[string]AttestationRecord{}
	for _, rec := range flatStore.All() {
		flatByID[rec.ID] = rec
	}
	treeByID := map[string]AttestationRecord{}
	for _, rec := range treeStore.All() {
		treeByID[rec.ID] = rec
	}
	for id, flatRec := range flatByID {
		treeRec, ok := treeByID[id]
		if !ok {
			res.OnlyInFlat = append(res.OnlyInFlat, id)
			continue
		}
		if !AttestationRecordsEqual(flatRec, treeRec) {
			res.DivergentByID = append(res.DivergentByID, id)
		}
	}
	for id := range treeByID {
		if _, ok := flatByID[id]; !ok {
			res.OnlyInTree = append(res.OnlyInTree, id)
		}
	}
	if len(res.OnlyInFlat)+len(res.OnlyInTree)+len(res.DivergentByID) > 0 {
		return res, fmt.Errorf("%w: only-in-flat=%d only-in-tree=%d divergent=%d",
			ErrAttestationAggregateDivergence,
			len(res.OnlyInFlat), len(res.OnlyInTree), len(res.DivergentByID))
	}
	return res, nil
}
