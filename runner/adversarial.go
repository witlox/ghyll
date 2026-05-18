package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Adversarial pass coordinator. Per gates.md §11: every arrow
// carrying any depth-sensitive clause runs three sub-activities
// BEFORE verification:
//
//  1. Clause-falsification — try to make each depth-sensitive clause
//     fail. The runner runs each clause via its existing evaluator;
//     a passing clause is not falsified, a failing clause is. Either
//     way the finding type is `clause-falsification` (the wire word
//     for "the adversary attempted falsification") with status open
//     iff the clause failed (gates.md §7.3).
//  2. Open sweep — find defects no clause names. Open by design;
//     the runner provides a hook so the harness layer (LLM-backed
//     adversary, lint scans, fuzzers) can plug in. Default returns
//     no findings.
//  3. Depth classification — classify each requirement on the
//     project's depth ladder. Hook-driven for the same reason. The
//     coordinator records classifications into the attached
//     ClassificationsStore.
//
// The producer role does NOT run the adversary — that's an instance-
// separation invariant (gates.md §11 + §1.1). The harness layer is
// responsible for spawning the fresh adversary instance; the runner
// is the *mechanism* it calls into.
//
// Findings are raised into the FindingsStore attached to the
// supplied Adversary. The arrow-id passed to Attack flows through
// to FindingRecord.ArrowID; the engine layer (phase-6 work) picks
// them up via the FindingsObserver hook.

// AdversaryFindingType discriminators per gates.md §7.3.
const (
	// FindingTypeClauseFalsification: the adversarial phase tried
	// and succeeded to falsify a depth-sensitive clause.
	FindingTypeClauseFalsification FindingType = "clause-falsification"

	// FindingTypeOpenSweep: open-sweep sub-activity found a defect
	// no clause named.
	FindingTypeOpenSweep FindingType = "open-sweep"

	// FindingTypeDepthBelowMin: depth-classification observed a
	// requirement below its declared minimum.
	FindingTypeDepthBelowMin FindingType = "depth-below-min"
)

// OpenSweepFn is the hook signature for the open-sweep sub-activity.
// Implementations scan the upstream artifact for defects no clause
// names. Each returned finding's Type SHOULD be FindingTypeOpenSweep
// (the coordinator does not enforce this — operators may use a
// project-extended type so cardinality-check tracks it).
//
// Return an error only for harness-side failures (couldn't run);
// for "found nothing," return nil + nil.
type OpenSweepFn func(ctx context.Context, attack AdversaryAttack) ([]FindingRecord, error)

// DepthClassifyFn is the hook signature for the depth-classification
// sub-activity. It receives the arrow's declared Requirements and
// returns one Classification per requirement (callers may omit
// requirements they couldn't classify; the resulting Unevaluated
// status of every-requirement-meets-min-depth surfaces the gap).
type DepthClassifyFn func(ctx context.Context, attack AdversaryAttack) ([]Classification, error)

// AdversaryAttack is the input to one adversarial pass invocation.
// Construct via NewAdversaryAttack so required defaults are applied.
type AdversaryAttack struct {
	// ArrowID identifies the arrow under attack. Required.
	ArrowID string

	// PassID identifies the current pass; flows into FindingRecord
	// and through to the runner's evaluation runs.
	PassID string

	// ProjectDir is the project root the attack runs against.
	ProjectDir string

	// DepthClauses lists the depth-sensitive clauses to attempt
	// falsification against. The coordinator invokes each through
	// the Runner.
	DepthClauses []Clause

	// Requirements lists the requirements the depth-classification
	// sub-activity should classify. Each is also declared into the
	// ClassificationsStore so every-requirement-meets-min-depth can
	// later evaluate them.
	Requirements []Requirement

	// Round is the remediation round number (0 = initial attack,
	// 1..N = remediation rounds per §2.1).
	Round int
}

