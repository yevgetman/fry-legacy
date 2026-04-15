package codereview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
)

// stubEngine is a test double that writes a predetermined review file and returns.
type stubEngine struct {
	reviewContent string // content to write to .fry/sprint-review.txt
	output        string // raw output returned by Run
	exitCode      int
	runErr        error
}

func (s *stubEngine) Run(_ context.Context, _ string, opts engine.RunOpts) (string, int, error) {
	if s.runErr != nil {
		return "", s.exitCode, s.runErr
	}
	if s.reviewContent != "" {
		dir := filepath.Join(opts.WorkDir, ".fry")
		_ = os.MkdirAll(dir, 0o755)
		path := filepath.Join(opts.WorkDir, SprintReviewFile)
		_ = os.WriteFile(path, []byte(s.reviewContent), 0o644)
	}
	return s.output, s.exitCode, nil
}

func (s *stubEngine) Name() string { return "claude" }

func baseOpts(dir string, eng engine.Engine) ReviewOpts {
	return ReviewOpts{
		ProjectDir: dir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Test Sprint", Prompt: "Do something"},
		Epic:       &epic.Epic{EffortLevel: epic.EffortStandard},
		Engine:     eng,
		Complexity: ComplexityLow,
		GitDiff:    "diff --git a/main.go b/main.go\n+fmt.Println(\"hello\")",
		Mode:       "software",
	}
}

func TestRunCodeReview_Pass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nAll good.\n\n## Findings\nNone.\n\n## Verdict\nPASS\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.True(t, result.Passed)
	assert.False(t, result.Blocking)
	assert.Equal(t, 1, result.Iterations) // no metadata → fallback to 1
	assert.False(t, result.ConvergedCleanly)
	assert.Nil(t, result.Metadata)
	assert.False(t, result.Recovered)
	assert.Empty(t, result.Findings)
}

func TestRunCodeReview_Blocking(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nSecurity issue found.\n\n## Findings\n" +
			"- **Location:** auth.go:10\n- **Description:** SQL injection in login\n- **Severity:** CRITICAL\n" +
			"- **Recommended Fix:** Use parameterized queries\n\n## Verdict\nFAIL\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, "CRITICAL", result.MaxSeverity)
	require.Len(t, result.Findings, 1)
	assert.Equal(t, "SQL injection in login", result.Findings[0].Description)
}

func TestRunCodeReview_Advisory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nMinor issues.\n\n## Findings\n" +
			"- **Location:** util.go:5\n- **Description:** Edge case not handled\n- **Severity:** MODERATE\n" +
			"\n## Verdict\nFAIL\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.False(t, result.Passed)
	assert.False(t, result.Blocking) // MODERATE is not blocking
	assert.Equal(t, "MODERATE", result.MaxSeverity)
}

func TestRunCodeReview_MissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Engine doesn't write the review file but returns output with findings
	eng := &stubEngine{
		output: `{"result": "## Summary\nFound issue.\n\n## Findings\n- **Location:** a.go:1\n- **Description:** Bug\n- **Severity:** HIGH\n\n## Verdict\nFAIL"}`,
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	// Recovery should extract findings from transcript
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.True(t, result.Recovered)
}

func TestRunCodeReview_Validation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{}

	_, err := RunCodeReview(context.Background(), ReviewOpts{Engine: eng, ProjectDir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "epic and sprint are required")

	_, err = RunCodeReview(context.Background(), ReviewOpts{Sprint: &epic.Sprint{}, Epic: &epic.Epic{}, ProjectDir: dir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestBuildReviewPrompt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := baseOpts(dir, &stubEngine{})
	prompt := buildReviewPrompt(opts, 3)

	assert.Contains(t, prompt, "CODE REVIEW — Sprint 1: Test Sprint")
	assert.Contains(t, prompt, "code reviewer")
	assert.Contains(t, prompt, "Sprint Goals")
	assert.Contains(t, prompt, "Do something")
	assert.Contains(t, prompt, "Changes Made This Sprint")
	assert.Contains(t, prompt, "Review Criteria")
	assert.Contains(t, prompt, "Correctness")
	assert.Contains(t, prompt, "EXIT CONDITION")
	assert.Contains(t, prompt, "Maximum iterations: 3")
	assert.Contains(t, prompt, ".fry/sprint-review.txt")
	assert.Contains(t, prompt, "## Review Metadata")
	assert.Contains(t, prompt, "Iterations completed:")
	assert.Contains(t, prompt, "Convergence: CONVERGED | ITERATION_LIMIT")
	assert.Contains(t, prompt, "## Review History")
}

func TestBuildReviewPrompt_WritingMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := baseOpts(dir, &stubEngine{})
	opts.Mode = "writing"
	prompt := buildReviewPrompt(opts, 3)

	assert.Contains(t, prompt, "content reviewer")
	assert.Contains(t, prompt, "Coherence")
	assert.Contains(t, prompt, "Accuracy")
}

func TestCleanup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fryDir := filepath.Join(dir, ".fry")
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	reviewPath := filepath.Join(dir, SprintReviewFile)
	promptPath := filepath.Join(dir, CodeReviewPromptFile)
	require.NoError(t, os.WriteFile(reviewPath, []byte("test"), 0o644))
	require.NoError(t, os.WriteFile(promptPath, []byte("test"), 0o644))

	require.NoError(t, Cleanup(dir))

	_, err := os.Stat(reviewPath)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(promptPath)
	assert.True(t, os.IsNotExist(err))
}

