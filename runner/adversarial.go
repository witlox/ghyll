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
//     fail. Per validation-pass-5 F4: an Unevaluated clause is NOT
//     treated as a non-falsification; it raises a clause-falsification
//     finding with Status=Unevaluated.
//  2. Open sweep — find defects no clause names. Hook-driven.
//  3. Depth classification — classify each requirement on the
//     project's depth ladder. Hook-driven.
//
// Hardenings (validation-pass-5):
//   - F1, F2, F40: defaults populated eagerly in NewAdversary;
//     ApplyDefaults() explicit for direct-struct construction.
//     Attack snapshots hooks into locals so concurrent hook-set
//     races don't poison in-flight attacks.
//   - F3: Adversary is SINGLE-SHOT (atomic.Bool used flag). The
//     fresh-instance-per-round invariant from §11 is now enforced.
//     RemediationLoop constructs a fresh Adversary per round.
//   - F5: OpenSweep + Classify hook panics are recovered as
//     HarnessErrors; the coordinator never crashes the goroutine.
//   - F11: operator-supplied Description/Evidence sanitized before
//     placement into finding records.
//   - F16: when a re-classification observes Above-min for a
//     requirement that has a prior open depth-below-min finding,
//     the finding is auto-resolved.
//   - F17: per-finding RaisedAt with nanosecond precision.
//   - F18: open-sweep hook output pre-validated; bad findings
//     surface as synthetic harness-error findings rather than
//     silent drops.

// AdversaryFindingType discriminators per gates.md §7.3.
const (
	FindingTypeClauseFalsification FindingType = "clause-falsification"
	FindingTypeOpenSweep           FindingType = "open-sweep"
	FindingTypeDepthBelowMin       FindingType = "depth-below-min"
)

// ErrAdversaryAlreadyUsed is returned by Attack when invoked on an
// Adversary that already ran. Per gates.md §11: each remediation
// round MUST use a fresh adversary instance (clean context).
var ErrAdversaryAlreadyUsed = errors.New("adversary-already-used")

// OpenSweepFn is the hook signature for the open-sweep sub-activity.
type OpenSweepFn func(ctx context.Context, attack AdversaryAttack) ([]FindingRecord, error)

// DepthClassifyFn is the hook signature for the depth-classification
// sub-activity.
type DepthClassifyFn func(ctx context.Context, attack AdversaryAttack) ([]Classification, error)

// AdversaryAttack is the input to one adversarial pass invocation.
type AdversaryAttack struct {
	ArrowID      string
	PassID       string
	ProjectDir   string
	DepthClauses []Clause
	Requirements []Requirement
	Round        int
}

// Adversary is the coordinator. SINGLE-SHOT: each instance runs at
// most one Attack call (F3). Construct via NewAdversary; the harness
// layer creates a fresh Adversary per remediation round.
type Adversary struct {
	FindingsStore        *FindingsStore
	ClassificationsStore *ClassificationsStore
	Runner               *Runner
	DepthLadder          *DepthLadder
	OpenSweep            OpenSweepFn
	Classify             DepthClassifyFn
	IDGen                func() string
	Now                  func() time.Time
	AdversaryRole        string

	used atomic.Bool
}

// NewAdversary returns an Adversary with required fields populated
// AND defaults applied eagerly. Subsequent direct-field assignment
// (e.g., a.OpenSweep = customHook) MUST happen before the first
// Attack call; an in-flight Attack's hook snapshot cannot race.
func NewAdversary(findings *FindingsStore, classifications *ClassificationsStore, runner *Runner) *Adversary {
	a := &Adversary{
		FindingsStore:        findings,
		ClassificationsStore: classifications,
		Runner:               runner,
	}
	a.ApplyDefaults()
	return a
}

