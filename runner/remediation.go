package runner

import (
	"context"
	"errors"
	"fmt"
)

// Remediation loop. Per gates.md §11 + §2.1: after the adversarial
// phase raises findings, the producer addresses each one and the
// adversary re-attacks. Bounded by remediation-rounds-max (default
// 5). Each re-attack is a FULL re-attack against the entire upstream
// artifact, with a fresh context. Non-convergence within the bound
// → escalate to the operator (do not spin).
//
// The runner is the *mechanism* of the loop. The producer's fix
// attempt is a hook (FixAttemptFn); the harness layer implements it
// (LLM-driven producer instance, batch script, etc.). The fresh-
// context-per-round invariant is the caller's responsibility (the
// loop simply invokes Attack again — the caller MUST spawn a fresh
// adversary instance per round per §11).

// DefaultRemediationRoundsMax is the gates.md §2.1 default.
const DefaultRemediationRoundsMax = 5

// FixAttemptFn is the producer-side hook. Given the open findings
// on the arrow, the producer attempts fixes (writes code, updates
// docs, etc.) and either:
//   - transitions findings to resolved (via FindingsStore.Transition)
//   - proposes accepted-risk (operator must attest — gates.md §11
//     forbids the producer from accepting its own risk; in v1 the
//     producer signals this via an "accept-risk" return and the
//     operator-attestation step lives above the runner)
//
// Returns true if any fix attempt was made (signals the loop to
// re-attack), false if the producer cannot make progress (signals
// the loop to escalate immediately).
//
// Hook errors are surfaced as harness errors on the final report;
// they do not abort the loop unless they indicate the harness
// itself failed.
type FixAttemptFn func(ctx context.Context, openFindings []FindingRecord) (madeProgress bool, err error)

// RemediationOutcome is the loop's terminal verdict.
type RemediationOutcome string

const (
	// RemediationConverged: zero open findings after a round.
	RemediationConverged RemediationOutcome = "converged"

	// RemediationEscalatedRounds: hit remediation-rounds-max without
	// converging. Per gates.md §11, escalate to operator.
	RemediationEscalatedRounds RemediationOutcome = "escalated-rounds-max"

	// RemediationEscalatedNoProgress: producer signalled it couldn't
	// make progress on a round. Escalate immediately.
	RemediationEscalatedNoProgress RemediationOutcome = "escalated-no-progress"

	// RemediationContextCancelled: ctx.Err() observed mid-loop.
	RemediationContextCancelled RemediationOutcome = "context-cancelled"
)

// RemediationConfig configures one remediation loop run.
type RemediationConfig struct {
	// RoundsMax bounds the loop. Zero means DefaultRemediationRoundsMax.
	RoundsMax int

	// FixAttempt is the producer-side callback. Required for a
	// non-trivial loop; if nil, the loop runs ONE attack and escalates.
	FixAttempt FixAttemptFn

	// AttackBuilder produces the AdversaryAttack for round N. The
	// caller MUST construct a FRESH adversary attack per round (the
	// fresh-context invariant); the harness layer typically
	// re-instantiates the LLM-backed adversary upstream.
	//
	// Round numbers start at 0 (initial attack).
	AttackBuilder func(round int) AdversaryAttack
}

// RemediationReport is the per-loop summary.
type RemediationReport struct {
	ArrowID        string
	Outcome        RemediationOutcome
	RoundsExecuted int
	Reports        []*AttackReport
	HarnessErrors  []string
}

