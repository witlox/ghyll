package bootstrap

import (
	"errors"
	"fmt"
	"math"
	"path"
	"reflect"
	"strings"

	"github.com/witlox/ghyll/catalogue"
)

// ErrModifyWeakening is returned by CheckModification when a proposed
// argument value would weaken (relax) the original. Per ADR-011 / D20:
// init auto-propose's modify path is raise-only — costs go up,
// thresholds tighten, never the reverse.
var ErrModifyWeakening = errors.New("cannot-weaken-default")

// ErrModifyUnsupportedType is returned when the rule for monotonic
// comparison of a particular argument type is not yet implemented.
// Currently supported: int, number, severity. Other types require
// equality (the modification must equal the original, or else extend
// is the correct verdict, or skip-with-residue).
var ErrModifyUnsupportedType = errors.New("modify-type-not-supported")

// ErrModifyNonFinite is returned when proposed numeric value is NaN
// or ±Inf. The raise-only check is undefined on non-finite floats
// (NaN compares false to everything), so such values are rejected
// outright. Maps to validation-pass-1 finding #2.
var ErrModifyNonFinite = errors.New("modify-non-finite-numeric")

// severityRank maps the closed severity enum (gates.md §7.3) to an
// integer where higher = stricter. `unevaluated` ranks at 0 (the
// floor): any concretely-assigned severity is stricter than an
// unevaluated one. This aligns with catalogue.canonicalSeverity which
// includes `unevaluated` (validation-pass-1 finding #1).
var severityRank = map[string]int{
	"unevaluated": 0,
	"info":        1,
	"low":         2,
	"medium":      3,
	"high":        4,
	"critical":    5,
}

// CheckModification verifies that proposed args do not weaken the
// original args of a clause, per the raise-only rule (ADR-011 D20).
//
// For each argument present in both original and proposed:
//
//   - int / number: proposed must be >= original.
//   - severity: proposed's rank must be >= original's rank (higher
//     severity is stricter).
//   - other types: equality is required for v1. To widen or alter,
//     the operator should use "extend" (add a new clause) or "skip"
//     (with residue), not "modify".
//
// Arguments present only in proposed (new keys) are treated as
// "extend" not "modify" and are not rejected by this check.
//
// Returns ErrModifyWeakening wrapped with detail on the first
// weakening detected. Returns ErrModifyUnsupportedType if the
// argument type isn't currently comparable.
func CheckModification(conceptName string, original, proposed map[string]any, cat *catalogue.Catalogue) error {
	if cat == nil {
		return errors.New("CheckModification: catalogue is nil")
	}
	concept, ok := cat.Get(conceptName)
	if !ok {
		return fmt.Errorf("CheckModification: unknown concept %q", conceptName)
	}

	// Semantics: `proposed` is a *diff* of changed arguments — keys
	// absent from proposed mean "no change". This matches init.feature
	// scenarios where the operator specifies only the modified arg
	// (e.g., `modify with {threshold: 0.85}` against an original
	// `mutation-score(threshold=0.7, scope=..., language=...)`).
	//
	// There is no syntax to *remove* an arg via modify; operators
	// who want to drop an arg use "skip" (with residue) instead.
	// (validation-pass-1 finding #19 mis-read the diff semantics
	// as full-replacement.)
	for k, propVal := range proposed {
		argSchema, declared := concept.Arguments[k]
		if !declared {
			return fmt.Errorf("CheckModification: %s: unknown argument %q", conceptName, k)
		}
		origVal, hadOriginal := original[k]
		if !hadOriginal {
			// Net-new arg in proposed — treated as extend semantics,
			// not modify. Skip the weakening check.
			continue
		}
		if err := checkNotWeakening(conceptName, k, argSchema, origVal, propVal); err != nil {
			return err
		}
	}
	return nil
}

