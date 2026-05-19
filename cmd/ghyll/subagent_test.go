package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/witlox/ghyll/config"
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

// newSubAgentTestSession returns a *Session for sub-agent testing.
// Uses an httptest server that returns a no-op response; RunSubAgent's
// flow can be exercised without standing up a real model endpoint.
func newSubAgentTestSession(t *testing.T, subModel string) *Session {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	cfg.SubAgent = config.SubAgentConfig{
		DefaultModel:   subModel,
		MaxTurns:       1,
		TokenBudget:    10000,
		TimeoutSeconds: 5,
	}
	sess, err := NewSession(SessionConfig{
		Cfg:           cfg,
		Workdir:       t.TempDir(),
		SessionID:     "subagent-test",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess
}

// TestSubAgent_UnknownModelReturnsError verifies RunSubAgent surfaces
// a clear error when SubAgent.DefaultModel is not in cfg.Models.
func TestSubAgent_UnknownModelReturnsError(t *testing.T) {
	sess := newSubAgentTestSession(t, "nonexistent-model")
	res := RunSubAgent(sess, "do something")
	if res.Error == "" {
		t.Fatal("expected error for unknown sub-agent model")
	}
	if !strings.Contains(res.Error, "not configured") {
		t.Errorf("error %q does not mention 'not configured'", res.Error)
	}
}

// TestSubAgent_UnknownDialectFamily verifies the dialect normalization
// catches a sub-agent whose model has an unknown dialect string.
func TestSubAgent_UnknownDialectFamily(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	// Inject a model with an unknown dialect that config.validate would
	// normally reject; for the test we set DisableEngine and skip the
	// config-validate step by directly constructing.
	cfg.Models["weird"] = config.ModelConfig{
		Endpoint:   cfg.Models["m25"].Endpoint,
		Dialect:    "minimax", // valid for parent
		MaxContext: 1000,
	}
	cfg.SubAgent.DefaultModel = "weird"
	cfg.SubAgent.MaxTurns = 1
	sess, err := NewSession(SessionConfig{
		Cfg: cfg, Workdir: t.TempDir(), SessionID: "weird",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the model's dialect AFTER session creation to
	// simulate a runtime mismatch.
	cfg.Models["weird"] = config.ModelConfig{
		Endpoint:   cfg.Models["weird"].Endpoint,
		Dialect:    "bogus-dialect-family",
		MaxContext: 1000,
	}
	sess.cfg = cfg
	res := RunSubAgent(sess, "irrelevant task")
	if res.Error == "" {
		t.Fatal("expected error for unknown dialect family")
	}
	// Accept either the normalize-error or the family-unsupported
	// branch (both reachable depending on the prefix match).
	if !strings.Contains(res.Error, "dialect") && !strings.Contains(res.Error, "unsupported") {
		t.Errorf("error %q does not mention dialect/unsupported", res.Error)
	}
}

// TestSubAgent_DefaultsToFastTier verifies that SubAgent.DefaultModel
// is empty causes resolution to fall through to cfg.Routing.DefaultModel.
func TestSubAgent_DefaultsToFastTier(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(server.Close)
	cfg := testConfig(server.URL)
	cfg.SubAgent = config.SubAgentConfig{
		DefaultModel:   "", // fall through
		MaxTurns:       1,
		TimeoutSeconds: 1,
	}
	sess, err := NewSession(SessionConfig{
		Cfg: cfg, Workdir: t.TempDir(), SessionID: "default",
		DisableEngine: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Use the resolver path inside RunSubAgent — when SubAgent.DefaultModel
	// is empty, the code falls back to cfg.Routing.DefaultModel ("m25").
	// We can't directly assert which model was used without exposing it,
	// but we CAN assert that RunSubAgent doesn't error on the
	// "model not configured" path (the lookup succeeded).
	res := RunSubAgent(sess, "task")
	// The httptest server returns 200 with empty body, which the stream
	// client will treat as an empty SSE — that should produce a model-
	// unreachable error, not a "not configured" error.
	if strings.Contains(res.Error, "not configured") {
		t.Errorf("default-fallback resolution failed: %q", res.Error)
	}
}
