package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/witlox/ghyll/config"
)

// supportedTraitLanguages enumerates the language ids that have a
// matching `guidelines/<lang>.md` in the embedded user-home seed.
// Kept in sync with config/userhome/guidelines/*.md.
var supportedTraitLanguages = map[string]bool{
	"go":     true,
	"python": true,
	"cpp":    true,
	"rust":   true,
}

// errUnsupportedTraitLanguage names a language the seed library
// doesn't carry a guideline for. Useful for typed error-handling in
// callers that want to suggest a fallback.
var errUnsupportedTraitLanguage = errors.New("unsupported trait language")

// resolveTraitLanguages converts the `--language` flag value into
// the ordered list of language ids whose guidelines the init
// command will inline into the project's instructions.md trait
// block.
//
// Flag semantics:
//   - "none" → empty slice (operator opts out of the trait block).
//   - "auto" → use profileLanguages (extension-detected by
//     bootstrap.ProfileRepo). Filtered to languages the seed
//     library actually ships. Empty if no supported language
//     detected — no error; just an empty trait block.
//   - any comma-separated explicit list ("go", "go,python") →
//     validated against supportedTraitLanguages.
func resolveTraitLanguages(flag string, profileLanguages []string) ([]string, error) {
	flag = strings.TrimSpace(flag)
	if flag == "" || flag == "none" {
		return nil, nil
	}
	if flag == "auto" {
		out := make([]string, 0, len(profileLanguages))
		seen := map[string]bool{}
		for _, lang := range profileLanguages {
			lang = strings.ToLower(strings.TrimSpace(lang))
			if seen[lang] || !supportedTraitLanguages[lang] {
				continue
			}
			seen[lang] = true
			out = append(out, lang)
		}
		return out, nil
	}
	// Explicit list.
	parts := strings.Split(flag, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		lang := strings.ToLower(strings.TrimSpace(p))
		if lang == "" {
			continue
		}
		if !supportedTraitLanguages[lang] {
			return nil, fmt.Errorf("%w: %q (supported: go, python, cpp, rust, or auto/none)",
				errUnsupportedTraitLanguage, lang)
		}
		if seen[lang] {
			continue
		}
		seen[lang] = true
		out = append(out, lang)
	}
	return out, nil
}

// traitBlockMarkers identify the inline-copied region inside
// <project>/.ghyll/instructions.md. Stable across `--force-traits`
// re-runs so a future invocation can rewrite the block in place
// without touching operator-authored content above or below.
var traitBlockMarkers = struct {
	begin string
	end   string
}{
	begin: "<!-- ghyll-traits-begin -->",
	end:   "<!-- ghyll-traits-end -->",
}

// traitBlockBodyRegex matches the entire trait block including its
// markers. (?s) makes . match newlines. Used to locate-and-replace
// an existing block on `--force-traits`.
var traitBlockBodyRegex = regexp.MustCompile(
	`(?s)` + regexp.QuoteMeta(traitBlockMarkers.begin) + `.*?` + regexp.QuoteMeta(traitBlockMarkers.end) + `\n?`,
)

// writeTraitBlock composes engineering.md plus each language's
// guideline into a single block delimited by ghyll-traits markers
// and inserts it into instructionsPath. Semantics:
//
//   - If instructionsPath does not exist: creates the file with a
//     short header explaining the file's role, then the trait block.
//   - If instructionsPath exists and contains a trait block:
//     unchanged unless force is true. force=true rewrites the
//     existing block in place (preserving operator-authored prose
//     above + below).
//   - If instructionsPath exists and contains no trait block:
//     appends the trait block at the end.
func writeTraitBlock(instructionsPath string, languages []string, force bool) error {
	body := composeTraitBlock(languages)
	existing, readErr := os.ReadFile(instructionsPath)
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", instructionsPath, readErr)
		}
		// Fresh file. Write a short header + the block.
		header := projectInstructionsHeader()
		return writeFileSeed(instructionsPath, []byte(header+"\n\n"+body+"\n"))
	}
	matches := traitBlockBodyRegex.Find(existing)
	if matches == nil {
		// No existing block — append.
		out := string(existing)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\n" + body + "\n"
		return writeFileSeed(instructionsPath, []byte(out))
	}
	if !force {
		// Existing block + no force: leave alone.
		return nil
	}
	// Replace the existing block.
	replaced := traitBlockBodyRegex.ReplaceAllString(string(existing), body+"\n")
	return writeFileSeed(instructionsPath, []byte(replaced))
}

// composeTraitBlock returns the marker-delimited body that goes
// into instructions.md. Always inlines engineering.md as the first
// trait (it's language-agnostic), then each requested language's
// guideline in order.
func composeTraitBlock(languages []string) string {
	var b strings.Builder
	b.WriteString(traitBlockMarkers.begin)
	b.WriteString("\n")
	if eng, ok := config.UserHomeGuideline("engineering"); ok {
		b.Write(eng)
		if len(eng) > 0 && eng[len(eng)-1] != '\n' {
			b.WriteString("\n")
		}
	}
	for _, lang := range languages {
		if g, ok := config.UserHomeGuideline(lang); ok {
			b.WriteString("\n")
			b.Write(g)
			if len(g) > 0 && g[len(g)-1] != '\n' {
				b.WriteString("\n")
			}
		}
	}
	b.WriteString(traitBlockMarkers.end)
	return b.String()
}

// projectInstructionsHeader is the short prose that opens a freshly-
// created <project>/.ghyll/instructions.md before the trait block.
// Identifies the file as project-local and points at ghyll init.
func projectInstructionsHeader() string {
	return "<!-- Generated by `ghyll init`. The trait block below is overwritten by `--force-traits`. -->\n# Project instructions"
}

// writeFileSeed writes data atomically (temp + rename) at mode
// 0o600 so the contents — which may carry operator workflow
// preferences — aren't world-readable. Caller-supplied data MUST
// already be the full file content; this is a write-replace, not
// an append.
func writeFileSeed(path string, data []byte) error {
	dir := strings.TrimSuffix(path, "/"+lastPathComponent(path))
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".ghyll-traits-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	if _, wErr := tmp.Write(data); wErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp %s: %w", tmpPath, wErr)
	}
	if cErr := tmp.Close(); cErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp %s: %w", tmpPath, cErr)
	}
	if chErr := os.Chmod(tmpPath, 0o600); chErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp %s: %w", tmpPath, chErr)
	}
	if rErr := os.Rename(tmpPath, path); rErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, rErr)
	}
	return nil
}

// lastPathComponent returns whatever follows the last forward slash
// in p, or p itself if none. Tiny helper that avoids dragging in
// path/filepath just for one Split.
func lastPathComponent(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}