// Adversary is the coordinator. Construct via NewAdversary;
// thread-safe to invoke Attack concurrently on different arrows but
// not on the same arrow (the FindingsStore and ClassificationsStore
// serialize on per-arrow keys).
type Adversary struct {
	// FindingsStore receives findings raised by sub-activities.
	// Required.
	FindingsStore *FindingsStore

	// ClassificationsStore receives requirement declarations and
	// classifications. Required.
	ClassificationsStore *ClassificationsStore

	// Runner is the evaluator dispatcher used for clause-falsification.
	// Required.
	Runner *Runner

	// DepthLadder is the project's ladder. Used for
	// depth-classification finding details. Defaults to
	// NewDefaultDepthLadder.
	DepthLadder *DepthLadder

	// OpenSweep is the open-sweep hook. Default is no-op
	// (returns nil, nil).
	OpenSweep OpenSweepFn

	// Classify is the depth-classification hook. Default is no-op.
	Classify DepthClassifyFn

	// IDGen produces unique finding IDs. Default is a process-local
	// timestamp+counter generator.
	IDGen func() string

	// Now is the clock for RaisedAt timestamps. Defaults to time.Now.
	Now func() time.Time

	// AdversaryRole is the RaisedByRole stamp on emitted findings.
	// Defaults to "adversary" (gates.md §1.1 synthetic role-id).
	AdversaryRole string
}

// NewAdversary returns an Adversary with required fields populated.
// Hooks default to no-ops; clock and IDGen are set if nil.
func NewAdversary(findings *FindingsStore, classifications *ClassificationsStore, runner *Runner) *Adversary {
	return &Adversary{
		FindingsStore:        findings,
		ClassificationsStore: classifications,
		Runner:               runner,
	}
}

func (a *Adversary) ensureDefaults() {
	if a.DepthLadder == nil {
		a.DepthLadder = NewDefaultDepthLadder()
	}
	if a.OpenSweep == nil {
		a.OpenSweep = noopOpenSweep
	}
	if a.Classify == nil {
		a.Classify = noopClassify
	}
	if a.IDGen == nil {
		a.IDGen = defaultAdversaryFindingIDGen
	}
	if a.Now == nil {
		a.Now = time.Now
	}
	if a.AdversaryRole == "" {
		a.AdversaryRole = "adversary"
	}
}

// AttackReport summarizes one adversarial pass. The engine layer
// (phase-6) consults this to decide whether to enter the remediation
// loop or proceed to verification.
type AttackReport struct {
	ArrowID                 string
	Round                   int
	ClauseFalsifications    []ClauseFalsificationResult
	OpenSweepFindings       []string // finding IDs raised
	DepthBelowMinFindings   []string // finding IDs raised
	ClassificationsRecorded int

	// HarnessErrors carries non-fatal errors from sub-activities so
	// the engine can surface them without failing the entire phase.
	HarnessErrors []string
}

// ClauseFalsificationResult is one entry in AttackReport.ClauseFalsifications.
type ClauseFalsificationResult struct {
	ClauseID    string
	Falsified   bool   // true if the clause failed (adversary won)
	FindingID   string // populated when Falsified
	EvaluatorID string // for traceability
	RunError    string
}

// AnyOpen reports whether any open finding (including the freshly-
// raised ones from this attack) is in scope for the remediation loop.
// Used by the loop to decide whether to keep iterating.
func (r AttackReport) AnyOpen() bool {
	return len(r.OpenSweepFindings) > 0 ||
		len(r.DepthBelowMinFindings) > 0 ||
		anyFalsified(r.ClauseFalsifications)
}

func anyFalsified(xs []ClauseFalsificationResult) bool {
	for _, x := range xs {
		if x.Falsified {
			return true
		}
	}
	return false
}

