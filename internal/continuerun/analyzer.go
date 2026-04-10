package continuerun

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/textutil"
	"github.com/yevgetman/fry/templates"
)

// AnalyzeOpts configures the LLM analysis agent.
type AnalyzeOpts struct {
	ProjectDir  string
	State       *BuildState
	Engine      engine.Engine
	Model       string
	EffortLevel string
	Verbose     bool
	Stdout      io.Writer // optional; defaults to os.Stdout when Verbose is true
}

// continueJSON is the expected JSON structure from the continue analysis agent.
type continueJSON struct {
	Verdict string `json:"verdict"`
	Sprint  int    `json:"sprint"`
	Reason  string `json:"reason"`
}

// Analyze runs the LLM analysis agent to determine where to resume a build.
func Analyze(ctx context.Context, opts AnalyzeOpts) (*ContinueDecision, error) {
	if opts.Engine == nil {
		return nil, fmt.Errorf("continue analyze: engine is required")
	}

	continuePrompt, err := templates.LoadText(config.ContinueInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("continue analyze: %w", err)
	}

	report := FormatReport(opts.State)

	// Write report to disk for user inspection
	reportPath := filepath.Join(opts.ProjectDir, config.ContinueReportFile)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return nil, fmt.Errorf("continue analyze: create dir: %w", err)
	}
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		return nil, fmt.Errorf("continue analyze: write report: %w", err)
	}

	// Build and write analysis prompt
	prompt := buildAnalysisPrompt(opts.State, report)
	promptPath := filepath.Join(opts.ProjectDir, config.ContinuePromptFile)
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("continue analyze: write prompt: %w", err)
	}

	frylog.Log("▶ CONTINUE  analyzing with engine=%s  model=%s...", opts.Engine.Name(), opts.Model)

	// Create log file
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("continue analyze: create logs dir: %w", err)
	}
	logPath := filepath.Join(buildLogsDir, fmt.Sprintf("continue_%s.log", time.Now().Format("20060102_150405")))
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("continue analyze: create log: %w", err)
	}
	defer logFile.Close()

	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionContinue,
		EffortLevel: opts.EffortLevel,
		WorkDir:     opts.ProjectDir,
	}
	if opts.Verbose {
		stdout := opts.Stdout
		if stdout == nil {
			stdout = os.Stdout
		}
		writer := io.MultiWriter(stdout, logFile)
		runOpts.Stdout = writer
		runOpts.Stderr = writer
	} else {
		runOpts.Stdout = logFile
		runOpts.Stderr = logFile
	}

	output, _, runErr := opts.Engine.Run(ctx, continuePrompt, runOpts)
	if runErr != nil && ctx.Err() == nil {
		frylog.Log("WARNING: continue agent exited with error (non-fatal): %v", runErr)
	} else if runErr != nil {
		return nil, runErr
	}

	// Read decision file if the agent wrote one
	decisionPath := filepath.Join(opts.ProjectDir, config.ContinueDecisionFile)
	if data, err := os.ReadFile(decisionPath); err == nil {
		output = string(data)
	}

	decision := ParseDecision(output, opts.State.TotalSprints)

	frylog.Log("▶ CONTINUE  decision: %s sprint %d — %q",
		decision.Verdict, decision.StartSprint, truncate(decision.Reason, 80))

	return decision, nil
}

// HeuristicAnalyze determines where to resume a build without an LLM call.
// It scans the BuildState for sprint completion markers, finds the first sprint
// without a completion marker, and returns that sprint as the resume point.
// Returns VerdictAuditIncomplete when all sprints are complete but the build
// audit sentinel is absent. Returns VerdictAllComplete when state is nil, has
// no sprints, or all sprints are complete and the audit is done.
func HeuristicAnalyze(state *BuildState) *ContinueDecision {
	if state == nil || state.TotalSprints == 0 {
		return &ContinueDecision{
			Verdict: VerdictAllComplete,
			Reason:  "heuristic continue: all sprints completed",
		}
	}

	if state.ResumePoint != nil {
		switch ContinueVerdict(strings.ToUpper(strings.TrimSpace(state.ResumePoint.Verdict))) {
		case VerdictResume:
			return &ContinueDecision{
				Verdict:     VerdictResume,
				StartSprint: state.ResumePoint.Sprint,
				Reason:      state.ResumePoint.Reason,
			}
		case VerdictContinueNext:
			return &ContinueDecision{
				Verdict:     VerdictContinueNext,
				StartSprint: state.ResumePoint.Sprint + 1,
				Reason:      state.ResumePoint.Reason,
			}
		case VerdictAuditIncomplete:
			return &ContinueDecision{
				Verdict:     VerdictAuditIncomplete,
				StartSprint: state.TotalSprints,
				Reason:      state.ResumePoint.Reason,
			}
		}
	}

	completed := make(map[int]bool, len(state.CompletedSprints))
	for _, s := range state.CompletedSprints {
		completed[s.Number] = true
	}

	for i := 1; i <= state.TotalSprints; i++ {
		if !completed[i] {
			return &ContinueDecision{
				Verdict:     VerdictResume,
				StartSprint: i,
				Reason:      "heuristic continue: first incomplete sprint",
			}
		}
	}

	if !state.BuildAuditComplete && state.AuditConfigured {
		return &ContinueDecision{
			Verdict:     VerdictAuditIncomplete,
			StartSprint: state.TotalSprints,
			Reason:      "heuristic continue: all sprints completed but build audit did not finish",
		}
	}

	return &ContinueDecision{
		Verdict:     VerdictAllComplete,
		StartSprint: state.TotalSprints,
		Reason:      "heuristic continue: all sprints completed",
	}
}

