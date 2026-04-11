package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yevgetman/fry/internal/agent"
	"github.com/yevgetman/fry/internal/audit"
	"github.com/yevgetman/fry/internal/color"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/consciousness"
	"github.com/yevgetman/fry/internal/continuerun"
	"github.com/yevgetman/fry/internal/copilot"
	"github.com/yevgetman/fry/internal/docker"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/finalize"
	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/githubissue"
	"github.com/yevgetman/fry/internal/lock"
	frlog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/metrics"
	"github.com/yevgetman/fry/internal/observer"
	"github.com/yevgetman/fry/internal/preflight"
	"github.com/yevgetman/fry/internal/prepare"
	"github.com/yevgetman/fry/internal/report"
	"github.com/yevgetman/fry/internal/review"
	"github.com/yevgetman/fry/internal/scan"
	"github.com/yevgetman/fry/internal/shellhook"
	"github.com/yevgetman/fry/internal/sprint"
	"github.com/yevgetman/fry/internal/steering"
	"github.com/yevgetman/fry/internal/summary"
	"github.com/yevgetman/fry/internal/textutil"
	"github.com/yevgetman/fry/internal/triage"
	"github.com/yevgetman/fry/internal/verify"
)

var (
	runEngine                string
	runDryRun                bool
	runUserPrompt            string
	runUserPromptFile        string
	runGitHubIssue           string
	runNoReview              bool
	runReview                bool
	runSimulateReview        string
	runPrepareEngine         string
	runPlanning              bool
	runMode                  string
	runEffort                string
	runNoAudit               bool
	runResume                bool
	runSprint                int
	runContinue              bool
	runNoProjectOverview     bool
	runYes                   bool
	runFullPrepare           bool
	runGitStrategy           string
	runBranchName            string
	runAlwaysVerify          bool
	runSimpleContinue        bool
	runSARIF                 bool
	runJSONReport            bool
	runShowTokens            bool
	runObserver              bool
	runNoObserver            bool
	runTriageOnly            bool
	runModel                 string
	runTelemetry             bool
	runNoTelemetry           bool
	runMCPConfig             string
	runConfirmFile           bool
	runCopilot               string // "" = unset; otherwise engine name (or "" with cmd.Flags().Changed("copilot")=true means default)
	runCopilotInterval       string
	runCopilotFrySource      string
	runCopilotModel          string
	runCopilotPassive        bool
	runCopilotPrintSummary   bool
	runNoCopilot             bool
	resolveGitHubIssuePrompt = githubissue.ResolvePrompt
)

type deferredEntry struct {
	SprintNumber int
	SprintName   string
	Failures     []verify.CheckResult
}

