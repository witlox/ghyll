package dialect

import (
	"testing"

	"github.com/witlox/ghyll/config"
)

func defaultRoutingConfig() config.RoutingConfig {
	return config.RoutingConfig{
		DefaultModel:          "m25",
		DeepModel:             "glm5",
		ContextDepthThreshold: 32000,
		ToolDepthThreshold:    5,
		EnableAutoRouting:     true,
	}
}

// TestScenario_Routing_FreshSession maps to:
// Scenario: Fresh session starts on fast tier
func TestScenario_Routing_FreshSession(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel: "m25",
		Config:      defaultRoutingConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q", d.Action, "none")
	}
}

// TestScenario_Routing_ContextDepthEscalates maps to:
// Scenario: Context depth escalates to deep tier
func TestScenario_Routing_ContextDepthEscalates(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:  "m25",
		ContextDepth: 35000,
		Config:       defaultRoutingConfig(),
	})
	if d.Action != "escalate" {
		t.Errorf("action = %q, want %q", d.Action, "escalate")
	}
	if d.TargetModel != "glm5" {
		t.Errorf("target = %q, want %q", d.TargetModel, "glm5")
	}
	if !d.NeedCompaction {
		t.Error("expected NeedCompaction=true for context depth escalation")
	}
}

// TestScenario_Routing_ToolDepthEscalates maps to:
// Scenario: Tool depth escalates to deep tier
func TestScenario_Routing_ToolDepthEscalates(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel: "m25",
		ToolDepth:   6,
		Config:      defaultRoutingConfig(),
	})
	if d.Action != "escalate" {
		t.Errorf("action = %q, want %q", d.Action, "escalate")
	}
	if d.TargetModel != "glm5" {
		t.Errorf("target = %q, want %q", d.TargetModel, "glm5")
	}
	if d.NeedCompaction {
		t.Error("tool depth escalation should not need compaction")
	}
}

// TestScenario_Routing_DeepOverride maps to:
// Scenario: /deep temporary override
func TestScenario_Routing_DeepOverride(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:  "m25",
		DeepOverride: true,
		Config:       defaultRoutingConfig(),
	})
	if d.Action != "escalate" {
		t.Errorf("action = %q, want %q", d.Action, "escalate")
	}
	if d.TargetModel != "glm5" {
		t.Errorf("target = %q, want %q", d.TargetModel, "glm5")
	}
}

// TestScenario_Routing_DeepReverts maps to:
// Scenario: /deep reverts when conditions clear
func TestScenario_Routing_DeepReverts(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:           "glm5",
		DeepOverride:          false, // /deep cleared after conditions change
		ContextCompactedBelow: 15000,
		Config:                defaultRoutingConfig(),
	})
	if d.Action != "de_escalate" {
		t.Errorf("action = %q, want %q", d.Action, "de_escalate")
	}
	if d.TargetModel != "m25" {
		t.Errorf("target = %q, want %q", d.TargetModel, "m25")
	}
}

// TestScenario_Routing_DeepIgnoredWhenLocked maps to:
// Scenario: /deep ignored when --model flag is set
func TestScenario_Routing_DeepIgnoredWhenLocked(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:  "m25",
		ModelLocked:  true,
		DeepOverride: true,
		Config:       defaultRoutingConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q (locked should override /deep)", d.Action, "none")
	}
}

// TestScenario_Routing_ExplicitModelFlag maps to:
// Scenario: Explicit model flag overrides routing
func TestScenario_Routing_ExplicitModelFlag(t *testing.T) {
	// Even with escalation conditions met, locked model doesn't change
	d := Evaluate(RouterInputs{
		ActiveModel:  "glm5",
		ModelLocked:  true,
		ContextDepth: 50000,
		ToolDepth:    10,
		Config:       defaultRoutingConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q", d.Action, "none")
	}
}

// TestScenario_Routing_DeEscalation maps to:
// Scenario: De-escalation after context compaction
func TestScenario_Routing_DeEscalation(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:           "glm5",
		ContextCompactedBelow: 15000,
		Config:                defaultRoutingConfig(),
	})
	if d.Action != "de_escalate" {
		t.Errorf("action = %q, want %q", d.Action, "de_escalate")
	}
	if d.TargetModel != "m25" {
		t.Errorf("target = %q, want %q", d.TargetModel, "m25")
	}
}

