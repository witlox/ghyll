// `/drain-amendments` — operator-triggered amendment commit
// (diamond v4 / Gap 2 closure).
//
// Per the v2 implementation contract (specs/v4/diamond-load-bearing-
// revised-v2.md §"Gap 2 wiring"), the integrator-pass-close path
// enqueues amendments onto AmendmentQueue but does NOT drain them
// automatically. The operator must declare an op-id and explicitly
// type `/drain-amendments` so the verdict is attributable and
// audited.
//
// Drains the queue head-to-tail under the committer's mutex; each
// commit emits OpEventAmendmentDrained on the bus.

package main

import (
	gocontext "context"
	"fmt"
	"strings"

	"github.com/witlox/ghyll/runner"
)

// handleDrainAmendmentsCommand is the slash-command entry point for
// `/drain-amendments`. Refuses without an op-id (per gates.md §3.7
// the amendment commit's audit row carries the operator identity).
//
// Each pending amendment is committed FIFO. NewLanguageBindings and
// NewArrows are sourced from the amendment's persisted overlay
// (the v1 wire passes empty maps; the analyst-rooted overlay path
// is a follow-up — the substrate accepts both shapes today).
//
// Output line per commit:
//
//	✓ amendment AM-x: arrows-added=N passes-aborted=M v=A→B
//
// Final line: summary count + any errors.
func (s *Session) handleDrainAmendmentsCommand(arg string) SlashCommandResult {
	_ = arg
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /drain-amendments unavailable",
		}
	}
	if s.opID == "" {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /drain-amendments refuses: no op-id set; use /op-id <identity> first",
		}
	}
	committer := s.engine.Committer()
	if committer == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /drain-amendments: committer unavailable (engine partially initialized)",
		}
	}
	queue := s.engine.Amendments()
	if queue == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /drain-amendments: amendment queue unavailable",
		}
	}
	pending := queue.Pending()
	if len(pending) == 0 {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "ℹ no pending amendments; queue is empty",
		}
	}

	// Live event surface: subscribe pre-commit so the operator sees
	// OpEventAmendment* events inline. Mirrors /run-arrow's shape.
	bus := s.engine.Bus()
	var captured []runner.OperatorEvent
	var unsubscribe func()
	if bus != nil {
		unsubscribe = bus.Subscribe(func(e runner.OperatorEvent) {
			switch e.Kind {
			case runner.OpEventAmendmentDrained,
				runner.OpEventAmendmentEnqueueRefused,
				runner.OpEventAmendmentEnqueued:
				captured = append(captured, e)
			}
		})
		defer unsubscribe()
	}

	var out strings.Builder
	ctx := s.SessionContext()
	if ctx == nil {
		ctx = gocontext.Background()
	}
	committed := 0
	for _, am := range pending {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(&out, "✗ /drain-amendments aborted: %v\n", err)
			break
		}
		req := runner.CommitRequest{
			Amendment: am,
			// V1 wire: NewArrows + NewLanguageBindings are the
			// analyst's response. The session-loop path passes empty
			// shapes (the integrator's enqueue carries the rationale
			// in description / contexts; the analyst's overlay path
			// is a separate component). The committer still aborts
			// affected in-flight passes and marks the queue entry as
			// drained, which is the operator-visible contract.
		}
		res, err := committer.Commit(ctx, req)
		if err != nil {
			fmt.Fprintf(&out, "✗ amendment %s: %v\n", sanitizeOneLine(am.ID), err)
			continue
		}
		committed++
		if res != nil {
			fmt.Fprintf(&out, "✓ amendment %s: arrows-added=%d passes-aborted=%d v=%d→%d\n",
				sanitizeOneLine(am.ID),
				len(res.AppendedArrows),
				len(res.AbortedPasses),
				res.GridVersionBefore, res.GridVersionAfter)
		}
	}
	if unsubscribe != nil {
		unsubscribe()
	}
	for _, e := range captured {
		switch e.Kind {
		case runner.OpEventAmendmentDrained:
			fmt.Fprintf(&out, "  · amendment-drained %s %s\n",
				sanitizeOneLine(e.ArrowID), sanitizeOneLine(e.Detail))
		case runner.OpEventAmendmentEnqueueRefused:
			fmt.Fprintf(&out, "  ⚠ amendment-enqueue-refused %s %s\n",
				sanitizeOneLine(e.ArrowID), sanitizeOneLine(e.Detail))
		case runner.OpEventAmendmentEnqueued:
			fmt.Fprintf(&out, "  · amendment-enqueued %s %s\n",
				sanitizeOneLine(e.ArrowID), sanitizeOneLine(e.Detail))
		}
	}
	fmt.Fprintf(&out, "drain complete: %d/%d committed", committed, len(pending))
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(out.String(), "\n"),
	}
}
