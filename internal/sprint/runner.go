package sprint

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
	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/heal"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/observer"
	"github.com/yevgetman/fry/internal/shellhook"
	"github.com/yevgetman/fry/internal/steering"
	"github.com/yevgetman/fry/internal/verify"
	"github.com/yevgetman/fry/templates"
)

const (
	StatusPass = "PASS"
	// PASS aligned: keep this unparenthesized form in the file for regex-based sanity check compatibility.
	StatusPassAligned                                 = "PASS (aligned)"
	StatusPassSanityPassedNoPromise                   = "PASS (sanity checks passed, no promise)"
	StatusPassAlignedNoPromise                        = "PASS (aligned, no promise)"
	StatusPassWithDeferredFailures                    = "PASS (deferred failures)"
	StatusPassAlignedWithDeferredFailures             = "PASS (aligned, deferred failures)"
	StatusFailSanityFailedAlignmentExhausted          = "FAIL (sanity checks failed, alignment exhausted)"
	StatusFailNoPromiseSanityFailedAlignmentExhausted = "FAIL (no promise, sanity checks failed, alignment exhausted)"
	StatusFailNoPrompt                                = "FAIL (no prompt)"
	StatusPaused                                      = "PAUSED"
	StatusSkipped                                     = "SKIPPED"
)

type SprintResult struct {
	Number                 int
	Name                   string
	Status                 string
	Duration               time.Duration
	HealAttempts           int                  // number of alignment agent invocations
	AuditWarning           string               // non-empty when MODERATE audit issues remain (advisory)
	DeferredFailures       []verify.CheckResult // sanity check failures below threshold
	VerificationResults    []verify.CheckResult // all check results from the final sanity check run
	VerificationPassCount  int                  // pass count from final sanity check run
	VerificationTotalCount int                  // total checks from final sanity check run
	SprintLogPath          string               // path to combined sprint log file
	PausePhase             string
	PauseDetail            string
	PauseCheckpointed      bool
}

type RunConfig struct {
	ProjectDir          string
	Epic                *epic.Epic
	Sprint              *epic.Sprint
	Engine              engine.Engine
	Verbose             bool
	DryRun              bool
	UserPrompt          string
	StartSprint         int
	EndSprint           int
	Mode                string
	IdentityDisposition string // behavioral disposition injected into sprint prompts
}

