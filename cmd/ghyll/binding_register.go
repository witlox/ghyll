// Package-local language-binding registration (diamond v4 Gap 3,
// ADR-v4-007).
//
// `registerGridBindings` populates a runner.Registry with one
// BindingEvaluator per declaration in grid.LanguageBindings.
// Validates concept-is-language-bound, command non-empty, key shape.
//
// Per ADR-v4-007 this lives in cmd/ghyll (package main) — `runner`
// cannot import `bootstrap` (the bootstrap → runner edge would
// cycle), and `bootstrap` should not own runtime-mutation logic. The
// integration site (`cmd/ghyll`) already imports both packages.

package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/witlox/ghyll/bootstrap"
	"github.com/witlox/ghyll/runner"
)

// ErrLanguageBindingInvalid is the typed refusal for a grid that
// declares a binding key whose concept is NOT in the
// language-bound:true set.
var ErrLanguageBindingInvalid = errors.New("language-binding-invalid")

// argValidationError captures one per-arrow / per-clause schema
// problem surfaced during the untyped-grid walk (R18 closure).
type argValidationError struct {
	ArrowIndex int
	Concept    string
	Arg        string
	Reason     string
}

func (e argValidationError) Error() string {
	return fmt.Sprintf("arrow[%d] concept %q arg %q: %s", e.ArrowIndex, e.Concept, e.Arg, e.Reason)
}

// registerGridBindings populates the registry with one
// runner.BindingEvaluator per (concept, language) declaration in
// grid.LanguageBindings.
//
// Validates:
//   - command is non-empty (ErrBindingCommandEmpty);
//   - key parses as `<concept>.<language>`
//     (delegates to bootstrap.BindingKeysFromStrings);
//   - concept is in the language-bound:true set
//     (ErrLanguageBindingInvalid).
//
// On any of the above, returns a wrapped error and leaves the
// registry in whatever state it had before the failure (callers
// that need atomicity build a snapshot via Registry.Snapshot first
// per ADR-v4-003).
//
// Pre-existing registrations for the same key are replaced via
// Registry.Replace so re-register on amendment works cleanly.
func registerGridBindings(reg *runner.Registry, grid *bootstrap.Grid, workdir string) error {
	if reg == nil {
		return errors.New("registerGridBindings: nil registry")
	}
	if grid == nil {
		return nil
	}
	// Sort keys for deterministic registration order; the registry
	// is unordered but error messages and tests benefit from
	// determinism.
	keys := make([]string, 0, len(grid.LanguageBindings))
	for k := range grid.LanguageBindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, keyStr := range keys {
		command := grid.LanguageBindings[keyStr]
		if command == "" {
			return fmt.Errorf("%w: %s", bootstrap.ErrBindingCommandEmpty, keyStr)
		}
		parsed, err := bootstrap.BindingKeysFromStrings([]string{keyStr})
		if err != nil {
			return fmt.Errorf("registerGridBindings: parse key %q: %w", keyStr, err)
		}
		k := parsed[0]
		if !runner.IsLanguageBoundConcept(k.Concept) {
			return fmt.Errorf("%w: %q is not a language-bound concept (key=%s)",
				ErrLanguageBindingInvalid, k.Concept, keyStr)
		}
		evaluator := runner.NewBindingEvaluator(command,
			runner.WithWorkingDir(workdir),
			runner.WithTimeout(runner.DefaultBindingTimeout),
		)
		fullKey := k.String()
		if err := reg.Register(fullKey, evaluator); err != nil {
			// Re-register path: existing binding gets replaced.
			if errors.Is(err, runner.ErrConceptAlreadyRegistered) {
				if rerr := reg.Replace(fullKey, evaluator); rerr != nil {
					return fmt.Errorf("registerGridBindings: replace %q: %w", fullKey, rerr)
				}
				continue
			}
			return fmt.Errorf("registerGridBindings: register %q: %w", fullKey, err)
		}
	}
	return nil
}

