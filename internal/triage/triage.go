package triage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/prepare"
	"github.com/yevgetman/fry/internal/textutil"
	"github.com/yevgetman/fry/templates"
)

// TriageOpts configures the triage classifier.
type TriageOpts struct {
	ProjectDir      string
	UserPrompt      string
	PlanContent     string // contents of plans/plan.md, may be empty
	ExecContent     string // contents of plans/executive.md, may be empty
	CodebaseContent string // contents of .fry-config/codebase.md, may be empty
	Engine          engine.Engine
	Model           string
	EffortLevel     string
	Mode            prepare.Mode
	Verbose         bool
}

// triageJSON is the expected JSON structure from the triage classifier.
type triageJSON struct {
	Complexity string `json:"complexity"`
	Effort     string `json:"effort"`
	Sprints    int    `json:"sprints"`
	Reason     string `json:"reason"`
}

// Classify runs a single cheap LLM call to classify task complexity.
// On any error (parse failure, LLM error, timeout), it returns ComplexityComplex
// to fail safe — better to over-prepare than under-prepare.
func Classify(ctx context.Context, opts TriageOpts) *TriageDecision {
	if opts.Engine == nil {
		frylog.Log("WARNING: triage: no engine provided; defaulting to COMPLEX")
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "no engine available"}
	}

	triagePrompt, err := templates.LoadText(config.TriageInvocationFile)
	if err != nil {
		frylog.Log("WARNING: triage: %v; defaulting to COMPLEX", err)
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "prompt load error"}
	}

	prompt := buildTriagePrompt(opts)

	// Write prompt to disk for user inspection.
	promptPath := filepath.Join(opts.ProjectDir, config.TriagePromptFile)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		frylog.Log("WARNING: triage: could not create dir for prompt: %v", err)
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "filesystem error"}
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		frylog.Log("WARNING: triage: could not write prompt file: %v", err)
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "filesystem error"}
	}

	// Create log file.
	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		frylog.Log("WARNING: triage: could not create logs dir: %v", err)
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "filesystem error"}
	}
	logPath := filepath.Join(buildLogsDir, fmt.Sprintf("triage_%s.log", time.Now().Format("20060102_150405")))

	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionTriage,
		EffortLevel: opts.EffortLevel,
		WorkDir:     opts.ProjectDir,
	}
	logFile, logErr := os.Create(logPath)
	if logErr == nil {
		defer logFile.Close()
		runOpts.Stdout = logFile
		runOpts.Stderr = logFile
	}

	// Remove stale decision file from a prior run so it doesn't get read back
	// if the current engine invocation writes output elsewhere.
	decisionPath := filepath.Join(opts.ProjectDir, config.TriageDecisionFile)
	if err := os.Remove(decisionPath); err != nil && !os.IsNotExist(err) {
		frylog.Log("WARNING: triage: remove stale decision: %v", err)
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "filesystem error"}
	}

	frylog.Log("▶ TRIAGE  classifying task complexity...  engine=%s  model=%s", opts.Engine.Name(), opts.Model)

	output, _, runErr := opts.Engine.Run(ctx, triagePrompt, runOpts)
	if runErr != nil && ctx.Err() != nil {
		frylog.Log("WARNING: triage: context cancelled; defaulting to COMPLEX")
		return &TriageDecision{Complexity: ComplexityComplex, Reason: "context cancelled"}
	}
	if runErr != nil {
		frylog.Log("WARNING: triage: engine exited with error (non-fatal): %v", runErr)
	}

	// Check if the agent wrote a decision file (may exist even if engine returned non-zero exit).
	if data, err := os.ReadFile(decisionPath); err == nil {
		output = string(data)
	}

	// If no output at all after error, default to COMPLEX.
	if strings.TrimSpace(output) == "" && runErr != nil {
		frylog.Log("WARNING: triage: no output from classifier; defaulting to COMPLEX")
		return &TriageDecision{Complexity: ComplexityComplex, Reason: fmt.Sprintf("engine error with no output: %v", runErr)}
	}

	decision := ParseClassification(output)

	effortStr := "auto"
	if decision.EffortLevel != "" {
		effortStr = string(decision.EffortLevel)
	}
	frylog.Log("▶ TRIAGE  result: %s  effort=%s  sprints=%d — %s", decision.Complexity, effortStr, decision.SprintCount, truncateReason(decision.Reason, 80))

	return decision
}

