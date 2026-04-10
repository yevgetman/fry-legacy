// Package heal implements the alignment loop (post-sprint fix system).
// When sanity checks fail after a sprint, this package re-invokes the AI agent
// with targeted fix prompts. The package name is retained for import stability;
// the user-facing term is "alignment."
package heal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/agentrun"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/shellhook"
	"github.com/yevgetman/fry/internal/steering"
	"github.com/yevgetman/fry/internal/verify"
	"github.com/yevgetman/fry/templates"
)

type HealOpts struct {
	ProjectDir          string
	Sprint              *epic.Sprint
	Epic                *epic.Epic
	Engine              engine.Engine
	Checks              []verify.Check // Initial checks (used if VerificationFile is empty)
	VerificationFile    string         // When set, re-parsed each alignment attempt so on-disk edits take effect
	UserPrompt          string
	Verbose             bool
	SprintLogFile       string
	MaxAttemptsOverride int // When > 0, overrides epic/sprint max alignment attempts (used by --resume)
	MaxFailPercent      int // Percentage of checks allowed to fail while still passing
	EffortLevel         epic.EffortLevel
	Mode                string
}

type HealResult struct {
	Healed           bool                 // all sanity checks passed (aligned)
	WithinThreshold  bool                 // fail percent within allowed threshold
	DeferredFailures []verify.CheckResult // checks that failed but within threshold
	Attempts         int                  // number of alignment agent invocations
	PassCount        int
	TotalCount       int
}

// healConfig holds the resolved alignment parameters for a single alignment loop run.
type healConfig struct {
	maxAttempts     int  // hard cap on attempts (ignored when hardCap is false)
	hardCap         bool // when false, attempts are unlimited (max effort)
	progressBased   bool // exit early when no progress is detected
	stuckThreshold  int  // consecutive no-progress attempts before exit
	maxFailPercent  int  // threshold for partial pass
	minForThreshold int  // min attempts before mid-loop threshold check (max effort only)
}

// effectiveHealConfig resolves alignment parameters from effort level, epic
// directives, per-sprint overrides, and resume mode. The priority order is:
//  1. MaxAttemptsOverride (from --resume, highest)
//  2. Per-sprint @max_heal_attempts
//  3. Global @max_heal_attempts (explicit)
//  4. Effort-level default
//  5. config.DefaultMaxHealAttempts (fallback when effort=auto)
func effectiveHealConfig(opts HealOpts) healConfig {
	failPercent := resolveFailPercent(opts)

	// 1. Resume mode override — highest priority
	if opts.MaxAttemptsOverride > 0 {
		return healConfig{
			maxAttempts:    opts.MaxAttemptsOverride,
			hardCap:        true,
			progressBased:  opts.EffortLevel.HealUsesProgressDetection(),
			stuckThreshold: opts.EffortLevel.HealStuckThreshold(),
			maxFailPercent: failPercent,
		}
	}

	// 2. Per-sprint @max_heal_attempts
	if opts.Sprint.MaxHealAttempts != nil {
		return healConfig{
			maxAttempts:    *opts.Sprint.MaxHealAttempts,
			hardCap:        true,
			maxFailPercent: failPercent,
		}
	}

	// 3. Global @max_heal_attempts (explicitly set in epic)
	if opts.Epic.MaxHealAttemptsSet {
		return healConfig{
			maxAttempts:    opts.Epic.MaxHealAttempts,
			hardCap:        true,
			maxFailPercent: failPercent,
		}
	}

	// 4. Effort-level defaults
	effort := opts.EffortLevel
	cfg := healConfig{
		maxAttempts:    effort.DefaultMaxHealAttempts(),
		hardCap:        effort.HealHasHardCap(),
		progressBased:  effort.HealUsesProgressDetection(),
		stuckThreshold: effort.HealStuckThreshold(),
		maxFailPercent: failPercent,
	}
	if effort == epic.EffortMax {
		cfg.minForThreshold = config.HealMinAttemptsMax
		cfg.maxAttempts = config.HealSafetyCapMax
	}

	// 5. Fallback for auto/empty effort level only — fast effort intentionally uses 0
	if effort == "" && cfg.maxAttempts <= 0 && cfg.hardCap {
		cfg.maxAttempts = config.DefaultMaxHealAttempts
	}

	return cfg
}