// requiredBindingsFromUntypedGrid walks the bootstrap.Grid.Arrows
// untyped slice (per bootstrap/grid.go:49) and emits the
// deduplicated set of BindingKey{Concept, Language} that the grid's
// declared arrows reference. Returns the keys (sorted), per-arrow
// schema errors (R18 closure: language arg must be a string), and
// a hard error on un-recoverable parse failure.
//
// This walker covers the pre-Replay phase: freshly-initialized
// projects have arrows declared in grid.yaml but the typed
// runner.Grid is empty until Replay populates it. The
// post-Replay walk is the companion `requiredBindingsFromTypedGrid`.
func requiredBindingsFromUntypedGrid(g *bootstrap.Grid) ([]bootstrap.BindingKey, []argValidationError, error) {
	if g == nil {
		return nil, nil, nil
	}
	seen := map[bootstrap.BindingKey]struct{}{}
	var validations []argValidationError
	for i, arrowMap := range g.Arrows {
		clausesRaw, ok := arrowMap["clauses"].([]any)
		if !ok {
			continue
		}
		for _, clRaw := range clausesRaw {
			cl, ok := clRaw.(map[string]any)
			if !ok {
				continue
			}
			concept, _ := cl["concept"].(string)
			if concept == "" {
				continue
			}
			if !runner.IsLanguageBoundConcept(concept) {
				continue
			}
			args, _ := cl["args"].(map[string]any)
			lang := ""
			if args != nil {
				if raw, ok := args["language"]; ok {
					if s, ok := raw.(string); ok {
						lang = s
					} else {
						validations = append(validations, argValidationError{
							ArrowIndex: i,
							Concept:    concept,
							Arg:        "language",
							Reason:     fmt.Sprintf("expected string, got %T", raw),
						})
						continue
					}
				}
			}
			seen[bootstrap.BindingKey{Concept: concept, Language: lang}] = struct{}{}
		}
	}
	out := make([]bootstrap.BindingKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, validations, nil
}

// requiredBindingsFromTypedGrid walks every dispatchable arrow in
// the typed runner.Grid (populated by Replay) and emits the
// deduplicated set of (concept, language) keys. The runner-side
// IsLanguageBoundConcept predicate is the source of truth.
func requiredBindingsFromTypedGrid(g *runner.Grid) []bootstrap.BindingKey {
	if g == nil {
		return nil
	}
	seen := map[bootstrap.BindingKey]struct{}{}
	for _, id := range g.Arrows() {
		def, ok := g.Lookup(id)
		if !ok {
			continue
		}
		for _, cls := range def.Clauses {
			if !runner.IsLanguageBoundConcept(cls.Concept) {
				continue
			}
			lang := languageFromArgs(cls.Args)
			seen[bootstrap.BindingKey{Concept: cls.Concept, Language: lang}] = struct{}{}
		}
	}
	out := make([]bootstrap.BindingKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// languageFromArgs extracts the "language" arg from a clause's Args
// map. Returns "" on missing-or-malformed (the coverage check then
// surfaces a typed MissingBindingError listing the missing
// "concept.").
func languageFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	raw, ok := args["language"]
	if !ok {
		return ""
	}
	lang, _ := raw.(string)
	return lang
}

// verifyBindingsCoverage walks both the typed and untyped grid
// sources, deduplicates the BindingKeys, and verifies each is
// registered. Returns *bootstrap.MissingBindingError on miss; nil
// on full coverage.
func verifyBindingsCoverage(reg *runner.Registry, typedGrid *runner.Grid, untypedGrid *bootstrap.Grid) error {
	seen := map[bootstrap.BindingKey]struct{}{}
	collect := func(key bootstrap.BindingKey) {
		seen[key] = struct{}{}
	}
	for _, k := range requiredBindingsFromTypedGrid(typedGrid) {
		collect(k)
	}
	untyped, _, _ := requiredBindingsFromUntypedGrid(untypedGrid)
	for _, k := range untyped {
		collect(k)
	}

	var missing []bootstrap.BindingKey
	for k := range seen {
		if _, _, ok := reg.Lookup(k.String()); !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].String() < missing[j].String() })
	return &bootstrap.MissingBindingError{Missing: missing}
}
