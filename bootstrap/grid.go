package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Grid is the on-disk representation of a project's arrow grid at a
// specific version, per ADR-010 / D38.
//
// Grids are immutable after write: each amendment writes a NEW
// grid.v<N+1>.yaml; old files persist on disk for audit. The
// active version is named by the grid.current pointer file.
type Grid struct {
	GridVersion    int    `yaml:"grid-version"`
	CreatedAt      string `yaml:"created-at"`
	CreatedByOpID  string `yaml:"created-by-op-id"`

	BoundedContexts             []BoundedContext  `yaml:"bounded-contexts"`
	LanguageBindings            map[string]string `yaml:"language-bindings,omitempty"`
	DepthLadder                 []DepthLadderTier `yaml:"depth-ladder"`
	SeverityThreshold           string            `yaml:"severity-threshold"`
	InsufficientBasisRoundsMax  int               `yaml:"insufficient-basis-rounds-max"`
	RemediationRoundsMax        int               `yaml:"remediation-rounds-max"`

	// Arrows and Residue use untyped shapes for v1; concrete types
	// will replace these as the runner / amendment components land.
	Arrows  []map[string]any `yaml:"arrows,omitempty"`
	Residue []map[string]any `yaml:"residue,omitempty"`
}

// BoundedContext is one declared context in the project (per D4).
type BoundedContext struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description,omitempty"`
}

// DepthLadderTier is one tier of the 4-tier depth ladder per ADR-011 / D26.
type DepthLadderTier struct {
	Tier  int    `yaml:"tier"`
	Label string `yaml:"label"`
}

// Filenames under .ghyll/. Constants are package-public so callers
// (init flow, amendment component, the runner's grid loader) all
// agree on the same paths.
const (
	// CurrentPointerFile is the one-line pointer naming the active grid version.
	CurrentPointerFile = "grid.current"

	// GridFilePrefix prefixes versioned grid files (grid.v1.yaml, grid.v2.yaml, ...).
	GridFilePrefix = "grid.v"
	GridFileSuffix = ".yaml"
)

// Sentinel errors callers can check via errors.Is.
var (
	ErrGridCurrentAbsent           = errors.New("grid-current-absent")
	ErrGridCurrentMalformed        = errors.New("grid-current-malformed")
	ErrGridCurrentPointsToMissing  = errors.New("grid-current-points-to-missing-version")
)

// DefaultDepthLadder returns the harness-shipped default depth ladder
// per ADR-011 §depth ladder. Project init may override the labels at
// auth time but the tier count is fixed at 4.
func DefaultDepthLadder() []DepthLadderTier {
	return []DepthLadderTier{
		{Tier: 0, Label: "NONE"},
		{Tier: 1, Label: "SHALLOW"},
		{Tier: 2, Label: "MOCKED"},
		{Tier: 3, Label: "REALISTIC"},
	}
}

// NewGrid returns a minimally-populated Grid with the schema's harness
// defaults: depth ladder, severity threshold = "medium",
// insufficient-basis-rounds-max = 3, remediation-rounds-max = 5
// (D15, D25, D30 from operator-decisions rounds 3 and 5).
//
// The caller is responsible for setting GridVersion, CreatedByOpID,
// BoundedContexts, and any additional content before writing.
// CreatedAt is set to time.Now in RFC3339 UTC.
func NewGrid(opID string) *Grid {
	return &Grid{
		GridVersion:                1,
		CreatedAt:                  time.Now().UTC().Format(time.RFC3339),
		CreatedByOpID:              opID,
		DepthLadder:                DefaultDepthLadder(),
		SeverityThreshold:          "medium",
		InsufficientBasisRoundsMax: 3,
		RemediationRoundsMax:       5,
	}
}

// Write persists the grid to dir using the atomic sequence per ADR-010:
//
//  1. Write content to .ghyll/grid.v<N>.yaml.tmp
//  2. fsync the temp file (content durable)
//  3. fsync the containing directory (the new directory entry is durable)
//  4. rename temp → grid.v<N>.yaml (atomic per POSIX)
//  5. fsync the directory again (the rename is durable)
//  6. Write grid.current.tmp containing "v<N>"
//  7. rename grid.current.tmp → grid.current (atomic)
//
// dir is the directory containing the .ghyll/ tree (typically the
// project root). Write creates dir/.ghyll/ if absent.
//
// A reader that observes grid.current = "v<N>" is guaranteed to see
// grid.v<N>.yaml intact on disk thanks to the fsync ordering above.
func (g *Grid) Write(dir string) error {
	if g == nil {
		return errors.New("Write: nil Grid")
	}
	if g.GridVersion < 1 {
		return fmt.Errorf("Write: invalid GridVersion %d (must be >= 1)", g.GridVersion)
	}

	ghyllDir := filepath.Join(dir, ".ghyll")
	if err := os.MkdirAll(ghyllDir, 0o755); err != nil {
		return fmt.Errorf("Write: mkdir %q: %w", ghyllDir, err)
	}

	gridName := fmt.Sprintf("%sv%d%s", "grid.", g.GridVersion, GridFileSuffix)
	gridPath := filepath.Join(ghyllDir, gridName)
	gridTmp := gridPath + ".tmp"

	data, err := yaml.Marshal(g)
	if err != nil {
		return fmt.Errorf("Write: marshal grid: %w", err)
	}

	// 1. Write content to temp file.
	f, err := os.OpenFile(gridTmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("Write: open %q: %w", gridTmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: write %q: %w", gridTmp, err)
	}
	// 2. fsync the temp file (content durable).
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: fsync %q: %w", gridTmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: close %q: %w", gridTmp, err)
	}
	// 3. fsync the directory (the new directory entry is durable).
	if err := fsyncDir(ghyllDir); err != nil {
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: fsync dir %q: %w", ghyllDir, err)
	}
	// 4. Atomic rename.
	if err := os.Rename(gridTmp, gridPath); err != nil {
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: rename %q → %q: %w", gridTmp, gridPath, err)
	}
	// 5. fsync the directory again (the rename is durable).
	if err := fsyncDir(ghyllDir); err != nil {
		return fmt.Errorf("Write: fsync dir post-rename: %w", err)
	}

	// 6+7. Write and rename the pointer.
	if err := writePointer(ghyllDir, g.GridVersion); err != nil {
		return err
	}

	return nil
}

