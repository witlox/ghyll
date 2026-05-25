// `/invalidate-arrow` — operator-triggered arrow invalidation
// (diamond v4 / integrator-pass I-C-1 closure).
//
// Per ADR-v4-008 + runner/operatorbus.go:88, OpEventArrowInvalidated
// is "produced when the operator types `/invalidate-arrow`". The
// substrate landed (consumer chain at modal_driver.go:173, observer
// at session_engine.go:644 writing to arrow_invalidations, typed
// payload contract per ADR-v4-005), but no producer existed in the
// production codebase — the table would only see rows via direct
// test-harness Publish.
//
// This file ships the producer. The shape mirrors
// drain_amendments_cmd.go:
//   - Refuse without an op-id (the row carries operator identity).
//   - Refuse on empty / unknown arrow-id (look up via Grid.Lookup).
//   - Publish OpEventArrowInvalidated with the typed payload
//     (arrow_id, op_id, reason, source); the observer in
//     session_engine.go writes the arrow_invalidations row
//     synchronously (Publish fans out inline).
//   - Surface inline confirmation referencing the persistence
//     target so the operator sees the row landed.

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/witlox/ghyll/runner"
)

// invalidateArrowOptions captures the parsed `/invalidate-arrow`
// args. The arrow-id is required; --reason is optional.
type invalidateArrowOptions struct {
	ArrowID string
	Reason  string
}

// parseInvalidateArrowArgs parses the arg-suffix of
// `/invalidate-arrow`. Returns usage error on empty / malformed
// input.
//
// Wire form:
//
//	<arrow-id> [--reason <text>]
//
// --reason may contain whitespace; everything after the flag is
// captured up to end-of-line. Unknown flags are rejected.
func parseInvalidateArrowArgs(arg string) (invalidateArrowOptions, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return invalidateArrowOptions{}, fmt.Errorf("arrow-id required")
	}
	fields := strings.Fields(trimmed)
	if strings.HasPrefix(fields[0], "--") {
		return invalidateArrowOptions{}, fmt.Errorf("arrow-id required (first positional must not start with --)")
	}
	opts := invalidateArrowOptions{ArrowID: fields[0]}
	i := 1
	for i < len(fields) {
		switch fields[i] {
		case "--reason":
			if i+1 >= len(fields) {
				return invalidateArrowOptions{}, fmt.Errorf("--reason requires a value")
			}
			// Consume the remainder of the input as the reason text
			// so reasons may contain whitespace. Locate the position
			// of `--reason` in the original (sanitized) input and
			// capture from after the flag.
			idx := strings.Index(trimmed, "--reason")
			if idx < 0 {
				// Defensive: fall back to single token.
				opts.Reason = strings.TrimSpace(fields[i+1])
			} else {
				opts.Reason = strings.TrimSpace(trimmed[idx+len("--reason"):])
			}
			if opts.Reason == "" {
				return invalidateArrowOptions{}, fmt.Errorf("--reason value must not be empty")
			}
			return opts, nil
		default:
			return invalidateArrowOptions{}, fmt.Errorf("unknown flag %q", fields[i])
		}
	}
	return opts, nil
}

// handleInvalidateArrowCommand is the slash-command entry point for
// `/invalidate-arrow`. Refuses without an op-id (per gates.md §3.7
// the arrow_invalidations audit row carries the operator identity).
//
// Output on success:
//
//	✓ arrow X invalidated; persisted to .ghyll/engine.db arrow_invalidations
//
// Refusals are prefixed with ✗ and explain the cause.
func (s *Session) handleInvalidateArrowCommand(arg string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /invalidate-arrow unavailable",
		}
	}
	if s.opID == "" {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /invalidate-arrow refuses: no op-id set; use /op-id <identity> first",
		}
	}
	opts, err := parseInvalidateArrowArgs(arg)
	if err != nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("usage: /invalidate-arrow <arrow-id> [--reason <text>]  (%v)", err),
		}
	}
	grid := s.engine.Grid()
	if grid == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /invalidate-arrow: grid unavailable (engine partially initialized)",
		}
	}
	if _, ok := grid.Lookup(opts.ArrowID); !ok {
		if len(grid.Arrows()) == 0 {
			return SlashCommandResult{
				Handled: true, ContinueLoop: true,
				Output: "✗ no grid; run `ghyll init` first",
			}
		}
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ arrow %q not in grid (try /list-arrows)", sanitizeOneLine(opts.ArrowID)),
		}
	}
	bus := s.engine.Bus()
	if bus == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /invalidate-arrow: operator bus unavailable (engine not fully initialized)",
		}
	}

	reason := opts.Reason
	if reason == "" {
		reason = "operator-requested"
	}

	// Publish the typed event. The observer registered in
	// attachJournal (session_engine.go:644) writes the
	// arrow_invalidations row synchronously inside Publish's
	// fan-out, so by the time Publish returns the row is on disk.
	//
	// Payload keys per ADR-v4-005: arrow_id, op_id, reason, source.
	// "source" distinguishes operator-typed invalidations from any
	// future auto-invalidation pathway.
	bus.Publish(runner.OperatorEvent{
		Kind:      runner.OpEventArrowInvalidated,
		ArrowID:   opts.ArrowID,
		OpID:      s.opID,
		Detail:    reason,
		Timestamp: time.Now(),
		Payload: map[string]string{
			"arrow_id": opts.ArrowID,
			"op_id":    s.opID,
			"reason":   reason,
			"source":   "operator",
		},
	})

	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("✓ arrow %s invalidated; persisted to .ghyll/engine.db arrow_invalidations (reason=%s op-id=%s)",
			sanitizeOneLine(opts.ArrowID), sanitizeOneLine(reason), sanitizeOneLine(s.opID)),
	}
}
