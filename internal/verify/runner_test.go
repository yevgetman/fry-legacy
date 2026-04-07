package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yevgetman/fry/internal/textutil"
)

func TestParseVerification(t *testing.T) {
	t.Parallel()

	path := writeVerificationFile(t, `
# comment
@sprint 3
@check_file go.mod
@check_file_contains README.md "hello world"
@check_cmd go test ./...
@check_cmd_output printf 'ready\n' | "^ready$"
`)

	checks, err := ParseVerification(path)
	require.NoError(t, err)
	require.Len(t, checks, 4)

	assert.Equal(t, Check{Sprint: 3, Type: CheckFile, Path: "go.mod"}, checks[0])
	assert.Equal(t, Check{Sprint: 3, Type: CheckFileContains, Path: "README.md", Pattern: "hello world"}, checks[1])
	assert.Equal(t, Check{Sprint: 3, Type: CheckCmd, Command: "go test ./..."}, checks[2])
	assert.Equal(t, Check{Sprint: 3, Type: CheckCmdOutput, Command: "printf 'ready\\n'", Pattern: "^ready$"}, checks[3])
}

func TestParseVerificationSkipsComments(t *testing.T) {
	t.Parallel()

	path := writeVerificationFile(t, `

# one
@sprint 1   
  	
# two
@check_file internal/verify/parser.go   
`)

	checks, err := ParseVerification(path)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	assert.Equal(t, 1, checks[0].Sprint)
	assert.Equal(t, "internal/verify/parser.go", checks[0].Path)
}

func TestParseVerificationMultipleSprints(t *testing.T) {
	t.Parallel()

	path := writeVerificationFile(t, `
@sprint 1
@check_file one.txt
@sprint 2
@check_cmd true
@sprint 1
@check_cmd_output printf 'x\n' | "x"
`)

	checks, err := ParseVerification(path)
	require.NoError(t, err)
	require.Len(t, checks, 3)
	assert.Equal(t, 1, checks[0].Sprint)
	assert.Equal(t, 2, checks[1].Sprint)
	assert.Equal(t, 1, checks[2].Sprint)
}

func TestParseCmdOutputWithPipeInCommand(t *testing.T) {
	t.Parallel()

	// Commands containing " | " (e.g. bash -c "... | wc -l") should split on the
	// LAST " | " so that the pipeline inside the bash -c string is preserved.
	path := writeVerificationFile(t, `
@sprint 1
@check_cmd_output bash -c "printf 'a\nb\n' | wc -l" | ^2$
`)
	checks, err := ParseVerification(path)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	assert.Equal(t, `bash -c "printf 'a\nb\n' | wc -l"`, checks[0].Command)
	assert.Equal(t, "^2$", checks[0].Pattern)
}

func TestUnquotePattern(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "hello", unquotePattern("\"hello\""))
	assert.Equal(t, "plain", unquotePattern("plain"))
	assert.Equal(t, `say "hi"`, unquotePattern(`say \"hi\"`))
}

func TestUnquotePatternEscapes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "hello", unquotePattern(`"hello"`))
	assert.Equal(t, `\test`, unquotePattern(`\\test`))
	assert.Equal(t, `say "hi"`, unquotePattern(`say \"hi\"`))
}

func TestRunChecksFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "present.txt"), []byte("ok"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "empty.txt"), nil, 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "dir"), 0o755))

	checks := []Check{
		{Sprint: 1, Type: CheckFile, Path: "present.txt"},
		{Sprint: 1, Type: CheckFile, Path: "missing.txt"},
		{Sprint: 1, Type: CheckFile, Path: "empty.txt"}, // .gitkeep semantics: empty must pass
		{Sprint: 1, Type: CheckFile, Path: "dir"},       // directory must NOT pass @check_file
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 4)
	assert.Equal(t, 2, passCount)
	assert.Equal(t, 4, totalCount)
	assert.True(t, results[0].Passed, "non-empty file should pass")
	assert.False(t, results[1].Passed, "missing file should fail")
	assert.True(t, results[2].Passed, "empty file (.gitkeep style) should pass")
	assert.False(t, results[3].Passed, "directory should fail @check_file")
}

