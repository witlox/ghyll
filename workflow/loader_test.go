package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupWorkflowDir(t *testing.T) (globalDir, projectDir string) {
	t.Helper()
	globalDir = filepath.Join(t.TempDir(), ".ghyll")
	projectDir = t.TempDir()
	return globalDir, projectDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestScenario_Workflow_LoadProjectInstructions maps to:
// Scenario: Load project instructions from .ghyll/
func TestScenario_Workflow_LoadProjectInstructions(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".ghyll/instructions.md"), "Always use BDD with TDD.\nFollow conventional commits.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.ProjectInstructions, "Always use BDD with TDD") {
		t.Error("project instructions not loaded")
	}
	if wf.Source != "ghyll" {
		t.Errorf("source = %q, want 'ghyll'", wf.Source)
	}
}

// TestScenario_Workflow_LoadGlobalInstructions maps to:
// Scenario: Load global instructions from ~/.ghyll/
func TestScenario_Workflow_LoadGlobalInstructions(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(globalDir, "instructions.md"), "Be concise and direct.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.GlobalInstructions, "Be concise and direct") {
		t.Error("global instructions not loaded")
	}
}

// TestScenario_Workflow_ConcatGlobalProjectLast maps to:
// Scenario: Global and project instructions concatenated — project last
func TestScenario_Workflow_ConcatGlobalProjectLast(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(globalDir, "instructions.md"), "Use verbose logging in tests.\n")
	writeFile(t, filepath.Join(projectDir, ".ghyll/instructions.md"), "Use minimal logging in tests.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.GlobalInstructions == "" || wf.ProjectInstructions == "" {
		t.Error("both should be loaded")
	}
}

// TestScenario_Workflow_LoadRoles maps to:
// Scenario: Load and activate role
func TestScenario_Workflow_LoadRoles(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".ghyll/roles/analyst.md"), "Do not write code. Produce specs only.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content, ok := wf.Roles["analyst"]; !ok {
		t.Error("analyst role not loaded")
	} else if !strings.Contains(content, "Do not write code") {
		t.Errorf("analyst content = %q", content)
	}
}

// TestScenario_Workflow_ProjectRolesOverrideGlobal maps to:
// Scenario: Project roles override global roles
func TestScenario_Workflow_ProjectRolesOverrideGlobal(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(globalDir, "roles/reviewer.md"), "Be lenient.")
	writeFile(t, filepath.Join(projectDir, ".ghyll/roles/reviewer.md"), "Be strict.")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Roles["reviewer"] != "Be strict." {
		t.Errorf("role content = %q, want 'Be strict.'", wf.Roles["reviewer"])
	}
}

// TestScenario_Workflow_LoadCommands maps to:
// Scenario: User-defined slash command injects prompt
func TestScenario_Workflow_LoadCommands(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".ghyll/commands/review.md"), "Review this code critically.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content, ok := wf.Commands["review"]; !ok {
		t.Error("review command not loaded")
	} else if !strings.Contains(content, "Review this code critically") {
		t.Errorf("command content = %q", content)
	}
}

// TestScenario_Workflow_FallbackClaude maps to:
// Scenario: Fallback to .claude/ when .ghyll/ absent — instructions
func TestScenario_Workflow_FallbackClaude(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".claude/CLAUDE.md"), "Use diamond workflow for features.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.ProjectInstructions, "Use diamond workflow") {
		t.Error("CLAUDE.md not loaded as instructions")
	}
	if wf.Source != "claude" {
		t.Errorf("source = %q, want 'claude'", wf.Source)
	}
}

// TestScenario_Workflow_FallbackClaudeRoles maps to:
// Scenario: Fallback to .claude/ — roles loaded from roles/
func TestScenario_Workflow_FallbackClaudeRoles(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".claude/roles/analyst.md"), "Do not write code.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := wf.Roles["analyst"]; !ok {
		t.Error("analyst role not loaded from .claude/ fallback")
	}
}

// TestScenario_Workflow_FallbackClaudeCommands maps to:
// Scenario: Fallback to .claude/ — commands loaded from commands/
func TestScenario_Workflow_FallbackClaudeCommands(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".claude/commands/verify.md"), "Run the full verification checklist.\n")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := wf.Commands["verify"]; !ok {
		t.Error("verify command not loaded from .claude/ fallback")
	}
}

// TestScenario_Workflow_GhyllTakesPrecedence maps to:
// Scenario: .ghyll/ takes precedence over .claude/
func TestScenario_Workflow_GhyllTakesPrecedence(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".ghyll/instructions.md"), "Use ghyll workflow.")
	writeFile(t, filepath.Join(projectDir, ".claude/CLAUDE.md"), "Use claude workflow.")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.ProjectInstructions, "Use ghyll workflow") {
		t.Error("ghyll should take precedence")
	}
	if strings.Contains(wf.ProjectInstructions, "Use claude workflow") {
		t.Error("claude content should not be loaded when .ghyll/ exists")
	}
}

// TestScenario_Workflow_NoWorkflowFolder maps to:
// Scenario: No workflow folder — session starts with bare prompt
func TestScenario_Workflow_NoWorkflowFolder(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Source != "none" {
		t.Errorf("source = %q, want 'none'", wf.Source)
	}
	if wf.ProjectInstructions != "" {
		t.Error("expected empty instructions")
	}
}

// TestScenario_Workflow_EmptyInstructions maps to:
// Scenario: Empty instructions file treated as no instructions
func TestScenario_Workflow_EmptyInstructions(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".ghyll/instructions.md"), "")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.ProjectInstructions != "" {
		t.Error("empty file should result in empty instructions")
	}
}

// TestScenario_Workflow_GlobalAndProjectCommandsMerged maps to:
// Scenario: Global and project commands merged
func TestScenario_Workflow_GlobalAndProjectCommandsMerged(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(globalDir, "commands/lint.md"), "Run the linter.")
	writeFile(t, filepath.Join(projectDir, ".ghyll/commands/review.md"), "Review the code.")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := wf.Commands["lint"]; !ok {
		t.Error("global command 'lint' not loaded")
	}
	if _, ok := wf.Commands["review"]; !ok {
		t.Error("project command 'review' not loaded")
	}
}

// TestScenario_Workflow_ProjectCommandOverridesGlobal maps to:
// Scenario: Project command overrides global command with same name
func TestScenario_Workflow_ProjectCommandOverridesGlobal(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(globalDir, "commands/check.md"), "Run basic checks.")
	writeFile(t, filepath.Join(projectDir, ".ghyll/commands/check.md"), "Run full verification.")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wf.Commands["check"] != "Run full verification." {
		t.Errorf("command = %q, want project version", wf.Commands["check"])
	}
}

// TestScenario_Workflow_FallbackInstructionsMdOverCLAUDE maps to:
// Scenario: Fallback .claude/ with instructions.md takes precedence over CLAUDE.md
func TestScenario_Workflow_FallbackInstructionsMdOverCLAUDE(t *testing.T) {
	globalDir, projectDir := setupWorkflowDir(t)
	writeFile(t, filepath.Join(projectDir, ".claude/instructions.md"), "From instructions.md")
	writeFile(t, filepath.Join(projectDir, ".claude/CLAUDE.md"), "From CLAUDE.md")

	wf, err := Load(globalDir, projectDir, []string{".claude"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(wf.ProjectInstructions, "From instructions.md") {
		t.Error("instructions.md should take precedence over CLAUDE.md")
	}
}
