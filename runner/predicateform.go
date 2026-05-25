package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/witlox/ghyll/internal/pathglob"
	"github.com/witlox/ghyll/internal/skipdirs"
)

// predicate-form built-in evaluator (Gap 4 / ADR-v4-006).
//
// Per gates/concepts/predicate-form.yaml:
//
//	scope:               path-glob, required
//	collection-locator:  string, required (yaml-path / regex / markdown-section-name)
//	predicate-grammar:   string, default "contains a comparison operator OR assert(...) form"
//
// Walks the scope, locates the collection in each matching file,
// validates that every entry is a predicate. Fails with a list of
// non-predicates. Unevaluated if the locator selects nothing.

// defaultPredicateGrammar matches the YAML's default: contains a
// comparison operator OR is in assert(...) form.
var defaultPredicateRE = regexp.MustCompile(`(?:[<>!=]=?|==|>=|<=|\bassert\()`)

// EvaluatePredicateForm is the built-in for predicate-form.
func EvaluatePredicateForm(ctx context.Context, c Clause) (*Result, error) {
	scope, err := requireStringArg(c.Args, "scope")
	if err != nil {
		return nil, fmt.Errorf("predicate-form: %w", err)
	}
	collectionLocator, err := requireStringArg(c.Args, "collection-locator")
	if err != nil {
		return nil, fmt.Errorf("predicate-form: %w", err)
	}
	grammar := ""
	if v, ok := c.Args["predicate-grammar"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("predicate-form: predicate-grammar must be string, got %T", v)
		}
		grammar = s
	}
	predicate, err := compilePredicateGrammar(grammar)
	if err != nil {
		return nil, fmt.Errorf("predicate-form: %w", err)
	}

	root := c.ProjectDir
	if root == "" {
		root = "."
	}

	var entries []predicateEntry
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) || errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != root && filepath.Dir(path) == root && skipdirs.IsBuildOrHarness(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !pathglob.Match(scope, rel) {
			return nil
		}
		fileEntries, err := locateCollectionEntries(ctx, path, rel, collectionLocator)
		if err != nil {
			return err
		}
		entries = append(entries, fileEntries...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("predicate-form: walk %q: %w", root, err)
	}

	if len(entries) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      "no-rule-selectable-locations",
			Details: map[string]any{
				"scope":              scope,
				"collection-locator": collectionLocator,
			},
		}, nil
	}

	nonPredicates := []map[string]any{}
	for _, e := range entries {
		if predicate(e.Text) {
			continue
		}
		nonPredicates = append(nonPredicates, map[string]any{
			"entry":    e.Text,
			"location": e.Location,
			"hint":     "entry lacks a comparison operator or assert(...) form",
		})
	}
	return &Result{
		Pass: len(nonPredicates) == 0,
		Details: map[string]any{
			"non-predicates": nonPredicates,
		},
	}, nil
}

type predicateEntry struct {
	Text     string
	Location string
}

func compilePredicateGrammar(grammar string) (func(string) bool, error) {
	if strings.TrimSpace(grammar) == "" {
		return func(s string) bool {
			return defaultPredicateRE.MatchString(s)
		}, nil
	}
	// If grammar is a regex-tagged string, compile; otherwise treat
	// the grammar string as natural language and fall back to the
	// default. v1 best-effort.
	if strings.HasPrefix(grammar, "regex:") {
		re, err := regexp.Compile(strings.TrimPrefix(grammar, "regex:"))
		if err != nil {
			return nil, fmt.Errorf("compile predicate-grammar: %w", err)
		}
		return func(s string) bool { return re.MatchString(s) }, nil
	}
	// Natural-language grammar (the YAML default): apply the
	// default heuristic.
	return func(s string) bool {
		return defaultPredicateRE.MatchString(s)
	}, nil
}

func locateCollectionEntries(_ context.Context, path, rel, locator string) ([]predicateEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	switch {
	case strings.HasPrefix(locator, "markdown-section:"):
		section := strings.TrimPrefix(locator, "markdown-section:")
		return parseMarkdownSectionEntries(string(data), rel, section), nil
	case strings.HasPrefix(locator, "regex:"):
		expr := strings.TrimPrefix(locator, "regex:")
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("compile collection-locator: %w", err)
		}
		var out []predicateEntry
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			m := re.FindStringSubmatch(sc.Text())
			if len(m) >= 2 {
				out = append(out, predicateEntry{Text: m[1], Location: fmt.Sprintf("%s:%d", rel, lineNo)})
			}
		}
		return out, nil
	case strings.HasPrefix(locator, "yaml-path:"):
		// Dotted yaml-path locator: walks the document at the named
		// path and emits each scalar entry. Path tokens follow the
		// same vocabulary as unique-definition's yaml-path (`a.b`,
		// `a[]`); the locator typically lands on a sequence whose
		// elements are predicate strings.
		fieldPath := strings.TrimPrefix(locator, "yaml-path:")
		return parseYAMLPathPredicates(data, rel, fieldPath), nil
	}
	return nil, fmt.Errorf("unsupported collection-locator %q (expected markdown-section:..., regex:..., or yaml-path:...)", locator)
}

func parseMarkdownSectionEntries(content, rel, section string) []predicateEntry {
	var out []predicateEntry
	sc := bufio.NewScanner(strings.NewReader(content))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	inSection := false
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trim, "#"))
			inSection = heading == section
			continue
		}
		if !inSection {
			continue
		}
		// Match `- entry` or `* entry` bullets.
		if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") {
			entry := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trim, "-"), "*"))
			if entry != "" {
				out = append(out, predicateEntry{Text: entry, Location: fmt.Sprintf("%s:%d", rel, lineNo)})
			}
		}
	}
	return out
}

// parseYAMLPathPredicates uses yaml.v3 to walk the document at the
// dotted path and emit one predicate entry per scalar at the leaf.
// Mirrors uniquedef.go's walkYAMLPath; kept package-local to keep
// the evaluator's responsibility surface small.
func parseYAMLPathPredicates(data []byte, rel, fieldPath string) []predicateEntry {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	cur := &doc
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}
	tokens := tokenizeYAMLPath(fieldPath)
	nodes := walkYAMLNodes([]*yaml.Node{cur}, tokens)
	var out []predicateEntry
	for _, n := range nodes {
		if n == nil {
			continue
		}
		switch n.Kind {
		case yaml.ScalarNode:
			if n.Value == "" {
				continue
			}
			out = append(out, predicateEntry{
				Text:     n.Value,
				Location: fmt.Sprintf("%s:%d", rel, n.Line),
			})
		case yaml.SequenceNode:
			for _, c := range n.Content {
				if c.Kind == yaml.ScalarNode && c.Value != "" {
					out = append(out, predicateEntry{
						Text:     c.Value,
						Location: fmt.Sprintf("%s:%d", rel, c.Line),
					})
				}
			}
		}
	}
	return out
}
