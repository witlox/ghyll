package bootstrap

import (
	"errors"
	"fmt"

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

// severityRank maps the closed severity enum (gates.md §7.3) to an
// integer where higher = stricter.
var severityRank = map[string]int{
	"info":     1,
	"low":      2,
	"medium":   3,
	"high":     4,
	"critical": 5,
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
			return fmt.Errorf("%w: %s.%s lowered from %q to %q",
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

// equalAny is a basic equality check for the unsupported-type fallback.
// It uses Go's == when types are directly comparable, and bytewise
// string comparison for strings. For deeper comparison (maps, slices),
// it returns false (forcing "modify-type-not-supported").
func equalAny(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64:
		af, aok := toFloat(a)
		bf, bok := toFloat(b)
		return aok && bok && af == bf
	}
	return false
}