func RunSprint(ctx context.Context, cfg RunConfig) (*SprintResult, error) {
	started := time.Now()
	if cfg.Epic == nil || cfg.Sprint == nil {
		return nil, fmt.Errorf("run sprint: epic and sprint are required")
	}

	agentPrompt, err := templates.LoadText(config.AgentInvocationFile)
	if err != nil {
		return nil, fmt.Errorf("run sprint: %w", err)
	}

	if err := InitSprintProgress(cfg.ProjectDir, cfg.Sprint.Number, cfg.Sprint.Name); err != nil {
		return nil, fmt.Errorf("run sprint: %w", err)
	}

	if strings.TrimSpace(cfg.Sprint.Prompt) == "" {
		return &SprintResult{
			Number:   cfg.Sprint.Number,
			Name:     cfg.Sprint.Name,
			Status:   StatusFailNoPrompt,
			Duration: time.Since(started),
		}, nil
	}
	if cfg.Engine == nil {
		return nil, fmt.Errorf("run sprint: engine is required")
	}

	userPrompt := cfg.UserPrompt
	if strings.TrimSpace(userPrompt) == "" {
		userPrompt = readOptionalPromptFile(filepath.Join(cfg.ProjectDir, config.UserPromptFile))
	}

	if _, err := AssemblePrompt(PromptOpts{
		ProjectDir:          cfg.ProjectDir,
		SprintNumber:        cfg.Sprint.Number,
		UserPrompt:          userPrompt,
		SprintPrompt:        cfg.Sprint.Prompt,
		SprintProgressFile:  config.SprintProgressFile,
		EpicProgressFile:    config.EpicProgressFile,
		Promise:             cfg.Sprint.Promise,
		EffortLevel:         cfg.Epic.EffortLevel,
		Mode:                cfg.Mode,
		IdentityDisposition: cfg.IdentityDisposition,
	}); err != nil {
		return nil, fmt.Errorf("run sprint: %w", err)
	}

	buildLogsDir := filepath.Join(cfg.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("run sprint: create build logs dir: %w", err)
	}

	sprintStamp := time.Now().Format("20060102_150405")
	sprintLogPath := filepath.Join(buildLogsDir, fmt.Sprintf("sprint%d_%s.log", cfg.Sprint.Number, sprintStamp))

	promiseToken := "===PROMISE: " + cfg.Sprint.Promise + "==="
	promiseFound := false

	// Pre-load sanity checks for no-op early exit detection
	checks, checkErr := loadVerificationChecks(cfg.ProjectDir, cfg.Epic.VerificationFile)
	if checkErr != nil {
		return nil, checkErr
	}
	warnOutOfRangeChecks(checks, cfg.Epic.TotalSprints)

	sprintCheckCount := countChecksForSprint(checks, cfg.Sprint.Number)

	consecutiveNoop := 0

	frylog.Log("=========================================")
	frylog.Log("STARTING SPRINT %d: %s", cfg.Sprint.Number, cfg.Sprint.Name)
	frylog.Log("Max iterations: %d", cfg.Sprint.MaxIterations)
	if cfg.Epic.EffortLevel != "" {
		frylog.Log("Effort level: %s", cfg.Epic.EffortLevel)
	}
	if sprintCheckCount > 0 {
		frylog.Log("Sanity checks: %d applicable to this sprint", sprintCheckCount)
	}
	frylog.Log("=========================================")

	for iter := 1; iter <= cfg.Sprint.MaxIterations; iter++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if err := shellhook.Run(ctx, cfg.ProjectDir, cfg.Epic.PreIterationCmd); err != nil {
			return nil, fmt.Errorf("run sprint pre-iteration hook: %w", err)
		}

		// Auto-compact sprint progress if it's grown too large.
		CompactSprintProgressIfNeeded(cfg.ProjectDir)

		// Layer 1 — Tier A: Check for mid-build user directive
		if directive, dErr := steering.ConsumeDirective(cfg.ProjectDir); dErr != nil {
			frylog.Log("  STEERING: warning: failed to read directive: %v", dErr)
		} else if directive != "" {
			frylog.Log("  STEERING: received user directive (injecting into prompt)")
			preview := directive
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			_ = observer.EmitEvent(cfg.ProjectDir, observer.Event{
				Type:   observer.EventDirectiveReceived,
				Sprint: cfg.Sprint.Number,
				Data:   map[string]string{"preview": preview},
			})
			// Reassemble prompt with the directive injected
			if _, pErr := AssemblePrompt(PromptOpts{
				ProjectDir:          cfg.ProjectDir,
				SprintNumber:        cfg.Sprint.Number,
				UserPrompt:          userPrompt,
				SprintPrompt:        cfg.Sprint.Prompt,
				SprintProgressFile:  config.SprintProgressFile,
				EpicProgressFile:    config.EpicProgressFile,
				Promise:             cfg.Sprint.Promise,
				EffortLevel:         cfg.Epic.EffortLevel,
				Mode:                cfg.Mode,
				IdentityDisposition: cfg.IdentityDisposition,
				SteeringDirective:   directive,
			}); pErr != nil {
				frylog.Log("  STEERING: failed to reassemble prompt with directive: %v", pErr)
			}
		}

		resolvedModel := engine.ResolveModel(cfg.Epic.AgentModel, cfg.Engine.Name(), string(cfg.Epic.EffortLevel), engine.SessionSprint)
		frylog.AgentBanner(cfg.Sprint.Number, cfg.Epic.TotalSprints, cfg.Sprint.Name, iter, cfg.Sprint.MaxIterations, cfg.Engine.Name(), resolvedModel)

		// Snapshot working tree before agent runs (for no-op detection)
		preIterDiff := gitDiffStat(ctx, cfg.ProjectDir)

		iterPath := filepath.Join(buildLogsDir, fmt.Sprintf("sprint%d_iter%d_%s.log", cfg.Sprint.Number, iter, time.Now().Format("20060102_150405")))
		output, err := agentrun.RunWithDualLogs(ctx, agentPrompt, iterPath, sprintLogPath, agentrun.DualLogOpts{
			Engine:      cfg.Engine,
			Model:       resolvedModel,
			SessionType: engine.SessionSprint,
			EffortLevel: string(cfg.Epic.EffortLevel),
			ExtraFlags:  strings.Fields(cfg.Epic.AgentFlags),
			WorkDir:     cfg.ProjectDir,
			Verbose:     cfg.Verbose,
		})
		if err != nil {
			return nil, err
		}

		if strings.Contains(output, promiseToken) {
			promiseFound = true
			break
		}

		// No-op detection: early exit when agent is stuck in audit loops
		noopThreshold := 2
		if cfg.Epic.EffortLevel == epic.EffortMax {
			noopThreshold = 3
		}
		postIterDiff := gitDiffStat(ctx, cfg.ProjectDir)
		if preIterDiff == postIterDiff {
			consecutiveNoop++
			frylog.Log("  ITER %d: no file changes detected (%d consecutive no-op)", iter, consecutiveNoop)
		} else {
			consecutiveNoop = 0
		}

		if consecutiveNoop >= noopThreshold {
			if len(checks) > 0 {
				_, passCount, totalCount := verify.RunChecks(ctx, checks, cfg.Sprint.Number, cfg.ProjectDir)
				if totalCount > 0 && passCount == totalCount {
					frylog.Log("  No file changes for %d consecutive iterations and sanity checks pass — exiting early.", consecutiveNoop)
					break
				}
			} else if consecutiveNoop >= noopThreshold+1 {
				// No sanity checks defined: use a slightly higher threshold
				// before concluding the agent is done, since we can't validate
				// completion via checks.
				frylog.Log("  No file changes for %d consecutive iterations (no sanity checks) — exiting early.", consecutiveNoop)
				break
			}
		}

		// Layer 1 — Tier C: Check for graceful stop requests.
		if steering.HasStopRequest(cfg.ProjectDir) {
			frylog.Log("  STEERING: pause requested — checkpointing and exiting")
			if err := git.GitCheckpoint(ctx, cfg.ProjectDir, cfg.Epic.Name, cfg.Sprint.Number, cfg.Sprint.Name, "paused"); err != nil {
				return nil, fmt.Errorf("run sprint: pause checkpoint: %w", err)
			}
			return &SprintResult{
				Number:            cfg.Sprint.Number,
				Name:              cfg.Sprint.Name,
				Status:            StatusPaused,
				Duration:          time.Since(started),
				PausePhase:        "sprint_iteration",
				PauseDetail:       fmt.Sprintf("after iteration %d", iter),
				PauseCheckpointed: true,
			}, nil
		}
	}

	if len(checks) > 0 {
		frylog.Log("  Running sanity checks...")
	}
	results, passCount, totalCount := verify.RunChecks(ctx, checks, cfg.Sprint.Number, cfg.ProjectDir)
	if totalCount > 0 {
		frylog.Log("  Sanity checks: %d/%d passed.", passCount, totalCount)
	}

	status, deferred, healAttempts, err := determineOutcome(ctx, cfg, checks, promiseFound, results, passCount, totalCount, sprintLogPath)
	if err != nil {
		return nil, err
	}

	if len(deferred) > 0 {
		summary := verify.CollectDeferredSummary(deferred)
		if appendErr := AppendToSprintProgress(cfg.ProjectDir,
			fmt.Sprintf("\nDeferred sanity check failures (%d):\n%s\n", len(deferred), summary)); appendErr != nil {
			frylog.Log("WARNING: could not write deferred failures to sprint progress: %v", appendErr)
		}
	}

	elapsed := time.Since(started)
	frylog.Log("SPRINT %d %s (%s)", cfg.Sprint.Number, status, elapsed.Round(time.Second))

	return &SprintResult{
		Number:                 cfg.Sprint.Number,
		Name:                   cfg.Sprint.Name,
		Status:                 status,
		Duration:               elapsed,
		HealAttempts:           healAttempts,
		DeferredFailures:       deferred,
		VerificationResults:    results,
		VerificationPassCount:  passCount,
		VerificationTotalCount: totalCount,
		SprintLogPath:          sprintLogPath,
	}, nil
}

