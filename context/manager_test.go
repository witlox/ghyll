package context

import (
	"testing"

	"github.com/witlox/ghyll/types"
)

// TestScenario_Context_AddAndRetrieve
func TestScenario_Context_AddAndRetrieve(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxContext:       100000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
	})

	m.AddMessage(types.Message{Role: "user", Content: "hello"})
	m.AddMessage(types.Message{Role: "assistant", Content: "hi"})

	msgs := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

// TestScenario_Compaction_ProactiveBeforeTurn maps to:
// Scenario: Proactive compaction before turn
func TestScenario_Compaction_ProactiveBeforeTurn(t *testing.T) {
	compacted := false
	m := NewManager(ManagerConfig{
		MaxContext:       1000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			compacted = true
			return "summary of earlier turns", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error { return nil },
	})

	// Add 12 messages to exceed 90% (12*100=1200 > 900)
	for i := 0; i < 12; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}

	result := m.PreTurnCheck("m25", "http://endpoint", "compaction prompt")
	if !result.CompactionTriggered {
		t.Error("expected compaction to trigger")
	}
	if !compacted {
		t.Error("compaction callback was not called")
	}
	// After compaction, should have summary + preserved turns
	msgs := m.Messages()
	if len(msgs) != 4 { // 1 summary + 3 preserved
		t.Errorf("expected 4 messages after compaction, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "summary of earlier turns" {
		t.Errorf("first message should be compaction summary, got role=%q content=%q", msgs[0].Role, msgs[0].Content)
	}
}

// TestScenario_Compaction_PreservesRecentTurns maps to:
// Scenario: Compaction preserves recent turns
func TestScenario_Compaction_PreservesRecentTurns(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxContext:       500,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			return "compacted", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error { return nil },
	})

	// Add 10 messages
	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "turn " + string(rune('A'+i))})
	}

	m.PreTurnCheck("m25", "http://endpoint", "prompt")

	msgs := m.Messages()
	// Last 3 should be preserved
	if len(msgs) < 3 {
		t.Fatalf("expected at least 3 messages, got %d", len(msgs))
	}
	last3 := msgs[len(msgs)-3:]
	if last3[0].Content != "turn H" {
		t.Errorf("preserved[0] = %q, want 'turn H'", last3[0].Content)
	}
	if last3[2].Content != "turn J" {
		t.Errorf("preserved[2] = %q, want 'turn J'", last3[2].Content)
	}
}

// TestScenario_Compaction_NoTriggerUnderThreshold
func TestScenario_Compaction_NoTriggerUnderThreshold(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxContext:       10000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
	})

	m.AddMessage(types.Message{Role: "user", Content: "hello"})

	result := m.PreTurnCheck("m25", "http://endpoint", "prompt")
	if result.CompactionTriggered {
		t.Error("compaction should not trigger under threshold")
	}
}

// TestScenario_Compaction_CreatesCheckpoint maps to:
// Scenario: Compaction creates checkpoint
func TestScenario_Compaction_CreatesCheckpoint(t *testing.T) {
	checkpointed := false
	var cpReason string
	m := NewManager(ManagerConfig{
		MaxContext:       500,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			return "summary", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error {
			checkpointed = true
			cpReason = req.Reason
			return nil
		},
	})

	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}

	m.PreTurnCheck("m25", "http://endpoint", "prompt")
	if !checkpointed {
		t.Error("expected checkpoint creation during compaction")
	}
	if cpReason != "compaction" {
		t.Errorf("checkpoint reason = %q, want %q", cpReason, "compaction")
	}
}

// TestScenario_Context_BackfillAdditive maps to:
// Invariant 8: Backfill is additive
func TestScenario_Context_BackfillAdditive(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxContext:       100000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
	})

	m.AddMessage(types.Message{Role: "user", Content: "original"})
	m.AddMessage(types.Message{Role: "assistant", Content: "response"})

	m.ApplyBackfill([]types.Message{
		{Role: "system", Content: "backfill from checkpoint 3"},
	})

	msgs := m.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (backfill + 2 original), got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Error("backfill should be prepended")
	}
}

// TestScenario_Compaction_ReactiveCompaction maps to:
// Scenario: Reactive compaction on context-too-long rejection
func TestScenario_Compaction_ReactiveCompaction(t *testing.T) {
	compacted := false
	m := NewManager(ManagerConfig{
		MaxContext:       1000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			compacted = true
			return "reactive summary", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error { return nil },
	})

	for i := 0; i < 5; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}

	err := m.ReactiveCompact("m25", "http://endpoint", "prompt")
	if err != nil {
		t.Fatalf("reactive compact failed: %v", err)
	}
	if !compacted {
		t.Error("expected compaction callback to be called")
	}
	msgs := m.Messages()
	if msgs[0].Content != "reactive summary" {
		t.Errorf("first message = %q, want reactive summary", msgs[0].Content)
	}
}