var runCmd = &cobra.Command{
	Use:   "run [epic.md] [start] [end]",
	Short: "Run fry against an epic",
	Args:  cobra.MaximumNArgs(3),
	RunE: func(cmd *cobra.Command, args []string) (retErr error) {
		if err := checkArgsForMissingDashes(cmd, args); err != nil {
			return err
		}

		projectPath, err := resolveProjectDir(projectDir)
		if err != nil {
			return err
		}
		repoExistedBeforeRun := fileExists(filepath.Join(projectPath, ".git"))

		effortLevel, err := epic.ParseEffortLevel(runEffort)
		if err != nil {
			return err
		}

		mode, err := resolveMode(runMode, runPlanning)
		if err != nil {
			return err
		}
		runPlanner := newEnginePlanner(resolvePrepareEngine(runPrepareEngine, runEngine))
		currentEngineName := func() string { return runPlanner.Current() }
		var buildStatus *agent.BuildStatus
		writeCurrentBuildStatus := func() {
			if buildStatus == nil {
				return
			}
			buildStatus.Build.Engine = currentEngineName()
			writeBuildStatus(projectPath, buildStatus)
		}

		gitStrategy, err := git.ParseGitStrategy(runGitStrategy)
		if err != nil {
			return err
		}
		if gitStrategy == git.StrategyCurrent && runBranchName != "" {
			return fmt.Errorf("--branch-name cannot be used with --git-strategy current")
		}
		if runTriageOnly && runFullPrepare {
			return fmt.Errorf("cannot use --triage-only with --full-prepare")
		}
		if runTriageOnly && (runResume || runContinue || runSimpleContinue) {
			return fmt.Errorf("cannot use --triage-only with --resume, --continue, or --simple-continue")
		}

		// When --continue is used and no explicit --mode was given,
		// auto-detect mode from the canonical build state after any
		// worktree/root selection has been resolved.
		userSetMode := strings.TrimSpace(runMode) != "" || runPlanning

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()

		// For --continue/--resume, detect if a prior run used a worktree and redirect
		// projectPath before any file reads (epic, user-prompt, etc.)
		var strategySetup *git.StrategySetup
		originalProjectPath := projectPath
		runPlanner.SetSwitchCallback(func(from, to string) {
			_ = observer.EmitEvent(projectPath, observer.Event{
				Type: observer.EventEngineFailover,
				Data: map[string]string{
					"from": from,
					"to":   to,
				},
			})
			writeCurrentBuildStatus()
		})
		if runContinue || runSimpleContinue || runResume {
			target, resolveErr := continuerun.ResolveContinueTarget(ctx, projectPath)
			if resolveErr != nil {
				frlog.Log("WARNING: continue target resolution failed: %v", resolveErr)
			} else if target != nil {
				if target.Reason != "" {
					frlog.Log("▶ CONTINUE  state selection: %s", target.Reason)
				}
				projectPath = target.ProjectDir
				strategySetup = target.Strategy
			}
			if strategySetup != nil && strategySetup.IsWorktree {
				frlog.Log("▶ CONTINUE  reattaching to worktree: %s", strategySetup.WorkDir)
			} else if strategySetup != nil && strategySetup.Strategy == git.StrategyBranch && strategySetup.BranchName != "" {
				// Checkout the branch if we're not already on it.
				if git.CurrentBranch(ctx, projectPath) != strategySetup.BranchName {
					if coErr := git.CheckoutBranch(ctx, projectPath, strategySetup.BranchName); coErr != nil {
						frlog.Log("WARNING: could not checkout branch %s: %v", strategySetup.BranchName, coErr)
					}
				}
				frlog.Log("▶ CONTINUE  reattaching to branch: %s", strategySetup.BranchName)
			}
		}
		if (runContinue || runSimpleContinue) && !userSetMode {
			if detected := continuerun.ReadBuildMode(projectPath); detected != "" {
				parsedMode, parseErr := prepare.ParseMode(detected)
				if parseErr == nil && parsedMode != mode {
					mode = parsedMode
					frlog.Log("▶ CONTINUE  auto-detected mode: %s", mode)
				}
			}
		}

		// Refresh file index if stale (codebase awareness).
		if scan.RefreshFileIndexIfStale(ctx, projectPath) {
			frlog.Log("  SCAN: refreshed stale file index")
		}

		epicArg := filepath.Join(config.FryDir, "epic.md")
		if len(args) > 0 {
			epicArg = args[0]
		}

		userPrompt, promptSource, err := resolveTopLevelPrompt(cmd.Context(), projectPath, runUserPrompt, runUserPromptFile, runGitHubIssue, !runDryRun)
		if err != nil {
			return err
		}

		epicPath, epicExists, err := resolveEpicPath(projectPath, epicArg)
		if err != nil {
			return err
		}

		// Acquire the build lock as early as possible so that prepare and
		// triage are mutually exclusive across concurrent fry invocations,
		// and so `fry status` can correctly report "Build in progress"
		// during those phases (which write to .fry/ before the run loop).
		// lockDir captures the original project path at acquisition time so
		// the release path is robust against any later worktree-redirect
		// reassignment of originalProjectPath.
		lockDir := originalProjectPath
		if err := lock.AcquireIfNotDryRun(lockDir, runDryRun); err != nil {
			return err
		}
		var lockOnce sync.Once
		releaseLock := func() {
			lockOnce.Do(func() {
				if err := lock.Release(lockDir); err != nil {
					fmt.Fprintf(os.Stderr, "fry: warning: %v\n", err)
				}
			})
		}
		defer releaseLock()
		// Copilot cleanup is best-effort and never blocks fry's exit.
		// CleanupOnExit drops stale lock/PID files and writes a final
		// snapshot. ArchiveCopilotDirIfDone moves the dir into archive/
		// only when the copilot has cleanly exited (final-summary.md
		// present, cron.id empty, no live tick lock) — otherwise it is
		// a no-op so the next run can pick up where this one left off.
		defer func() {
			copilot.CleanupOnExit(projectPath)
			if _, err := copilot.ArchiveCopilotDirIfDone(projectPath, ""); err != nil {
				frlog.Log("WARNING: copilot archive: %v", err)
			}
		}()

		// Start the copilot supervisor. It runs for the lifetime of the
		// build and manages the copilot scheduler lifecycle: starts the
		// scheduler when a manifest appears (from --copilot bootstrap or
		// `fry copilot start`), stops it on `fry copilot stop`. This
		// replaces the previous direct scheduler defer with a unified
		// lifecycle manager.
		copilotSupervisor := copilot.StartSupervisor(projectPath)
		defer copilotSupervisor.Stop()

		// Early copilot bootstrap: start the copilot immediately so it
		// can watch prepare/triage for hangs and failures. Epic details
		// are unknown at this point — we use placeholders and update the
		// manifest once the epic is parsed and the worktree is set up.
		if copilotShouldBootstrap(cmd, "") {
			copilotEngine := resolveCopilotEngine()
			frySrcDir := copilot.DiscoverFrySourceDir(runCopilotFrySource)
			passive := runCopilotPassive || frySrcDir == ""
			earlyResult, earlyErr := copilot.Bootstrap(copilot.BootstrapOpts{
				ProjectDir:   projectPath,
				FrySourceDir: frySrcDir,
				Engine:       copilotEngine,
				Model:        runCopilotModel,
				EpicName:     "(preparing)",
				EffortLevel:  "unknown",
				TotalSprints: 0,
				BuildPID:     os.Getpid(),
				Interval:     runCopilotInterval,
				RunID:        time.Now().UTC().Format("20060102-150405"),
				Passive:      passive,
				DryRun:       runDryRun,
				Stdout:       cmd.OutOrStdout(),
			})
			if earlyErr != nil {
				frlog.Log("WARNING: early copilot bootstrap failed: %v", earlyErr)
			} else if earlyResult != nil && earlyResult.Scheduler != nil {
				copilotSupervisor.SetScheduler(earlyResult.Scheduler)
			}
		}

		printMigrationHintIfNeeded(cmd.OutOrStdout(), projectPath, epicArg)

		if runTriageOnly && epicExists {
			return fmt.Errorf("--triage-only: epic already exists at %s; triage only runs when no epic exists", epicArg)
		}

		var triageDecision *triage.TriageDecision
		if !epicExists {
			if runResume {
				return fmt.Errorf("--resume requires existing build artifacts; epic file not found at %s", epicArg)
			}
			if runContinue {
				return fmt.Errorf("--continue requires existing build artifacts; epic file not found at %s", epicArg)
			}
			if runSimpleContinue {
				return fmt.Errorf("--simple-continue requires existing build artifacts; epic file not found at %s", epicArg)
			}
			prepareEngineName := resolvePrepareEngine(runPrepareEngine, runEngine)
			runPlanner.SetDefault(prepareEngineName)
			// Build engine factory with MCP config for auto-prepare paths
			earlyPrepareFactory := func(name string) (engine.Engine, error) {
				if runMCPConfig == "" {
					return runPlanner.Build(name)
				}
				mcpPath := runMCPConfig
				if abs, err := filepath.Abs(runMCPConfig); err == nil {
					mcpPath = abs
				}
				return runPlanner.Build(name, engine.WithMCPConfig(mcpPath))
			}
			if runFullPrepare {
				writeBuildPhase(projectPath, "prepare")
				if err := prepare.RunPrepare(cmd.Context(), prepare.PrepareOpts{
					ProjectDir:          projectPath,
					EpicFilename:        filepath.Base(epicPath),
					Engine:              runPlanner.Current(),
					UserPrompt:          userPrompt,
					UserPromptSource:    promptSource,
					SkipProjectOverview: runNoProjectOverview || runDryRun,
					AutoAccept:          runYes,
					ConfirmFile:         runConfirmFile,
					Mode:                mode,
					EffortLevel:         effortLevel,
					EnableReview:        runReview,
					Stdin:               os.Stdin,
					Stdout:              cmd.OutOrStdout(),
					EngineFactory:       earlyPrepareFactory,
				}); err != nil {
					return err
				}
			} else {
				writeBuildPhase(projectPath, "triage")
				var err error
				triageDecision, err = runTriageGate(cmd.Context(), projectPath, epicPath, prepareEngineName, userPrompt, promptSource, effortLevel, mode, os.Stdin, cmd.OutOrStdout(), runNoProjectOverview || runDryRun, runYes, runTriageOnly, runConfirmFile, runPlanner)
				if err != nil {
					return err
				}
			}
		}

		if runTriageOnly {
			// When --no-project-overview or --dry-run skipped the interactive
			// confirmation, the summary was never shown — display it now.
			// Otherwise ConfirmDecision already printed it.
			if triageDecision != nil && (runNoProjectOverview || runDryRun) {
				triage.DisplayTriageSummary(cmd.OutOrStdout(), triageDecision)
			}
			return nil
		}

		ep, err := epic.ParseEpic(epicPath)
		if err != nil {
			return err
		}
		// Apply effort override if user specified one and the epic doesn't have one
		if effortLevel != "" && ep.EffortLevel == "" {
			ep.EffortLevel = effortLevel
		} else if effortLevel != "" && ep.EffortLevel != "" && effortLevel != ep.EffortLevel {
			frlog.Log("WARNING: --effort %s ignored; epic already specifies @effort %s. To change effort level, re-run fry prepare with --effort %s.", effortLevel, ep.EffortLevel, effortLevel)
		}
		// Apply model override — unconditional, takes precedence over @model in epic.
		if runModel != "" {
			ep.AgentModel = runModel
		}
		if err := epic.ValidateEpic(ep); err != nil {
			return err
		}

		// --always-verify: force sanity checks, alignment, and audit regardless of effort/complexity.
		if runAlwaysVerify {
			ep.AuditAfterSprint = true
			if ep.MaxHealAttempts == 0 {
				ep.MaxHealAttempts = config.DefaultMaxHealAttempts
			}
			ep.MaxHealAttemptsSet = true
			// Generate sanity checks if none exist (simple tasks skip this).
			verifyPath := filepath.Join(projectPath, config.DefaultVerificationFile)
			if _, statErr := os.Stat(verifyPath); os.IsNotExist(statErr) {
				checks := triage.GenerateVerificationChecks(projectPath, ep.TotalSprints)
				if len(checks) > 0 {
					if writeErr := triage.WriteVerificationFile(verifyPath, checks); writeErr != nil {
						frlog.Log("WARNING: --always-verify: could not write sanity checks file: %v", writeErr)
					} else {
						frlog.Log("  VERIFY: generated heuristic sanity checks (--always-verify)")
					}
				} else {
					frlog.Log("WARNING: --always-verify: no recognized build system detected; skipping heuristic check generation")
				}
			}
		}

		buildDefault := config.DefaultEngine
		switch mode {
		case prepare.ModePlanning:
			buildDefault = config.DefaultPlanningEngine
		case prepare.ModeWriting:
			buildDefault = config.DefaultWritingEngine
		}
		engineName, err := engine.ResolveEngine(runEngine, ep.Engine, "", buildDefault)
		if err != nil {
			return err
		}
		runPlanner.SetDefault(engineName)

		mcpConfig := runMCPConfig
		if mcpConfig == "" {
			mcpConfig = ep.MCPConfig
		}
		if mcpConfig != "" {
			abs, absErr := filepath.Abs(mcpConfig)
			if absErr == nil {
				mcpConfig = abs
			}
		}
		var mcpOpts []engine.EngineOpt
		if mcpConfig != "" {
			mcpOpts = append(mcpOpts, engine.WithMCPConfig(mcpConfig))
		}

		buildEngine, err := runPlanner.Build(engineName, mcpOpts...)
		if err != nil {
			return err
		}

		// Validate --continue and --simple-continue flag conflicts
		if runContinue && runSimpleContinue {
			return fmt.Errorf("cannot use --continue with --simple-continue; use one or the other")
		}
		if runContinue {
			if runSprint > 0 {
				return fmt.Errorf("cannot use --continue with --sprint; --continue auto-detects the resume point")
			}
			if len(args) > 1 {
				return fmt.Errorf("cannot use --continue with positional sprint arguments")
			}
			if runResume {
				return fmt.Errorf("cannot use --continue with --resume; --continue auto-detects whether to resume")
			}
		}
		if runSimpleContinue {
			if runSprint > 0 {
				return fmt.Errorf("cannot use --simple-continue with --sprint")
			}
			if len(args) > 1 {
				return fmt.Errorf("cannot use --simple-continue with positional sprint arguments")
			}
			if runResume {
				return fmt.Errorf("cannot use --simple-continue with --resume")
			}
		}

		var startSprint, endSprint int
		var auditOnlyResume bool
		if runContinue || runSimpleContinue {
			frlog.Log("▶ CONTINUE  collecting build state...")
			state, collectErr := continuerun.CollectBuildState(cmd.Context(), projectPath, ep, runAlwaysVerify)
			if collectErr != nil {
				return fmt.Errorf("continue: %w", collectErr)
			}

			report := continuerun.FormatReport(state)
			fmt.Fprint(cmd.OutOrStdout(), report)
			fmt.Fprintln(cmd.OutOrStdout())

			var decision *continuerun.ContinueDecision
			if runSimpleContinue {
				frlog.Log("▶ CONTINUE  using heuristic continue (--simple-continue)...")
				decision = continuerun.HeuristicAnalyze(state)
			} else {
				continueOverride := ep.AuditModel
				if continueOverride == "" {
					continueOverride = ep.AgentModel
				}
				continueModel := engine.ResolveModel(continueOverride, currentEngineName(), string(ep.EffortLevel), engine.SessionContinue)
				var analyzeErr error
				decision, analyzeErr = continuerun.Analyze(cmd.Context(), continuerun.AnalyzeOpts{
					ProjectDir:  projectPath,
					State:       state,
					Engine:      buildEngine,
					Model:       continueModel,
					EffortLevel: string(ep.EffortLevel),
					Verbose:     frlog.Verbose,
				})
				if analyzeErr != nil {
					return fmt.Errorf("continue: %w", analyzeErr)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Decision: %s (sprint %d)\n", decision.Verdict, decision.StartSprint)
			fmt.Fprintf(cmd.OutOrStdout(), "Reason: %s\n", decision.Reason)
			if len(decision.Preconditions) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Pre-conditions:")
				for _, pc := range decision.Preconditions {
					fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", pc)
				}
			}

			switch decision.Verdict {
			case continuerun.VerdictAllComplete:
				fmt.Fprintf(cmd.OutOrStdout(), "All %d sprints already complete. Nothing to do.\n", ep.TotalSprints)
				return nil
			case continuerun.VerdictAuditIncomplete:
				resumeFinalizationOnly := resumeNeedsFinalizationOnly(state)
				if runNoAudit && !resumeFinalizationOnly {
					frlog.Log("All sprints complete. --no-audit prevents build audit; treating as complete.")
					fmt.Fprintf(cmd.OutOrStdout(), "All %d sprints complete. Build audit skipped (--no-audit).\n", ep.TotalSprints)
					return nil
				}
				if resumeFinalizationOnly {
					frlog.Log("All sprints complete but final build finalization did not finish. Resuming finalization...")
					fmt.Fprintf(cmd.OutOrStdout(), "All %d sprints complete. Resuming final build finalization...\n", ep.TotalSprints)
				} else {
					frlog.Log("All sprints complete but build audit did not finish. Resuming from build audit...")
					fmt.Fprintf(cmd.OutOrStdout(), "All %d sprints complete. Resuming from build audit...\n", ep.TotalSprints)
				}
				startSprint = ep.TotalSprints + 1 // > endSprint skips sprint loop, falls through to build audit
				endSprint = ep.TotalSprints
				auditOnlyResume = true
			case continuerun.VerdictBlocked:
				return fmt.Errorf("continue: blocked — %s", decision.Reason)
			case continuerun.VerdictResume:
				startSprint = decision.StartSprint
				endSprint = ep.TotalSprints
				runResume = true
			case continuerun.VerdictResumeFresh:
				startSprint = decision.StartSprint
				endSprint = ep.TotalSprints
			case continuerun.VerdictContinueNext:
				startSprint = decision.StartSprint
				endSprint = ep.TotalSprints
			default:
				return fmt.Errorf("continue: unexpected verdict %q", decision.Verdict)
			}

			// startSprint may be TotalSprints+1 for VerdictAuditIncomplete (skips sprint loop)
			if decision.Verdict != continuerun.VerdictAuditIncomplete {
				if startSprint < 1 || startSprint > ep.TotalSprints {
					return fmt.Errorf("continue: agent returned invalid sprint %d (total: %d)", startSprint, ep.TotalSprints)
				}
			}
		} else {
			rangeArgs := []string{}
			if len(args) > 1 {
				rangeArgs = args[1:]
			}
			if runSprint > 0 {
				if len(rangeArgs) > 0 {
					return fmt.Errorf("cannot use --sprint with positional sprint arguments")
				}
				rangeArgs = []string{strconv.Itoa(runSprint)}
			}
			startSprint, endSprint, err = resolveSprintRange(rangeArgs, ep.TotalSprints)
			if err != nil {
				return err
			}
		}

		if runDryRun {
			if auditOnlyResume {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to run — will resume from build audit only.")
				return nil
			}
			return printDryRunReport(cmd.OutOrStdout(), projectPath, epicPath, ep, currentEngineName(), startSprint, endSprint)
		}

		// Ensure git is initialised before strategy setup; branch/worktree
		// strategies require an existing repository.
		if err := git.InitGit(ctx, projectPath); err != nil {
			return err
		}

		// --- Git strategy setup ---
		freshInit := repoExistedBeforeRun && git.IsFreshlyInitialized(ctx, projectPath)
		if strategySetup == nil && gitStrategy != git.StrategyCurrent {
			gitStrategy = resolveRunGitStrategy(gitStrategy, triageDecision, repoExistedBeforeRun, freshInit)
			if gitStrategy == git.StrategyCurrent && runBranchName != "" {
				switch {
				case !repoExistedBeforeRun || freshInit:
					frlog.Log("WARNING: --branch-name ignored because git strategy resolved to current (new repo initialized for first build)")
				case triageDecision == nil:
					frlog.Log("WARNING: --branch-name ignored because git strategy resolved to current (epic already exists; use --git-strategy branch to force)")
				}
			}

			if gitStrategy != git.StrategyCurrent {
				branchName := runBranchName
				if branchName == "" {
					branchName = git.GenerateBranchName(ep.Name)
				}

				var setupErr error
				strategySetup, setupErr = git.SetupStrategy(ctx, git.StrategyOpts{
					ProjectDir: projectPath,
					Strategy:   gitStrategy,
					BranchName: branchName,
					EpicName:   ep.Name,
					ForceReuse: runContinue || runResume,
				})
				if setupErr != nil {
					return fmt.Errorf("git strategy setup: %w", setupErr)
				}

				projectPath = strategySetup.WorkDir
				originalProjectPath = strategySetup.OriginalDir

				// Re-resolve epicPath relative to redirected projectPath
				epicPath = filepath.Join(projectPath, epicArg)

				defer func() {
					if cleanupErr := strategySetup.Cleanup(); cleanupErr != nil {
						fmt.Fprintf(os.Stderr, "fry: warning: %v\n", cleanupErr)
					}
				}()

				if persistErr := git.PersistStrategy(originalProjectPath, strategySetup); persistErr != nil {
					frlog.Log("WARNING: could not persist git strategy: %v", persistErr)
				}

				frlog.Log("  GIT: strategy=%s  branch=%s  workdir=%s", strategySetup.Strategy, strategySetup.BranchName, strategySetup.WorkDir)
			}
		} else if strategySetup != nil {
			// Already set up from --continue detection
			defer func() {
				if cleanupErr := strategySetup.Cleanup(); cleanupErr != nil {
					fmt.Fprintf(os.Stderr, "fry: warning: %v\n", cleanupErr)
				}
			}()
		}

		results := initializeSprintResults(ep, startSprint, endSprint)
		var mu sync.Mutex
		currentSprint := 0
		epicName := ep.Name // guarded by mu; updated after replan
		// Clear any stale exit reason from a prior failed run before this build
		// starts so monitors and status readers see a clean active state.
		writeExitReason(projectPath, nil, 0)
		clearGracefulStopArtifacts(projectPath, originalProjectPath)

		// Persist exit reason so `fry status` can show why the build stopped.
		defer func() {
			mu.Lock()
			lastSprint := currentSprint
			mu.Unlock()
			writeExitReason(projectPath, retErr, lastSprint)
			if retErr != nil {
				if buildStatus != nil {
					buildStatus.Build.Engine = currentEngineName()
				}
				markBuildFailed(projectPath, originalProjectPath, buildStatus)
			}
		}()

		buildStart := time.Now()
		buildReport := report.BuildReport{
			EpicName:  ep.Name,
			StartTime: buildStart,
		}
		var validationChecklist []audit.ValidationItem
		sprintReportResults := make([]report.SprintResult, 0, endSprint-startSprint+1)
		sprintTokens := make([]metrics.SprintTokens, 0, endSprint-startSprint+1)

		signalCh := make(chan os.Signal, 1)
		signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(signalCh)

		interrupted := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-signalCh:
			}
			close(interrupted)
			cancel()
		}()

		wasInterrupted := func() bool {
			select {
			case <-interrupted:
				return true
			default:
				return false
			}
		}

		// Clamp CurrentSprint to a valid range for preflight. When auditOnlyResume
		// is true, startSprint is TotalSprints+1 (a sentinel to skip the sprint loop),
		// which is not a valid sprint number for preflight checks.
		preflightSprint := startSprint
		if auditOnlyResume {
			preflightSprint = ep.TotalSprints
		}
		if err := preflight.RunPreflight(preflight.PreflightConfig{
			ProjectDir:       projectPath,
			Engine:           currentEngineName(),
			DockerFromSprint: ep.DockerFromSprint,
			CurrentSprint:    preflightSprint,
			RequiredTools:    ep.RequiredTools,
			PreflightCmds:    ep.PreflightCmds,
		}); err != nil {
			return err
		}
		reviewSummary := review.DeviationSummary{
			TotalSprints: startSprintCount(startSprint, endSprint),
			AllLowRisk:   true,
		}
		if auditOnlyResume {
			reviewSummary.TotalSprints = ep.TotalSprints
		}

		var allDeferredFailures []deferredEntry
		modeStr := string(mode)

		// Persist mode for --continue auto-detection on subsequent runs
		modePath := filepath.Join(projectPath, config.BuildModeFile)
		if mkErr := os.MkdirAll(filepath.Dir(modePath), 0o755); mkErr != nil {
			frlog.Log("WARNING: could not create dir for build mode: %v", mkErr)
		} else if writeErr := os.WriteFile(modePath, []byte(modeStr+"\n"), 0o644); writeErr != nil {
			frlog.Log("WARNING: could not persist build mode: %v", writeErr)
		}

		// Write build phase for status tracking
		writeBuildPhase(projectPath, "sprint")
		// Also write phase to original project dir so status can find it when worktree is active
		if originalProjectPath != projectPath {
			writeBuildPhase(originalProjectPath, "sprint:worktree")
		}

		// Build run lineage metadata for immutable per-run snapshots.
		runMeta := &agent.RunMeta{
			RunID: agent.GenerateRunID(),
		}
		switch {
		case runResume:
			runMeta.RunType = agent.RunTypeResume
		case runContinue || runSimpleContinue:
			runMeta.RunType = agent.RunTypeContinue
		default:
			runMeta.RunType = agent.RunTypeFresh
		}
		if runMeta.RunType != agent.RunTypeFresh {
			runMeta.ParentRunID = agent.LatestRunID(projectPath)
		}

		// Initialize machine-readable build status for agent polling.
		buildStatus = &agent.BuildStatus{
			Version: 1,
			Run:     runMeta,
			Build: agent.BuildInfo{
				Epic:         ep.Name,
				Effort:       string(ep.EffortLevel),
				Engine:       currentEngineName(),
				Mode:         modeStr,
				TotalSprints: ep.TotalSprints,
				Status:       "running",
				Phase:        "sprint",
				StartedAt:    buildStart,
				GitBranch:    gitBranchFromHead(projectPath),
			},
			Sprints: initialSprintStatuses(projectPath, ep, startSprint, endSprint, runResume || runContinue || runSimpleContinue || auditOnlyResume),
		}
		writeCurrentBuildStatus()

		// Initialize observer metacognitive layer
		observerEnabled := runObserver && !runNoObserver && !runDryRun && ep.EffortLevel != epic.EffortFast
		observer.SetEnabled(observerEnabled)
		settings := consciousness.LoadSettings()
		var telemetryFlag *bool
		if runNoTelemetry {
			telemetryFlag = telemetryBoolPtr(false)
		} else if runTelemetry {
			telemetryFlag = telemetryBoolPtr(true)
		}
		telemetryEnabled := consciousness.TelemetryEnabled(telemetryFlag, settings)
		var collector *consciousness.Collector

		if observerEnabled {
			initFn := observer.InitNewSession
			sessionMode := consciousness.SessionModeNew
			if runResume || runContinue || runSimpleContinue || auditOnlyResume {
				initFn = observer.ResumeSession
				sessionMode = consciousness.SessionModeResume
			}
			if initErr := initFn(projectPath, ep.Name, string(ep.EffortLevel), ep.TotalSprints); initErr != nil {
				frlog.Log("WARNING: observer: init failed: %v", initErr)
				observerEnabled = false
			} else {
				var collectErr error
				collector, collectErr = consciousness.NewCollector(consciousness.CollectorOptions{
					ProjectDir:    projectPath,
					EpicName:      ep.Name,
					Engine:        currentEngineName(),
					EffortLevel:   string(ep.EffortLevel),
					TotalSprints:  ep.TotalSprints,
					CurrentSprint: startSprint,
					Mode:          sessionMode,
					UploadEnabled: telemetryEnabled,
				})
				if collectErr != nil {
					frlog.Log("WARNING: consciousness: init failed: %v", collectErr)
				}
			}
		}

		// Always relocate the supervisor to the worktree if the project
		// path changed, so it looks for manifests in the right place.
		// This must happen regardless of whether copilotShouldBootstrap
		// is true — the early bootstrap may have started the supervisor
		// before the worktree existed.
		if projectPath != originalProjectPath {
			copilotSupervisor.Relocate(projectPath)
		}

		// Copilot promotion (Phase 11). Now that the epic is parsed and
		// the worktree is set up, update the existing copilot session
		// with real epic details. The session stays continuous — same
		// session ID, same conversation history. The copilot picks up
		// the new context on its next tick.
		//
		// If the copilot was NOT early-bootstrapped (auto-enable for max
		// effort where effort is unknown until epic parse), do the first
		// bootstrap here.
		if copilotShouldBootstrap(cmd, ep.EffortLevel) {
			existingManifest, _ := copilot.ReadManifest(projectPath)
			if existingManifest == nil && originalProjectPath != projectPath {
				existingManifest, _ = copilot.ReadManifest(originalProjectPath)
			}

			if existingManifest != nil && existingManifest.SessionID != "" {
				// Copilot already running — promote with real details.
				if promoteErr := copilot.PromoteCopilot(copilot.PromoteOpts{
					ProjectDir:   projectPath,
					OldDir:       originalProjectPath,
					EpicName:     ep.Name,
					EffortLevel:  string(ep.EffortLevel),
					TotalSprints: ep.TotalSprints,
				}); promoteErr != nil {
					frlog.Log("WARNING: copilot promote failed: %v", promoteErr)
				} else {
					frlog.Log("  COPILOT: promoted with epic=%q, sprints=%d", ep.Name, ep.TotalSprints)
				}
				// Supervisor already relocated unconditionally above.
			} else {
				// No early bootstrap (auto-enable for max effort) — first bootstrap now.
				copilotEngine := resolveCopilotEngine()
				frySrcDir := copilot.DiscoverFrySourceDir(runCopilotFrySource)
				passive := runCopilotPassive || frySrcDir == ""

				bootstrapResult, bootErr := copilot.Bootstrap(copilot.BootstrapOpts{
					ProjectDir:   projectPath,
					FrySourceDir: frySrcDir,
					Engine:       copilotEngine,
					Model:        runCopilotModel,
					EpicName:     ep.Name,
					EffortLevel:  string(ep.EffortLevel),
					TotalSprints: ep.TotalSprints,
					BuildPID:     os.Getpid(),
					Interval:     runCopilotInterval,
					RunID:        time.Now().UTC().Format("20060102-150405"),
					Passive:      passive,
					DryRun:       runDryRun,
					Stdout:       cmd.OutOrStdout(),
				})
				if bootErr != nil {
					frlog.Log("WARNING: copilot bootstrap failed: %v", bootErr)
				} else if bootstrapResult != nil && bootstrapResult.Manifest != nil {
					if bootstrapResult.Manifest.Mode == copilot.ModeActive {
						frlog.Log("  COPILOT: active session %s (engine=%s, interval=%s)",
							bootstrapResult.Manifest.SessionID, copilotEngine, runCopilotInterval)
					}
					if bootstrapResult.Scheduler != nil {
						copilotSupervisor.SetScheduler(bootstrapResult.Scheduler)
					}
				}
			}
		}

		// Load identity disposition for sprint prompts
		var identityDisposition string
		if disp, dispErr := consciousness.LoadDisposition(); dispErr == nil {
			identityDisposition = disp
		}

		uploadTimeout := time.Duration(config.UploadTimeoutSeconds) * time.Second
		flushPendingUploads := func() {
			if collector == nil || !telemetryEnabled {
				return
			}
			_ = consciousness.UploadPendingInBackground(config.ConsciousnessAPIURL, config.ConsciousnessWriteKey, projectPath, uploadTimeout)
		}
		flushPendingUploads()
		persistCheckpoint := func(baseCtx context.Context, checkpoint consciousness.ObservationCheckpoint) {
			if collector == nil {
				return
			}
			persisted, persistErr := collector.AddCheckpoint(checkpoint)
			if persistErr != nil {
				frlog.Log("WARNING: consciousness: could not persist checkpoint: %v", persistErr)
				return
			}
			if collectorErr := collector.SetCurrentSprint(checkpoint.SprintNum); collectorErr != nil {
				frlog.Log("WARNING: consciousness: could not update session sprint: %v", collectorErr)
			}
			if persisted.ParseStatus == consciousness.ParseStatusFailed {
				return
			}

			distillCtx := baseCtx
			cancel := func() {}
			if distillCtx == nil || distillCtx.Err() != nil {
				distillCtx, cancel = context.WithTimeout(context.Background(), uploadTimeout)
			}
			defer cancel()

			distillEngine, distillErr := runPlanner.Build(currentEngineName(), mcpOpts...)
			if distillErr != nil {
				frlog.Log("WARNING: consciousness: could not create distillation engine: %v", distillErr)
				return
			}
			distillModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionExperienceSummary)
			summary, distillErr := consciousness.DistillCheckpoint(distillCtx, consciousness.DistillOpts{
				ProjectDir:  projectPath,
				Engine:      distillEngine,
				Model:       distillModel,
				EffortLevel: string(ep.EffortLevel),
				Record:      collector.GetRecord(),
				Checkpoint:  persisted,
				Verbose:     frlog.Verbose,
			})
			if distillErr != nil {
				frlog.Log("WARNING: consciousness: checkpoint distillation failed: %v", distillErr)
				if err := collector.RecordDistillationFailure(); err != nil {
					frlog.Log("WARNING: consciousness: could not record distillation failure: %v", err)
				}
				return
			}
			if err := collector.RecordDistillation(summary); err != nil {
				frlog.Log("WARNING: consciousness: could not persist checkpoint summary: %v", err)
				return
			}
			flushPendingUploads()
		}

		// Harness self-check: validate sanity check targets before the build loop.
		verificationPath := filepath.Join(projectPath, ep.VerificationFile)
		if harnessChecks, parseErr := verify.ParseVerification(verificationPath); parseErr == nil && len(harnessChecks) > 0 {
			if harnessResult := verify.ValidateHarness(projectPath, harnessChecks); harnessResult.HasIssues() {
				frlog.Log("  HARNESS: %d issue(s) found in sanity check targets", len(harnessResult.Issues))
				for _, issue := range harnessResult.Issues {
					frlog.Log("  HARNESS: sprint %d — %s: %s (%s)", issue.Sprint, issue.Type, issue.Message, issue.Target)
				}
			}
		}

		exitErr := error(nil)
		for sprintNum := startSprint; sprintNum <= endSprint; sprintNum++ {
			if sprintNum < 1 || sprintNum > len(ep.Sprints) {
				return fmt.Errorf("sprint %d out of range [1, %d]", sprintNum, len(ep.Sprints))
			}
			spr := &ep.Sprints[sprintNum-1]

			mu.Lock()
			currentSprint = sprintNum
			mu.Unlock()
			if collector != nil {
				if err := collector.SetCurrentSprint(sprintNum); err != nil {
					frlog.Log("WARNING: consciousness: could not update current sprint: %v", err)
				}
			}

			if observerEnabled {
				_ = observer.EmitEvent(projectPath, observer.Event{
					Type:   observer.EventSprintStart,
					Sprint: spr.Number,
					Data:   map[string]string{"name": spr.Name},
				})
			}

			// Update build status for agent polling
			buildStatus.Build.CurrentSprint = spr.Number
			sprintStartedAt := time.Now()
			buildStatus.Sprints = append(buildStatus.Sprints, agent.SprintStatus{
				Number:    spr.Number,
				Name:      spr.Name,
				Status:    "running",
				StartedAt: &sprintStartedAt,
			})
			writeCurrentBuildStatus()
			_ = copilot.WriteStateSnapshot(projectPath)

			if ep.DockerFromSprint > 0 && sprintNum >= ep.DockerFromSprint {
				if err := docker.EnsureDockerUp(ctx, projectPath, ep.DockerReadyCmd, ep.DockerReadyTimeout); err != nil {
					return err
				}
			}

			// Sprint-scoped prerequisite check: infer env vars, Docker, tools from prompt.
			if pfResult := preflight.RunSprintPreflight(spr.Prompt); pfResult != nil && pfResult.HasBlockers() {
				frlog.Log("  PREFLIGHT: sprint %d has missing prerequisites: %s", spr.Number, pfResult.Summary())
				if len(buildStatus.Sprints) > 0 {
					idx := findSprintStatusIndex(buildStatus, spr.Number)
					if idx >= 0 {
						buildStatus.Sprints[idx].Warnings = append(buildStatus.Sprints[idx].Warnings,
							"missing prerequisites: "+pfResult.Summary())
						writeCurrentBuildStatus()
					}
				}
			}

			if err := shellhook.Run(ctx, projectPath, ep.PreSprintCmd); err != nil {
				return err
			}

			// Skip epic progress reset in resume mode to preserve prior context
			if !runResume && sprint.ShouldResetEpicProgress(startSprint, sprintNum, endSprint, ep.TotalSprints) {
				if err := sprint.InitEpicProgress(projectPath, ep.Name); err != nil {
					return err
				}
			}

			// In resume mode, the first sprint skips iterations and goes straight
			// to sanity checks + alignment with a boosted attempt budget.
			sprintStart := time.Now()
			var result *sprint.SprintResult
			if runResume && sprintNum == startSprint {
				result, err = sprint.ResumeSprint(ctx, sprint.RunConfig{
					ProjectDir:          projectPath,
					Epic:                ep,
					Sprint:              spr,
					Engine:              buildEngine,
					Verbose:             frlog.Verbose,
					DryRun:              false,
					UserPrompt:          userPrompt,
					StartSprint:         startSprint,
					EndSprint:           endSprint,
					Mode:                modeStr,
					IdentityDisposition: identityDisposition,
				})
			} else {
				result, err = sprint.RunSprint(ctx, sprint.RunConfig{
					ProjectDir:          projectPath,
					Epic:                ep,
					Sprint:              spr,
					Engine:              buildEngine,
					Verbose:             frlog.Verbose,
					DryRun:              false,
					UserPrompt:          userPrompt,
					StartSprint:         startSprint,
					EndSprint:           endSprint,
					Mode:                modeStr,
					IdentityDisposition: identityDisposition,
				})
			}
			if err != nil {
				var exitReq *steering.ExitRequestError
				if errors.As(err, &exitReq) {
					if settleErr := settleGracefulExit(
						ctx,
						cmd.OutOrStdout(),
						projectPath,
						originalProjectPath,
						buildStatus,
						ep,
						spr,
						exitReq.Phase,
						exitReq.Detail,
						steering.ResumeVerdictResume,
						true,
						observerEnabled,
					); settleErr != nil {
						return settleErr
					}
					return nil
				}
				if wasInterrupted() {
					mu.Lock()
					activeSprint := currentSprint
					name := epicName
					mu.Unlock()
					if collector != nil {
						persistCheckpoint(context.Background(), consciousness.ObservationCheckpoint{
							CheckpointType: consciousness.CheckpointTypeInterruption,
							WakePoint:      "interruption",
							SprintNum:      activeSprint,
							ParseStatus:    consciousness.ParseStatusOK,
							ParseError:     "build interrupted by signal",
						})
					}
					if activeSprint > 0 {
						commitCtx, commitCancel := context.WithTimeout(context.Background(), 10*time.Second)
						if err := git.CommitPartialWork(commitCtx, projectPath, name, activeSprint, spr.Name); err != nil {
							fmt.Fprintf(os.Stderr, "fry: warning: failed to commit partial work: %v\n", err)
						}
						commitCancel()
					}
					exitErr = fmt.Errorf("interrupted by signal")
					break
				}
				return err
			}

			mu.Lock()
			results[sprintNum-startSprint] = *result
			mu.Unlock()

			// Layer 1 — Tier C: handle PAUSED result
			if result.Status == sprint.StatusPaused {
				if settleErr := settleGracefulExit(
					ctx,
					cmd.OutOrStdout(),
					projectPath,
					originalProjectPath,
					buildStatus,
					ep,
					spr,
					result.PausePhase,
					result.PauseDetail,
					steering.ResumeVerdictResume,
					!result.PauseCheckpointed,
					observerEnabled,
				); settleErr != nil {
					return settleErr
				}
				return nil
			}

			if observerEnabled {
				_ = observer.EmitEvent(projectPath, observer.Event{
					Type:   observer.EventSprintComplete,
					Sprint: spr.Number,
					Data: map[string]string{
						"status":             result.Status,
						"duration":           result.Duration.Round(time.Second).String(),
						"alignment_attempts": strconv.Itoa(result.HealAttempts),
					},
				})
				if result.HealAttempts > 0 {
					_ = observer.EmitEvent(projectPath, observer.Event{
						Type:   observer.EventAlignmentComplete,
						Sprint: spr.Number,
						Data: map[string]string{
							"attempts": strconv.Itoa(result.HealAttempts),
							"status":   result.Status,
						},
					})
				}
			}

			// Collect per-sprint data for the build report and token summary.
			sprintEnd := time.Now()
			sprintPassed := isPassStatus(result.Status)
			sprintVerify := &report.VerificationResult{
				TotalChecks:  result.VerificationTotalCount,
				PassedChecks: result.VerificationPassCount,
				FailedChecks: result.VerificationTotalCount - result.VerificationPassCount,
			}
			for _, cr := range result.VerificationResults {
				sprintVerify.CheckResults = append(sprintVerify.CheckResults, report.CheckResult{
					Name:    cr.Check.Command + cr.Check.Path,
					Type:    cr.Check.Type.String(),
					Passed:  cr.Passed,
					Message: truncateString(cr.Output, 4096),
				})
			}
			if result.VerificationTotalCount == 0 {
				sprintVerify = nil
			}
			var sprintTokenUsage *metrics.TokenUsage
			if (runShowTokens || runJSONReport) && result.SprintLogPath != "" {
				if logData, readErr := os.ReadFile(result.SprintLogPath); readErr == nil {
					u := metrics.ParseTokens(currentEngineName(), string(logData))
					if u.Total > 0 {
						sprintTokenUsage = &u
					}
				}
			}
			reportSprint := report.SprintResult{
				SprintNum:    spr.Number,
				Name:         spr.Name,
				StartTime:    sprintStart,
				EndTime:      sprintEnd,
				Passed:       sprintPassed,
				HealAttempts: result.HealAttempts,
				Verification: sprintVerify,
				TokenUsage:   sprintTokenUsage,
			}
			sprintReportResults = append(sprintReportResults, reportSprint)
			if sprintTokenUsage != nil {
				sprintTokens = append(sprintTokens, metrics.SprintTokens{
					SprintNum: spr.Number,
					Usage:     *sprintTokenUsage,
				})
			}

			if len(result.DeferredFailures) > 0 {
				allDeferredFailures = append(allDeferredFailures, deferredEntry{
					SprintNumber: spr.Number,
					SprintName:   spr.Name,
					Failures:     result.DeferredFailures,
				})
				writeDeferredFailuresArtifact(projectPath, allDeferredFailures)
				frlog.Log("  WARNING: %d deferred sanity check failures (within %d%% threshold)",
					len(result.DeferredFailures), ep.MaxFailPercent)
			}

			// Update build status with sprint result
			updateBuildStatusSprint(buildStatus, spr.Number, result)
			writeCurrentBuildStatus()
			writeRollingResults(projectPath, buildStatus)
			_ = copilot.WriteStateSnapshot(projectPath)

			if isPassStatus(result.Status) {
				if steering.HasStopRequest(projectPath) {
					if settleErr := settleGracefulExit(
						ctx,
						cmd.OutOrStdout(),
						projectPath,
						originalProjectPath,
						buildStatus,
						ep,
						spr,
						"sprint_post_run",
						"after sprint verification and before sprint audit",
						steering.ResumeVerdictResume,
						true,
						observerEnabled,
					); settleErr != nil {
						return settleErr
					}
					return nil
				}

				// Sprint audit
				if ep.AuditAfterSprint && !runNoAudit && (ep.EffortLevel != epic.EffortFast || runAlwaysVerify) {
					buildStatus.Build.Phase = "audit"
					writeCurrentBuildStatus()
					writeBuildPhase(projectPath, "audit")
					if originalProjectPath != projectPath {
						writeBuildPhase(originalProjectPath, "audit:worktree")
					}
					auditEngine, err := resolveAuditEngine(runPlanner, currentEngineName(), ep.AuditEngine, mcpOpts...)
					if err != nil {
						return err
					}
					gitDiff, err := git.GitDiffForAudit(ctx, projectPath)
					if err != nil {
						frlog.Log("WARNING: could not capture git diff for audit: %v", err)
						gitDiff = "(git diff unavailable)"
					}
					complexity := audit.ClassifyComplexity(gitDiff, modeStr)
					frlog.Log("  AUDIT: classified sprint complexity as %s", complexity)
					auditResult, err := audit.RunAuditLoop(ctx, audit.AuditOpts{
						ProjectDir: projectPath,
						Sprint:     spr,
						Epic:       ep,
						Engine:     auditEngine,
						Complexity: complexity,
						GitDiff:    gitDiff,
						DiffFn:     func() (string, error) { return git.GitDiffForAudit(ctx, projectPath) },
						ProgressFn: func(progress audit.AuditProgress) {
							updateBuildStatusAuditProgress(buildStatus, spr.Number, progress)
							writeCurrentBuildStatus()
						},
						Verbose: frlog.Verbose,
						Mode:    modeStr,
					})
					if err != nil {
						var exitReq *steering.ExitRequestError
						if errors.As(err, &exitReq) {
							if settleErr := settleGracefulExit(
								ctx,
								cmd.OutOrStdout(),
								projectPath,
								originalProjectPath,
								buildStatus,
								ep,
								spr,
								exitReq.Phase,
								exitReq.Detail,
								steering.ResumeVerdictResume,
								true,
								observerEnabled,
							); settleErr != nil {
								return settleErr
							}
							return nil
						}
						return err
					}
					if auditResult.Metrics != nil {
						auditResult.Metrics.EscapedToBuildAudit = totalFindingCount(auditResult.SeverityCounts)
						if err := writeAuditMetricsArtifact(projectPath, spr.Number, auditResult.Metrics); err != nil {
							frlog.Log("WARNING: could not write audit metrics artifact: %v", err)
						}
						frlog.Log("  AUDIT METRICS: %d calls, %dms, %.0f%% no-op rate, %.1f verify yield",
							auditResult.Metrics.TotalCalls(),
							auditResult.Metrics.TotalDurationMs(),
							auditResult.Metrics.NoOpRate()*100,
							auditResult.Metrics.VerifyYield(),
						)
					}
					if !auditResult.Passed {
						// Blocker findings are advisory — log them but do not
						// halt the build. The fix loop already attempted remediation;
						// blockers that couldn't be fixed are surfaced in the result
						// for the user but do not prevent forward progress.
						if auditResult.Blocked && len(auditResult.Blockers) > 0 {
							blockerSummary := auditSummarizeBlockers(auditResult)
							frlog.Log("  AUDIT: advisory blockers — %s", blockerSummary)
							mu.Lock()
							results[sprintNum-startSprint].AuditWarning = blockerSummary
							mu.Unlock()
						}
						if auditResult.Blocking {
							frlog.Log("  AUDIT: FAILED — %s remain after %d audit cycles",
								audit.FormatCounts(auditResult.SeverityCounts), auditResult.Iterations)
							if cleanupErr := audit.Cleanup(projectPath); cleanupErr != nil {
								frlog.Log("WARNING: audit cleanup failed: %v", cleanupErr)
							}
							failStatus := fmt.Sprintf("FAIL (audit: %s)", auditResult.MaxSeverity)
							mu.Lock()
							results[sprintNum-startSprint].Status = failStatus
							mu.Unlock()
							if err := sprint.AppendToEpicProgress(projectPath,
								fmt.Sprintf("## Sprint %d: %s \u2014 %s\n\n", spr.Number, spr.Name, failStatus)); err != nil {
								frlog.Log("warning: append epic progress: %v", err)
							}
							fmt.Fprintf(cmd.OutOrStdout(), "Resume:   fry run --resume --sprint %d\n", spr.Number)
							fmt.Fprintf(cmd.OutOrStdout(), "Restart:  fry run --sprint %d\n", spr.Number)
							fmt.Fprintf(cmd.OutOrStdout(), "Continue: fry run --continue\n")
							exitErr = fmt.Errorf("sprint %d audit failed: %s after %d cycles",
								spr.Number, failStatus, auditResult.Iterations)
							break
						}
						warning := fmt.Sprintf("%s remain after %d audit cycles (advisory)",
							audit.FormatCounts(auditResult.SeverityCounts), auditResult.Iterations)
						frlog.Log("  AUDIT: %s", warning)
						mu.Lock()
						results[sprintNum-startSprint].AuditWarning = warning
						mu.Unlock()
					}
					// Note LOW remainders at high/max effort (audit passed but LOW items remain)
					if auditResult.Passed && auditResult.SeverityCounts["LOW"] > 0 &&
						(ep.EffortLevel == epic.EffortHigh || ep.EffortLevel == epic.EffortMax) {
						lowNote := fmt.Sprintf("%d LOW issues remain after %d audit cycles (non-blocking)",
							auditResult.SeverityCounts["LOW"], auditResult.Iterations)
						frlog.Log("  AUDIT: %s", lowNote)
						mu.Lock()
						if results[sprintNum-startSprint].AuditWarning == "" {
							results[sprintNum-startSprint].AuditWarning = lowNote
						} else {
							results[sprintNum-startSprint].AuditWarning += "; " + lowNote
						}
						mu.Unlock()
					}
					if cleanupErr := audit.Cleanup(projectPath); cleanupErr != nil {
						frlog.Log("WARNING: audit cleanup failed: %v", cleanupErr)
					}

					if observerEnabled {
						auditData := map[string]string{
							"passed":     strconv.FormatBool(auditResult.Passed),
							"iterations": strconv.Itoa(auditResult.Iterations),
						}
						if auditResult.MaxSeverity != "" {
							auditData["max_severity"] = auditResult.MaxSeverity
						}
						_ = observer.EmitEvent(projectPath, observer.Event{
							Type:   observer.EventAuditComplete,
							Sprint: spr.Number,
							Data:   auditData,
						})
					}

					// Update build status with audit result
					updateBuildStatusAudit(buildStatus, spr.Number, auditResult)
					writeCurrentBuildStatus()
					buildStatus.Build.Phase = "sprint"
					writeCurrentBuildStatus()
					writeBuildPhase(projectPath, "sprint")
					if originalProjectPath != projectPath {
						writeBuildPhase(originalProjectPath, "sprint:worktree")
					}
					_ = copilot.WriteStateSnapshot(projectPath)
				}

				if steering.HasStopRequest(projectPath) {
					if settleErr := settleGracefulExit(
						ctx,
						cmd.OutOrStdout(),
						projectPath,
						originalProjectPath,
						buildStatus,
						ep,
						spr,
						"sprint_audit",
						"after sprint audit and before checkpoint",
						steering.ResumeVerdictResume,
						true,
						observerEnabled,
					); settleErr != nil {
						return settleErr
					}
					return nil
				}

				frlog.Log("  GIT: checkpoint — sprint %d complete", spr.Number)
				if err := git.GitCheckpoint(ctx, projectPath, ep.Name, spr.Number, spr.Name, "complete"); err != nil {
					return err
				}

				// Recover AD-status files after checkpoint. If the commit didn't
				// capture all files (e.g. engine cleanup race), materialise them
				// from the index now so subsequent sprints and fast-effort builds
				// (which skip audit and its per-cycle recovery) have files on disk.
				if adFiles, adErr := git.ListADFiles(ctx, projectPath); adErr == nil && len(adFiles) > 0 {
					frlog.Log("  GIT: recovering %d AD-status files after checkpoint", len(adFiles))
					_ = git.CheckoutIndexAll(ctx, projectPath)
				}

				// Remove nested .git directories left by tools like create-next-app.
				// These cause the parent repo to record gitlinks instead of tracking
				// files normally. Runs in fry's process to bypass engine sandbox
				// restrictions that block rm -rf inside the AI agent session.
				if cleaned, cleanErr := git.CleanNestedGitDirs(projectPath); cleanErr == nil && len(cleaned) > 0 {
					frlog.Log("  GIT: removed %d nested .git directory(ies): %v", len(cleaned), cleaned)
				}

				compactEngine, err := runPlanner.Build(currentEngineName(), mcpOpts...)
				if err != nil {
					return err
				}
				compactModel := engine.ResolveModel(ep.AgentModel, currentEngineName(), string(ep.EffortLevel), engine.SessionCompaction)
				compacted, err := sprint.CompactSprintProgress(ctx, projectPath, spr.Number, spr.Name, result.Status, compactEngine, ep.CompactWithAgent, compactModel, string(ep.EffortLevel))
				if err != nil {
					return err
				}
				if err := sprint.AppendToEpicProgress(projectPath, compacted+"\n"); err != nil {
					return err
				}
				frlog.Log("  GIT: checkpoint — sprint %d compacted", spr.Number)
				if err := git.GitCheckpoint(ctx, projectPath, ep.Name, spr.Number, spr.Name, "compacted"); err != nil {
					return err
				}

				if steering.HasStopRequest(projectPath) {
					verdict := settledSprintResumeVerdict(ep, spr)
					detail := "after sprint checkpoint and compaction"
					if verdict == steering.ResumeVerdictAuditIncomplete {
						detail = "after the final sprint settled and before final build finalization"
					}
					if spr.Number < ep.TotalSprints || verdict == steering.ResumeVerdictAuditIncomplete {
						if settleErr := settleGracefulExit(
							ctx,
							cmd.OutOrStdout(),
							projectPath,
							originalProjectPath,
							buildStatus,
							ep,
							spr,
							"sprint_boundary",
							detail,
							verdict,
							false,
							observerEnabled,
						); settleErr != nil {
							return settleErr
						}
						return nil
					}
				}

				// Layer 1 — Tier B: Hold at sprint boundary
				if steering.IsHoldRequested(projectPath) {
					_ = steering.ClearHold(projectPath)
					remainingSprints := ep.TotalSprints - spr.Number
					holdPrompt := fmt.Sprintf(
						"Sprint %d of %d complete (%s). Holding for your review.\n\n"+
							"Options:\n"+
							"- Say \"continue\" to proceed as planned\n"+
							"- Provide a directive for the next sprint\n"+
							"- Say \"replan: <instructions>\" to replan remaining sprints (%d remaining)\n",
						spr.Number, ep.TotalSprints, spr.Name, remainingSprints)
					frlog.Log("  STEERING: hold requested — waiting for user decision")
					_ = steering.WriteDecisionNeeded(projectPath, holdPrompt)
					if observerEnabled {
						_ = observer.EmitEvent(projectPath, observer.Event{
							Type:   observer.EventDecisionNeeded,
							Sprint: spr.Number,
							Data: map[string]string{
								"reason":            "hold_after_sprint",
								"completed_sprint":  spr.Name,
								"remaining_sprints": strconv.Itoa(remainingSprints),
							},
						})
					}

					decision, waitErr := steering.WaitForDecision(ctx, projectPath)
					if waitErr != nil {
						_ = steering.ClearDecisionNeeded(projectPath)
						var exitReq *steering.ExitRequestError
						if errors.As(waitErr, &exitReq) {
							verdict := settledSprintResumeVerdict(ep, spr)
							if settleErr := settleGracefulExit(
								ctx,
								cmd.OutOrStdout(),
								projectPath,
								originalProjectPath,
								buildStatus,
								ep,
								spr,
								exitReq.Phase,
								"while holding after the sprint settled and before final build finalization",
								verdict,
								false,
								observerEnabled,
							); settleErr != nil {
								return settleErr
							}
							return nil
						}
						return fmt.Errorf("steering: wait for decision: %w", waitErr)
					}
					_ = steering.ClearDecisionNeeded(projectPath)
					if observerEnabled {
						_ = observer.EmitEvent(projectPath, observer.Event{
							Type:   observer.EventDecisionReceived,
							Sprint: spr.Number,
							Data:   map[string]string{"preview": truncateString(decision, 200)},
						})
					}
					frlog.Log("  STEERING: decision received — %s", truncateString(decision, 80))

					decision = strings.TrimSpace(decision)
					if decision == "" {
						frlog.Log("  STEERING: empty decision received — continuing as planned")
					}
					lowerDecision := strings.ToLower(decision)
					if strings.HasPrefix(lowerDecision, "replan:") || lowerDecision == "replan" {
						// Trigger replan with user's instructions
						frlog.Log("  STEERING: triggering replan of remaining sprints")
						replanEngine, rErr := resolveReviewEngine(runPlanner, currentEngineName(), ep.ReviewEngine, mcpOpts...)
						if rErr != nil {
							return rErr
						}
						replanModel := engine.ResolveModel(ep.ReviewModel, replanEngine.Name(), string(ep.EffortLevel), engine.SessionReplan)
						// Strip the "replan:" prefix case-insensitively
						userInstructions := decision
						if strings.HasPrefix(lowerDecision, "replan:") {
							userInstructions = decision[len("replan:"):]
						}
						userInstructions = strings.TrimSpace(userInstructions)
						if userInstructions == "" {
							userInstructions = "Replan the remaining sprints based on completed work."
						}
						affectedSprints := make([]int, 0, ep.TotalSprints-spr.Number)
						for i := spr.Number + 1; i <= ep.TotalSprints; i++ {
							affectedSprints = append(affectedSprints, i)
						}
						devSpec := &review.DeviationSpec{
							Trigger:         "User-requested replan via build steering: " + userInstructions,
							AffectedSprints: affectedSprints,
							RiskAssessment:  "user-directed",
							Details:         userInstructions,
						}
						if rpErr := review.RunReplan(ctx, review.ReplanOpts{
							ProjectDir:      projectPath,
							EpicPath:        epicPath,
							DeviationSpec:   devSpec,
							CompletedSprint: spr.Number,
							MaxScope:        ep.MaxDeviationScope,
							Engine:          replanEngine,
							Model:           replanModel,
							EffortLevel:     string(ep.EffortLevel),
							Verbose:         frlog.Verbose,
						}); rpErr != nil {
							return fmt.Errorf("steering replan: %w", rpErr)
						}
						// Re-parse the modified epic
						ep, err = epic.ParseEpic(epicPath)
						if err != nil {
							return fmt.Errorf("re-parse epic after steering replan: %w", err)
						}
						endSprint = ep.TotalSprints
						// Refresh spr pointer to the new epic's sprint data
						if sprintNum >= 1 && sprintNum <= len(ep.Sprints) {
							spr = &ep.Sprints[sprintNum-1]
						}
						// Grow results slice if the replan added sprints
						for len(results) < endSprint-startSprint+1 {
							idx := startSprint + len(results)
							name := ""
							if idx >= 1 && idx <= len(ep.Sprints) {
								name = ep.Sprints[idx-1].Name
							}
							results = append(results, sprint.SprintResult{
								Number: idx,
								Name:   name,
								Status: sprint.StatusSkipped,
							})
						}
					} else if strings.ToLower(decision) != "continue" && decision != "" {
						// Inject as user prompt for the next sprint
						userPrompt = decision
					}
					// else: "continue" — proceed as planned
				}

				if ep.ReviewBetweenSprints && !runNoReview && spr.Number < ep.TotalSprints && ep.EffortLevel != epic.EffortFast {
					reviewSummary.ReviewsConducted++

					reviewEngine, err := resolveReviewEngine(runPlanner, currentEngineName(), ep.ReviewEngine, mcpOpts...)
					if err != nil {
						return err
					}
					reviewResult, err := review.RunSprintReview(ctx, review.RunReviewOpts{
						ProjectDir:      projectPath,
						SprintNum:       spr.Number,
						TotalSprints:    ep.TotalSprints,
						SprintName:      spr.Name,
						Epic:            ep,
						Engine:          reviewEngine,
						SimulateVerdict: runSimulateReview,
						Verbose:         frlog.Verbose,
						Mode:            modeStr,
					})
					if err != nil {
						return err
					}

					if reviewResult.Verdict == review.VerdictDeviate && reviewResult.Deviation != nil {
						frlog.Log("  REVIEW: verdict DEVIATE — replanning sprints %s (risk: %s)",
							formatAffectedSprints(reviewResult.Deviation.AffectedSprints),
							reviewResult.Deviation.RiskAssessment)
					} else {
						frlog.Log("  REVIEW: verdict %s", reviewResult.Verdict)
					}

					if observerEnabled {
						reviewData := map[string]string{"verdict": string(reviewResult.Verdict)}
						if reviewResult.Deviation != nil {
							reviewData["trigger"] = reviewResult.Deviation.Trigger
							reviewData["risk_assessment"] = reviewResult.Deviation.RiskAssessment
							reviewData["affected_sprints"] = formatAffectedSprints(reviewResult.Deviation.AffectedSprints)
							if reviewResult.Deviation.Details != "" {
								reviewData["deviation_details"] = textutil.TruncateUTF8(reviewResult.Deviation.Details, 500)
							}
						}
						_ = observer.EmitEvent(projectPath, observer.Event{
							Type:   observer.EventReviewComplete,
							Sprint: spr.Number,
							Data:   reviewData,
						})
					}

					// Update build status with review verdict
					updateBuildStatusReview(buildStatus, spr.Number, string(reviewResult.Verdict))
					writeCurrentBuildStatus()

					entry := review.DeviationLogEntry{
						SprintNum:  spr.Number,
						SprintName: spr.Name,
						Verdict:    reviewResult.Verdict,
						Impact:     strings.TrimSpace(reviewResult.RawOutput),
					}
					if reviewResult.Deviation != nil {
						entry.Trigger = reviewResult.Deviation.Trigger
						entry.RiskAssessment = reviewResult.Deviation.RiskAssessment
						entry.AffectedSprints = reviewResult.Deviation.AffectedSprints
						if strings.TrimSpace(reviewResult.Deviation.Details) != "" {
							entry.Impact = strings.TrimSpace(reviewResult.Deviation.Details)
						}
						if !strings.Contains(strings.ToLower(reviewResult.Deviation.RiskAssessment), "low") {
							reviewSummary.AllLowRisk = false
						}
					}
					if err := review.AppendDeviationLog(projectPath, entry); err != nil {
						return err
					}

					if reviewResult.Verdict == review.VerdictDeviate {
						reviewSummary.DeviationsApplied++
						if reviewResult.Deviation == nil {
							return fmt.Errorf("review requested deviation without a deviation spec")
						}
						replanModel := engine.ResolveModel(ep.ReviewModel, reviewEngine.Name(), string(ep.EffortLevel), engine.SessionReplan)
						if err := review.RunReplan(ctx, review.ReplanOpts{
							ProjectDir:      projectPath,
							EpicPath:        epicPath,
							DeviationSpec:   reviewResult.Deviation,
							CompletedSprint: spr.Number,
							MaxScope:        ep.MaxDeviationScope,
							Engine:          reviewEngine,
							Model:           replanModel,
							EffortLevel:     string(ep.EffortLevel),
							Verbose:         frlog.Verbose,
						}); err != nil {
							return err
						}
						ep, err = epic.ParseEpic(epicPath)
						if err != nil {
							return err
						}
						if runModel != "" {
							ep.AgentModel = runModel
						}
						if err := epic.ValidateEpic(ep); err != nil {
							return err
						}
						mu.Lock()
						epicName = ep.Name
						mu.Unlock()
						verificationPath := ep.VerificationFile
						if !filepath.IsAbs(verificationPath) {
							verificationPath = filepath.Join(projectPath, verificationPath)
						}
						if _, err := verify.ParseVerification(verificationPath); err != nil && !os.IsNotExist(err) {
							return err
						}
						frlog.Log("  GIT: checkpoint — sprint %d reviewed-deviate", spr.Number)
						if err := git.GitCheckpoint(ctx, projectPath, ep.Name, spr.Number, spr.Name, "reviewed-deviate"); err != nil {
							return err
						}
					}
				}

				if steering.HasStopRequest(projectPath) {
					verdict := settledSprintResumeVerdict(ep, spr)
					detail := "after sprint steering and review and before the next sprint"
					if verdict == steering.ResumeVerdictAuditIncomplete {
						detail = "after the final sprint settled and before final build finalization"
					}
					if settleErr := settleGracefulExit(
						ctx,
						cmd.OutOrStdout(),
						projectPath,
						originalProjectPath,
						buildStatus,
						ep,
						spr,
						"sprint_boundary",
						detail,
						verdict,
						false,
						observerEnabled,
					); settleErr != nil {
						return settleErr
					}
					return nil
				}

				// Observer wake-up: after sprint
				if observerEnabled && observer.ShouldWakeUp(ep.EffortLevel, observer.WakeAfterSprint) {
					observerModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionObserver)
					frlog.Log("  OBSERVER: wake-up after sprint %d...  model=%s", spr.Number, observerModel)
					obsEngine, engErr := runPlanner.Build(currentEngineName(), mcpOpts...)
					if engErr != nil {
						frlog.Log("WARNING: observer: could not create engine: %v", engErr)
					} else if obs, obsErr := observer.WakeUp(ctx, observer.ObserverOpts{
						ProjectDir:   projectPath,
						Engine:       obsEngine,
						Model:        observerModel,
						EpicName:     ep.Name,
						WakePoint:    observer.WakeAfterSprint,
						SprintNum:    spr.Number,
						TotalSprints: ep.TotalSprints,
						EffortLevel:  ep.EffortLevel,
						Verbose:      frlog.Verbose,
					}); obsErr != nil {
						frlog.Log("  OBSERVER: wake-up failed (non-fatal): %v", obsErr)
					} else if obs != nil && collector != nil {
						checkpoint := consciousness.ObservationCheckpoint{
							CheckpointType:  consciousness.CheckpointTypeObservation,
							WakePoint:       string(observer.WakeAfterSprint),
							SprintNum:       spr.Number,
							ParseStatus:     obs.ParseStatus,
							ParseError:      obs.ParseError,
							ScratchpadDelta: obs.ScratchpadDelta,
							Directives:      obs.Directives,
							RawOutputPath:   obs.RawOutputPath,
						}
						if obs.ParseStatus != consciousness.ParseStatusFailed && strings.TrimSpace(obs.Thoughts) != "" {
							checkpoint.Observation = &consciousness.BuildObservation{
								Timestamp: time.Now().UTC(),
								WakePoint: string(observer.WakeAfterSprint),
								SprintNum: spr.Number,
								Thoughts:  obs.Thoughts,
							}
						}
						persistCheckpoint(ctx, checkpoint)
					}
				}

				continue
			}

			if err := sprint.AppendToEpicProgress(projectPath,
				fmt.Sprintf("## Sprint %d: %s \u2014 %s\n\n", spr.Number, spr.Name, result.Status)); err != nil {
				frlog.Log("warning: append epic progress: %v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Resume:   fry run --resume --sprint %d\n", spr.Number)
			fmt.Fprintf(cmd.OutOrStdout(), "Restart:  fry run --sprint %d\n", spr.Number)
			fmt.Fprintf(cmd.OutOrStdout(), "Continue: fry run --continue\n")
			exitErr = fmt.Errorf("sprint %d failed: %s", spr.Number, result.Status)
			break
		}

		mu.Lock()
		summaryCopy := append([]sprint.SprintResult(nil), results...)
		mu.Unlock()

		// When resuming for audit-only, the sprint loop was skipped so results is empty.
		// Populate summaryCopy from epic-progress.txt so printBuildSummary and the build
		// audit receive meaningful sprint context.
		// Known limitation: Duration is zero for all entries because epic-progress.txt
		// does not record per-sprint timing. printBuildSummary and the build audit prompt
		// will display "0s" for sprint durations on audit-only resumes.
		if auditOnlyResume {
			progressPath := filepath.Join(projectPath, config.EpicProgressFile)
			if data, readErr := os.ReadFile(progressPath); readErr == nil {
				for _, cs := range continuerun.ParseCompletedSprints(string(data)) {
					summaryCopy = append(summaryCopy, sprint.SprintResult{
						Number: cs.Number,
						Name:   cs.Name,
						Status: cs.Status,
					})
				}
			} else {
				frlog.Log("WARNING: audit-only resume: could not read epic-progress: %v", readErr)
			}
		}

		if ep.EffortLevel != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", color.CyanText("Effort level:"), ep.EffortLevel)
		}
		printBuildSummary(cmd.OutOrStdout(), summaryCopy)

		if ep.ReviewBetweenSprints && !runNoReview && !auditOnlyResume {
			if reviewSummary.ReviewsConducted == 0 {
				reviewSummary.AllLowRisk = true
			}
			if err := review.AppendDeviationSummary(projectPath, reviewSummary); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Reviews: %d, deviations: %d\n", reviewSummary.ReviewsConducted, reviewSummary.DeviationsApplied)
		}

		// Run final build audit once the entire epic has completed successfully.
		// This runs BEFORE the build summary so that audit results can be included in the summary.
		// The audit runs when all sprints are complete — either from a full run (sprint 1 to last)
		// or from a resumed/continued run where prior sprints are recorded in epic-progress.txt.
		var buildAuditResult *audit.AuditResult
		var reportingFailure *agent.ReportingFailure
		fullBuildComplete := auditOnlyResume || startSprint == 1 || allSprintsCompleted(projectPath, ep.TotalSprints, startSprint, summaryCopy)
		if exitErr == nil && fullBuildComplete && endSprint == ep.TotalSprints && ep.AuditAfterSprint && !runNoAudit && (ep.EffortLevel != epic.EffortFast || runAlwaysVerify) {
			if steering.HasStopRequest(projectPath) {
				lastSprint := &ep.Sprints[ep.TotalSprints-1]
				if settleErr := settleGracefulExit(
					ctx,
					cmd.OutOrStdout(),
					projectPath,
					originalProjectPath,
					buildStatus,
					ep,
					lastSprint,
					"build_audit",
					"before build audit",
					steering.ResumeVerdictAuditIncomplete,
					false,
					observerEnabled,
				); settleErr != nil {
					return settleErr
				}
				return nil
			}

			deferredContent := readDeferredFailuresArtifact(projectPath)
			if analysis := audit.AnalyzeDeferredFailures(deferredContent); analysis != nil {
				validationChecklist = analysis.Checklist
			}
			auditEngine, err := resolveAuditEngine(runPlanner, currentEngineName(), ep.AuditEngine, mcpOpts...)
			if err != nil {
				frlog.Log("WARNING: could not create engine for build audit: %v", err)
			} else {
				buildStatus.Build.Phase = "build-audit"
				writeCurrentBuildStatus()
				writeBuildPhase(projectPath, "build-audit")
				if originalProjectPath != projectPath {
					writeBuildPhase(originalProjectPath, "build-audit:worktree")
				}
				buildAuditModel := engine.ResolveModel(ep.AuditModel, auditEngine.Name(), string(ep.EffortLevel), engine.SessionBuildAudit)
				frlog.Log("▶ BUILD AUDIT  running holistic audit across all %d sprints...  engine=%s  model=%s", ep.TotalSprints, auditEngine.Name(), buildAuditModel)
				result, auditErr := audit.RunBuildAudit(ctx, audit.BuildAuditOpts{
					ProjectDir:       projectPath,
					Epic:             ep,
					Engine:           auditEngine,
					Results:          summaryCopy,
					Verbose:          frlog.Verbose,
					Model:            buildAuditModel,
					DeferredFailures: deferredContent,
					Mode:             string(mode),
				})
				if auditErr != nil {
					if fo := engine.DetectFailoverCondition(auditEngine.Name(), "", auditErr); fo.Detected {
						frlog.Log("  BUILD AUDIT: %s on primary engine, attempting fallback...", fo.Reason)
						if fbEng, fbName, fbErr := runPlanner.BuildOneShot(mcpOpts...); fbErr == nil {
							fbModel := engine.ResolveModelForSession(fbName, string(ep.EffortLevel), engine.SessionBuildAudit)
							frlog.Log("  BUILD AUDIT: retrying with %s (model=%s)", fbName, fbModel)
							fbResult, fbAuditErr := audit.RunBuildAudit(ctx, audit.BuildAuditOpts{
								ProjectDir:       projectPath,
								Epic:             ep,
								Engine:           fbEng,
								Results:          summaryCopy,
								Verbose:          frlog.Verbose,
								Model:            fbModel,
								DeferredFailures: deferredContent,
								Mode:             string(mode),
							})
							if fbAuditErr == nil {
								auditErr = nil
								result = fbResult
								frlog.Log("  BUILD AUDIT: fallback succeeded")
							} else {
								frlog.Log("WARNING: build audit fallback also failed: %v", fbAuditErr)
							}
						}
					}
				}
				if auditErr != nil {
					frlog.Log("WARNING: build audit failed: %v", auditErr)
					reportingFailure = &agent.ReportingFailure{Stage: "build_audit", Message: auditErr.Error()}
				} else {
					if steering.HasStopRequest(projectPath) {
						lastSprint := &ep.Sprints[ep.TotalSprints-1]
						if settleErr := settleGracefulExit(
							ctx,
							cmd.OutOrStdout(),
							projectPath,
							originalProjectPath,
							buildStatus,
							ep,
							lastSprint,
							"build_audit",
							"after build audit and before finalization",
							steering.ResumeVerdictAuditIncomplete,
							false,
							observerEnabled,
						); settleErr != nil {
							return settleErr
						}
						return nil
					}
					buildAuditResult = result
					if result.Passed {
						frlog.Log("  BUILD AUDIT: PASS (%s)", audit.FormatCounts(result.SeverityCounts))
					} else if result.Blocking {
						frlog.Log("  BUILD AUDIT: FAILED — %s remain", audit.FormatCounts(result.SeverityCounts))
					} else {
						frlog.Log("  BUILD AUDIT: %s remain (advisory)", audit.FormatCounts(result.SeverityCounts))
					}
					if result.Passed || !result.Blocking {
						if sentinelErr := writeBuildAuditSentinel(projectPath); sentinelErr != nil {
							frlog.Log("WARNING: could not write build audit sentinel: %v", sentinelErr)
						}
					}
					frlog.Log("  GIT: checkpoint — build-audit")
					if gitErr := git.GitCheckpoint(ctx, projectPath, ep.Name, ep.TotalSprints, "", "build-audit"); gitErr != nil {
						frlog.Log("WARNING: git checkpoint after build audit failed: %v", gitErr)
					}
				}
				buildStatus.Build.Phase = "sprint"
				writeCurrentBuildStatus()
				writeBuildPhase(projectPath, "sprint")
				if originalProjectPath != projectPath {
					writeBuildPhase(originalProjectPath, "sprint:worktree")
				}
			}

			// Re-run deferred sanity checks to see if the build audit fixed them
			if len(allDeferredFailures) > 0 {
				frlog.Log("  Re-running deferred sanity checks after build audit...")
				for _, entry := range allDeferredFailures {
					var deferredChecks []verify.Check
					for _, f := range entry.Failures {
						deferredChecks = append(deferredChecks, f.Check)
					}
					_, passCount, totalCount := verify.RunChecks(ctx, deferredChecks, entry.SprintNumber, projectPath)
					if passCount == totalCount {
						frlog.Log("  Sprint %d deferred failures: ALL FIXED by build audit", entry.SprintNumber)
					} else {
						frlog.Log("  Sprint %d deferred failures: %d/%d still failing", entry.SprintNumber, totalCount-passCount, totalCount)
					}
				}
			}
		}

		// Run a single-pass build audit for triaged tasks that skipped the main
		// build audit gate (e.g. simple+low, moderate+low where AuditAfterSprint=false).
		if exitErr == nil && buildAuditResult == nil && !runNoAudit && triageDecision != nil {
			deferredContent := readDeferredFailuresArtifact(projectPath)
			if analysis := audit.AnalyzeDeferredFailures(deferredContent); analysis != nil {
				validationChecklist = analysis.Checklist
			}
			auditEngine, auditEngErr := runPlanner.Build(currentEngineName(), mcpOpts...)
			if auditEngErr != nil {
				frlog.Log("WARNING: could not create engine for triage build audit: %v", auditEngErr)
			} else {
				buildAuditModel := engine.ResolveModelForSession(currentEngineName(), string(ep.EffortLevel), engine.SessionBuildAudit)
				frlog.Log("▶ BUILD AUDIT  single-pass audit for triaged task...  engine=%s  model=%s", currentEngineName(), buildAuditModel)
				result, auditErr := audit.RunBuildAudit(ctx, audit.BuildAuditOpts{
					ProjectDir:       projectPath,
					Epic:             ep,
					Engine:           auditEngine,
					Results:          summaryCopy,
					Verbose:          frlog.Verbose,
					Model:            buildAuditModel,
					DeferredFailures: deferredContent,
					Mode:             string(mode),
				})
				if auditErr != nil {
					frlog.Log("WARNING: triage build audit failed: %v", auditErr)
					reportingFailure = &agent.ReportingFailure{Stage: "build_audit", Message: auditErr.Error()}
				} else {
					buildAuditResult = result
					if result.Passed {
						frlog.Log("  BUILD AUDIT: PASS (%s)", audit.FormatCounts(result.SeverityCounts))
					} else {
						frlog.Log("  BUILD AUDIT: %s (advisory)", audit.FormatCounts(result.SeverityCounts))
					}
					if result.Passed || !result.Blocking {
						if sentinelErr := writeBuildAuditSentinel(projectPath); sentinelErr != nil {
							frlog.Log("WARNING: could not write build audit sentinel: %v", sentinelErr)
						}
					}
					if gitErr := git.GitCheckpoint(ctx, projectPath, ep.Name, ep.TotalSprints, "", "build-audit"); gitErr != nil {
						frlog.Log("WARNING: git checkpoint after build audit failed: %v", gitErr)
					}
				}
			}
		}

		// Update build status with build audit result
		if buildAuditResult != nil {
			buildStatus.BuildAudit = &agent.BuildAuditStatus{
				Ran:      true,
				Passed:   buildAuditResult.Passed,
				Blocking: buildAuditResult.Blocking,
				Findings: buildAuditResult.SeverityCounts,
			}
			writeCurrentBuildStatus()
		}

		// Observer: build audit event and wake-up
		if observerEnabled {
			buildAuditData := map[string]string{"ran": strconv.FormatBool(buildAuditResult != nil)}
			if buildAuditResult != nil {
				buildAuditData["passed"] = strconv.FormatBool(buildAuditResult.Passed)
				if buildAuditResult.MaxSeverity != "" {
					buildAuditData["max_severity"] = buildAuditResult.MaxSeverity
				}
			}
			_ = observer.EmitEvent(projectPath, observer.Event{
				Type: observer.EventBuildAuditDone,
				Data: buildAuditData,
			})
			_ = copilot.WriteStateSnapshot(projectPath)

			if observer.ShouldWakeUp(ep.EffortLevel, observer.WakeAfterBuildAudit) {
				observerModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionObserver)
				frlog.Log("  OBSERVER: wake-up after build audit...  model=%s", observerModel)
				obsEngine, engErr := runPlanner.Build(currentEngineName(), mcpOpts...)
				if engErr != nil {
					frlog.Log("WARNING: observer: could not create engine: %v", engErr)
				} else if obs, obsErr := observer.WakeUp(ctx, observer.ObserverOpts{
					ProjectDir:   projectPath,
					Engine:       obsEngine,
					Model:        observerModel,
					EpicName:     ep.Name,
					WakePoint:    observer.WakeAfterBuildAudit,
					TotalSprints: ep.TotalSprints,
					EffortLevel:  ep.EffortLevel,
					Verbose:      frlog.Verbose,
					BuildData:    buildAuditData,
				}); obsErr != nil {
					frlog.Log("  OBSERVER: wake-up failed (non-fatal): %v", obsErr)
				} else if obs != nil && collector != nil {
					checkpoint := consciousness.ObservationCheckpoint{
						CheckpointType:  consciousness.CheckpointTypeObservation,
						WakePoint:       string(observer.WakeAfterBuildAudit),
						SprintNum:       ep.TotalSprints,
						ParseStatus:     obs.ParseStatus,
						ParseError:      obs.ParseError,
						ScratchpadDelta: obs.ScratchpadDelta,
						Directives:      obs.Directives,
						RawOutputPath:   obs.RawOutputPath,
					}
					if obs.ParseStatus != consciousness.ParseStatusFailed && strings.TrimSpace(obs.Thoughts) != "" {
						checkpoint.Observation = &consciousness.BuildObservation{
							Timestamp: time.Now().UTC(),
							WakePoint: string(observer.WakeAfterBuildAudit),
							SprintNum: ep.TotalSprints,
							Thoughts:  obs.Thoughts,
						}
					}
					persistCheckpoint(ctx, checkpoint)
				}
			}
		}

		// Generate build summary document (after build audit so results can be included)
		summaryEngine, err := runPlanner.Build(currentEngineName(), mcpOpts...)
		if err != nil {
			frlog.Log("WARNING: could not create engine for build summary: %v", err)
		} else {
			summaryModel := engine.ResolveModel(ep.AgentModel, currentEngineName(), string(ep.EffortLevel), engine.SessionBuildSummary)
			frlog.Log("▶ BUILD SUMMARY  generating...  engine=%s  model=%s", currentEngineName(), summaryModel)
			summaryErr := summary.GenerateBuildSummary(ctx, summary.SummaryOpts{
				ProjectDir:       projectPath,
				EpicName:         ep.Name,
				Engine:           summaryEngine,
				Results:          summaryCopy,
				EffortLevel:      string(ep.EffortLevel),
				Verbose:          frlog.Verbose,
				Model:            summaryModel,
				BuildAuditResult: buildAuditResult,
			})
			if summaryErr != nil {
				if fo := engine.DetectFailoverCondition(currentEngineName(), "", summaryErr); fo.Detected {
					frlog.Log("  BUILD SUMMARY: %s on primary engine, attempting fallback...", fo.Reason)
					if fbEng, fbName, fbErr := runPlanner.BuildOneShot(mcpOpts...); fbErr == nil {
						fbModel := engine.ResolveModelForSession(fbName, string(ep.EffortLevel), engine.SessionBuildSummary)
						frlog.Log("  BUILD SUMMARY: retrying with %s (model=%s)", fbName, fbModel)
						summaryErr = summary.GenerateBuildSummary(ctx, summary.SummaryOpts{
							ProjectDir:       projectPath,
							EpicName:         ep.Name,
							Engine:           fbEng,
							Results:          summaryCopy,
							EffortLevel:      string(ep.EffortLevel),
							Verbose:          frlog.Verbose,
							Model:            fbModel,
							BuildAuditResult: buildAuditResult,
						})
						if summaryErr == nil {
							frlog.Log("  BUILD SUMMARY: fallback succeeded")
						} else {
							frlog.Log("WARNING: build summary fallback also failed: %v", summaryErr)
						}
					}
				}
			}
			if summaryErr != nil {
				frlog.Log("WARNING: build summary generation failed: %v", summaryErr)
				if reportingFailure == nil {
					reportingFailure = &agent.ReportingFailure{Stage: "summary", Message: summaryErr.Error()}
				} else {
					reportingFailure.Stage += "+summary"
					reportingFailure.Message += "; summary: " + summaryErr.Error()
				}
			} else {
				frlog.Log("  BUILD SUMMARY: complete")
			}
		}

		// Determine build outcome for observer and collector
		buildOutcome := "success"
		if exitErr != nil {
			buildOutcome = "failure"
		}

		// Clean up steering files so stale directives don't affect the next run
		steering.CleanupAll(projectPath)

		// Observer: final wake-up at build end
		if observerEnabled && !wasInterrupted() {
			_ = observer.EmitEvent(projectPath, observer.Event{
				Type: observer.EventBuildEnd,
				Data: map[string]string{"outcome": buildOutcome},
			})
			_ = copilot.WriteStateSnapshot(projectPath)

			if observer.ShouldWakeUp(ep.EffortLevel, observer.WakeBuildEnd) {
				observerModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionObserver)
				frlog.Log("  OBSERVER: final wake-up...  model=%s", observerModel)
				obsEngine, engErr := runPlanner.Build(currentEngineName(), mcpOpts...)
				if engErr != nil {
					frlog.Log("WARNING: observer: could not create engine: %v", engErr)
				} else if obs, obsErr := observer.WakeUp(ctx, observer.ObserverOpts{
					ProjectDir:   projectPath,
					Engine:       obsEngine,
					Model:        observerModel,
					EpicName:     ep.Name,
					WakePoint:    observer.WakeBuildEnd,
					TotalSprints: ep.TotalSprints,
					EffortLevel:  ep.EffortLevel,
					Verbose:      frlog.Verbose,
					BuildData:    map[string]string{"outcome": buildOutcome},
				}); obsErr != nil {
					frlog.Log("  OBSERVER: final wake-up failed (non-fatal): %v", obsErr)
				} else if obs != nil && collector != nil {
					checkpoint := consciousness.ObservationCheckpoint{
						CheckpointType:  consciousness.CheckpointTypeObservation,
						WakePoint:       string(observer.WakeBuildEnd),
						SprintNum:       ep.TotalSprints,
						ParseStatus:     obs.ParseStatus,
						ParseError:      obs.ParseError,
						ScratchpadDelta: obs.ScratchpadDelta,
						Directives:      obs.Directives,
						RawOutputPath:   obs.RawOutputPath,
					}
					if obs.ParseStatus != consciousness.ParseStatusFailed && strings.TrimSpace(obs.Thoughts) != "" {
						checkpoint.Observation = &consciousness.BuildObservation{
							Timestamp: time.Now().UTC(),
							WakePoint: string(observer.WakeBuildEnd),
							SprintNum: ep.TotalSprints,
							Thoughts:  obs.Thoughts,
						}
					}
					persistCheckpoint(ctx, checkpoint)
				}
			}
		}

		// Synthesize checkpoint summaries into the final experience summary.
		if collector != nil && !wasInterrupted() && len(collector.GetRecord().CheckpointSummaries) > 0 {
			consciousnessEngine, cErr := runPlanner.Build(currentEngineName(), mcpOpts...)
			if cErr != nil {
				frlog.Log("WARNING: could not create engine for experience summary: %v", cErr)
			} else {
				cModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionExperienceSummary)
				frlog.Log("  CONSCIOUSNESS: synthesizing experience summary...  model=%s", cModel)

				sprintOutcomes := make([]consciousness.SprintOutcome, len(summaryCopy))
				for i, r := range summaryCopy {
					sprintOutcomes[i] = consciousness.SprintOutcome{
						Number:       r.Number,
						Name:         r.Name,
						Status:       r.Status,
						HealAttempts: r.HealAttempts,
					}
				}

				expSummary, sErr := consciousness.SummarizeExperience(ctx, consciousness.SummarizeOpts{
					ProjectDir:    projectPath,
					Engine:        consciousnessEngine,
					Model:         cModel,
					EffortLevel:   string(ep.EffortLevel),
					Record:        collector.GetRecord(),
					SprintResults: sprintOutcomes,
					BuildOutcome:  buildOutcome,
					Verbose:       frlog.Verbose,
				})
				if sErr != nil {
					frlog.Log("WARNING: experience summary failed (non-fatal): %v", sErr)
				} else {
					collector.SetSummary(expSummary)
					frlog.Log("  CONSCIOUSNESS: experience summary complete (%d bytes)", len(expSummary))
				}
			}
		}

		// Finalize and write the build experience record
		if collector != nil {
			if finalizeErr := collector.Finalize(buildOutcome); finalizeErr != nil {
				frlog.Log("WARNING: could not write experience record: %v", finalizeErr)
			} else {
				frlog.Log("  CONSCIOUSNESS: experience record written to ~/.fry/experiences/")
				flushPendingUploads()
			}
		}

		// Extract codebase-specific memories from this build.
		{
			memEngine, memErr := runPlanner.Build(currentEngineName(), mcpOpts...)
			if memErr != nil {
				frlog.Log("WARNING: could not create engine for memory extraction: %v", memErr)
			} else {
				memModel := engine.ResolveModel("", currentEngineName(), string(ep.EffortLevel), engine.SessionCodebaseMemory)
				frlog.Log("  MEMORY: extracting codebase learnings...  model=%s", memModel)

				// Collect build context for memory extraction.
				scratchpad, _ := os.ReadFile(filepath.Join(projectPath, config.ObserverScratchpadFile))
				events, _ := os.ReadFile(filepath.Join(projectPath, config.ObserverEventsFile))
				auditContent, _ := os.ReadFile(filepath.Join(projectPath, config.SprintAuditFile))

				diffStat := ""
				if ds, dsErr := git.GitDiffForAudit(ctx, projectPath); dsErr == nil && len(ds) < 5000 {
					diffStat = ds
				}

				buildID := fmt.Sprintf("build-%s", time.Now().Format("20060102-150405"))
				var sprintSummaryLines []string
				for _, r := range summaryCopy {
					sprintSummaryLines = append(sprintSummaryLines,
						fmt.Sprintf("Sprint %d (%s): %s [heal=%d]", r.Number, r.Name, r.Status, r.HealAttempts))
				}

				if extractErr := scan.ExtractCodebaseMemories(ctx, scan.MemoryExtractionOpts{
					ProjectDir:    projectPath,
					Engine:        memEngine,
					Model:         memModel,
					EffortLevel:   string(ep.EffortLevel),
					BuildID:       buildID,
					SprintCount:   ep.TotalSprints,
					Scratchpad:    string(scratchpad),
					Events:        string(events),
					SprintSummary: strings.Join(sprintSummaryLines, "\n"),
					GitDiffStat:   diffStat,
					AuditFindings: string(auditContent),
				}); extractErr != nil {
					frlog.Log("WARNING: memory extraction failed (non-fatal): %v", extractErr)
				} else {
					frlog.Log("  MEMORY: codebase learnings extracted")
				}

				// Compact if over threshold.
				if scan.NeedsCompaction(projectPath) {
					frlog.Log("  MEMORY: compacting memories (over %d threshold)...", config.MaxMemoryCount)
					if compactErr := scan.CompactMemories(ctx, projectPath, memEngine, memModel, string(ep.EffortLevel)); compactErr != nil {
						frlog.Log("WARNING: memory compaction failed (non-fatal): %v", compactErr)
					} else {
						frlog.Log("  MEMORY: compaction complete")
					}
				}
			}
		}

		// Incremental codebase.md update (or first-time generation for from-scratch projects).
		{
			codebasePath := filepath.Join(projectPath, config.CodebaseFile)
			_, codebaseExists := os.Stat(codebasePath)

			if codebaseExists == nil {
				// Existing codebase.md — check if incremental update is warranted.
				if diffStat, dsErr := git.GitDiffForAudit(ctx, projectPath); dsErr == nil {
					if scan.ShouldUpdateCodebaseDoc(diffStat) {
						scanEng, sErr := runPlanner.Build(currentEngineName(), mcpOpts...)
						if sErr == nil {
							scanModel := engine.ResolveModelForSession(currentEngineName(), string(ep.EffortLevel), engine.SessionCodebaseScan)
							frlog.Log("  SCAN: updating codebase.md (significant changes detected)")
							if updateErr := scan.UpdateCodebaseDoc(ctx, projectPath, diffStat, scanEng, scanModel); updateErr != nil {
								frlog.Log("WARNING: codebase.md update failed (non-fatal): %v", updateErr)
							}
						}
					}
				}
			} else if os.IsNotExist(codebaseExists) {
				// No codebase.md — generate one for the first time (from-scratch project).
				scanEng, sErr := runPlanner.Build(currentEngineName(), mcpOpts...)
				if sErr == nil {
					snap, snapErr := scan.RunStructuralScan(ctx, projectPath)
					if snapErr == nil {
						scanModel := engine.ResolveModelForSession(currentEngineName(), string(ep.EffortLevel), engine.SessionCodebaseScan)
						frlog.Log("  SCAN: generating initial codebase.md (first build complete)")
						if genErr := scan.RunSemanticScan(ctx, scan.SemanticScanOpts{
							ProjectDir:  projectPath,
							Snapshot:    snap,
							Engine:      scanEng,
							Model:       scanModel,
							EffortLevel: string(ep.EffortLevel),
						}); genErr != nil {
							frlog.Log("WARNING: initial codebase.md generation failed (non-fatal): %v", genErr)
						}
						// Also write file index.
						_ = scan.WriteFileIndex(snap, filepath.Join(projectPath, config.FileIndexFile))
					}
				}
			}
		}

		// Upload queued consciousness events in the background.
		var uploadDone <-chan struct{}
		if collector != nil && telemetryEnabled {
			uploadDone = consciousness.UploadPendingInBackground(config.ConsciousnessAPIURL, config.ConsciousnessWriteKey, projectPath, uploadTimeout)
			frlog.Log("  CONSCIOUSNESS: upload queue flush initiated")
		}

		releaseLock()

		// When auditOnlyResume is true the sprint loop was skipped, so sprintReportResults
		// is empty. Synthesize it from summaryCopy so the JSON build report includes sprint data.
		if auditOnlyResume && len(sprintReportResults) == 0 {
			for _, r := range summaryCopy {
				sprintReportResults = append(sprintReportResults, report.SprintResult{
					SprintNum: r.Number,
					Name:      r.Name,
					Passed:    strings.HasPrefix(r.Status, "PASS"),
				})
			}
		}

		buildReport.ValidationChecklist = validationChecklist
		if err := writeValidationChecklistArtifact(projectPath, validationChecklist); err != nil {
			frlog.Log("WARNING: could not write validation checklist artifact: %v", err)
		}

		// Write JSON build report if --json-report flag is set.
		if runJSONReport {
			buildEnd := time.Now()
			buildReport.EndTime = buildEnd
			buildReport.Duration = buildEnd.Sub(buildStart)
			buildReport.Sprints = sprintReportResults
			reportPath := filepath.Join(projectPath, config.BuildReportFile)
			if writeErr := report.Write(reportPath, buildReport); writeErr != nil {
				frlog.Log("WARNING: could not write JSON build report: %v", writeErr)
			} else {
				frlog.Log("  BUILD REPORT: written to %s", config.BuildReportFile)
			}
		}

		// Write SARIF report if --sarif flag is set and a build audit was run.
		if runSARIF && buildAuditResult != nil {
			sarifData, sarifErr := audit.ConvertToSARIF(buildAuditResult.UnresolvedFindings)
			if sarifErr != nil {
				frlog.Log("WARNING: could not generate SARIF report: %v", sarifErr)
			} else {
				sarifPath := filepath.Join(projectPath, config.BuildAuditSARIFFile)
				if writeErr := os.WriteFile(sarifPath, sarifData, 0o644); writeErr != nil {
					frlog.Log("WARNING: could not write SARIF report: %v", writeErr)
				} else {
					frlog.Log("  SARIF: build-audit.sarif written (%d findings)", len(buildAuditResult.UnresolvedFindings))
				}
			}
		}

		// Print per-sprint token summary to stderr if --show-tokens is set.
		if runShowTokens && len(sprintTokens) > 0 {
			tw := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "Sprint\tInput Tokens\tOutput Tokens\tTotal")
			fmt.Fprintln(tw, "------\t------------\t-------------\t-----")
			var totalIn, totalOut int
			for _, st := range sprintTokens {
				if st.Usage.Total == 0 && strings.EqualFold(currentEngineName(), "ollama") {
					fmt.Fprintf(tw, "%d\t-\t-\t-\n", st.SprintNum)
				} else {
					fmt.Fprintf(tw, "%d\t%d\t%d\t%d\n", st.SprintNum, st.Usage.Input, st.Usage.Output, st.Usage.Total)
				}
				totalIn += st.Usage.Input
				totalOut += st.Usage.Output
			}
			if strings.EqualFold(currentEngineName(), "ollama") && totalIn == 0 && totalOut == 0 {
				fmt.Fprintln(tw, "TOTAL\t-\t-\t-")
			} else {
				fmt.Fprintf(tw, "TOTAL\t%d\t%d\t%d\n", totalIn, totalOut, totalIn+totalOut)
			}
			_ = tw.Flush()
		}

		// Final build status update
		if exitErr != nil {
			buildStatus.Build.Status = "failed"
			buildStatus.Build.Phase = "failed"
			writeBuildPhase(projectPath, "failed")
		} else if reportingFailure != nil {
			buildStatus.Build.Status = "completed_with_reporting_failure"
			buildStatus.Build.Phase = "complete"
			buildStatus.ReportingFailure = reportingFailure
			writeBuildPhase(projectPath, "complete")
		} else {
			buildStatus.Build.Status = "completed"
			buildStatus.Build.Phase = "complete"
			writeBuildPhase(projectPath, "complete")
		}
		writeCurrentBuildStatus()
		if originalProjectPath != projectPath {
			if exitErr != nil {
				writeBuildPhase(originalProjectPath, "failed")
			} else {
				writeBuildPhase(originalProjectPath, "complete")
			}
		}
		_ = copilot.ForceWriteStateSnapshot(projectPath)

		// Wait for upload to complete (bounded by upload timeout)
		if uploadDone != nil {
			<-uploadDone
		}

		// Auto-finalize: archive .fry/, clean up git state, remove steering
		// artifacts so the repo is ready for a new fry run.
		finalize.Finalize(ctx, finalize.FinalizeOpts{
			ProjectPath:         projectPath,
			OriginalProjectPath: originalProjectPath,
			StrategySetup:       strategySetup,
			BuildErr:            exitErr,
		})

		return exitErr
	},
}

