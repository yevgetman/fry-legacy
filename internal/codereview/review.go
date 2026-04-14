package codereview

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/templates"
)

const (
	// File paths for code review artifacts.
	SprintReviewFile      = ".fry/sprint-review.txt"
	CodeReviewPromptFile  = ".fry/code-review-prompt.md"
	ReviewInvocationFile  = "invocations/review.txt"
	DefaultMaxIterations  = 3
	MaxReviewDiffBytes    = 100_000
)

// RunCodeReview runs a single-session code review for a sprint.
//
// One engine.Run() call is made. The agent prompt instructs it to review,
// fix issues above LOW, and re-review until clean — all within one session.
// Fry reads the final .fry/sprint-review.txt to determine pass/fail.
func RunCodeReview(ctx context.Context, opts ReviewOpts) (*ReviewResult, error) {
	if opts.Epic == nil || opts.Sprint == nil {
		return nil, fmt.Errorf("run code review: epic and sprint are required")
	}
	if opts.Engine == nil {
		return nil, fmt.Errorf("run code review: engine is required")
	}

	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("run code review: create build logs dir: %w", err)
	}

	reviewFilePath := filepath.Join(opts.ProjectDir, SprintReviewFile)
	promptPath := filepath.Join(opts.ProjectDir, CodeReviewPromptFile)

	// Clean up any stale review file from a previous run.
	_ = os.Remove(reviewFilePath)

	// Refresh git diff if a DiffFn is available.
	if opts.DiffFn != nil {
		if freshDiff, err := opts.DiffFn(); err == nil && freshDiff != "" {
			opts.GitDiff = freshDiff
		}
	}

	// Build the review prompt.
	maxIter := effectiveMaxIterations(opts)
	prompt := buildReviewPrompt(opts, maxIter)

	// Write prompt to disk so the agent can read it.
	if err := writeFile(promptPath, prompt); err != nil {
		return nil, fmt.Errorf("run code review: write prompt: %w", err)
	}

	// Load invocation template.
	invocation, err := templates.LoadText(ReviewInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("run code review: %w", err)
	}

	// Resolve model.
	model := engine.ResolveModel(
		opts.Epic.ReviewModel,
		opts.Engine.Name(),
		string(opts.Epic.EffortLevel),
		engine.SessionCodeReview,
	)

	// Report progress.
	if opts.ProgressFn != nil {
		opts.ProgressFn(ReviewProgress{
			Stage:      "reviewing",
			Complexity: opts.Complexity,
		})
	}

	// Set up logging.
	logPath := filepath.Join(buildLogsDir,
		fmt.Sprintf("sprint%d_review_%s.log", opts.Sprint.Number, time.Now().Format("20060102_150405")))

	var logBuf bytes.Buffer
	var stdout, stderr bytes.Buffer

	runOpts := engine.RunOpts{
		Model:            model,
		SessionType:      engine.SessionCodeReview,
		StructuredOutput: true,
		EffortLevel:      string(opts.Epic.EffortLevel),
		WorkDir:          opts.ProjectDir,
	}

	// Set up log writers.
	logFile, logErr := os.Create(logPath)
	if logErr == nil {
		defer logFile.Close()
		runOpts.Stdout = &logBuf
		runOpts.Stderr = &logBuf
	}

	frylog.Log("  REVIEW: starting single-session review (model=%s, complexity=%s, max_iterations=%d)",
		model, opts.Complexity, maxIter)

	// Single engine call — the agent self-loops internally.
	start := time.Now()
	output, _, runErr := opts.Engine.Run(ctx, invocation, runOpts)
	durationMs := time.Since(start).Milliseconds()

	// Flush log buffer to file.
	if logFile != nil {
		_, _ = logFile.Write(logBuf.Bytes())
	}

	// Capture metrics.
	metrics := &ReviewMetrics{
		ContentComplexity: opts.Complexity,
		Call: &CallMetric{
			SessionType: engine.SessionCodeReview,
			PromptBytes: len(prompt),
			OutputBytes: len(output) + stdout.Len() + stderr.Len(),
			DurationMs:  durationMs,
			Model:       model,
		},
	}

	if runErr != nil {
		return nil, fmt.Errorf("run code review: engine error: %w", runErr)
	}

	// Read the review output file.
	content, readErr := readReviewOutput(reviewFilePath, SprintReviewFile, opts.ProjectDir, output, logPath)
	if readErr != nil {
		frylog.Log("  REVIEW: WARNING: %v", readErr)
		// If we can't read the output, treat as a blocking failure.
		return &ReviewResult{
			Passed:     false,
			Blocking:   true,
			Iterations: 1,
			MaxSeverity: "HIGH",
			SeverityCounts: map[string]int{"HIGH": 1},
			Findings: []Finding{{
				Description: fmt.Sprintf("Review agent did not produce readable output: %v", readErr),
				Severity:    "HIGH",
			}},
			Complexity: opts.Complexity,
			Metrics:    metrics,
		}, nil
	}

	// Parse findings from the final review output.
	contentStr := string(content)
	findings := parseFindings(contentStr)
	severityCounts := countSeverities(findings)
	maxSev := maxFindingSeverity(findings)
	passed := isReviewPass(maxSev)
	blocking := isBlockingSeverity(maxSev)

	metrics.FinalFindingCount = len(findings)

	if passed {
		frylog.Log("  REVIEW: PASSED — no issues above LOW")
	} else if blocking {
		frylog.Log("  REVIEW: FAILED — %s remain", FormatCounts(severityCounts))
	} else {
		frylog.Log("  REVIEW: advisory — %s remain", FormatCounts(severityCounts))
	}

	// Report final progress.
	if opts.ProgressFn != nil {
		opts.ProgressFn(ReviewProgress{
			Stage:      "complete",
			Findings:   severityCounts,
			Complexity: opts.Complexity,
		})
	}

	return &ReviewResult{
		Passed:         passed,
		Blocking:       blocking,
		Iterations:     1,
		MaxSeverity:    maxSev,
		SeverityCounts: severityCounts,
		Findings:       findings,
		Complexity:     opts.Complexity,
		Metrics:        metrics,
	}, nil
}

// Cleanup removes transient review files.
func Cleanup(projectDir string) error {
	for _, rel := range []string{SprintReviewFile, CodeReviewPromptFile} {
		path := filepath.Join(projectDir, rel)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("review cleanup: %w", err)
		}
	}
	return nil
}

func effectiveMaxIterations(opts ReviewOpts) int {
	if opts.Epic.MaxReviewIterations > 0 {
		return opts.Epic.MaxReviewIterations
	}
	return DefaultMaxIterations
}