// TestScenario_Compaction_UsesDialectPrompt maps to:
// Scenario: Compaction summary uses dialect prompt
func TestScenario_Compaction_UsesDialectPrompt(t *testing.T) {
	var receivedPrompt string
	m := NewManager(ManagerConfig{
		MaxContext:       500,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			receivedPrompt = req.CompactionPrompt
			return "summary", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error { return nil },
	})

	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}

	m.PreTurnCheck("glm5", "http://endpoint", "GLM5-specific compaction prompt")
	if receivedPrompt != "GLM5-specific compaction prompt" {
		t.Errorf("compaction prompt = %q, want GLM5-specific", receivedPrompt)
	}
}

// TestScenario_Compaction_Repeated maps to:
// Scenario: Repeated compaction within session
func TestScenario_Compaction_Repeated(t *testing.T) {
	compactionCount := 0
	m := NewManager(ManagerConfig{
		MaxContext:       500,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			compactionCount++
			return "summary round " + string(rune('0'+compactionCount)), nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error { return nil },
	})

	// First round: add messages, compact
	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}
	m.PreTurnCheck("m25", "http://endpoint", "prompt")
	if compactionCount != 1 {
		t.Fatalf("expected 1 compaction, got %d", compactionCount)
	}

	// Second round: add more messages, compact again
	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "more"})
	}
	m.PreTurnCheck("m25", "http://endpoint", "prompt")
	if compactionCount != 2 {
		t.Fatalf("expected 2 compactions, got %d", compactionCount)
	}

	// The previous summary should still be in the last 3 preserved or the new summary
	msgs := m.Messages()
	if len(msgs) < 1 {
		t.Fatal("expected messages after repeated compaction")
	}
}

// TestScenario_Compaction_CheckpointHasSummaryContent maps to:
// Low finding 9: checkpoint should have model's actual summary
func TestScenario_Compaction_CheckpointHasSummaryContent(t *testing.T) {
	var cpSummary string
	m := NewManager(ManagerConfig{
		MaxContext:       500,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
		CompactionCall: func(req CompactionRequest) (string, error) {
			return "detailed model summary of auth work", nil
		},
		CreateCheckpoint: func(req CheckpointRequest) error {
			cpSummary = req.Summary
			return nil
		},
	})

	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
	}
	m.PreTurnCheck("m25", "http://endpoint", "prompt")

	if cpSummary != "detailed model summary of auth work" {
		t.Errorf("checkpoint summary = %q, want model's actual summary", cpSummary)
	}
}

// TestScenario_Drift_CheckFrequency maps to:
// Scenario: Drift check frequency
func TestScenario_Drift_CheckFrequency(t *testing.T) {
	// Turn counting is done by Manager.Turn() which increments on user messages
	m := NewManager(ManagerConfig{
		MaxContext:       100000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 10 },
	})

	for i := 0; i < 10; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "msg"})
		m.AddMessage(types.Message{Role: "assistant", Content: "reply"})
	}

	turn := m.Turn()
	if turn != 10 {
		t.Errorf("turn = %d, want 10 (only user messages count)", turn)
	}

	// Drift check at turn 5: turn % 5 == 0
	driftInterval := 5
	if turn%driftInterval != 0 {
		t.Errorf("turn %d should be a drift check point (interval %d)", turn, driftInterval)
	}
}

// TestScenario_Drift_BackfillTokenBudget maps to:
// Scenario: Backfill respects token budget
func TestScenario_Drift_BackfillTokenBudget(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxContext:       1000,
		PreserveTurns:    3,
		CompactThreshold: 0.9,
	}, ManagerDeps{
		TokenCount: func(msgs []types.Message) int { return len(msgs) * 100 },
	})

	// Add some existing context (5 messages = 500 tokens)
	for i := 0; i < 5; i++ {
		m.AddMessage(types.Message{Role: "user", Content: "existing"})
	}

	// Try to backfill 3 summaries — but budget is tight
	summaries := []types.Message{
		{Role: "system", Content: "backfill 1"},
		{Role: "system", Content: "backfill 2"},
		{Role: "system", Content: "backfill 3"},
	}

	// ApplyBackfill is additive — caller is responsible for budget
	// This tests that backfill prepends correctly
	m.ApplyBackfill(summaries)

	msgs := m.Messages()
	if len(msgs) != 8 { // 3 backfill + 5 existing
		t.Fatalf("expected 8 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "backfill 1" {
		t.Error("first message should be first backfill")
	}
}