func resolveRunGitStrategy(requested git.GitStrategy, triageDecision *triage.TriageDecision, repoExistedBeforeRun, isFreshlyInitialized bool) git.GitStrategy {
	if requested != git.StrategyAuto {
		return requested
	}

	// A repo created for this build — or one that only has the fry-automated
	// initial commit — has no existing history to isolate, so keep the first
	// run on the primary branch instead of branching or using a worktree.
	if !repoExistedBeforeRun || isFreshlyInitialized {
		return git.StrategyCurrent
	}

	if triageDecision != nil {
		if triageDecision.GitStrategyOverride != "" {
			parsed, err := git.ParseGitStrategy(triageDecision.GitStrategyOverride)
			if err == nil {
				return parsed
			}
		}
		return git.ResolveAutoStrategy(string(triageDecision.Complexity))
	}

	// No triage decision (epic already existed). Default to current for backwards compat.
	return git.StrategyCurrent
}

func init() {
	runCmd.Flags().StringVar(&runEngine, "engine", "", "Execution engine")
	runCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Preview actions without executing")
	runCmd.Flags().StringVar(&runUserPrompt, "user-prompt", "", "Additional user prompt")
	runCmd.Flags().StringVar(&runUserPromptFile, "user-prompt-file", "", "Path to file containing user prompt")
	runCmd.Flags().StringVar(&runGitHubIssue, "gh-issue", "", "GitHub issue URL to use as the task definition (requires gh auth)")
	runCmd.Flags().BoolVar(&runReview, "review", false, "Enable sprint review between sprints")
	runCmd.Flags().BoolVar(&runNoReview, "no-review", false, "Disable sprint review")
	runCmd.Flags().StringVar(&runSimulateReview, "simulate-review", "", "Simulate review verdict")
	runCmd.Flags().StringVar(&runPrepareEngine, "prepare-engine", "", "Engine for auto-prepare")
	runCmd.Flags().BoolVar(&runPlanning, "planning", false, "Use planning mode (alias for --mode planning)")
	runCmd.Flags().StringVar(&runMode, "mode", "", "Execution mode: software, planning, writing")
	runCmd.Flags().StringVar(&runEffort, "effort", "", "Effort level: fast, standard, high, max (default: auto)")
	runCmd.Flags().BoolVar(&runNoAudit, "no-audit", false, "Disable sprint and build audits")
	runCmd.Flags().BoolVar(&runResume, "resume", false, "Resume failed sprint: skip iterations, go straight to sanity checks + alignment with boosted attempts")
	runCmd.Flags().IntVar(&runSprint, "sprint", 0, "Start from sprint N (alternative to positional sprint argument)")
	runCmd.Flags().BoolVar(&runContinue, "continue", false, "Use an LLM agent to analyze build state and resume from where it left off")
	runCmd.Flags().BoolVar(&runNoProjectOverview, "no-project-overview", false, "Skip interactive confirmations (triage classification and project overview)")
	runCmd.Flags().BoolVar(&runNoProjectOverview, "no-sanity-check", false, "Deprecated alias for --no-project-overview")
	_ = runCmd.Flags().MarkHidden("no-sanity-check")
	runCmd.Flags().BoolVar(&runFullPrepare, "full-prepare", false, "Skip triage and run full prepare pipeline when no epic exists")
	runCmd.Flags().StringVar(&runGitStrategy, "git-strategy", "", "Git branching strategy: auto, current, branch, worktree (default: auto)")
	runCmd.Flags().StringVar(&runBranchName, "branch-name", "", "Git branch name (auto-generated from epic name if not specified)")
	runCmd.Flags().BoolVar(&runAlwaysVerify, "always-verify", false, "Force sanity checks, alignment, and audit to run regardless of effort level or triage complexity")
	runCmd.Flags().BoolVar(&runSimpleContinue, "simple-continue", false, "Resume from first incomplete sprint without LLM analysis (lightweight alternative to --continue)")
	runCmd.Flags().BoolVar(&runSARIF, "sarif", false, "Write build-audit.sarif in SARIF 2.1.0 format alongside build-audit.md")
	runCmd.Flags().BoolVar(&runJSONReport, "json-report", false, "Write build-report.json with structured sprint results")
	runCmd.Flags().BoolVar(&runShowTokens, "show-tokens", false, "Print per-sprint token usage summary to stderr after the run")
	runCmd.Flags().BoolVar(&runObserver, "observer", false, "Enable the observer metacognitive layer")
	runCmd.Flags().BoolVar(&runNoObserver, "no-observer", false, "Disable the observer metacognitive layer (default)")
	runCmd.Flags().BoolVar(&runTriageOnly, "triage-only", false, "Run triage classification and exit without generating artifacts")
	runCmd.Flags().StringVar(&runModel, "model", "", "Override agent model for sprints (e.g. opus[1m], sonnet, haiku)")
	runCmd.Flags().BoolVar(&runTelemetry, "telemetry", false, "Enable experience upload to consciousness API")
	runCmd.Flags().BoolVar(&runNoTelemetry, "no-telemetry", false, "Disable experience upload")
	runCmd.Flags().StringVar(&runMCPConfig, "mcp-config", "", "Path to MCP server configuration file (Claude engine only)")
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "Auto-accept all interactive confirmation prompts")
	runCmd.Flags().BoolVar(&runConfirmFile, "confirm-file", false, "Use file-based interactive prompts (.fry/confirm-prompt.json) instead of stdin")

	// Copilot flags. --copilot is hybrid: presence enables, optional value
	// selects the engine. The empty default + cmd.Flags().Changed("copilot")
	// pattern mirrors --planning.
	runCmd.Flags().StringVar(&runCopilot, "copilot", "", "Enable build copilot (default engine: claude). Use =<engine> to choose.")
	runCmd.Flags().Lookup("copilot").NoOptDefVal = "claude" // bare --copilot → claude
	runCmd.Flags().StringVar(&runCopilotInterval, "copilot-interval", "10m", "Copilot wake interval (1m–1h)")
	runCmd.Flags().StringVar(&runCopilotFrySource, "copilot-fry-source", "", "Path to fry source tree (default: auto-detected)")
	runCmd.Flags().StringVar(&runCopilotModel, "copilot-model", "", "Override copilot agent model")
	runCmd.Flags().BoolVar(&runCopilotPassive, "copilot-passive", false, "Disable copilot interventions; events + summary only")
	runCmd.Flags().BoolVar(&runCopilotPrintSummary, "copilot-print-summary", false, "Print copilot final summary to stdout when fry exits")
	runCmd.Flags().BoolVar(&runNoCopilot, "no-copilot", false, "Explicitly disable copilot (overrides auto-enable at max effort)")
}

func resolveProjectDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	return filepath.Abs(dir)
}

// copilotShouldBootstrap encapsulates the decision logic for whether
// `fry run` should launch a copilot session for this build:
//
//   - --no-copilot always wins (returns false)
//   - --copilot (or --copilot=<engine>) explicitly enables (returns true)
//   - effort=max auto-enables unless --no-copilot was set
//   - everything else: opt-in
func copilotShouldBootstrap(cmd *cobra.Command, effortLevel epic.EffortLevel) bool {
	if runNoCopilot {
		return false
	}
	if cmd.Flags().Changed("copilot") {
		return true
	}
	if effortLevel == epic.EffortMax {
		frlog.Log("  COPILOT: auto-enabled for max effort (use --no-copilot to disable)")
		return true
	}
	return false
}

// resolveCopilotEngine returns the engine name for the copilot. Defaults
// to claude when --copilot was passed without a value or when auto-enabled.
func resolveCopilotEngine() string {
	if runCopilot == "" {
		return "claude"
	}
	switch strings.ToLower(strings.TrimSpace(runCopilot)) {
	case "claude", "codex":
		return strings.ToLower(strings.TrimSpace(runCopilot))
	case "auto":
		// Auto: inherit build engine when supported, else claude.
		switch runEngine {
		case "claude", "codex":
			return runEngine
		default:
			return "claude"
		}
	default:
		// Unknown value — log a warning, fall back to claude.
		frlog.Log("WARNING: unknown --copilot=%s, falling back to claude", runCopilot)
		return "claude"
	}
}

