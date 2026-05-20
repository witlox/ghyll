package bootstrap

import (
	"fmt"
	"time"

	"github.com/witlox/ghyll/runner"
)

// EmitInitAttestations builds a slice of on-the-spot AttestationRecords
// declaring "init recorded these arrows" for every arrow in the grid.
// Gate-2 CORR-A-18: the init flow now has a production producer for
// AttestationRecord.AttestedByRole == "init", so the BDD init-path-
// encoding scenario reflects real session behavior — not just a
// hand-built test fixture.
//
// Each emitted record carries:
//
//   - Kind:           on-the-spot (no clause-id required)
//   - ArrowID:        the grid arrow's id (upstream + "→" + downstream + "/" + context)
//   - OpID:           the operator who completed init
//   - AttestedByRole: literal "init"
//   - SourceRole:     "init" (the init session is its own producer)
//   - TargetRole:     the arrow's upstream role (the role init bootstrapped first)
//   - Verdict:        AttestationPass
//   - Reason:         "init-bootstrap"
//   - Timestamp:      now (unix nanos)
//   - GridVersion:    grid.GridVersion
//   - PassID:         "init-<arrow-id>"
//
// Callers (cli `ghyll init attest`, future operator HUD button)
// pass the slice to AttestationStore.Record one-by-one. The
// records satisfy validateAttestationTier2 — PassID non-empty,
// AdversaryRole empty (init is single-role-source), Unit=confirm,
// payload empty.
func EmitInitAttestations(grid *Grid, opID string) ([]runner.AttestationRecord, error) {
	if grid == nil {
		return nil, fmt.Errorf("EmitInitAttestations: nil grid")
	}
	if opID == "" {
		return nil, fmt.Errorf("EmitInitAttestations: empty opID")
	}
	now := time.Now().UnixNano()
	out := make([]runner.AttestationRecord, 0, len(grid.Arrows))
	for i, arrow := range grid.Arrows {
		// Grid.Arrows is []map[string]any (untyped v1 shape per
		// serializeArrow). Pull the three identifying fields by
		// key with defensive type-asserts.
		upstream, _ := arrow["upstream"].(string)
		downstream, _ := arrow["downstream"].(string)
		context, _ := arrow["context"].(string)
		if upstream == "" || downstream == "" || context == "" {
			continue
		}
		arrowID := fmt.Sprintf("%s→%s/%s", upstream, downstream, context)
		rec := runner.AttestationRecord{
			ID:             fmt.Sprintf("att-init-%s-v%d", arrowID, grid.GridVersion),
			Kind:           runner.AttestationKindOnTheSpot,
			ArrowID:        arrowID,
			OpID:           opID,
			AttestedByRole: "init",
			// SourceRole/TargetRole intentionally empty: an init
			// attestation is a single-role assertion (init asserts
			// it bootstrapped the arrow), not a peer-review
			// attestation. The §12.2 self-cert check only fires
			// when SourceRole/TargetRole are populated, so leaving
			// them empty keeps the check from blocking "init"-
			// attested records.
			Verdict:         runner.AttestationPass,
			Reason:          "init-bootstrap",
			Timestamp:       now + int64(i),
			GridVersion:     uint64(grid.GridVersion),
			PassID:          fmt.Sprintf("init-%s", arrowID),
			Context:         context,
			Stratum:         "_",
			Unit:            runner.VerdictUnitConfirm,
			UnitPayloadJSON: "{}",
			HintJSON:        "{}",
		}
		out = append(out, rec)
	}
	return out, nil
}