// TestScenario_Routing_DriftEscalates maps to:
// Scenario: Drift backfill triggers escalation
func TestScenario_Routing_DriftEscalates(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:       "m25",
		BackfillTriggered: true,
		Config:            defaultRoutingConfig(),
	})
	if d.Action != "escalate" {
		t.Errorf("action = %q, want %q", d.Action, "escalate")
	}
	if d.TargetModel != "glm5" {
		t.Errorf("target = %q, want %q", d.TargetModel, "glm5")
	}
}

// TestScenario_Routing_NoDeEscalateWithDeepOverride
// Ensures GLM-5 stays active when /deep is set, even if context is low
func TestScenario_Routing_NoDeEscalateWithDeepOverride(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:           "glm5",
		DeepOverride:          true,
		ContextCompactedBelow: 10000,
		Config:                defaultRoutingConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q (deep override prevents de-escalation)", d.Action, "none")
	}
}

// TestScenario_Routing_SteadyState
// No escalation conditions met — stay on current model
func TestScenario_Routing_SteadyState(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:  "m25",
		ContextDepth: 10000,
		ToolDepth:    2,
		Config:       defaultRoutingConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q", d.Action, "none")
	}
}

// singleTierConfig returns a routing config with no deep tier configured.
// Escalation is disabled; the router should degrade to steady-state.
func singleTierConfig() config.RoutingConfig {
	c := defaultRoutingConfig()
	c.DeepModel = ""
	return c
}

// TestScenario_Routing_SingleTierNoEscalate verifies that when DeepModel is
// unset, the router never escalates regardless of trigger conditions. This
// exercises the canEscalate guard on rows 2-5.
func TestScenario_Routing_SingleTierNoEscalate(t *testing.T) {
	cases := []struct {
		name   string
		inputs RouterInputs
	}{
		{"deep override", RouterInputs{ActiveModel: "m25", DeepOverride: true}},
		{"backfill", RouterInputs{ActiveModel: "m25", BackfillTriggered: true}},
		{"context depth", RouterInputs{ActiveModel: "m25", ContextDepth: 40000}},
		{"tool depth", RouterInputs{ActiveModel: "m25", ToolDepth: 10}},
	}
	for _, tc := range cases {
		tc.inputs.Config = singleTierConfig()
		d := Evaluate(tc.inputs)
		if d.Action != "none" {
			t.Errorf("%s: action = %q, want %q (single-tier must not escalate)", tc.name, d.Action, "none")
		}
	}
}

// TestScenario_Routing_SingleTierNoDeEscalate verifies that Row 6
// de-escalation cannot fire when DeepModel is unset (ADV-5 fix). Before
// the fix, Row 6 lacked the canEscalate guard; with DeepModel="" the row
// compared ActiveModel == "" which never matched, so it was dead code —
// but the guard makes the intent explicit and defends against future
// regressions.
func TestScenario_Routing_SingleTierNoDeEscalate(t *testing.T) {
	d := Evaluate(RouterInputs{
		ActiveModel:           "m25",
		ContextCompactedBelow: 10000,
		Config:                singleTierConfig(),
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q (single-tier must not de-escalate)", d.Action, "none")
	}
}

// TestScenario_Routing_DeepEqualsDefaultNoEscalate verifies that when
// deep_model == default_model, escalation is disabled. This is the ADV-4
// edge case — accepted as intended behaviour but pinned by a test so the
// semantics cannot drift silently.
func TestScenario_Routing_DeepEqualsDefaultNoEscalate(t *testing.T) {
	cfg := defaultRoutingConfig()
	cfg.DeepModel = cfg.DefaultModel
	d := Evaluate(RouterInputs{
		ActiveModel:  "m25",
		ContextDepth: 40000,
		Config:       cfg,
	})
	if d.Action != "none" {
		t.Errorf("action = %q, want %q (deep==default disables escalation)", d.Action, "none")
	}
}
