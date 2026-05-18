package catalogue

// Concept is the in-memory representation of one catalogue concept,
// loaded from gates/concepts/<name>.yaml. The struct shape matches the
// YAML schema specified in ADR-006.
type Concept struct {
	Name          string                    `yaml:"concept"`
	Description   string                    `yaml:"description"`
	LanguageBound bool                      `yaml:"language-bound"`
	Arguments     map[string]ArgumentSchema `yaml:"arguments"`
	Evaluator     EvaluatorContract         `yaml:"evaluator"`
	DefaultCost   int                       `yaml:"default-cost"`
	EdgeCases     []string                  `yaml:"edge-cases"`
}

// ArgumentSchema is the typed specification of one argument in a
// catalogue concept. Captures the YAML shape per ADR-006.
//
// Type names follow the catalogue's common-types vocabulary:
// path-glob, artifact-ref, language-id, severity, depth-tier, role-id,
// bounded-context-id, pass-id, arrow-id, dependency-id, finding-status,
// plus primitives (string, int, number, boolean, list, command,
// duration, enum, enum-or-path, int-or-range).
type ArgumentSchema struct {
	// Type is the argument's declared type. Required.
	Type string `yaml:"type"`

	// Required indicates whether the argument must be supplied.
	Required bool `yaml:"required"`

	// Default is the value applied when the argument is absent and
	// Required is false. Kept as any to accept YAML's loose typing
	// (string, int, float, bool, list, map). Nil if no default declared.
	Default any `yaml:"default,omitempty"`

	// Description is the human-facing one-line description.
	Description string `yaml:"description"`

	// Range, if present, declares numeric bounds for int/number types.
	// Format: [min, max], inclusive. Either bound may be tilde (~) for
	// unbounded in YAML; here represented as untyped slice.
	Range []any `yaml:"range,omitempty"`

	// Values, if present, declares the allowed enum members for type=enum.
	Values []string `yaml:"values,omitempty"`

	// Items, if present, declares the element schema for type=list.
	// May be a type-name string (e.g., "string") or a nested object
	// (e.g., { type: list, items: path }). Kept as any to accept both
	// shapes per the schema YAMLs.
	Items any `yaml:"items,omitempty"`
}

// EvaluatorContract describes how a concept's evaluator behaves at
// runtime. Currently every concept's contract is "machine" (the harness
// or build/test tooling evaluates deterministically); the Produces map
// is free-form per-concept output shape.
type EvaluatorContract struct {
	Contract string         `yaml:"contract"`
	Produces map[string]any `yaml:"produces"`
}
