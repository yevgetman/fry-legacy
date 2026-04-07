package verify

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yevgetman/fry/internal/textutil"
)

// defaultCheckTimeout is the maximum time a single sanity check command
// is allowed to run before being killed. This prevents hanging builds.
const defaultCheckTimeout = 120 * time.Second

func RunChecks(ctx context.Context, checks []Check, sprintNum int, projectDir string) ([]CheckResult, int, int) {
	var filtered []Check
	for _, check := range checks {
		if check.Sprint == sprintNum {
			filtered = append(filtered, check)
		}
	}

	results := make([]CheckResult, 0, len(filtered))
	passCount := 0

	for _, check := range filtered {
		result := runCheck(ctx, check, projectDir)
		if result.Passed {
			passCount++
		}
		results = append(results, result)
	}

	return results, passCount, len(filtered)
}

func runCheck(ctx context.Context, check Check, projectDir string) CheckResult {
	result := CheckResult{Check: check}

	switch check.Type {
	case CheckFile:
		// `@check_file` means "this path exists and is a regular file."
		// Do NOT require info.Size() > 0 — that breaks legitimately empty
		// placeholder files like .gitkeep, .keep, and .touch markers, and
		// forces alignment agents to write garbage bytes into them. Users
		// who need "exists and non-empty" should use @check_file_contains.
		info, err := os.Stat(filepath.Join(projectDir, check.Path))
		result.Passed = err == nil && !info.IsDir()
	case CheckFileContains:
		checkCtx, checkCancel := context.WithTimeout(ctx, defaultCheckTimeout)
		defer checkCancel()
		targetPath := filepath.Join(projectDir, check.Path)
		// Normalize BRE-style \| to ERE-style | since we use grep -E.
		// LLMs frequently generate \| for alternation, which in ERE means
		// a literal pipe character rather than OR.
		pattern := strings.ReplaceAll(check.Pattern, `\|`, "|")
		// Auto-escape literal "function call" parens like `default(uuid())`
		// or `min(8)` so that grep -E doesn't interpret them as regex
		// grouping metacharacters and silently fail to match real text.
		// AI agents emit these patterns constantly and they're the
		// number-one cause of false-positive sanity check failures that
		// trigger pointless alignment loops.
		pattern = AutoEscapeLiteralParens(pattern)
		cmd := exec.CommandContext(checkCtx, "bash", "-c", fmt.Sprintf("grep -qE -- %s %s", textutil.ShellQuote(pattern), textutil.ShellQuote(targetPath)))
		var stderrBuf cappedBuffer
		cmd.Stderr = &stderrBuf
		result.Passed = cmd.Run() == nil
		result.Output = stderrBuf.String()
	case CheckCmd:
		checkCtx, checkCancel := context.WithTimeout(ctx, defaultCheckTimeout)
		defer checkCancel()
		cmd := exec.CommandContext(checkCtx, "bash", "-c", check.Command)
		cmd.Dir = projectDir
		var combined cappedBuffer
		cmd.Stdout = &combined
		cmd.Stderr = &combined
		err := cmd.Run()
		result.Output = combined.String()
		result.Passed = err == nil
	case CheckCmdOutput:
		checkCtx, checkCancel := context.WithTimeout(ctx, defaultCheckTimeout)
		defer checkCancel()
		command := exec.CommandContext(checkCtx, "bash", "-c", check.Command)
		command.Dir = projectDir

		var stdout, stderr cappedBuffer
		command.Stdout = &stdout
		command.Stderr = &stderr

		_ = command.Run() // exit code is irrelevant for output-pattern checks
		result.Output = stdout.String()
		if result.Output == "" && stderr.Len() > 0 {
			result.Output = stderr.String()
		}

		// Trim leading/trailing whitespace from each line before matching.
		// This prevents platform-specific formatting (e.g., macOS wc -w
		// producing "  42" instead of "42") from causing false negatives.
		trimmed := trimOutputLines(stdout.String())
		// When the command produces no output at all, ensure grep still has
		// one empty line to match against so that patterns like ^$ work.
		if trimmed == "" {
			trimmed = "\n"
		}
		// Same auto-escape rationale as CheckFileContains.
		cmdPattern := AutoEscapeLiteralParens(strings.ReplaceAll(check.Pattern, `\|`, "|"))
		grep := exec.CommandContext(checkCtx, "bash", "-c", fmt.Sprintf("grep -qE -- %s", textutil.ShellQuote(cmdPattern)))
		grep.Stdin = strings.NewReader(trimmed)
		result.Passed = grep.Run() == nil
	case CheckTest:
		checkCtx, checkCancel := context.WithTimeout(ctx, defaultCheckTimeout)
		defer checkCancel()
		cmd := exec.CommandContext(checkCtx, "bash", "-c", check.Command)
		cmd.Dir = projectDir
		var combined cappedBuffer
		cmd.Stdout = &combined
		cmd.Stderr = &combined
		runErr := cmd.Run()
		result.Output = combined.String()
		parseTestOutput(&result, check.Command, result.Output)
		result.Passed = runErr == nil && result.TestFailCount == 0
	}

	return result
}

