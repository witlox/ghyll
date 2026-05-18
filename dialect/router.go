package dialect

import "github.com/witlox/ghyll/config"

// RouterInputs are the values the router evaluates.
// Provided by cmd/ghyll from various sources.
type RouterInputs struct {
	ContextDepth          int
	ToolDepth             int
	ModelLocked           bool
	DeepOverride          bool
	ActiveModel           string
	BackfillTriggered     bool
	ContextCompactedBelow int

	// GateFloor is the v2 gate's MinTier requirement for the
	// currently-traversing arrow (runner.RoutingRequirement.MinTier).
	// Passed as int (0..3 == NONE/SHALLOW/MOCKED/REALISTIC) so this
	// package stays a near-leaf (no runner import).
	//
	// Zero means "no gate floor" — the legacy behavior. Out-of-range
	// values (negative or > 3) produce `Action: ActionInvalid` so
	// caller bugs surface loudly.
	GateFloor int

	Config config.RoutingConfig
}

// RoutingDecision is the router's output.
type RoutingDecision struct {
	Action         string
	TargetModel    string
	NeedCompaction bool
	Reason         RoutingReason // validation-pass-8 R6
}

// Action constants. ActionGateUnsatisfiable and ActionGateLockedConflict
// (validation-pass-8 R1, R7) surface §7.1 violations: a depth-sensitive
// gate must NEVER be silently laundered through an insufficient tier.
const (
	ActionNone               = "none"
	ActionEscalate           = "escalate"
	ActionDeEscalate         = "de_escalate"
	ActionInvalid            = "invalid"
	ActionGateUnsatisfiable  = "gate_unsatisfiable"   // R1: no DeepModel for an active gate floor
	ActionGateLockedConflict = "gate_locked_conflict" // R7: ModelLocked vs active gate floor
)

// RoutingReason is the typed wire form for why a routing decision
// fired. Carried on RoutingDecision so the session-loop layer (and
// downstream attestation) can distinguish gate-floor escalations
// from context-depth escalations. Validation-pass-8 R6/R12.
type RoutingReason string

const (
	ReasonGateFloor          RoutingReason = "gate-floor"
	ReasonDeepOverride       RoutingReason = "deep-override"
	ReasonBackfill           RoutingReason = "backfill"
	ReasonContextDepth       RoutingReason = "context-depth"
	ReasonToolDepth          RoutingReason = "tool-depth"
	ReasonContextCompacted   RoutingReason = "context-compacted"
	ReasonModelLocked        RoutingReason = "model-locked"
	ReasonSteadyState        RoutingReason = "steady-state"
	ReasonInvalidInput       RoutingReason = "invalid-input"
	ReasonGateUnsatisfiable  RoutingReason = "gate-unsatisfiable"
	ReasonGateLockedConflict RoutingReason = "gate-locked-conflict"
)

// Bounds for the gate-floor mechanism. Mirrors runner.DepthRank's
// MinDepthRank / MaxDepthRank.
const (
	gateRankMin = 0
	gateRankMax = 3
)