// Attack runs one round of the adversarial phase: clause-falsification
// → open-sweep → depth-classification. Findings are raised into the
// FindingsStore; classifications into the ClassificationsStore. The
// AttackReport is the engine's summary input.
//
// Per gates.md §11: each round is a FULL re-attack against the entire
// upstream artifact, not just prior-finding targets — the harness
// layer is responsible for re-running this with a fresh context per
// round.
func (a *Adversary) Attack(ctx context.Context, attack AdversaryAttack) (*AttackReport, error) {
	a.ensureDefaults()
	if a.FindingsStore == nil {
		return nil, errors.New("Adversary: FindingsStore required")
	}
	if a.ClassificationsStore == nil {
		return nil, errors.New("Adversary: ClassificationsStore required")
	}
	if a.Runner == nil {
		return nil, errors.New("Adversary: Runner required")
	}
	if strings.TrimSpace(attack.ArrowID) == "" {
		return nil, errors.New("AdversaryAttack: ArrowID required")
	}
	if strings.TrimSpace(attack.PassID) == "" {
		return nil, errors.New("AdversaryAttack: PassID required")
	}

	report := &AttackReport{
		ArrowID: attack.ArrowID,
		Round:   attack.Round,
	}
	raisedAt := a.Now().UTC().Format(time.RFC3339)

	// Pre-declare requirements (idempotent across rounds — duplicate
	// declarations are silently ignored; an actual duplicate ID error
	// at first-declare time is surfaced).
	for _, r := range attack.Requirements {
		if err := a.ClassificationsStore.DeclareRequirement(attack.ArrowID, r); err != nil {
			if errors.Is(err, ErrRequirementDuplicateID) {
				continue
			}
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("declare-requirement %q: %v", r.ID, err))
		}
	}

	// 1. Clause-falsification.
	for _, cls := range attack.DepthClauses {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// Each clause needs a clause-id; if the operator didn't set one,
		// synthesize from the concept name + the attack's pass-id.
		clauseID := cls.ClauseID
		if clauseID == "" {
			clauseID = fmt.Sprintf("%s/%s/round%d", attack.PassID, cls.Concept, attack.Round)
		}
		cls.ArrowID = attack.ArrowID
		if cls.ProjectDir == "" {
			cls.ProjectDir = attack.ProjectDir
		}
		run, err := a.Runner.Evaluate(ctx, clauseID, attack.PassID, cls)
		entry := ClauseFalsificationResult{ClauseID: clauseID}
		if run != nil {
			entry.EvaluatorID = run.Evaluator.String()
		}
		if err != nil {
			entry.RunError = err.Error()
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("falsify %q: %v", clauseID, err))
		} else if run != nil && run.EndStatus == StatusFail {
			entry.Falsified = true
			fid := a.IDGen()
			severity := SeverityHigh
			if run.Result != nil {
				severity = severityForClauseFalsification(run.Result)
			}
			err := a.FindingsStore.Raise(FindingRecord{
				ID:           fid,
				ArrowID:      attack.ArrowID,
				Type:         FindingTypeClauseFalsification,
				Severity:     severity,
				Status:       FindingStatusOpen,
				Description:  fmt.Sprintf("clause %s falsified (round %d)", clauseID, attack.Round),
				RaisedAt:     raisedAt,
				RaisedByRole: a.AdversaryRole,
			})
			if err != nil {
				report.HarnessErrors = append(report.HarnessErrors,
					fmt.Sprintf("raise clause-falsification: %v", err))
			} else {
				entry.FindingID = fid
			}
		}
		report.ClauseFalsifications = append(report.ClauseFalsifications, entry)
	}

	// 2. Open sweep.
	if err := ctx.Err(); err != nil {
		return report, err
	}
	openFindings, err := a.OpenSweep(ctx, attack)
	if err != nil {
		report.HarnessErrors = append(report.HarnessErrors, fmt.Sprintf("open-sweep: %v", err))
	}
	for _, f := range openFindings {
		f.ArrowID = attack.ArrowID
		if f.Type == "" {
			f.Type = FindingTypeOpenSweep
		}
		if f.Status == FindingStatus(0) {
			// Caller may have set Status=0 (open by intent); set
			// explicitly so Raise's validation doesn't drift.
			f.Status = FindingStatusOpen
		}
		if f.ID == "" {
			f.ID = a.IDGen()
		}
		if f.RaisedAt == "" {
			f.RaisedAt = raisedAt
		}
		if f.RaisedByRole == "" {
			f.RaisedByRole = a.AdversaryRole
		}
		if err := a.FindingsStore.Raise(f); err != nil {
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("raise open-sweep %q: %v", f.ID, err))
			continue
		}
		report.OpenSweepFindings = append(report.OpenSweepFindings, f.ID)
	}

	// 3. Depth classification.
	if err := ctx.Err(); err != nil {
		return report, err
	}
	classifications, err := a.Classify(ctx, attack)
	if err != nil {
		report.HarnessErrors = append(report.HarnessErrors, fmt.Sprintf("classify: %v", err))
	}
	// Build a lookup of declared minimums for the depth-below-min check.
	minByReq := make(map[string]DepthRank, len(attack.Requirements))
	for _, r := range attack.Requirements {
		minByReq[r.ID] = r.MinDepth
	}
	for _, c := range classifications {
		if err := a.ClassificationsStore.RecordClassification(attack.ArrowID, c); err != nil {
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("record classification %q: %v", c.RequirementID, err))
			continue
		}
		report.ClassificationsRecorded++
		min, ok := minByReq[c.RequirementID]
		if !ok {
			continue
		}
		if c.Observed < min {
			fid := a.IDGen()
			err := a.FindingsStore.Raise(FindingRecord{
				ID:      fid,
				ArrowID: attack.ArrowID,
				Type:    FindingTypeDepthBelowMin,
				// Below-min defects are severity-high by default; the
				// engine can re-classify per project policy via
				// Transition.
				Severity: SeverityHigh,
				Status:   FindingStatusOpen,
				Description: fmt.Sprintf(
					"requirement %s observed at %s (rank %d) < min %s (rank %d): %s",
					c.RequirementID,
					a.DepthLadder.Label(c.Observed), c.Observed,
					a.DepthLadder.Label(min), min,
					c.Evidence,
				),
				RaisedAt:     raisedAt,
				RaisedByRole: a.AdversaryRole,
			})
			if err != nil {
				report.HarnessErrors = append(report.HarnessErrors,
					fmt.Sprintf("raise depth-below-min: %v", err))
			} else {
				report.DepthBelowMinFindings = append(report.DepthBelowMinFindings, fid)
			}
		}
	}

	return report, nil
}

// noopOpenSweep is the default OpenSweep hook.
func noopOpenSweep(_ context.Context, _ AdversaryAttack) ([]FindingRecord, error) {
	return nil, nil
}

// noopClassify is the default Classify hook.
func noopClassify(_ context.Context, _ AdversaryAttack) ([]Classification, error) {
	return nil, nil
}

// severityForClauseFalsification translates a clause Result into a
// severity rank. v1 heuristic: failed clauses default to high; a
// future amendment could read severity hints from Result.Details
// per per-concept policy.
func severityForClauseFalsification(_ *Result) int {
	return SeverityHigh
}

// adversaryFindingSeq is the per-process counter that disambiguates
// finding IDs produced at the same nanosecond (cf. amendment F40).
var adversaryFindingSeq atomic.Uint64

// defaultAdversaryFindingIDGen produces a unique finding ID of the
// form "adv-<nano>-<seq>-<processIDPrefix>".
func defaultAdversaryFindingIDGen() string {
	seq := adversaryFindingSeq.Add(1)
	return fmt.Sprintf("adv-%s-%d-%s",
		time.Now().UTC().Format("20060102-150405.000000000"),
		seq,
		processIDPrefix)
}