func resolveUserPrompt(projectDir, provided, promptFile string, persist bool) (string, error) {
	if strings.TrimSpace(provided) != "" && strings.TrimSpace(promptFile) != "" {
		return "", fmt.Errorf("cannot use both --user-prompt and --user-prompt-file")
	}

	if strings.TrimSpace(promptFile) != "" {
		data, err := os.ReadFile(promptFile)
		if err != nil {
			return "", fmt.Errorf("reading user prompt file: %w", err)
		}
		provided = string(data)
	}

	if strings.TrimSpace(provided) != "" {
		if persist {
			path := filepath.Join(projectDir, config.UserPromptFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(provided), 0o644); err != nil {
				return "", err
			}
		}
		return provided, nil
	}

	data, err := os.ReadFile(filepath.Join(projectDir, config.UserPromptFile))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func resolveTopLevelPrompt(ctx context.Context, projectDir, provided, promptFile, issueURL string, persist bool) (string, string, error) {
	if strings.TrimSpace(issueURL) != "" {
		if strings.TrimSpace(provided) != "" || strings.TrimSpace(promptFile) != "" {
			return "", "", fmt.Errorf("cannot use --gh-issue with --user-prompt or --user-prompt-file")
		}
		prompt, _, err := resolveGitHubIssuePrompt(ctx, projectDir, issueURL, persist)
		if err != nil {
			return "", "", err
		}
		return prompt, "--gh-issue " + issueURL, nil
	}

	prompt, err := resolveUserPrompt(projectDir, provided, promptFile, persist)
	if err != nil {
		return "", "", err
	}
	if persist && (strings.TrimSpace(provided) != "" || strings.TrimSpace(promptFile) != "") {
		clearGitHubIssueArtifact(projectDir)
	}
	return prompt, userPromptSource(prompt, provided, promptFile), nil
}

func clearGitHubIssueArtifact(projectDir string) {
	_ = os.Remove(filepath.Join(projectDir, config.GitHubIssueFile))
}

// userPromptSource returns a human-readable description of where the user prompt
// was loaded from, for use in log messages. Returns empty string if no prompt.
func userPromptSource(prompt, flagValue, fileFlagValue string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	if strings.TrimSpace(fileFlagValue) != "" {
		return "--user-prompt-file " + fileFlagValue
	}
	if strings.TrimSpace(flagValue) != "" {
		return "--user-prompt flag"
	}
	return config.UserPromptFile
}

// checkArgsForMissingDashes detects positional arguments that look like flag names
// without the leading "--". This catches common mistakes like
// "fry run user-prompt-file ./file.md" instead of "fry run --user-prompt-file ./file.md".
func checkArgsForMissingDashes(cmd *cobra.Command, args []string) error {
	for _, arg := range args {
		// Skip args that start with dashes (already a flag) or look like file paths.
		if strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".") {
			continue
		}
		// Check if this arg matches a known flag name on this command or its parent.
		if f := cmd.Flags().Lookup(arg); f != nil {
			return fmt.Errorf("%q looks like a flag — did you mean --%s?", arg, arg)
		}
		if cmd.HasParent() {
			if f := cmd.Parent().Flags().Lookup(arg); f != nil {
				return fmt.Errorf("%q looks like a flag — did you mean --%s?", arg, arg)
			}
		}
	}
	return nil
}