func TestRunChecksCmd(t *testing.T) {
	t.Parallel()

	checks := []Check{
		{Sprint: 1, Type: CheckCmd, Command: "true"},
		{Sprint: 1, Type: CheckCmd, Command: "false"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 2)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 2, totalCount)
	assert.True(t, results[0].Passed)
	assert.False(t, results[1].Passed)
}

func TestRunChecksNoShortCircuit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	marker := filepath.Join(projectDir, "marker.txt")

	checks := []Check{
		{Sprint: 1, Type: CheckCmd, Command: "false"},
		{Sprint: 1, Type: CheckCmd, Command: "printf ok > marker.txt"},
		{Sprint: 1, Type: CheckFile, Path: "marker.txt"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 3)
	assert.Equal(t, 2, passCount)
	assert.Equal(t, 3, totalCount)
	assert.False(t, results[0].Passed)
	assert.True(t, results[1].Passed)
	assert.True(t, results[2].Passed)
	_, err := os.Stat(marker)
	assert.NoError(t, err)
}

func TestCollectFailures(t *testing.T) {
	t.Parallel()

	cmdOutput := strings.Join([]string{
		"line1", "line2", "line3", "line4", "line5",
		"line6", "line7", "line8", "line9", "line10", "line11",
	}, "\n")

	report := CollectFailures([]CheckResult{
		{Check: Check{Type: CheckFile, Path: "missing.txt"}},
		{Check: Check{Type: CheckFileContains, Path: "go.mod", Pattern: "module x"}},
		{Check: Check{Type: CheckCmd, Command: "go test ./..."}, Output: strings.Repeat("a\n", 25)},
		{Check: Check{Type: CheckCmdOutput, Command: "printf nope", Pattern: "^ok$"}, Output: cmdOutput},
		{Check: Check{Type: CheckTest, Command: "go test -v ./..."}, Output: "--- FAIL: TestFoo\n", TestFailCount: 1, TestFramework: "go"},
	}, 0, 5)

	assert.Contains(t, report, "Sanity checks: 0/5 passed.\n\nFailed checks:")
	assert.Contains(t, report, "- FAILED: File missing or empty: missing.txt")
	assert.Contains(t, report, "- FAILED: File 'go.mod' does not contain pattern: module x")
	assert.Contains(t, report, "- FAILED: Command failed: go test ./...")
	assert.Contains(t, report, "  Output (truncated):")
	assert.Contains(t, report, "- FAILED: Command output mismatch: printf nope")
	assert.Contains(t, report, "  Expected pattern: ^ok$")
	assert.NotContains(t, report, "line11")
	assert.Contains(t, report, "FAILED: Test command failed: go test -v ./...")
	assert.Contains(t, report, "fail=1")
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "''", textutil.ShellQuote(""))
	assert.Equal(t, "'simple'", textutil.ShellQuote("simple"))
	assert.Equal(t, `'`+`it'\''s`+`'`, textutil.ShellQuote("it's"))
	assert.Equal(t, "'$HOME *.go'", textutil.ShellQuote("$HOME *.go"))
}

func TestRunChecksFileContains(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0o644))

	checks := []Check{
		{Sprint: 1, Type: CheckFileContains, Path: "go.mod", Pattern: "module example"},
		{Sprint: 1, Type: CheckFileContains, Path: "go.mod", Pattern: "^nonexistent$"},
		{Sprint: 1, Type: CheckFileContains, Path: "missing.txt", Pattern: "anything"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 3)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 3, totalCount)
	assert.True(t, results[0].Passed)
	assert.False(t, results[1].Passed)
	assert.False(t, results[2].Passed)
}

