package main

import (
	gocontext "context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/witlox/ghyll/runner"
)

// run-arrow / list-arrows wire the dispatcher / engineRuntime.RunArrow
// path into the operator's REPL surface (integrator finding C-3).
// Before this, RunArrow was defined on engineRuntime and exercised
// only by tier0 tests — the model could chat, but no operator-facing
// path actually executed arrows.
//
// Slash commands shipped:
//
//   /list-arrows
//     Renders the grid snapshot (sorted arrow ids + source→target,
//     stratum, context). When the grid is empty, surfaces the
//     "no grid; run `ghyll init` first" hint (C-3 final-step).
//
//   /run-arrow <arrow-id> [--context <ctx>]
//     Validates arrow-id, looks the arrow up on the live grid,
//     resolves a depth tier via runner.RouteArrow over the arrow's
//     clauses (gates.md §8 max-over-clauses), and dispatches via
//     engineRuntime.RunArrow. Pass open/close events from the
//     OperatorBus are streamed inline; the final ArrowStatus
//     surfaces once Dispatch returns. Any
//     OpEventInsufficientBasisRoundsExceeded that fires during the
//     dispatch (e.g., when the IBTracker's rolling counter crosses
//     the configured ceiling) is also surfaced inline.
//
// The handler returns control to the chat loop after Dispatch
// returns (synchronous from the dispatcher's perspective).
// Operator modal escalation (IB rounds, attestation prompts) flows
// through the existing modalDriver subscription that
// session.initEngine wired against the same bus, so blocking
// modal reads are NOT serialized through this handler.

// runArrowOptions captures the parsed `/run-arrow` args.
type runArrowOptions struct {
	ArrowID string
	Context string // optional override
}

// parseRunArrowArgs parses the arg-suffix of `/run-arrow`. Returns
// usage error on empty / malformed input.
//
// Wire form:
//
//	<arrow-id> [--context <ctx>]
//
// Whitespace around tokens is trimmed; the arrow-id is the first
// non-flag positional. Unknown flags are rejected.
func parseRunArrowArgs(arg string) (runArrowOptions, error) {
	fields := strings.Fields(arg)
	if len(fields) == 0 {
		return runArrowOptions{}, errors.New("arrow-id required")
	}
	var opts runArrowOptions
	i := 0
	// First positional must be the arrow-id (not a flag).
	if strings.HasPrefix(fields[0], "--") {
		return runArrowOptions{}, errors.New("arrow-id required (first positional must not start with --)")
	}
	opts.ArrowID = fields[0]
	if strings.TrimSpace(opts.ArrowID) == "" {
		return runArrowOptions{}, errors.New("arrow-id must not be empty")
	}
	i++
	for i < len(fields) {
		switch fields[i] {
		case "--context":
			if i+1 >= len(fields) {
				return runArrowOptions{}, errors.New("--context requires a value")
			}
			opts.Context = strings.TrimSpace(fields[i+1])
			if opts.Context == "" {
				return runArrowOptions{}, errors.New("--context value must not be empty")
			}
			i += 2
		default:
			return runArrowOptions{}, fmt.Errorf("unknown flag %q", fields[i])
		}
	}
	return opts, nil
}

