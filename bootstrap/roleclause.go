package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Role file parsing.
//
// A role file (specs/direction/roles/<role>.md) declares the role's
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
)

// expectedHeader is the canonical column header for a role's
// exit-gate table. Match is whitespace-tolerant but case-sensitive on
// the column names (the markdown spec uses Title Case).
const expectedHeader = "| # | Clause | Concept (machine) or attested judgement | Eval | Depth |"

// ParseRoleFile reads path and returns the parsed role file. The Role
// name is derived from the filename (e.g., analyst.md → "analyst").
func ParseRoleFile(path string) (*RoleFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFile: read %q: %w", path, err)
	}
	clauses, err := parseExitGateTable(string(data))
	if err != nil {
		return nil, fmt.Errorf("ParseRoleFile: %q: %w", path, err)
	}
	role := strings.TrimSuffix(filepathBase(path), ".md")
	return &RoleFile{
		Role:    role,
		Path:    path,
		Clauses: clauses,
	}, nil
}

// filepathBase returns the basename of a path. Inlined here to avoid
// pulling path/filepath for a one-call need.
func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
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
func parseRoleClauseRow(line string) (RoleClause, error) {
	// Strip leading/trailing pipes, split by `|`. Cells may contain
	// backticks but not raw pipe characters (markdown convention).
	inner := strings.TrimPrefix(line, "|")
	inner = strings.TrimSuffix(inner, "|")
	cells := strings.Split(inner, "|")
	if len(cells) != 5 {
		return RoleClause{}, fmt.Errorf("%w: expected 5 cells, got %d", ErrRoleClauseMalformed, len(cells))
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
// Returns an error if the cell doesn't match either form.
func parseConceptCell(cell string) (string, string, error) {
	if cell == "(judgement)" {
		return "", "", nil
	}
	m := conceptCallRE.FindStringSubmatch(cell)
	if m == nil {
		return "", "", fmt.Errorf("not a recognized concept call or (judgement)")
	}
	return m[1], m[2], nil
}