func TestRunChecksCmdOutput(t *testing.T) {
	t.Parallel()

	checks := []Check{
		{Sprint: 1, Type: CheckCmdOutput, Command: "echo hello", Pattern: "^hello$"},
		{Sprint: 1, Type: CheckCmdOutput, Command: "echo world", Pattern: "^nope$"},
		{Sprint: 1, Type: CheckCmdOutput, Command: "false", Pattern: "anything"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 3)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 3, totalCount)
	assert.True(t, results[0].Passed)
	assert.False(t, results[1].Passed)
	assert.False(t, results[2].Passed)
}

func TestRunChecksCmdOutputTrimsWhitespace(t *testing.T) {
	t.Parallel()

	// Simulate macOS wc -w which outputs leading whitespace (e.g., "     42")
	checks := []Check{
		{Sprint: 1, Type: CheckCmdOutput, Command: "printf '   42\\n'", Pattern: "^42$"},
		{Sprint: 1, Type: CheckCmdOutput, Command: "printf '  hello  \\n  world  \\n'", Pattern: "^world$"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 2)
	assert.Equal(t, 2, passCount)
	assert.Equal(t, 2, totalCount)
	assert.True(t, results[0].Passed, "leading whitespace should be trimmed before matching")
	assert.True(t, results[1].Passed, "per-line trimming should work across multiple lines")
}

func TestRunChecksCmdOutputEmptyMatchesCaretDollar(t *testing.T) {
	t.Parallel()

	// When a command produces no output, ^$ (empty line) should match.
	// This is the pattern used to assert "no diff" via git diff HEAD -- file.
	checks := []Check{
		{Sprint: 1, Type: CheckCmdOutput, Command: "true", Pattern: "^$"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 1, totalCount)
	assert.True(t, results[0].Passed, "empty command output should match ^$")
}

func TestRunChecksCmdOutputNonZeroExitEmptyOutput(t *testing.T) {
	t.Parallel()

	// A command that exits non-zero but produces no output (e.g. grep with no
	// matches exits 1) should still match ^$. CheckCmdOutput semantics are
	// output-only: exit code is irrelevant.
	checks := []Check{
		{Sprint: 1, Type: CheckCmdOutput, Command: "false", Pattern: "^$"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 1, totalCount)
	assert.True(t, results[0].Passed, "non-zero exit with empty output must match ^$")
}

func TestTrimOutputLines(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "42", trimOutputLines("   42"))
	assert.Equal(t, "42\n", trimOutputLines("   42\n"))
	assert.Equal(t, "hello\nworld", trimOutputLines("  hello  \n  world  "))
	assert.Equal(t, "", trimOutputLines(""))
	assert.Equal(t, "ok", trimOutputLines("ok"))
}

func TestEvaluateThresholdNoChecks(t *testing.T) {
	t.Parallel()

	outcome := EvaluateThreshold(nil, 0, 0, 20)
	assert.True(t, outcome.WithinThreshold)
	assert.Equal(t, float64(0), outcome.FailPercent)
	assert.Empty(t, outcome.DeferredFailures)
}

func TestEvaluateThresholdAllPass(t *testing.T) {
	t.Parallel()

	results := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "a.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "b.txt"}, Passed: true},
	}
	outcome := EvaluateThreshold(results, 2, 2, 20)
	assert.True(t, outcome.WithinThreshold)
	assert.Equal(t, float64(0), outcome.FailPercent)
	assert.Empty(t, outcome.DeferredFailures)
}

func TestEvaluateThresholdWithinThreshold(t *testing.T) {
	t.Parallel()

	results := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "a.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "b.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "c.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "d.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "e.txt"}, Passed: false},
	}
	// 1 of 5 = 20%, threshold is 20% → within
	outcome := EvaluateThreshold(results, 4, 5, 20)
	assert.True(t, outcome.WithinThreshold)
	assert.InDelta(t, 20.0, outcome.FailPercent, 0.01)
	require.Len(t, outcome.DeferredFailures, 1)
	assert.Equal(t, "e.txt", outcome.DeferredFailures[0].Check.Path)
}

