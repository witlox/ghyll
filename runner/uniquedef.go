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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/witlox/ghyll/internal/pathglob"
	"github.com/witlox/ghyll/internal/skipdirs"
)

// unique-definition built-in evaluator (Gap 4 / ADR-v4-006).
//
// Per gates/concepts/unique-definition.yaml:
//
//	scope:               path-glob, required
//	field:               string, required
//	field-locator-rule:  string, required (regex / yaml-path / column-name)
//	case-sensitive:      bool, default true
//
// The evaluator walks every file under scope, extracts values of the
// named field via the locator rule, and detects duplicates. Returns
// Pass=true when every value is unique; Pass=false with a list of
// duplicates otherwise; Unevaluated when the locator selects nothing.

// EvaluateUniqueDefinition is the built-in for unique-definition.
func EvaluateUniqueDefinition(ctx context.Context, c Clause) (*Result, error) {
	scope, err := requireStringArg(c.Args, "scope")
	if err != nil {
		return nil, fmt.Errorf("unique-definition: %w", err)
	}
	field, err := requireStringArg(c.Args, "field")
	if err != nil {
		return nil, fmt.Errorf("unique-definition: %w", err)
	}
	locatorRule, err := requireStringArg(c.Args, "field-locator-rule")
	if err != nil {
		return nil, fmt.Errorf("unique-definition: %w", err)
	}
	caseSensitive := true
	if v, ok := c.Args["case-sensitive"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("unique-definition: case-sensitive must be bool, got %T", v)
		}
		caseSensitive = b
	}

	locator, err := compileFieldLocator(locatorRule, field)
	if err != nil {
		return nil, fmt.Errorf("unique-definition: %w", err)
	}

	root := c.ProjectDir
	if root == "" {
		root = "."
	}

	var observations []fieldHit
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
		hits, err := scanFileForFieldValues(ctx, path, rel, locator)
		if err != nil {
			return err
		}
		observations = append(observations, hits...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("unique-definition: walk %q: %w", root, err)
	}

	if len(observations) == 0 {
		return &Result{
			Unevaluated: true,
			Reason:      "no-rule-selectable-locations",
			Details: map[string]any{
				"scope":              scope,
				"field":              field,
				"field-locator-rule": locatorRule,
			},
		}, nil
	}

	// Group by canonicalized value.
	bucket := map[string][]string{}
	for _, obs := range observations {
		key := obs.Value
		if !caseSensitive {
			key = strings.ToLower(key)
		}
		bucket[key] = append(bucket[key], obs.Location)
	}
	dupes := []map[string]any{}
	values := make([]string, 0, len(bucket))
	for k := range bucket {
		values = append(values, k)
	}
	sort.Strings(values)
	for _, k := range values {
		locs := bucket[k]
		if len(locs) < 2 {
			continue
		}
		dupes = append(dupes, map[string]any{
			"value":     k,
			"locations": locs,
		})
	}
	return &Result{
		Pass: len(dupes) == 0,
		Details: map[string]any{
			"duplicates": dupes,
		},
	}, nil
}

// fieldLocator is a compiled rule that produces (value, location)
// observations from one file. The locator family is intentionally
// minimal in v1: regex (single capture group), yaml-path (best-
// effort), column-name (CSV header lookup). Other rules surface a
// schema error at compile time.
type fieldLocator func(ctx context.Context, path, rel string) ([]fieldHit, error)

type fieldHit struct {
	Value    string
	Location string
}