// resolveFailPercent returns the effective max-fail-percent, preferring an
// explicit @max_fail_percent directive over the effort-level default.
// Falls back to whatever the caller passed in (typically the parser default).
func resolveFailPercent(opts HealOpts) int {
	if opts.Epic.MaxFailPercentSet {
		return opts.MaxFailPercent
	}
	if opts.EffortLevel != "" {
		return opts.EffortLevel.DefaultMaxFailPercent()
	}
	return opts.MaxFailPercent
}

// healGroupSeverity returns a priority level for a check type used in targeted
// alignment. Lower values mean higher priority (addressed first).
// Severity order: file existence (0) > file contents (1) > commands (2).
func healGroupSeverity(ct verify.CheckType) int {
	switch ct {
	case verify.CheckFile:
		return 0
	case verify.CheckFileContains:
		return 1
	default: // CheckCmd, CheckCmdOutput, CheckTest
		return 2
	}
}

// groupFailedChecks returns failed check results keyed by severity level.
func groupFailedChecks(results []verify.CheckResult) map[int][]verify.CheckResult {
	groups := make(map[int][]verify.CheckResult)
	for _, r := range results {
		if !r.Passed {
			sev := healGroupSeverity(r.Check.Type)
			groups[sev] = append(groups[sev], r)
		}
	}
	return groups
}

// highestPriorityGroup returns the results for the lowest-severity (highest-priority) group.
func highestPriorityGroup(groups map[int][]verify.CheckResult) []verify.CheckResult {
	minSev := -1
	for sev := range groups {
		if minSev < 0 || sev < minSev {
			minSev = sev
		}
	}
	if minSev < 0 {
		return nil
	}
	return groups[minSev]
}

