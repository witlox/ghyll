package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/runner"
)

// Step bindings for the Tier 2 (ADR-016) @deferred scenarios in
// attestation.feature that are now wireable end-to-end:
//
//   - "Three-role chain path encoding" (line 224)
//   - "init arrow path encoding" (line 232)
//   - "Missing required field is detected" (line 167)
//
// These exercise EncodeAttestationPath and AttestationVerifier
// directly. The fuller modal-flow scenarios (verdict capture,
// escalation, multi-operator handoff) require modalDriver +
// session fixtures and are wired in a later wave.
//
// State lives on ScenarioState (T2*) so each scenario starts
// clean.

func registerTier2ModalSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// ---- Three-role chain path encoding -------------------------

	ctx.Step(`^an arrow with role-pair containing the adversary segment \(e\.g\., analyst→adversary→architect\)$`, func() error {
		state.T2Rec = runner.AttestationRecord{
			ID:             "att-three-role",
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A-three-role",
			ClauseID:       "C-three-role",
			OpID:           "alice@example.com",
			AttestedByRole: "operator",
			SourceRole:     "analyst",
			AdversaryRole:  "adversary",
			TargetRole:     "architect",
			Verdict:        runner.AttestationPass,
			Timestamp:      1716100000_000000000,
			GridVersion:    7,
			PassID:         "P-three-role",
			Context:        "ctxA",
			Stratum:        "L1",
			Unit:           runner.VerdictUnitConfirm,
			HintJSON:       "{}",
		}
		return nil
	})

	ctx.Step(`^an attestation record is written$`, func() error {
		path, truncated, err := runner.EncodeAttestationPath(state.T2Rec)
		state.T2Path = path
		state.T2Truncated = truncated
		state.T2EncodeErr = err
		return nil
	})

	ctx.Step(`^the path component for the role-pair is "([^"]+)" \(double-underscore separator\)$`, func(want string) error {
		if state.T2EncodeErr != nil {
			return fmt.Errorf("encode errored: %w", state.T2EncodeErr)
		}
		if !strings.Contains(state.T2Path, want) {
			return fmt.Errorf("path %q does not contain role-pair %q", state.T2Path, want)
		}
		return nil
	})

	ctx.Step(`^NOT "analyst-adversary-architect" or "analyst→adversary→architect"$`, func() error {
		if strings.Contains(state.T2Path, "analyst-adversary-architect") {
			return fmt.Errorf("path %q contains single-dash form", state.T2Path)
		}
		if strings.Contains(state.T2Path, "analyst→adversary→architect") {
			return fmt.Errorf("path %q contains Unicode-arrow form", state.T2Path)
		}
		return nil
	})

	ctx.Step(`^the path is filesystem-portable \(no Unicode glyphs, no path separators, ≤ 255 bytes per component\)$`, func() error {
		if state.T2EncodeErr != nil {
			return state.T2EncodeErr
		}
		for _, seg := range strings.Split(state.T2Path, string(filepath.Separator)) {
			if len(seg) > 255 {
				return fmt.Errorf("segment %q exceeds 255 bytes", seg)
			}
			// No path separators inside a segment.
			if strings.ContainsAny(seg, "/\\") {
				return fmt.Errorf("segment %q contains a separator", seg)
			}
			// No non-ASCII characters (Unicode glyphs).
			for i := 0; i < len(seg); i++ {
				if seg[i] >= 0x80 {
					return fmt.Errorf("segment %q contains non-ASCII byte 0x%02x", seg, seg[i])
				}
			}
		}
		return nil
	})

	// ---- init arrow path encoding -------------------------------

	ctx.Step(`^an attestation record for the init arrow$`, func() error {
		state.T2Rec = runner.AttestationRecord{
			ID:             "att-init",
			Kind:           runner.AttestationKindDepthType,
			ArrowID:        "A-init",
			ClauseID:       "C-init",
			OpID:           "alice@example.com",
			AttestedByRole: "init",
			SourceRole:     "init",
			TargetRole:     "analyst",
			Verdict:        runner.AttestationPass,
			Timestamp:      1716100000_000000000,
			GridVersion:    1,
			PassID:         "P-init",
			Unit:           runner.VerdictUnitConfirm,
			HintJSON:       "{}",
		}
		return nil
	})

	ctx.Step(`^the path is constructed$`, func() error {
		path, truncated, err := runner.EncodeAttestationPath(state.T2Rec)
		state.T2Path = path
		state.T2Truncated = truncated
		state.T2EncodeErr = err
		return err
	})

	ctx.Step(`^the role-pair component is "init__analyst"$`, func() error {
		if !strings.Contains(state.T2Path, "init__analyst") {
			return fmt.Errorf("path %q lacks role-pair init__analyst", state.T2Path)
		}
		return nil
	})

	ctx.Step(`^the context and stratum components are empty / "_" \(init is project-scoped, not context-scoped — per components/init\.md sub-phase A\)$`, func() error {
		segs := strings.Split(state.T2Path, string(filepath.Separator))
		// Expected segments: [v1 _ stratum-_ init__analyst P-init.jsonl]
		if len(segs) < 5 {
			return fmt.Errorf("path %q has too few segments", state.T2Path)
		}
		if segs[1] != "_" {
			return fmt.Errorf("context segment = %q; want \"_\"", segs[1])
		}
		if segs[2] != "stratum-_" {
			return fmt.Errorf("stratum segment = %q; want \"stratum-_\"", segs[2])
		}
		return nil
	})

	ctx.Step(`^the path is consistently chosen \(not sometimes "v<N>/_/_/init__analyst/\.\.\." and sometimes "v<N>/init__analyst/\.\.\."\)$`, func() error {
		// Run the encode again and verify byte-for-byte stability.
		path, _, err := runner.EncodeAttestationPath(state.T2Rec)
		if err != nil {
			return err
		}
		if path != state.T2Path {
			return fmt.Errorf("path not stable across encodes: %q vs %q", path, state.T2Path)
		}
		return nil
	})

	// ---- Missing required field is detected ----------------------

	ctx.Step(`^an attestation record with unit "([^"]+)" but no "([^"]+)" array$`, func(unit, field string) error {
		state.T2VerifyDir = state.T2TempDir(unit + "-" + field)
		state.T2VerifyPath = filepath.Join(state.T2VerifyDir, "att.jsonl")
		// Hand-craft a JSONL line whose unit declares record-
		// locations-inspected but whose unit_payload_json carries
		// an empty inspected array.
		// _ = field   // referenced in the step description; actual omission tested via payload below.
		line := `{"attestation_id":"att-missing-field","kind":"depth-type","arrow_id":"A","clause_id":"C","op_id":"o","attested_by_role":"operator","verdict":"fail","timestamp":1716100000000000000,"grid_version":1,"unit":"` + unit + `","unit_payload_json":"{\"inspected\":[]}"}` + "\n"
		if err := os.WriteFile(state.T2VerifyPath, []byte(line), 0o644); err != nil {
			return err
		}
		_ = field
		return nil
	})

	ctx.Step(`^the verifier reads it$`, func() error {
		v := &runner.AttestationVerifier{}
		res, err := v.VerifyFile(state.T2VerifyPath)
		state.T2VerifyResult = res
		state.T2VerifyErr = err
		return err
	})

	ctx.Step(`^the record is flagged as malformed$`, func() error {
		if state.T2VerifyResult.Failed != 1 {
			return fmt.Errorf("VerifyResult.Failed = %d; want 1 (the malformed line)",
				state.T2VerifyResult.Failed)
		}
		return nil
	})

	ctx.Step(`^the operator session that produced it is alerted$`, func() error {
		// The verifier surfaces an Issue per malformed line; the
		// CLI prints them. "Alerted" here means: the issues list
		// names the specific field that was missing.
		hadFieldMention := false
		for _, iss := range state.T2VerifyResult.Issues {
			if strings.Contains(iss.Reason, "ValidateUnitPayload") ||
				strings.Contains(iss.Reason, "inspected") ||
				strings.Contains(iss.Reason, "missing") {
				hadFieldMention = true
			}
		}
		if !hadFieldMention {
			return fmt.Errorf("no issue surfaces the missing field; issues: %v", state.T2VerifyResult.Issues)
		}
		return nil
	})

}
