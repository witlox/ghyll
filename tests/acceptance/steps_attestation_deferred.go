package acceptance

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/runner"
)

// Step bindings for attestation.feature "Verifier reads attestation
// log". Drives AttestationStore + JSONL Writer + AttestationVerifier
// end-to-end: write three records on the same (pass-id, clause-id)
// across rounds, then verify them via the existing verifier and
// reconstruct the operator decision chain by reading the JSONL.

func registerAttestationDeferredSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "att-verifier-")
		if err != nil {
			return c, err
		}
		state.AVJSONLPath = filepath.Join(dir, ".ghyll", "attestations.jsonl")
		state.AVStore = runner.NewAttestationStore()
		w, err := runner.NewAttestationJSONLWriter(state.AVJSONLPath)
		if err != nil {
			return c, err
		}
		state.AVWriter = w
		state.AVStore.Observe(w.Observer())
		state.AVPassID = ""
		state.AVClauseID = ""
		state.AVResult = runner.VerifyResult{}
		state.AVMatching = nil
		state.AVVerifyResErr = nil
		return c, nil
	})

	// -------- Verifier reads attestation log --------

	ctx.Step(`^a pass-id and a clause-id$`, func() error {
		state.AVPassID = "P-verify"
		state.AVClauseID = "C-verify"

		// Write three records for the same arrow + clause across
		// rounds. Verdicts traverse insufficient-basis → insufficient-
		// basis → pass — the canonical operator decision chain.
		base := runner.AttestationRecord{
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A-verify",
			ClauseID:       state.AVClauseID,
			OpID:           "alice@example.com",
			AttestedByRole: "operator",
			SourceRole:     "analyst",
			TargetRole:     "architect",
			GridVersion:    1,
		}
		rounds := []struct {
			id      string
			ts      int64
			verdict runner.AttestationVerdict
			reason  string
		}{
			{"att-1", 1716100000_000000000, runner.AttestationInsufficientBasis, "round-1 residue"},
			{"att-2", 1716100100_000000000, runner.AttestationInsufficientBasis, "round-2 deeper hint requested"},
			{"att-3", 1716100200_000000000, runner.AttestationPass, "round-3 confirmed"},
		}
		for _, r := range rounds {
			rec := base
			rec.ID = r.id
			rec.Timestamp = r.ts
			rec.Verdict = r.verdict
			rec.Reason = r.reason
			if err := state.AVStore.Record(rec); err != nil {
				return fmt.Errorf("record %s: %w", r.id, err)
			}
		}
		return nil
	})

	ctx.Step(`^the verifier component reads the attestation file$`, func() error {
		if err := state.AVWriter.Close(); err != nil {
			return fmt.Errorf("close writer: %w", err)
		}
		v := &runner.AttestationVerifier{}
		res, err := v.VerifyFile(state.AVJSONLPath)
		state.AVResult = res
		state.AVVerifyResErr = err
		if err != nil {
			return err
		}
		// Also read the file directly and gather records matching
		// (pass-id implicit in the test setup, clause-id explicit) so
		// the next steps can assert ordering / completeness.
		f, err := os.Open(state.AVJSONLPath)
		if err != nil {
			return fmt.Errorf("open jsonl: %w", err)
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec struct {
				ID             string `json:"attestation_id"`
				Kind           string `json:"kind"`
				ArrowID        string `json:"arrow_id"`
				ClauseID       string `json:"clause_id"`
				OpID           string `json:"op_id"`
				AttestedByRole string `json:"attested_by_role"`
				Verdict        string `json:"verdict"`
				Reason         string `json:"reason"`
				Timestamp      int64  `json:"timestamp"`
				GridVersion    uint64 `json:"grid_version"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				return fmt.Errorf("unmarshal line: %w", err)
			}
			if rec.ClauseID != state.AVClauseID {
				continue
			}
			state.AVMatching = append(state.AVMatching, runner.AttestationRecord{
				ID:             rec.ID,
				Kind:           runner.AttestationKind(rec.Kind),
				ArrowID:        rec.ArrowID,
				ClauseID:       rec.ClauseID,
				OpID:           rec.OpID,
				AttestedByRole: rec.AttestedByRole,
				Verdict:        runner.AttestationVerdict(rec.Verdict),
				Reason:         rec.Reason,
				Timestamp:      rec.Timestamp,
				GridVersion:    rec.GridVersion,
			})
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		return nil
	})

	ctx.Step(`^it finds all records for that clause in chronological order$`, func() error {
		if len(state.AVMatching) != 3 {
			return fmt.Errorf("matching records for %q = %d; want 3",
				state.AVClauseID, len(state.AVMatching))
		}
		// Chronological-order claim: timestamps strictly increasing.
		if !sort.SliceIsSorted(state.AVMatching, func(i, j int) bool {
			return state.AVMatching[i].Timestamp < state.AVMatching[j].Timestamp
		}) {
			return errors.New("records not in chronological order")
		}
		return nil
	})

	ctx.Step(`^can reconstruct the operator's decision chain$`, func() error {
		// The operator decision chain is the verdict sequence per round.
		// For the seeded data: insufficient-basis → insufficient-basis →
		// pass. Reading the matching records in chronological order
		// yields exactly that chain.
		want := []runner.AttestationVerdict{
			runner.AttestationInsufficientBasis,
			runner.AttestationInsufficientBasis,
			runner.AttestationPass,
		}
		if len(state.AVMatching) != len(want) {
			return fmt.Errorf("decision chain length = %d; want %d",
				len(state.AVMatching), len(want))
		}
		for i, v := range want {
			if state.AVMatching[i].Verdict != v {
				return fmt.Errorf("decision[%d] = %q; want %q",
					i, state.AVMatching[i].Verdict, v)
			}
		}
		return nil
	})

	ctx.Step(`^can verify that the required fields per unit are present$`, func() error {
		// The verifier validated every record (depth-type kind requires
		// clause_id, valid verdict, non-empty attestation_id/arrow_id/
		// op_id/attested_by_role, timestamp > 0, §12.2 self-cert
		// check). VerifyResult.Failed=0 means all three lines passed
		// the schema invariants.
		if state.AVVerifyResErr != nil {
			return fmt.Errorf("verify errored: %w", state.AVVerifyResErr)
		}
		if state.AVResult.Failed != 0 {
			return fmt.Errorf("VerifyResult.Failed = %d; want 0. issues: %v",
				state.AVResult.Failed, state.AVResult.Issues)
		}
		if state.AVResult.OK != 3 {
			return fmt.Errorf("VerifyResult.OK = %d; want 3", state.AVResult.OK)
		}
		return nil
	})
}