func RunHealLoop(ctx context.Context, opts HealOpts) (*HealResult, error) {
	if opts.Epic == nil || opts.Sprint == nil {
		return nil, fmt.Errorf("run heal loop: epic and sprint are required")
	}
	if opts.Engine == nil {
		return nil, fmt.Errorf("run heal loop: engine is required")
	}

	healPrompt, err := templates.LoadText(config.HealInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("run heal loop: %w", err)
	}

	cfg := effectiveHealConfig(opts)

	buildLogsDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("run heal loop: create build logs dir: %w", err)
	}

	// Run an initial check to populate baseline results. This ensures
	// lastResults/lastPass/lastTotal are always initialized even when
	// maxAttempts is 0 (e.g., fast effort) or when the loop exits early.
	initialChecks, initialErr := reloadChecks(opts)
	if initialErr != nil {
		return nil, fmt.Errorf("run heal loop: initial check reload: %w", initialErr)
	}
	lastResults, lastPass, lastTotal := verify.RunChecks(ctx, initialChecks, opts.Sprint.Number, opts.ProjectDir)
	if lastTotal == lastPass {
		return &HealResult{Healed: true, Attempts: 0, PassCount: lastPass, TotalCount: lastTotal}, nil
	}

	prevFailCount := lastTotal - lastPass
	stuckCount := 0
	var healAgentRuns int
	resolvedModel := engine.ResolveModel(opts.Epic.AgentModel, opts.Engine.Name(), string(opts.EffortLevel), engine.SessionHeal)

	// Targeted alignment state: track resolved severity groups and stall.
	targetedMode := true
	// resolvedGroups records which severity buckets have been resolved at least
	// once. It is used exclusively by the stall detector to avoid counting the
	// same bucket twice across iterations. It does NOT filter highestPriorityGroup —
	// currentGroups already excludes resolved checks because groupFailedChecks only
	// includes Passed==false results.
	resolvedGroups := make(map[int]bool)
	targetedStall := 0
	lastGroups := groupFailedChecks(lastResults)

	for attempt := 1; !cfg.hardCap || attempt <= cfg.maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if steering.HasStopRequest(opts.ProjectDir) {
			return nil, steering.NewExitRequestError("alignment", fmt.Sprintf("before alignment attempt %d", attempt))
		}

		checks, reloadErr := reloadChecks(opts)
		if reloadErr != nil {
			return nil, fmt.Errorf("run heal loop: reload checks: %w", reloadErr)
		}

		results, passCount, totalCount := verify.RunChecks(ctx, checks, opts.Sprint.Number, opts.ProjectDir)
		if totalCount == passCount {
			return &HealResult{Healed: true, Attempts: healAgentRuns, PassCount: passCount, TotalCount: totalCount}, nil
		}

		// Update group state and detect stall for targeted alignment.
		currentGroups := groupFailedChecks(results)
		newlyResolved := 0
		for sev := range lastGroups {
			if _, stillFailing := currentGroups[sev]; !stillFailing {
				if !resolvedGroups[sev] {
					resolvedGroups[sev] = true
					newlyResolved++
				}
			}
		}
		if targetedMode {
			if newlyResolved == 0 {
				targetedStall++
			} else {
				targetedStall = 0
			}
			if targetedStall >= 2 {
				targetedMode = false
				frylog.Log("  Targeted alignment stalled after 2 iterations without progress — falling back to all-at-once mode.")
			}
		}
		lastGroups = currentGroups

		// Build failure report: targeted (highest-priority unfixed group) or all-at-once.
		var failureReport string
		if targetedMode {
			if group := highestPriorityGroup(currentGroups); len(group) > 0 {
				failureReport = verify.CollectFailures(group, 0, len(group))
			} else {
				failureReport = verify.CollectFailures(results, passCount, totalCount)
			}
		} else {
			failureReport = verify.CollectFailures(results, passCount, totalCount)
		}
		prompt := buildHealPrompt(opts, failureReport)
		promptPath := filepath.Join(opts.ProjectDir, config.PromptFile)
		if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
			return nil, fmt.Errorf("run heal loop: write heal prompt: %w", err)
		}

		if cfg.hardCap {
			frylog.Log(
				"▶ AGENT  Sprint %d/%d \"%s\"  align %d/%d  engine=%s  model=%s",
				opts.Sprint.Number,
				opts.Epic.TotalSprints,
				opts.Sprint.Name,
				attempt,
				cfg.maxAttempts,
				opts.Engine.Name(),
				agentModel(resolvedModel),
			)
		} else {
			frylog.Log(
				"▶ AGENT  Sprint %d/%d \"%s\"  align %d (progress-based)  engine=%s  model=%s",
				opts.Sprint.Number,
				opts.Epic.TotalSprints,
				opts.Sprint.Name,
				attempt,
				opts.Engine.Name(),
				agentModel(resolvedModel),
			)
		}

		healLogPath := filepath.Join(
			buildLogsDir,
			fmt.Sprintf("sprint%d_align%d_%s.log", opts.Sprint.Number, attempt, time.Now().Format("20060102_150405")),
		)
		healAgentRuns++
		if _, err := agentrun.RunWithDualLogs(ctx, healPrompt, healLogPath, opts.SprintLogFile, agentrun.DualLogOpts{
			Engine:      opts.Engine,
			Model:       resolvedModel,
			SessionType: engine.SessionHeal,
			EffortLevel: string(opts.EffortLevel),
			ExtraFlags:  strings.Fields(opts.Epic.AgentFlags),
			WorkDir:     opts.ProjectDir,
			Verbose:     opts.Verbose,
		}); err != nil {
			return nil, err
		}

		if err := shellhook.Run(ctx, opts.ProjectDir, opts.Epic.PreSprintCmd); err != nil {
			return nil, fmt.Errorf("run heal loop: pre-sprint hook: %w", err)
		}

		frylog.Log("  Re-running sanity checks after alignment attempt %d...", attempt)
		checks, reloadErr = reloadChecks(opts)
		if reloadErr != nil {
			return nil, fmt.Errorf("run heal loop: reload checks after attempt %d: %w", attempt, reloadErr)
		}
		results, passCount, totalCount = verify.RunChecks(ctx, checks, opts.Sprint.Number, opts.ProjectDir)
		lastResults = results
		lastPass = passCount
		lastTotal = totalCount

		if steering.HasStopRequest(opts.ProjectDir) {
			return nil, steering.NewExitRequestError("alignment", fmt.Sprintf("after alignment attempt %d", attempt))
		}

		if totalCount == passCount {
			frylog.Log("  Alignment attempt %d SUCCEEDED — all checks now pass.", attempt)
			return &HealResult{Healed: true, Attempts: healAgentRuns, PassCount: passCount, TotalCount: totalCount}, nil
		}

		currentFailCount := totalCount - passCount
		frylog.Log("  Alignment attempt %d — %d/%d checks still failing.", attempt, currentFailCount, totalCount)

		// Progress detection: exit early if stuck
		if cfg.progressBased && cfg.stuckThreshold > 0 {
			if currentFailCount >= prevFailCount {
				stuckCount++
			} else {
				stuckCount = 0
			}
			prevFailCount = currentFailCount

			if stuckCount >= cfg.stuckThreshold {
				frylog.Log("  No alignment progress for %d consecutive attempts — stopping.", stuckCount)
				failureReport = verify.CollectFailures(results, passCount, totalCount)
				entry := fmt.Sprintf("--- Alignment attempt %d failed (stuck, stopping) ---\n\n%s\n\n", attempt, failureReport)
				if err := appendToSprintProgress(opts.ProjectDir, entry); err != nil {
					return nil, fmt.Errorf("run heal loop: append failure report: %w", err)
				}
				break
			}
		}

		// Max effort: mid-loop threshold check after minimum attempts
		if !cfg.hardCap && cfg.minForThreshold > 0 && attempt >= cfg.minForThreshold {
			outcome := verify.EvaluateThreshold(results, passCount, totalCount, cfg.maxFailPercent)
			if outcome.WithinThreshold {
				frylog.Log("  After %d attempts, failure rate %.0f%% is within %d%% threshold — moving on.",
					attempt, outcome.FailPercent, cfg.maxFailPercent)
				return &HealResult{
					WithinThreshold:  true,
					DeferredFailures: outcome.DeferredFailures,
					Attempts:         healAgentRuns,
					PassCount:        passCount,
					TotalCount:       totalCount,
				}, nil
			}
		}

		failureReport = verify.CollectFailures(results, passCount, totalCount)
		entry := fmt.Sprintf("--- Alignment attempt %d failed ---\n\n%s\n\n", attempt, failureReport)
		if err := appendToSprintProgress(opts.ProjectDir, entry); err != nil {
			return nil, fmt.Errorf("run heal loop: append failure report: %w", err)
		}

		// Safety cap: prevent truly infinite loops even in unlimited mode
		if !cfg.hardCap && cfg.maxAttempts > 0 && attempt >= cfg.maxAttempts {
			frylog.Log("  Safety cap of %d alignment attempts reached.", cfg.maxAttempts)
			break
		}
	}

	if cfg.hardCap && cfg.maxAttempts > 0 {
		frylog.Log("  All %d alignment attempts exhausted.", cfg.maxAttempts)
	}

	// Evaluate threshold: allow partial pass if failures are within tolerance
	outcome := verify.EvaluateThreshold(lastResults, lastPass, lastTotal, cfg.maxFailPercent)
	if outcome.WithinThreshold {
		frylog.Log("  Failure rate %.0f%% is within %d%% threshold — deferring %d failures.",
			outcome.FailPercent, cfg.maxFailPercent, len(outcome.DeferredFailures))
		return &HealResult{
			WithinThreshold:  true,
			DeferredFailures: outcome.DeferredFailures,
			Attempts:         healAgentRuns,
			PassCount:        lastPass,
			TotalCount:       lastTotal,
		}, nil
	}

	return &HealResult{Attempts: healAgentRuns, PassCount: lastPass, TotalCount: lastTotal}, nil
}

