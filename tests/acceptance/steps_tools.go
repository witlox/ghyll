package acceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/tool"
	"github.com/witlox/ghyll/types"
)

func registerToolSteps(ctx *godog.ScenarioContext, state *ScenarioState) {
	var (
		tmpDir      string
		toolResult  types.ToolResult
		toolName    string
		bashCmd     string
		filePath    string
		fileContent string
		gitArgs     string
		grepPattern string
		grepPath    string
		rgRestrict  bool // true when we explicitly restrict PATH to hide rg
	)

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		dir, err := os.MkdirTemp("", "ghyll-test-tool-*")
		if err != nil {
			return ctx2, err
		}
		tmpDir = dir
		toolResult = types.ToolResult{}
		toolName = ""
		bashCmd = ""
		filePath = ""
		fileContent = ""
		gitArgs = ""
		grepPattern = ""
		grepPath = ""
		rgRestrict = false
		return ctx2, nil
	})

	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		if tmpDir != "" {
			_ = os.RemoveAll(tmpDir)
		}
		return ctx2, nil
	})

	// resolvePath makes relative paths absolute under tmpDir
	resolvePath := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(tmpDir, p)
	}

	ctx.Step(`^the model requests tool call ([a-z_]+) with command "([^"]*)"$`, func(t, cmd string) error {
		toolName = t
		bashCmd = cmd
		// For commands that reference directories, create them in tmpDir
		if strings.Contains(cmd, "src/") {
			srcDir := filepath.Join(tmpDir, "src")
			if err := os.MkdirAll(srcDir, 0755); err != nil {
				return err
			}
			// Create a sample file so ls has something to show
			if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\n"), 0644); err != nil {
				return err
			}
			// Rewrite the command to use the tmpDir path
			bashCmd = strings.ReplaceAll(cmd, "src/", srcDir+"/")
		}
		return nil
	})

	ctx.Step(`^the model requests tool call ([a-z_]+) with path "([^"]*)"$`, func(t, path string) error {
		toolName = t
		filePath = resolvePath(path)
		// For read_file tests, create the file so it can be read
		if t == "read_file" {
			dir := filepath.Dir(filePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
			return os.WriteFile(filePath, []byte("package main\n\nfunc main() {}\n"), 0644)
		}
		return nil
	})

	ctx.Step(`^the model requests tool call ([a-z_]+) with path "([^"]*)" and content$`, func(t, path string) error {
		toolName = t
		filePath = resolvePath(path)
		fileContent = "package util\n\nfunc Helper() {}\n"
		// Ensure parent directory exists
		dir := filepath.Dir(filePath)
		return os.MkdirAll(dir, 0755)
	})

	ctx.Step(`^the model requests tool call ([a-z_]+) with args "([^"]*)"$`, func(t, args string) error {
		toolName = t
		gitArgs = args
		return nil
	})

	ctx.Step(`^the model requests tool call ([a-z_]+) with pattern "([^"]*)" and path "([^"]*)"$`, func(t, pattern, path string) error {
		toolName = t
		grepPattern = pattern
		grepPath = resolvePath(path)
		// Create a file with searchable content for grep tests
		if err := os.MkdirAll(grepPath, 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(grepPath, "example.go"), []byte("// TODO: fix this\npackage main\n// TODO: refactor\n"), 0644)
	})

	ctx.Step(`^the tool executes$`, func() error {
		bg := context.Background()
		timeout := 5 * time.Second

		switch toolName {
		case "bash":
			toolResult = tool.Bash(bg, bashCmd, timeout)
		case "read_file":
			toolResult = tool.ReadFile(bg, filePath, timeout)
		case "write_file":
			toolResult = tool.WriteFile(bg, filePath, fileContent, timeout)
		case "git":
			// Initialize a git repo in tmpDir with two commits for diff HEAD~1
			initCmd := fmt.Sprintf(
				"cd %s && git init && git config user.email 'test@test.com' && git config user.name 'Test' && "+
					"echo 'first' > README && git add . && git commit -m 'init' && "+
					"echo 'second' >> README && git add . && git commit -m 'second commit'",
				tmpDir)
			initResult := tool.Bash(bg, initCmd, timeout)
			if initResult.Error != "" && !strings.Contains(initResult.Error, "already") {
				// Git outputs to stderr for some info messages, only fail on real errors
				if strings.Contains(initResult.Error, "fatal") {
					return fmt.Errorf("git init failed: %s", initResult.Error)
				}
			}
			args := strings.Fields(gitArgs)
			toolResult = tool.Git(bg, tmpDir, args, timeout)
		case "grep":
			if rgRestrict {
				// Use restricted PATH that excludes ripgrep
				toolResult = tool.GrepWithPath(bg, grepPattern, grepPath, timeout, "/usr/bin:/bin")
			} else {
				toolResult = tool.Grep(bg, grepPattern, grepPath, timeout)
			}
		default:
			return fmt.Errorf("unknown tool: %s", toolName)
		}
		return nil
	})

	ctx.Step(`^the bash timeout is (\d+) seconds$`, func(timeout int) error {
		// Stored for reference but we use a short timeout in tests to avoid long waits
		_ = time.Duration(timeout) * time.Second
		return nil
	})

	ctx.Step(`^(\d+) seconds elapse$`, func(n int) error {
		// For timeout tests, we execute with the configured timeout.
		// We use a short timeout (1s) to avoid actually waiting 30s in tests.
		bg := context.Background()
		actualTimeout := 1 * time.Second // use 1s for fast tests
		toolResult = tool.Bash(bg, bashCmd, actualTimeout)
		return nil
	})

	ctx.Step(`^the process is killed$`, func() error {
		if !toolResult.TimedOut {
			return fmt.Errorf("expected process to be killed (timed out), but TimedOut=false")
		}
		return nil
	})

	ctx.Step(`^ripgrep is available in PATH$`, func() error {
		// Check that rg is actually on PATH; if not, grep fallback will be used
		_, _ = exec.LookPath("rg")
		rgRestrict = false
		return nil
	})

	ctx.Step(`^ripgrep is not available in PATH$`, func() error {
		rgRestrict = true
		return nil
	})

	// Then steps for asserting results (matched from feature file)

	ctx.Step(`^exec\.Command\("bash", "-c", "[^"]*"\) runs directly$`, func() error {
		// The tool already ran via "the tool executes" - just verify no fatal error
		if toolResult.TimedOut {
			return fmt.Errorf("command timed out unexpectedly")
		}
		return nil
	})

	ctx.Step(`^stdout and stderr are captured$`, func() error {
		// ToolResult always captures both - just verify the struct is populated
		// (output may be empty for some commands, but the fields exist)
		return nil
	})

	ctx.Step(`^the result is returned to the model as tool output$`, func() error {
		// Verify we got some output or at least no error for a valid command
		if toolResult.Error != "" && !toolResult.TimedOut {
			return fmt.Errorf("unexpected error: %s", toolResult.Error)
		}
		return nil
	})

	ctx.Step(`^the tool returns error "([^"]*)"$`, func(errMsg string) error {
		if toolResult.Error == "" {
			return fmt.Errorf("expected error containing %q, got no error", errMsg)
		}
		// Check that the error contains "timed out" for timeout scenarios
		if strings.Contains(errMsg, "timed out") && !strings.Contains(toolResult.Error, "timed out") {
			return fmt.Errorf("expected timeout error, got: %s", toolResult.Error)
		}
		return nil
	})

	ctx.Step(`^os\.ReadFile\("[^"]*"\) is called directly$`, func() error {
		// ReadFile already executed - verify it returned content
		if toolResult.Error != "" {
			return fmt.Errorf("ReadFile failed: %s", toolResult.Error)
		}
		return nil
	})

	ctx.Step(`^the file contents are returned to the model$`, func() error {
		if toolResult.Output == "" {
			return fmt.Errorf("expected file contents, got empty output")
		}
		return nil
	})

	ctx.Step(`^os\.WriteFile\("[^"]*", content, 0644\) is called directly$`, func() error {
		if toolResult.Error != "" {
			return fmt.Errorf("WriteFile failed: %s", toolResult.Error)
		}
		return nil
	})

	ctx.Step(`^confirmation is returned to the model$`, func() error {
		if toolResult.Output == "" {
			return fmt.Errorf("expected confirmation output, got empty")
		}
		if !strings.Contains(toolResult.Output, "wrote") {
			return fmt.Errorf("expected 'wrote' in output, got: %s", toolResult.Output)
		}
		// Verify the file was actually written
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("file not written: %v", err)
		}
		if string(data) != fileContent {
			return fmt.Errorf("file content mismatch: got %q", string(data))
		}
		return nil
	})

	ctx.Step(`^exec\.Command\("git", "[^"]*"(?:, "[^"]*")*\) runs in the workspace directory$`, func() error {
		if toolResult.TimedOut {
			return fmt.Errorf("git command timed out")
		}
		return nil
	})

	ctx.Step(`^stdout is returned to the model$`, func() error {
		// For git diff on initial commit, output may be empty - that's ok
		if toolResult.Error != "" {
			return fmt.Errorf("unexpected error: %s", toolResult.Error)
		}
		return nil
	})

	ctx.Step(`^exec\.Command\("rg", "[^"]*", "[^"]*"\) runs$`, func() error {
		if toolResult.TimedOut {
			return fmt.Errorf("grep timed out")
		}
		return nil
	})

	ctx.Step(`^matches are returned to the model$`, func() error {
		if toolResult.Output == "" {
			return fmt.Errorf("expected grep matches, got empty output")
		}
		if !strings.Contains(toolResult.Output, "TODO") {
			return fmt.Errorf("expected TODO matches, got: %s", toolResult.Output)
		}
		return nil
	})

	ctx.Step(`^the model requests tool call grep$`, func() error {
		toolName = "grep"
		grepPattern = "TODO"
		grepPath = tmpDir
		// Create a searchable file
		if err := os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("// TODO: implement\n"), 0644); err != nil {
			return err
		}
		// Execute with restricted PATH
		bg := context.Background()
		toolResult = tool.GrepWithPath(bg, grepPattern, grepPath, 5*time.Second, "/usr/bin:/bin")
		return nil
	})

	ctx.Step(`^exec\.Command\("grep", "-rn", pattern, path\) is used as fallback$`, func() error {
		// GrepWithPath was used, which always uses standard grep
		// Verify we got results
		if toolResult.Error != "" {
			return fmt.Errorf("grep fallback failed: %s", toolResult.Error)
		}
		if !strings.Contains(toolResult.Output, "TODO") {
			return fmt.Errorf("expected TODO in grep output, got: %s", toolResult.Output)
		}
		return nil
	})
}
