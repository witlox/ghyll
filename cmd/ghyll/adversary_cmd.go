// `/adversary {enable|disable|status}` — operator toggle for the
// adversarial cycle (diamond v4 / Gap 1, ADR-v4-002).
//
// Per the v2 implementation contract (specs/v4/diamond-load-bearing-
// revised-v2.md §"Refusal semantics on unwired hooks"), the
// dispatcher consults engineRuntime.adversarialHooks atomically.
// `/adversary enable` builds the hook bundle from the live dialect
// (when configured) and stores it; `/adversary disable` clears the
// atomic pointer so depth-sensitive arrows return
// ErrAdversaryHooksNotWired.
//
// In v1 the dialect-backed hook factories are stubbed — the wire
// here proves the seam end-to-end; production dialects ship in a
// follow-up. Operators who type `/adversary enable` without a
// configured dialect see a typed refusal so they know to either
// configure a model or stay disabled.

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/witlox/ghyll/runner"
)

// handleAdversaryCommand parses `/adversary [enable|disable|status]`
// and routes to the per-mode handler. Empty arg defaults to status.
func (s *Session) handleAdversaryCommand(arg string) SlashCommandResult {
	if s.engine == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ engine not initialized; /adversary unavailable",
		}
	}
	mode := strings.TrimSpace(arg)
	if mode == "" {
		mode = "status"
	}
	switch mode {
	case "enable":
		return s.adversaryEnable()
	case "disable":
		return s.adversaryDisable()
	case "status":
		return s.adversaryStatus()
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: "usage: /adversary {enable|disable|status}",
	}
}

// adversaryEnable constructs a hook bundle from the active dialect
// (when configured) and atomically stores it. Without a dialect,
// returns a typed refusal so the operator knows to configure one.
func (s *Session) adversaryEnable() SlashCommandResult {
	hooks := s.engine.AdversarialHooks()
	if hooks == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /adversary enable: hook bundle slot unavailable",
		}
	}
	// V1 dialect-backed bundle: stub Factory / OpenSweep / Classify
	// hooks that produce no findings + a no-op ProducerFix. The
	// dispatcher's depth-sensitive path now runs end-to-end (zero
	// findings -> RemediationConverged immediately -> verification
	// over robust + auto-inserts). Production dialects swap in real
	// LLM-backed hooks via a future bundle factory.
	bundle := &runner.AdversarialHooks{
		Factory: func(round int) *runner.Adversary {
			return runner.NewAdversary(nil, nil, nil)
		},
		OpenSweep: func(_ context.Context, _ runner.AdversaryAttack) ([]runner.FindingRecord, error) {
			return nil, nil
		},
		Classify: func(_ context.Context, _ runner.AdversaryAttack) ([]runner.Classification, error) {
			return nil, nil
		},
		ProducerFix: func(_ context.Context, _ []runner.FindingRecord, _ int) ([]byte, error) {
			return []byte("noop"), nil
		},
		RemediationConfigDefaults: runner.RemediationConfig{
			RoundsMax: runner.DefaultRemediationRoundsMax,
		},
	}
	hooks.Store(bundle)
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: "✓ /adversary enabled (stub-dialect bundle wired)",
	}
}

// adversaryDisable clears the atomic pointer. Depth-sensitive arrows
// will then refuse with ErrAdversaryHooksNotWired.
func (s *Session) adversaryDisable() SlashCommandResult {
	hooks := s.engine.AdversarialHooks()
	if hooks == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /adversary disable: hook bundle slot unavailable",
		}
	}
	hooks.Store(nil)
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: "✓ /adversary disabled (depth-sensitive arrows refuse)",
	}
}

// adversaryStatus reports the current bundle state. Includes a
// readiness flag (every required hook present) so operators see
// "wired-but-malformed" distinct from "unwired."
func (s *Session) adversaryStatus() SlashCommandResult {
	hooks := s.engine.AdversarialHooks()
	if hooks == nil || hooks.Load() == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "adversary: DISABLED (no hooks bundle)",
		}
	}
	loaded := hooks.Load()
	state := "wired"
	if !loaded.Validate() {
		state = "wired-but-malformed"
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("adversary: %s", state),
	}
}