// ParseClassification extracts a TriageDecision from LLM output.
// Any parse failure defaults to ComplexityComplex.
func ParseClassification(output string) *TriageDecision {
	decision := &TriageDecision{
		Complexity: ComplexityComplex,
		Reason:     "could not parse classifier output",
	}

	var parsed triageJSON
	if err := textutil.ExtractJSON(output, &parsed); err != nil {
		return decision
	}

	c := Complexity(strings.ToUpper(strings.TrimSpace(parsed.Complexity)))
	switch c {
	case ComplexitySimple, ComplexityModerate, ComplexityComplex:
		decision.Complexity = c
	default:
		return decision
	}

	if parsed.Sprints >= 0 {
		decision.SprintCount = parsed.Sprints
	}

	if lvl, err := epic.ParseEffortLevel(parsed.Effort); err == nil {
		decision.EffortLevel = lvl
	}

	if r := strings.TrimSpace(parsed.Reason); r != "" {
		decision.Reason = r
	}

	return decision
}

func buildTriagePrompt(opts TriageOpts) string {
	var b strings.Builder

	b.WriteString("# Task Complexity Triage\n\n")
	b.WriteString("You are a task complexity classifier. Given a project description, classify the work\n")
	b.WriteString("into exactly one of three categories.\n\n")

	b.WriteString("## Classification Rules\n\n")
	b.WriteString("| Category | When to use | Sprint count |\n")
	b.WriteString("|----------|-------------|---------------|\n")

	switch opts.Mode {
	case prepare.ModePlanning:
		b.WriteString("| SIMPLE   | Single deliverable: one-page brief, short analysis, simple outline. | 1 |\n")
		b.WriteString("| MODERATE | Multi-section document: research report, strategy doc, detailed plan. | 1-2 |\n")
		b.WriteString("| COMPLEX  | Multi-document project, cross-referenced research, full strategic planning suite. | 0 (defer to full prepare) |\n\n")
	case prepare.ModeWriting:
		b.WriteString("| SIMPLE   | Short-form writing: blog post, single essay, README, changelog entry. | 1 |\n")
		b.WriteString("| MODERATE | Medium-form: article series, tutorial with examples, landing page copy. | 1-2 |\n")
		b.WriteString("| COMPLEX  | Long-form: book chapter, documentation site, multi-part content suite. | 0 (defer to full prepare) |\n\n")
	default: // software
		b.WriteString("| SIMPLE   | Single well-bounded task: 1-3 files, no new integrations, no database, no API design, no testing framework setup. Examples: add a CLI flag, fix a bug in one function, update a config, write a short script. | 1 |\n")
		b.WriteString("| MODERATE | Multi-file feature with some integration: 4-10 files, a few connected pieces but no complex architecture. Examples: add a REST endpoint with tests, create a small CLI tool, build a form with validation. | 1-2 |\n")
		b.WriteString("| COMPLEX  | Anything else. Multiple subsystems, databases, APIs, substantial testing infrastructure, Docker, CI/CD, architectural decisions, or any uncertainty about scope. | 0 (defer to full prepare) |\n\n")
	}

	b.WriteString("## CRITICAL BIAS RULE\n\n")
	b.WriteString("When in doubt, ALWAYS classify as COMPLEX. A complex task misidentified as simple wastes\n")
	b.WriteString("far more tokens than a simple task getting full preparation. Err heavily on the side of COMPLEX.\n\n")

	b.WriteString("Specific signals that MUST push toward COMPLEX:\n")
	b.WriteString("- Any mention of database, ORM, migrations, or schema\n")
	b.WriteString("- Any mention of Docker, CI/CD, or deployment\n")
	b.WriteString("- Any mention of authentication, authorization, or security\n")
	b.WriteString("- Any mention of multiple services or microservices\n")
	b.WriteString("- Any mention of an existing large codebase to integrate with\n")
	b.WriteString("- Task description is vague, underspecified, or ambiguous\n")
	b.WriteString("- More than 10 files expected to change\n")
	b.WriteString("- Multiple programming languages involved\n")
	b.WriteString("- Testing infrastructure needs to be set up from scratch\n")
	b.WriteString("- Any new package or module with its own public API\n")
	b.WriteString("- Refactoring that spans multiple subsystems\n")
	b.WriteString("- The task mentions \"architecture\" or \"design\"\n\n")

	b.WriteString("## Effort Level Guidelines\n\n")
	b.WriteString("In addition to complexity, suggest an effort level for the task.\n")
	b.WriteString("Valid effort levels for SIMPLE and MODERATE tasks are: fast, standard, high.\n")
	b.WriteString("(\"max\" is reserved for COMPLEX tasks only.)\n\n")
	b.WriteString("| Effort | When to use |\n")
	b.WriteString("|--------|-------------|\n")
	b.WriteString("| fast     | Minimal scope: a typo fix, config tweak, single-file change with no tests needed. |\n")
	b.WriteString("| standard | Standard scope: multi-file change, tests expected, moderate integration. |\n")
	b.WriteString("| high   | Demanding scope: many files, thorough testing, edge cases matter, quality is critical. |\n\n")
	b.WriteString("When in doubt, prefer standard. Only suggest fast for truly trivial tasks. Suggest high when\n")
	b.WriteString("quality and thoroughness are explicitly important or the task is near the boundary of COMPLEX.\n\n")

	b.WriteString("## Project Inputs\n\n")

	hasInput := false
	if strings.TrimSpace(opts.PlanContent) != "" {
		b.WriteString("### Build Plan\n\n")
		b.WriteString(opts.PlanContent)
		b.WriteString("\n\n")
		hasInput = true
	}
	if strings.TrimSpace(opts.ExecContent) != "" {
		b.WriteString("### Executive Context\n\n")
		b.WriteString(opts.ExecContent)
		b.WriteString("\n\n")
		hasInput = true
	}
	if strings.TrimSpace(opts.UserPrompt) != "" {
		b.WriteString("### User Directive\n\n")
		b.WriteString(opts.UserPrompt)
		b.WriteString("\n\n")
		hasInput = true
	}
	if strings.TrimSpace(opts.CodebaseContent) != "" {
		b.WriteString("### Existing Codebase\n\n")
		b.WriteString("This task modifies an existing codebase. Consider the codebase's size, complexity,\n")
		b.WriteString("and patterns when classifying. Tasks that integrate with established code are often\n")
		b.WriteString("more complex than equivalent greenfield tasks.\n\n")
		b.WriteString(opts.CodebaseContent)
		b.WriteString("\n\n")
		hasInput = true
	}
	if !hasInput {
		b.WriteString("(No project inputs available — classify as COMPLEX.)\n\n")
	}

	b.WriteString("## Output Format\n\n")
	b.WriteString("Write your classification to .fry/triage-decision.txt as a single JSON object — no other text:\n\n")
	b.WriteString("```json\n")
	b.WriteString("{\n")
	b.WriteString("  \"complexity\": \"SIMPLE|MODERATE|COMPLEX\",\n")
	b.WriteString("  \"effort\": \"fast|standard|high\",\n")
	b.WriteString("  \"sprints\": N,\n")
	b.WriteString("  \"reason\": \"1-2 sentence justification\"\n")
	b.WriteString("}\n")
	b.WriteString("```\n")

	return b.String()
}

func truncateReason(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