func TestEvaluateThresholdExceedsThreshold(t *testing.T) {
	t.Parallel()

	results := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "a.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "b.txt"}, Passed: false},
		{Check: Check{Type: CheckFile, Path: "c.txt"}, Passed: false},
	}
	// 2 of 3 = 66.7%, threshold 20% → exceeds
	outcome := EvaluateThreshold(results, 1, 3, 20)
	assert.False(t, outcome.WithinThreshold)
	assert.InDelta(t, 66.67, outcome.FailPercent, 0.01)
	assert.Empty(t, outcome.DeferredFailures)
}

func TestEvaluateThresholdZeroPercent(t *testing.T) {
	t.Parallel()

	results := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "a.txt"}, Passed: true},
		{Check: Check{Type: CheckFile, Path: "b.txt"}, Passed: false},
	}
	// strict mode: 0% threshold, any failure exceeds
	outcome := EvaluateThreshold(results, 1, 2, 0)
	assert.False(t, outcome.WithinThreshold)
}

func TestEvaluateThresholdHundredPercent(t *testing.T) {
	t.Parallel()

	results := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "a.txt"}, Passed: false},
		{Check: Check{Type: CheckFile, Path: "b.txt"}, Passed: false},
	}
	// 100% threshold → always within
	outcome := EvaluateThreshold(results, 0, 2, 100)
	assert.True(t, outcome.WithinThreshold)
	require.Len(t, outcome.DeferredFailures, 2)
}

func TestCollectDeferredSummary(t *testing.T) {
	t.Parallel()

	deferred := []CheckResult{
		{Check: Check{Type: CheckFile, Path: "missing.txt"}},
		{Check: Check{Type: CheckFileContains, Path: "go.mod", Pattern: "module x"}},
		{Check: Check{Type: CheckCmd, Command: "go test ./..."}},
		{Check: Check{Type: CheckCmdOutput, Command: "echo nope", Pattern: "^ok$"}},
		{Check: Check{Type: CheckTest, Command: "pytest tests/"}, TestFailCount: 2, TestPassCount: 1},
	}
	summary := CollectDeferredSummary(deferred)
	assert.Contains(t, summary, "DEFERRED: File missing or empty: missing.txt")
	assert.Contains(t, summary, "DEFERRED: File 'go.mod' does not contain pattern: module x")
	assert.Contains(t, summary, "DEFERRED: Command failed: go test ./...")
	assert.Contains(t, summary, "DEFERRED: Command output mismatch: echo nope (expected pattern: ^ok$)")
	assert.Contains(t, summary, "DEFERRED: Test command failed: pytest tests/")
}

// --- P1: cappedBuffer tests ---

func TestCappedBuffer_NormalWrite(t *testing.T) {
	t.Parallel()

	var buf cappedBuffer
	n, err := buf.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", buf.String())
	assert.Equal(t, 5, buf.Len())
}

func TestCappedBuffer_ExceedsCap(t *testing.T) {
	t.Parallel()

	var buf cappedBuffer
	// Write up to near the cap
	bigChunk := make([]byte, maxCheckOutput-10)
	for i := range bigChunk {
		bigChunk[i] = 'x'
	}
	_, err := buf.Write(bigChunk)
	require.NoError(t, err)
	assert.Equal(t, maxCheckOutput-10, buf.Len())

	// Write more than remaining — truncated to remaining (10 bytes)
	n, err := buf.Write([]byte("0123456789extra"))
	require.NoError(t, err)
	assert.Equal(t, 15, n) // reports full input length per io.Writer contract
	assert.Equal(t, maxCheckOutput, buf.Len())
}

func TestCappedBuffer_DiscardAfterFull(t *testing.T) {
	t.Parallel()

	var buf cappedBuffer
	full := make([]byte, maxCheckOutput)
	for i := range full {
		full[i] = 'a'
	}
	_, err := buf.Write(full)
	require.NoError(t, err)

	// Subsequent writes silently discarded
	n, err := buf.Write([]byte("more data"))
	require.NoError(t, err)
	assert.Equal(t, 9, n)
	assert.Equal(t, maxCheckOutput, buf.Len())
}

