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

	// PreSwitchCheckpoint is set by EITHER memory-scope (lastCP
	// observation) OR routing-scope (lastDecision was an
	// escalation/de-escalation) signaling that the "checkpoint
	// before model switch" precondition is satisfied. Lifted to
	// ScenarioState so a single canonical step impl can verify
	// the shared signal (resolves the cross-file step-regex
	// ambiguity that previously blocked Strict=true).
	PreSwitchCheckpoint bool

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

	// Runner (specs/features/runner.feature).
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

	// State-machine step state (specs/features/state-machine.feature).
	SMClauseStatus          runner.ClauseStatus   // current clause status under test
	SMClauseStatusName      string                // wire-form name as the feature posed it
	SMClauseRecordedAt      time.Time             // timestamp captured at transition
	SMTransitionError       error                 // last attempted transition's error
	SMRunner                *runner.Runner        // for depth-below-required tests
	SMRunnerRegistry        *runner.Registry      // shared registry across the scenario
	SMRunnerEvalRun         *runner.EvaluationRun // result of the last Evaluate
	SMArrowClauses          []runner.ClauseDeriveInput
	SMArrowFindings         []runner.Finding
	SMDerivedStatus         runner.ArrowStatus
	SMArrowBlockingClauses  int
	SMArrowBlockingFindings int
	SMFindingsStore         *runner.FindingsStore // freshly constructed per scenario
	SMFindingID             string                // current finding under test
	SMFindingError          error                 // last finding-transition error

	// Amendment step state (specs/features/amendment.feature).
	AmendQueue          *runner.AmendmentQueue
	AmendObservedEvents []runner.AmendmentEvent
	AmendLastErr        error
	AmendDrained        []runner.AmendmentRequest
	AmendGridDir        string // tmpdir for grid.Write tests
	AmendGridVersion    int    // last grid version written

	// State-machine grid-missing scenario.
	SMMissingGridDir     string
	SMMissingGridVersion int
	SMMissingGridErr     error

	// Attestation step state (specs/features/attestation.feature).
	AttRegistry        *bootstrap.SessionRegistry
	AttSession         *bootstrap.Session
	AttSessionErr      error
	AttOpIDAttempt     string
	AttFindings        *runner.FindingsStore
	AttFindingID       string
	AttOperatorErr     error
	AttOperatorPayload string // JSON marshal output (separate from AttOpIDAttempt)

	// IB tracker + bus for the insufficient-basis-rounds-max
	// scenarios. Constructed lazily by the relevant step bindings.
	IBBus              *runner.OperatorBus
	IBTracker          *runner.InsufficientBasisTracker
	IBEscalationEvents []runner.OperatorEvent

	// Adversarial step state (specs/features/adversarial.feature).
	AdvAdversary       *runner.Adversary
	AdvFindings        *runner.FindingsStore
	AdvClassifications *runner.ClassificationsStore
	AdvRunner          *runner.Runner
	AdvRegistry        *runner.Registry
	AdvAttack          runner.AdversaryAttack
	AdvReport          *runner.AttackReport
	AdvAttackErr       error
	AdvOpenSweepFn     runner.OpenSweepFn
	AdvClassifyFn      runner.DepthClassifyFn
	AdvTmpProjectDir   string // freshly-created per scenario; guaranteed empty (no TODO leakage)

	// Subprocess evaluator step state (runner.feature subprocess
	// scenarios).
	SubprocResult  *runner.Result
	SubprocErr     error
	SubprocCommand string
	SubprocTimeout time.Duration
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
