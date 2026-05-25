package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/witlox/ghyll/internal/safefile"
	"gopkg.in/yaml.v3"
)

// Grid is the on-disk representation of a project's arrow grid at a
// specific version, per ADR-010 / D38.
//
// Grids are immutable after write: each amendment writes a NEW
// grid.v<N+1>.yaml; old files persist on disk for audit. The
// active version is named by the grid.current pointer file.
type Grid struct {
	GridVersion   int    `yaml:"grid-version"`
	CreatedAt     string `yaml:"created-at"`
	CreatedByOpID string `yaml:"created-by-op-id"`

	BoundedContexts            []BoundedContext  `yaml:"bounded-contexts"`
	LanguageBindings           map[string]string `yaml:"language-bindings,omitempty"`
	DepthLadder                []DepthLadderTier `yaml:"depth-ladder"`
	SeverityThreshold          string            `yaml:"severity-threshold"`
	InsufficientBasisRoundsMax int               `yaml:"insufficient-basis-rounds-max"`
	RemediationRoundsMax       int               `yaml:"remediation-rounds-max"`

	// Tier 2 (ADR-016 / Step 11):

	// ResidueNoteMaxBytes caps the operator's write-residue-note
	// payload (insufficient-basis verdicts, escalation residues).
	// Default 16 KiB. Zero means "use built-in default".
	ResidueNoteMaxBytes int `yaml:"residue-note-max-bytes,omitempty"`

	// ModalPendingMaxLen caps the in-flight modal queue. Beyond
	// this length the modal driver drops new events + emits
	// OpEventModalBackpressure. Default 64. Zero means "use
	// built-in default".
	ModalPendingMaxLen int `yaml:"modal-pending-max-len,omitempty"`

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
	ErrGridCurrentAbsent          = errors.New("grid-current-absent")
	ErrGridCurrentMalformed       = errors.New("grid-current-malformed")
	ErrGridCurrentPointsToMissing = errors.New("grid-current-points-to-missing-version")

	// ErrGridVersionExists — destination grid.v<N>.yaml already on disk.
	// Per ADR-010, grid files are immutable after write; the writer
	// refuses to overwrite. validation-pass-1 finding #5.
	ErrGridVersionExists = errors.New("grid-version-already-exists")

	// ErrGridInconsistent — grid.current and on-disk grid files
	// disagree (e.g., a grid.v(N+1).yaml exists while grid.current
	// still names vN). Suggests a crash between the grid-file rename
	// and the pointer update. validation-pass-1 finding #4 / FM-12.
	ErrGridInconsistent = errors.New("grid-inconsistent")
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
		ResidueNoteMaxBytes:        DefaultResidueNoteMaxBytes,
		ModalPendingMaxLen:         DefaultModalPendingMaxLen,
	}
}

// Tier 2 default policy values (ADR-016 / Step 11). The grid
// schema overrides these per-project; zero on disk normalizes to
// the default via Grid.NormalizeTier2Defaults.
const (
	DefaultResidueNoteMaxBytes = 16 * 1024
	DefaultModalPendingMaxLen  = 64
)