// --- P1: RunChecks sprint filtering ---

func TestRunChecks_SprintFiltering(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "a.txt"), []byte("content\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "b.txt"), []byte("content\n"), 0o644))

	checks := []Check{
		{Sprint: 1, Type: CheckFile, Path: "a.txt"},
		{Sprint: 2, Type: CheckFile, Path: "b.txt"},
		{Sprint: 1, Type: CheckFile, Path: "b.txt"},
	}

	results, pass, total := RunChecks(context.Background(), checks, 1, projectDir)
	assert.Equal(t, 2, total, "should only run sprint 1 checks")
	assert.Equal(t, 2, pass)
	assert.Len(t, results, 2)

	results2, pass2, total2 := RunChecks(context.Background(), checks, 2, projectDir)
	assert.Equal(t, 1, total2)
	assert.Equal(t, 1, pass2)
	assert.Len(t, results2, 1)
}

func TestRunChecks_EmptyChecks(t *testing.T) {
	t.Parallel()

	results, pass, total := RunChecks(context.Background(), nil, 1, t.TempDir())
	assert.Equal(t, 0, total)
	assert.Equal(t, 0, pass)
	assert.Empty(t, results)
}

func TestRunChecks_NoMatchingSprint(t *testing.T) {
	t.Parallel()

	checks := []Check{
		{Sprint: 1, Type: CheckFile, Path: "a.txt"},
	}
	results, pass, total := RunChecks(context.Background(), checks, 99, t.TempDir())
	assert.Equal(t, 0, total)
	assert.Equal(t, 0, pass)
	assert.Empty(t, results)
}

// --- CheckFile semantics: empty files (e.g. .gitkeep) must pass ---

func TestRunCheck_CheckFileZeroSize_Passes(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "empty.txt"), []byte(""), 0o644))

	checks := []Check{
		{Sprint: 1, Type: CheckFile, Path: "empty.txt"},
	}
	results, pass, _ := RunChecks(context.Background(), checks, 1, projectDir)
	assert.Equal(t, 1, pass, "zero-size file should pass CheckFile (.gitkeep style)")
	assert.True(t, results[0].Passed)
}

func TestParseVerificationCheckTest(t *testing.T) {
	t.Parallel()

	path := writeVerificationFile(t, `
@sprint 1
@check_test go test ./...
@check_test pytest tests/
`)
	checks, err := ParseVerification(path)
	require.NoError(t, err)
	require.Len(t, checks, 2)
	assert.Equal(t, Check{Sprint: 1, Type: CheckTest, Command: "go test ./..."}, checks[0])
	assert.Equal(t, Check{Sprint: 1, Type: CheckTest, Command: "pytest tests/"}, checks[1])
}

func TestCheckTestGoPassingOutput(t *testing.T) {
	t.Parallel()

	// Test that a CheckTest command exiting 0 with no FAIL lines passes.
	// Framework detection and count parsing are tested separately in TestParseGoTestOutput.
	checks := []Check{
		{Sprint: 1, Type: CheckTest, Command: "true"},
	}
	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 1, totalCount)
	assert.True(t, results[0].Passed)
	assert.Equal(t, 0, results[0].TestFailCount)
}

func TestCheckTestGoFailingOutput(t *testing.T) {
	t.Parallel()

	// Test that a CheckTest command exiting non-zero fails.
	checks := []Check{
		{Sprint: 1, Type: CheckTest, Command: "false"},
	}
	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 0, passCount)
	assert.Equal(t, 1, totalCount)
	assert.False(t, results[0].Passed)
}

func TestCheckTestUnknownFramework(t *testing.T) {
	t.Parallel()

	// Unknown framework falls back to exit code only
	checks := []Check{
		{Sprint: 1, Type: CheckTest, Command: "true"},
	}
	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, passCount)
	assert.Equal(t, 1, totalCount)
	assert.True(t, results[0].Passed)
	assert.Equal(t, "unknown", results[0].TestFramework)
	assert.Equal(t, 0, results[0].TestPassCount)
	assert.Equal(t, 0, results[0].TestFailCount)
}