func compileFieldLocator(rule, field string) (fieldLocator, error) {
	rule = strings.TrimSpace(rule)
	switch {
	case strings.HasPrefix(rule, "regex:"):
		expr := strings.TrimPrefix(rule, "regex:")
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("compile regex locator: %w", err)
		}
		return func(_ context.Context, path, rel string) ([]fieldHit, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, nil
			}
			var hits []fieldHit
			sc := bufio.NewScanner(strings.NewReader(string(data)))
			sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			lineNo := 0
			for sc.Scan() {
				lineNo++
				m := re.FindStringSubmatch(sc.Text())
				if len(m) >= 2 {
					hits = append(hits, fieldHit{Value: m[1], Location: fmt.Sprintf("%s:%d", rel, lineNo)})
				} else if len(m) == 1 {
					hits = append(hits, fieldHit{Value: m[0], Location: fmt.Sprintf("%s:%d", rel, lineNo)})
				}
			}
			return hits, nil
		}, nil
	case rule == "column-name", strings.HasPrefix(rule, "column:"):
		// CSV column lookup: first line is the header; pick `field`
		// column index; emit values per row.
		return func(_ context.Context, path, rel string) ([]fieldHit, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, nil
			}
			sc := bufio.NewScanner(strings.NewReader(string(data)))
			sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			var header []string
			var rowNo int
			var hits []fieldHit
			for sc.Scan() {
				row := strings.Split(sc.Text(), ",")
				if header == nil {
					header = row
					for i, h := range header {
						header[i] = strings.TrimSpace(h)
					}
					continue
				}
				rowNo++
				idx := -1
				for i, h := range header {
					if h == field {
						idx = i
						break
					}
				}
				if idx < 0 || idx >= len(row) {
					continue
				}
				hits = append(hits, fieldHit{Value: strings.TrimSpace(row[idx]), Location: fmt.Sprintf("%s:row%d", rel, rowNo)})
			}
			return hits, nil
		}, nil
	case strings.HasPrefix(rule, "yaml-path:"):
		// Dotted yaml-path locator: walks the YAML document at the
		// declared path (e.g., `yaml-path:arrows[].id` or
		// `yaml-path:bounded-contexts[].id`) and emits the value of
		// `field` from each selected node. Path syntax:
		//   - `a.b.c`     — nested map traversal
		//   - `a[]`       — iterate over a sequence; descendants
		//                   apply to each element
		//   - `a.b[].id`  — common case: list of objects, pick `id`
		// Leaf-only behavior: when the path resolves to a sequence
		// of scalars, each scalar is emitted directly (the `field`
		// arg is then a label only).
		fieldPath := strings.TrimPrefix(rule, "yaml-path:")
		return func(_ context.Context, path, rel string) ([]fieldHit, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, nil
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				return nil, nil
			}
			values := walkYAMLPath(&doc, fieldPath, field)
			var hits []fieldHit
			for _, v := range values {
				if v.value == "" {
					continue
				}
				hits = append(hits, fieldHit{
					Value:    v.value,
					Location: fmt.Sprintf("%s:%d", rel, v.line),
				})
			}
			return hits, nil
		}, nil
	}
	return nil, fmt.Errorf("unsupported field-locator-rule %q (expected regex:..., yaml-path:..., column:..., or column-name)", rule)
}

func scanFileForFieldValues(ctx context.Context, path, rel string, locator fieldLocator) ([]fieldHit, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return locator(ctx, path, rel)
}

// yamlPathHit pairs the extracted scalar with its source line so
// the locator can report file:line of the hit.
type yamlPathHit struct {
	value string
	line  int
}

// walkYAMLPath traverses the YAML document at the dotted path and
// returns scalar values for the named field (when the path lands on
// a sequence of mappings) or each scalar element (when the path
// lands on a sequence of scalars).
//
// Path tokens are dot-separated; an empty `[]` suffix iterates a
// sequence; bare names match map keys. Line numbers come from the
// yaml-v3 node positions.
func walkYAMLPath(root *yaml.Node, path, field string) []yamlPathHit {
	if root == nil {
		return nil
	}
	// Unwrap document node.
	cur := root
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}
	tokens := tokenizeYAMLPath(path)
	nodes := walkYAMLNodes([]*yaml.Node{cur}, tokens)
	var out []yamlPathHit
	for _, n := range nodes {
		if n == nil {
			continue
		}
		switch n.Kind {
		case yaml.ScalarNode:
			out = append(out, yamlPathHit{value: n.Value, line: n.Line})
		case yaml.MappingNode:
			if field == "" {
				continue
			}
			for i := 0; i+1 < len(n.Content); i += 2 {
				k := n.Content[i]
				v := n.Content[i+1]
				if k.Kind == yaml.ScalarNode && k.Value == field && v.Kind == yaml.ScalarNode {
					out = append(out, yamlPathHit{value: v.Value, line: v.Line})
				}
			}
		}
	}
	return out
}

// tokenizeYAMLPath splits a path like `a.b[].c` into ordered tokens:
//   - bare names ("a", "b", "c")
//   - "[]" for sequence iteration
func tokenizeYAMLPath(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(p, ".") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasSuffix(raw, "[]") {
			name := strings.TrimSuffix(raw, "[]")
			if name != "" {
				out = append(out, name)
			}
			out = append(out, "[]")
			continue
		}
		out = append(out, raw)
	}
	return out
}

// walkYAMLNodes steps through tokens, descending mappings or
// iterating sequences as directed.
func walkYAMLNodes(nodes []*yaml.Node, tokens []string) []*yaml.Node {
	for _, tok := range tokens {
		var next []*yaml.Node
		for _, n := range nodes {
			if n == nil {
				continue
			}
			switch tok {
			case "[]":
				if n.Kind == yaml.SequenceNode {
					next = append(next, n.Content...)
				}
			default:
				if n.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(n.Content); i += 2 {
						k := n.Content[i]
						v := n.Content[i+1]
						if k.Kind == yaml.ScalarNode && k.Value == tok {
							next = append(next, v)
						}
					}
				}
			}
		}
		nodes = next
	}
	return nodes
}