// ParseDecision extracts a ContinueDecision from LLM output.
func ParseDecision(output string, totalSprints int) *ContinueDecision {
	decision := &ContinueDecision{
		Verdict: VerdictBlocked,
		Reason:  "could not parse agent decision",
	}

	var parsed continueJSON
	if err := textutil.ExtractJSON(output, &parsed); err == nil {
		v := ContinueVerdict(strings.ToUpper(strings.TrimSpace(parsed.Verdict)))
		switch v {
		case VerdictResume, VerdictResumeFresh, VerdictContinueNext,
			VerdictAllComplete, VerdictAuditIncomplete, VerdictBlocked:
			decision.Verdict = v
		}
		if parsed.Sprint >= 1 && parsed.Sprint <= totalSprints {
			decision.StartSprint = parsed.Sprint
		}
		if r := strings.TrimSpace(parsed.Reason); r != "" {
			decision.Reason = r
		}
	}

	// Extract preconditions from markdown checklist (format-agnostic)
	decision.Preconditions = parsePreconditions(output)

	return decision
}

// parsePreconditions extracts "- [ ] ..." lines from the output.
func parsePreconditions(output string) []string {
	var items []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ] ") {
			items = append(items, strings.TrimPrefix(trimmed, "- [ ] "))
		}
	}
	return items
}

func buildAnalysisPrompt(state *BuildState, report string) string {
	var b strings.Builder

	b.WriteString("# Continue Analysis — Build Resume Decision\n\n")
	b.WriteString("## Your Role\n")
	b.WriteString("You are a build analyst. Review the build state report below and decide\n")
	b.WriteString("how the build should be resumed. Do NOT modify any source code.\n\n")

	b.WriteString("## Build State Report\n\n")
	b.WriteString(report)
	b.WriteString("\n\n")

	b.WriteString("## Decision Options\n\n")
	b.WriteString("| Verdict | When to use |\n")
	b.WriteString("|---|---|\n")
	b.WriteString("| RESUME | Sprint was partially completed — code work exists but sanity checks failed. Skip to sanity checks + alignment. |\n")
	b.WriteString("| RESUME_FRESH | Sprint needs a full re-run from scratch (e.g., work was corrupted or insufficient). |\n")
	b.WriteString("| CONTINUE_NEXT | The active sprint is actually done but wasn't recorded as complete. Start the next unstarted sprint. |\n")
	b.WriteString("| ALL_COMPLETE | All sprints have passed. Nothing to do. |\n")
	b.WriteString("| AUDIT_INCOMPLETE | All sprints passed but the build audit sentinel is absent — resume from build audit. |\n")
	b.WriteString("| BLOCKED | Cannot continue without user action (e.g., Docker not running, missing tools). |\n\n")

	b.WriteString("## Important Guidelines\n\n")
	b.WriteString("- If the last failure was an environment issue (Docker not running, missing tool) and the\n")
	b.WriteString("  environment is now fixed, recommend RESUME so sanity checks re-run.\n")
	b.WriteString("- If the environment issue is still present, recommend BLOCKED with preconditions.\n")
	b.WriteString("- If partial work exists (iterations ran, code was written) but sanity checks failed\n")
	b.WriteString("  for code reasons, recommend RESUME to attempt alignment.\n")
	b.WriteString("- If no work exists for the next sprint, recommend RESUME_FRESH.\n")
	b.WriteString("- If there's evidence of successful iterations but no PASS recorded, check\n")
	b.WriteString("  whether the work looks complete and recommend RESUME or CONTINUE_NEXT.\n\n")

	b.WriteString("## Output Format\n\n")
	b.WriteString("Write your analysis to .fry/continue-decision.txt in EXACTLY this format:\n\n")
	b.WriteString("```\n")
	b.WriteString("## Analysis\n")
	b.WriteString("<2-5 sentences about what happened and why the build stopped>\n\n")
	b.WriteString("## Decision\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\"verdict\": \"VERDICT_HERE\", \"sprint\": N, \"reason\": \"1-2 sentence explanation\"}\n")
	b.WriteString("```\n\n")
	b.WriteString("## Pre-conditions\n")
	b.WriteString("- [ ] Action the user must take (if any)\n\n")
	b.WriteString("## Recommended Command\n")
	b.WriteString("fry run --resume --sprint N  (or whatever is appropriate)\n")
	b.WriteString("```\n")

	return b.String()
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return textutil.TruncateUTF8(s, max)
}
