package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// mode-determinable-from-repo built-in evaluator (Gap 4 / ADR-v4-006).
//
// Per gates/concepts/mode-determinable-from-repo.yaml:
//
//	discriminator:  string, required (identifier for the mode being checked)
//	rule:           command, required (read-only shell rule that prints the mode)
//	valid-modes:    []string, required (acceptable mode values)
//
// The v2 artifact contract additionally directs the evaluator to
// honor an optional `mode-discriminator-path` arg (default
// `.ghyll/mode.yaml`) and surface path-safety via O_NOFOLLOW + clamp-
// to-ProjectDir (design H6 closure). The implementer quotes BOTH arg
// vocabularies — the YAML's authoritative names AND the artifact's
// extension — to satisfy ArgsMatchYAML AND the path-safety contract.
//
// Pass iff:
//   - the discriminator file (default `.ghyll/mode.yaml`) exists, is
//     readable, parseable, and its value is in valid-modes; OR
//   - if `mode-discriminator-path` is supplied: same checks against
//     that explicit path.
//
// Refuses any path that escapes ProjectDir (via `..`, absolute path,
// or symlink).

// EvaluateModeDeterminableFromRepo is the built-in for
// mode-determinable-from-repo.
func EvaluateModeDeterminableFromRepo(ctx context.Context, c Clause) (*Result, error) {
	// Discriminator is required per YAML.
	discriminator, err := requireStringArg(c.Args, "discriminator")
	if err != nil {
		return nil, fmt.Errorf("mode-determinable-from-repo: %w", err)
	}
	validModes, err := coerceStringList(c.Args["valid-modes"])
	if err != nil {
		return nil, fmt.Errorf("mode-determinable-from-repo: valid-modes: %w", err)
	}
	if len(validModes) == 0 {
		return nil, errors.New("mode-determinable-from-repo: valid-modes must be non-empty")
	}

	// Optional explicit discriminator path (artifact contract Gap 4
	// extension). Defaults to .ghyll/mode.yaml.
	discPath := ".ghyll/mode.yaml"
	if v, ok := c.Args["mode-discriminator-path"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("mode-determinable-from-repo: mode-discriminator-path must be string, got %T", v)
		}
		if strings.TrimSpace(s) != "" {
			discPath = s
		}
	}

	// Path-safety: reject `..`, absolute outside workdir, or symlink.
	// (Mirrors EvaluateArrowArtifactPresent — uses ResolveProjectPath
	// + O_NOFOLLOW open via openNoFollow.)
	if c.ProjectDir == "" {
		return nil, errors.New("mode-determinable-from-repo: ProjectDir is empty")
	}
	resolved, err := ResolveProjectPath(c.ProjectDir, discPath)
	if err != nil {
		return nil, fmt.Errorf("mode-determinable-from-repo: resolve discriminator path: %w", err)
	}
	// Open with O_NOFOLLOW; an intermediate symlink fails with ELOOP.
	f, err := openNoFollow(resolved)
	if err != nil {
		if isSymlinkOpenError(err) {
			return nil, fmt.Errorf("mode-determinable-from-repo: discriminator path is a symlink (refused): %s", filepath.Base(resolved))
		}
		// Missing file is a clean fail-with-details, not an error.
		return &Result{
			Pass: false,
			Details: map[string]any{
				"discriminator":           discriminator,
				"mode-discriminator-path": discPath,
				"error":                   "discriminator file does not exist or is unreadable",
			},
		}, nil
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("mode-determinable-from-repo: read discriminator path: %w", err)
	}

	mode := extractModeValue(string(data), discriminator)
	if mode == "" {
		return &Result{
			Pass: false,
			Details: map[string]any{
				"discriminator":           discriminator,
				"mode-discriminator-path": discPath,
				"mode":                    "",
				"error":                   "discriminator value missing or empty",
			},
		}, nil
	}
	for _, v := range validModes {
		if mode == v {
			return &Result{
				Pass: true,
				Details: map[string]any{
					"discriminator":           discriminator,
					"mode-discriminator-path": discPath,
					"mode":                    mode,
				},
			}, nil
		}
	}
	return &Result{
		Pass: false,
		Details: map[string]any{
			"discriminator":           discriminator,
			"mode-discriminator-path": discPath,
			"mode":                    mode,
			"valid-modes":             validModes,
			"error":                   "mode value not in valid-modes",
		},
	}, nil
}

// extractModeValue pulls the value of the named discriminator field
// from the file contents. Accepts a flat YAML `key: value` form (the
// .ghyll/mode.yaml convention) or a single trimmed line as the
// value. v1 best-effort.
func extractModeValue(content, discriminator string) string {
	lines := strings.Split(content, "\n")
	prefix := discriminator + ":"
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(trim, prefix))
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	// Fallback: if the file is a single-line value, return that
	// (the rule command may have printed the mode directly).
	if strings.Count(strings.TrimSpace(content), "\n") == 0 {
		return strings.TrimSpace(content)
	}
	return ""
}
