package config

import (
	"embed"
	"io/fs"
	"sort"
	"strings"

	_ "embed"
)

// defaultTemplate holds the canonical example config TOML. The
// example.toml file lives next to this file inside the config/
// package directory so go:embed can resolve it without a build tag
// or generator step.
//
// Shipped to operators verbatim on first run when ~/.ghyll/config.toml
// is missing (C-2): the binary is self-sufficient and does not depend
// on the release tarball carrying example.toml.
//
//go:embed example.toml
var defaultTemplate []byte

// DefaultTemplate returns the embedded default config TOML. The
// returned slice is a fresh copy — callers may mutate it freely
// (e.g., to inject env-specific endpoints) without poisoning the
// shared backing array.
func DefaultTemplate() []byte {
	out := make([]byte, len(defaultTemplate))
	copy(out, defaultTemplate)
	return out
}

// userHomeFS embeds the biased default user-home tree shipped on
// first run alongside config.toml. Layout matches what lands in
// ~/.ghyll/ verbatim:
//
//	instructions.md
//	commands/{status,verify,spec-check}.md
//	guidelines/{engineering,ci,go,python,cpp,rust}.md
//
// All files carry a "ghyll bias — edit/delete as needed" header so
// operators know they're seeds, not policy. Per the loading model:
//
//   - instructions.md + commands/*.md auto-load every session.
//   - guidelines/*.md sit in the library; `ghyll init --language`
//     inline-copies the chosen language's guideline plus
//     engineering.md into <project>/.ghyll/instructions.md inside
//     a <!-- ghyll-traits-begin --> / <!-- ghyll-traits-end -->
//     marker block.
//
//go:embed userhome/instructions.md userhome/commands/*.md userhome/guidelines/*.md
var userHomeFS embed.FS

// UserHomeFiles returns the embedded user-home seed tree as a map
// of project-relative paths (e.g. "instructions.md",
// "commands/status.md") to file content. Order-stable: keys are
// sorted lexicographically so callers can iterate deterministically.
func UserHomeFiles() map[string][]byte {
	out := map[string][]byte{}
	_ = fs.WalkDir(userHomeFS, "userhome", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := userHomeFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel := strings.TrimPrefix(path, "userhome/")
		buf := make([]byte, len(data))
		copy(buf, data)
		out[rel] = buf
		return nil
	})
	return out
}

// UserHomePathsSorted returns the relative paths of every file in
// the seed tree, sorted. Helper for tests + the seeder so output is
// reproducible.
func UserHomePathsSorted() []string {
	files := UserHomeFiles()
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// UserHomeGuideline returns the content of a single guideline file
// from the seed library, keyed by language name (e.g. "go",
// "python", "cpp", "rust") or by named guideline ("engineering",
// "ci"). Returns nil + false if the name doesn't resolve.
//
// Used by `ghyll init --language <lang>` to compose the trait block
// it appends to <project>/.ghyll/instructions.md.
func UserHomeGuideline(name string) ([]byte, bool) {
	files := UserHomeFiles()
	data, ok := files["guidelines/"+name+".md"]
	return data, ok
}