func TestParseGoTestOutput(t *testing.T) {
	t.Parallel()

	output := "--- PASS: TestFoo (0.00s)\n--- PASS: TestBar (0.01s)\n--- FAIL: TestBaz (0.02s)\nFAIL\n"
	result := CheckResult{}
	parseGoTestOutput(&result, output)
	assert.Equal(t, 2, result.TestPassCount)
	assert.Equal(t, 1, result.TestFailCount)
}

func TestParsePytestOutput(t *testing.T) {
	t.Parallel()

	output := "collected 5 items\n\n5 passed, 2 failed, 1 skipped in 0.12s\n"
	result := CheckResult{}
	parsePytestOutput(&result, output)
	assert.Equal(t, 5, result.TestPassCount)
	assert.Equal(t, 2, result.TestFailCount)
	assert.Equal(t, 1, result.TestSkipCount)
}

func TestParseJestOutput(t *testing.T) {
	t.Parallel()

	output := "Tests: 1 failed, 2 skipped, 7 passed, 10 total\n"
	result := CheckResult{}
	parseJestOutput(&result, output)
	assert.Equal(t, 1, result.TestFailCount)
	assert.Equal(t, 2, result.TestSkipCount)
	assert.Equal(t, 7, result.TestPassCount)
}

// --- B4: Edge case tests ---

// TestRunnerCommandTimeout verifies that a command that runs longer than the
// parent context deadline is killed and the check fails.
func TestRunnerCommandTimeout(t *testing.T) {
	t.Parallel()

	// Cancel the context after a very short delay to simulate a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	checks := []Check{
		{Sprint: 1, Type: CheckCmd, Command: "sleep 10"},
	}

	results, passCount, totalCount := RunChecks(ctx, checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, totalCount)
	assert.Equal(t, 0, passCount)
	assert.False(t, results[0].Passed, "command that exceeds context deadline must fail")
}

// TestRunnerCheckFileContainsTimeout verifies that a CheckFileContains check
// with an already-expired context fails instead of hanging.
func TestRunnerCheckFileContainsTimeout(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "data.txt"), []byte("hello world"), 0o644))

	// Create a pre-expired context.
	deadline := time.Now()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	checks := []Check{
		{Sprint: 1, Type: CheckFileContains, Path: "data.txt", Pattern: "hello"},
	}

	results, passCount, totalCount := RunChecks(ctx, checks, 1, projectDir)
	require.Len(t, results, 1)
	assert.Equal(t, 1, totalCount)
	assert.Equal(t, 0, passCount)
	assert.False(t, results[0].Passed, "CheckFileContains with expired context must fail")
}

// TestRunnerLargeOutputTruncation verifies that a command generating more than
// maxCheckOutput bytes completes without OOM and the output is capped.
func TestRunnerLargeOutputTruncation(t *testing.T) {
	t.Parallel()

	// Generate slightly more than 10 MB of output using dd reading from /dev/zero.
	const overMaxBytes = maxCheckOutput + 1024

	checks := []Check{
		{
			Sprint:  1,
			Type:    CheckCmd,
			Command: "dd if=/dev/zero bs=1024 count=10241 2>/dev/null | cat",
		},
	}

	results, _, totalCount := RunChecks(context.Background(), checks, 1, t.TempDir())
	require.Len(t, results, 1)
	assert.Equal(t, 1, totalCount)
	// The output must be capped at maxCheckOutput.
	assert.LessOrEqual(t, len(results[0].Output), overMaxBytes,
		"output must be capped at maxCheckOutput bytes")
}

