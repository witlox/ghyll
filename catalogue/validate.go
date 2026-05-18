package catalogue

import (
	"fmt"
	"strings"
)

// canonicalSeverity is the closed severity enum per gates.md §7.3 + D33.
// `unevaluated` is allowed as a finding's severity when assignment was
// depth-below-required; the catalogue accepts it as a valid value for
// severity-typed arguments that may carry it.
var canonicalSeverity = map[string]struct{}{
	"info":        {},
	"low":         {},
	"medium":      {},
	"high":        {},
	"critical":    {},
	"unevaluated": {},
}

// canonicalFindingStatus is the closed finding-status enum per
// gates.md §7.3 + D33 + D42.
var canonicalFindingStatus = map[string]struct{}{
	"open":          {},
	"running":       {},
	"resolved":      {},
	"accepted-risk": {},
	"unevaluated":   {},
}

// Validate checks that args match the named concept's schema. On
// success it returns a normalized arg map (defaults applied for absent
// optional arguments). On failure it returns the first error
// encountered.
//
// Validation steps:
//
//  1. Concept exists in the catalogue.
//  2. No unknown arguments (every key in args is declared in the
//     concept's schema).
//  3. Every required argument is present.
//  4. Each argument's value is type-compatible with its declared type.
//  5. Numeric range constraints (where declared) are satisfied.
//  6. Enum constraints (where declared) are satisfied.
//
// Validate does NOT evaluate the argument (e.g., glob expansion, file
// existence, command resolution). Those are evaluator responsibilities.
func (c *Catalogue) Validate(name string, args map[string]any) (map[string]any, error) {
	concept, ok := c.Get(name)
	if !ok {
		return nil, fmt.Errorf("validate: unknown concept %q", name)
	}

	// Check no unknown arguments.
	for k := range args {
		if _, declared := concept.Arguments[k]; !declared {
			return nil, fmt.Errorf("validate: %s: unknown argument %q", name, k)
		}
	}

	// Build normalized output. Apply defaults for absent optional args.
	out := make(map[string]any, len(concept.Arguments))
	for argName, schema := range concept.Arguments {
		val, present := args[argName]
		if !present {
			if schema.Required {
				return nil, fmt.Errorf("validate: %s: missing required argument %q", name, argName)
			}
			if schema.Default != nil {
				out[argName] = schema.Default
			}
			continue
		}
		if err := checkType(name, argName, schema, val); err != nil {
			return nil, err
		}
		out[argName] = val
	}

	return out, nil
}

// checkType verifies a single argument value matches its declared type
// and any type-specific constraints (range, enum members).
func checkType(conceptName, argName string, schema ArgumentSchema, val any) error {
	switch schema.Type {
	case "string", "path-glob", "artifact-ref", "language-id",
		"role-id", "bounded-context-id", "pass-id", "arrow-id",
		"dependency-id", "command", "duration", "enum-or-path":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("validate: %s.%s: type %q requires string, got %T", conceptName, argName, schema.Type, val)
		}

	case "int":
		if !isInt(val) {
			return fmt.Errorf("validate: %s.%s: type int requires integer, got %T", conceptName, argName, val)
		}
		return checkNumericRange(conceptName, argName, schema, val)

	case "number":
		if !isNumeric(val) {
			return fmt.Errorf("validate: %s.%s: type number requires numeric, got %T", conceptName, argName, val)
		}
		return checkNumericRange(conceptName, argName, schema, val)

	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("validate: %s.%s: type boolean requires bool, got %T", conceptName, argName, val)
		}

	case "list":
		if _, ok := val.([]any); !ok {
			// yaml.v3 may unmarshal lists as []interface{}; tolerate
			// other slice shapes by checking reflectively in future.
			return fmt.Errorf("validate: %s.%s: type list requires array, got %T", conceptName, argName, val)
		}
		// Item-level validation is out of scope for v1; the items
		// schema (when declared) is informational. A future enhancement
		// can recursively validate each item.

	case "severity":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("validate: %s.%s: type severity requires string, got %T", conceptName, argName, val)
		}
		if _, ok := canonicalSeverity[s]; !ok {
			return fmt.Errorf("validate: %s.%s: severity %q is not in canonical enum (info|low|medium|high|critical|unevaluated)", conceptName, argName, s)
		}

	case "finding-status":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("validate: %s.%s: type finding-status requires string, got %T", conceptName, argName, val)
		}
		if _, ok := canonicalFindingStatus[s]; !ok {
			return fmt.Errorf("validate: %s.%s: finding-status %q is not in canonical enum", conceptName, argName, s)
		}

	case "depth-tier":
		if !isInt(val) {
			return fmt.Errorf("validate: %s.%s: type depth-tier requires int 0..3, got %T", conceptName, argName, val)
		}
		n := toInt(val)
		if n < 0 || n > 3 {
			return fmt.Errorf("validate: %s.%s: depth-tier must be in [0,3], got %d", conceptName, argName, n)
		}

	case "enum":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("validate: %s.%s: type enum requires string, got %T", conceptName, argName, val)
		}
		if len(schema.Values) == 0 {
			return fmt.Errorf("validate: %s.%s: type enum declared without values list (schema bug)", conceptName, argName)
		}
		found := false
		for _, v := range schema.Values {
			if v == s {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("validate: %s.%s: %q not in enum [%s]", conceptName, argName, s, strings.Join(schema.Values, ", "))
		}

	case "int-or-range":
		// Either an int (exact) or a 2-element list of ints (range).
		if isInt(val) {
			return nil
		}
		if list, ok := val.([]any); ok && len(list) == 2 && isInt(list[0]) && isInt(list[1]) {
			return nil
		}
		return fmt.Errorf("validate: %s.%s: int-or-range requires int or [min, max], got %T", conceptName, argName, val)

	default:
		// Unknown / not-yet-implemented types accept any value at
		// the catalogue boundary. The evaluator may apply additional
		// validation when it processes the argument.
	}

	return nil
}

// checkNumericRange validates Range constraint if declared on the schema.
func checkNumericRange(conceptName, argName string, schema ArgumentSchema, val any) error {
	if len(schema.Range) != 2 {
		return nil
	}
	min, minOK := toFloat(schema.Range[0])
	max, maxOK := toFloat(schema.Range[1])
	if !minOK || !maxOK {
		// Range bounds are not numeric (e.g., the YAML used `~` for
		// unbounded). For v1, skip the check when bounds aren't
		// numeric; future versions can interpret unbounded.
		return nil
	}
	v, ok := toFloat(val)
	if !ok {
		return fmt.Errorf("validate: %s.%s: cannot range-check non-numeric value %T", conceptName, argName, val)
	}
	if v < min || v > max {
		return fmt.Errorf("validate: %s.%s: value %v outside range [%v, %v]", conceptName, argName, v, min, max)
	}
	return nil
}

// isInt reports whether val is an integer (Go's int or YAML-decoded int).
// yaml.v3 decodes integers as int (or int64 on 32-bit). Floats are NOT ints.
func isInt(val any) bool {
	switch val.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// isNumeric reports whether val is int-like or float-like.
func isNumeric(val any) bool {
	if isInt(val) {
		return true
	}
	switch val.(type) {
	case float32, float64:
		return true
	}
	return false
}

// toInt coerces a numeric val to int. Returns 0 if not numeric.
func toInt(val any) int {
	switch v := val.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	}
	return 0
}

// toFloat coerces a numeric val to float64. Returns (0, false) if not numeric.
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
