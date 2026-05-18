package bootstrap

import (
	"errors"
	"strings"
	"testing"
)

func TestBindingKeyString(t *testing.T) {
	k := BindingKey{Concept: "mutation-score", Language: "rust"}
	if got := k.String(); got != "mutation-score.rust" {
		t.Errorf("String() = %q; want mutation-score.rust", got)
	}
}

func TestDeclareBinding_StoresOnGrid(t *testing.T) {
	g := NewGrid("alice")
	if err := g.DeclareBinding("mutation-score", "rust", "cargo-mutants"); err != nil {
		t.Fatalf("DeclareBinding: %v", err)
	}
	cmd, ok := g.LookupBinding("mutation-score", "rust")
	if !ok {
		t.Fatal("LookupBinding: not found after declare")
	}
	if cmd != "cargo-mutants" {
		t.Errorf("cmd = %q; want cargo-mutants", cmd)
	}
}

func TestDeclareBinding_TrimsWhitespace(t *testing.T) {
	g := NewGrid("alice")
	if err := g.DeclareBinding("  mutation-score  ", " rust ", "  cargo-mutants  "); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.LookupBinding("mutation-score", "rust"); !ok {
		t.Error("LookupBinding should find trimmed key")
	}
	// LookupBinding also trims, so untrimmed args still work.
	if _, ok := g.LookupBinding("  mutation-score  ", "rust"); !ok {
		t.Error("LookupBinding should trim its args")
	}
}

func TestDeclareBinding_RejectsEmpty(t *testing.T) {
	cases := []struct {
		label              string
		concept, lang, cmd string
		wantErr            error
	}{
		{"empty concept", "", "rust", "cargo", ErrBindingConceptEmpty},
		{"whitespace concept", "   ", "rust", "cargo", ErrBindingConceptEmpty},
		{"empty lang", "mutation-score", "", "cargo", ErrBindingLanguageEmpty},
		{"whitespace lang", "mutation-score", "   ", "cargo", ErrBindingLanguageEmpty},
		{"empty cmd", "mutation-score", "rust", "", ErrBindingCommandEmpty},
		{"whitespace cmd", "mutation-score", "rust", "   ", ErrBindingCommandEmpty},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			g := NewGrid("alice")
			err := g.DeclareBinding(c.concept, c.lang, c.cmd)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v; want %v", err, c.wantErr)
			}
		})
	}
}

func TestDeclareBinding_OverwriteAllowed(t *testing.T) {
	// Init re-entry may amend a binding; we allow overwrite rather
	// than forcing the operator through a delete + re-declare flow.
	g := NewGrid("alice")
	_ = g.DeclareBinding("lint-clean", "go", "staticcheck")
	if err := g.DeclareBinding("lint-clean", "go", "staticcheck && go vet"); err != nil {
		t.Fatalf("overwrite should succeed: %v", err)
	}
	cmd, _ := g.LookupBinding("lint-clean", "go")
	if cmd != "staticcheck && go vet" {
		t.Errorf("cmd = %q; want overwritten value", cmd)
	}
}

func TestDeclareBinding_NilReceiver(t *testing.T) {
	var g *Grid
	if err := g.DeclareBinding("x", "y", "z"); err == nil {
		t.Error("nil receiver should error")
	}
}

func TestLookupBinding_NilOrEmpty(t *testing.T) {
	var g *Grid
	if _, ok := g.LookupBinding("x", "y"); ok {
		t.Error("nil grid Lookup should return false")
	}
	g2 := NewGrid("alice")
	if _, ok := g2.LookupBinding("x", "y"); ok {
		t.Error("empty-bindings Lookup should return false")
	}
}

func TestCheckRequiredBindings_NoMissing(t *testing.T) {
	g := NewGrid("alice")
	_ = g.DeclareBinding("lint-clean", "go", "staticcheck")
	_ = g.DeclareBinding("mutation-score", "go", "go-mutesting")
	required := []BindingKey{
		{Concept: "lint-clean", Language: "go"},
		{Concept: "mutation-score", Language: "go"},
	}
	if err := g.CheckRequiredBindings(required); err != nil {
		t.Errorf("CheckRequiredBindings: %v; want nil (all declared)", err)
	}
}

func TestCheckRequiredBindings_EmptyRequired(t *testing.T) {
	g := NewGrid("alice")
	if err := g.CheckRequiredBindings(nil); err != nil {
		t.Errorf("nil required: got %v; want nil", err)
	}
	if err := g.CheckRequiredBindings([]BindingKey{}); err != nil {
		t.Errorf("empty required: got %v; want nil", err)
	}
}