// TestRunnerRegexEdgeCases tests CheckFileContains behaviour for edge-case patterns:
// empty pattern, a pattern with literal parentheses, and a pattern that
// matches the entire file content.
func TestRunnerRegexEdgeCases(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "data.txt"), []byte("hello world\ncall(arg)\n"), 0o644))

	cases := []struct {
		name     string
		pattern  string
		wantPass bool
	}{
		{
			name:     "empty pattern matches everything",
			pattern:  "",
			wantPass: true,
		},
		{
			name:     "escaped parentheses match literal parentheses in file",
			pattern:  `call\(arg\)`,
			wantPass: true,
		},
		{
			name:     "pattern matching entire content passes",
			pattern:  "hello world",
			wantPass: true,
		},
		{
			name:     "pattern not in file fails",
			pattern:  "(unclosed",
			wantPass: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checks := []Check{
				{Sprint: 1, Type: CheckFileContains, Path: "data.txt", Pattern: tc.pattern},
			}
			results, _, _ := RunChecks(context.Background(), checks, 1, projectDir)
			require.Len(t, results, 1)
			assert.Equal(t, tc.wantPass, results[0].Passed, "pattern=%q", tc.pattern)
		})
	}
}

// TestRunnerMixedCheckTypes verifies that a mix of check types are all evaluated
// and that each result correctly reflects the pass/fail state.
func TestRunnerMixedCheckTypes(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "exists.txt"), []byte("content\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "search.txt"), []byte("needle\n"), 0o644))

	checks := []Check{
		{Sprint: 1, Type: CheckFile, Path: "exists.txt"},           // pass: file exists and non-empty
		{Sprint: 1, Type: CheckFile, Path: "missing.txt"},          // fail: file absent
		{Sprint: 1, Type: CheckFileContains, Path: "search.txt", Pattern: "needle"}, // pass
		{Sprint: 1, Type: CheckCmd, Command: "true"},               // pass
		{Sprint: 1, Type: CheckCmd, Command: "false"},              // fail
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 5)
	assert.Equal(t, 5, totalCount)
	assert.Equal(t, 3, passCount)

	assert.True(t, results[0].Passed, "file_exists check on existing non-empty file should pass")
	assert.False(t, results[1].Passed, "file_exists check on missing file should fail")
	assert.True(t, results[2].Passed, "file_contains check for present needle should pass")
	assert.True(t, results[3].Passed, "cmd 'true' should pass")
	assert.False(t, results[4].Passed, "cmd 'false' should fail")
}

func TestRunChecksFileContainsRegex(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "doc.md"), []byte("# Introduction\n\nSome text\n"), 0o644))

	checks := []Check{
		{Sprint: 1, Type: CheckFileContains, Path: "doc.md", Pattern: "^# "},
		{Sprint: 1, Type: CheckFileContains, Path: "doc.md", Pattern: "^# Foo"},
	}

	results, passCount, totalCount := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 2)
	assert.Equal(t, 2, totalCount)
	assert.Equal(t, 1, passCount)
	assert.True(t, results[0].Passed, "^# should match a line starting with '# '")
	assert.False(t, results[1].Passed, "^# Foo should not match '# Introduction'")
}

func TestRunChecksFileContainsBREAlternation(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "view.swift"), []byte("let progress = animationProgress\n"), 0o644))

	checks := []Check{
		// BRE-style \| should be normalized to ERE | for alternation.
		{Sprint: 1, Type: CheckFileContains, Path: "view.swift", Pattern: `trim\|animationProgress`},
		// ERE-style | should continue to work.
		{Sprint: 1, Type: CheckFileContains, Path: "view.swift", Pattern: "trim|animationProgress"},
		// Pattern with no alternation should be unaffected.
		{Sprint: 1, Type: CheckFileContains, Path: "view.swift", Pattern: "animationProgress"},
	}

	results, passCount, _ := RunChecks(context.Background(), checks, 1, projectDir)
	require.Len(t, results, 3)
	assert.Equal(t, 3, passCount)
	assert.True(t, results[0].Passed, `BRE-style \| should match as alternation`)
	assert.True(t, results[1].Passed, "ERE-style | should match as alternation")
	assert.True(t, results[2].Passed, "plain pattern should match")
}

func writeVerificationFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "verification.md")
	require.NoError(t, os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o644))
	return path
}
