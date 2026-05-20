// Package modal implements the Tier 2 operator verdict modal
// (ADR-016 Part D). The chat REPL drives the modal between
// turns via OperatorModalPrompt; verdict submissions feed into
// AttestationStore.Record.
//
// Two implementations:
//
//   - TermModal: tty interactive. Reads from stdin, writes
//     prompts to stdout. Blocks the chat-loop until the
//     operator answers OR cancels via ctx.
//   - StubModal: scripted. Pre-loaded with responses; each
//     Present* call consumes the next. Used by tests and BDD
//     bindings.
//
// Errors:
//
//   - ErrModalSkipped — operator typed `skip`; the clause stays
//     pending. Returned by PresentVerdict only;
//     PresentEscalation has no skip option (operator MUST choose
//     option 1 or 2).
//
// Hint carries the dispatcher-synthesized payload (ADR-016 Part
// G). EscalationChoice records option (1=accepted-risk,
// 2=route-upstream) + the residue/rationale string.
package modal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/witlox/ghyll/runner"
)

// Hint is the dispatcher-synthesized payload presented to the
// operator inside the verdict modal. Mirrors runner.Hint so the
// modal package doesn't need to import the dispatcher's hint type
// directly (but matches its JSON shape for serialization
// round-trip via OperatorEvent.Detail).
type Hint struct {
	ArrowID        string `json:"arrow_id"`
	ClauseID       string `json:"clause_id"`
	Concept        string `json:"concept"`
	AttestationRef string `json:"attestation_ref"`
}

// VerdictSubmission is the operator's answer to a verdict modal.
type VerdictSubmission struct {
	Verdict runner.AttestationVerdict
	Unit    runner.VerdictUnit
	Payload runner.VerdictUnitPayload
}

// EscalationChoice is the operator's answer to an escalation
// prompt (presented after 3 insufficient-basis rounds).
type EscalationChoice struct {
	// Option is 1 (accepted-risk) or 2 (route-upstream).
	Option int
	// Residue / rationale text the operator provides. Required
	// for both options (gate-1 F-23 / ADR-016 Part D).
	Residue string
}

// OperatorModalPrompt is the chat REPL's contract for blocking
// modal interactions. Implementations: TermModal (tty),
// StubModal (test).
type OperatorModalPrompt interface {
	// PresentVerdict blocks until the operator submits a verdict
	// (pass / fail / insufficient-basis) OR returns
	// ErrModalSkipped. ctx-cancel aborts with ctx.Err().
	PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error)

	// PresentEscalation blocks until the operator chooses option
	// 1 (accepted-risk + residue) or option 2 (route-upstream +
	// rationale). No skip; no default. ctx-cancel aborts.
	PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error)
}

// ErrModalSkipped is returned by PresentVerdict when the operator
// types "skip" — the clause stays pending; the next REPL turn
// re-presents.
var ErrModalSkipped = errors.New("modal-skipped")

// ErrEscalationNoDefault is returned by PresentEscalation when
// the operator's input doesn't match option 1 or 2 after the
// configured retries.
var ErrEscalationNoDefault = errors.New("modal-escalation-no-default")

// --- TermModal (tty interactive) ----------------------------

// TermModal is the tty-interactive implementation. Lines are
// pulled from a session-scoped LineReader (one goroutine + one
// bufio.Scanner over stdin); prompts are written to Out.
//
// Gate-2 CONC-C-1/C-2: TermModal no longer holds its own
// bufio.Scanner. The session owns a single LineReader shared
// between REPL and TermModal so dual-scanner buffer-loss + the
// ctx-cancel goroutine leak are both gone.
//
// Tier 2 minimal: prompts are plain-text Q/A. Future versions
// may add ANSI styling, multi-line residue editing, etc.
type TermModal struct {
	// Lines is the shared line-source. Required.
	Lines *LineReader
	// Out is the prompt destination (typically os.Stdout).
	Out io.Writer
}

// PresentVerdict implements OperatorModalPrompt.
func (m *TermModal) PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error) {
	if m.Lines == nil {
		return VerdictSubmission{}, fmt.Errorf("modal: TermModal.Lines is nil")
	}
	writePrompt(m.Out,
		"\n",
		"── attestation request ─────────────────\n",
		fmt.Sprintf("  arrow:           %s\n", hint.ArrowID),
		fmt.Sprintf("  clause:          %s\n", hint.ClauseID),
		fmt.Sprintf("  concept:         %s\n", hint.Concept),
		fmt.Sprintf("  attestation-ref: %s\n", hint.AttestationRef),
		"────────────────────────────────────────\n",
		"verdict? [pass / fail / insufficient-basis / skip]: ",
	)

	if err := ctx.Err(); err != nil {
		return VerdictSubmission{}, err
	}
	line, err := m.Lines.Next(ctx)
	if err != nil {
		return VerdictSubmission{}, err
	}
	line = strings.TrimSpace(line)
	switch strings.ToLower(line) {
	case "skip", "s":
		return VerdictSubmission{}, ErrModalSkipped
	case "pass", "p":
		return m.promptPass(ctx)
	case "fail", "f":
		return m.promptFail(ctx)
	case "insufficient-basis", "ib", "i":
		return m.promptInsufficientBasis(ctx)
	default:
		return VerdictSubmission{}, fmt.Errorf("verdict %q: expected pass/fail/insufficient-basis/skip", line)
	}
}

func (m *TermModal) promptPass(_ context.Context) (VerdictSubmission, error) {
	// `confirm` unit has no payload.
	return VerdictSubmission{
		Verdict: runner.AttestationPass,
		Unit:    runner.VerdictUnitConfirm,
	}, nil
}

