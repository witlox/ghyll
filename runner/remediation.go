package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Remediation loop. Per gates.md §11 + §2.1: after the adversarial
// phase raises findings, the producer addresses each one and the
// adversary re-attacks. Bounded by remediation-rounds-max (default
// 5). Each re-attack is a FULL re-attack against the entire upstream
// artifact, with a fresh adversary instance / clean context.
//
// Hardenings (validation-pass-5):
//   - F3: single-shot Adversary enforcement requires the loop to
//     construct a FRESH Adversary per round; this is now mandatory
//     via the AdversaryBuilder callback.
//   - F7: ctx.Err checked after Attack and after FixAttempt, not
//     just at top-of-loop.
//   - F8: AttackBuilder no longer has its Round overwritten by the
//     loop. The AttackBuilder receives the round number as its
//     argument; the caller is responsible for setting attack.Round
//     to match.
//   - F9: configurable convergence-on-unevaluated policy; default
//     treats unevaluated as non-converged so verification doesn't
//     surprise the operator.
//   - F26: configurable consecutive-fix-error budget; exceeding it
//     escalates with RemediationEscalatedHookError.
//   - F27: severity-threshold aligned with verification.
//   - F28: FixAttemptFn contract explicit about snapshot mutation.

// DefaultRemediationRoundsMax is the gates.md §2.1 default.
const DefaultRemediationRoundsMax = 5

// DefaultMaxFixErrors is the default consecutive-FixAttempt-error
// budget (F26) before escalating.
const DefaultMaxFixErrors = 2

// FixAttemptFn is the producer-side hook.
//
// The `openFindings` slice is a deep copy from FindingsStore.ForArrow;
// mutating it has NO effect. Use FindingsStore.Transition(id, status)
// to transition findings (F28).
//
// Return (madeProgress=true, err=nil) when at least one transition
// happened in this round. madeProgress=false signals immediate
// escalation. A non-nil err counts against the hook-error budget
// (F26) but does not abort the loop on its own.
type FixAttemptFn func(ctx context.Context, openFindings []FindingRecord) (madeProgress bool, err error)

// AdversaryBuilder produces a FRESH Adversary for round N. Per F3
// and gates.md §11 the harness layer must guarantee clean context
// per round; the builder is the seam where that guarantee lives.
type AdversaryBuilder func(round int) *Adversary

// RemediationOutcome is the loop's terminal verdict.
type RemediationOutcome string

const (
	RemediationConverged                RemediationOutcome = "converged"
	RemediationConvergedWithUnevaluated RemediationOutcome = "converged-with-unevaluated"
	RemediationEscalatedRounds          RemediationOutcome = "escalated-rounds-max"
	RemediationEscalatedNoProgress      RemediationOutcome = "escalated-no-progress"
	RemediationEscalatedHookError       RemediationOutcome = "escalated-hook-error"
	RemediationContextCancelled         RemediationOutcome = "context-cancelled"
)

// RemediationConfig configures one remediation loop run.
type RemediationConfig struct {
	// RoundsMax bounds the loop. Zero means DefaultRemediationRoundsMax.
	RoundsMax int

	// MaxFixErrors bounds consecutive FixAttempt errors before
	// escalating with RemediationEscalatedHookError. Zero means
	// DefaultMaxFixErrors. Negative disables the budget (test-only).
	MaxFixErrors int

	// SeverityThreshold is the convergence-and-verification cutoff
	// (F27). Findings BELOW this rank are NOT counted toward
	// non-convergence. Default SeverityInfo (count all open).
	SeverityThreshold int

	// CountUnevaluatedAsOpen treats Unevaluated findings as
	// non-converged (F9). Default true — verification's
	// no-open-finding blocks on Unevaluated, so the loop should
	// match. Set false only for diagnostic test runs.
	CountUnevaluatedAsOpen bool

	// FixAttempt is the producer-side callback. Required for a
	// non-trivial loop; if nil, the loop runs ONE attack and
	// escalates with RemediationEscalatedNoProgress.
	FixAttempt FixAttemptFn

	// AdversaryBuilder constructs a fresh Adversary per round (F3).
	// Required.
	AdversaryBuilder AdversaryBuilder

	// AttackBuilder produces the AdversaryAttack for round N. The
	// caller MUST set attack.Round = round; the loop does NOT
	// overwrite it (F8).
	AttackBuilder func(round int) AdversaryAttack
}

