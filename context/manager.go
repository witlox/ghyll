package context

import (
	"sync"

	"github.com/witlox/ghyll/types"
)

// ManagerConfig holds configuration for the context manager.
type ManagerConfig struct {
	MaxContext       int     // max tokens for the active model
	PreserveTurns    int     // number of recent turns to preserve during compaction
	CompactThreshold float64 // fraction of max (e.g., 0.9 = 90%)
}

// CheckpointRequest is the input for creating a new checkpoint.
type CheckpointRequest struct {
	SessionID    string
	Turn         int
	ActiveModel  string
	Summary      string
	Messages     []types.Message
	FilesTouched []string
	ToolsUsed    []string
	InjectionSig []string
	Reason       string // "interval", "compaction", "handoff", "shutdown"
}

// CompactionRequest is passed to the compaction callback.
// Invariant 24a: contains only turns to summarize, not the full context.
type CompactionRequest struct {
	TurnsToSummarize []types.Message
	CompactionPrompt string
	ModelEndpoint    string
}

// ManagerDeps are callbacks provided by cmd/ghyll to wire packages
// that cannot import each other.
type ManagerDeps struct {
	TokenCount       func([]types.Message) int
	CompactionCall   func(CompactionRequest) (string, error)
	CreateCheckpoint func(CheckpointRequest) error
	Embed            func([]types.Message) ([]float32, error)
}

// PreTurnResult reports what happened during the pre-turn check.
type PreTurnResult struct {
	CompactionTriggered bool
	TokenCount          int
	Error               error
}

// Manager owns the context window. Invariant 5: single owner.
type Manager struct {
	mu       sync.Mutex
	messages []types.Message
	config   ManagerConfig
	deps     ManagerDeps
	turn     int
}

// NewManager creates a context manager.
func NewManager(config ManagerConfig, deps ManagerDeps) *Manager {
	return &Manager{
		config: config,
		deps:   deps,
	}
}

// Messages returns a copy of the current context window.
func (m *Manager) Messages() []types.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]types.Message, len(m.messages))
	copy(cp, m.messages)
	return cp
}

// AddMessage appends a message to the context window.
func (m *Manager) AddMessage(msg types.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	if msg.Role == "user" {
		m.turn++
	}
}

// Turn returns the current turn number.
func (m *Manager) Turn() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turn
}

// PreTurnCheck runs before each turn. Triggers compaction if needed.
// Invariant 6: token budget respected.
// Invariant 21: proactive before reactive.
func (m *Manager) PreTurnCheck(activeModel string, endpoint string, compactionPrompt string) PreTurnResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deps.TokenCount == nil {
		return PreTurnResult{}
	}

	tokens := m.deps.TokenCount(m.messages)
	threshold := int(float64(m.config.MaxContext) * m.config.CompactThreshold)

	if tokens <= threshold {
		return PreTurnResult{TokenCount: tokens}
	}

	// Trigger compaction
	err := m.compact(activeModel, endpoint, compactionPrompt)
	newTokens := m.deps.TokenCount(m.messages)

	return PreTurnResult{
		CompactionTriggered: true,
		TokenCount:          newTokens,
		Error:               err,
	}
}

// ReactiveCompact triggers compaction in response to a context_length_exceeded error.
// Invariant 23: reactive retry is once (caller enforces the retry limit).
func (m *Manager) ReactiveCompact(activeModel string, endpoint string, compactionPrompt string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.compact(activeModel, endpoint, compactionPrompt)
}

// compact runs the compaction flow. Must be called with mu held.
func (m *Manager) compact(activeModel string, endpoint string, compactionPrompt string) error {
	if m.deps.CompactionCall == nil {
		return nil
	}

	preserve := m.config.PreserveTurns
	if preserve > len(m.messages) {
		preserve = len(m.messages)
	}

	toSummarize := m.messages[:len(m.messages)-preserve]
	preserved := m.messages[len(m.messages)-preserve:]

	// Call compaction via callback (invariant 24a: separate API call)
	summary, err := m.deps.CompactionCall(CompactionRequest{
		TurnsToSummarize: toSummarize,
		CompactionPrompt: compactionPrompt,
		ModelEndpoint:    endpoint,
	})
	if err != nil {
		return err
	}

	// Create checkpoint with the actual compaction summary (invariant 22).
	// Uses the model's summary so drift detection can find it later.
	if m.deps.CreateCheckpoint != nil {
		_ = m.deps.CreateCheckpoint(CheckpointRequest{
			Turn:        m.turn,
			ActiveModel: activeModel,
			Summary:     summary,
			Messages:    m.messages,
			Reason:      "compaction",
		})
	}

	// Replace old turns with summary + preserved turns
	m.messages = make([]types.Message, 0, 1+preserve)
	m.messages = append(m.messages, types.Message{
		Role:    "system",
		Content: summary,
	})
	m.messages = append(m.messages, preserved...)

	return nil
}

// ApplyBackfill injects checkpoint summaries into the context.
// Invariant 8: additive — prepend only, no remove.
func (m *Manager) ApplyBackfill(summaries []types.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(summaries, m.messages...)
}