func (m *TermModal) promptFail(ctx context.Context) (VerdictSubmission, error) {
	writePrompt(m.Out, "inspected locations (comma-separated, e.g. 'file.go:42-50, other.go:1'): ")
	line, err := m.Lines.Next(ctx)
	if err != nil {
		return VerdictSubmission{}, err
	}
	parts := strings.Split(line, ",")
	inspected := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			inspected = append(inspected, p)
		}
	}
	return VerdictSubmission{
		Verdict: runner.AttestationFail,
		Unit:    runner.VerdictUnitRecordLocationsInspected,
		Payload: runner.VerdictUnitPayload{Inspected: inspected},
	}, nil
}

func (m *TermModal) promptInsufficientBasis(ctx context.Context) (VerdictSubmission, error) {
	writePrompt(m.Out, "residue note (why is the basis insufficient?): ")
	line, err := m.Lines.Next(ctx)
	if err != nil {
		return VerdictSubmission{}, err
	}
	return VerdictSubmission{
		Verdict: runner.AttestationInsufficientBasis,
		Unit:    runner.VerdictUnitWriteResidueNote,
		Payload: runner.VerdictUnitPayload{Residue: strings.TrimSpace(line)},
	}, nil
}

// PresentEscalation implements OperatorModalPrompt.
func (m *TermModal) PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error) {
	if m.Lines == nil {
		return EscalationChoice{}, fmt.Errorf("modal: TermModal.Lines is nil")
	}
	writePrompt(m.Out,
		"\n",
		"── escalation: 3 insufficient-basis rounds ──\n",
		fmt.Sprintf("  arrow:    %s\n", hint.ArrowID),
		fmt.Sprintf("  clause:   %s\n", hint.ClauseID),
		"  options:\n",
		"    1) accept risk     (record residue note; finding → accepted-risk)\n",
		"    2) route upstream  (record rationale; pass aborts; deeper-tier retry)\n",
		"─────────────────────────────────────────────\n",
		"choice (1 or 2): ",
	)

	if err := ctx.Err(); err != nil {
		return EscalationChoice{}, err
	}
	line, err := m.Lines.Next(ctx)
	if err != nil {
		return EscalationChoice{}, err
	}
	opt, parseErr := strconv.Atoi(strings.TrimSpace(line))
	if parseErr != nil || (opt != 1 && opt != 2) {
		return EscalationChoice{}, fmt.Errorf("%w: %q", ErrEscalationNoDefault, line)
	}
	var promptText string
	if opt == 1 {
		promptText = "residue note (why the basis remains insufficient): "
	} else {
		promptText = "rationale (why deeper rework is needed): "
	}
	writePrompt(m.Out, promptText)
	residue, err := m.Lines.Next(ctx)
	if err != nil {
		return EscalationChoice{}, err
	}
	return EscalationChoice{
		Option:  opt,
		Residue: strings.TrimSpace(residue),
	}, nil
}

// writePrompt writes a sequence of strings to w, ignoring write
// errors (operator-facing prompts; a write failure typically
// means the operator already disconnected — the next Lines.Next
// will surface ctx.Err() or io.EOF).
func writePrompt(w io.Writer, parts ...string) {
	for _, p := range parts {
		_, _ = io.WriteString(w, p)
	}
}

// readLineCtx is the legacy bufio.Scanner-per-call helper. Kept
// only as a build-time symbol so tests that imported it directly
// don't break; nothing in the production path uses it post-
// CONC-C-1/C-2 remediation. Will be removed once test fixtures
// are migrated.
//
//nolint:unused
func readLineCtx(ctx context.Context, scanner *bufio.Scanner) (string, error) {
	type readResult struct {
		line string
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		if !scanner.Scan() {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			done <- readResult{"", err}
			return
		}
		done <- readResult{scanner.Text(), nil}
	}()
	select {
	case r := <-done:
		return r.line, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// --- StubModal (test fixture) -------------------------------

// StubModal is the test-injectable implementation. Pre-loaded
// with scripted responses; each Present* call consumes the next.
// Empty queue → PresentVerdict returns ErrModalSkipped;
// PresentEscalation returns ErrEscalationNoDefault.
type StubModal struct {
	Verdicts    []VerdictSubmission
	Escalations []EscalationChoice
	// Errors injects ErrModalSkipped or other errors at specific
	// indices into the verdict queue. Same length as Verdicts;
	// non-nil entries override the corresponding Verdicts[i].
	VerdictErrs []error

	vIdx, eIdx int
}

// PresentVerdict implements OperatorModalPrompt.
func (m *StubModal) PresentVerdict(ctx context.Context, hint Hint) (VerdictSubmission, error) {
	if err := ctx.Err(); err != nil {
		return VerdictSubmission{}, err
	}
	if m.vIdx >= len(m.Verdicts) {
		return VerdictSubmission{}, ErrModalSkipped
	}
	defer func() { m.vIdx++ }()
	if m.vIdx < len(m.VerdictErrs) && m.VerdictErrs[m.vIdx] != nil {
		return VerdictSubmission{}, m.VerdictErrs[m.vIdx]
	}
	return m.Verdicts[m.vIdx], nil
}

// PresentEscalation implements OperatorModalPrompt.
func (m *StubModal) PresentEscalation(ctx context.Context, hint Hint) (EscalationChoice, error) {
	if err := ctx.Err(); err != nil {
		return EscalationChoice{}, err
	}
	if m.eIdx >= len(m.Escalations) {
		return EscalationChoice{}, ErrEscalationNoDefault
	}
	defer func() { m.eIdx++ }()
	return m.Escalations[m.eIdx], nil
}
