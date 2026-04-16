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
	Config                config.RoutingConfig
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
func Evaluate(inputs RouterInputs) RoutingDecision {
	cfg := inputs.Config

	// Row 1: model locked — absolute, no changes
	if inputs.ModelLocked {
		return RoutingDecision{Action: "none", TargetModel: inputs.ActiveModel}
	}

	// No escalation possible without a deep tier model
	canEscalate := cfg.DeepModel != "" && cfg.DeepModel != cfg.DefaultModel

	// Row 2: /deep override, currently on fast tier
	if canEscalate && inputs.DeepOverride && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 3: backfill triggered, currently on fast tier
	if canEscalate && inputs.BackfillTriggered && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 4: context depth exceeds threshold, currently on fast tier
	if canEscalate && inputs.ContextDepth > cfg.ContextDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel, NeedCompaction: true}
	}

	// Row 5: tool depth exceeds threshold, currently on fast tier
	if canEscalate && inputs.ToolDepth > cfg.ToolDepthThreshold && inputs.ActiveModel == cfg.DefaultModel {
		return RoutingDecision{Action: "escalate", TargetModel: cfg.DeepModel}
	}

	// Row 6: de-escalation — context compacted below threshold, on deep tier, no /deep
	if canEscalate && inputs.ContextCompactedBelow > 0 &&
		inputs.ContextCompactedBelow < cfg.ContextDepthThreshold &&
		inputs.ActiveModel == cfg.DeepModel &&
		!inputs.DeepOverride {
		return RoutingDecision{Action: "de_escalate", TargetModel: cfg.DefaultModel}
	}

	// Row 7: steady state
	return RoutingDecision{Action: "none", TargetModel: inputs.ActiveModel}
}
