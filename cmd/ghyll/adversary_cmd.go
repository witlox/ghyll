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
// Diamond v4 / W-C-1 closure (ADR-v4-002): `/adversary enable` now
// CONSULTS the session's active model + its endpoint to decide
// whether a real adversary bundle can be wired. With no dialect
// configured the command refuses with a typed `no-dialect-configured`
// error rather than silently installing a no-op stub. With a dialect
// available, the bundle's Factory returns a real Adversary backed by
// the runtime's stores + a tier-bound Runner so `Attack` actually
// drives clause falsification through the registered evaluators.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/witlox/ghyll/runner"
)

// ErrNoDialectConfigured is the typed sentinel `/adversary enable`
// returns when the session has no active dialect resolvable from the
// loaded config (no endpoint, no model selected). Operators see this
// instead of a silent no-op bundle install. Mirrors ADR-v4-002's
// `no-dialect-configured` contract.
var ErrNoDialectConfigured = errors.New("no-dialect-configured")

// dialectConfigured reports whether the session's active model
// resolves to a usable dialect. A "usable" dialect means: the active
// model name is non-empty AND its configured endpoint is non-empty.
// The endpoint check matches `cmd/ghyll/main.go`'s pre-flight model
// check (config.validate refuses empty endpoints) — but at session
// runtime a follow-on dialect change could in principle yield an
// empty endpoint, so we re-check at /adversary enable time.
func (s *Session) dialectConfigured() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	name := strings.TrimSpace(s.activeModel)
	if name == "" {
		return false
	}
	mc, ok := s.cfg.Models[name]
	if !ok {
		return false
	}
	return strings.TrimSpace(mc.Endpoint) != "" &&
		strings.TrimSpace(mc.Dialect) != ""
}

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
// returns ErrNoDialectConfigured so the operator knows to configure
// one (ADR-v4-002 refusal contract).
func (s *Session) adversaryEnable() SlashCommandResult {
	hooks := s.engine.AdversarialHooks()
	if hooks == nil {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /adversary enable: hook bundle slot unavailable",
		}
	}
	if !s.dialectConfigured() {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("✗ /adversary enable refused: %s "+
				"(no active model endpoint resolves; configure a model "+
				"in ghyll.toml or pass --model)", ErrNoDialectConfigured),
		}
	}
	bundle := s.buildAdversaryBundle()
	if bundle == nil || !bundle.Validate() {
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: "✗ /adversary enable: bundle build returned malformed hooks (programming error)",
		}
	}
	hooks.Store(bundle)
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("✓ /adversary enabled (dialect=%s bundle wired)",
			s.activeModel),
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

// adversaryStatus reports the current bundle state + the resolved
// dialect. Includes a readiness flag (every required hook present)
// so operators see "wired-but-malformed" distinct from "unwired."
func (s *Session) adversaryStatus() SlashCommandResult {
	hooks := s.engine.AdversarialHooks()
	if hooks == nil || hooks.Load() == nil {
		dialect := "no-dialect-configured"
		if s.dialectConfigured() {
			dialect = s.activeModel
		}
		return SlashCommandResult{
			Handled: true, ContinueLoop: true,
			Output: fmt.Sprintf("adversary: DISABLED (dialect=%s)", dialect),
		}
	}
	loaded := hooks.Load()
	state := "wired"
	if !loaded.Validate() {
		state = "wired-but-malformed"
	}
	dialect := s.activeModel
	if !s.dialectConfigured() {
		dialect = "no-dialect-configured"
	}
	return SlashCommandResult{
		Handled: true, ContinueLoop: true,
		Output: fmt.Sprintf("adversary: %s (dialect=%s)", state, dialect),
	}
}

// buildAdversaryBundle constructs the real adversary hook bundle from
// the active dialect (W-C-1 closure). The bundle's Factory returns a
// fully-wired Adversary whose Attack drives clause falsification
// through the runtime's evaluators against the runtime's stores.
//
// V1 scope: OpenSweep / Classify are no-op hooks (LLM-backed sweep +
// depth-classify are dialect-specific extensions that ship per-family;
// the Factory + per-round Runner injection makes the cycle do real
// work via clause-falsification even with no-op LLM hooks). The
// ProducerFix hook returns a deterministic "no-op-fix" marker that
// the harness treats as zero-progress; per-round findings drive
// convergence/escalation through the existing remediation loop.
//
// Returns a Validate()=true bundle; callers refuse to install if
// somehow Validate() ends up false (programming error).
func (s *Session) buildAdversaryBundle() *runner.AdversarialHooks {
	if s == nil || s.engine == nil {
		return nil
	}
	rt := s.engine
	return &runner.AdversarialHooks{
		Factory: func(round int) *runner.Adversary {
			// Real Factory: wire the runtime's stores so per-round
			// Attack reports land on the live runtime (M3 closure).
			// The dispatcher-side wrapper at dispatcher_adversarial.go
			// fills these too, but supplying them at Factory time
			// makes the bundle self-contained for direct-test paths.
			a := runner.NewAdversary(rt.findings, rt.classifications, nil)
			a.AdversaryRole = fmt.Sprintf("adversary@%s", s.activeModel)
			return a
		},
		OpenSweep: func(_ context.Context, _ runner.AdversaryAttack) ([]runner.FindingRecord, error) {
			// V1 dialect-bundle OpenSweep: no-op. Real LLM-backed
			// sweep is a follow-up per dialect family (one production
			// hook bundle per dialect ships with its own OpenSweep).
			// Returning empty here is correct: the cycle still drives
			// clause-falsification through the Runner via Attack.
			return nil, nil
		},
		Classify: func(_ context.Context, _ runner.AdversaryAttack) ([]runner.Classification, error) {
			// V1 dialect-bundle Classify: no-op. Same rationale as
			// OpenSweep — the dispatch loop runs clause-falsification
			// even with no classify findings.
			return nil, nil
		},
		ProducerFix: func(_ context.Context, _ []runner.FindingRecord, _ int) ([]byte, error) {
			// V1 dialect-bundle ProducerFix: deterministic marker. The
			// harness treats this as a zero-progress fix; the
			// remediation loop converges on rounds without new
			// findings or escalates on rounds-max per cfg defaults.
			return []byte("no-op-fix"), nil
		},
		RemediationConfigDefaults: runner.RemediationConfig{
			RoundsMax: runner.DefaultRemediationRoundsMax,
		},
	}
}

// autoEnableAdversarial wires the bundle at session-open time when a
// dialect is configured. Per ADR-v4-002 the default is ON when a
// dialect resolves; OFF (with a banner) otherwise. Called from
// initEngine AFTER s.engine is set. Outputs an operator banner; the
// CI path (no API key, no endpoint) sees the disabled banner.
func (s *Session) autoEnableAdversarial() {
	if s == nil || s.engine == nil {
		return
	}
	hooks := s.engine.AdversarialHooks()
	if hooks == nil {
		return
	}
	if !s.dialectConfigured() {
		s.output("ℹ adversarial cycle: disabled (no dialect configured; type `/adversary enable` to wire)")
		return
	}
	bundle := s.buildAdversaryBundle()
	if bundle == nil || !bundle.Validate() {
		s.output("⚠ adversarial cycle: auto-enable refused (bundle build failed; type `/adversary status` to inspect)")
		return
	}
	hooks.Store(bundle)
	s.output(fmt.Sprintf("ℹ adversarial cycle: enabled (dialect=%s)", s.activeModel))
}
