package main

import (
	"testing"
)

// TestScenario_SubAgent_ExcludedTools maps to:
// Scenario: Sub-agent cannot spawn sub-agents
// Scenario: Sub-agent tool set excludes plan mode tools
func TestScenario_SubAgent_ExcludedTools(t *testing.T) {
	excluded := []string{"agent", "enter_plan_mode", "exit_plan_mode"}
	for _, tool := range excluded {
		if !excludedSubAgentTools[tool] {
			t.Errorf("tool %q should be excluded from sub-agents", tool)
		}
	}

	// Verify normal tools are NOT excluded
	allowed := []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob", "git", "web_fetch", "web_search"}
	for _, tool := range allowed {
		if excludedSubAgentTools[tool] {
			t.Errorf("tool %q should be available to sub-agents", tool)
		}
	}
}

// TestScenario_SubAgent_ToolCount maps to:
// Scenario: Sub-agent has access to new tools
func TestScenario_SubAgent_ToolCount(t *testing.T) {
	// Per architecture: sub-agents get 9 tools (12 minus 3 excluded)
	if len(excludedSubAgentTools) != 3 {
		t.Errorf("expected 3 excluded tools, got %d", len(excludedSubAgentTools))
	}
}
