package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/witlox/ghyll/bootstrap"
)

// registerOrphanSteps wires step definitions for init.feature scenario
// 41 (Init runs orphan-symbol extraction during brownfield discovery).
//
// The flow: a brownfield init with declared contexts walks each
// context's source, extracts exported symbols per language, and
// classifies any symbol not referenced by the specs as a residue
// candidate for operator triage.
func registerOrphanSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	ctx.Step(`^a brownfield init with declared bounded contexts$`, state.aBrownfieldInitWithDeclaredBoundedContexts)
	ctx.Step(`^init walks each context's source$`, state.initWalksEachContextsSource)
	ctx.Step(`^init extracts the exported-symbol list per language$`, state.initExtractsExportedSymbolListPerLanguage)
	ctx.Step(`^presents orphans \(symbols with no clear spec mapping\) as residue candidates for operator triage$`, state.presentsOrphansAsResidueCandidates)

	ctx.After(func(_ context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		state.ExtractedSymbols = nil
		state.ExtractedOrphans = nil
		return nil, nil
	})
}

// aBrownfieldInitWithDeclaredBoundedContexts sets up a brownfield
// project dir with two contexts containing Go source. The profile is
// built via ProfileRepo and stashed for the When/Then steps.
func (s *ScenarioState) aBrownfieldInitWithDeclaredBoundedContexts() error {
	if err := s.ensureProjectTestDir(); err != nil {
		return err
	}
	// Two contexts; one with an exported symbol that has a spec
	// reference, one with an exported symbol that does NOT — the
	// classifier should flag only the second.
	for _, c := range []string{"contextA", "contextB"} {
		dir := filepath.Join(s.ProjectTestDir, "src", c)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		body := fmt.Sprintf(`package %s
func ExportedIn%s() {}
type ExportedTypeIn%s struct{}
`, c, c, c)
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o644); err != nil {
			return fmt.Errorf("write source: %w", err)
		}
	}
	// Provide a specs/ directory that references one of the symbols
	// so we have at least one non-orphan AND at least one orphan.
	specsDir := filepath.Join(s.ProjectTestDir, "specs")
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return fmt.Errorf("mkdir specs: %w", err)
	}
	if err := os.WriteFile(filepath.Join(specsDir, "arch.md"),
		[]byte("ExportedIncontextA handles requests for contextA.\n"),
		0o644); err != nil {
		return fmt.Errorf("write spec: %w", err)
	}
	// Build a brownfield profile via ProfileRepo (NOT a synthetic
	// profile — exercise the same path init's brownfield discovery
	// would).
	profile, err := bootstrap.ProfileRepo(s.ProjectTestDir)
	if err != nil {
		return fmt.Errorf("ProfileRepo: %w", err)
	}
	if !profile.IsBrownfield() {
		return fmt.Errorf("expected brownfield; got %q", profile.Mode)
	}
	if len(profile.BoundedContexts) != 2 {
		return fmt.Errorf("profile contexts = %d; want 2", len(profile.BoundedContexts))
	}
	s.Profile = profile
	return nil
}

// initWalksEachContextsSource calls ExtractContextSymbols for every
// declared context, accumulating the result. Returns the first error.
func (s *ScenarioState) initWalksEachContextsSource() error {
	if s.Profile == nil {
		return errors.New("no profile; brownfield-init step must run first")
	}
	var all []bootstrap.ExportedSymbol
	for _, c := range s.Profile.BoundedContexts {
		symbols, err := bootstrap.ExtractContextSymbols(s.ProjectTestDir, c.ID)
		if err != nil {
			return fmt.Errorf("ExtractContextSymbols(%s): %w", c.ID, err)
		}
		all = append(all, symbols...)
	}
	s.ExtractedSymbols = all
	return nil
}

// initExtractsExportedSymbolListPerLanguage verifies the extraction
// returned at least one symbol per context, all tagged with the
// expected language id.
func (s *ScenarioState) initExtractsExportedSymbolListPerLanguage() error {
	if len(s.ExtractedSymbols) == 0 {
		return errors.New("no symbols extracted")
	}
	// Group by (context, language) to verify per-language tagging.
	seen := make(map[string]int)
	for _, sym := range s.ExtractedSymbols {
		if sym.Language == "" {
			return fmt.Errorf("symbol %q has no Language", sym.Name)
		}
		if sym.Context == "" {
			return fmt.Errorf("symbol %q has no Context", sym.Name)
		}
		seen[sym.Context+":"+sym.Language]++
	}
	// We seeded 2 contexts × 2 exported entities each in Go.
	if got := seen["contextA:go"]; got < 2 {
		return fmt.Errorf("contextA:go symbol count = %d; want >= 2", got)
	}
	if got := seen["contextB:go"]; got < 2 {
		return fmt.Errorf("contextB:go symbol count = %d; want >= 2", got)
	}
	return nil
}

// presentsOrphansAsResidueCandidates runs ClassifyOrphans against the
// project's specs/ directory and verifies at least one symbol was
// flagged. (The setup spec references one symbol; the rest should
// surface as orphan candidates.)
func (s *ScenarioState) presentsOrphansAsResidueCandidates() error {
	specsDir := filepath.Join(s.ProjectTestDir, "specs")
	orphans, err := bootstrap.ClassifyOrphans(s.ExtractedSymbols, specsDir)
	if err != nil {
		return fmt.Errorf("ClassifyOrphans: %w", err)
	}
	if len(orphans) == 0 {
		return errors.New("expected at least one orphan candidate; got none")
	}
	// Every candidate must have a non-empty Reason (operator-facing
	// text).
	for _, o := range orphans {
		if o.Reason == "" {
			return fmt.Errorf("orphan %q has empty Reason", o.Symbol.Name)
		}
	}
	s.ExtractedOrphans = orphans
	return nil
}
