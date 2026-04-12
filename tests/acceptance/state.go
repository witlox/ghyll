package acceptance

// ScenarioState holds shared state across steps within a single scenario.
// Reset between scenarios by godog's scenario lifecycle.
type ScenarioState struct {
	// Config
	ConfigPath string
	ConfigErr  error

	// Routing
	ActiveModel  string
	ModelLocked  bool
	DeepOverride bool
	AutoRouting  bool
	ToolDepth    int

	// Context
	ContextTokens  int
	MaxContext     int
	Messages       int
	TurnsPreserved int

	// Stream
	StreamError    error
	RetryCount     int
	FallbackModel  string
	PartialContent string

	// Memory
	Checkpoints    []string
	LastCheckpoint string
	ChainValid     bool

	// Drift
	Similarity float64
	Threshold  float64
	Drifted    bool

	// Terminal output (for display assertions)
	TerminalOutput []string
}

// AddTerminal records a terminal output message for assertion in steps.
func (s *ScenarioState) AddTerminal(msg string) {
	s.TerminalOutput = append(s.TerminalOutput, msg)
}
