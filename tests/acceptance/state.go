package acceptance

import (
	"time"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/catalogue"
	"github.com/witlox/ghyll/runner"
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

	// Auto-propose loop state (ADR-011 §B.2 + init.feature 89..121).
	Proposal         *bootstrap.ArrowProposal
	ProposalApplyErr error
	// AllProposals captures the full diamond expansion for the
	// "per (role-pair, context) arrow proposal" scenario.
	AllProposals        []*bootstrap.ArrowProposal
	BoundedContextCount int

	// Project profile (init sub-phase A — greenfield/brownfield).
	ProjectTestDir string
	Profile        *bootstrap.ProjectProfile
	ProfileErr     error

	// Refusal flow (init.feature 25, 66, 77).
	PendingRisk   bootstrap.RiskAssessment
	RiskEvaluated bool

	// Re-init on missing binding (init.feature 49, 59).
	BindingGrid           *bootstrap.Grid
	RequiredBindings      []bootstrap.BindingKey
	PendingClauseConcept  string
	PendingClauseLanguage string
	MissingBindingErr     error

	// End-to-end init driver (init.feature 129, 138, 146).
	DriverGrid     *bootstrap.Grid
	DriverWriteErr error

	// Session registry (init.feature 198, 205, 212).
	SessionRegistry  *bootstrap.SessionRegistry
	BobDeclareErr    error
	UnknownClauseErr error

	// Modify-non-monotonic outline (init.feature 184).
	ModifyFixtureArg  string
	ModifyFixtureOrig map[string]any
	ModifyFixtureProp map[string]any
	ModifyFixtureCat  *catalogue.Catalogue
	ModifyFixtureErr  error

	// Orphan-symbol extraction (init.feature 41).
	ExtractedSymbols []bootstrap.ExportedSymbol
	ExtractedOrphans []bootstrap.OrphanCandidate

	// Runner (specs/v2/features/runner.feature).
	RunnerClauses           []runner.ClauseDeriveInput
	RunnerFindings          []runner.Finding
	RunnerSeverityThresh    int
	RunnerArrowStatus       runner.ArrowStatus
	RunnerBlockingCount     int
	RunnerUpstreamStatus    runner.ArrowStatus
	RunnerUpstreamArrowID   string
	RunnerDownstreamArrowID string
	RunnerInvalidatingGV    int
	RunnerTransitionErr     error

	// Grid filesystem state (ADR-010 — versioned grid files + grid.current pointer).
	GridTestDir string
	GridReadErr error
}

// AddTerminal records a terminal output message for assertion in steps.
func (s *ScenarioState) AddTerminal(msg string) {
	s.TerminalOutput = append(s.TerminalOutput, msg)
}

// buildClauseArgs returns a default arg map for a catalogue concept,
// populating both defaulted args AND required-no-default args with
// synthetic placeholder values. Used by step files that build
// proposed clauses; validation-pass-2 F29 now enforces required-arg
// completeness at Apply time.
func buildClauseArgs(c catalogue.Concept) map[string]any {
	out := map[string]any{}
	for name, schema := range c.Arguments {
		if schema.Default != nil {
			out[name] = schema.Default
			continue
		}
		if schema.Required {
			out[name] = syntheticBDDArgValue(schema.Type)
		}
	}
	return out
}

// syntheticBDDArgValue mirrors bootstrap.syntheticArgValue (which is
// test-only in that package); duplicated here so step files don't
// reach into bootstrap's unexported helpers.
func syntheticBDDArgValue(argType string) any {
	switch argType {
	case "path-glob":
		return "src/**"
	case "regex":
		return "^test"
	case "language-id":
		return "go"
	case "severity":
		return "medium"
	case "boolean":
		return false
	case "int", "depth-tier", "int-or-range":
		return 0
	case "number":
		return 0.5
	case "list":
		return []any{}
	}
	return "test-value"
}
