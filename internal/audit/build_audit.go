package audit

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
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/review"
	"github.com/yevgetman/fry/internal/sprint"
	"github.com/yevgetman/fry/internal/textutil"
	"github.com/yevgetman/fry/templates"
)

const (
	maxBuildAuditExecutiveBytes  = 5_000
	maxBuildAuditPlanBytes       = 50_000
	maxBuildAuditEpicBytes       = 50_000
	maxBuildAuditUserPromptBytes = 2_000
	maxBuildAuditDeviationBytes  = 10_000
	maxBuildAuditPromptBytes     = 200_000
)

type BuildAuditOpts struct {
	ProjectDir       string
	Epic             *epic.Epic
	Engine           engine.Engine
	Results          []sprint.SprintResult
	Verbose          bool
	Model            string
	DeferredFailures string // content of .fry/deferred-failures.md
	Mode             string
	Stdout           io.Writer // optional; defaults to os.Stdout when Verbose is true
}

// RunBuildAudit performs a final holistic audit of the entire codebase after
// all sprints have completed. It launches a single agent session that iteratively
// audits, classifies, reports, and remediates issues across the full build.
// The returned AuditResult contains structured findings parsed from the agent's
// report, enabling downstream consumers (e.g., build summary) to include audit results.
func RunBuildAudit(ctx context.Context, opts BuildAuditOpts) (*AuditResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if opts.Epic == nil {
		return nil, fmt.Errorf("run build audit: epic is required")
	}
	if opts.Engine == nil {
		return nil, fmt.Errorf("run build audit: engine is required")
	}

	buildAuditPrompt, err := templates.LoadText(config.BuildAuditInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("run build audit: %w", err)
	}

	frylog.Log("▶ BUILD AUDIT  running final holistic audit for %q", opts.Epic.Name)

	prompt := buildBuildAuditPrompt(opts)

	// Write prompt file
	promptPath := filepath.Join(opts.ProjectDir, config.BuildAuditPromptFile)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return nil, fmt.Errorf("run build audit: create dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return nil, fmt.Errorf("run build audit: write prompt: %w", err)
	}
	// Prompt file is preserved for resumability — a later retry can
	// re-invoke the same prompt without reconstructing it from raw logs.

	// Create log file
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("run build audit: create logs dir: %w", err)
	}
	logPath := filepath.Join(buildLogsDir,
		fmt.Sprintf("build_audit_%s.log", time.Now().Format("20060102_150405")),
	)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("run build audit: create log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionBuildAudit,
		EffortLevel: string(opts.Epic.EffortLevel),
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

	output, _, runErr := opts.Engine.Run(ctx, buildAuditPrompt, runOpts)
	if runErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("run build audit: agent run: %w", runErr)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Parse audit results from the report file
	auditPath := filepath.Join(opts.ProjectDir, config.BuildAuditFile)
	content, readErr := readAuditOutput(
		auditPath,
		config.BuildAuditFile,
		"build audit session",
		"BUILD AUDIT",
		"run build audit",
		opts.ProjectDir,
		output,
		logPath,
	)
	if readErr != nil {
		return nil, readErr
	}

	frylog.Log("  BUILD AUDIT: report written to %s", config.BuildAuditFile)

	// Parse structured results from the report
	maxSev := parseAuditSeverity(string(content))
	counts := countAuditSeverities(string(content))
	findings := parseFindings(string(content))

	return &AuditResult{
		Passed:             isAuditPass(maxSev),
		Blocking:           isBlockingSeverity(maxSev),
		Iterations:         1,
		MaxSeverity:        maxSev,
		SeverityCounts:     counts,
		UnresolvedFindings: findings,
	}, nil
}