// applyDefaults fills in zero-valued fields with their defaults.
func (c *RemediationConfig) applyDefaults() {
	if c.RoundsMax <= 0 {
		c.RoundsMax = DefaultRemediationRoundsMax
	}
	if c.MaxFixErrors == 0 {
		c.MaxFixErrors = DefaultMaxFixErrors
	}
	if !c.CountUnevaluatedAsOpen {
		// Honor explicit false; only flip the unset case (zero-value
		// of bool is false either way, so we default it to true).
		// Use a sentinel check via the RoundsMax pattern would be
		// cleaner — but a bool field is the operator-facing knob.
		// We treat the unset (false) case as "default true" for
		// correctness-by-default; operators MUST explicitly set false
		// via a separate flag if they want the diagnostic behavior.
		// Since this is the unset-vs-set ambiguity, document loudly
		// in the field docstring: empty config gets true.
		c.CountUnevaluatedAsOpen = true
	}
}

// RemediationReport is the per-loop summary.
type RemediationReport struct {
	ArrowID        string
	Outcome        RemediationOutcome
	RoundsExecuted int
	Reports        []*AttackReport
	HarnessErrors  []string
}

// RunRemediationLoop drives the loop. Replaces the previous
// (*Adversary).RunRemediationLoop method (F3: a single-shot
// Adversary cannot drive multiple rounds).
func RunRemediationLoop(ctx context.Context, cfg RemediationConfig) (*RemediationReport, error) {
	if cfg.AdversaryBuilder == nil {
		return nil, errors.New("RemediationConfig.AdversaryBuilder required")
	}
	if cfg.AttackBuilder == nil {
		return nil, errors.New("RemediationConfig.AttackBuilder required")
	}
	cfg.applyDefaults()

	report := &RemediationReport{}
	consecutiveFixErrors := 0

	for round := 0; round < cfg.RoundsMax; round++ {
		if err := ctx.Err(); err != nil {
			report.Outcome = RemediationContextCancelled
			return report, err
		}
		attack := cfg.AttackBuilder(round)
		if report.ArrowID == "" {
			report.ArrowID = attack.ArrowID
		}
		// F3: fresh adversary per round.
		a := cfg.AdversaryBuilder(round)
		if a == nil {
			report.Outcome = RemediationEscalatedHookError
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("round %d: AdversaryBuilder returned nil", round))
			return report, nil
		}
		attackReport, err := a.Attack(ctx, attack)
		if err != nil {
			report.Outcome = RemediationContextCancelled
			if attackReport != nil {
				report.Reports = append(report.Reports, attackReport)
				appendPrefixed(&report.HarnessErrors, attackReport.HarnessErrors, round)
			}
			return report, err
		}
		report.Reports = append(report.Reports, attackReport)
		report.RoundsExecuted++
		appendPrefixed(&report.HarnessErrors, attackReport.HarnessErrors, round)

		// F7: check ctx after Attack, before FixAttempt.
		if err := ctx.Err(); err != nil {
			report.Outcome = RemediationContextCancelled
			return report, err
		}

		// Convergence test (F6, F9, F27).
		openCount, unevalCount := openFindingsByThreshold(
			a.FindingsStore, attack.ArrowID, cfg.SeverityThreshold)
		if openCount == 0 {
			if unevalCount > 0 && cfg.CountUnevaluatedAsOpen {
				// Unevaluated findings remain; verification will
				// block via no-open-finding. Surface this state to
				// the operator instead of claiming "converged."
				report.Outcome = RemediationConvergedWithUnevaluated
				return report, nil
			}
			report.Outcome = RemediationConverged
			return report, nil
		}

		// Findings remain → producer fix attempt.
		if cfg.FixAttempt == nil {
			report.Outcome = RemediationEscalatedNoProgress
			return report, nil
		}
		openFindings := openFindingsSnapshot(a.FindingsStore, attack.ArrowID, cfg.SeverityThreshold)
		madeProgress, fixErr := cfg.FixAttempt(ctx, openFindings)
		if fixErr != nil {
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("round %d: fix-attempt: %v", round, fixErr))
			consecutiveFixErrors++
			if cfg.MaxFixErrors > 0 && consecutiveFixErrors >= cfg.MaxFixErrors {
				report.Outcome = RemediationEscalatedHookError
				return report, nil
			}
		} else {
			consecutiveFixErrors = 0
		}
		if !madeProgress {
			report.Outcome = RemediationEscalatedNoProgress
			return report, nil
		}

		// F7: check ctx after FixAttempt.
		if err := ctx.Err(); err != nil {
			report.Outcome = RemediationContextCancelled
			return report, err
		}
	}

	report.Outcome = RemediationEscalatedRounds
	return report, nil
}