// parseTestOutput detects the test framework and populates test counts on the sanity check result.
func parseTestOutput(result *CheckResult, command, output string) {
	cmd := strings.TrimSpace(command)
	switch {
	case strings.HasPrefix(cmd, "go test"):
		result.TestFramework = "go"
		parseGoTestOutput(result, output)
	case strings.HasPrefix(cmd, "pytest"):
		result.TestFramework = "pytest"
		parsePytestOutput(result, output)
	case strings.HasPrefix(cmd, "npm test") || strings.HasPrefix(cmd, "jest") || strings.Contains(cmd, "jest"):
		result.TestFramework = "jest"
		parseJestOutput(result, output)
	default:
		result.TestFramework = "unknown"
	}
}

var (
	goTestFailRe  = regexp.MustCompile(`(?m)^--- FAIL:`)
	goTestPassRe  = regexp.MustCompile(`(?m)^--- PASS:`)
)

func parseGoTestOutput(result *CheckResult, output string) {
	result.TestPassCount = len(goTestPassRe.FindAllString(output, -1))
	result.TestFailCount = len(goTestFailRe.FindAllString(output, -1))
}

var (
	pytestPassedRe  = regexp.MustCompile(`(\d+) passed`)
	pytestFailedRe  = regexp.MustCompile(`(\d+) failed`)
	pytestSkippedRe = regexp.MustCompile(`(\d+) skipped`)
)

func parsePytestOutput(result *CheckResult, output string) {
	// Match each token independently so order doesn't matter.
	// Modern pytest may print "1 failed, 3 passed, 1 skipped in 0.52s".
	if m := pytestPassedRe.FindStringSubmatch(output); m != nil {
		result.TestPassCount, _ = strconv.Atoi(m[1])
	}
	if m := pytestFailedRe.FindStringSubmatch(output); m != nil {
		result.TestFailCount, _ = strconv.Atoi(m[1])
	}
	if m := pytestSkippedRe.FindStringSubmatch(output); m != nil {
		result.TestSkipCount, _ = strconv.Atoi(m[1])
	}
}

var (
	jestPassedRe  = regexp.MustCompile(`(\d+) passed`)
	jestFailedRe  = regexp.MustCompile(`(\d+) failed`)
	jestSkippedRe = regexp.MustCompile(`(\d+) skipped`)
)

func parseJestOutput(result *CheckResult, output string) {
	// Find the "Tests:" summary line; apply regexes independently so that
	// "Tests: 3 failed, 3 total" (no "passed" token) still records counts.
	var summaryLine string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Tests:") {
			summaryLine = line
			break
		}
	}
	if summaryLine == "" {
		return
	}
	if m := jestPassedRe.FindStringSubmatch(summaryLine); m != nil {
		result.TestPassCount, _ = strconv.Atoi(m[1])
	}
	if m := jestFailedRe.FindStringSubmatch(summaryLine); m != nil {
		result.TestFailCount, _ = strconv.Atoi(m[1])
	}
	if m := jestSkippedRe.FindStringSubmatch(summaryLine); m != nil {
		result.TestSkipCount, _ = strconv.Atoi(m[1])
	}
}

// trimOutputLines trims leading and trailing whitespace from each line of
// output. This normalizes platform differences (e.g., macOS wc produces
// leading spaces) so that anchored patterns like ^[0-9]+$ match reliably.
func trimOutputLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, "\n")
}

// maxCheckOutput caps the amount of output captured from sanity check commands
// to prevent unbounded memory growth on pathologically verbose checks.
const maxCheckOutput = 10 * 1024 * 1024 // 10 MB

// cappedBuffer is a bytes.Buffer that stops accepting writes after maxCheckOutput.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := maxCheckOutput - c.buf.Len()
	if remaining <= 0 {
		return len(p), nil // discard silently
	}
	if len(p) > remaining {
		_, err := c.buf.Write(p[:remaining])
		return len(p), err
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *cappedBuffer) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Len()
}
