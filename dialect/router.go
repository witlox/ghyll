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
	ContextCompactedBelow int // post-compaction depth, 0 if not compacted

	// GateFloor is the v2 gate's MinTier requirement for the
	// currently-traversing arrow (runner.RoutingRequirement.MinTier).
	// Passed as int (0..3 == NONE/SHALLOW/MOCKED/REALISTIC) so this
	// package stays a near-leaf (no runner import).
	//
	// Zero means "no gate floor" — the legacy behavior, where the
	// router decides purely on context/tool depth and operator
	// overrides.
	GateFloor int

	Config config.RoutingConfig
}

// RoutingDecision is the router's output.
// cmd/ghyll orchestrates the actual compaction and handoff.
type RoutingDecision struct {
	Action         string // "none", "escalate", "de_escalate"
	TargetModel    string
	NeedCompaction bool
}

// Evaluate applies the routing decision table.
// Rows evaluated top to bottom, first match wins.
//
// Gate-floor logic (Row 2): if the v2 gate demands MinTier >=
// GateFloorEscalateAtRank, the dispatcher MUST land on DeepModel.
// This row is positioned before the existing escalation rows so a
// gate requirement always escalates regardless of context-depth.
//
// De-escalation (Row 7) is BLOCKED while the gate floor is active
// — the gate's depth requirement is authoritative; per-turn
// context-depth signals cannot override it.
func Evaluate(inputs RouterInputs) RoutingDecision {
	cfg := inputs.Config

	// Row 1: model locked — absolute, no changes
	if inputs.ModelLocked {
		return RoutingDecision{Action: "none", TargetModel: inputs.ActiveModel}
	}

	// No escalation possible without a deep tier model
	canEscalate := cfg.DeepModel != "" && cfg.DeepModel != cfg.DefaultModel

	// Row 2: gate-floor escalation. The v2 routing decision is
	// authoritative; per-turn signals layer on top.
	gateFloorActive := inputs.GateFloor >= cfg.GateFloorEscalateAtRank && cfg.GateFloorEscalateAtRank > 0
	if canEscalate && gateFloorActive && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 3: /deep override, currently on fast tier
	if canEscalate && inputs.DeepOverride && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 4: backfill triggered, currently on fast tier
	if canEscalate && inputs.BackfillTriggered && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 5: context depth exceeds threshold, currently on fast tier
	if canEscalate && inputs.ContextDepth > cfg.ContextDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel, NeedCompaction: true}
	}

	// Row 6: tool depth exceeds threshold, currently on fast tier
	if canEscalate && inputs.ToolDepth > cfg.ToolDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 7: de-escalation. BLOCKED when gate-floor is active — the
	// gate's depth requirement is authoritative and cannot be
	// overridden by post-compaction context-depth signals.
	if canEscalate && !gateFloorActive &&
		inputs.ContextCompactedBelow > 0 &&
		inputs.ContextCompactedBelow < cfg.ContextDepthThreshold &&
		inputs.ActiveModel == cfg.DeepModel &&
		!inputs.DeepOverride {
		return RoutingDecision{Action: "de_escalate", TargetModel: cfg.DefaultModel}
	}

	// Row 8: steady state
	return RoutingDecision{Action: "none", TargetModel: inputs.ActiveModel}
}
