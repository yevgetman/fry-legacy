package summary

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/audit"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/sprint"
	"github.com/yevgetman/fry/internal/textutil"
)

const (
	maxLogBytes      = 30_000  // per-log-file cap to keep prompt bounded
	maxTotalLogCap   = 200_000 // total log material cap
	maxEpicFileBytes = 50_000
)

type SummaryOpts struct {
	ProjectDir       string
	EpicName         string
	Engine           engine.Engine
	Results          []sprint.SprintResult
	EffortLevel      string
	Verbose          bool
	Model            string
	BuildAuditResult *audit.AuditResult // nil if build audit was skipped or failed
	Stdout           io.Writer          // optional; defaults to os.Stdout when Verbose is true
}

// GenerateBuildSummary invokes a separate agent session that reads all build
// artifacts and logs, then writes a comprehensive build-summary.md to the
// project root.
func GenerateBuildSummary(ctx context.Context, opts SummaryOpts) error {
	if opts.Engine == nil {
		return fmt.Errorf("generate build summary: engine is required")
	}

	frylog.Log("▶ SUMMARY  generating build summary for %q", opts.EpicName)

	prompt := buildSummaryPrompt(opts)

	// Write prompt file
	promptPath := filepath.Join(opts.ProjectDir, config.SummaryPromptFile)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return fmt.Errorf("generate build summary: create dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("generate build summary: write prompt: %w", err)
	}

	// Create log file for this agent session
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return fmt.Errorf("generate build summary: create logs dir: %w", err)
	}
	logPath := filepath.Join(buildLogsDir,
		fmt.Sprintf("summary_%s.log", time.Now().Format("20060102_150405")),
	)
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("generate build summary: create log: %w", err)
	}
	defer logFile.Close()

	invocationPrompt := "Read and execute ALL instructions in " + config.SummaryPromptFile + ". Generate a comprehensive build summary and write it to " + config.SummaryFile + " in the project root. Overwrite the file if it already exists."

	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionBuildSummary,
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

	_, _, runErr := opts.Engine.Run(ctx, invocationPrompt, runOpts)
	if runErr != nil && ctx.Err() == nil {
		// Agent exited non-zero but context wasn't cancelled — log warning and continue
		frylog.Log("  SUMMARY: agent exited with error (non-fatal): %v", runErr)
	} else if runErr != nil {
		return fmt.Errorf("generate build summary: %w", runErr)
	}

	// Verify the summary was actually written
	summaryPath := filepath.Join(opts.ProjectDir, config.SummaryFile)
	if _, err := os.Stat(summaryPath); err != nil {
		if os.IsNotExist(err) {
			frylog.Log("  SUMMARY: WARNING — agent did not produce %s", config.SummaryFile)
			return nil
		}
		return fmt.Errorf("check summary file: %w", err)
	}

	frylog.Log("  SUMMARY: build summary written to %s", config.SummaryFile)

	// Prompt file is preserved for resumability — a later retry can
	// re-invoke the same prompt without reconstructing it from raw logs.

	return nil
}