// Evaluate applies the routing decision table.
//
// Precedence (rows evaluated top to bottom, first match wins):
//
//  1. Invalid input — out-of-range GateFloor.
//  2. ModelLocked + gate floor active → ActionGateLockedConflict (R7).
//  3. ModelLocked alone → ActionNone, ReasonModelLocked.
//  4. Gate floor active + no DeepModel → ActionGateUnsatisfiable (R1).
//  5. Gate floor active + not yet on DeepModel → escalate, ReasonGateFloor.
//  6. /deep override → escalate, ReasonDeepOverride.
//  7. Backfill triggered → escalate, ReasonBackfill.
//  8. Context depth threshold → escalate + needCompaction, ReasonContextDepth.
//  9. Tool depth threshold → escalate, ReasonToolDepth.
//
// 10. De-escalation (blocked while gate floor active) → ReasonContextCompacted.
// 11. Steady state → ActionNone, ReasonSteadyState.
//
// Per gates.md §7.1: a depth-sensitive gate is NEVER laundered. The
// gate-unsatisfiable / gate-locked-conflict actions surface that
// invariant to the session loop; the session must route the clause
// to operator attestation rather than dispatch on the insufficient
// tier.
func Evaluate(inputs RouterInputs) RoutingDecision {
	cfg := inputs.Config

	// R3: validate the GateFloor input. Out-of-range is a programmer
	// or config error; surface explicitly rather than silently
	// disable.
	if inputs.GateFloor < gateRankMin || inputs.GateFloor > gateRankMax {
		return RoutingDecision{
			Action:      ActionInvalid,
			TargetModel: inputs.ActiveModel,
			Reason:      ReasonInvalidInput,
		}
	}

	canEscalate := cfg.DeepModel != "" && cfg.DeepModel != cfg.DefaultModel

	// gateFloorActive: the operator's explicit GateFloorDisabled bool
	// (R4) cleanly distinguishes "off" from "default-on at threshold".
	gateFloorActive := !cfg.GateFloorDisabled &&
		cfg.GateFloorEscalateAtRank > 0 &&
		inputs.GateFloor >= cfg.GateFloorEscalateAtRank

	// Row 1a: Locked + active gate floor → §7.1 conflict.
	if inputs.ModelLocked && gateFloorActive {
		return RoutingDecision{
			Action:      ActionGateLockedConflict,
			TargetModel: inputs.ActiveModel,
			Reason:      ReasonGateLockedConflict,
		}
	}

	// Row 1b: Locked alone — absolute, no changes.
	if inputs.ModelLocked {
		return RoutingDecision{
			Action:      ActionNone,
			TargetModel: inputs.ActiveModel,
			Reason:      ReasonModelLocked,
		}
	}

	// Row 2a: gate floor active but no DeepModel to escalate to →
	// §7.1 unsatisfiable. The session loop must NOT silently
	// dispatch on the insufficient tier.
	if gateFloorActive && !canEscalate {
		return RoutingDecision{
			Action:      ActionGateUnsatisfiable,
			TargetModel: inputs.ActiveModel,
			Reason:      ReasonGateUnsatisfiable,
		}
	}

	// Row 2b: gate floor active + can escalate + not yet on DeepModel.
	// Authoritative over per-turn signals (/deep, context-depth, etc.).
	// Note R5: this fires even when ActiveModel is a third value
	// (operator manually chose a non-DefaultModel non-DeepModel) —
	// the gate's depth requirement is authoritative.
	if gateFloorActive && inputs.ActiveModel != cfg.DeepModel {
		return RoutingDecision{
			Action:      ActionEscalate,
			TargetModel: cfg.DeepModel,
			Reason:      ReasonGateFloor,
		}
	}

	// Row 3: /deep override, currently on fast tier.
	if canEscalate && inputs.DeepOverride && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{
			Action:      ActionEscalate,
			TargetModel: cfg.DeepModel,
			Reason:      ReasonDeepOverride,
		}
	}

	// Row 4: backfill triggered, currently on fast tier.
	if canEscalate && inputs.BackfillTriggered && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{
			Action:      ActionEscalate,
			TargetModel: cfg.DeepModel,
			Reason:      ReasonBackfill,
		}
	}

	// Row 5: context depth threshold.
	if canEscalate && inputs.ContextDepth > cfg.ContextDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{
			Action:         ActionEscalate,
			TargetModel:    cfg.DeepModel,
			NeedCompaction: true,
			Reason:         ReasonContextDepth,
		}
	}

	// Row 6: tool depth threshold.
	if canEscalate && inputs.ToolDepth > cfg.ToolDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{
			Action:      ActionEscalate,
			TargetModel: cfg.DeepModel,
			Reason:      ReasonToolDepth,
		}
	}

	// Row 7: de-escalation. BLOCKED when gate-floor is active.
	if canEscalate && !gateFloorActive &&
		inputs.ContextCompactedBelow > 0 &&
		inputs.ContextCompactedBelow < cfg.ContextDepthThreshold &&
		inputs.ActiveModel == cfg.DeepModel &&
		!inputs.DeepOverride {
		return RoutingDecision{
			Action:      ActionDeEscalate,
			TargetModel: cfg.DefaultModel,
			Reason:      ReasonContextCompacted,
		}
	}

	// Row 8: steady state.
	return RoutingDecision{
		Action:      ActionNone,
		TargetModel: inputs.ActiveModel,
		Reason:      ReasonSteadyState,
	}
}
