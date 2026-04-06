package config

// Terminology mapping:
// - "Verification" (package verify) -> user-facing: "sanity checks"
// - "Healing" (package heal) -> user-facing: "alignment"
// - File paths (.fry/verification.md) and epic directives (@verification,
//   @max_heal_attempts) retain their original names for backward compatibility.

const (
	FryDir                    = ".fry"
	FryConfigDir              = ".fry-config"
	ProjectConfigFile         = ".fry-config/config.json"
	PlansDir                  = "plans"
	BuildLogsDir              = ".fry/build-logs"
	AuditSessionsDir          = ".fry/sessions"
	DefaultEngine             = "claude"
	DefaultOllamaModel        = "llama3"
	DefaultPrepareEngine      = "claude"
	DefaultPlanningEngine     = "claude"
	DefaultWritingEngine      = "claude"
	DefaultMaxHealAttempts    = 3
	DefaultMaxFailPercent     = 20
	DefaultDockerReadyTimeout = 30
	DefaultMaxDeviationScope  = 3
	MaxDeviationScopeCap      = 10
	DefaultVerificationFile   = ".fry/verification.md" // DefaultVerificationFile is the path to the sanity checks definition file (historically named "verification.md").
	PromptFile                = ".fry/prompt.md"
	SprintProgressFile        = ".fry/sprint-progress.txt"
	EpicProgressFile          = ".fry/epic-progress.txt"
	ReviewPromptFile          = ".fry/review-prompt.md"
	DeviationLogFile          = ".fry/deviation-log.md"
	LockFile                  = ".fry/.fry.lock"
	UserPromptFile            = ".fry/user-prompt.txt"
	GitHubIssueFile           = ".fry/github-issue.md"
	PlanFile                  = "plans/plan.md"
	ExecutiveFile             = "plans/executive.md"
	PlanningOutputDir         = "output"
	WritingOutputDir          = "output"
	MediaDir                  = "media"
	AssetsDir                 = "assets"
	AgentsFile                = ".fry/AGENTS.md"
	Version                   = "0.1.0"
	AgentInvocationPrompt     = "Read and execute ALL instructions in .fry/prompt.md. Before starting, read .fry/sprint-progress.txt for context from previous iterations in this sprint, and .fry/epic-progress.txt for summaries of prior sprints. Also read plans/plan.md for strategic context on how this sprint fits the overall plan. If a media/ directory exists, it contains assets (images, PDFs, etc.) that may be referenced in the plan — use or copy them as instructed. After completing your work, append your progress to .fry/sprint-progress.txt."
	HealInvocationPrompt      = "Read and execute ALL instructions in .fry/prompt.md. This is an ALIGNMENT pass — fix the sanity-check failures described in the prompt."
	DefaultEffortLevel        = "" // auto-detect
	ResumeHealMultiplier      = 2
	ResumeMinHealAttempts     = 6

	// Effort-level alignment constants
	HealAttemptsHigh       = 10 // alignment attempts at high effort
	HealStuckThresholdHigh = 2  // alignment stuck threshold at high effort
	HealStuckThresholdMax  = 3
	HealMinAttemptsMax     = 10 // min attempts before mid-loop threshold exit in max effort
	HealSafetyCapMax       = 50 // alignment safety cap at max effort
	MaxFailPercentMax      = 10 // stricter threshold for max effort

	// Audit constants
	SprintAuditFile             = ".fry/sprint-audit.txt"
	SprintReviewLogPattern      = "sprint%d_review_%s.log"
	AuditPromptFile             = ".fry/audit-prompt.md"
	DefaultMaxAuditIterations   = 3
	MaxAuditDiffBytes           = 100_000
	AuditInvocationPrompt       = "Read and execute ALL instructions in .fry/audit-prompt.md. This is a sprint audit session. Follow the role, scope, and output instructions in the prompt file. Do NOT modify source or content files."
	AuditVerifyInvocationPrompt = "Read and execute ALL instructions in .fry/audit-prompt.md. This is an audit verify session. Verify only the listed issues, write the results to .fry/sprint-audit.txt, and do NOT modify source or content files."
	AuditFixInvocationPrompt    = "Read and execute ALL instructions in .fry/audit-prompt.md. This is an audit fix session. You have full codebase context from the audit phase. Fix only the listed issues and preserve unrelated behavior."

	// Two-level audit loop constants
	DefaultMaxOuterAuditCycles           = 3   // outer audit cycles (medium/default)
	DefaultMaxInnerFixIter               = 3   // fix attempts per audit report (medium/default)
	MaxOuterCyclesHighCap                = 12  // outer audit cycles at high effort
	MaxOuterCyclesMaxCap                 = 100 // outer audit cycles at max effort (safety valve; stale detection governs actual exit)
	MaxInnerFixIterHigh                  = 7   // inner fix cap at high effort
	MaxInnerFixIterMax                   = 10  // inner fix cap at max effort
	AuditSessionMaxCalls                 = 3   // max same-role audit continuity calls before refresh
	AuditSessionMaxPromptBytes           = 24_000
	AuditSessionMaxTokens                = 12_000
	AuditSessionMaxCarry                 = 8 // unresolved findings carried into one same-role audit session before refresh
	FixSessionMaxCalls                   = 4
	FixSessionMaxPromptBytes             = 48_000
	FixSessionMaxTokens                  = 20_000
	FixSessionMaxCarry                   = 10 // unresolved findings carried into one same-role fix session before refresh
	MaxFixFileInlineBytes = 8_000 // max bytes of target file content inlined per file in fix prompt

	DeferredFailuresFile = ".fry/deferred-failures.md"

	// Summary constants
	SummaryFile       = "build-summary.md"
	SummaryPromptFile = ".fry/summary-prompt.md"

	// Rolling state for resumable reporting
	RollingResultsFile = ".fry/rolling-results.json"

	// Build report constants
	BuildReportFile         = ".fry/build-report.json"
	BuildStatusFile         = ".fry/build-status.json"
	ValidationChecklistFile = ".fry/validation-checklist.md"

	// Build audit constants
	BuildAuditSARIFFile        = "build-audit.sarif"
	BuildAuditFile             = "build-audit.md"
	BuildAuditPromptFile       = ".fry/build-audit-prompt.md"
	BuildAuditInvocationPrompt = "Read and execute ALL instructions in .fry/build-audit-prompt.md. You are performing a final holistic audit of the entire codebase. Audit, classify, report, remediate, and re-audit as instructed in the prompt."
	BuildAuditCompleteFile     = ".fry/build-audit-complete"

	// Run snapshot constants
	RunsDir   = ".fry/runs"
	RunPrefix = "run-" // run directory names: run-YYYYMMDD-HHMMSS

	// Archive constants
	ArchiveDir    = ".fry-archive"
	ArchivePrefix = ".fry--build--"

	// Build phase and mode persistence
	BuildPhaseFile      = ".fry/build-phase.txt"
	BuildModeFile       = ".fry/build-mode.txt"
	BuildExitReasonFile = ".fry/build-exit-reason.txt"

	// Continue constants
	ContinuePromptFile       = ".fry/continue-prompt.md"
	ContinueDecisionFile     = ".fry/continue-decision.txt"
	ContinueReportFile       = ".fry/continue-report.md"
	ContinueInvocationPrompt = "Read and execute ALL instructions in .fry/continue-prompt.md. You are a build analyst. Review the build state report and output your decision to .fry/continue-decision.txt. Do NOT modify any source code."

	// Triage constants
	TriagePromptFile       = ".fry/triage-prompt.md"
	TriageDecisionFile     = ".fry/triage-decision.txt"
	TriageInvocationPrompt = "Read and execute ALL instructions in .fry/triage-prompt.md. You are a task complexity classifier. Write your classification to .fry/triage-decision.txt. Do NOT modify any source code."

	// Git strategy constants
	DefaultGitStrategy = "auto"
	GitBranchPrefix    = "fry/"
	GitWorktreeDir     = ".fry-worktrees"
	GitStrategyFile    = ".fry/git-strategy.txt"

	// Observer constants
	ObserverDir                = ".fry/observer"
	ObserverEventsFile         = ".fry/observer/events.jsonl"
	ObserverScratchpadFile     = ".fry/observer/scratchpad.md"
	ObserverPromptFile         = ".fry/observer/wake-prompt.md"
	MaxObserverEvents          = 50
	MaxObserverIdentityBytes   = 10_000
	MaxObserverScratchpadBytes = 20_000
	ObserverInvocationPrompt   = "Read and execute ALL instructions in .fry/observer/wake-prompt.md. You are the Observer — a metacognitive layer watching a build. Read your identity, scratchpad, and events, then write your observations."

	// Identity constants (compiled into binary via go:embed)
	IdentityCoreFile        = "identity/core.md"
	IdentityDispositionFile = "identity/disposition.md"
	IdentityDomainsDir      = "identity/domains"
	IdentityJSONFile        = "identity/identity.json"

	// Experience collection constants
	ExperiencesDir                    = ".fry/experiences"
	ConsciousnessDir                  = ".fry/consciousness"
	ConsciousnessSessionFile          = ".fry/consciousness/session.json"
	ConsciousnessCheckpointsFile      = ".fry/consciousness/checkpoints.jsonl"
	ConsciousnessCheckpointsDir       = ".fry/consciousness/checkpoints"
	ConsciousnessScratchpadHistory    = ".fry/consciousness/scratchpad-history.jsonl"
	ConsciousnessDistilledDir         = ".fry/consciousness/distilled"
	ConsciousnessUploadQueueDir       = ".fry/consciousness/upload-queue"
	ConsciousnessPromptFile           = ".fry/consciousness-prompt.md"
	ConsciousnessCheckpointPromptFile = ".fry/consciousness/checkpoint-prompt.md"

	// Telemetry / experience upload constants
	SettingsFile         = ".fry/settings.json"
	PendingUploadsDir    = ".fry/experiences/pending"
	ConsciousnessAPIURL  = "https://fry-consciousness-api.yevgetman.workers.dev"
	UploadTimeoutSeconds = 10
	TelemetryEnvVar      = "FRY_TELEMETRY"
	// ConsciousnessWriteKey is a public write-only key for the consciousness API.
	// It only permits POSTing anonymized experience summaries — no read access.
	// This is intentionally compiled into the binary (same pattern as Sentry DSNs).
	ConsciousnessWriteKey = "c23060cb24e9133926314894db50089b03791731cae04d7f8ba96dc01c5330d0"

	// File-based interactive prompt constants
	ConfirmPromptFile   = ".fry/confirm-prompt.json"
	ConfirmResponseFile = ".fry/confirm-response.json"
	ConfirmPollInterval = 2   // seconds between polls for response file
	ConfirmTimeoutSec   = 300 // 5 minutes

	// Build steering constants (Layer 1: file-based IPC)
	AgentDirectiveFile = ".fry/agent-directive.md"
	AgentHoldFile      = ".fry/agent-hold-after-sprint"
	AgentPauseFile     = ".fry/agent-pause"
	DecisionNeededFile = ".fry/decision-needed.md"
	ExitRequestFile    = ".fry/exit-request.json"
	ResumePointFile    = ".fry/resume-point.json"

	// Codebase awareness constants — these live in .fry-config/ so they
	// survive fry clean / archive (which only removes .fry/).
	CodebaseFile         = ".fry-config/codebase.md"
	FileIndexFile        = ".fry-config/file-index.txt"
	CodebaseMemoriesDir  = ".fry-config/codebase-memories"
	MaxMemoryCount       = 50
	CompactedMemoryCount = 20
	MaxMemoryPromptBytes = 10240 // 10KB cap for memory injection into prompt

	// Rate-limit retry constants
	RateLimitMaxRetries   = 5
	RateLimitBaseDelaySec = 10   // seconds; converted to time.Duration by caller
	RateLimitMaxDelaySec  = 120  // seconds
	RateLimitJitter       = 0.25 // fraction of delay randomized [0.0, 1.0]

	// Monitor constants
	MonitorDefaultIntervalSec  = 2  // default polling interval in seconds
	MonitorDefaultLogTailLines = 20 // lines to tail from active build log
	MonitorIdleSlowdownTicks   = 10 // unchanged ticks before slowing to idle interval
	MonitorSlowIntervalSec     = 5  // idle polling interval in seconds
)