func determineOutcome(ctx context.Context, cfg RunConfig, checks []verify.Check, promiseFound bool, results []verify.CheckResult, passCount, totalCount int, sprintLogPath string) (string, []verify.CheckResult, int, error) {
	hasChecks := totalCount > 0
	checksPass := totalCount == passCount

	healOpts := heal.HealOpts{
		ProjectDir:       cfg.ProjectDir,
		Sprint:           cfg.Sprint,
		Epic:             cfg.Epic,
		Engine:           cfg.Engine,
		Checks:           checks,
		VerificationFile: cfg.Epic.VerificationFile,
		UserPrompt:       cfg.UserPrompt,
		Verbose:          cfg.Verbose,
		SprintLogFile:    sprintLogPath,
		MaxFailPercent:   cfg.Epic.MaxFailPercent,
		EffortLevel:      cfg.Epic.EffortLevel,
		Mode:             cfg.Mode,
	}

	switch {
	case promiseFound && !hasChecks:
		return StatusPass, nil, 0, nil
	case promiseFound && checksPass:
		return StatusPass, nil, 0, nil
	case promiseFound && hasChecks && !checksPass:
		hr, err := heal.RunHealLoop(ctx, healOpts)
		if err != nil {
			return "", nil, 0, err
		}
		if hr.Healed {
			return StatusPassAligned, nil, hr.Attempts, nil
		}
		if hr.WithinThreshold {
			return StatusPassWithDeferredFailures, hr.DeferredFailures, hr.Attempts, nil
		}
		return StatusFailSanityFailedAlignmentExhausted, nil, hr.Attempts, nil
	case !promiseFound && !hasChecks:
		return fmt.Sprintf("FAIL (no promise after %d iters)", cfg.Sprint.MaxIterations), nil, 0, nil
	case !promiseFound && checksPass:
		return StatusPassSanityPassedNoPromise, nil, 0, nil
	default:
		hr, err := heal.RunHealLoop(ctx, healOpts)
		if err != nil {
			return "", nil, 0, err
		}
		if hr.Healed {
			return StatusPassAlignedNoPromise, nil, hr.Attempts, nil
		}
		if hr.WithinThreshold {
			return StatusPassAlignedWithDeferredFailures, hr.DeferredFailures, hr.Attempts, nil
		}
		return StatusFailNoPromiseSanityFailedAlignmentExhausted, nil, hr.Attempts, nil
	}
}