func TestCheckRequiredBindings_OneMissing(t *testing.T) {
	// Scenario 49: a single mutation-score.rust missing.
	g := NewGrid("alice")
	_ = g.DeclareBinding("lint-clean", "go", "staticcheck")
	required := []BindingKey{
		{Concept: "lint-clean", Language: "go"},
		{Concept: "mutation-score", Language: "rust"},
	}
	err := g.CheckRequiredBindings(required)
	if err == nil {
		t.Fatal("expected MissingBindingError; got nil")
	}
	if !errors.Is(err, ErrMissingBinding) {
		t.Errorf("errors.Is ErrMissingBinding: false; want true (err = %v)", err)
	}
	mbe := AsMissingBindingError(err)
	if mbe == nil {
		t.Fatal("AsMissingBindingError returned nil")
	}
	if len(mbe.Missing) != 1 {
		t.Fatalf("len(Missing) = %d; want 1", len(mbe.Missing))
	}
	if mbe.Missing[0].String() != "mutation-score.rust" {
		t.Errorf("missing key = %q; want mutation-score.rust", mbe.Missing[0].String())
	}
}

func TestCheckRequiredBindings_AllMissingCollected(t *testing.T) {
	// Scenario 59: three required, two missing; collect ALL missing
	// (not just first) so init's re-entry can present them together.
	g := NewGrid("alice")
	_ = g.DeclareBinding("lint-clean", "go", "staticcheck")
	required := []BindingKey{
		{Concept: "lint-clean", Language: "go"},
		{Concept: "mutation-score", Language: "rust"},
		{Concept: "tests-pass", Language: "python"},
	}
	err := g.CheckRequiredBindings(required)
	if err == nil {
		t.Fatal("expected MissingBindingError; got nil")
	}
	mbe := AsMissingBindingError(err)
	if mbe == nil {
		t.Fatal("AsMissingBindingError returned nil")
	}
	if len(mbe.Missing) != 2 {
		t.Fatalf("len(Missing) = %d; want 2 (mutation-score.rust + tests-pass.python)", len(mbe.Missing))
	}
	// Sort is lexicographic on "concept.language".
	keys := []string{mbe.Missing[0].String(), mbe.Missing[1].String()}
	want := []string{"mutation-score.rust", "tests-pass.python"}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("Missing[%d] = %q; want %q", i, keys[i], want[i])
		}
	}
	// The error message includes both missing keys.
	msg := err.Error()
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("error message %q should contain %q", msg, w)
		}
	}
}

func TestCheckRequiredBindings_Deduplicates(t *testing.T) {
	g := NewGrid("alice")
	required := []BindingKey{
		{Concept: "lint-clean", Language: "go"},
		{Concept: "lint-clean", Language: "go"}, // duplicate
		{Concept: "mutation-score", Language: "rust"},
	}
	err := g.CheckRequiredBindings(required)
	mbe := AsMissingBindingError(err)
	if mbe == nil {
		t.Fatal("expected MissingBindingError")
	}
	if len(mbe.Missing) != 2 {
		t.Errorf("len(Missing) = %d; want 2 (duplicate collapsed)", len(mbe.Missing))
	}
}

func TestCheckRequiredBindings_ReentryFlow(t *testing.T) {
	// End-to-end: declare → check fails → operator declares → check
	// passes. Mimics the runner-side suspend + re-init + resume flow.
	g := NewGrid("alice")
	required := []BindingKey{
		{Concept: "mutation-score", Language: "rust"},
	}
	err := g.CheckRequiredBindings(required)
	if !errors.Is(err, ErrMissingBinding) {
		t.Fatalf("first check: want missing-binding; got %v", err)
	}
	// Re-entry: operator declares the missing binding.
	mbe := AsMissingBindingError(err)
	for _, k := range mbe.Missing {
		if err := g.DeclareBinding(k.Concept, k.Language, "cargo-mutants"); err != nil {
			t.Fatalf("DeclareBinding during re-entry: %v", err)
		}
	}
	if err := g.CheckRequiredBindings(required); err != nil {
		t.Errorf("second check (after re-entry): %v; want nil", err)
	}
}

func TestAsMissingBindingError_NotMatching(t *testing.T) {
	if got := AsMissingBindingError(nil); got != nil {
		t.Error("AsMissingBindingError(nil) should be nil")
	}
	if got := AsMissingBindingError(errors.New("something else")); got != nil {
		t.Error("AsMissingBindingError of unrelated error should be nil")
	}
}

func TestBindingKeysFromStrings(t *testing.T) {
	got, err := BindingKeysFromStrings([]string{
		"lint-clean.go",
		"mutation-score.rust",
		"tests-pass.python",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d; want 3", len(got))
	}
	if got[0].Concept != "lint-clean" || got[0].Language != "go" {
		t.Errorf("[0] = %+v; want {lint-clean, go}", got[0])
	}
}

func TestBindingKeysFromStrings_Malformed(t *testing.T) {
	cases := []string{
		"no-dot",
		".only-language",
		"only-concept.",
		" . ",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			_, err := BindingKeysFromStrings([]string{c})
			if err == nil {
				t.Errorf("expected error for %q", c)
			}
		})
	}
}