// ApplyDefaults populates default hooks / clock / IDGen / role. Idempotent.
// Called by NewAdversary; exposed for direct-struct construction.
func (a *Adversary) ApplyDefaults() {
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

// AttackReport summarizes one adversarial pass.
type AttackReport struct {
	ArrowID                 string
	Round                   int
	ClauseFalsifications    []ClauseFalsificationResult
	OpenSweepFindings       []string
	DepthBelowMinFindings   []string
	ResolvedFindings        []string // F16: prior below-min findings auto-resolved on re-classify
	ClassificationsRecorded int
	HarnessErrors           []string
}

// ClauseFalsificationResult is one entry in AttackReport.ClauseFalsifications.
type ClauseFalsificationResult struct {
	ClauseID    string
	Falsified   bool
	Unevaluated bool // F4: clause ended Unevaluated rather than Pass/Fail
	FindingID   string
	EvaluatorID string
	RunError    string
}

// RaisedThisRound reports whether THIS round's attack raised any
// finding (clause-falsification, open-sweep, depth-below-min).
// NOTE: this is round-local — for cross-round state consult
// FindingsStore.ForArrow.
func (r AttackReport) RaisedThisRound() bool {
	return len(r.OpenSweepFindings) > 0 ||
		len(r.DepthBelowMinFindings) > 0 ||
		anyFalsifiedOrUnevaluated(r.ClauseFalsifications)
}

// AnyOpen is the legacy alias for RaisedThisRound. Kept for callers
// upgraded incrementally; new code SHOULD use RaisedThisRound (F19).
//
// Deprecated: use RaisedThisRound; the name "AnyOpen" misleads
// readers into thinking it consults the FindingsStore.
func (r AttackReport) AnyOpen() bool {
	return r.RaisedThisRound()
}

func anyFalsifiedOrUnevaluated(xs []ClauseFalsificationResult) bool {
	for _, x := range xs {
		if x.Falsified || x.Unevaluated {
			return true
		}
	}
	return false
}

// Attack runs one round of the adversarial phase. Single-shot:
// returns ErrAdversaryAlreadyUsed on second call (F3).
func (a *Adversary) Attack(ctx context.Context, attack AdversaryAttack) (*AttackReport, error) {
	// Required-field validation BEFORE the used-flag flip so a
	// misconfigured Adversary doesn't burn the single-shot.
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
	if !a.used.CompareAndSwap(false, true) {
		return nil, ErrAdversaryAlreadyUsed
	}
	a.ApplyDefaults() // idempotent; covers direct-construction callers

	// F2: snapshot hooks into locals so a concurrent operator-side
	// mutation (post-construct hook install) doesn't race the in-
	// flight attack.
	openSweep := a.OpenSweep
	classify := a.Classify
	idGen := a.IDGen
	nowFn := a.Now
	role := a.AdversaryRole
	ladder := a.DepthLadder

	stamp := func() string {
		return nowFn().UTC().Format(time.RFC3339Nano) // F17: per-finding nano precision
	}

	report := &AttackReport{
		ArrowID: attack.ArrowID,
		Round:   attack.Round,
	}

	// Pre-declare requirements (idempotent across rounds).
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
		// Diamond v4 / R5 closure: ALWAYS namespace adversarial-phase
		// clauseIDs by wrapping the declared ID with /adv/round<N>.
		// v1's H6 closure only namespaced the auto-synthesis branch;
		// R5 flagged that a declared-ClauseID arrow would collide with
		// the verification phase across rounds. The rewrite handles
		// both cases.
		declared := cls.ClauseID
		var clauseID string
		if declared != "" {
			clauseID = fmt.Sprintf("%s/adv/round%d", declared, attack.Round)
		} else {
			clauseID = fmt.Sprintf("%s/adv/%s/round%d", attack.PassID, cls.Concept, attack.Round)
		}
		cls.ArrowID = attack.ArrowID
		if cls.ProjectDir == "" {
			cls.ProjectDir = attack.ProjectDir
		}
		// F38: every clause in this attack shares attack.PassID by
		// design — the pass-id keys the whole adversarial round; the
		// clauseID disambiguates per-clause. Engine-side persistence
		// aggregates EvaluationRuns by PassID.
		run, err := a.Runner.Evaluate(ctx, clauseID, attack.PassID, cls)
		entry := ClauseFalsificationResult{ClauseID: clauseID}
		if run != nil {
			entry.EvaluatorID = run.Evaluator.String()
		}
		switch {
		case err != nil:
			entry.RunError = err.Error()
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("falsify %q: %v", clauseID, err))
		case run != nil && run.EndStatus == StatusFail:
			entry.Falsified = true
			fid := idGen()
			err := a.FindingsStore.Raise(FindingRecord{
				ID:           fid,
				ArrowID:      attack.ArrowID,
				Type:         FindingTypeClauseFalsification,
				Severity:     SeverityHigh,
				Status:       FindingStatusOpen,
				Description:  sanitizeOneLine(fmt.Sprintf("clause %s falsified (round %d)", clauseID, attack.Round)),
				RaisedAt:     stamp(),
				RaisedByRole: role,
			})
			if err != nil {
				report.HarnessErrors = append(report.HarnessErrors,
					fmt.Sprintf("raise clause-falsification %q: %v", clauseID, err))
			} else {
				entry.FindingID = fid
			}
		case run != nil && run.EndStatus == StatusUnevaluated:
			// F4: clause ended Unevaluated (no model, missing dep).
			// Per gates.md §11 this is a depth-sensitive signal that
			// must NOT be silently treated as a pass.
			entry.Unevaluated = true
			fid := idGen()
			err := a.FindingsStore.Raise(FindingRecord{
				ID:      fid,
				ArrowID: attack.ArrowID,
				Type:    FindingTypeClauseFalsification,
				// Severity is meaningless on Unevaluated; per
				// gates.md §7.3 it propagates regardless of rank.
				// Use SeverityInfo so the int validates cleanly.
				Severity:     SeverityInfo,
				Status:       FindingStatusUnevaluated,
				Description:  sanitizeOneLine(fmt.Sprintf("clause %s ended Unevaluated (round %d)", clauseID, attack.Round)),
				RaisedAt:     stamp(),
				RaisedByRole: role,
			})
			if err != nil {
				report.HarnessErrors = append(report.HarnessErrors,
					fmt.Sprintf("raise unevaluated %q: %v", clauseID, err))
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
	openFindings, sweepErr := safeInvokeOpenSweep(ctx, openSweep, attack)
	if sweepErr != nil {
		report.HarnessErrors = append(report.HarnessErrors, fmt.Sprintf("open-sweep: %v", sweepErr))
	}
	for _, f := range openFindings {
		f.ArrowID = attack.ArrowID
		if f.Type == "" {
			f.Type = FindingTypeOpenSweep
		}
		if f.Status == FindingStatus(0) {
			f.Status = FindingStatusOpen
		}
		if f.ID == "" {
			f.ID = idGen()
		}
		if f.RaisedAt == "" {
			f.RaisedAt = stamp()
		}
		if f.RaisedByRole == "" {
			f.RaisedByRole = role
		}
		// F11: sanitize operator-supplied Description before persisting.
		f.Description = sanitizeOneLine(f.Description)
		if err := a.FindingsStore.Raise(f); err != nil {
			// F18: a bad open-sweep finding (Severity=99, bad Type,
			// etc.) is suspicious — raise a synthetic harness-error
			// finding so the gap surfaces in the FindingsStore rather
			// than silently disappearing.
			report.HarnessErrors = append(report.HarnessErrors,
				fmt.Sprintf("raise open-sweep %q: %v", f.ID, err))
			synthID := idGen()
			_ = a.FindingsStore.Raise(FindingRecord{
				ID:       synthID,
				ArrowID:  attack.ArrowID,
				Type:     FindingTypeOpenSweep,
				Severity: SeverityMedium,
				Status:   FindingStatusOpen,
				Description: sanitizeOneLine(fmt.Sprintf(
					"open-sweep hook produced an invalid finding (rejected by raise): %v", err)),
				RaisedAt:     stamp(),
				RaisedByRole: role,
			})
			continue
		}
		report.OpenSweepFindings = append(report.OpenSweepFindings, f.ID)
	}

	// 3. Depth classification.
	if err := ctx.Err(); err != nil {
		return report, err
	}
	classifications, classifyErr := safeInvokeClassify(ctx, classify, attack)
	if classifyErr != nil {
		report.HarnessErrors = append(report.HarnessErrors, fmt.Sprintf("classify: %v", classifyErr))
	}
	minByReq := make(map[string]DepthRank, len(attack.Requirements))
	for _, r := range attack.Requirements {
		minByReq[r.ID] = r.MinDepth
	}
	for _, c := range classifications {
		// F11: sanitize evidence before persistence.
		c.Evidence = sanitizeOneLine(c.Evidence)
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
			fid := idGen()
			err := a.FindingsStore.Raise(FindingRecord{
				ID:       fid,
				ArrowID:  attack.ArrowID,
				Type:     FindingTypeDepthBelowMin,
				Severity: SeverityHigh,
				Status:   FindingStatusOpen,
				Description: sanitizeOneLine(fmt.Sprintf(
					"requirement %s observed at %s (rank %d) < min %s (rank %d): %s",
					c.RequirementID,
					ladder.Label(c.Observed), c.Observed,
					ladder.Label(min), min,
					c.Evidence,
				)),
				RaisedAt:     stamp(),
				RaisedByRole: role,
			})
			if err != nil {
				report.HarnessErrors = append(report.HarnessErrors,
					fmt.Sprintf("raise depth-below-min: %v", err))
			} else {
				report.DepthBelowMinFindings = append(report.DepthBelowMinFindings, fid)
			}
		} else {
			// F16: re-classification observed Above-or-equal-to min.
			// Search for prior open depth-below-min findings on this
			// (arrow, requirement) and auto-resolve them. Without this
			// step, a successful remediation leaves a stale finding
			// open and the arrow can never close.
			for _, prior := range a.FindingsStore.ForArrow(attack.ArrowID) {
				if prior.Type != FindingTypeDepthBelowMin {
					continue
				}
				if prior.Status != FindingStatusOpen && prior.Status != FindingStatusRunning {
					continue
				}
				if !strings.Contains(prior.Description, fmt.Sprintf("requirement %s ", c.RequirementID)) {
					continue
				}
				if err := a.FindingsStore.TransitionWithReason(prior.ID,
					FindingStatusResolved, role,
					fmt.Sprintf("re-classified at %s (≥ min) in round %d", ladder.Label(c.Observed), attack.Round)); err == nil {
					report.ResolvedFindings = append(report.ResolvedFindings, prior.ID)
				}
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

// safeInvokeOpenSweep wraps the open-sweep hook with panic recovery
// (F5). A panic becomes a harness error, not a goroutine crash.
func safeInvokeOpenSweep(ctx context.Context, fn OpenSweepFn, attack AdversaryAttack) (findings []FindingRecord, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("open-sweep hook panicked: %v", r)
		}
	}()
	return fn(ctx, attack)
}

// safeInvokeClassify wraps the depth-classify hook with panic recovery.
func safeInvokeClassify(ctx context.Context, fn DepthClassifyFn, attack AdversaryAttack) (cls []Classification, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("classify hook panicked: %v", r)
		}
	}()
	return fn(ctx, attack)
}

// adversaryFindingSeq is the per-process counter that disambiguates
// finding IDs produced at the same nanosecond.
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