func TestEffectiveMaxIterations(t *testing.T) {
	t.Parallel()

	opts := ReviewOpts{Epic: &epic.Epic{}}
	assert.Equal(t, DefaultMaxIterations, effectiveMaxIterations(opts))

	opts.Epic.MaxReviewIterations = 5
	assert.Equal(t, 5, effectiveMaxIterations(opts))
}

func TestRunCodeReview_EngineError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{runErr: fmt.Errorf("context canceled")}
	_, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine error")
}

func TestRunCodeReview_WithMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nAll good after fixes.\n\n## Findings\nNone.\n\n## Verdict\nPASS\n\n" +
			"## Review Metadata\n- Iterations completed: 3\n- Convergence: CONVERGED\n\n" +
			"## Review History\n### Pass 1\nFound: 2 CRITICAL, 1 HIGH, 0 MODERATE, 0 LOW\nFixed: 2 CRITICAL, 1 HIGH, 0 MODERATE\n" +
			"### Pass 2\nFound: 0 CRITICAL, 0 HIGH, 1 MODERATE, 0 LOW\nFixed: 0 CRITICAL, 0 HIGH, 1 MODERATE\n" +
			"### Pass 3\nFound: 0 CRITICAL, 0 HIGH, 0 MODERATE, 1 LOW\nFixed: 0 CRITICAL, 0 HIGH, 0 MODERATE\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.True(t, result.Passed)
	assert.Equal(t, 3, result.Iterations)
	assert.True(t, result.ConvergedCleanly)
	assert.False(t, result.Recovered)

	require.NotNil(t, result.Metadata)
	assert.Equal(t, 3, result.Metadata.IterationsCompleted)
	assert.Equal(t, ConvergenceConverged, result.Metadata.Convergence)
	require.Len(t, result.Metadata.History, 3)

	assert.Equal(t, 1, result.Metadata.History[0].PassNumber)
	assert.Equal(t, 2, result.Metadata.History[0].FoundCounts["CRITICAL"])
	assert.Equal(t, 1, result.Metadata.History[0].FoundCounts["HIGH"])
	assert.Equal(t, 2, result.Metadata.History[0].FixedCounts["CRITICAL"])
}

func TestRunCodeReview_IterationLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nStill has issues.\n\n## Findings\n" +
			"- **Location:** db.go:55\n- **Description:** Connection pool not closed on shutdown\n- **Severity:** HIGH\n\n" +
			"## Verdict\nFAIL\n\n" +
			"## Review Metadata\n- Iterations completed: 3\n- Convergence: ITERATION_LIMIT\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, 3, result.Iterations)
	assert.False(t, result.ConvergedCleanly)
	require.NotNil(t, result.Metadata)
	assert.Equal(t, ConvergenceIterationLimit, result.Metadata.Convergence)
}

func TestRunCodeReview_NoMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Old-format output with no metadata section
	eng := &stubEngine{
		reviewContent: "## Summary\nAll good.\n\n## Findings\nNone.\n\n## Verdict\nPASS\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations) // graceful fallback
	assert.False(t, result.ConvergedCleanly)
	assert.Nil(t, result.Metadata)
}

func TestRunCodeReview_Dedup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &stubEngine{
		reviewContent: "## Summary\nDuplicate findings.\n\n## Findings\n" +
			"- **Location:** api.go:10\n- **Description:** Missing null check\n- **Severity:** HIGH\n\n" +
			"- **Location:** api.go:10\n- **Description:** Missing null check\n- **Severity:** MODERATE\n\n" +
			"## Verdict\nFAIL\n",
	}
	result, err := RunCodeReview(context.Background(), baseOpts(dir, eng))
	require.NoError(t, err)

	// Duplicates should be merged, keeping the higher severity
	require.Len(t, result.Findings, 1)
	assert.Equal(t, "HIGH", result.Findings[0].Severity)
}