func buildSummaryPrompt(opts SummaryOpts) string {
	var b strings.Builder

	b.WriteString("# BUILD SUMMARY GENERATION\n\n")
	b.WriteString(fmt.Sprintf("Generate a comprehensive build summary for epic: **%s**\n\n", opts.EpicName))

	b.WriteString("## Your Role\n")
	b.WriteString("You are a build report generator. Read all provided build artifacts and logs,\n")
	b.WriteString("then produce a detailed build summary document.\n")
	b.WriteString("Do NOT modify any source code. Only write the summary file.\n\n")

	// Sprint results table
	b.WriteString("## Sprint Results\n\n")
	b.WriteString("| Sprint | Name | Status | Duration | Audit |\n")
	b.WriteString("|--------|------|--------|----------|-------|\n")
	for _, r := range opts.Results {
		audit := ""
		if r.AuditWarning != "" {
			audit = r.AuditWarning
		}
		b.WriteString(fmt.Sprintf("| %d | %s | %s | %s | %s |\n",
			r.Number, r.Name, r.Status, r.Duration.Round(time.Second), audit))
	}
	b.WriteString("\n")

	// Build audit results
	if opts.BuildAuditResult != nil {
		r := opts.BuildAuditResult
		b.WriteString("## Build Audit Results\n\n")
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("- **Status:** %s\n", status))
		if r.MaxSeverity != "" {
			b.WriteString(fmt.Sprintf("- **Max Severity:** %s\n", r.MaxSeverity))
		}
		b.WriteString(fmt.Sprintf("- **Severity Counts:** %s\n", audit.FormatCounts(r.SeverityCounts)))
		if r.Blocking {
			b.WriteString("- **Blocking:** yes (CRITICAL or HIGH issues remain)\n")
		}
		if len(r.UnresolvedFindings) > 0 {
			b.WriteString("\n### Unresolved Findings\n\n")
			for i, f := range r.UnresolvedFindings {
				loc := f.Location
				if loc == "" {
					loc = "(no location)"
				}
				b.WriteString(fmt.Sprintf("%d. [%s] %s (**%s**)\n", i+1, loc, f.Description, f.Severity))
			}
		}
		b.WriteString("\n")
	}

	// Epic file
	epicPath := filepath.Join(opts.ProjectDir, config.FryDir, "epic.md")
	if data, err := os.ReadFile(epicPath); err == nil {
		content := string(data)
		if len(content) > maxEpicFileBytes {
			content = textutil.TruncateUTF8(content, maxEpicFileBytes) + "\n...(truncated)"
		}
		b.WriteString("## Epic Definition\n")
		b.WriteString("```\n")
		b.WriteString(content)
		b.WriteString("\n```\n\n")
	}

	// Epic progress (cross-sprint summaries)
	epicProgressPath := filepath.Join(opts.ProjectDir, config.EpicProgressFile)
	if data, err := os.ReadFile(epicProgressPath); err == nil && len(data) > 0 {
		b.WriteString("## Epic Progress (Cross-Sprint Summaries)\n")
		b.WriteString(string(data))
		b.WriteString("\n\n")
	}

	// Deferred sanity check failures
	deferredPath := filepath.Join(opts.ProjectDir, config.DeferredFailuresFile)
	if data, err := os.ReadFile(deferredPath); err == nil && len(data) > 0 {
		b.WriteString("## Deferred Sanity Check Failures\n")
		b.WriteString(string(data))
		b.WriteString("\n\n")
	}

	// Deviation log
	deviationPath := filepath.Join(opts.ProjectDir, config.DeviationLogFile)
	if data, err := os.ReadFile(deviationPath); err == nil && len(data) > 0 {
		b.WriteString("## Deviation Log\n")
		b.WriteString(string(data))
		b.WriteString("\n\n")
	}

	// Build logs — include all logs sorted by name, capped
	b.WriteString("## Build Logs\n\n")
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	totalLogBytes := 0
	logEntries := collectBuildLogs(buildLogsDir)
	for _, entry := range logEntries {
		if totalLogBytes >= maxTotalLogCap {
			b.WriteString("\n...(remaining logs omitted — total log cap reached)\n")
			break
		}
		content := entry.content
		if len(content) > maxLogBytes {
			content = textutil.TruncateUTF8(content, maxLogBytes) + "\n...(truncated)"
		}
		remaining := maxTotalLogCap - totalLogBytes
		if len(content) > remaining {
			content = textutil.TruncateUTF8(content, remaining) + "\n...(truncated at total cap)"
		}
		totalLogBytes += len(content)

		b.WriteString(fmt.Sprintf("### %s\n", entry.name))
		b.WriteString("```\n")
		b.WriteString(content)
		b.WriteString("\n```\n\n")
	}

	// Instructions for the output
	b.WriteString("## Output Instructions\n\n")
	b.WriteString(fmt.Sprintf("Write the build summary to `%s` in the project root.\n", config.SummaryFile))
	b.WriteString("If this file already exists, overwrite it completely.\n\n")
	b.WriteString("The summary document MUST include these sections:\n\n")
	b.WriteString("### 1. Overview\n")
	b.WriteString("- Epic name and purpose\n")
	b.WriteString("- Total sprints executed, pass/fail counts\n")
	b.WriteString("- Overall build result (success/partial/failure)\n")
	b.WriteString("- Total build duration\n\n")
	b.WriteString("### 2. What Was Built\n")
	b.WriteString("- For each sprint: what was built, key deliverables, files created/modified\n")
	b.WriteString("- Summarize the final state of the project\n\n")
	b.WriteString("### 3. Build Events\n")
	b.WriteString("- Chronological account of significant events during the build\n")
	b.WriteString("- Include: sanity check failures and how they were aligned\n")
	b.WriteString("- Include: audit findings (by severity) and their remediations\n")
	b.WriteString("- Include: any sprint review deviations and replanning decisions\n\n")
	b.WriteString("### 4. Audit & Sanity Check Report\n")
	b.WriteString("- For each sprint: list all audit findings with severity\n")
	b.WriteString("- Detail which findings were remediated and which remain\n")
	b.WriteString("- Highlight any CRITICAL or HIGH issues that blocked the build\n")
	b.WriteString("- Include the build-level audit results: overall pass/fail, severity breakdown, fixes applied, and any unresolved cross-cutting findings\n")
	b.WriteString("- List all sanity checks and their pass/fail status\n\n")
	b.WriteString("### 5. Advisory Messages\n")
	b.WriteString("- Collect ALL advisory messages, warnings, and non-blocking issues\n")
	b.WriteString("- Include MODERATE audit findings that were advisory-only\n")
	b.WriteString("- Include any alignment loop warnings or partial fixes\n")
	b.WriteString("- Include any review/deviation advisories\n\n")
	b.WriteString("### 6. Final Notes\n")
	b.WriteString("- Any recommendations for follow-up work\n")
	b.WriteString("- Known limitations or remaining issues\n\n")
	b.WriteString("Use markdown formatting. Be factual and specific — reference actual file names,\n")
	b.WriteString("error messages, and severity levels from the logs. Do not fabricate details.\n")

	return b.String()
}

type logEntry struct {
	name    string
	content string
}

func collectBuildLogs(dir string) []logEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// Sort by name (which includes timestamps, so chronological)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Bound each in-memory read so a runaway log file (engine looping on stdout
	// can produce hundreds of MB) cannot OOM the host before the prompt-builder
	// truncation runs. Read maxLogBytes+1 so the prompt builder can still detect
	// "this was truncated" via len(content) > maxLogBytes.
	var logs []logEntry
	for _, name := range names {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, maxLogBytes+1))
		_ = f.Close()
		if err != nil {
			continue
		}
		logs = append(logs, logEntry{name: name, content: string(data)})
	}
	return logs
}
