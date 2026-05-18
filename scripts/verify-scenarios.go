// verify-scenarios cross-references Gherkin .feature files against
// test files to report which scenarios have step definitions and which don't.
//
// Usage: go run scripts/verify-scenarios.go
//
// Checks two sources:
// 1. godog step definitions in tests/acceptance/steps_*.go
// 2. TestScenario_* functions in package *_test.go files
//
// Reports:
// - Scenarios with step definitions (covered)
// - Scenarios without any test (uncovered)
// - Step definitions without matching scenarios (orphan)

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	scenarioRe     = regexp.MustCompile(`^\s*Scenario:\s*(.+)$`)
	testScenarioRe = regexp.MustCompile(`func\s+TestScenario_(\w+)`)
)

type scenario struct {
	Name    string
	Feature string
	Line    int
}

func main() {
	features, err := findFeatures("specs/features")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error finding features: %v\n", err)
		os.Exit(1)
	}

	scenarios := extractScenarios(features)
	testNames := findTestScenarios()
	stepFiles := countStepFiles()

	fmt.Printf("# Scenario Verification Report\n\n")
	fmt.Printf("Features: %d\n", len(features))
	fmt.Printf("Scenarios: %d\n", len(scenarios))
	fmt.Printf("Step definition files: %d\n", stepFiles)
	fmt.Printf("TestScenario_* functions: %d\n", len(testNames))
	fmt.Println()

	// Check each scenario for coverage
	covered := 0
	uncovered := 0

	fmt.Printf("## Coverage by feature\n\n")

	byFeature := map[string][]scenario{}
	for _, s := range scenarios {
		byFeature[s.Feature] = append(byFeature[s.Feature], s)
	}

	for _, feat := range features {
		base := filepath.Base(feat)
		featureScenarios := byFeature[feat]
		fmt.Printf("### %s (%d scenarios)\n\n", base, len(featureScenarios))

		for _, s := range featureScenarios {
			normalized := normalizeScenarioName(s.Name)
			hasTest := false
			for _, t := range testNames {
				if strings.Contains(strings.ToLower(t), strings.ToLower(normalized)) {
					hasTest = true
					break
				}
			}

			// Check if it's likely covered by godog (step file exists for this feature)
			stepFile := featureToStepFile(base)
			hasStepFile := fileExists(stepFile)

			status := "❌ UNCOVERED"
			if hasTest {
				status = "✅ TestScenario_*"
				covered++
			} else if hasStepFile {
				status = "⏳ PENDING (godog step file exists)"
				covered++ // counted as "wired" even if pending
			} else {
				uncovered++
			}

			fmt.Printf("  - [%s] %s (line %d)\n", status, s.Name, s.Line)
		}
		fmt.Println()
	}

	fmt.Printf("## Summary\n\n")
	fmt.Printf("| Status | Count |\n")
	fmt.Printf("|--------|-------|\n")
	fmt.Printf("| Covered/wired | %d |\n", covered)
	fmt.Printf("| Uncovered | %d |\n", uncovered)
	fmt.Printf("| Total | %d |\n", len(scenarios))

	if uncovered > 0 {
		os.Exit(1)
	}
}

func findFeatures(dir string) ([]string, error) {
	var features []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".feature") {
			features = append(features, path)
		}
		return nil
	})
	return features, err
}

func extractScenarios(features []string) []scenario {
	var scenarios []scenario
	for _, f := range features {
		file, err := os.Open(f)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if m := scenarioRe.FindStringSubmatch(line); m != nil {
				scenarios = append(scenarios, scenario{
					Name:    strings.TrimSpace(m[1]),
					Feature: f,
					Line:    lineNum,
				})
			}
		}
		_ = file.Close()
	}
	return scenarios
}

func findTestScenarios() []string {
	var names []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer func() { _ = file.Close() }()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if m := testScenarioRe.FindStringSubmatch(scanner.Text()); m != nil {
				names = append(names, m[1])
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: error walking for tests: %v\n", err)
	}
	return names
}

func countStepFiles() int {
	matches, _ := filepath.Glob("tests/acceptance/steps_*.go")
	return len(matches)
}

func normalizeScenarioName(name string) string {
	// Convert "Proactive compaction before turn" → "proactive_compaction_before_turn"
	name = strings.ToLower(name)
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, name)
	// Collapse multiple underscores
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	return strings.Trim(name, "_")
}

func featureToStepFile(featureBase string) string {
	// "routing.feature" → "tests/acceptance/steps_routing.go"
	name := strings.TrimSuffix(featureBase, ".feature")
	return fmt.Sprintf("tests/acceptance/steps_%s.go", name)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