// checkNotWeakening enforces the monotonic-modify rule for a single
// argument value.
func checkNotWeakening(conceptName, argName string, schema catalogue.ArgumentSchema, orig, proposed any) error {
	switch schema.Type {
	case "int", "number":
		origF, ok1 := toFloat(orig)
		propF, ok2 := toFloat(proposed)
		if !ok1 || !ok2 {
			return fmt.Errorf("CheckModification: %s.%s: cannot compare non-numeric values (%T vs %T)",
				conceptName, argName, orig, proposed)
		}
		// Reject NaN / ±Inf on either side: comparisons are undefined
		// for NaN (always false), so a NaN proposed value would bypass
		// the raise-only check silently. ±Inf is rejected for symmetry
		// (no concept's range admits Inf as a meaningful threshold).
		// validation-pass-1 finding #2.
		if math.IsNaN(origF) || math.IsNaN(propF) || math.IsInf(origF, 0) || math.IsInf(propF, 0) {
			return fmt.Errorf("%w: %s.%s has non-finite value (orig=%v, proposed=%v)",
				ErrModifyNonFinite, conceptName, argName, orig, proposed)
		}
		if propF < origF {
			return fmt.Errorf("%w: %s.%s lowered from %v to %v",
				ErrModifyWeakening, conceptName, argName, orig, proposed)
		}

	case "severity":
		origS, ok1 := orig.(string)
		propS, ok2 := proposed.(string)
		if !ok1 || !ok2 {
			return fmt.Errorf("CheckModification: %s.%s: severity values must be strings (%T vs %T)",
				conceptName, argName, orig, proposed)
		}
		origRank, knownOrig := severityRank[origS]
		propRank, knownProp := severityRank[propS]
		if !knownOrig || !knownProp {
			return fmt.Errorf("CheckModification: %s.%s: severity outside canonical enum (%q, %q)",
				conceptName, argName, origS, propS)
		}
		if propRank < origRank {
			return fmt.Errorf("%w: lower threshold (%s.%s lowered from %q to %q)",
				ErrModifyWeakening, conceptName, argName, origS, propS)
		}

	case "path-glob":
		// Path-glob narrowing rule: a tighter scope (fewer files
		// allowed to fail) is a raise — accepted. Widening (more
		// files allowed to fail) is a weakening — refused. See
		// init.feature 184 outline: "src/**" → "src/main.go" accepts;
		// the reverse refuses with "wider scope".
		origS, ok1 := orig.(string)
		propS, ok2 := proposed.(string)
		if !ok1 || !ok2 {
			return fmt.Errorf("CheckModification: %s.%s: path-glob values must be strings (%T vs %T)",
				conceptName, argName, orig, proposed)
		}
		if !isPathGlobNarrowing(origS, propS) {
			return fmt.Errorf("%w: wider scope (%s.%s went from %q to %q)",
				ErrModifyWeakening, conceptName, argName, origS, propS)
		}

	case "regex":
		// Regex widening rule: a regex that matches MORE strings is a
		// stricter check (catches more findings) — accepted. A regex
		// that matches FEWER strings is a weakening (fewer markers)
		// — refused. See init.feature 184 outline: "^TODO" →
		// "^TODO|^XXX" accepts; the reverse refuses with "fewer
		// markers".
		origS, ok1 := orig.(string)
		propS, ok2 := proposed.(string)
		if !ok1 || !ok2 {
			return fmt.Errorf("CheckModification: %s.%s: regex values must be strings (%T vs %T)",
				conceptName, argName, orig, proposed)
		}
		if !isRegexWidening(origS, propS) {
			return fmt.Errorf("%w: fewer markers (%s.%s went from %q to %q)",
				ErrModifyWeakening, conceptName, argName, origS, propS)
		}

	default:
		// For other types, equality is required for v1. Anything else
		// (widening a scope, broadening a regex, etc.) needs real
		// monotonicity logic not yet implemented.
		if !equalAny(orig, proposed) {
			return fmt.Errorf("%w: %s.%s type %q (orig=%v, proposed=%v)",
				ErrModifyUnsupportedType, conceptName, argName, schema.Type, orig, proposed)
		}
	}
	return nil
}

// toFloat coerces a numeric val to float64. Returns (0, false) if not
// numeric. Mirrors catalogue.toFloat (kept package-local to avoid
// adding to catalogue's public API).
func toFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// isPathGlobNarrowing reports whether `proposed` is a narrower (or
// equal) path-glob than `orig`. Narrowing = the set of paths matched
// by `proposed` is a subset of those matched by `orig`. For the
// init.feature 184 outline:
//
//   - "src/**"     → "src/main.go" : narrowing (literal is matched by orig)
//   - "src/main.go" → "src/**"      : widening   (refused)
//
// Implementation: equality is always narrowing. A literal proposed
// (no wildcard characters) is narrowing if the original glob would
// match it (using a recursive matcher that handles ** as a
// multi-segment wildcard). Other combinations require equality —
// glob-vs-glob subset detection is the operator's job to encode via
// extend rather than guess at heuristically.
func isPathGlobNarrowing(orig, proposed string) bool {
	if orig == proposed {
		return true
	}
	if isPathGlobLiteral(proposed) {
		return pathGlobMatchRecursive(orig, proposed)
	}
	return false
}

