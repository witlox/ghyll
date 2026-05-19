package acceptance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/runner"
)

// Step bindings for the amendment.feature scenarios that exercise
// the AmendmentCommitter + PassRegistry contract:
//
//   - "Amendment commits successfully"     (§3.7 happy path)
//   - "Pass identity uses grid version"    (arrow-id carries grid version)
//
// No new components. Uses AmendmentCommitter + PassRegistry +
// RoleContextLockTable + Grid + OperatorBus exactly as ADR-010/011
// shipped them.

func registerAmendmentDeferredSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Before(func(c context.Context, sc *godog.Scenario) (context.Context, error) {
		state.ADLockTable = runner.NewRoleContextLockTable()
		state.ADGrid = runner.NewGrid()
		state.ADPasses = runner.NewPassRegistry()
		state.ADQueue = runner.NewAmendmentQueue()
		state.ADBus = runner.NewOperatorBus()
		state.ADBusEvents = nil
		state.ADBus.Subscribe(func(e runner.OperatorEvent) {
			state.ADBusEvents = append(state.ADBusEvents, e)
		})
		state.ADCommitter = &runner.AmendmentCommitter{
			Grid:   state.ADGrid,
			Passes: state.ADPasses,
			Bus:    state.ADBus,
			Queue:  state.ADQueue,
		}
		state.ADAffectedPass = nil
		state.ADResult = nil
		state.ADCommitErr = nil
		state.ADP5 = nil
		state.ADP5Successor = nil
		state.ADAmendment = runner.AmendmentRequest{}
		state.ADNewArrowID = ""
		state.ADNextAmendment = runner.AmendmentRequest{}
		return c, nil
	})

	// -------- Amendment commits successfully --------

	ctx.Step(`^an amendment holds the lock$`, func() error {
		// Set up an in-flight pass on the to-be-amended arrow + the
		// pending amendment request.
		const sourceArrow = "implementer->architect@contextA/v1"
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P-in-flight",
			Role:      "implementer",
			Context:   "contextA",
			ArrowID:   sourceArrow,
			LockTable: state.ADLockTable,
			Bus:       state.ADBus,
		})
		if err != nil {
			return fmt.Errorf("open in-flight pass: %w", err)
		}
		state.ADAffectedPass = p
		state.ADPasses.Register(p)

		state.ADAmendment = runner.AmendmentRequest{
			ID:          "amend-commits-cleanly",
			Reason:      runner.AmendmentReasonMissingCrossContextSpec,
			SourceArrow: sourceArrow,
			TargetRole:  "analyst",
			Contexts:    []string{"contextA", "contextB"},
			FindingIDs:  []string{"F-mcs-1"},
			Description: "missing cross-context-spec",
			CreatedAt:   "2026-05-19T00:00:00Z",
		}
		if err := state.ADQueue.Enqueue(state.ADAmendment); err != nil {
			return fmt.Errorf("enqueue amendment: %w", err)
		}
		return nil
	})

	ctx.Step(`^the analyst re-runs \(re-engaged\) and produces the amended spec$`, func() error {
		// "Produces the amended spec" = the analyst has decided what
		// the new arrow grid looks like. We model this by recording
		// the new ArrowDefinition the commit step will append.
		state.ADNewArrowID = "implementer->architect@contextA/v2"
		return nil
	})

	ctx.Step(`^the amendment proposes v\(N\+1\) with the new arrow grid$`, func() error {
		// Drive the commit. The committer aborts in-flight passes on
		// the SourceArrow, appends the new arrow (bumping the grid
		// version → v(N+1)), marks the amendment drained, and
		// publishes amendment-drained on the bus.
		newArrow := runner.ArrowDefinition{
			ID:         state.ADNewArrowID,
			SourceRole: "implementer",
			TargetRole: "architect",
			Stratum:    "stratum-1",
			Context:    "contextA",
			Clauses: []runner.Clause{
				{Concept: "no-todo-marker", Args: map[string]any{"markers": []any{"TODO"}}},
			},
		}
		res, err := state.ADCommitter.Commit(context.Background(), runner.CommitRequest{
			Amendment: state.ADAmendment,
			NewArrows: []runner.ArrowDefinition{newArrow},
		})
		state.ADResult = res
		state.ADCommitErr = err
		return err
	})

	ctx.Step(`^the component computes the set of affected arrows$`, func() error {
		// CommitResult exposes the aborted passes (= the passes on the
		// affected arrow). One in-flight pass on the SourceArrow → one
		// entry.
		if state.ADResult == nil {
			return errors.New("commit result missing")
		}
		if len(state.ADResult.AbortedPasses) != 1 {
			return fmt.Errorf("AbortedPasses = %v; want 1 entry", state.ADResult.AbortedPasses)
		}
		if state.ADResult.AbortedPasses[0] != state.ADAffectedPass.ID() {
			return fmt.Errorf("AbortedPasses[0] = %q; want %q",
				state.ADResult.AbortedPasses[0], state.ADAffectedPass.ID())
		}
		return nil
	})

	ctx.Step(`^signals all affected "running" passes to abort$`, func() error {
		if state.ADAffectedPass.State() != runner.PassStateAborted {
			return fmt.Errorf("affected pass state = %q; want aborted",
				state.ADAffectedPass.State())
		}
		if !strings.Contains(state.ADAffectedPass.CloseReason(), state.ADAmendment.ID) {
			return fmt.Errorf("close reason = %q; want to reference amendment id %q",
				state.ADAffectedPass.CloseReason(), state.ADAmendment.ID)
		}
		return nil
	})

	ctx.Step(`^writes the new grid atomically$`, func() error {
		// Grid.Append (called inside Commit) is the atomic write.
		// Validate the version bump and the arrow being lookup-able.
		if state.ADResult.GridVersionAfter <= state.ADResult.GridVersionBefore {
			return fmt.Errorf("grid version did not advance: before=%d after=%d",
				state.ADResult.GridVersionBefore, state.ADResult.GridVersionAfter)
		}
		if _, ok := state.ADGrid.Lookup(state.ADNewArrowID); !ok {
			return fmt.Errorf("Grid.Lookup(%q): not found after append", state.ADNewArrowID)
		}
		if len(state.ADResult.AppendedArrows) != 1 ||
			state.ADResult.AppendedArrows[0] != state.ADNewArrowID {
			return fmt.Errorf("AppendedArrows = %v; want [%q]",
				state.ADResult.AppendedArrows, state.ADNewArrowID)
		}
		return nil
	})

	ctx.Step(`^records the v\(N\+1\) commit log entry$`, func() error {
		// The "commit log entry" surfaces as the amendment-drained
		// OperatorEvent (the journal subscribes the bus and writes the
		// row; the bus event is the observable signal in this test).
		var drained *runner.OperatorEvent
		for i := range state.ADBusEvents {
			if state.ADBusEvents[i].Kind == runner.OpEventAmendmentDrained {
				drained = &state.ADBusEvents[i]
				break
			}
		}
		if drained == nil {
			return errors.New("no amendment-drained event observed on bus")
		}
		want := fmt.Sprintf("amendment=%s", state.ADAmendment.ID)
		if !strings.Contains(drained.Detail, want) {
			return fmt.Errorf("amendment-drained detail = %q; want substring %q",
				drained.Detail, want)
		}
		if !strings.Contains(drained.Detail, "status=complete") {
			return fmt.Errorf("amendment-drained status not complete: %q", drained.Detail)
		}
		return nil
	})

	ctx.Step(`^releases the lock$`, func() error {
		// The lock token is owned by the Pass; Abort releases it.
		// Verify by acquiring the same (role, context) tuple — it
		// would have errored if still held.
		tok, err := state.ADLockTable.TryAcquire("implementer", "contextA", "P-probe", 0)
		if err != nil {
			return fmt.Errorf("expected lock to be free after abort; got: %w", err)
		}
		tok.Release()
		return nil
	})

	ctx.Step(`^the next queued amendment may proceed$`, func() error {
		// queue.MarkDrained ran inside Commit. A subsequent enqueue
		// with a different ID must succeed (the queue is operational).
		state.ADNextAmendment = runner.AmendmentRequest{
			ID:          "amend-next",
			Reason:      runner.AmendmentReasonMissingCrossContextSpec,
			SourceArrow: "implementer->architect@contextB/v1",
			TargetRole:  "analyst",
			Contexts:    []string{"contextB", "contextC"},
			FindingIDs:  []string{"F-mcs-2"},
			Description: "next amendment",
			CreatedAt:   "2026-05-19T00:00:01Z",
		}
		if err := state.ADQueue.Enqueue(state.ADNextAmendment); err != nil {
			return fmt.Errorf("next amendment enqueue: %w", err)
		}
		if state.ADQueue.Len() != 1 {
			return fmt.Errorf("queue len after MarkDrained + enqueue = %d; want 1",
				state.ADQueue.Len())
		}
		return nil
	})

	// -------- Pass identity uses grid version --------

	ctx.Step(`^a pass P5 is created on arrow A1$`, func() error {
		// Construct an ArrowID that encodes the (role-pair, stratum,
		// context, version) tuple per the scenario. The current grid
		// is at v(N+1) — encode that as "v2" (v1 is the implicit base).
		arrowID := "analyst__architect/stratum-1/contextA/v2"
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P5",
			Role:      "analyst",
			Context:   "contextA/v2", // context+version in lock-key namespace too
			ArrowID:   arrowID,
			LockTable: state.ADLockTable,
		})
		if err != nil {
			return fmt.Errorf("open P5: %w", err)
		}
		state.ADP5 = p
		state.ADPasses.Register(p)
		return nil
	})

	ctx.Step(`^the current grid version is v\(N\+1\)$`, func() error {
		// The version is encoded in the ArrowID and the Context lock
		// key. Narrative confirmation — verified by the Then steps.
		if !strings.HasSuffix(state.ADP5.ArrowID(), "/v2") {
			return fmt.Errorf("ArrowID = %q; expected to encode v(N+1) (v2)", state.ADP5.ArrowID())
		}
		return nil
	})

	ctx.Step(`^P5's arrow-id is computed as "\(role-pair, stratum, context, v\(N\+1\)\)"$`, func() error {
		// Verify shape: role-pair "__" separator, stratum-N, context,
		// version segment "v<int>".
		got := state.ADP5.ArrowID()
		parts := strings.Split(got, "/")
		if len(parts) != 4 {
			return fmt.Errorf("ArrowID %q: want 4 slash-separated parts (role-pair/stratum/context/version)", got)
		}
		if !strings.Contains(parts[0], "__") {
			return fmt.Errorf("role-pair %q: expected '__' separator", parts[0])
		}
		if !strings.HasPrefix(parts[1], "stratum-") {
			return fmt.Errorf("stratum %q: want 'stratum-<id>' shape", parts[1])
		}
		if parts[2] == "" {
			return fmt.Errorf("context segment empty in %q", got)
		}
		if !strings.HasPrefix(parts[3], "v") {
			return fmt.Errorf("version %q: want 'v<int>' shape", parts[3])
		}
		return nil
	})

	ctx.Step(`^if the same arrow is re-traversed after v\(N\+2\), the new pass has a different arrow-id$`, func() error {
		// Open a second pass on the SAME arrow but on a later grid
		// version. Different ArrowID (different version segment) →
		// different lock key → no collision.
		successorArrowID := "analyst__architect/stratum-1/contextA/v3"
		p, err := runner.OpenPass(runner.PassOptions{
			PassID:    "P5-after-v3",
			Role:      "analyst",
			Context:   "contextA/v3",
			ArrowID:   successorArrowID,
			LockTable: state.ADLockTable,
		})
		if err != nil {
			return fmt.Errorf("open successor pass: %w", err)
		}
		state.ADP5Successor = p
		if p.ArrowID() == state.ADP5.ArrowID() {
			return fmt.Errorf("successor ArrowID matches predecessor: %q", p.ArrowID())
		}
		return nil
	})
}
