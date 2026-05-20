package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ghyll "github.com/witlox/ghyll"
)

// Role file parsing.
//
// A role file (specs/architecture/roles/<role>.md) declares the role's
// exit-gate clauses in a markdown table whose header is:
//
//   | # | Clause | Concept (machine) or attested judgement | Eval | Depth |
//
// Each subsequent row describes one clause:
//
//   | G1 | <clause text> | `concept-name`(<args>) | machine | depth-robust |
//   | G7 | <clause text> | (judgement)            | attested | depth-sensitive |
//
// ParseRoleFile loads a file and returns the typed clause list.

// RoleFile is a parsed role markdown file.
type RoleFile struct {
	Role    string       // role name (e.g., "analyst"), derived from filename
	Path    string       // absolute or relative path used to load
	Clauses []RoleClause // exit-gate clauses in source order
}

// RoleClause is one row from a role's exit-gate table.
//
// For machine clauses, ConceptName is the catalogue concept name
// (e.g., "unique-definition") and ConceptArgsRaw is the literal text
// between the parentheses (e.g., "`ubiquitous-language.md`"). The raw
// string is preserved verbatim; auto-propose maps it to concept
// argument names in a later phase.
//
// For attested clauses, ConceptName is empty and ConceptArgsRaw is
// empty (the cell reads "(judgement)").
type RoleClause struct {
	ID             string // e.g., "G1"
	ClauseText     string // human description (column 2)
	ConceptName    string // machine concept; "" for attested
	ConceptArgsRaw string // literal text inside (...) for machine; "" for attested
	EvalType       string // "machine" or "attested"
	DepthType      string // "depth-robust" or "depth-sensitive"
}

// IsMachine reports whether the clause's evaluator is the harness
// (concept name is set, eval column is "machine").
func (c RoleClause) IsMachine() bool {
	return c.EvalType == "machine"
}

// IsAttested reports whether the clause requires an attested judgement
// (no concept name, eval column is "attested").
func (c RoleClause) IsAttested() bool {
	return c.EvalType == "attested"
}

// Role-file parsing errors.
var (
	ErrRoleFileNoTable     = errors.New("role-file-no-exit-gate-table")
	ErrRoleClauseMalformed = errors.New("role-clause-malformed")
	// ErrRoleNameUnknown is returned by ParseRoleFileEmbedded when the
	// requested role name isn't one of the four canonical ADR-008 roles.
	ErrRoleNameUnknown = errors.New("role-name-unknown")
)

// expectedHeader is the canonical column header for a role's
// exit-gate table. Match is whitespace-tolerant but case-sensitive on
// the column names (the markdown spec uses Title Case).
const expectedHeader = "| # | Clause | Concept (machine) or attested judgement | Eval | Depth |"

// ParseRoleFile reads path and returns the parsed role file. The Role
// name is derived from the filename (e.g., analyst.md → "analyst").
//
// Use this entry point only when the caller has a real filesystem
// path (custom role overrides during development, test fixtures with
// bespoke clauses, etc.). For loading the four canonical role
// contracts that ship inside the binary, prefer
// ParseRoleFileEmbedded.
func ParseRoleFile(path string) (*RoleFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFile: read %q: %w", path, err)
	}
	rf, err := parseRoleFileBytes(data, path)
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFile: %q: %w", path, err)
	}
	return rf, nil
}

// ParseRoleFileEmbedded loads one of the four canonical role
// contracts (analyst, architect, implementer, integrator) from the
// embedded FS in the repo-root `ghyll` package and returns the parsed
// RoleFile.
//
// Unlike ParseRoleFile, this works inside a released binary where
// specs/architecture/roles/ is no longer present on disk. This is the
// path that `ghyll init` and other production-time consumers must use
// (integrator finding H-2).
//
// roleName must be one of: "analyst", "architect", "implementer",
// "integrator". Any other value returns an error referencing
// ErrRoleNameUnknown so callers can distinguish "no such role" from
// "role parse failed".
func ParseRoleFileEmbedded(roleName string) (*RoleFile, error) {
	switch roleName {
	case "analyst", "architect", "implementer", "integrator":
	default:
		return nil, fmt.Errorf("ParseRoleFileEmbedded: %w: %q", ErrRoleNameUnknown, roleName)
	}
	embeddedPath := "specs/architecture/roles/" + roleName + ".md"
	data, err := ghyll.RolesFS.ReadFile(embeddedPath)
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFileEmbedded: read embedded %q: %w", embeddedPath, err)
	}
	rf, err := parseRoleFileBytes(data, embeddedPath)
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFileEmbedded: %q: %w", embeddedPath, err)
	}
	return rf, nil
}