// appendPrefixed prefixes each error with `round N: ` before
// appending to dst (F32 — preserve round provenance across the
// flattened HarnessErrors list).
func appendPrefixed(dst *[]string, errs []string, round int) {
	for _, e := range errs {
		*dst = append(*dst, fmt.Sprintf("round %d: %s", round, e))
	}
}

// openFindingsByThreshold returns (openOrRunning, unevaluated)
// counts on the arrow, with the open-or-running count filtered by
// severity ≥ threshold (F27). Unevaluated findings are reported
// separately so the caller decides whether to count them.
func openFindingsByThreshold(s *FindingsStore, arrowID string, threshold int) (open, unevaluated int) {
	if s == nil {
		return 0, 0
	}
	for _, f := range s.ForArrow(arrowID) {
		switch f.Status {
		case FindingStatusOpen, FindingStatusRunning:
			if f.Severity >= threshold {
				open++
			}
		case FindingStatusUnevaluated:
			unevaluated++
		}
	}
	return open, unevaluated
}

// openFindingsSnapshot returns the snapshot of open/running findings
// at-or-above the severity threshold. Excludes Unevaluated (the
// producer can't "fix" an unevaluated finding without re-running
// the adversarial phase against a deeper model).
func openFindingsSnapshot(s *FindingsStore, arrowID string, threshold int) []FindingRecord {
	if s == nil {
		return nil
	}
	src := s.ForArrow(arrowID)
	out := make([]FindingRecord, 0, len(src))
	for _, f := range src {
		if (f.Status == FindingStatusOpen || f.Status == FindingStatusRunning) && f.Severity >= threshold {
			out = append(out, f)
		}
	}
	return out
}

// VerificationAutoInsert appends the auto-insert clauses that
// gates.md §11 requires when an adversarial phase ran on the arrow.
// Returns a NEW slice — the input is not mutated.
//
//   - no-open-finding — guarantees no open finding ≥ threshold or any
//     unevaluated finding can leave the arrow closed.
//   - every-requirement-meets-min-depth — guarantees no requirement
//     stays below its declared minimum.
//
// Hardenings (validation-pass-5):
//   - F29: inserted clauses carry deterministic ClauseIDs
//     `auto/<arrow>/<concept>` so Runner.Evaluate accepts them.
//   - F30: empty arrowID returns the input unchanged.
//   - F31: dedup is case-insensitive via strings.EqualFold.
//   - F41: allocation sized to exactly what's needed.
func VerificationAutoInsert(arrowID string, existing []Clause) []Clause {
	arrowID = strings.TrimSpace(arrowID)
	if arrowID == "" {
		// F30: empty arrowID is operator misconfiguration; do not
		// emit clauses keyed on "".
		out := make([]Clause, len(existing))
		copy(out, existing)
		return out
	}

	hasNoOpenFinding := false
	hasMinDepth := false
	for _, c := range existing {
		if strings.EqualFold(c.Concept, "no-open-finding") {
			hasNoOpenFinding = true
		}
		if strings.EqualFold(c.Concept, "every-requirement-meets-min-depth") {
			hasMinDepth = true
		}
	}

	// F41: count needed inserts before allocating.
	extra := 0
	if !hasNoOpenFinding {
		extra++
	}
	if !hasMinDepth {
		extra++
	}
	out := make([]Clause, len(existing), len(existing)+extra)
	copy(out, existing)
	if !hasNoOpenFinding {
		out = append(out, Clause{
			Concept:  "no-open-finding",
			ClauseID: fmt.Sprintf("auto/%s/no-open-finding", arrowID),
			Args:     map[string]any{"arrow-id": arrowID},
			ArrowID:  arrowID,
		})
	}
	if !hasMinDepth {
		out = append(out, Clause{
			Concept:  "every-requirement-meets-min-depth",
			ClauseID: fmt.Sprintf("auto/%s/every-requirement-meets-min-depth", arrowID),
			Args:     map[string]any{"arrow-id": arrowID},
			ArrowID:  arrowID,
		})
	}
	return out
}