// RunRemediationLoop drives the loop. Returns once the outcome is
// terminal. The Adversary's stores accumulate findings across rounds
// — the loop does NOT reset them. The harness layer transitions
// findings to resolved between rounds via the producer's fix-attempt
// callback.
func (a *Adversary) RunRemediationLoop(ctx context.Context, cfg RemediationConfig) (*RemediationReport, error) {
	if cfg.AttackBuilder == nil {
		return nil, errors.New("RemediationConfig.AttackBuilder required")
	}
	maxRounds := cfg.RoundsMax
	if maxRounds <= 0 {
		maxRounds = DefaultRemediationRoundsMax
	}

	report := &RemediationReport{}

	for round := 0; round < maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			report.Outcome = RemediationContextCancelled
			return report, err
		}
		attack := cfg.AttackBuilder(round)
		attack.Round = round
		if report.ArrowID == "" {
			report.ArrowID = attack.ArrowID
		}
		attackReport, err := a.Attack(ctx, attack)
		if err != nil {
			report.Outcome = RemediationContextCancelled
			if attackReport != nil {
				report.Reports = append(report.Reports, attackReport)
			}
			return report, err
		}
		report.Reports = append(report.Reports, attackReport)
		report.RoundsExecuted++
		report.HarnessErrors = append(report.HarnessErrors, attackReport.HarnessErrors...)

		// Convergence: no open findings on the arrow above the engine's
		// threshold. The adversarial pass raises into FindingsStore; we
		// query it for the canonical answer (the attack report alone is
		// last-round-only).
		openCount := countOpenFindings(a.FindingsStore, attack.ArrowID)
		if openCount == 0 {
			report.Outcome = RemediationConverged
			return report, nil
		}

		// More open findings → producer fix attempt. If no callback,
		// escalate immediately.
		if cfg.FixAttempt == nil {
			report.Outcome = RemediationEscalatedNoProgress
			return report, nil
		}
		openFindings := openFindingsSnapshot(a.FindingsStore, attack.ArrowID)
		madeProgress, fixErr := cfg.FixAttempt(ctx, openFindings)
		if fixErr != nil {
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("fix-attempt round %d: %v", round, fixErr))
		}
		if !madeProgress {
			report.Outcome = RemediationEscalatedNoProgress
			return report, nil
		}
	}

	// Bound hit.
	report.Outcome = RemediationEscalatedRounds
	return report, nil
}

// countOpenFindings returns how many findings on the arrow are in
// open or running status. Resolved / accepted-risk / unevaluated
// are excluded from the convergence test. (unevaluated still blocks
// the verification gate via no-open-finding; the remediation loop's
// job is convergence, not block-detection.)
func countOpenFindings(s *FindingsStore, arrowID string) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, f := range s.ForArrow(arrowID) {
		if f.Status == FindingStatusOpen || f.Status == FindingStatusRunning {
			n++
		}
	}
	return n
}

// openFindingsSnapshot returns the snapshot of open/running findings
// the producer needs to act on.
func openFindingsSnapshot(s *FindingsStore, arrowID string) []FindingRecord {
	if s == nil {
		return nil
	}
	src := s.ForArrow(arrowID)
	out := make([]FindingRecord, 0, len(src))
	for _, f := range src {
		if f.Status == FindingStatusOpen || f.Status == FindingStatusRunning {
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
// If an arrow has no depth-sensitive clauses (purely machine /
// depth-robust), the adversarial phase doesn't run and this helper
// SHOULD NOT be called. The engine layer (phase-6) decides.
func VerificationAutoInsert(arrowID string, existing []Clause) []Clause {
	out := make([]Clause, len(existing), len(existing)+2)
	copy(out, existing)

	// Skip insertion if the operator's clause set already includes
	// the auto-insert concept on this arrow — they may have explicit
	// overrides with project-specific args.
	hasNoOpenFinding := false
	hasMinDepth := false
	for _, c := range existing {
		switch c.Concept {
		case "no-open-finding":
			hasNoOpenFinding = true
		case "every-requirement-meets-min-depth":
			hasMinDepth = true
		}
	}
	if !hasNoOpenFinding {
		out = append(out, Clause{
			Concept: "no-open-finding",
			Args:    map[string]any{"arrow-id": arrowID},
			ArrowID: arrowID,
		})
	}
	if !hasMinDepth {
		out = append(out, Clause{
			Concept: "every-requirement-meets-min-depth",
			Args:    map[string]any{"arrow-id": arrowID},
			ArrowID: arrowID,
		})
	}
	return out
}
