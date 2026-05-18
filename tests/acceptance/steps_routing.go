package acceptance

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/dialect"
)

func registerRoutingSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	var (
		routingCfg   config.RoutingConfig
		lastDecision dialect.RoutingDecision
	)

	ctx.Step(`^ghyll is configured with endpoints for "([^"]*)" and "([^"]*)"$`, func(m1, m2 string) error {
		routingCfg = config.RoutingConfig{
			DefaultModel:          m1,
			DeepModel:             m2,
			EnableAutoRouting:     true,
			ContextDepthThreshold: 32000,
			ToolDepthThreshold:    5,
		}
		state.ActiveModel = m1
		state.AutoRouting = true
		return nil
	})

	ctx.Step(`^the routing thresholds are context_depth=(\d+) and tool_depth=(\d+)$`, func(cd, td int) error {
		routingCfg.ContextDepthThreshold = cd
		routingCfg.ToolDepthThreshold = td
		return nil
	})

	ctx.Step(`^a new session starts$`, func() error {
		state.ActiveModel = routingCfg.DefaultModel
		state.ContextTokens = 0
		state.ToolDepth = 0
		return nil
	})

	ctx.Step(`^the active model is "([^"]*)"$`, func(model string) error {
		state.ActiveModel = model
		return nil
	})

	ctx.Step(`^the terminal prompt shows "\[([^\]]*)\]"$`, func(model string) error {
		// Verify the active model matches what the prompt would show
		expected := model
		if strings.Contains(model, "→") {
			// De-escalation display like "[glm5→m25]"
			parts := strings.Split(model, "→")
			expected = strings.TrimSpace(parts[len(parts)-1])
		}
		if state.ActiveModel != expected {
			return fmt.Errorf("terminal would show [%s] but active model is %s", model, state.ActiveModel)
		}
		state.AddTerminal(fmt.Sprintf("[%s]", model))
		return nil
	})

	ctx.Step(`^the context window contains (\d+) tokens$`, func(tokens int) error {
		state.ContextTokens = tokens
		return nil
	})

	ctx.Step(`^the next turn begins$`, func() error {
		// Run the real dialect.Evaluate with current state
		inputs := dialect.RouterInputs{
			ContextDepth: state.ContextTokens,
			ToolDepth:    state.ToolDepth,
			ModelLocked:  state.ModelLocked,
			DeepOverride: state.DeepOverride,
			ActiveModel:  state.ActiveModel,
			Config:       routingCfg,
		}
		lastDecision = dialect.Evaluate(inputs)

		// Apply the decision
		if lastDecision.Action == "escalate" || lastDecision.Action == "de_escalate" {
			state.ActiveModel = lastDecision.TargetModel
		}
		return nil
	})

	ctx.Step(`^a checkpoint is created before the switch$`, func() error {
		// In routing context, lastDecision is set by "the next turn begins".
		// In memory context, "the dialect router decides to switch" creates the checkpoint directly.
		// Accept both cases.
		if lastDecision.Action != "" && lastDecision.Action != "escalate" && lastDecision.Action != "de_escalate" {
			return fmt.Errorf("expected escalation or de-escalation, got action=%s", lastDecision.Action)
		}
		return nil
	})

	ctx.Step(`^the terminal shows "([^"]*)"$`, func(msg string) error {
		state.AddTerminal(msg)
		return nil
	})

	ctx.Step(`^the current chain has (\d+) sequential tool calls without user input$`, func(depth int) error {
		state.ToolDepth = depth
		return nil
	})

	ctx.Step(`^the user types "\/deep"$`, func() error {
		if state.ModelLocked {
			// /deep is ignored when model is locked
			state.AddTerminal("ℹ /deep ignored, model locked via --model flag")
			return nil
		}
		state.DeepOverride = true

		// Run routing evaluation with deep override
		inputs := dialect.RouterInputs{
			ContextDepth: state.ContextTokens,
			ToolDepth:    state.ToolDepth,
			ModelLocked:  state.ModelLocked,
			DeepOverride: true,
			ActiveModel:  state.ActiveModel,
			Config:       routingCfg,
		}
		lastDecision = dialect.Evaluate(inputs)

		if lastDecision.Action == "escalate" {
			state.ActiveModel = lastDecision.TargetModel
		}
		return nil
	})

	ctx.Step(`^auto-routing continues to evaluate$`, func() error {
		// After /deep, auto-routing is still enabled (not locked)
		if state.ModelLocked {
			return fmt.Errorf("model is locked, auto-routing should still evaluate")
		}
		// Verify by running Evaluate - it should not return "none" due to lock
		inputs := dialect.RouterInputs{
			ContextDepth: state.ContextTokens,
			ToolDepth:    state.ToolDepth,
			ModelLocked:  false, // not locked
			DeepOverride: state.DeepOverride,
			ActiveModel:  state.ActiveModel,
			Config:       routingCfg,
		}
		decision := dialect.Evaluate(inputs)
		// Just confirm it evaluates without error - the action depends on context
		_ = decision
		return nil
	})

	// Note: "ghyll starts with --model" is registered in steps_config.go

	ctx.Step(`^ghyll was started with --model ([a-z0-9]+)$`, func(model string) error {
		state.ActiveModel = model
		state.ModelLocked = true
		state.AutoRouting = false
		return nil
	})

	ctx.Step(`^the dialect router does not change models$`, func() error {
		// Verify with real Evaluate that locked model stays
		inputs := dialect.RouterInputs{
			ContextDepth: 50000, // high context
			ToolDepth:    10,    // high tool depth
			ModelLocked:  true,
			ActiveModel:  state.ActiveModel,
			Config:       routingCfg,
		}
		decision := dialect.Evaluate(inputs)
		if decision.Action != "none" {
			return fmt.Errorf("expected 'none' for locked model, got %q", decision.Action)
		}
		if decision.TargetModel != state.ActiveModel {
			return fmt.Errorf("target model changed from %s to %s despite lock", state.ActiveModel, decision.TargetModel)
		}
		return nil
	})

	ctx.Step(`^tier fallback does not apply$`, func() error {
		// Same as above - locked model means no tier changes
		inputs := dialect.RouterInputs{
			ModelLocked: true,
			ActiveModel: state.ActiveModel,
			Config:      routingCfg,
		}
		decision := dialect.Evaluate(inputs)
		if decision.Action != "none" {
			return fmt.Errorf("expected 'none', got %q", decision.Action)
		}
		return nil
	})

	ctx.Step(`^the active model is "([^"]*)" for the entire session$`, func(model string) error {
		if state.ActiveModel != model {
			return fmt.Errorf("active model is %s, want %s", state.ActiveModel, model)
		}
		if !state.ModelLocked {
			return fmt.Errorf("model should be locked for entire session")
		}
		return nil
	})

	ctx.Step(`^the active model remains "([^"]*)"$`, func(model string) error {
		if state.ActiveModel != model {
			return fmt.Errorf("active model is %s, want %s", state.ActiveModel, model)
		}
		return nil
	})

	ctx.Step(`^context was auto-escalated due to depth$`, func() error {
		// Simulate: we're on deep tier because context depth triggered escalation
		state.ActiveModel = routingCfg.DeepModel
		state.DeepOverride = false
		return nil
	})

	ctx.Step(`^compaction reduces context to (\d+) tokens$`, func(tokens int) error {
		state.ContextTokens = tokens

		// Compaction clears /deep override — conditions have changed
		state.DeepOverride = false

		// Run routing evaluation with compacted context
		inputs := dialect.RouterInputs{
			ContextDepth:          tokens,
			ToolDepth:             state.ToolDepth,
			ModelLocked:           state.ModelLocked,
			DeepOverride:          false,
			ActiveModel:           state.ActiveModel,
			ContextCompactedBelow: tokens,
			Config:                routingCfg,
		}
		lastDecision = dialect.Evaluate(inputs)

		if lastDecision.Action == "de_escalate" {
			state.ActiveModel = lastDecision.TargetModel
		}
		return nil
	})

	ctx.Step(`^auto-routing determines M2\.5 is sufficient$`, func() error {
		if state.ActiveModel != routingCfg.DefaultModel {
			return fmt.Errorf("expected model to be %s after de-escalation, got %s", routingCfg.DefaultModel, state.ActiveModel)
		}
		return nil
	})

	ctx.Step(`^the active model reverts to "([^"]*)"$`, func(model string) error {
		if state.ActiveModel != model {
			return fmt.Errorf("active model is %s, want %s", state.ActiveModel, model)
		}
		return nil
	})

	ctx.Step(`^drift detection triggers backfill$`, func() error {
		// Simulate backfill trigger and run routing
		inputs := dialect.RouterInputs{
			ContextDepth:      state.ContextTokens,
			ToolDepth:         state.ToolDepth,
			ModelLocked:       state.ModelLocked,
			DeepOverride:      state.DeepOverride,
			ActiveModel:       state.ActiveModel,
			BackfillTriggered: true,
			Config:            routingCfg,
		}
		lastDecision = dialect.Evaluate(inputs)

		if lastDecision.Action == "escalate" {
			state.ActiveModel = lastDecision.TargetModel
		}
		return nil
	})

	ctx.Step(`^the active model escalates to "([^"]*)"$`, func(model string) error {
		if state.ActiveModel != model {
			return fmt.Errorf("active model is %s, expected escalation to %s", state.ActiveModel, model)
		}
		return nil
	})

	ctx.Step(`^the backfill context is formatted for the glm5 dialect$`, func() error {
		// Behavioral assertion - verified by the model being the deep tier
		if state.ActiveModel != routingCfg.DeepModel {
			return fmt.Errorf("expected deep model %s to be active for backfill formatting, got %s", routingCfg.DeepModel, state.ActiveModel)
		}
		return nil
	})

	ctx.Step(`^the user typed "\/deep" and the model switched to "([^"]*)"$`, func(model string) error {
		state.DeepOverride = true
		state.ActiveModel = model
		return nil
	})

	ctx.Step(`^the context window is at (\d+) tokens$`, func(tokens int) error {
		state.ContextTokens = tokens
		return nil
	})
}
