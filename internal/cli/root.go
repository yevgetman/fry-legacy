package cli

import (
	"github.com/spf13/cobra"

	"github.com/yevgetman/fry/internal/color"
	frlog "github.com/yevgetman/fry/internal/log"
)

var (
	projectDir       string
	noColor          bool
	fallbackEngine   string
	noEngineFailover bool

	rootCmd = &cobra.Command{
		Use:   "fry",
		Short: "Automated AI build orchestration engine",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if noColor {
				color.SetEnabled(false)
			}
			if color.Enabled() {
				frlog.SetColorize(color.ColorizeLogLine)
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCmd.RunE(cmd, args)
		},
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&projectDir, "project-dir", ".", "Project directory")
	rootCmd.PersistentFlags().BoolVarP(&frlog.Verbose, "verbose", "v", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
	rootCmd.PersistentFlags().StringVar(&fallbackEngine, "fallback-engine", "", "Fallback engine for sticky cross-engine failover (default: Claude<->Codex)")
	rootCmd.PersistentFlags().BoolVar(&noEngineFailover, "no-engine-failover", false, "Disable cross-engine failover and stay on the selected engine")

	// These flags are intentionally registered on both rootCmd and runCmd.
	// rootCmd delegates to runCmd when invoked without a subcommand (e.g.,
	// "fry --engine claude"), so rootCmd needs its own flag definitions.
	// Both bind to the same variables so they stay in sync.
	rootCmd.Flags().StringVar(&runEngine, "engine", "", "Execution engine")
	rootCmd.Flags().BoolVar(&runDryRun, "dry-run", false, "Preview actions without executing")
	rootCmd.Flags().StringVar(&runUserPrompt, "user-prompt", "", "Additional user prompt")
	rootCmd.Flags().StringVar(&runUserPromptFile, "user-prompt-file", "", "Path to file containing user prompt")
	rootCmd.Flags().StringVar(&runGitHubIssue, "gh-issue", "", "GitHub issue URL to use as the task definition (requires gh auth)")
	rootCmd.Flags().BoolVar(&runReview, "review", false, "Enable sprint review between sprints")
	rootCmd.Flags().BoolVar(&runNoReview, "no-review", false, "Disable sprint review")
	rootCmd.Flags().StringVar(&runSimulateReview, "simulate-review", "", "Simulate review verdict")
	rootCmd.Flags().StringVar(&runPrepareEngine, "prepare-engine", "", "Engine for auto-prepare")
	rootCmd.Flags().BoolVar(&runPlanning, "planning", false, "Use planning mode (alias for --mode planning)")
	rootCmd.Flags().StringVar(&runMode, "mode", "", "Execution mode: software, planning, writing")
	rootCmd.Flags().StringVar(&runEffort, "effort", "", "Effort level: fast, standard, high, max (default: auto)")
	rootCmd.Flags().BoolVar(&runNoAudit, "no-audit", false, "Disable sprint and build audits")
	rootCmd.Flags().BoolVar(&runResume, "resume", false, "Resume failed sprint: skip iterations, go straight to sanity checks + alignment with boosted attempts")
	rootCmd.Flags().IntVar(&runSprint, "sprint", 0, "Start from sprint N (alternative to positional sprint argument)")
	rootCmd.Flags().BoolVar(&runContinue, "continue", false, "Use an LLM agent to analyze build state and resume from where it left off")
	rootCmd.Flags().BoolVar(&runNoProjectOverview, "no-project-overview", false, "Skip interactive confirmations (triage classification and project overview)")
	rootCmd.Flags().BoolVar(&runNoProjectOverview, "no-sanity-check", false, "Deprecated alias for --no-project-overview")
	_ = rootCmd.Flags().MarkHidden("no-sanity-check")
	rootCmd.Flags().BoolVar(&runFullPrepare, "full-prepare", false, "Skip triage and run full prepare pipeline when no epic exists")
	rootCmd.Flags().StringVar(&runGitStrategy, "git-strategy", "", "Git branching strategy: auto, current, branch, worktree (default: auto)")
	rootCmd.Flags().StringVar(&runBranchName, "branch-name", "", "Git branch name (auto-generated from epic name if not specified)")
	rootCmd.Flags().BoolVar(&runAlwaysVerify, "always-verify", false, "Force sanity checks, alignment, and audit to run regardless of effort level or triage complexity")
	rootCmd.Flags().BoolVar(&runSimpleContinue, "simple-continue", false, "Resume from first incomplete sprint without LLM analysis (lightweight alternative to --continue)")
	rootCmd.Flags().BoolVar(&runTriageOnly, "triage-only", false, "Run triage classification and exit without generating artifacts")
	rootCmd.Flags().StringVar(&runModel, "model", "", "Override agent model for sprints (e.g. opus[1m], sonnet, haiku)")
	rootCmd.Flags().BoolVar(&runTelemetry, "telemetry", false, "Enable experience upload to consciousness API")
	rootCmd.Flags().BoolVar(&runNoTelemetry, "no-telemetry", false, "Disable experience upload")
	rootCmd.Flags().StringVar(&runMCPConfig, "mcp-config", "", "Path to MCP server configuration file (Claude engine only)")
	rootCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "Auto-accept all interactive confirmation prompts")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(prepareCmd)
	rootCmd.AddCommand(replanCmd)
	rootCmd.AddCommand(cleanCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(identityCmd)
	rootCmd.AddCommand(reflectCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(teamCmd)
	rootCmd.AddCommand(eventsCmd)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(monitorCmd)
	rootCmd.AddCommand(destroyCmd)
	rootCmd.AddCommand(copilotCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
