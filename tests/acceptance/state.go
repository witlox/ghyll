package acceptance

import (
	"time"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/types"
)

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
	Checkpoints     []string
	LastCheckpoint  string
	ChainValid      bool
	PendingVerifyCP interface{} // *memory.Checkpoint for cross-step-file sharing

	// Drift
	Similarity float64
	Threshold  float64
	Drifted    bool

	// Keys
	KeysDir  string
	DeviceID string

	// Sync
	SyncRepoDir    string
	SyncRemoteDir  string
	SyncBranchName string

	// Stream
	StreamEndpoint string
	StreamDialect  string

	// Compaction
	CompactionTriggered bool
	CompactionSummary   string

	// Terminal output (for display assertions)
	TerminalOutput []string

	// Tool testing (shared across edit/glob/web step files)
	ToolResult  types.ToolResult
	TmpDir      string
	GlobalDir   string // ~/.ghyll/ equivalent for tests
	ToolTimeout time.Duration

	// Initialization / operator session (gates.md §2; bootstrap pkg).
	PendingOpID        string
	OperatorSession    *bootstrap.Session
	OperatorSessionErr error

	// Operator-modify path (init auto-propose, ADR-011 D20).
	ProposedConcept string
	ProposedArgs    map[string]any
	ModifyArgs      map[string]any
	ModifyErr       error

	// Grid filesystem state (ADR-010 — versioned grid files + grid.current pointer).
	GridTestDir string
	GridReadErr error
}

// AddTerminal records a terminal output message for assertion in steps.
func (s *ScenarioState) AddTerminal(msg string) {
	s.TerminalOutput = append(s.TerminalOutput, msg)
}