// parseRoleFileBytes is the shared parser body for ParseRoleFile and
// ParseRoleFileEmbedded. label is the source identifier used to
// populate RoleFile.Path and to derive the role name from its
// basename (e.g., "specs/architecture/roles/analyst.md" → "analyst").
func parseRoleFileBytes(data []byte, label string) (*RoleFile, error) {
	clauses, err := parseExitGateTable(string(data))
	if err != nil {
		return nil, err
	}
	// Validation-pass-2 F25: use filepath.Base (handles Windows
	// separators and special path forms) rather than a hand-rolled
	// scan on '/' and '\'.
	role := strings.TrimSuffix(filepath.Base(label), ".md")
	return &RoleFile{
		Role:    role,
		Path:    label,
		Clauses: clauses,
	}, nil
}

// parseExitGateTable finds the canonical exit-gate header and parses
// every subsequent table row until a non-table line.
//
// Returns ErrRoleFileNoTable if the header isn't found.
func parseExitGateTable(content string) ([]RoleClause, error) {
	lines := strings.Split(content, "\n")
	headerLine := -1
	normalizedExpected := normalizeHeader(expectedHeader)
	for i, line := range lines {
		if normalizeHeader(line) == normalizedExpected {
			headerLine = i
			break
		}
	}
	if headerLine == -1 {
		return nil, ErrRoleFileNoTable
	}
	// Skip header + separator (`|---|---|...`).
	if headerLine+2 >= len(lines) {
		return nil, ErrRoleFileNoTable
	}
	// Validation-pass-2 F48: verify the row at headerLine+1 actually
	// looks like a separator. Without this, a missing separator
	// silently drops the first data row (the parser treats it as the
	// separator).
	separator := strings.TrimSpace(lines[headerLine+1])
	if !markdownSeparatorRE.MatchString(separator) {
		return nil, fmt.Errorf("%w: expected separator after header (got %q)",
			ErrRoleFileNoTable, separator)
	}
	var clauses []RoleClause
	for i := headerLine + 2; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || !strings.HasPrefix(line, "|") {
			break
		}
		clause, err := parseRoleClauseRow(line)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}
		clauses = append(clauses, clause)
	}
	return clauses, nil
}

// normalizeHeader collapses internal whitespace so the parser tolerates
// extra spaces in the table header without rejecting valid files.
func normalizeHeader(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// parseRoleClauseRow parses one markdown table row into a RoleClause.
// Expects exactly 5 logical cells (between leading and trailing `|`).
//
// Validation-pass-2 F21: pipes inside backtick-quoted spans and
// pipes escaped as `\|` are treated as literal cell content, not
// cell separators. This lets clause descriptions or arg hints
// contain `|` (e.g., a regex alternation in an args parenthetical).
func parseRoleClauseRow(line string) (RoleClause, error) {
	// Strip leading/trailing pipes.
	inner := strings.TrimPrefix(line, "|")
	inner = strings.TrimSuffix(inner, "|")
	cells := splitMarkdownCells(inner)
	if len(cells) != 5 {
		return RoleClause{}, fmt.Errorf("%w: expected 5 cells, got %d (escape literal | as \\| or wrap in backticks)",
			ErrRoleClauseMalformed, len(cells))
	}
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}

	conceptCell := cells[2]
	conceptName, argsRaw, err := parseConceptCell(conceptCell)
	if err != nil {
		return RoleClause{}, fmt.Errorf("%w: concept cell %q: %w", ErrRoleClauseMalformed, conceptCell, err)
	}

	evalType := cells[3]
	if evalType != "machine" && evalType != "attested" {
		return RoleClause{}, fmt.Errorf("%w: eval %q not in {machine, attested}", ErrRoleClauseMalformed, evalType)
	}
	if evalType == "attested" && conceptName != "" {
		return RoleClause{}, fmt.Errorf("%w: attested clause must use (judgement); got concept %q", ErrRoleClauseMalformed, conceptName)
	}
	if evalType == "machine" && conceptName == "" {
		return RoleClause{}, fmt.Errorf("%w: machine clause must name a concept; got (judgement)", ErrRoleClauseMalformed)
	}

	depthType := cells[4]
	if depthType != "depth-robust" && depthType != "depth-sensitive" {
		return RoleClause{}, fmt.Errorf("%w: depth %q not in {depth-robust, depth-sensitive}", ErrRoleClauseMalformed, depthType)
	}

	return RoleClause{
		ID:             cells[0],
		ClauseText:     cells[1],
		ConceptName:    conceptName,
		ConceptArgsRaw: argsRaw,
		EvalType:       evalType,
		DepthType:      depthType,
	}, nil
}

