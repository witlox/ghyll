// Package workflow loads project instructions, roles, and commands
// from .ghyll/ (or fallback .claude/) directories.
// Invariant 47: global prepended, project appended (project has last word).
// Invariant 51: fallback to .claude/ with CLAUDE.md → instructions mapping.
package workflow

import (
	"os"
	"path/filepath"
	"strings"
)

// Workflow is the loaded result of scanning .ghyll/ (or fallback .claude/).
// Budget enforcement is done by cmd/ghyll using dialect.TokenCount.
//
// ADR-008: runtime free-form workflow roles are not loaded; the four
// diamond roles (analyst / architect / implementer / integrator) are
// contracts defined in specs/architecture/roles/ and not swappable
// at runtime.
type Workflow struct {
	GlobalInstructions  string            // from ~/.ghyll/instructions.md (raw)
	ProjectInstructions string            // from <repo>/.ghyll/instructions.md or CLAUDE.md (raw)
	Commands            map[string]string // command name → content
	Source              string            // "ghyll", "claude", "none"
}

// Load reads workflow files from global and project directories.
// globalDir is typically ~/.ghyll/. projectDir is the repo root.
// fallbackFolders lists alternative folder names to try (e.g., [".claude"]).
//
// Load reads project instructions and slash commands only. The
// roles/ subdirectory under .ghyll or .claude is intentionally
// not scanned — those files belong to Claude Code's own build
// agents, not ghyll's runtime roles (see ADR-008).
func Load(globalDir, projectDir string, fallbackFolders []string) (*Workflow, error) {
	wf := &Workflow{
		Commands: make(map[string]string),
		Source:   "none",
	}

	// Load global instructions + commands
	wf.GlobalInstructions = readFileIfExists(filepath.Join(globalDir, "instructions.md"))
	loadDir(filepath.Join(globalDir, "commands"), wf.Commands)

	// Try project .ghyll/ first
	ghyllDir := filepath.Join(projectDir, ".ghyll")
	if dirExists(ghyllDir) {
		wf.Source = "ghyll"
		wf.ProjectInstructions = readFileIfExists(filepath.Join(ghyllDir, "instructions.md"))
		loadDirOverride(filepath.Join(ghyllDir, "commands"), wf.Commands)
		return wf, nil
	}

	// Try fallback folders
	for _, folder := range fallbackFolders {
		fallbackDir := filepath.Join(projectDir, folder)
		if !dirExists(fallbackDir) {
			continue
		}

		wf.Source = strings.TrimPrefix(folder, ".")

		// Load instructions: instructions.md takes precedence over CLAUDE.md
		instructions := readFileIfExists(filepath.Join(fallbackDir, "instructions.md"))
		if instructions == "" {
			instructions = readFileIfExists(filepath.Join(fallbackDir, "CLAUDE.md"))
		}
		wf.ProjectInstructions = instructions

		loadDirOverride(filepath.Join(fallbackDir, "commands"), wf.Commands)
		return wf, nil
	}

	return wf, nil
}

func readFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// loadDir loads all .md files from a directory into the map.
func loadDir(dir string, target map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		content := readFileIfExists(filepath.Join(dir, e.Name()))
		if content != "" {
			target[name] = content
		}
	}
}

// loadDirOverride loads .md files, overriding existing entries (project > global).
func loadDirOverride(dir string, target map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		content := readFileIfExists(filepath.Join(dir, e.Name()))
		if content != "" {
			target[name] = content // override global
		}
	}
}