func resolveEpicPath(projectDir, requested string) (string, bool, error) {
	if strings.TrimSpace(requested) == "" {
		requested = filepath.Join(config.FryDir, "epic.md")
	}

	candidates := []string{requested}
	if !filepath.IsAbs(requested) {
		candidates = []string{filepath.Join(projectDir, requested), requested}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			absPath, absErr := filepath.Abs(candidate)
			return absPath, true, absErr
		}
	}

	resolved := filepath.Join(projectDir, config.FryDir, filepath.Base(requested))
	return resolved, false, nil
}

func resolveSprintRange(args []string, totalSprints int) (int, int, error) {
	startSprint := 1
	endSprint := totalSprints

	if len(args) >= 1 {
		value, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start sprint %q", args[0])
		}
		startSprint = value
		endSprint = totalSprints
	}
	if len(args) >= 2 {
		value, err := strconv.Atoi(args[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end sprint %q", args[1])
		}
		endSprint = value
	}
	if startSprint < 1 || endSprint < startSprint || endSprint > totalSprints {
		return 0, 0, fmt.Errorf("invalid sprint range %d-%d (total sprints: %d)", startSprint, endSprint, totalSprints)
	}
	return startSprint, endSprint, nil
}

func printMigrationHintIfNeeded(w io.Writer, projectDir, epicArg string) {
	base := filepath.Base(epicArg)
	rootPath := filepath.Join(projectDir, base)
	fryPath := filepath.Join(projectDir, config.FryDir, base)
	if _, err := os.Stat(rootPath); err != nil {
		return
	}
	if _, err := os.Stat(fryPath); err == nil {
		return
	}
	fmt.Fprintln(w, "NOTE: Found fry artifacts in project root (old layout).")
	fmt.Fprintln(w, "  fry now stores all generated artifacts in .fry/.")
	fmt.Fprintln(w, "  To migrate: mv epic.md AGENTS.md verification.md sprint-progress.txt epic-progress.txt .fry/")
}