// NormalizeTier2Defaults fills ResidueNoteMaxBytes and
// ModalPendingMaxLen with built-in defaults if the grid file
// omitted them (YAML zero values). Called after Read so the
// runtime sees concrete numbers, not zeros.
func (g *Grid) NormalizeTier2Defaults() {
	if g == nil {
		return
	}
	if g.ResidueNoteMaxBytes <= 0 {
		g.ResidueNoteMaxBytes = DefaultResidueNoteMaxBytes
	}
	if g.ModalPendingMaxLen <= 0 {
		g.ModalPendingMaxLen = DefaultModalPendingMaxLen
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
	// Post-prod-readiness adversarial M-B: create .ghyll/ at 0o700
	// rather than the os.MkdirAll default (0o755). The grid yaml
	// inside is project-shared (0o644 is fine), but the sibling
	// engine.db carries attestation records keyed by op-id. A
	// world-stat-able directory exposes engine.db's existence + size
	// to other users on the same host. Tighten the dir even if the
	// individual files inside remain group/world readable.
	if err := os.MkdirAll(ghyllDir, 0o700); err != nil {
		return fmt.Errorf("Write: mkdir %q: %w", ghyllDir, err)
	}
	// MkdirAll is a no-op when the dir already exists — make sure
	// the tighter mode is enforced even if a pre-existing dir was
	// created at 0o755 by an older binary or by a parallel writer
	// (the engine init path).
	if err := os.Chmod(ghyllDir, 0o700); err != nil {
		return fmt.Errorf("Write: chmod %q: %w", ghyllDir, err)
	}

	gridName := fmt.Sprintf("%s%d%s", GridFilePrefix, g.GridVersion, GridFileSuffix)
	gridPath := filepath.Join(ghyllDir, gridName)
	gridTmp := gridPath + ".tmp"

	// Refuse if destination already exists: grid files are immutable
	// per ADR-010 (validation-pass-1 finding #5).
	if _, err := os.Lstat(gridPath); err == nil {
		return fmt.Errorf("%w: %s", ErrGridVersionExists, gridPath)
	}

	data, err := yaml.Marshal(g)
	if err != nil {
		return fmt.Errorf("Write: marshal grid: %w", err)
	}

	// 1. Write content to temp file with O_EXCL: fail if a stale tmp
	//    already exists (validation-pass-1 finding #5 / #22).
	f, err := os.OpenFile(gridTmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("Write: open %q (O_EXCL): %w", gridTmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(gridTmp)
		return fmt.Errorf("Write: write %q: %w", gridTmp, err)
	}
	// 2. fsync the temp file (content durable).
	if err := f.Sync(); err != nil {
		_ = f.Close()
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
		_ = f.Close()
		_ = os.Remove(currentTmp)
		return fmt.Errorf("Write: write pointer tmp: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
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
//
// Close errors are surfaced rather than dropped (validation-pass-1
// finding #13): a Close error on some filesystems (NFS particularly)
// indicates the data fsync masked is not actually durable.
func fsyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// NextVersion returns the version integer for a grid that would
// succeed this one. Helper for callers (amendment component, future
// re-init flows) that bump versions on writes, so the increment is
// not hand-rolled at each call site (validation-pass-1 finding #24).
// A nil receiver returns 1 (fresh-project start).
func (g *Grid) NextVersion() int {
	if g == nil {
		return 1
	}
	return g.GridVersion + 1
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
// Returns ErrGridInconsistent if the on-disk grid files reveal a
// half-completed Write (e.g., grid.v(N+1).yaml exists but
// grid.current still says vN). validation-pass-1 finding #4.
func Read(dir string) (*Grid, error) {
	version, err := ReadCurrent(dir)
	if err != nil {
		return nil, err
	}
	if err := detectInconsistency(dir, version); err != nil {
		return nil, err
	}
	return ReadVersion(dir, version)
}

// detectInconsistency scans .ghyll/ for grid.v<N>.yaml files whose
// version exceeds the version named in grid.current. Such files
// suggest a crash between the grid-file rename and the pointer update.
// Returns ErrGridInconsistent if any grid file with version > current
// is found; nil on consistent state.
func detectInconsistency(dir string, current int) error {
	ghyllDir := filepath.Join(dir, ".ghyll")
	entries, err := os.ReadDir(ghyllDir)
	if err != nil {
		// Let the underlying reader produce a more specific error.
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, GridFilePrefix) || !strings.HasSuffix(name, GridFileSuffix) {
			continue
		}
		verStr := strings.TrimSuffix(strings.TrimPrefix(name, GridFilePrefix), GridFileSuffix)
		n, err := strconv.Atoi(verStr)
		if err != nil || n < 1 {
			continue
		}
		if n > current {
			return fmt.Errorf("%w: grid.v%d.yaml exists but grid.current points to v%d (possible crash mid-Write; recover by either removing grid.v%d.yaml or moving grid.current to v%d)",
				ErrGridInconsistent, n, current, n, n)
		}
	}
	return nil
}

// ReadVersion loads a specific grid version, independent of
// grid.current. Useful for inspecting historical versions or for the
// state-machine engine's boot recovery.
// MaxGridFileBytes caps grid.v<N>.yaml at 4 MiB. Tier 3 / SR H-2:
// previously unbounded os.ReadFile + yaml.Unmarshal could OOM on
// a 2 GB / alias-bomb grid file.
const MaxGridFileBytes = 4 * 1024 * 1024

// MaxBoundedContextsImport caps the number of bounded contexts a
// loaded grid yaml can declare. Tier 3 / SR L-8 — prevents 100k-
// context forged yaml.
const MaxBoundedContextsImport = 256

func ReadVersion(dir string, version int) (*Grid, error) {
	gridName := fmt.Sprintf("%s%d%s", GridFilePrefix, version, GridFileSuffix)
	gridPath := filepath.Join(dir, ".ghyll", gridName)
	// Tier 3 / SR H-2: cap at 4 MiB + refuse symlinks before
	// yaml.Unmarshal allocates.
	data, err := safefile.ReadCappedFile(gridPath, MaxGridFileBytes)
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
	// Tier 3 / SR L-8: bound bounded-contexts at the load
	// boundary (DeclareContext enforces this in code-paths that
	// build the grid; yaml-load was the missing path).
	if len(g.BoundedContexts) > MaxBoundedContextsImport {
		return nil, fmt.Errorf("ReadVersion: bounded-contexts count %d exceeds cap %d",
			len(g.BoundedContexts), MaxBoundedContextsImport)
	}
	// Tier 3 / SR M-10: bound ResidueNoteMaxBytes range so a
	// forged grid with `residue-note-max-bytes: 1` doesn't
	// silently kill operator residue flow, and so a `: huge`
	// doesn't allow oversized residues to flow.
	if g.ResidueNoteMaxBytes != 0 {
		if g.ResidueNoteMaxBytes < 1024 || g.ResidueNoteMaxBytes > 1*1024*1024 {
			return nil, fmt.Errorf("ReadVersion: residue-note-max-bytes %d out of range [1024, 1048576]",
				g.ResidueNoteMaxBytes)
		}
	}
	g.NormalizeTier2Defaults()
	return &g, nil
}
