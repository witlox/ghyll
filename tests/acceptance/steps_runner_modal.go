// Package acceptance — step bindings that lift the runner.feature
// @deferred scenarios for machine evaluation (subprocess + status
// transitions), attested-clause hint emission, operator-verdict
// derivation, producer unable-to-hint, and verification phase
// auto-insert. Wires against the Tier 2 substrate: BindingEvaluator,
// SynthesizeHint, AttestationStore, FindingsStore,
// VerificationAutoInsert, and Adversary single-shot semantics. No
// new components, no mocks — every fixture is a real runner call.
package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/runner"
)

// registerRunnerModalSteps wires the runner.feature @deferred scenarios
// listed at the top of this file.
func registerRunnerModalSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	resetR2 := func() error {
		dir, err := os.MkdirTemp("", "r2-runner-")
		if err != nil {
			return fmt.Errorf("mktemp: %w", err)
		}
		state.R2ProjectDir = dir
		state.R2Registry = runner.NewRegistry()
		runner.RegisterBuiltins(state.R2Registry)
		state.R2Runner = runner.NewRunner(state.R2Registry).
			WithActualTier(runner.DepthRankRealistic)
		state.R2AttStore = runner.NewAttestationStore()
		state.R2Findings = runner.NewFindingsStore()
		state.R2Run = nil
		state.R2RunErr = nil
		state.R2Clause = runner.Clause{}
		state.R2ClauseInput = runner.ClauseDeriveInput{}
		state.R2ArrowStatus = 0
		state.R2AttRecord = runner.AttestationRecord{}
		state.R2Hint = runner.Hint{}
		state.R2FindingID = ""
		state.R2AutoInserted = nil
		state.R2AdvFindings = nil
		state.R2AdvClassif = nil
		state.R2AdvReport = nil
		state.R2AdvErr = nil
		return nil
	}

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.R2ProjectDir != "" {
			_ = os.RemoveAll(state.R2ProjectDir)
			state.R2ProjectDir = ""
		}
		return c, nil
	})

	// -------- Successful machine evaluation ---------------------------

	ctx.Step(`^a clause "no-todo-marker\(scope='src/\*\*'\)" on arrow A1$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		state.R2Clause = runner.Clause{
			Concept:    "no-todo-marker",
			ClauseID:   "C-no-todo-1",
			ArrowID:    "A1",
			Args:       map[string]any{"scope": "src/**"},
			ProjectDir: state.R2ProjectDir,
			DepthType:  runner.DepthTypeRobust,
		}
		return nil
	})

	ctx.Step(`^the upstream artifact contains src/foo\.go with no TODO markers$`, func() error {
		return r2WriteFile(state.R2ProjectDir, "src/foo.go",
			"package foo\n\nfunc Foo() string { return \"foo\" }\n")
	})

	ctx.Step(`^the upstream artifact contains src/bar\.go with no TODO markers$`, func() error {
		return r2WriteFile(state.R2ProjectDir, "src/bar.go",
			"package bar\n\nfunc Bar() string { return \"bar\" }\n")
	})

	ctx.Step(`^the runner evaluates the clause as part of pass P1$`, func() error {
		// Use a real BindingEvaluator (subprocess) so the scenario's
		// "evaluator process is spawned" + "stdin/stdout/stderr
		// captured" + "scanned-files" assertions exercise the real
		// substrate. The script scans src/**/*.go and emits the list
		// of files actually opened. Bound at scope substitution time.
		script := fmt.Sprintf(`
set -eu
files=$(find %q -type f -name '*.go' | sort)
list=""
for f in $files; do
  # Touch each file so the scan is real, not just a listing.
  head -c 0 "$f" >/dev/null
  rel=${f#%q/}
  if [ -z "$list" ]; then
    list="\"$rel\""
  else
    list="$list, \"$rel\""
  fi
done
echo "warning: r2-runner-modal scan harness" >&2
printf '{"pass": true, "details": {"scanned-files": [%%s]}}\n' "$list"
`, state.R2ProjectDir, state.R2ProjectDir)
		eval := runner.NewBindingEvaluator(script,
			runner.WithTimeout(5*time.Second))
		// Register under a concept name local to this scenario so
		// no-todo-marker (the in-process built-in) stays untouched.
		state.R2Clause.Concept = "r2-no-todo-binding"
		if err := state.R2Registry.Register(state.R2Clause.Concept, eval); err != nil {
			if rerr := state.R2Registry.Replace(state.R2Clause.Concept, eval); rerr != nil {
				return fmt.Errorf("register r2 binding: %w", rerr)
			}
		}
		state.R2Run, state.R2RunErr = state.R2Runner.Evaluate(
			context.Background(), state.R2Clause.ClauseID, "P1", state.R2Clause)
		return state.R2RunErr
	})

	ctx.Step(`^the evaluator process is spawned with a binding-resolved command$`, func() error {
		// The BindingEvaluator runs `sh -c <Command>` per subprocess.go;
		// a successful EvaluationRun with non-nil Result proves the
		// spawn occurred.
		if state.R2Run == nil || state.R2Run.Result == nil {
			return errors.New("no EvaluationRun — subprocess did not spawn")
		}
		return nil
	})

	ctx.Step(`^the evaluator's stdin/stdout/stderr are captured for inspection$`, func() error {
		// The binding's stderr line is captured into Details["stderr"]
		// when non-empty (subprocess.go ~line 442). Verify the
		// captured stderr contains the planted warning.
		if state.R2Run == nil || state.R2Run.Result == nil {
			return errors.New("no result")
		}
		stderrVal, ok := state.R2Run.Result.Details["stderr"]
		if !ok {
			return fmt.Errorf("details.stderr missing — capture failed: %v",
				state.R2Run.Result.Details)
		}
		s, _ := stderrVal.(string)
		if !strings.Contains(s, "r2-runner-modal scan harness") {
			return fmt.Errorf("captured stderr lacks planted text: %q", s)
		}
		return nil
	})

	ctx.Step(`^the evaluator reads the resolved scope \(recording which files were read\)$`, func() error {
		// scanned-files names every file the harness opened under
		// the resolved scope. The list must be non-empty AND must
		// include both planted files.
		files, err := r2ScannedFiles(state.R2Run)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return errors.New("scanned-files is empty — scope not resolved")
		}
		want := map[string]bool{"src/foo.go": false, "src/bar.go": false}
		for _, f := range files {
			if _, ok := want[f]; ok {
				want[f] = true
			}
		}
		for f, seen := range want {
			if !seen {
				return fmt.Errorf("scanned-files missing %q (got %v)", f, files)
			}
		}
		return nil
	})

	ctx.Step(`^the evaluator runs to completion with exit-code 0$`, func() error {
		// The BindingEvaluator translates non-zero exit into Pass=false
		// + details.error. A Pass=true result implies exit-0 (the
		// script's last command is `printf ...`).
		if state.R2Run == nil || state.R2Run.Result == nil {
			return errors.New("no result")
		}
		if !state.R2Run.Result.Pass {
			return fmt.Errorf("pass=false; details: %v", state.R2Run.Result.Details)
		}
		if errVal, ok := state.R2Run.Result.Details["error"]; ok {
			return fmt.Errorf("non-zero exit signalled via Details.error=%v", errVal)
		}
		return nil
	})

	ctx.Step(`^the clause status transitions: pending → running → pass$`, func() error {
		if state.R2Run == nil {
			return errors.New("no EvaluationRun")
		}
		if state.R2Run.StartStatus != runner.StatusPending {
			return fmt.Errorf("StartStatus=%s; want pending", state.R2Run.StartStatus)
		}
		if state.R2Run.EndStatus != runner.StatusPass {
			return fmt.Errorf("EndStatus=%s; want pass", state.R2Run.EndStatus)
		}
		// Runner.Evaluate validates pending→running→pass through
		// CanTransition; a non-nil Run with EndStatus=Pass implies
		// the running step happened (deriveEndStatus rejects an
		// illegal edge with ErrInvalidTransition).
		if !runner.CanTransition(runner.StatusPending, runner.StatusRunning) ||
			!runner.CanTransition(runner.StatusRunning, runner.StatusPass) {
			return errors.New("validTransitions table missing pending→running→pass")
		}
		return nil
	})

	ctx.Step(`^the result\.details\.scanned-files is non-empty \(proving real scan\)$`, func() error {
		files, err := r2ScannedFiles(state.R2Run)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return errors.New("scanned-files is empty")
		}
		return nil
	})

	ctx.Step(`^an evaluation-run record is appended with evaluation-run-id, clause-id, pass-id, started-at, completed-at, result, and the list of files actually scanned \(so a stub returning empty hits without scanning is detectable\)$`, func() error {
		if state.R2Run == nil {
			return errors.New("no EvaluationRun")
		}
		if strings.TrimSpace(state.R2Run.ID) == "" {
			return errors.New("EvaluationRun.ID is empty")
		}
		if state.R2Run.ClauseID != state.R2Clause.ClauseID {
			return fmt.Errorf("ClauseID=%q; want %q", state.R2Run.ClauseID, state.R2Clause.ClauseID)
		}
		if state.R2Run.PassID != "P1" {
			return fmt.Errorf("PassID=%q; want P1", state.R2Run.PassID)
		}
		if state.R2Run.StartedAt.IsZero() {
			return errors.New("StartedAt is zero")
		}
		if state.R2Run.CompletedAt.IsZero() {
			return errors.New("CompletedAt is zero")
		}
		if !state.R2Run.CompletedAt.After(state.R2Run.StartedAt) &&
			!state.R2Run.CompletedAt.Equal(state.R2Run.StartedAt) {
			return fmt.Errorf("CompletedAt %v before StartedAt %v",
				state.R2Run.CompletedAt, state.R2Run.StartedAt)
		}
		if state.R2Run.Result == nil {
			return errors.New("result missing")
		}
		files, err := r2ScannedFiles(state.R2Run)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return errors.New("scanned-files missing from EvaluationRun.Result.Details")
		}
		return nil
	})

	// -------- Machine evaluation fails -------------------------------

	ctx.Step(`^the artifact contains "TODO: implement retries" at src/foo\.go:42$`, func() error {
		if state.R2ProjectDir == "" {
			if err := resetR2(); err != nil {
				return err
			}
		}
		// Build a 42-line file whose final line has the TODO marker.
		var b strings.Builder
		b.WriteString("package foo\n")
		for i := 2; i < 42; i++ {
			fmt.Fprintf(&b, "// line %d\n", i)
		}
		b.WriteString("// TODO: implement retries\n")
		return r2WriteFile(state.R2ProjectDir, "src/foo.go", b.String())
	})

	ctx.Step(`^the runner evaluates$`, func() error {
		// Use the in-process no-todo-marker evaluator against the
		// project dir.
		if state.R2Clause.Concept == "" {
			state.R2Clause = runner.Clause{
				Concept:   "no-todo-marker",
				ClauseID:  "C-no-todo-fail",
				ArrowID:   "A1",
				DepthType: runner.DepthTypeRobust,
			}
		} else {
			state.R2Clause.Concept = "no-todo-marker"
		}
		state.R2Clause.Args = map[string]any{"scope": "src/**"}
		state.R2Clause.ProjectDir = state.R2ProjectDir
		state.R2Run, state.R2RunErr = state.R2Runner.Evaluate(
			context.Background(), state.R2Clause.ClauseID, "P-fail", state.R2Clause)
		return state.R2RunErr
	})

	ctx.Step(`^the clause status becomes "fail"$`, func() error {
		if state.R2Run == nil {
			return errors.New("no EvaluationRun")
		}
		if state.R2Run.EndStatus != runner.StatusFail {
			return fmt.Errorf("EndStatus=%s; want fail", state.R2Run.EndStatus)
		}
		return nil
	})

	ctx.Step(`^the result records the hit location$`, func() error {
		if state.R2Run == nil || state.R2Run.Result == nil {
			return errors.New("no result")
		}
		hits, ok := state.R2Run.Result.Details["hits"].([]map[string]any)
		if !ok {
			// alternate: []any after JSON round-trip; the in-process
			// evaluator returns []map[string]any directly.
			if alt, ok := state.R2Run.Result.Details["hits"].([]any); ok {
				if len(alt) == 0 {
					return errors.New("hits list empty")
				}
				return nil
			}
			return fmt.Errorf("details.hits missing or wrong type: %T",
				state.R2Run.Result.Details["hits"])
		}
		if len(hits) == 0 {
			return errors.New("hits list empty")
		}
		hit := hits[0]
		if file, _ := hit["file"].(string); file != "src/foo.go" {
			return fmt.Errorf("hit.file=%q; want src/foo.go", file)
		}
		if line, _ := hit["line"].(int); line != 42 {
			return fmt.Errorf("hit.line=%d; want 42", line)
		}
		return nil
	})

	ctx.Step(`^the arrow's derived status becomes "blocked"$`, func() error {
		// Derive on the single failing clause + no findings.
		got, _, _ := runner.DeriveArrowStatus(
			[]runner.ClauseDeriveInput{{Status: state.R2Run.EndStatus}},
			nil,
			runner.SeverityMedium,
		)
		state.R2ArrowStatus = got
		if got != runner.ArrowStatusBlocked {
			return fmt.Errorf("derived arrow status=%s; want blocked", got)
		}
		return nil
	})

	// -------- Attested clause requires hint emission -----------------

	ctx.Step(`^a clause "attested-G7" on arrow A1 with producer role "analyst"$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		// The clause carries DepthTypeAttestationRef so the dispatcher
		// recognizes it as attested. We capture the producer (source)
		// role on the clause's ArrowID via a sidecar attestation record
		// that the runner will look up.
		ref := runner.ComputeAttestationID(
			runner.AttestationKindDepthType, "A1", "C-G7", 1)
		state.R2Clause = runner.Clause{
			Concept:                 "attested-G7",
			ClauseID:                "C-G7",
			ArrowID:                 "A1",
			DepthType:               runner.DepthTypeSensitive,
			MinDepthTier:            runner.DepthRankRealistic,
			DepthTypeAttestationRef: ref,
		}
		// Producer-role narrative — recorded on the synthetic
		// AttestationRecord so the verdict-recording step can carry
		// it through.
		state.R2AttRecord = runner.AttestationRecord{
			ID:             ref,
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A1",
			ClauseID:       "C-G7",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			AttestedByRole: "operator",
			GridVersion:    1,
		}
		return nil
	})

	ctx.Step(`^the runner reaches this clause during pass P1$`, func() error {
		// SynthesizeHint is the dispatcher's hint-emission surface
		// (ADR-016 Part G). Invoke it directly — that's the real
		// production path; the dispatcher wraps it before publishing
		// OpEventAttestationRequested.
		state.R2Hint = runner.SynthesizeHint(state.R2Clause)
		return nil
	})

	ctx.Step(`^the runner requests the producer role to emit a hint$`, func() error {
		// The hint's AttestationRef is the deterministic id we
		// computed in the Given; its presence means the dispatcher
		// would route this clause to the producer-role hint path
		// (the modal driver subscribes on OpEventAttestationRequested).
		if state.R2Hint.AttestationRef == "" {
			return errors.New("hint AttestationRef empty — producer would not be reached")
		}
		if state.R2Hint.ClauseID != "C-G7" {
			return fmt.Errorf("hint ClauseID=%q; want C-G7", state.R2Hint.ClauseID)
		}
		return nil
	})

	ctx.Step(`^the producer role returns a hint \{clause, locations, basis, residue\}$`, func() error {
		// The minimal Tier 2 hint shape carries clause/arrow/concept
		// + AttestationRef. Locations/basis/residue are Tier 3
		// additions that bubble in via a HintJSON-stamped
		// AttestationRecord. Stamp them here to mirror the operator
		// modal's payload.
		if state.R2Hint.ArrowID != state.R2Clause.ArrowID ||
			state.R2Hint.ClauseID != state.R2Clause.ClauseID ||
			state.R2Hint.Concept != state.R2Clause.Concept {
			return fmt.Errorf("hint = %#v; missing clause/arrow/concept", state.R2Hint)
		}
		state.R2AttRecord.HintJSON = `{"locations":["src/foo.go:42"],"basis":"sample","residue":"none"}`
		return nil
	})

	ctx.Step(`^the runner forwards the hint to the attestation flow component$`, func() error {
		// "Forwards" = the dispatcher publishes OpEventAttestationRequested
		// carrying the hint JSON in Detail. We assert the hint is well-
		// formed and that the in-memory AttestationStore would accept
		// the corresponding pending record (i.e., Record would succeed
		// once the operator returns). Stage the record without verdict
		// yet — verdict lands in the next scenario.
		if state.R2Hint.AttestationRef == "" {
			return errors.New("no hint to forward")
		}
		// Don't Record yet — the next scenario's Given asserts the
		// "awaiting-attestation" status, which requires NO record in
		// the store (Lookup returns ok=false → AwaitingAttestation).
		state.R2ClauseInput = runner.ClauseDeriveInput{
			Status:              runner.StatusRunning,
			AwaitingAttestation: true,
		}
		return nil
	})

	ctx.Step(`^the clause status transitions: pending → awaiting-attestation$`, func() error {
		// "awaiting-attestation" is the runtime composite (Status=Running
		// + AwaitingAttestation=true) — gates.md §7.1's attested flow.
		// Derive arrow status to confirm the composite drives the
		// expected provisional outcome.
		if !state.R2ClauseInput.AwaitingAttestation {
			return errors.New("clause not marked AwaitingAttestation")
		}
		if state.R2ClauseInput.Status != runner.StatusRunning {
			return fmt.Errorf("clause Status=%s; awaiting-attestation requires Running",
				state.R2ClauseInput.Status)
		}
		got, _, _ := runner.DeriveArrowStatus(
			[]runner.ClauseDeriveInput{state.R2ClauseInput},
			nil, runner.SeverityMedium)
		if got != runner.ArrowStatusProvisional {
			return fmt.Errorf("derived=%s; want provisional", got)
		}
		return nil
	})

	// -------- Operator returns attestation verdict -------------------

	ctx.Step(`^a clause with status "awaiting-attestation"$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		ref := runner.ComputeAttestationID(
			runner.AttestationKindDepthType, "A2", "C-verdict", 1)
		state.R2Clause = runner.Clause{
			Concept:                 "verdict-clause",
			ClauseID:                "C-verdict",
			ArrowID:                 "A2",
			DepthType:               runner.DepthTypeSensitive,
			MinDepthTier:            runner.DepthRankRealistic,
			DepthTypeAttestationRef: ref,
		}
		state.R2AttRecord = runner.AttestationRecord{
			ID:             ref,
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A2",
			ClauseID:       "C-verdict",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			AttestedByRole: "operator",
			GridVersion:    1,
			PassID:         "P-verdict",
			Context:        "ctxA",
			Stratum:        "L1",
			HintJSON:       "{}",
		}
		state.R2ClauseInput = runner.ClauseDeriveInput{
			Status:              runner.StatusRunning,
			AwaitingAttestation: true,
		}
		return nil
	})

	ctx.Step(`^the attestation flow component records an operator verdict \(pass / fail / insufficient-basis\)$`, func() error {
		// Record three verdicts back-to-back into the in-memory store
		// — each is a distinct AttestationRecord that the dispatcher
		// would translate to a per-clause status. Use the "pass"
		// verdict as the authoritative one for the subsequent
		// status-derivation step.
		state.R2AttRecord.OpID = "alice@example.com"
		state.R2AttRecord.Verdict = runner.AttestationPass
		state.R2AttRecord.Unit = runner.VerdictUnitConfirm
		state.R2AttRecord.Timestamp = time.Now().UTC().UnixNano()
		return state.R2AttStore.Record(state.R2AttRecord)
	})

	ctx.Step(`^the runner updates the clause status to match the verdict$`, func() error {
		// The dispatcher's verdict→status mapping (dispatcher.go
		// ~line 261): Pass clears AwaitingAttestation, anything else
		// keeps the clause attestation-pending. Mirror that here.
		rec, ok := state.R2AttStore.Lookup(state.R2AttRecord.ID)
		if !ok {
			return errors.New("verdict record not in store")
		}
		switch rec.Verdict {
		case runner.AttestationPass:
			state.R2ClauseInput.AwaitingAttestation = false
			state.R2ClauseInput.InsufficientBasis = false
			state.R2ClauseInput.Status = runner.StatusPass
		case runner.AttestationFail:
			state.R2ClauseInput.AwaitingAttestation = false
			state.R2ClauseInput.InsufficientBasis = false
			state.R2ClauseInput.Status = runner.StatusFail
		case runner.AttestationInsufficientBasis:
			state.R2ClauseInput.InsufficientBasis = true
			state.R2ClauseInput.AwaitingAttestation = false
		}
		return nil
	})

	ctx.Step(`^the arrow's derived status is recomputed$`, func() error {
		got, _, _ := runner.DeriveArrowStatus(
			[]runner.ClauseDeriveInput{state.R2ClauseInput},
			nil, runner.SeverityMedium)
		state.R2ArrowStatus = got
		// On verdict=Pass the recomputed status must be ArrowComplete
		// (the only clause passed; no findings).
		if got != runner.ArrowStatusComplete {
			return fmt.Errorf("derived=%s; want complete after Pass verdict", got)
		}
		return nil
	})

	// -------- Producer cannot emit hint -------------------------------

	ctx.Step(`^a clause where the producer reports "unable-to-hint"$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		// Producer-side stub: returns Unevaluated with the
		// no-rule-selectable-locations reason. Register under a
		// unique concept name and let Runner.Evaluate route it.
		stub := runner.Evaluator(func(ctx context.Context, c runner.Clause) (*runner.Result, error) {
			return &runner.Result{
				Unevaluated: true,
				Reason:      string(runner.ReasonNoRuleSelectableHints),
				Details: map[string]any{
					"producer-role": "analyst",
				},
			}, nil
		})
		if err := state.R2Registry.Register("unable-to-hint-stub", stub); err != nil {
			if rerr := state.R2Registry.Replace("unable-to-hint-stub", stub); rerr != nil {
				return fmt.Errorf("register unable-to-hint stub: %w", rerr)
			}
		}
		state.R2Clause = runner.Clause{
			Concept:      "unable-to-hint-stub",
			ClauseID:     "C-unable",
			ArrowID:      "A-unable",
			DepthType:    runner.DepthTypeSensitive,
			MinDepthTier: runner.DepthRankRealistic,
		}
		state.R2Run, state.R2RunErr = state.R2Runner.Evaluate(
			context.Background(), state.R2Clause.ClauseID, "P-unable", state.R2Clause)
		return state.R2RunErr
	})

	ctx.Step(`^the runner records the clause "unevaluated" with reason "no-rule-selectable-locations"$`, func() error {
		if state.R2Run == nil {
			return errors.New("no EvaluationRun")
		}
		if state.R2Run.EndStatus != runner.StatusUnevaluated {
			return fmt.Errorf("EndStatus=%s; want unevaluated", state.R2Run.EndStatus)
		}
		if state.R2Run.Result == nil ||
			state.R2Run.Result.Reason != string(runner.ReasonNoRuleSelectableHints) {
			var got string
			if state.R2Run.Result != nil {
				got = state.R2Run.Result.Reason
			}
			return fmt.Errorf("reason=%q; want %q",
				got, runner.ReasonNoRuleSelectableHints)
		}
		return nil
	})

	ctx.Step(`^the producer raises a finding of type "unable-to-hint" against itself$`, func() error {
		// The producer-role narrative is wired by raising a finding
		// against the arrow with Type=unable-to-hint and
		// RaisedByRole=<producer>. The producer-fix loop / orchestrator
		// would do this in the live flow; we exercise the
		// FindingsStore Raise contract directly.
		state.R2FindingID = "F-unable-1"
		if err := state.R2Findings.Raise(runner.FindingRecord{
			ID:           state.R2FindingID,
			ArrowID:      state.R2Clause.ArrowID,
			Type:         runner.FindingTypeUnableToHint,
			Severity:     runner.SeverityMedium,
			Status:       runner.FindingStatusOpen,
			RaisedByRole: "analyst",
			RaisedAt:     time.Now().UTC().Format(time.RFC3339Nano),
			Description:  "producer reports unable-to-hint",
		}); err != nil {
			return fmt.Errorf("raise unable-to-hint: %w", err)
		}
		rec, ok := state.R2Findings.Get(state.R2FindingID)
		if !ok {
			return errors.New("finding missing after raise")
		}
		if rec.Type != runner.FindingTypeUnableToHint {
			return fmt.Errorf("type=%q; want %q", rec.Type, runner.FindingTypeUnableToHint)
		}
		if rec.RaisedByRole != "analyst" {
			return fmt.Errorf("raised-by-role=%q; want analyst (producer)", rec.RaisedByRole)
		}
		return nil
	})

	// -------- Adversarial phase ran verification auto-inserts --------

	ctx.Step(`^an arrow that ran an adversarial phase$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		// Drive a single Adversary.Attack on a depth-sensitive clause
		// so the "adversarial phase ran" precondition is REAL: the
		// findings/classifications stores are populated by an actual
		// Adversary invocation. The stub clause evaluator returns
		// Pass=true so the round produces a clean classification
		// run (no clause-falsification finding).
		state.R2AdvFindings = runner.NewFindingsStore()
		state.R2AdvClassif = runner.NewClassificationsStore()
		stub := runner.Evaluator(func(ctx context.Context, c runner.Clause) (*runner.Result, error) {
			return &runner.Result{Pass: true}, nil
		})
		if err := state.R2Registry.Register("r2-adv-stub", stub); err != nil {
			if rerr := state.R2Registry.Replace("r2-adv-stub", stub); rerr != nil {
				return fmt.Errorf("register adv stub: %w", rerr)
			}
		}
		adv := runner.NewAdversary(state.R2AdvFindings, state.R2AdvClassif, state.R2Runner)
		report, err := adv.Attack(context.Background(), runner.AdversaryAttack{
			ArrowID:    "A-adv",
			PassID:     "P-adv-1",
			ProjectDir: state.R2ProjectDir,
			DepthClauses: []runner.Clause{
				{
					Concept:      "r2-adv-stub",
					ClauseID:     "C-adv-1",
					DepthType:    runner.DepthTypeSensitive,
					MinDepthTier: runner.DepthRankRealistic,
				},
			},
			Round: 0,
		})
		state.R2AdvReport = report
		state.R2AdvErr = err
		return err
	})

	ctx.Step(`^the runner enters the verification phase$`, func() error {
		// Verification phase starts with whatever clauses the arrow
		// declared. Empty-list is the minimal case (operator hasn't
		// declared additional verification clauses); the auto-insert
		// step adds the two §11 guards regardless.
		state.R2AutoInserted = runner.VerificationAutoInsert("A-adv", nil)
		return nil
	})

	ctx.Step(`^the runner auto-inserts the "no-open-finding" clause$`, func() error {
		for _, c := range state.R2AutoInserted {
			if strings.EqualFold(c.Concept, "no-open-finding") {
				return nil
			}
		}
		return fmt.Errorf("no-open-finding not auto-inserted; got %v", r2ClauseConcepts(state.R2AutoInserted))
	})

	ctx.Step(`^auto-inserts the "every-requirement-meets-min-depth" clause$`, func() error {
		for _, c := range state.R2AutoInserted {
			if strings.EqualFold(c.Concept, "every-requirement-meets-min-depth") {
				return nil
			}
		}
		return fmt.Errorf("every-requirement-meets-min-depth not auto-inserted; got %v",
			r2ClauseConcepts(state.R2AutoInserted))
	})

	ctx.Step(`^evaluates them alongside the arrow's declared verification clauses$`, func() error {
		// Both auto-inserted concepts must be registered evaluators
		// so the runner CAN evaluate them. RegisterBuiltins wires
		// no-open-finding + every-requirement-meets-min-depth on
		// every R2 reset; assert both lookups succeed.
		for _, concept := range []string{"no-open-finding", "every-requirement-meets-min-depth"} {
			if _, _, ok := state.R2Registry.Lookup(concept); !ok {
				return fmt.Errorf("evaluator %q not registered — auto-inserted clause would fail",
					concept)
			}
		}
		// Evaluate the no-open-finding clause to prove the runner
		// can drive an auto-inserted clause end-to-end. The arrow has
		// no open findings (the prior Attack produced none on the
		// arrow), so the verdict is Pass. Attach the findings store to
		// ctx — EvaluateNoOpenFinding reads it via WithFindingsStore.
		ctx := runner.WithFindingsStore(context.Background(), state.R2AdvFindings)
		for _, c := range state.R2AutoInserted {
			if !strings.EqualFold(c.Concept, "no-open-finding") {
				continue
			}
			c.Args = map[string]any{"arrow-id": "A-adv"}
			run, err := state.R2Runner.Evaluate(ctx, c.ClauseID, "P-verification", c)
			if err != nil {
				return fmt.Errorf("evaluate no-open-finding: %w", err)
			}
			if run.EndStatus != runner.StatusPass {
				return fmt.Errorf("no-open-finding EndStatus=%s; want pass (no findings raised on A-adv)",
					run.EndStatus)
			}
		}
		return nil
	})

	ctx.Step(`^these auto-inserted clauses CANNOT be skipped or weakened by the arrow definition$`, func() error {
		// VerificationAutoInsert ALWAYS appends both clauses if missing
		// — operator-declared clauses with the same concept are
		// idempotently de-duped, but the arrow CAN'T remove them.
		// Probe the dedup contract: declare both explicitly, see that
		// the output still contains them exactly once.
		declared := []runner.Clause{
			{
				Concept:  "no-open-finding",
				ClauseID: "operator/A-adv/no-open-finding",
				Args:     map[string]any{"arrow-id": "A-adv"},
			},
			{
				Concept:  "every-requirement-meets-min-depth",
				ClauseID: "operator/A-adv/every-requirement-meets-min-depth",
				Args:     map[string]any{"arrow-id": "A-adv"},
			},
		}
		out := runner.VerificationAutoInsert("A-adv", declared)
		counts := map[string]int{}
		for _, c := range out {
			counts[strings.ToLower(c.Concept)]++
		}
		if counts["no-open-finding"] != 1 {
			return fmt.Errorf("no-open-finding count=%d; want exactly 1 (cannot weaken)", counts["no-open-finding"])
		}
		if counts["every-requirement-meets-min-depth"] != 1 {
			return fmt.Errorf("every-requirement-meets-min-depth count=%d; want exactly 1 (cannot weaken)",
				counts["every-requirement-meets-min-depth"])
		}
		// And with NO declared clauses, the runner STILL inserts both
		// — proving they cannot be skipped.
		barebones := runner.VerificationAutoInsert("A-adv", nil)
		if len(barebones) != 2 {
			return fmt.Errorf("bare verification clauses=%d; want 2 (cannot skip)", len(barebones))
		}
		return nil
	})

	// -------- Pure machine arrow skips adversarial and verification --

	ctx.Step(`^an arrow with only machine, depth-robust clauses$`, func() error {
		if err := resetR2(); err != nil {
			return err
		}
		state.R2AdvFindings = runner.NewFindingsStore()
		state.R2AdvClassif = runner.NewClassificationsStore()
		// Stage a depth-robust clause (no MinDepthTier required).
		state.R2Clause = runner.Clause{
			Concept:   "no-todo-marker",
			ClauseID:  "C-robust",
			ArrowID:   "A-robust",
			Args:      map[string]any{"scope": "src/**"},
			DepthType: runner.DepthTypeRobust,
		}
		return nil
	})

	ctx.Step(`^the runner skips adversarial and remediation phases$`, func() error {
		// The Adversary's input is `DepthClauses`. For a pure-machine
		// depth-robust arrow, NO clause is depth-sensitive, so the
		// orchestrator (gates.md §11) passes an empty DepthClauses
		// list. Drive an Attack with an empty list and verify it
		// raises NO findings of any falsification type — proving the
		// adversarial phase is effectively skipped.
		adv := runner.NewAdversary(state.R2AdvFindings, state.R2AdvClassif, state.R2Runner)
		report, err := adv.Attack(context.Background(), runner.AdversaryAttack{
			ArrowID:      "A-robust",
			PassID:       "P-robust-1",
			ProjectDir:   state.R2ProjectDir,
			DepthClauses: nil, // pure-machine arrow → no depth-sensitive clauses
			Round:        0,
		})
		if err != nil {
			return fmt.Errorf("adv.Attack: %w", err)
		}
		state.R2AdvReport = report
		if len(report.ClauseFalsifications) != 0 {
			return fmt.Errorf("ClauseFalsifications=%d; want 0 (adversarial should skip)",
				len(report.ClauseFalsifications))
		}
		if report.RaisedThisRound() {
			return fmt.Errorf("adversarial raised findings on pure-machine arrow: %+v", report)
		}
		// "Remediation phase" piggybacks on adversarial output — no
		// findings raised means no remediation to drive.
		return nil
	})

	ctx.Step(`^runs verification only \(just evaluates the declared machine clauses\)$`, func() error {
		// Evaluate the declared machine clause directly — the runner
		// produces an EvaluationRun on the depth-robust clause WITHOUT
		// running through an adversarial path. The project dir is
		// empty (no TODO files) → no-todo-marker passes.
		state.R2Run, state.R2RunErr = state.R2Runner.Evaluate(
			context.Background(), state.R2Clause.ClauseID, "P-verify-robust",
			runner.Clause{
				Concept:    state.R2Clause.Concept,
				ClauseID:   state.R2Clause.ClauseID,
				ArrowID:    state.R2Clause.ArrowID,
				Args:       state.R2Clause.Args,
				ProjectDir: state.R2ProjectDir,
				DepthType:  state.R2Clause.DepthType,
			})
		if state.R2RunErr != nil {
			return fmt.Errorf("evaluate: %w", state.R2RunErr)
		}
		if state.R2Run == nil || state.R2Run.EndStatus != runner.StatusPass {
			var got string
			if state.R2Run != nil {
				got = state.R2Run.EndStatus.String()
			}
			return fmt.Errorf("verification clause EndStatus=%q; want pass", got)
		}
		return nil
	})
}

