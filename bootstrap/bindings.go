package bootstrap

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Language bindings. Per ADR-005 + D18: the harness ships NO language
// bindings. Each project declares them at init time
// (lint-clean.go = "staticcheck && go vet", mutation-score.rust =
// "cargo-mutants", ...). If a needed binding is absent at any later
// point, the harness suspends the current pass and re-enters init
// scoped to the missing binding(s).
//
// Bindings are stored on the grid as a flat map keyed by
// "concept.language". The helpers here keep callers (the runner, init
// re-entry flow, BDD steps) from poking the map directly so:
//
//   - Concept and language strings get normalized consistently.
//   - Empty values can't be silently declared.
//   - Missing-binding detection always returns ALL missing bindings,
//     never just the first (init.feature scenario 59 requires it).

// BindingKey is one (concept, language) pair that some clause
// instance needs a binding for. Carried in MissingBindingError and
// in the runner's required-binding manifest.
type BindingKey struct {
	Concept  string
	Language string
}

// String returns "concept.language" — the canonical key used on the
// grid's LanguageBindings map.
func (k BindingKey) String() string {
	return k.Concept + "." + k.Language
}

// Binding errors.
//
// ErrMissingBinding is the sentinel callers compare against; the
// concrete error returned by CheckRequiredBindings is a
// *MissingBindingError so callers can inspect the full Missing list.
var (
	ErrMissingBinding       = errors.New("missing-binding")
	ErrBindingConceptEmpty  = errors.New("binding-concept-empty")
	ErrBindingLanguageEmpty = errors.New("binding-language-empty")
	ErrBindingCommandEmpty  = errors.New("binding-command-empty")
)

// MissingBindingError is returned by CheckRequiredBindings when at
// least one required binding is undeclared. Missing is the full,
// deduplicated, lexicographically sorted list — init's re-entry flow
// reads it and presents every gap in a single operator interaction
// (scenario 59).
type MissingBindingError struct {
	Missing []BindingKey
}

// Error returns "missing-binding: <key>, <key>, ...". The sentinel
// substring is the suspend reason scenarios 49 + 59 assert against.
func (e *MissingBindingError) Error() string {
	if e == nil || len(e.Missing) == 0 {
		return ErrMissingBinding.Error()
	}
	parts := make([]string, len(e.Missing))
	for i, k := range e.Missing {
		parts[i] = k.String()
	}
	return ErrMissingBinding.Error() + ": " + strings.Join(parts, ", ")
}

// Is supports errors.Is(err, ErrMissingBinding) so callers can match
// the suspend reason without depending on the concrete type.
func (e *MissingBindingError) Is(target error) bool {
	return target == ErrMissingBinding
}

// DeclareBinding adds a (concept, language) → command mapping to the
// grid. Refuses empty concept / language / command (silent no-op
// bindings would defeat the purpose).
//
// Re-declaring an existing binding overwrites the command. This is
// intentional: init's re-entry flow may amend an earlier declaration
// (the binding was wrong, not just missing). Future iterations may
// require an explicit Overwrite step; for now the bias is toward
// flexibility during re-entry.
func (g *Grid) DeclareBinding(concept, language, command string) error {
	if g == nil {
		return errors.New("DeclareBinding: nil Grid")
	}
	concept = strings.TrimSpace(concept)
	language = strings.TrimSpace(language)
	command = strings.TrimSpace(command)
	if concept == "" {
		return ErrBindingConceptEmpty
	}
	if language == "" {
		return ErrBindingLanguageEmpty
	}
	if command == "" {
		return ErrBindingCommandEmpty
	}
	if g.LanguageBindings == nil {
		g.LanguageBindings = make(map[string]string)
	}
	key := BindingKey{Concept: concept, Language: language}.String()
	g.LanguageBindings[key] = command
	return nil
}

// LookupBinding returns the command for the (concept, language)
// pair, and a boolean reporting whether the binding is declared.
//
// Concept and language are not normalized further — callers must
// pass the same form they used to declare. Both DeclareBinding and
// LookupBinding apply only TrimSpace; mixed case is significant.
func (g *Grid) LookupBinding(concept, language string) (string, bool) {
	if g == nil || g.LanguageBindings == nil {
		return "", false
	}
	key := BindingKey{
		Concept:  strings.TrimSpace(concept),
		Language: strings.TrimSpace(language),
	}.String()
	cmd, ok := g.LanguageBindings[key]
	return cmd, ok
}

// CheckRequiredBindings returns nil if every required (concept,
// language) pair has a declared binding on the grid; otherwise
// returns a *MissingBindingError listing every missing binding.
//
// Always returns the full set of missing bindings (deduplicated,
// lexicographically sorted), not just the first — scenario 59:
//
//	"init collects all missing bindings and presents them together
//	 for operator declaration in a single re-entry"
//
// Callers detect the suspend signal via errors.Is(err, ErrMissingBinding)
// and inspect the concrete error to enumerate the gaps.
func (g *Grid) CheckRequiredBindings(required []BindingKey) error {
	if len(required) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(required))
	var missing []BindingKey
	for _, k := range required {
		// Normalize before dedup so "go" vs " go " don't both appear.
		key := BindingKey{
			Concept:  strings.TrimSpace(k.Concept),
			Language: strings.TrimSpace(k.Language),
		}
		dedupKey := key.String()
		if _, dup := seen[dedupKey]; dup {
			continue
		}
		seen[dedupKey] = struct{}{}
		if _, ok := g.LookupBinding(key.Concept, key.Language); !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Slice(missing, func(i, j int) bool {
		return missing[i].String() < missing[j].String()
	})
	return &MissingBindingError{Missing: missing}
}

// AsMissingBindingError unwraps an error to its concrete
// *MissingBindingError, or returns nil if err is not one. Provided
// so callers don't repeat the type assertion at every call site.
func AsMissingBindingError(err error) *MissingBindingError {
	var mbe *MissingBindingError
	if errors.As(err, &mbe) {
		return mbe
	}
	return nil
}

// BindingKeysFromStrings parses "concept.language" strings into
// BindingKey values. Used by tests and by the operator-facing prompt
// that presents missing bindings ("declare these: mutation-score.rust,
// lint-clean.go").
//
// Returns an error on the first malformed string (no dot, empty
// concept, empty language).
func BindingKeysFromStrings(keys []string) ([]BindingKey, error) {
	out := make([]BindingKey, 0, len(keys))
	for _, s := range keys {
		dot := strings.Index(s, ".")
		if dot < 0 {
			return nil, fmt.Errorf("binding key %q: expected \"concept.language\"", s)
		}
		concept := strings.TrimSpace(s[:dot])
		language := strings.TrimSpace(s[dot+1:])
		if concept == "" {
			return nil, fmt.Errorf("binding key %q: empty concept", s)
		}
		if language == "" {
			return nil, fmt.Errorf("binding key %q: empty language", s)
		}
		out = append(out, BindingKey{Concept: concept, Language: language})
	}
	return out, nil
}