// writePointer writes .ghyll/grid.current atomically (temp + rename)
// containing the single line "v<N>\n".
func writePointer(ghyllDir string, version int) error {
	currentPath := filepath.Join(ghyllDir, CurrentPointerFile)
	currentTmp := currentPath + ".tmp"

	content := fmt.Sprintf("v%d\n", version)
	f, err := os.OpenFile(currentTmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("Write: open pointer tmp %q: %w", currentTmp, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		f.Close()
		_ = os.Remove(currentTmp)
		return fmt.Errorf("Write: write pointer tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(currentTmp)
		return fmt.Errorf("Write: fsync pointer tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(currentTmp)
		return fmt.Errorf("Write: close pointer tmp: %w", err)
	}
	if err := os.Rename(currentTmp, currentPath); err != nil {
		_ = os.Remove(currentTmp)
		return fmt.Errorf("Write: rename pointer: %w", err)
	}
	if err := fsyncDir(ghyllDir); err != nil {
		return fmt.Errorf("Write: fsync dir after pointer: %w", err)
	}
	return nil
}

// fsyncDir opens the directory and calls Sync on it. On some platforms
// (Windows) this is a no-op; on POSIX it ensures the directory's
// directory-entry changes are durable.
func fsyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ReadCurrent reads .ghyll/grid.current and returns the version
// integer it names (e.g., "v3" → 3).
//
// Returns ErrGridCurrentAbsent if the pointer file does not exist.
// Returns ErrGridCurrentMalformed if the content is not a single
// well-formed "v<N>" line.
func ReadCurrent(dir string) (int, error) {
	currentPath := filepath.Join(dir, ".ghyll", CurrentPointerFile)
	data, err := os.ReadFile(currentPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrGridCurrentAbsent
		}
		return 0, fmt.Errorf("ReadCurrent: %w", err)
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return 0, ErrGridCurrentMalformed
	}
	if strings.Contains(s, "\n") {
		// Multi-line content is malformed (the pointer should be exactly one line).
		return 0, ErrGridCurrentMalformed
	}
	if !strings.HasPrefix(s, "v") {
		return 0, ErrGridCurrentMalformed
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 1 {
		return 0, ErrGridCurrentMalformed
	}
	return n, nil
}

// Read loads the active grid: follows .ghyll/grid.current to find the
// version, then reads .ghyll/grid.v<N>.yaml.
//
// Returns ErrGridCurrentAbsent if grid.current doesn't exist.
// Returns ErrGridCurrentMalformed if grid.current is corrupted.
// Returns ErrGridCurrentPointsToMissing if grid.current names a
// version whose grid.v<N>.yaml file does not exist.
func Read(dir string) (*Grid, error) {
	version, err := ReadCurrent(dir)
	if err != nil {
		return nil, err
	}
	return ReadVersion(dir, version)
}

// ReadVersion loads a specific grid version, independent of
// grid.current. Useful for inspecting historical versions or for the
// state-machine engine's boot recovery.
func ReadVersion(dir string, version int) (*Grid, error) {
	gridName := fmt.Sprintf("%sv%d%s", "grid.", version, GridFileSuffix)
	gridPath := filepath.Join(dir, ".ghyll", gridName)
	data, err := os.ReadFile(gridPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: grid.v%d.yaml not found", ErrGridCurrentPointsToMissing, version)
		}
		return nil, fmt.Errorf("ReadVersion: read %q: %w", gridPath, err)
	}
	var g Grid
	if err := yaml.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("ReadVersion: parse %q: %w", gridPath, err)
	}
	if g.GridVersion != version {
		return nil, fmt.Errorf("ReadVersion: %q declares grid-version=%d but file is named v%d", gridPath, g.GridVersion, version)
	}
	return &g, nil
}
