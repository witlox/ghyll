package acceptance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/tool"
)

func registerGlobSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	// resolvePath maps feature file's hardcoded paths to real tmpDir
	resolvePath := func(p string) string {
		for _, prefix := range []string{"/tmp/ghyll-test-glob/", "/tmp/ghyll-test-edit/"} {
			if strings.HasPrefix(p, prefix) {
				return filepath.Join(state.TmpDir, strings.TrimPrefix(p, prefix))
			}
		}
		trimmed := strings.TrimSuffix(p, "/")
		for _, exact := range []string{"/tmp/ghyll-test-glob", "/tmp/ghyll-test-edit"} {
			if trimmed == exact {
				return state.TmpDir
			}
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(state.TmpDir, p)
	}

	// NOTE: "a workspace directory", "the tool result indicates error", and
	// "the tool timeout is" are registered in steps_edit.go (shared steps).
	// They use state.ToolResult, state.TmpDir, state.ToolTimeout.

	ctx.Step(`^the following file structure:$`, func(table *godog.Table) error {
		for _, row := range table.Rows[1:] { // skip header
			relPath := row.Cells[0].Value
			absPath := filepath.Join(state.TmpDir, relPath)
			if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
				return err
			}
			content := fmt.Sprintf("// %s\npackage placeholder\n", relPath)
			if strings.HasSuffix(relPath, ".md") {
				content = fmt.Sprintf("# %s\n", relPath)
			}
			if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
				return err
			}
		}
		return nil
	})

	ctx.Step(`^I call glob with pattern "([^"]*)" path "([^"]*)"$`, func(pattern, path string) error {
		absPath := resolvePath(path)
		state.ToolResult = tool.Glob(context.Background(), pattern, absPath, state.ToolTimeout)
		return nil
	})

	ctx.Step(`^the result contains (\d+) paths$`, func(expected int) error {
		if expected == 0 {
			if state.ToolResult.Output != "" && state.ToolResult.Error == "" {
				return fmt.Errorf("expected 0 paths, got: %s", state.ToolResult.Output)
			}
			return nil
		}
		if state.ToolResult.Error != "" {
			return fmt.Errorf("unexpected error: %s", state.ToolResult.Error)
		}
		lines := strings.Split(strings.TrimSpace(state.ToolResult.Output), "\n")
		var paths []string
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				paths = append(paths, l)
			}
		}
		if len(paths) != expected {
			return fmt.Errorf("expected %d paths, got %d: %v", expected, len(paths), paths)
		}
		return nil
	})

	ctx.Step(`^the result includes "([^"]*)"$`, func(path string) error {
		if !strings.Contains(state.ToolResult.Output, path) {
			return fmt.Errorf("result does not include %q, output: %s", path, state.ToolResult.Output)
		}
		return nil
	})

	ctx.Step(`^the result does not include "([^"]*)"$`, func(path string) error {
		if strings.Contains(state.ToolResult.Output, path) {
			return fmt.Errorf("result unexpectedly includes %q", path)
		}
		return nil
	})

	ctx.Step(`^"([^"]*)" was modified more recently than "([^"]*)"$`, func(newer, older string) error {
		olderPath := filepath.Join(state.TmpDir, older)
		newerPath := filepath.Join(state.TmpDir, newer)
		past := time.Now().Add(-10 * time.Second)
		if err := os.Chtimes(olderPath, past, past); err != nil {
			return fmt.Errorf("chtimes on %s: %v", older, err)
		}
		now := time.Now()
		if err := os.Chtimes(newerPath, now, now); err != nil {
			return fmt.Errorf("chtimes on %s: %v", newer, err)
		}
		return nil
	})

	ctx.Step(`^"([^"]*)" appears before "([^"]*)" in the result$`, func(first, second string) error {
		idx1 := strings.Index(state.ToolResult.Output, first)
		idx2 := strings.Index(state.ToolResult.Output, second)
		if idx1 == -1 {
			return fmt.Errorf("%q not found in result", first)
		}
		if idx2 == -1 {
			return fmt.Errorf("%q not found in result", second)
		}
		if idx1 >= idx2 {
			return fmt.Errorf("%q (at %d) does not appear before %q (at %d) in: %s", first, idx1, second, idx2, state.ToolResult.Output)
		}
		return nil
	})

	ctx.Step(`^a symlink "([^"]*)" pointing to "([^"]*)"$`, func(link, target string) error {
		absLink := filepath.Join(state.TmpDir, link)
		absTarget := target
		if strings.HasPrefix(target, "/tmp/ghyll-test-glob/") {
			absTarget = filepath.Join(state.TmpDir, strings.TrimPrefix(target, "/tmp/ghyll-test-glob/"))
		}
		if err := os.MkdirAll(filepath.Dir(absLink), 0755); err != nil {
			return err
		}
		return os.Symlink(absTarget, absLink)
	})

	ctx.Step(`^the result is returned within the timeout$`, func() error {
		if state.ToolResult.TimedOut {
			return fmt.Errorf("glob timed out")
		}
		if state.ToolResult.Duration > state.ToolTimeout {
			return fmt.Errorf("glob took %s, exceeds timeout %s", state.ToolResult.Duration, state.ToolTimeout)
		}
		return nil
	})
}