// handleRunArrowCommand is the slash-command entry point for
// `/run-arrow`. It owns:
//   - input parsing,
//   - grid lookup,
//   - depth-tier resolution,
//   - bus subscription for inline event surfacing,
//   - RunArrow dispatch,
//   - result + status rendering.
//
// Returns a SlashCommandResult whose Output is the
// pretty-printed event trace + final status line.
func (s *Session) handleRunArrowCommand(arg string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /run-arrow unavailable",
		}
	}
	opts, err := parseRunArrowArgs(arg)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("usage: /run-arrow <arrow-id> [--context <ctx>]  (%v)", err),
		}
	}
	def, ok := s.engine.Grid().Lookup(opts.ArrowID)
	if !ok {
		// Soft hint when the grid is empty entirely (gate-and-arrow
		// runtime dormant for this project).
		if len(s.engine.Grid().Arrows()) == 0 {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "✗ no grid; run `ghyll init` first",
			}
		}
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ arrow %q not in grid (try /list-arrows)", opts.ArrowID),
		}
	}

	// Resolve depth tier via the routing layer. RouteArrow returns
	// the max MinDepthTier across clauses; passes ActualTier ≥
	// MinTier are valid per the dispatcher contract. We pin at
	// MinTier (the spec floor) rather than DepthRankRealistic so
	// the runner doesn't over-route a depth-robust arrow.
	tier := runner.DepthRankShallow
	if req, rerr := runner.RouteArrow(def.Clauses); rerr == nil && req.Routed {
		if req.MinTier > runner.DepthRankNone {
			tier = req.MinTier
		}
	}

	// Resolve context: --context wins, else the arrow's own
	// declared Context, else the dispatcher fails. Both have been
	// trimmed; def.Context is guaranteed non-empty by Validate.
	ctxName := opts.Context
	if ctxName == "" {
		ctxName = def.Context
	}

	// Resolve role: source-role of the arrow. Per ADR-009 / gates.md
	// §12, the pass that traverses arrow A is owned by the
	// SourceRole. The operator may /run-arrow from any role context;
	// the dispatch identifies the source role as the canonical
	// owner of the locks + the OperatorEvent.Role field.
	role := def.SourceRole

	// Live event surface: subscribe pre-dispatch so we capture every
	// pass-opened / pass-closed / IB-rounds-exceeded fired during
	// the synchronous dispatch. Subscriber is removed on return so
	// nothing leaks past the slash-command lifetime.
	//
	// H-C post-prod-readiness adversarial: nil-guard `bus` even
	// though `s.engine != nil` is already checked above. The
	// engineRuntime.Bus() contract returns nil only on a nil
	// receiver today, but a future refactor that splits engine
	// open from bus wiring would silently nil-deref here. Surface
	// a clean error instead.
	bus := s.engine.Bus()
	if bus == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /run-arrow: operator bus unavailable (engine not fully initialized)",
		}
	}
	var (
		mu     sync.Mutex
		events []runner.OperatorEvent
	)
	unsubscribe := bus.Subscribe(func(e runner.OperatorEvent) {
		switch e.Kind {
		case runner.OpEventPassOpened,
			runner.OpEventPassClosed,
			runner.OpEventInsufficientBasisRoundsExceeded,
			// I-H-2 closure: extend the filter to capture the
			// adversarial-cycle round events so the per-command
			// summary mirrors what the modal driver renders inline.
			// Pre-fix, the operator saw cycle progress in the modal
			// pane (via the modal driver's d.output) but the
			// /run-arrow output string never reflected the rounds —
			// an observable inconsistency between the two surfaces.
			runner.OpEventAdversarialRoundStart,
			runner.OpEventRemediationConverged,
			runner.OpEventRemediationEscalated:
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		}
	})
	// Defensive: the deferred call is a no-op after the explicit
	// unsubscribe below (the bus closer is idempotent), but it
	// guards against early returns added in a future refactor.
	defer unsubscribe()

	// SessionContext lets `/exit` cancel an in-flight dispatch.
	ctx := s.SessionContext()
	if ctx == nil {
		ctx = gocontext.Background()
	}

	res, err := s.engine.RunArrow(ctx, role, ctxName, def, tier)

	// Post-prod-readiness adversarial L-A: drop the subscription
	// BEFORE taking the final snapshot. The previous order
	// (snapshot → defer unsubscribe at function return) left a
	// window where a publisher firing between the snapshot and the
	// deferred unsubscribe would append to `events` but never reach
	// `captured`. After this unsubscribe returns, the bus's
	// subscriber list no longer references our callback, so no
	// further Publish fan-out can invoke it. We then take the
	// snapshot under the same mutex the callback would have used,
	// so any callback already in-flight when unsubscribe was
	// called has either finished appending (and we see its event)
	// or is still holding the mutex (in which case mu.Lock below
	// blocks until it returns, then we see its event).
	unsubscribe()
	mu.Lock()
	captured := append([]runner.OperatorEvent(nil), events...)
	mu.Unlock()

	var out strings.Builder
	for _, e := range captured {
		switch e.Kind {
		case runner.OpEventPassOpened:
			fmt.Fprintf(&out, "  · pass-opened   pass=%s role=%s context=%s\n",
				sanitizeOneLine(e.PassID),
				sanitizeOneLine(e.Role),
				sanitizeOneLine(e.Detail))
		case runner.OpEventPassClosed:
			fmt.Fprintf(&out, "  · pass-closed   pass=%s role=%s state/reason=%s\n",
				sanitizeOneLine(e.PassID),
				sanitizeOneLine(e.Role),
				sanitizeOneLine(e.Detail))
		case runner.OpEventInsufficientBasisRoundsExceeded:
			fmt.Fprintf(&out, "  ⚠ ib-rounds-exceeded  arrow=%s clause=%s detail=%s\n",
				sanitizeOneLine(e.ArrowID),
				sanitizeOneLine(e.ClauseID),
				sanitizeOneLine(e.Detail))
		case runner.OpEventAdversarialRoundStart:
			// I-H-2 closure: mirror the modal driver's per-event
			// surfacing so the summary string includes the cycle's
			// rounds.
			fmt.Fprintf(&out, "  · adversarial-round-start  arrow=%s pass=%s %s\n",
				sanitizeOneLine(e.ArrowID),
				sanitizeOneLine(e.PassID),
				sanitizeOneLine(e.Detail))
		case runner.OpEventRemediationConverged:
			fmt.Fprintf(&out, "  · remediation-converged  arrow=%s pass=%s %s\n",
				sanitizeOneLine(e.ArrowID),
				sanitizeOneLine(e.PassID),
				sanitizeOneLine(e.Detail))
		case runner.OpEventRemediationEscalated:
			fmt.Fprintf(&out, "  ⚠ remediation-escalated  arrow=%s pass=%s %s\n",
				sanitizeOneLine(e.ArrowID),
				sanitizeOneLine(e.PassID),
				sanitizeOneLine(e.Detail))
		}
	}

	if err != nil {
		var busy *runner.ErrRoleContextBusy
		if errors.As(err, &busy) {
			fmt.Fprintf(&out, "✗ /run-arrow: (role=%s, context=%s) held by pass %s",
				sanitizeOneLine(busy.Role),
				sanitizeOneLine(busy.Context),
				sanitizeOneLine(busy.HoldingPass))
		} else {
			fmt.Fprintf(&out, "✗ /run-arrow %s: %v", sanitizeOneLine(opts.ArrowID), err)
		}
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: strings.TrimRight(out.String(), "\n"),
		}
	}
	if res == nil {
		fmt.Fprintf(&out, "✗ /run-arrow %s: dispatcher returned nil result",
			sanitizeOneLine(opts.ArrowID))
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: strings.TrimRight(out.String(), "\n"),
		}
	}

	fmt.Fprintf(&out, "✓ arrow %s dispatched: pass=%s status=%s clauses=%d blocking-clauses=%d blocking-findings=%d",
		sanitizeOneLine(def.ID),
		sanitizeOneLine(res.PassID),
		sanitizeOneLine(res.ArrowStatus.String()),
		len(res.Runs),
		res.BlockingClauses,
		res.BlockingFindings,
	)
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(out.String(), "\n"),
	}
}