func telemetryBoolPtr(b bool) *bool { return &b }

func resolvePrepareEngine(prepareFlag, runFlag string) string {
	if strings.TrimSpace(prepareFlag) != "" {
		return prepareFlag
	}
	if strings.TrimSpace(runFlag) != "" {
		return runFlag
	}
	return os.Getenv("FRY_ENGINE")
}

func printDryRunReport(w io.Writer, projectDir, epicPath string, ep *epic.Epic, engineName string, startSprint, endSprint int) error {
	fmt.Fprintf(w, "%s %s\n", color.CyanText("Epic:"), ep.Name)
	fmt.Fprintf(w, "%s %s\n", color.CyanText("Project dir:"), projectDir)
	fmt.Fprintf(w, "%s %s\n", color.CyanText("Epic file:"), epicPath)
	fmt.Fprintf(w, "%s %s\n", color.CyanText("Engine:"), engineName)
	fmt.Fprintf(w, "%s %s\n", color.CyanText("Effort:"), ep.EffortLevel)
	if runContinue {
		fmt.Fprintln(w, "Mode: continue (auto-detected resume point)")
	} else if runResume {
		fmt.Fprintln(w, "Mode: resume (skip iterations, sanity checks + alignment only)")
	}
	fmt.Fprintf(w, "Sprints: %d-%d of %d\n", startSprint, endSprint, ep.TotalSprints)
	fmt.Fprintln(w, "Sanity checks:")
	verificationPath := ep.VerificationFile
	if !filepath.IsAbs(verificationPath) {
		verificationPath = filepath.Join(projectDir, verificationPath)
	}
	if _, err := os.Stat(verificationPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "  (none)")
			return nil
		}
		return err
	}
	checks, err := verify.ParseVerification(verificationPath)
	if err != nil {
		return err
	}
	if len(checks) == 0 {
		fmt.Fprintln(w, "  (none)")
		return nil
	}
	for _, check := range checks {
		if check.Sprint < startSprint || check.Sprint > endSprint {
			continue
		}
		fmt.Fprintf(w, "  Sprint %d: %s\n", check.Sprint, check.Type)
	}
	return nil
}

func initializeSprintResults(ep *epic.Epic, startSprint, endSprint int) []sprint.SprintResult {
	results := make([]sprint.SprintResult, 0, endSprint-startSprint+1)
	for sprintNum := startSprint; sprintNum <= endSprint; sprintNum++ {
		name := ""
		if sprintNum >= 1 && sprintNum <= len(ep.Sprints) {
			name = ep.Sprints[sprintNum-1].Name
		}
		results = append(results, sprint.SprintResult{
			Number: sprintNum,
			Name:   name,
			Status: sprint.StatusSkipped,
		})
	}
	return results
}

func printBuildSummary(w io.Writer, results []sprint.SprintResult) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SPRINT\tNAME\tSTATUS\tDURATION")
	for _, result := range results {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", result.Number, result.Name, colorizeStatus(result.Status), result.Duration.Round(1e9))
	}
	_ = tw.Flush()

	// Print warnings after the table
	for _, result := range results {
		if result.AuditWarning != "" {
			fmt.Fprintf(w, "  %s Sprint %d: %s\n", color.YellowText("⚠"), result.Number, result.AuditWarning)
		}
		if len(result.DeferredFailures) > 0 {
			fmt.Fprintf(w, "  %s Sprint %d: %d deferred sanity check failures\n", color.YellowText("⚠"), result.Number, len(result.DeferredFailures))
		}
	}
}

func colorizeStatus(status string) string {
	if strings.HasPrefix(status, "PASS") {
		return color.GreenText(status)
	}
	if strings.HasPrefix(status, "FAIL") {
		return color.RedText(status)
	}
	if status == sprint.StatusSkipped {
		return color.YellowText(status)
	}
	return status
}

func isPassStatus(status string) bool {
	return strings.HasPrefix(status, "PASS")
}

func resolveAuditEngine(planner *enginePlanner, buildEngineName, auditEngineName string, engineOpts ...engine.EngineOpt) (engine.Engine, error) {
	name := buildEngineName
	if strings.TrimSpace(auditEngineName) != "" {
		name = auditEngineName
	}
	return planner.Build(name, engineOpts...)
}

func resolveReviewEngine(planner *enginePlanner, buildEngineName, reviewEngineName string, engineOpts ...engine.EngineOpt) (engine.Engine, error) {
	name := buildEngineName
	if strings.TrimSpace(reviewEngineName) != "" {
		name = reviewEngineName
	}
	return planner.Build(name, engineOpts...)
}

func startSprintCount(startSprint, endSprint int) int {
	return endSprint - startSprint + 1
}

func writeDeferredFailuresArtifact(projectDir string, entries []deferredEntry) {
	path := filepath.Join(projectDir, config.DeferredFailuresFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		frlog.Log("WARNING: could not create deferred failures dir: %v", err)
		return
	}
	var b strings.Builder
	b.WriteString("# Deferred Sanity Check Failures\n\n")
	for _, entry := range entries {
		_, _ = fmt.Fprintf(&b, "## Sprint %d: %s\n\n", entry.SprintNumber, entry.SprintName)
		b.WriteString(verify.CollectDeferredSummary(entry.Failures))
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		frlog.Log("WARNING: could not write deferred failures artifact: %v", err)
	}
}

func readDeferredFailuresArtifact(projectDir string) string {
	path := filepath.Join(projectDir, config.DeferredFailuresFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func writeAuditMetricsArtifact(projectDir string, sprintNum int, metrics *audit.AuditMetrics) error {
	if metrics == nil {
		return nil
	}
	path := filepath.Join(projectDir, config.BuildLogsDir, fmt.Sprintf("sprint%d_audit_metrics.json", sprintNum))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func writeValidationChecklistArtifact(projectDir string, checklist []audit.ValidationItem) error {
	path := filepath.Join(projectDir, config.ValidationChecklistFile)
	if len(checklist) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := audit.RenderValidationChecklist(checklist)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func totalFindingCount(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func writeBuildAuditSentinel(projectDir string) error {
	finalPath := filepath.Join(projectDir, config.BuildAuditCompleteFile)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("write build audit sentinel: %w", err)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(finalPath), "fry-build-audit-sentinel-*")
	if err != nil {
		return fmt.Errorf("write build audit sentinel: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(time.Now().UTC().Format(time.RFC3339) + "\n"); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write build audit sentinel: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write build audit sentinel: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write build audit sentinel: %w", err)
	}
	return nil
}

func formatAffectedSprints(sprints []int) string {
	if len(sprints) == 0 {
		return "unknown"
	}
	parts := make([]string, len(sprints))
	for i, s := range sprints {
		parts[i] = strconv.Itoa(s)
	}
	return strings.Join(parts, ", ")
}

// allSprintsCompleted checks whether all sprints prior to startSprint are recorded
// as completed in epic-progress.txt. Combined with the current run completing through
// endSprint == totalSprints, this confirms the full codebase was built — enabling the
// build audit to run even on resumed/continued builds.
func allSprintsCompleted(projectDir string, totalSprints, startSprint int, currentResults []sprint.SprintResult) bool {
	if startSprint == 1 {
		return true // full run from the start
	}

	// Check epic-progress.txt for completed sprints before our start point.
	progressPath := filepath.Join(projectDir, config.EpicProgressFile)
	data, err := os.ReadFile(progressPath)
	if err != nil {
		return false
	}

	// Build set of completed sprints from epic-progress.txt (prior runs)
	// and from the current run's results.
	completed := make(map[int]bool, totalSprints)
	for _, cs := range continuerun.ParseCompletedSprints(string(data)) {
		completed[cs.Number] = true
	}
	for _, r := range currentResults {
		if strings.HasPrefix(r.Status, "PASS") {
			completed[r.Number] = true
		}
	}

	// All sprints 1..totalSprints must be accounted for.
	for i := 1; i <= totalSprints; i++ {
		if !completed[i] {
			return false
		}
	}
	return true
}

// writeExitReason persists the reason the build stopped to .fry/build-exit-reason.txt.
// On success (nil error), the file is removed so status doesn't show stale reasons.
func writeExitReason(projectDir string, buildErr error, sprintNum int) {
	path := filepath.Join(projectDir, config.BuildExitReasonFile)
	if buildErr == nil {
		_ = os.Remove(path)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "fry: warning: write exit reason: %v\n", err)
		return
	}
	reason := buildErr.Error()
	if sprintNum > 0 {
		reason = fmt.Sprintf("After sprint %d: %s", sprintNum, reason)
	}
	if err := os.WriteFile(path, []byte(reason), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "fry: warning: write exit reason: %v\n", err)
	}
}

// writeBuildPhase writes the current build phase to .fry/build-phase.txt.
func writeBuildPhase(projectDir, phase string) {
	path := filepath.Join(projectDir, config.BuildPhaseFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(phase+"\n"), 0o644)
}

func clearGracefulStopArtifacts(projectPath, originalProjectPath string) {
	for _, dir := range []string{projectPath, originalProjectPath} {
		if dir == "" {
			continue
		}
		if err := steering.ClearStopRequest(dir); err != nil {
			frlog.Log("WARNING: could not clear stop request artifacts in %s: %v", dir, err)
		}
		if err := steering.ClearResumePoint(dir); err != nil {
			frlog.Log("WARNING: could not clear resume point in %s: %v", dir, err)
		}
	}
}

func resumeNeedsFinalizationOnly(state *continuerun.BuildState) bool {
	if state == nil || state.ResumePoint == nil {
		return false
	}
	reason := strings.ToLower(strings.TrimSpace(state.ResumePoint.Reason))
	return !state.AuditConfigured || state.BuildAuditComplete || strings.Contains(reason, "finalization")
}

func settledSprintResumeVerdict(ep *epic.Epic, spr *epic.Sprint) string {
	if ep == nil || spr == nil {
		return steering.ResumeVerdictContinueNext
	}
	if spr.Number == ep.TotalSprints {
		return steering.ResumeVerdictAuditIncomplete
	}
	return steering.ResumeVerdictContinueNext
}

func settleGracefulExit(
	ctx context.Context,
	w io.Writer,
	projectPath string,
	originalProjectPath string,
	buildStatus *agent.BuildStatus,
	ep *epic.Epic,
	spr *epic.Sprint,
	phase string,
	detail string,
	verdict string,
	checkpoint bool,
	observerEnabled bool,
) error {
	stopReq, err := steering.ReadStopRequest(projectPath)
	if err != nil {
		return fmt.Errorf("settle graceful exit: read stop request: %w", err)
	}

	if checkpoint {
		frlog.Log("  GIT: checkpoint — paused")
		if err := git.GitCheckpoint(ctx, projectPath, ep.Name, spr.Number, spr.Name, "paused"); err != nil {
			return err
		}
	}

	for _, dir := range []string{projectPath, originalProjectPath} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		if err := steering.ClearStopRequest(dir); err != nil {
			return fmt.Errorf("settle graceful exit: clear stop request in %s: %w", dir, err)
		}
	}

	recommended := recommendedResumeCommand(verdict, spr.Number)
	point := steering.ResumePoint{
		Phase:              phase,
		Verdict:            verdict,
		Reason:             detail,
		Sprint:             spr.Number,
		SprintName:         spr.Name,
		RecommendedCommand: recommended,
	}
	if stopReq != nil {
		point.Source = stopReq.Source
		point.RequestedAt = stopReq.RequestedAt
	}
	if err := steering.WriteResumePoint(projectPath, point); err != nil {
		return fmt.Errorf("settle graceful exit: write resume point: %w", err)
	}

	if buildStatus != nil {
		buildStatus.Build.Status = "paused"
		if strings.TrimSpace(phase) != "" {
			buildStatus.Build.Phase = phase
		}
		writeBuildStatus(projectPath, buildStatus)
	}

	phaseLabel := phase
	if phaseLabel == "" {
		phaseLabel = "sprint"
	}
	writeBuildPhase(projectPath, phaseLabel)
	if originalProjectPath != "" && originalProjectPath != projectPath {
		writeBuildPhase(originalProjectPath, phaseLabel+":worktree")
	}

	if observerEnabled {
		evt := observer.Event{
			Type: observer.EventBuildPaused,
			Data: map[string]string{
				"phase": phaseLabel,
			},
		}
		if spr != nil {
			evt.Sprint = spr.Number
		}
		if detail != "" {
			evt.Data["detail"] = detail
		}
		_ = observer.EmitEvent(projectPath, evt)
	}

	if spr != nil {
		frlog.Log("  BUILD EXITED at sprint %d (%s). Use --continue to resume.", spr.Number, phaseLabel)
	} else {
		frlog.Log("  BUILD EXITED at phase %s. Use --continue to resume.", phaseLabel)
	}

	printGracefulExitHints(w, verdict, spr)
	return nil
}

func recommendedResumeCommand(verdict string, sprintNum int) string {
	switch verdict {
	case steering.ResumeVerdictResume:
		return fmt.Sprintf("fry run --resume --sprint %d", sprintNum)
	case steering.ResumeVerdictAuditIncomplete:
		return "fry run --continue"
	case steering.ResumeVerdictContinueNext:
		return "fry run --continue"
	default:
		return "fry run --continue"
	}
}

func printGracefulExitHints(w io.Writer, verdict string, spr *epic.Sprint) {
	switch verdict {
	case steering.ResumeVerdictResume:
		if spr != nil {
			fmt.Fprintf(w, "Resume:   fry run --resume --sprint %d\n", spr.Number)
		}
		fmt.Fprintln(w, "Continue: fry run --continue")
	case steering.ResumeVerdictAuditIncomplete:
		fmt.Fprintln(w, "Continue: fry run --continue")
	default:
		fmt.Fprintln(w, "Continue: fry run --continue")
	}
}

// gitBranchFromHead reads the current branch name from .git/HEAD without a subprocess.
func gitBranchFromHead(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(string(data))
	if strings.HasPrefix(ref, "ref: refs/heads/") {
		return strings.TrimPrefix(ref, "ref: refs/heads/")
	}
	return ""
}

// writeBuildStatus writes the build status file, logging a warning on failure.
func writeBuildStatus(projectDir string, status *agent.BuildStatus) {
	if err := agent.WriteBuildStatus(projectDir, status); err != nil {
		frlog.Log("WARNING: could not write build status: %v", err)
	}
}

func writeRollingResults(projectDir string, status *agent.BuildStatus) {
	if status == nil {
		return
	}
	rolling := make([]agent.RollingSprintResult, len(status.Sprints))
	for i, s := range status.Sprints {
		rolling[i] = agent.RollingSprintResult{
			Number:      s.Number,
			Name:        s.Name,
			Status:      s.Status,
			DurationSec: s.DurationSec,
		}
	}
	if err := agent.WriteRollingResults(projectDir, rolling); err != nil {
		frlog.Log("WARNING: could not write rolling results: %v", err)
	}
}

func markBuildFailed(projectPath, originalProjectPath string, buildStatus *agent.BuildStatus) {
	writeBuildPhase(projectPath, "failed")
	if originalProjectPath != "" && originalProjectPath != projectPath {
		writeBuildPhase(originalProjectPath, "failed")
	}
	if buildStatus == nil {
		return
	}
	buildStatus.Build.Status = "failed"
	buildStatus.Build.Phase = "failed"
	writeBuildStatus(projectPath, buildStatus)
}

func initialSprintStatuses(projectDir string, ep *epic.Epic, startSprint, endSprint int, restoreHistory bool) []agent.SprintStatus {
	if !restoreHistory || startSprint <= 1 {
		return make([]agent.SprintStatus, 0, endSprint)
	}

	completedBySprint := make(map[int]continuerun.CompletedSprint)
	if data, err := os.ReadFile(filepath.Join(projectDir, config.EpicProgressFile)); err == nil {
		for _, cs := range continuerun.ParseCompletedSprints(string(data)) {
			completedBySprint[cs.Number] = cs
		}
	}

	statuses := make([]agent.SprintStatus, 0, endSprint)
	for i := 1; i < startSprint && i <= len(ep.Sprints); i++ {
		sp := agent.SprintStatus{
			Number: i,
			Name:   ep.Sprints[i-1].Name,
			Status: "PASS",
		}
		if cs, ok := completedBySprint[i]; ok {
			if cs.Name != "" {
				sp.Name = cs.Name
			}
			if cs.Status != "" {
				sp.Status = cs.Status
			}
		}
		statuses = append(statuses, sp)
	}
	return statuses
}

// updateBuildStatusSprint updates the build status with a completed sprint's results.
func updateBuildStatusSprint(status *agent.BuildStatus, sprintNum int, result *sprint.SprintResult) {
	idx := findSprintStatusIndex(status, sprintNum)
	if idx < 0 {
		return
	}
	now := time.Now()
	status.Sprints[idx].Status = result.Status
	status.Sprints[idx].FinishedAt = &now
	status.Sprints[idx].DurationSec = result.Duration.Seconds()

	if result.HealAttempts > 0 {
		outcome := "exhausted"
		if strings.Contains(result.Status, "deferred") {
			outcome = "within_threshold"
		} else if strings.HasPrefix(result.Status, "PASS") {
			outcome = "healed"
		}
		status.Sprints[idx].Alignment = &agent.AlignmentStatus{
			Attempts: result.HealAttempts,
			Outcome:  outcome,
		}
	}

	if result.VerificationTotalCount > 0 {
		checks := &agent.SanityCheckStatus{
			Passed: result.VerificationPassCount,
			Total:  result.VerificationTotalCount,
		}
		for _, cr := range result.VerificationResults {
			target := cr.Check.Path
			if target == "" {
				target = cr.Check.Command
			}
			entry := agent.CheckResultEntry{
				Type:   cr.Check.Type.String(),
				Target: target,
				Passed: cr.Passed,
			}
			if !cr.Passed && cr.Output != "" {
				entry.Output = textutil.TruncateUTF8(cr.Output, 512)
			}
			checks.Results = append(checks.Results, entry)
		}
		status.Sprints[idx].SanityChecks = checks
	}

	if len(result.DeferredFailures) > 0 {
		status.Sprints[idx].DeferredFailures = len(result.DeferredFailures)
	}
	if result.AuditWarning != "" {
		status.Sprints[idx].Warnings = append(status.Sprints[idx].Warnings, result.AuditWarning)
	}
}

// updateBuildStatusAudit updates a sprint's audit information in the build status.
func updateBuildStatusAudit(status *agent.BuildStatus, sprintNum int, auditResult *audit.AuditResult) {
	if auditResult == nil {
		return
	}
	idx := findSprintStatusIndex(status, sprintNum)
	if idx < 0 {
		return
	}
	outcome := "pass"
	if auditResult.Blocked {
		outcome = "blocked"
	} else if auditResult.Blocking {
		outcome = "failed"
	} else if !auditResult.Passed {
		outcome = "advisory"
	}
	status.Sprints[idx].Audit = &agent.AuditStatus{
		Cycles:        auditResult.Iterations,
		Findings:      auditResult.SeverityCounts,
		Outcome:       outcome,
		Blocked:       auditResult.Blocked,
		BlockerCounts: auditResult.BlockerCounts,
		Blockers:      buildStatusAuditBlockers(auditResult.Blockers),
		Complexity:    string(auditResult.Complexity),
		Metrics:       buildStatusAuditMetricsSnapshot(auditResult.Metrics),
	}
	// Update sprint status if audit failed
	if auditResult.Blocked {
		status.Sprints[idx].Status = fmt.Sprintf("BLOCKED (audit: %s)", summarizeAuditBlockerCategories(auditResult.BlockerCounts))
	} else if auditResult.Blocking {
		status.Sprints[idx].Status = fmt.Sprintf("FAIL (audit: %s)", auditResult.MaxSeverity)
	}
}

// updateBuildStatusAuditProgress updates a sprint's in-flight audit state in the build status.
func updateBuildStatusAuditProgress(status *agent.BuildStatus, sprintNum int, progress audit.AuditProgress) {
	if status == nil {
		return
	}
	idx := findSprintStatusIndex(status, sprintNum)
	if idx < 0 {
		return
	}
	current := status.Sprints[idx].Audit
	findings := progress.Findings
	if findings == nil && current != nil {
		findings = current.Findings
	}
	blockers := progress.Blockers
	if blockers == nil && current != nil {
		blockers = current.BlockerCounts
	}
	status.Sprints[idx].Audit = &agent.AuditStatus{
		Cycles:         progress.Cycle,
		Findings:       findings,
		Outcome:        "running",
		BlockerCounts:  blockers,
		Active:         true,
		Stage:          progress.Stage,
		CurrentCycle:   progress.Cycle,
		MaxCycles:      progress.MaxCycles,
		CurrentFix:     progress.Fix,
		MaxFixes:       progress.MaxFixes,
		TargetIssues:   progress.TargetIssues,
		IssueHeadlines: append([]string(nil), progress.Headlines...),
		Complexity:     string(progress.Complexity),
		Metrics: &agent.AuditMetricsSnapshot{
			TotalCalls:       progress.Metrics.TotalCalls,
			DurationMs:       progress.Metrics.DurationMs,
			NoOpFixCalls:     progress.Metrics.NoOpFixCalls,
			SessionRefreshes: progress.Metrics.SessionRefreshes,
			NoOpRate:         progress.Metrics.NoOpRate,
			VerifyCalls:      progress.Metrics.VerifyCalls,
			VerifyResolutions: progress.Metrics.VerifyResolutions,
			VerifyYield:      progress.Metrics.VerifyYield,
		},
	}
}

func buildStatusAuditBlockers(findings []audit.Finding) []agent.AuditBlocker {
	if len(findings) == 0 {
		return nil
	}
	blockers := make([]agent.AuditBlocker, 0, len(findings))
	for _, finding := range findings {
		details := strings.TrimSpace(finding.BlockerDetails)
		if details == "" {
			details = strings.TrimSpace(finding.Description)
		}
		blockers = append(blockers, agent.AuditBlocker{
			Category: finding.Category,
			Location: finding.Location,
			Details:  textutil.TruncateUTF8(details, 256),
			Severity: finding.Severity,
		})
	}
	return blockers
}

func summarizeAuditBlockerCategories(counts map[string]int) string {
	if len(counts) == 0 {
		return "blockers"
	}
	order := []string{
		audit.FindingCategoryEnvironmentBlocker,
		audit.FindingCategoryHarnessBlocker,
		audit.FindingCategoryExternalDependencyBlocker,
	}
	var parts []string
	for _, category := range order {
		if counts[category] > 0 {
			parts = append(parts, category)
		}
	}
	if len(parts) == 0 {
		for category := range counts {
			parts = append(parts, category)
		}
	}
	return strings.Join(parts, ", ")
}

func auditSummarizeBlockers(result *audit.AuditResult) string {
	if result == nil {
		return "blocked by unresolved audit prerequisites"
	}
	summary := strings.TrimSpace(auditSummarizeBlockerFindings(result.Blockers))
	if summary == "" {
		return "blocked by unresolved audit prerequisites"
	}
	return summary
}

func auditSummarizeBlockerFindings(findings []audit.Finding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		detail := strings.TrimSpace(finding.BlockerDetails)
		if detail == "" {
			detail = strings.TrimSpace(finding.Description)
		}
		if detail == "" {
			continue
		}
		parts = append(parts, finding.Category+": "+detail)
	}
	return strings.Join(parts, "; ")
}