func buildBuildAuditPrompt(opts BuildAuditOpts) string {
	var (
		introSection         strings.Builder
		codebaseSection      strings.Builder
		executiveSection     string
		planSection          string
		epicSection          string
		userPromptSection    string
		scopeSection         strings.Builder
		deferredSection      string
		deviationSection     string
		crossDocumentSection strings.Builder
		instructionsSection  strings.Builder
	)

	introSection.WriteString("# FINAL BUILD AUDIT\n\n")
	introSection.WriteString("You are reviewing this document corpus for the first time.\n")
	introSection.WriteString("You have no knowledge of how it was produced, how many iterations it went through, or what prior reviewers found.\n\n")
	introSection.WriteString("Your primary job: find contradictions, inconsistencies, and gaps — especially across documents that reference each other's figures, assumptions, or conclusions. Use the repository as primary evidence.\n\n")
	introSection.WriteString("Treat generated progress artifacts as supporting context, not proof.\n\n")

	appendCodebaseContext(&codebaseSection, opts.ProjectDir)

	executivePath := filepath.Join(opts.ProjectDir, config.ExecutiveFile)
	if data, err := os.ReadFile(executivePath); err == nil {
		content := string(data)
		if len(content) > maxBuildAuditExecutiveBytes {
			content = textutil.TruncateUTF8(content, maxBuildAuditExecutiveBytes) + "\n...(truncated)"
		}
		executiveSection = "## Project Context (executive.md)\n" + content + "\n\n"
	}

	planPath := filepath.Join(opts.ProjectDir, config.PlanFile)
	if data, err := os.ReadFile(planPath); err == nil {
		content := string(data)
		if len(content) > maxBuildAuditPlanBytes {
			content = textutil.TruncateUTF8(content, maxBuildAuditPlanBytes) + "\n...(truncated)"
		}
		planSection = "## Implementation Plan (plan.md)\n" + content + "\n\n"
	}

	epicPath := filepath.Join(opts.ProjectDir, config.FryDir, "epic.md")
	if data, err := os.ReadFile(epicPath); err == nil {
		content := string(data)
		if len(content) > maxBuildAuditEpicBytes {
			content = textutil.TruncateUTF8(content, maxBuildAuditEpicBytes) + "\n...(truncated)"
		}
		epicSection = "## Epic Definition (epic.md)\n```\n" + content + "\n```\n\n"
	}

	userPromptPath := filepath.Join(opts.ProjectDir, config.UserPromptFile)
	if data, err := os.ReadFile(userPromptPath); err == nil && len(data) > 0 {
		content := string(data)
		if len(content) > maxBuildAuditUserPromptBytes {
			content = textutil.TruncateUTF8(content, maxBuildAuditUserPromptBytes) + "\n...(truncated)"
		}
		userPromptSection = "## Original User Prompt\n" + content + "\n\n"
	}

	scopeSection.WriteString("## Build Scope\n\n")
	scopeSection.WriteString("| Sprint | Name |\n")
	scopeSection.WriteString("|--------|------|\n")
	for _, r := range opts.Results {
		fmt.Fprintf(&scopeSection, "| %d | %s |\n", r.Number, r.Name)
	}
	scopeSection.WriteString("\n")

	deferredSection = RenderDeferredAnalysis(AnalyzeDeferredFailures(opts.DeferredFailures))
	if devLog, err := review.ReadDeviationLog(opts.ProjectDir); err == nil && strings.TrimSpace(devLog) != "" {
		devLog = strings.TrimSpace(devLog)
		if len(devLog) > maxBuildAuditDeviationBytes {
			devLog = textutil.TruncateUTF8(devLog, maxBuildAuditDeviationBytes) + "\n...(deviation log truncated)"
		}
		deviationSection = "## Intentional Divergences Log\n\n" +
			"The following cross-document differences were intentionally applied during the build.\n" +
			"Do not flag these as contradictions unless the underlying assumption has changed.\n\n" +
			devLog + "\n\n"
	}

	crossDocumentSection.WriteString("## Cross-Document Integrity\n\n")
	crossDocumentSection.WriteString("Pay special attention to:\n")
	crossDocumentSection.WriteString("- Numerical assumptions that appear in multiple documents (costs, rates, timelines)\n")
	crossDocumentSection.WriteString("- Conclusions in one document that depend on analysis in another\n")
	crossDocumentSection.WriteString("- Figures quoted in summaries that may not match their source calculations\n")
	crossDocumentSection.WriteString("- Terminology or definitions used inconsistently across documents\n\n")

	instructionsSection.WriteString("---\n\n")
	instructionsSection.WriteString("## Instructions\n\n")
	iterCap := config.MaxOuterCyclesHighCap
	if opts.Epic != nil && opts.Epic.EffortLevel == epic.EffortMax {
		iterCap = config.MaxOuterCyclesMaxCap
	}
	fmt.Fprintf(&instructionsSection, "Repeat the following cycle until the EXIT CONDITION is met (max %d iterations).\n\n", iterCap)

	instructionsSection.WriteString("### Step 1 — Audit\n\n")
	if opts.Mode == "writing" {
		instructionsSection.WriteString("Meticulously audit all written content against these criteria:\n\n")
		instructionsSection.WriteString("- **Coherence** — Content flows logically and tells a consistent story throughout.\n")
		instructionsSection.WriteString("- **Accuracy** — Factual claims are correct and properly supported.\n")
		instructionsSection.WriteString("- **Completeness** — All required topics are covered at sufficient depth.\n")
		instructionsSection.WriteString("- **Tone & Voice** — Writing voice is consistent and appropriate for the audience.\n")
		instructionsSection.WriteString("- **Structure** — Sections are well-organized with clear headings and transitions.\n")
		instructionsSection.WriteString("- **Depth** — Content is substantive rather than superficial or padded.\n\n")

		instructionsSection.WriteString("### Step 2 — Classify\n\n")
		instructionsSection.WriteString("Assign every finding a severity:\n\n")
		instructionsSection.WriteString("| Severity | Definition |\n")
		instructionsSection.WriteString("|----------|------------|\n")
		instructionsSection.WriteString("| CRITICAL | Factual errors, contradictions, or missing core content |\n")
		instructionsSection.WriteString("| HIGH | Major structural problems or significant gaps in coverage |\n")
		instructionsSection.WriteString("| MODERATE | Weak transitions, inconsistent voice, or shallow treatment |\n")
		instructionsSection.WriteString("| LOW | Minor style, formatting, or word choice issues |\n\n")
	} else {
		instructionsSection.WriteString("Meticulously audit the entire codebase against these criteria:\n\n")
		instructionsSection.WriteString("- **Correctness** — Code is coherent with the aim and function of the application; no bugs.\n")
		instructionsSection.WriteString("- **Usability** — No UX friction, confusing flows, or accessibility gaps.\n")
		instructionsSection.WriteString("- **Edge cases** — Boundary conditions, empty states, invalid input, and race conditions are handled.\n")
		instructionsSection.WriteString("- **Security** — No vulnerabilities (injection, auth flaws, data exposure, etc.).\n")
		instructionsSection.WriteString("- **Performance** — No bottlenecks, memory leaks, or unnecessary complexity.\n")
		instructionsSection.WriteString("- **Code quality** — Clean style, consistent patterns, clear naming, appropriate abstractions.\n\n")

		instructionsSection.WriteString("### Step 2 — Classify\n\n")
		instructionsSection.WriteString("Assign every finding a severity:\n\n")
		instructionsSection.WriteString("| Severity | Definition |\n")
		instructionsSection.WriteString("|----------|------------|\n")
		instructionsSection.WriteString("| CRITICAL | Data loss, security breach, or crash under normal use |\n")
		instructionsSection.WriteString("| HIGH | Significant bug or vulnerability; affects core functionality |\n")
		instructionsSection.WriteString("| MODERATE | Noticeable issue; degraded experience or maintainability risk |\n")
		instructionsSection.WriteString("| LOW | Minor style, naming, or cosmetic concern |\n\n")
	}

	instructionsSection.WriteString("### Step 3 — Report\n\n")
	fmt.Fprintf(&instructionsSection, "Save a comprehensive report to `%s` in the project root. For each finding include: location, description, severity, and recommended fix.\n\n", config.BuildAuditFile)

	instructionsSection.WriteString("### Step 4 — Evaluate exit condition\n\n")
	fmt.Fprintf(&instructionsSection, "- **If no issues exist, or all remaining issues are LOW severity → save the final report to `%s` and stop.** ← EXIT CONDITION\n", config.BuildAuditFile)
	instructionsSection.WriteString("- **Otherwise → continue to Step 5.**\n\n")

	instructionsSection.WriteString("### Step 5 — Remediate\n\n")
	fmt.Fprintf(&instructionsSection, "Using `%s` as your plan, fix **all** issues (including LOW severity). Also fix any deferred sanity check failures listed above. Then return to **Step 1** and re-audit the modified codebase.\n\n", config.BuildAuditFile)
	instructionsSection.WriteString("Before returning to Step 1, re-read each document from the beginning as if seeing it for the first time. Do not assume your prior fixes were correct.\n\n")

	instructionsSection.WriteString("---\n\n")
	instructionsSection.WriteString("### Guardrails\n\n")
	fmt.Fprintf(&instructionsSection, "- Each iteration must produce a fresh `%s` that reflects the *current* state of the code.\n", config.BuildAuditFile)
	instructionsSection.WriteString("- Do not skip issues to force an early exit; report honestly.\n")
	fmt.Fprintf(&instructionsSection, "- If you reach %d iterations without meeting the exit condition, stop and report the remaining issues with an explanation of why they persist.\n", iterCap)

	totalLen := introSection.Len() + codebaseSection.Len() + len(executiveSection) + len(planSection) +
		len(epicSection) + len(userPromptSection) + scopeSection.Len() + len(deferredSection) +
		len(deviationSection) + crossDocumentSection.Len() + instructionsSection.Len()
	if overflow := totalLen - maxBuildAuditPromptBytes; overflow > 0 {
		deviationSection = truncatePromptSection(deviationSection, overflow, 2_000, "deviation log")
		totalLen = introSection.Len() + codebaseSection.Len() + len(executiveSection) + len(planSection) +
			len(epicSection) + len(userPromptSection) + scopeSection.Len() + len(deferredSection) +
			len(deviationSection) + crossDocumentSection.Len() + instructionsSection.Len()
	}
	if overflow := totalLen - maxBuildAuditPromptBytes; overflow > 0 {
		deferredSection = truncatePromptSection(deferredSection, overflow, 4_000, "deferred analysis")
		totalLen = introSection.Len() + codebaseSection.Len() + len(executiveSection) + len(planSection) +
			len(epicSection) + len(userPromptSection) + scopeSection.Len() + len(deferredSection) +
			len(deviationSection) + crossDocumentSection.Len() + instructionsSection.Len()
	}
	if overflow := totalLen - maxBuildAuditPromptBytes; overflow > 0 {
		planSection = truncatePromptSection(planSection, overflow, 8_000, "implementation plan")
	}

	var b strings.Builder
	b.WriteString(introSection.String())
	b.WriteString(codebaseSection.String())
	b.WriteString(executiveSection)
	b.WriteString(planSection)
	b.WriteString(epicSection)
	b.WriteString(userPromptSection)
	b.WriteString(scopeSection.String())
	b.WriteString(deferredSection)
	b.WriteString(deviationSection)
	b.WriteString(crossDocumentSection.String())
	b.WriteString(instructionsSection.String())
	return b.String()
}

func truncatePromptSection(section string, overflow int, minBytes int, label string) string {
	if overflow <= 0 || section == "" || len(section) <= minBytes {
		return section
	}
	newLen := len(section) - overflow
	if newLen < minBytes {
		newLen = minBytes
	}
	if newLen >= len(section) {
		return section
	}
	return textutil.TruncateUTF8(section, newLen) + "\n...(" + label + " truncated to stay under prompt cap)\n\n"
}