// reloadChecks re-parses the verification file from disk when VerificationFile
// is set, so that on-disk edits by the alignment agent take effect between
// attempts. Falls back to the pre-loaded Checks slice otherwise.
func reloadChecks(opts HealOpts) ([]verify.Check, error) {
	if opts.VerificationFile == "" {
		return opts.Checks, nil
	}
	path := opts.VerificationFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(opts.ProjectDir, path)
	}
	checks, err := verify.ParseVerification(path)
	if err != nil {
		return nil, err
	}
	return checks, nil
}

func buildHealPrompt(opts HealOpts, failureReport string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# ALIGNMENT MODE — Sprint %d: %s\n\n", opts.Sprint.Number, opts.Sprint.Name))
	b.WriteString("## What happened\n")
	b.WriteString("The sprint finished its work but FAILED independent sanity checks.\n")
	b.WriteString("Your job is to fix ONLY the issues described below. Do not start the sprint over.\n")
	b.WriteString("Do not refactor or reorganize. Make the minimum changes needed to pass the checks.\n\n")
	b.WriteString("## Failed sanity checks\n\n")
	b.WriteString(failureReport)
	b.WriteString("\n\n")
	b.WriteString("## Instructions\n")
	b.WriteString(fmt.Sprintf("1. Read %s for context on what was built this sprint\n", config.SprintProgressFile))
	b.WriteString(fmt.Sprintf("2. Read %s for context on what was built in prior sprints\n", config.EpicProgressFile))
	b.WriteString("3. Read the failed checks above carefully\n")
	if opts.Mode == "writing" {
		b.WriteString("4. Fix each failure — create missing content files, add missing sections, expand insufficient content\n")
	} else {
		b.WriteString("4. Fix each failure — create missing files, fix build errors, correct config\n")
	}
	if opts.Mode == "writing" {
		b.WriteString("5. After fixing, review the content for completeness and consistency\n")
	} else {
		b.WriteString("5. After fixing, do a final spot check (e.g., run the build command if applicable)\n")
	}
	b.WriteString(fmt.Sprintf("6. Append a brief note to %s about what you fixed in this alignment pass\n\n", config.SprintProgressFile))
	b.WriteString("## Context files\n")
	b.WriteString(fmt.Sprintf("- Read %s for current sprint iteration history\n", config.SprintProgressFile))
	b.WriteString(fmt.Sprintf("- Read %s for prior sprint summaries\n", config.EpicProgressFile))
	b.WriteString(fmt.Sprintf("- Read %s for the overall project plan\n", config.PlanFile))
	// Only reference executive.md if the file actually exists
	executivePath := filepath.Join(opts.ProjectDir, config.ExecutiveFile)
	if _, err := os.Stat(executivePath); err == nil {
		b.WriteString(fmt.Sprintf("- Read %s for executive context\n", config.ExecutiveFile))
	}
	if strings.TrimSpace(opts.UserPrompt) != "" {
		b.WriteString(fmt.Sprintf("- User directive: %s\n", strings.TrimSpace(opts.UserPrompt)))
	}
	b.WriteString("\n")
	b.WriteString("Do NOT output any promise tokens. Just fix the issues.\n")
	return b.String()
}

func appendToSprintProgress(projectDir, entry string) error {
	progressPath := filepath.Join(projectDir, config.SprintProgressFile)
	if err := os.MkdirAll(filepath.Dir(progressPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(progressPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = file.WriteString(entry)
	return err
}

func agentModel(model string) string {
	if model == "" {
		return "default"
	}
	return model
}