// updateBuildStatusReview updates a sprint's review verdict in the build status.
func updateBuildStatusReview(status *agent.BuildStatus, sprintNum int, verdict string) {
	idx := findSprintStatusIndex(status, sprintNum)
	if idx < 0 {
		return
	}
	status.Sprints[idx].Review = &agent.ReviewStatus{Verdict: verdict}
}

// findSprintStatusIndex returns the index of the sprint in the status, or -1.
func findSprintStatusIndex(status *agent.BuildStatus, sprintNum int) int {
	for i := range status.Sprints {
		if status.Sprints[i].Number == sprintNum {
			return i
		}
	}
	return -1
}

func buildStatusAuditMetricsSnapshot(metrics *audit.AuditMetrics) *agent.AuditMetricsSnapshot {
	if metrics == nil {
		return nil
	}
	snapshot := metrics.Snapshot()
	return &agent.AuditMetricsSnapshot{
		TotalCalls:       snapshot.TotalCalls,
		DurationMs:       snapshot.DurationMs,
		NoOpFixCalls:     snapshot.NoOpFixCalls,
		SessionRefreshes: snapshot.SessionRefreshes,
		NoOpRate:         snapshot.NoOpRate,
		VerifyCalls:      snapshot.VerifyCalls,
		VerifyResolutions: snapshot.VerifyResolutions,
		VerifyYield:      snapshot.VerifyYield,
	}
}

// resolveMode reconciles the --mode flag with the legacy --planning bool flag.
// Returns an error if both --mode and --planning are set to conflicting values.
func resolveMode(modeFlag string, planningFlag bool) (prepare.Mode, error) {
	if strings.TrimSpace(modeFlag) != "" && planningFlag {
		return "", fmt.Errorf("cannot use both --mode and --planning; use --mode planning instead")
	}
	if planningFlag {
		return prepare.ModePlanning, nil
	}
	return prepare.ParseMode(modeFlag)
}

// runTriageGate classifies task complexity and takes the appropriate execution path.
// For SIMPLE tasks, it builds the epic programmatically. For MODERATE, it builds
// a programmatic epic with auto-generated sanity checks. For COMPLEX, it falls
// through to full prepare.
func runTriageGate(ctx context.Context, projectPath, epicPath, prepareEngineName, userPrompt, promptSource string, effortLevel epic.EffortLevel, mode prepare.Mode, stdin io.Reader, stdout io.Writer, skipConfirm bool, autoAccept bool, triageOnly bool, confirmFile bool, planner *enginePlanner) (*triage.TriageDecision, error) {
	// Read available inputs.
	planPath := filepath.Join(projectPath, config.PlanFile)
	executivePath := filepath.Join(projectPath, config.ExecutiveFile)

	planContent, _ := readOptionalFile(planPath)
	execContent, _ := readOptionalFile(executivePath)

	// Validate that we have at least some input.
	if strings.TrimSpace(planContent) == "" && strings.TrimSpace(execContent) == "" && strings.TrimSpace(userPrompt) == "" {
		return nil, fmt.Errorf("prepare requires %s, %s, or --user-prompt", config.PlanFile, config.ExecutiveFile)
	}

	// Resolve engine for triage.
	engName, err := engine.ResolveEngine(prepareEngineName, "", "", config.DefaultPrepareEngine)
	if err != nil {
		return nil, fmt.Errorf("triage: resolve engine: %w", err)
	}
	planner.SetDefault(engName)
	var triageMCPOpts []engine.EngineOpt
	if runMCPConfig != "" {
		mcpPath := runMCPConfig
		if abs, err := filepath.Abs(runMCPConfig); err == nil {
			mcpPath = abs
		}
		triageMCPOpts = append(triageMCPOpts, engine.WithMCPConfig(mcpPath))
	}
	eng, err := planner.Build(engName, triageMCPOpts...)
	if err != nil {
		return nil, fmt.Errorf("triage: create engine: %w", err)
	}
	triageModel := engine.ResolveModelForSession(engName, string(effortLevel), engine.SessionTriage)

	// Load codebase context for triage if available.
	triageCodebaseContent := ""
	if data, readErr := os.ReadFile(filepath.Join(projectPath, config.CodebaseFile)); readErr == nil {
		triageCodebaseContent = string(data)
	}

	decision := triage.Classify(ctx, triage.TriageOpts{
		ProjectDir:      projectPath,
		UserPrompt:      userPrompt,
		PlanContent:     planContent,
		ExecContent:     execContent,
		CodebaseContent: triageCodebaseContent,
		Engine:          eng,
		Model:           triageModel,
		EffortLevel:     string(effortLevel),
		Mode:            mode,
		Verbose:         frlog.Verbose,
	})

	// Interactive confirmation (unless skipped).
	if !skipConfirm {
		result, confirmErr := triage.ConfirmDecision(triage.ConfirmOpts{
			Decision:    decision,
			Stdin:       stdin,
			Stdout:      stdout,
			AutoAccept:  autoAccept,
			ConfirmFile: confirmFile,
			ProjectDir:  projectPath,
			Ctx:         ctx,
		})
		if confirmErr != nil {
			return nil, confirmErr
		}
		decision.Complexity = result.Complexity
		if result.EffortLevel != "" {
			decision.EffortLevel = result.EffortLevel
		}
		if result.GitStrategy != "" {
			decision.GitStrategyOverride = result.GitStrategy
		}
	}

	if triageOnly {
		return decision, nil
	}

	// Resolve effort: CLI flag > triage suggestion > default per difficulty.
	resolvedEffort := effortLevel
	if resolvedEffort == "" {
		resolvedEffort = decision.EffortLevel
	}

	// Non-software modes (planning, writing) always need the full prepare pipeline
	// because programmatic epic builders only produce software-mode epics.
	if mode != prepare.ModeSoftware && mode != "" {
		decision.Complexity = triage.ComplexityComplex
		frlog.Log("  TRIAGE: non-software mode (%s) — routing to full prepare pipeline", mode)
	}

	activeEngineName := planner.Current()

	switch decision.Complexity {
	case triage.ComplexitySimple:
		// Cap max → high for simple tasks.
		if resolvedEffort == epic.EffortMax {
			frlog.Log("  TRIAGE: --effort max capped to high for simple task")
			resolvedEffort = epic.EffortHigh
		}
		if resolvedEffort == "" {
			resolvedEffort = epic.EffortFast
		}

		ep, buildErr := triage.BuildSimpleEpic(triage.SimpleEpicOpts{
			ProjectDir:  projectPath,
			UserPrompt:  userPrompt,
			PlanContent: planContent,
			ExecContent: execContent,
			EngineName:  activeEngineName,
			EffortLevel: resolvedEffort,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("triage simple path: %w", buildErr)
		}
		if err := triage.WriteEpicFile(epicPath, ep); err != nil {
			return nil, fmt.Errorf("triage simple path: %w", err)
		}
		frlog.Log("  TRIAGE: built 1-sprint epic programmatically (no LLM prepare)  effort=%s", resolvedEffort)

	case triage.ComplexityModerate:
		// Cap max → high for moderate tasks.
		if resolvedEffort == epic.EffortMax {
			frlog.Log("  TRIAGE: --effort max capped to high for moderate task")
			resolvedEffort = epic.EffortHigh
		}
		if resolvedEffort == "" {
			resolvedEffort = epic.EffortStandard
		}

		ep, buildErr := triage.BuildModerateEpic(triage.ModerateEpicOpts{
			ProjectDir:  projectPath,
			UserPrompt:  userPrompt,
			PlanContent: planContent,
			ExecContent: execContent,
			EngineName:  activeEngineName,
			EffortLevel: resolvedEffort,
			SprintCount: decision.SprintCount,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("triage moderate path: %w", buildErr)
		}
		if err := triage.WriteEpicFile(epicPath, ep); err != nil {
			return nil, fmt.Errorf("triage moderate path: %w", err)
		}

		// Generate heuristic sanity checks.
		verifyPath := filepath.Join(projectPath, config.DefaultVerificationFile)
		checks := triage.GenerateVerificationChecks(projectPath, ep.TotalSprints)
		if err := triage.WriteVerificationFile(verifyPath, checks); err != nil {
			frlog.Log("WARNING: triage moderate path: could not write sanity checks file: %v", err)
		}

		frlog.Log("  TRIAGE: built %d-sprint moderate epic programmatically (no LLM prepare)  effort=%s", ep.TotalSprints, resolvedEffort)

	case triage.ComplexityComplex:
		// Complex tasks default to high effort for thorough audit cycles.
		// Only auto-elevate when user didn't explicitly set --effort.
		complexEffort := resolvedEffort
		if effortLevel == "" && (complexEffort == "" || complexEffort == epic.EffortFast || complexEffort == epic.EffortStandard) {
			complexEffort = epic.EffortHigh
			frlog.Log("  TRIAGE: complex task auto-elevated to effort=high")
		}

		frlog.Log("  TRIAGE: task classified as complex — running full prepare pipeline")
		writeBuildPhase(projectPath, "prepare")
		triagePrepareFactory := func(name string) (engine.Engine, error) {
			if runMCPConfig == "" {
				return planner.Build(name)
			}
			mcpPath := runMCPConfig
			if abs, err := filepath.Abs(runMCPConfig); err == nil {
				mcpPath = abs
			}
			return planner.Build(name, engine.WithMCPConfig(mcpPath))
		}
		if err := prepare.RunPrepare(ctx, prepare.PrepareOpts{
			ProjectDir:          projectPath,
			EpicFilename:        filepath.Base(epicPath),
			Engine:              planner.Current(),
			UserPrompt:          userPrompt,
			UserPromptSource:    promptSource,
			SkipProjectOverview: runNoProjectOverview || runDryRun,
			AutoAccept:          runYes,
			ConfirmFile:         confirmFile,
			Mode:                mode,
			EffortLevel:         complexEffort,
			EnableReview:        runReview,
			Stdin:               stdin,
			Stdout:              stdout,
			EngineFactory:       triagePrepareFactory,
		}); err != nil {
			return nil, err
		}
	}

	return decision, nil
}

// truncateString returns s truncated to maxBytes bytes, appending a notice if truncated.
func truncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "... [truncated]"
}

func readOptionalFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}