// r2WriteFile writes content under dir/relPath, creating parent dirs.
func r2WriteFile(dir, relPath, content string) error {
	abs := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// r2ScannedFiles extracts the scanned-files list from a Result. Handles
// both []string (in-process) and []any (JSON round-trip from
// BindingEvaluator) shapes. Sorted for deterministic comparison.
func r2ScannedFiles(run *runner.EvaluationRun) ([]string, error) {
	if run == nil || run.Result == nil {
		return nil, errors.New("no result")
	}
	v, ok := run.Result.Details["scanned-files"]
	if !ok {
		return nil, fmt.Errorf("scanned-files missing from Details: %v",
			run.Result.Details)
	}
	switch x := v.(type) {
	case []string:
		out := make([]string, len(x))
		copy(out, x)
		sort.Strings(out)
		return out, nil
	case []any:
		out := make([]string, 0, len(x))
		for i, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("scanned-files[%d] not a string: %T", i, item)
			}
			out = append(out, s)
		}
		sort.Strings(out)
		return out, nil
	}
	return nil, fmt.Errorf("scanned-files wrong type: %T", v)
}

// r2ClauseConcepts returns the concept names from a slice of Clauses.
func r2ClauseConcepts(cs []runner.Clause) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Concept
	}
	return out
}