// handleListArrowsCommand renders the grid snapshot — every
// declared arrow id sorted, with its source→target / stratum /
// context. Surfaces the "no grid; run `ghyll init` first" hint
// when the grid is empty (C-3 final-step).
func (s *Session) handleListArrowsCommand() SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /list-arrows unavailable",
		}
	}
	ids := s.engine.Grid().Arrows()
	if len(ids) == 0 {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "no grid; run `ghyll init` first",
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "grid arrows (%d, version=%d):\n",
		len(ids), s.engine.Grid().Version())
	for _, id := range ids {
		def, ok := s.engine.Grid().Lookup(id)
		if !ok {
			// Concurrent invalidation between Arrows() + Lookup();
			// degrade gracefully rather than aborting the listing.
			fmt.Fprintf(&b, "  %s  (lookup-failed)\n", sanitizeOneLine(id))
			continue
		}
		fmt.Fprintf(&b, "  %s  %s → %s  stratum=%s context=%s clauses=%d\n",
			sanitizeOneLine(def.ID),
			sanitizeOneLine(def.SourceRole),
			sanitizeOneLine(def.TargetRole),
			sanitizeOneLine(def.Stratum),
			sanitizeOneLine(def.Context),
			len(def.Clauses),
		)
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: strings.TrimRight(b.String(), "\n"),
	}
}

// SessionContext is defined on Session in session.go; we rely on
// it here for /run-arrow cancellation plumbing.