// isPathGlobLiteral reports whether s contains no path-glob wildcard
// characters. A literal can be tested as a concrete path against
// another glob.
func isPathGlobLiteral(s string) bool {
	return !strings.ContainsAny(s, "*?[")
}

// pathGlobMatchRecursive reports whether pattern matches name with
// `**` as a multi-segment wildcard (matches zero or more path
// segments).
//
// path.Match alone doesn't handle `**`; we expand `**/` segments by
// trying zero-or-more replacements. The number of `**` tokens in
// realistic globs is small, so the recursion depth stays bounded.
func pathGlobMatchRecursive(pattern, name string) bool {
	// Fast path: no doublestar — defer to path.Match.
	if !strings.Contains(pattern, "**") {
		ok, err := path.Match(pattern, name)
		return err == nil && ok
	}
	// Split on the first ** and try matching zero or more path
	// segments in its place.
	idx := strings.Index(pattern, "**")
	prefix := pattern[:idx]
	suffix := pattern[idx+2:]
	// Strip a trailing slash on prefix and a leading slash on suffix
	// so we can paste them back with an explicit "" or "<segments>/".
	prefix = strings.TrimSuffix(prefix, "/")
	suffix = strings.TrimPrefix(suffix, "/")
	// Try each substitution: empty (zero segments) and each prefix of
	// the segments in `name` not yet consumed by `prefix`.
	// For simplicity, enumerate by splitting `name` by `/` and trying
	// every split point.
	segments := strings.Split(name, "/")
	for i := 0; i <= len(segments); i++ {
		for j := i; j <= len(segments); j++ {
			var middle string
			if j > i {
				middle = strings.Join(segments[i:j], "/")
			}
			// Reconstruct the candidate that pattern would match with
			// "**" expanded to middle.
			parts := []string{}
			if prefix != "" {
				parts = append(parts, prefix)
			}
			if middle != "" {
				parts = append(parts, middle)
			}
			if suffix != "" {
				parts = append(parts, suffix)
			}
			candidate := strings.Join(parts, "/")
			// path.Match on the candidate vs name (which may contain
			// other wildcards from prefix/suffix).
			ok, err := path.Match(candidate, name)
			if err == nil && ok {
				return true
			}
		}
	}
	return false
}

// isRegexWidening reports whether `proposed` matches at least as many
// strings as `orig`. Used as the accept-direction for the regex
// modify rule: more strings caught = stricter check.
//
// We can't decide regex subset in general, but the cases ADR-011's
// modify-non-monotonic outline exercises are alternation-based:
//
//   - "^TODO"        → "^TODO|^XXX" : widening (more alternations)
//   - "^TODO|^XXX"   → "^TODO"       : narrowing (refused)
//
// Heuristic: split each side on `|`, compare alternation sets. The
// proposed widens the original iff proposed's set ⊇ original's set.
// Identical strings widen trivially.
func isRegexWidening(orig, proposed string) bool {
	if orig == proposed {
		return true
	}
	origAlts := splitTrim(orig, "|")
	propAlts := splitTrim(proposed, "|")
	have := make(map[string]struct{}, len(propAlts))
	for _, a := range propAlts {
		have[a] = struct{}{}
	}
	for _, a := range origAlts {
		if _, ok := have[a]; !ok {
			return false
		}
	}
	return true
}

// splitTrim splits s by sep and trims whitespace from each piece.
// Empty pieces (from leading/trailing separators) are dropped.
func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// equalAny reports whether two values are deeply equal for the
// unsupported-type fallback. Uses reflect.DeepEqual for maps / slices
// (so identical list-args round-trip without triggering
// ErrModifyUnsupportedType per validation-pass-1 finding #18).
// Numeric values compare via float coercion so int vs float64 of the
// same magnitude are considered equal.
func equalAny(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// Numeric fast-path: coerce both to float64 and compare.
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	// Deep equality for everything else (strings, bools, maps, slices).
	return reflect.DeepEqual(a, b)
}