// splitMarkdownCells splits a markdown table row's inner content on
// `|`, but respects backtick spans and `\|` escapes — pipes inside
// those are kept as literal content. Validation-pass-2 F21.
func splitMarkdownCells(inner string) []string {
	var out []string
	var cur strings.Builder
	inBacktick := false
	for i := 0; i < len(inner); i++ {
		b := inner[i]
		// Handle escaped pipe regardless of backtick state.
		if b == '\\' && i+1 < len(inner) && inner[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if b == '`' {
			inBacktick = !inBacktick
			cur.WriteByte(b)
			continue
		}
		if b == '|' && !inBacktick {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(b)
	}
	out = append(out, cur.String())
	return out
}

// markdownSeparatorRE matches a markdown table separator row like
// `|---|---|---|---|---|` (with optional whitespace and alignment
// colons). Used to verify the row after the table header actually
// is a separator (validation-pass-2 F48).
var markdownSeparatorRE = regexp.MustCompile(`^\|(\s*:?-+:?\s*\|)+$`)

// conceptCallRE matches `concept-name`(args). Supports backticked
// concept names and arbitrary characters (incl. backticks, unicode
// arrows) inside the parentheses. The args group is non-greedy and
// captures everything up to the closing paren.
//
// Examples it matches:
//
//	`unique-definition`(`ubiquitous-language.md`)
//	`arrow-artifact-present`(analyst→architect coverage-claim)
//	`no-orphan-symbol`(exported-behaviours)
var conceptCallRE = regexp.MustCompile("^`([a-z][a-z0-9-]*)`\\((.*)\\)$")

// parseConceptCell distinguishes machine concept calls from attested
// judgements.
//
// Returns (conceptName, argsRaw, nil) for machine clauses, where
// argsRaw is the literal string inside the outermost parens (may be
// empty for arg-less concepts).
//
// Returns ("", "", nil) for attested clauses (cell reads "(judgement)").
//
// Validation-pass-2 F22: case-insensitive on "(judgement)" and
// tolerates internal whitespace ("(Judgement)", "( judgement )" all
// accepted).
//
// Validation-pass-2 F49: strips zero-width characters (ZWSP, ZWNJ,
// ZWJ, BOM, WJ) and bidi controls before matching so a stray pasted
// invisible char doesn't defeat the regex with no diagnostic.
//
// Returns an error if the cell doesn't match either form.
func parseConceptCell(cell string) (string, string, error) {
	clean := stripZeroWidthAndBidi(cell)
	// Case-insensitive attested check (F22).
	if isAttestedJudgement(clean) {
		return "", "", nil
	}
	m := conceptCallRE.FindStringSubmatch(clean)
	if m == nil {
		return "", "", fmt.Errorf("not a recognized concept call or (judgement)")
	}
	return m[1], m[2], nil
}

// isAttestedJudgement reports whether cell reads "(judgement)" with
// case-insensitive matching and tolerance for whitespace inside the
// parens.
func isAttestedJudgement(cell string) bool {
	cell = strings.TrimSpace(cell)
	if !strings.HasPrefix(cell, "(") || !strings.HasSuffix(cell, ")") {
		return false
	}
	inner := strings.TrimSpace(cell[1 : len(cell)-1])
	return strings.EqualFold(inner, "judgement")
}

// stripZeroWidthAndBidi removes characters that are invisible but
// could disrupt cell-level pattern matching. Mirrors the set
// rejected by op-id validation (session.go) for consistency.
func stripZeroWidthAndBidi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 0x200B, 0x200C, 0x200D, // ZWSP / ZWNJ / ZWJ
			0x200E, 0x200F, // LRM / RLM
			0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // LRE/RLE/PDF/LRO/RLO
			0x2060,                         // WJ
			0x2066, 0x2067, 0x2068, 0x2069, // LRI/RLI/FSI/PDI
			0xFEFF: // BOM
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