// ResumeSprint skips the iteration loop and goes straight to sanity checks + alignment
// with a boosted alignment budget. It preserves existing sprint-progress.txt so the agent
// retains full context from the previous failed attempt.
func ResumeSprint(ctx context.Context, cfg RunConfig) (*SprintResult, error) {
	started := time.Now()
	if cfg.Epic == nil || cfg.Sprint == nil {
		return nil, fmt.Errorf("resume sprint: epic and sprint are required")
	}
	if cfg.Engine == nil {
		return nil, fmt.Errorf("resume sprint: engine is required")
	}

	// DO NOT call InitSprintProgress — preserve existing progress with prior context
	if err := AppendToSprintProgress(cfg.ProjectDir,
		"\n--- RESUME MODE ---\nResuming from previous failed attempt. Skipping iteration loop, going straight to sanity checks + alignment.\n\n"); err != nil {
		frylog.Log("WARNING: could not write resume marker to sprint progress: %v", err)
	}

	checks, err := loadVerificationChecks(cfg.ProjectDir, cfg.Epic.VerificationFile)
	if err != nil {
		return nil, err
	}

	buildLogsDir := filepath.Join(cfg.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(buildLogsDir, 0o755); err != nil {
		return nil, fmt.Errorf("resume sprint: create build logs dir: %w", err)
	}
	sprintLogPath := filepath.Join(buildLogsDir,
		fmt.Sprintf("sprint%d_resume_%s.log", cfg.Sprint.Number, time.Now().Format("20060102_150405")))

	frylog.Log("=========================================")
	frylog.Log("RESUMING SPRINT %d: %s", cfg.Sprint.Number, cfg.Sprint.Name)
	frylog.Log("Skipping iterations — going straight to sanity checks + alignment")
	frylog.Log("=========================================")

	if len(checks) == 0 {
		frylog.Log("  No sanity checks defined — nothing to resume.")
		elapsed := time.Since(started)
		return &SprintResult{
			Number:        cfg.Sprint.Number,
			Name:          cfg.Sprint.Name,
			Status:        StatusPass,
			Duration:      elapsed,
			SprintLogPath: sprintLogPath,
		}, nil
	}

	results, passCount, totalCount := verify.RunChecks(ctx, checks, cfg.Sprint.Number, cfg.ProjectDir)
	frylog.Log("  Sanity checks: %d/%d passed.", passCount, totalCount)

	if passCount == totalCount {
		frylog.Log("  All checks pass — no alignment needed.")
		elapsed := time.Since(started)
		return &SprintResult{
			Number:                 cfg.Sprint.Number,
			Name:                   cfg.Sprint.Name,
			Status:                 StatusPass,
			Duration:               elapsed,
			VerificationPassCount:  passCount,
			VerificationTotalCount: totalCount,
			SprintLogPath:          sprintLogPath,
		}, nil
	}

	// Calculate boosted alignment attempts for resume using effort-level-aware base
	maxAttempts := cfg.Epic.MaxHealAttempts
	if cfg.Sprint.MaxHealAttempts != nil {
		maxAttempts = *cfg.Sprint.MaxHealAttempts
	}
	if !cfg.Epic.MaxHealAttemptsSet && maxAttempts <= 0 && cfg.Epic.EffortLevel != "" {
		maxAttempts = cfg.Epic.EffortLevel.DefaultMaxHealAttempts()
	}
	if maxAttempts <= 0 {
		maxAttempts = config.DefaultMaxHealAttempts
	}
	boostedAttempts := maxAttempts * config.ResumeHealMultiplier
	if boostedAttempts < config.ResumeMinHealAttempts {
		boostedAttempts = config.ResumeMinHealAttempts
	}
	frylog.Log("  Entering alignment loop with %d attempts (resume mode, was %d)...", boostedAttempts, maxAttempts)

	if err := AppendToSprintProgress(cfg.ProjectDir,
		fmt.Sprintf("Sanity checks failed: %d/%d passing. Starting resume alignment with %d attempts.\n\n",
			passCount, totalCount, boostedAttempts)); err != nil {
		frylog.Log("WARNING: could not write sanity check status to sprint progress: %v", err)
	}

	failureReport := verify.CollectFailures(results, passCount, totalCount)
	if err := AppendToSprintProgress(cfg.ProjectDir,
		fmt.Sprintf("Current failures:\n%s\n\n", failureReport)); err != nil {
		frylog.Log("WARNING: could not write failure report to sprint progress: %v", err)
	}

	hr, err := heal.RunHealLoop(ctx, heal.HealOpts{
		ProjectDir:          cfg.ProjectDir,
		Sprint:              cfg.Sprint,
		Epic:                cfg.Epic,
		Engine:              cfg.Engine,
		Checks:              checks,
		VerificationFile:    cfg.Epic.VerificationFile,
		UserPrompt:          cfg.UserPrompt,
		Verbose:             cfg.Verbose,
		SprintLogFile:       sprintLogPath,
		MaxAttemptsOverride: boostedAttempts,
		MaxFailPercent:      cfg.Epic.MaxFailPercent,
		EffortLevel:         cfg.Epic.EffortLevel,
		Mode:                cfg.Mode,
	})
	if err != nil {
		return nil, err
	}

	var deferred []verify.CheckResult
	status := StatusFailSanityFailedAlignmentExhausted
	if hr.Healed {
		status = StatusPassAligned
	} else if hr.WithinThreshold {
		status = StatusPassWithDeferredFailures
		deferred = hr.DeferredFailures
	}

	if len(deferred) > 0 {
		summary := verify.CollectDeferredSummary(deferred)
		if appendErr := AppendToSprintProgress(cfg.ProjectDir,
			fmt.Sprintf("\nDeferred sanity check failures (%d):\n%s\n", len(deferred), summary)); appendErr != nil {
			frylog.Log("WARNING: could not write deferred failures to sprint progress: %v", appendErr)
		}
	}

	elapsed := time.Since(started)
	frylog.Log("SPRINT %d RESUME %s (%s)", cfg.Sprint.Number, status, elapsed.Round(time.Second))

	return &SprintResult{
		Number:                 cfg.Sprint.Number,
		Name:                   cfg.Sprint.Name,
		Status:                 status,
		Duration:               elapsed,
		HealAttempts:           hr.Attempts,
		DeferredFailures:       deferred,
		VerificationPassCount:  hr.PassCount,
		VerificationTotalCount: hr.TotalCount,
		SprintLogPath:          sprintLogPath,
	}, nil
}

func loadVerificationChecks(projectDir, verificationFile string) ([]verify.Check, error) {
	path := verificationFile
	if path == "" {
		path = config.DefaultVerificationFile
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectDir, path)
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return verify.ParseVerification(path)
}

func gitDiffStat(ctx context.Context, projectDir string) string {
	return git.DiffStatForNoopDetection(ctx, projectDir)
}

func countChecksForSprint(checks []verify.Check, sprintNum int) int {
	count := 0
	for _, c := range checks {
		if c.Sprint == sprintNum {
			count++
		}
	}
	return count
}

func warnOutOfRangeChecks(checks []verify.Check, totalSprints int) {
	if totalSprints <= 0 {
		return
	}
	seen := make(map[int]bool)
	for _, c := range checks {
		if c.Sprint > totalSprints && !seen[c.Sprint] {
			frylog.Log("WARNING: sanity checks file defines checks for sprint %d but epic only has %d sprints — these checks will never run", c.Sprint, totalSprints)
			seen[c.Sprint] = true
		}
	}
}
