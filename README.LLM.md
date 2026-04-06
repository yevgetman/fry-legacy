# README.LLM.md — Fry Codebase Reference for AI Agents

> **Purpose:** Compact, comprehensive codebase map for AI agents. Eliminates the need for deep directory traversal. Covers architecture, file layout, key types, execution flow, and configuration.

## What Is Fry

Fry is a Go CLI tool that orchestrates AI agents (OpenAI Codex, Claude Code, or Ollama) to autonomously build software or generate documents. It decomposes a human-authored plan into sequential "sprints," executes each sprint through an iterative AI agent loop, runs sanity checks with machine-executable checks, auto-aligns on failure, audits quality, and git-checkpoints every sprint.

**Version:** 0.1.0
**Language:** Go 1.22
**Module:** `github.com/yevgetman/fry`
**Binary:** `bin/fry` (single static executable)
**Dependencies:** cobra (CLI), testify (testing only)

---

## Directory Layout

```
fry/
├── cmd/fry/main.go              # Entry point — calls cli.Execute()
├── internal/
│   ├── cli/                     # Cobra commands: root, run, config, init, prepare, replan, clean, destroy, exit, monitor, reflect, audit, agent, status, identity, version
│   │   ├── root.go              # Persistent flags (--project-dir, --verbose/-v, --engine, etc.)
│   │   ├── engine_factory.go    # Sticky engine planner + resilient/failover engine construction
│   │   ├── run.go               # Main orchestration: sprint loop, audit, review, continue
│   │   ├── config.go            # Repo-local Fry settings CLI (`fry config get|set`)
│   │   ├── init.go              # Scaffold project structure; auto-detect and scan existing codebases
│   │   ├── prepare.go           # Generate .fry/ artifacts from plans
│   │   ├── replan.go            # Mid-build replanning
│   │   ├── clean.go             # Archive .fry/ and build outputs to .fry-archive/
│   │   ├── destroy.go           # Remove all fry artifacts completely (inverse of init)
│   │   ├── version.go           # Version subcommand
│   │   ├── status.go            # Show current build state (no LLM call)
│   │   ├── team.go              # Standalone tmux-backed team runtime (start, status, scale, pause, resume, shutdown, attach, assign)
│   │   ├── agent.go             # Agent foundation commands (prompt) + events command
│   │   ├── monitor.go           # Real-time build monitoring (event stream, dashboard, log tail)
│   │   ├── identity.go          # Print compiled-in identity
│   │   ├── reflect.go           # Trigger identity reflection
│   │   └── audit.go             # Standalone build-level audit
│   ├── color/
│   │   ├── color.go             # ANSI color utilities, TTY detection, NO_COLOR support
│   │   └── logcolor.go          # Pattern-matched log line colorizer
│   ├── config/config.go         # All constants: paths, defaults, invocation prompts
│   ├── confirm/                 # Interactive confirmation via file-based IPC
│   ├── settings/
│   │   ├── settings.go          # Repo-local Fry settings (.fry-config/config.json): Load, Save, GetEngine, SetEngine
│   │   └── settings_test.go     # Tests for repo-local settings persistence and validation
│   ├── engine/
│   │   ├── engine.go            # Engine interface + ResolveEngine + NewEngine
│   │   ├── models.go            # Tier-based model selection, validation, session types
│   │   ├── codex.go             # Codex CLI wrapper
│   │   ├── claude.go            # Claude Code CLI wrapper
│   │   ├── ollama.go            # Ollama engine — shells out to `ollama run <model>`, reads prompt via stdin
│   │   ├── resilient.go         # ResilientEngine decorator — retry with exponential backoff on rate limits
│   │   ├── failover.go          # FailoverEngine decorator — sticky cross-engine promotion after transient failures
│   │   ├── failure.go           # Failover-worthy transient failure detection
│   │   └── ratelimit.go         # Rate-limit detection (regex patterns for 429, overloaded, etc.)
│   ├── epic/
│   │   ├── types.go             # Epic, Sprint, EffortLevel types
│   │   ├── parser.go            # State-machine .md parser for epic files
│   │   └── validator.go         # Epic structural validation
│   ├── githubissue/
│   │   ├── githubissue.go       # GitHub issue URL parsing, gh auth validation, fetch, prompt/context rendering
│   │   └── githubissue_test.go  # Tests for URL parsing, gh validation, persistence
│   ├── sprint/
│   │   ├── runner.go            # Sprint execution loop (iterations, no-op detection)
│   │   ├── prompt.go            # Layered prompt assembly (10 layers, 0.5 through 5)
│   │   ├── progress.go          # Iteration memory management
│   │   └── compactor.go         # Sprint progress → epic-progress summarization
│   ├── steering/                # Graceful exits, stop requests, hold/pause sentinels, and resume points
│   ├── verify/
│   │   ├── types.go             # CheckType: FILE, FILE_CONTAINS, CMD, CMD_OUTPUT, TEST
│   │   ├── parser.go            # verification.md parser
│   │   ├── runner.go            # Check execution with timeout
│   │   ├── collector.go         # Failure report aggregation
│   │   └── selfcheck.go         # Harness self-validation: ValidateHarness checks file targets for absolute paths, traversal, missing parents
│   ├── heal/heal.go             # Alignment loop on sanity check failure (package name `heal` is backward-compatible)
│   ├── agent/                   # Build state assembly, build status persistence, and runtime events for `fry status`
│   │   ├── buildstatus.go       # BuildStatus, RunMeta, RunSummary types; WriteBuildStatus (atomic + per-run snapshot); ScanRuns
│   │   ├── state.go             # ReadBuildState: assembles live BuildState from .fry/ artifacts (events, lock, exit reason)
│   │   ├── events.go            # Build event helpers
│   │   ├── prompt.go            # Agent prompt helpers
│   │   ├── artifacts.go         # Build artifact path helpers
│   │   └── types.go             # BuildState (runtime status for `fry status --json`)
│   ├── agentrun/agentrun.go     # Shared dual-log agent execution helper used by sprint and heal packages
│   ├── audit/
│   │   ├── audit.go             # Per-sprint two-level audit: outer audit cycles + inner fix loops
│   │   ├── build_audit.go       # Final holistic codebase audit
│   │   ├── complexity.go        # Sprint complexity classification for adaptive audit budgets
│   │   ├── deferred.go          # Deferred failure interaction analysis + validation checklist rendering
│   │   ├── findingstate.go      # Finding artifact fingerprints + repeated-unchanged/reopening classification
│   │   ├── fixhistory.go        # Per-finding fix-attempt history + behavior-unchanged escalation signals for audit fix prompts
│   │   ├── metrics.go           # Per-call audit metrics and summaries
│   │   ├── recovery.go          # Structured stdout/log recovery for audit outputs
│   │   ├── session.go           # Same-role audit session continuity budgets, refresh logic, and session file management
│   │   └── session_summary.go   # Compact carry-forward summaries used when audit/fix sessions refresh
│   ├── triage/
│   │   ├── types.go             # Complexity, TriageDecision types
│   │   ├── triage.go            # Classify (single LLM call), ParseClassification, prompt builder
│   │   ├── confirm.go           # ConfirmDecision (interactive triage confirmation/adjustment)
│   │   └── builder.go           # BuildSimpleEpic, BuildModerateEpic, WriteEpicFile, GenerateVerificationChecks, WriteVerificationFile
│   ├── review/
│   │   ├── reviewer.go          # Sprint review (CONTINUE vs DEVIATE verdict)
│   │   ├── replanner.go         # Dynamic epic modification
│   │   ├── deviation.go         # Deviation log entry management and build summary
│   │   ├── deviation_prompt.go  # Filtered deviation-context loading for prompts
│   │   └── types.go             # ReviewVerdict, DeviationSpec
│   ├── prepare/
│   │   ├── prepare.go           # Steps 0-3 artifact generation
│   │   ├── mode.go              # Mode type (software, planning, writing) + ParseMode
│   │   ├── overview.go          # Interactive project overview summary
│   │   ├── software.go          # Software-mode prompt builders
│   │   ├── planning.go          # Planning-mode prompt builders
│   │   └── writing.go           # Writing-mode prompt builders
│   ├── git/
│   │   ├── git.go               # Git init, checkpoints (commit format: "EpicName — SprintName: Sprint N label [automated]"), diff capture
│   │   ├── types.go             # GitStrategy type, StrategySetup, ParseGitStrategy
│   │   ├── strategy.go          # SetupStrategy, ResolveAutoStrategy, GenerateBranchName, worktree/branch helpers
│   │   └── scan.go              # ScanWorktreeBuilds (scan .fry-worktrees/ for builds)
│   ├── docker/docker.go         # Docker Compose lifecycle, health checks
│   ├── preflight/
│   │   ├── preflight.go         # Pre-build tool/command validation
│   │   └── sprint_infer.go      # Sprint-scoped preflight: infer env vars, Docker refs, external tools from prompt text
│   ├── archive/
│   │   ├── archive.go           # Build archiving (.fry/ → .fry-archive/)
│   │   └── scan.go              # BuildSummary type, ScanArchives, ScanBuildDir (lightweight build scanning)
│   ├── scan/
│   │   ├── types.go             # StructuralSnapshot, FileEntry, GitHistory, Language, Dependency types
│   │   ├── detect.go            # IsExistingProject: heuristic detection (git history, markers, file count)
│   │   ├── structural.go        # RunStructuralScan: file tree, languages, frameworks, deps, git history
│   │   ├── semantic.go          # RunSemanticScan: LLM-powered codebase analysis → .fry-config/codebase.md
│   │   ├── memories.go          # ExtractCodebaseMemories: post-build learning extraction + dedup
│   │   └── compact.go           # CompactMemories: reduce memories from 50+ to ~20 via LLM
│   ├── lock/lock.go             # File-based build concurrency lock + IsLocked check
│   ├── log/log.go               # Verbose logging, agent banners
│   ├── media/media.go           # Binary asset scanning (images, PDFs, fonts)
│   ├── assets/assets.go         # Text asset scanning + prompt injection
│   ├── continuerun/
│   │   ├── types.go             # BuildState, ContinueDecision, verdict types
│   │   ├── collector.go         # Programmatic build state collection from .fry/ artifacts
│   │   ├── report.go            # BuildState → human-readable markdown report
│   │   └── analyzer.go          # LLM analysis agent for resume decisions
│   ├── summary/summary.go       # AI-generated build summary
│   ├── consciousness/
│   │   ├── identity.go          # Identity loading (JSON-first, .md fallback): LoadCoreIdentity, LoadDisposition, LoadFullIdentity
│   │   ├── identity_json.go     # JSON identity types + loader + renderer: IdentityJSON, LoadIdentityJSON, RenderIdentityForPrompt
│   │   ├── reflect.go           # Remote reflection trigger: TriggerReflection (POST to /reflect)
│   │   ├── collector.go         # Session manager: durable checkpoints, run segments, final BuildRecord persistence
│   │   ├── session.go           # Session/checkpoint/distillation types and status enums
│   │   ├── summarize.go         # Checkpoint distillation + final experience synthesis from checkpoint summaries
│   │   ├── upload.go            # Checkpoint-aware upload queue + legacy final-summary retry
│   │   ├── local_status.go      # Local consciousness health reader/formatter for `fry status --consciousness`
│   │   ├── settings.go          # User settings (~/.fry/settings.json): LoadSettings, TelemetryEnabled
│   │   └── instance.go          # Anonymized machine identifier: InstanceID
│   ├── observer/
│   │   ├── observer.go          # Observer lifecycle: InitNewSession, ResumeSession, WakeUp, ShouldWakeUp
│   │   ├── event.go             # Event types, EmitEvent, ReadEvents, ReadRecentEvents
│   │   ├── identity.go          # ReadIdentity (delegates to consciousness.LoadCoreIdentity)
│   │   └── prompt.go            # Wake-up prompt builder, strict response parser, directive extraction
│   ├── monitor/
│   │   ├── snapshot.go          # Snapshot and EnrichedEvent types
│   │   ├── source.go            # Source interface + core polling implementations (event, phase, status, lock, progress, log, exit)
│   │   ├── logevents.go         # Verbose synthetic events derived from build-log filenames
│   │   ├── enrichment.go        # Pure event enrichment: elapsed times, sprint fractions, phase transitions
│   │   ├── stream.go            # Monitor orchestrator: New, Run (continuous), Snapshot (one-shot)
│   │   └── render.go            # Rendering: stream, dashboard (including live sprint-audit progress), log tail, waiting/ended messages
│   ├── team/
│   │   ├── runtime.go           # Team lifecycle: Start, Resume, Shutdown, Scale
│   │   ├── types.go             # Config, Task, WorkerIdentity, WorkerRecord, StartOptions, IsolationMode
│   │   ├── state.go             # Team state persistence: SaveConfig, LoadConfig, SaveTask, LoadTasks
│   │   ├── workers.go           # Worker management: SaveIdentity, SaveWorkerRecord, ListWorkers
│   │   ├── tasks.go             # Task state: SaveTask, LoadTasks, ClaimTask, CompleteTask
│   │   ├── liveness.go          # Worker liveness checks and dead-worker reconciliation
│   │   ├── status.go            # Team status rendering for fry status and fry team status
│   │   ├── paths.go             # Team directory path helpers
│   │   ├── integration.go       # Output integration: worktree merge, integrated-output assembly
│   │   └── tmux.go              # tmux session management: create, attach, spawn worker panes
│   ├── metrics/tokens.go        # Token usage parsing for Claude and Codex engines
│   ├── report/report.go         # BuildReport types and JSON serialisation (--json-report)
│   ├── shellhook/shellhook.go   # Pre-sprint/iteration shell commands
│   ├── severity/severity.go     # Shared severity ranking: Rank(sev string) int — used by audit and continuerun
│   └── textutil/textutil.go     # Shell quoting, UTF-8 truncation, JSON extraction, artifact resolution
├── templates/
│   ├── embed.go                 # //go:embed *.md identity/*.md for compile-time inclusion
│   ├── AGENTS.md                # Placeholder (generated via fry prepare)
│   ├── GENERATE_EPIC.md         # LLM prompt template for epic generation
│   ├── epic-example.md          # Fully-commented epic file example
│   ├── verification-example.md  # Sanity check examples
│   └── identity/                # Compiled-in identity layers (read-only during builds)
│       ├── core.md              # Fundamental self-knowledge (~500 tokens, always loaded)
│       └── disposition.md       # Behavioral tendencies (~500 tokens, always loaded)
├── docs/                        # 31 user-facing documentation files (see below)
├── plans/                       # User-authored inputs
│   ├── plan.md                  # Build strategy (what to build)
│   └── executive.md             # Project context (why to build it)
├── output/                      # Planning/writing mode deliverables (--mode planning|writing)
├── Makefile                     # build, test, lint, clean, install
├── go.mod / go.sum
├── .fry-worktrees/              # Git worktrees for worktree strategy (gitignored)
├── .env.example                 # FRY_ENGINE=codex|claude
└── .gitignore
```

### Generated Artifacts (`.fry/` at runtime)

| File | Purpose |
|------|---------|
| `epic.md` | Sprint definitions with prompts, parsed by `epic/parser.go` |
| `AGENTS.md` | Project-specific agent rules |
| `verification.md` | Machine-executable acceptance checks |
| `prompt.md` | Assembled prompt for current sprint iteration |
| `sprint-progress.txt` | Append-only iteration log within current sprint |
| `epic-progress.txt` | Compacted summaries of completed sprints |
| `user-prompt.txt` | Persisted user directive |
| `github-issue.md` | Persisted fetched GitHub issue context when `--gh-issue` is used |
| `build-mode.txt` | Persisted build mode (software/planning/writing) for `--continue` auto-detection |
| `deviation-log.md` | Deviations detected during sprint reviews |
| `deferred-failures.md` | Sanity check failures below threshold, deferred to build audit |
| `sprint-audit.txt` | Current sprint's audit findings (agent-written or recovered from structured stdout) |
| `audit-prompt.md` | Assembled audit, fix, or verify prompt |
| `sessions/` | Transient same-role audit session IDs for Claude/Codex continuity; refreshed automatically when continuity budgets are exceeded |
| `validation-checklist.md` | Build-audit checklist synthesized from deferred failure analysis |
| `review-prompt.md` | Assembled review prompt |
| `summary-prompt.md` | Assembled summary prompt |
| `build-logs/` | Timestamped per-iteration/alignment/audit/continue logs |
| `continue-prompt.md` | Assembled prompt for --continue analysis agent |
| `continue-decision.txt` | LLM agent's resume decision (verdict, sprint, reason) |
| `continue-report.md` | Programmatic build state report (input to analysis) |
| `exit-request.json` | Structured graceful-exit request written by `fry exit` |
| `resume-point.json` | Settled resume checkpoint (phase, sprint, verdict, recommended command) |
| `triage-prompt.md` | Classifier prompt for triage gate |
| `triage-decision.txt` | Triage classifier output (complexity, effort, sprints, reason) |
| `git-strategy.txt` | Persisted git strategy for `--continue`/`--resume` reattachment |
| `observer/events.jsonl` | Observer event stream (reset only for a new logical session) |
| `observer/scratchpad.md` | Observer working memory (preserved on resume) |
| `observer/wake-prompt.md` | Observer wake-up prompt (transient, deleted after use) |
| `consciousness/session.json` | Durable consciousness session state |
| `consciousness/checkpoints.jsonl` | Append-only checkpoint log |
| `consciousness/checkpoints/` | Per-checkpoint durable records |
| `consciousness/scratchpad-history.jsonl` | Scratchpad delta history |
| `consciousness/distilled/` | Distilled checkpoint summaries |
| `consciousness/upload-queue/` | Pending checkpoint/lifecycle uploads |
| `build-phase.txt` | Current build phase (triage, prepare, sprint, audit, build-audit, complete, failed) for `fry status` |
| `build-status.json` | Machine-readable build status snapshot (latest-run pointer); updated atomically after every state change. Contains `run` field with `run_id`, `run_type`, and `parent_run_id` for lineage tracking |
| `runs/<run-id>/build-status.json` | Immutable per-run status snapshot; one per build/continue/resume invocation, queryable via `fry status --run` |
| `rolling-results.json` | Compact per-sprint outcomes (number, name, status, duration); updated after every sprint for resumable final-stage reporting |
| `build-report.json` | Machine-readable BuildReport JSON (written at build end with `--json-report`) |
| `confirm-prompt.json` | File-based interactive prompt for agent LLMs (transient, `--confirm-file`) |
| `confirm-response.json` | Agent response to interactive prompt (transient, `--confirm-file`) |
| `.fry.lock` | Concurrency lock |

---

## Core Types

### Epic (`internal/epic/types.go`)

```go
type Epic struct {
    Name, Engine             string
    EffortLevel              EffortLevel    // fast|standard|high|max
    DockerFromSprint         int
    DockerReadyCmd           string
    DockerReadyTimeout       int
    RequiredTools            []string
    PreflightCmds            []string
    PreSprintCmd             string
    PreIterationCmd          string
    AgentModel, AgentFlags   string
    VerificationFile         string
    MaxHealAttempts          int
    MaxHealAttemptsSet       bool           // true when @max_heal_attempts was explicitly set
    MaxFailPercent           int            // 0-100; default 20 (10 for max effort)
    MaxFailPercentSet        bool           // true when @max_fail_percent was explicitly set
    CompactWithAgent         bool
    ReviewBetweenSprints     bool
    ReviewEngine, ReviewModel string
    MaxDeviationScope        int
    AuditAfterSprint         bool
    MaxAuditIterations       int
    MaxAuditIterationsSet    bool
    AuditEngine, AuditModel  string
    Sprints                  []Sprint
    TotalSprints             int
}

type Sprint struct {
    Number, MaxIterations    int
    Name, Promise, Prompt    string
    MaxHealAttempts          *int  // per-sprint override
}
```

### Engine Interface (`internal/engine/engine.go`)

```go
type Engine interface {
    Run(ctx context.Context, prompt string, opts RunOpts) (output string, exitCode int, err error)
    Name() string
}
```

Resolution precedence: CLI flag → epic `@engine` → `FRY_ENGINE` env → default (claude for all modes and stages).

All engines are wrapped in a `ResilientEngine` decorator (`internal/engine/resilient.go`) that auto-retries on rate-limit errors with exponential backoff (max 5 retries, 10s base delay, 120s cap, 25% jitter). Claude and Codex sessions can then be wrapped in a sticky `FailoverEngine` (`internal/engine/failover.go`) that promotes the fallback engine for the rest of the build after a transient failure succeeds on fallback. Detection patterns are in `internal/engine/ratelimit.go` and `internal/engine/failure.go`. Ollama is excluded from rate-limit detection and has no implicit fallback target.

`ClaudeEngine` accepts an optional MCP config path via `WithMCPConfig()` engine option, appending `--mcp-config <path>` to every invocation. Set via `--mcp-config` CLI flag or `@mcp_config` epic directive.

### Effort Levels (`internal/epic/types.go`)

| Level | MaxIterations | MaxSprints |
|-------|---------------|------------|
| fast | 12 | 2 |
| standard | 20 | 4 |
| high | 25 | 10 |
| max | 40 | 10 |

### ReportingFailure (`internal/agent/buildstatus.go`)

```go
type ReportingFailure struct {
    Stage   string `json:"stage"`             // build_audit, summary
    Message string `json:"message,omitempty"` // error description
}
```

Separates core build completion from post-build reporting failures. When a build succeeds but the audit or summary generation fails (e.g. quota exhaustion), the build status is set to `completed_with_reporting_failure` instead of `failed`.

### RollingSprintResult (`internal/agent/buildstatus.go`)

```go
type RollingSprintResult struct {
    Number      int     `json:"number"`
    Name        string  `json:"name"`
    Status      string  `json:"status"`
    DurationSec float64 `json:"duration_sec,omitempty"`
}
```

Compact per-sprint outcome persisted to `.fry/rolling-results.json` after each sprint. Provides durable structured input for resumable final-stage reporting so that build audit and summary can resume from disk state rather than reconstructing from raw logs.

### Sanity Checks (`internal/verify/types.go`)

Five check primitives: `@check_file` (file exists), `@check_file_contains` (regex match in file), `@check_cmd` (command exits 0), `@check_cmd_output` (command output matches regex), `@check_test` (go test command passes (exits 0 AND zero test failures detected; test count and framework are parsed from output)).

---

## Execution Flow

```
User Input                          Generated Artifacts
─────────────────────               ────────────────────
plans/plan.md         ──┐
plans/executive.md    ──┤  Triage Gate (1 cheap LLM call)
--user-prompt "..."   ──┤    ↓ Interactive confirmation [Y/n/a] (--yes auto-accepts, --no-project-overview skips)
assets/               ──┤    SIMPLE   → programmatic epic (0 LLM calls)
media/                ──┘    MODERATE → programmatic epic + auto-sanity-checks (0 LLM calls)
                               COMPLEX  → full prepare (3-4 LLM calls):
                               (manifest only)   .fry/AGENTS.md, .fry/epic.md, .fry/verification.md
                             (--full-prepare bypasses triage → always full prepare)

                        fry run
                        ───────
Before sprint loop:
  1. Preflight checks (required tools + custom commands)
  2. Git init (if needed)
  3. Git strategy setup (new repos stay on current branch for the first build; otherwise branch/worktree creation and artifact copy; persisted to .fry/git-strategy.txt)
  4. --continue: collect build state + structured resume point + LLM analysis (auto-detect resume point, reattach to persisted strategy)
  5. Harness self-validation: validates sanity check file targets (absolute paths, path traversal, missing parents, empty targets)

For each sprint (startSprint → endSprint):
  6. Sprint preflight: infer env vars, Docker refs, and external tools from sprint prompt text and warn about missing dependencies
  7. Docker up (if @docker_from_sprint <= current sprint)
  8. Pre-sprint shell hook
  9. Assemble layered prompt → .fry/prompt.md
 10. Agent iteration loop:
     │  for iter = 1 to maxIterations:
     │    ├─ Pre-iteration shell hook
     │    ├─ Run AI engine (codex|claude CLI)
     │    ├─ Append output → .fry/sprint-progress.txt
     │    ├─ Check promise token → early exit if found
     │    └─ No-op detection (no git diff) → early exit
 11. Run sanity checks
 12. If checks fail: alignment loop (effort-level-aware: fast=skip, standard=3, high=up to 10 with progress detection, max=unlimited with progress detection)
 13. Sprint audit (if enabled & effort != fast) — two-level loop:
     │  ├─ Outer loop (audit cycles): audit agent reviews + verifies previous issues
     │  ├─ Inner loop (fix iterations): fix agent → verify agent → repeat until resolved
     │  ├─ Issues tracked per-finding, FIFO ordered (oldest first)
     │  ├─ Sprint diff is classified as low/moderate/high complexity to adapt prompt emphasis and loop budgets
     │  ├─ Fix loop clusters related findings by remediation scope before prompting, validates each pass against a diff contract (issue IDs, target files, expected evidence), rejects empty/comment-only/out-of-scope diffs, routes already-fixed no-op claims to verify, and excludes blocker-category findings from normal code-fix iterations
     │  ├─ Fix loop skips verify on true no-op fix attempts and carries forward per-finding fix history
     │  ├─ Audit prompts include relevant intentional divergences from `.fry/deviation-log.md`
     │  ├─ Claude/Codex reuse same-role audit and fix sessions within the sprint audit; verify remains stateless
     │  ├─ Audit/fix/build-audit prompts include `.fry-config/codebase.md` and codebase memories when present
     │  ├─ If the agent forgets to write the audit file, Fry tries to recover a structured report from final stdout/log output before failing
     │  ├─ Verify agent must emit explicit per-issue outcome statuses (for example `RESOLVED`, `BEHAVIOR_UNCHANGED`, `BLOCKED`); unrecoverable missing output fails the audit
     │  ├─ Metrics are recorded per call and written to `.fry/build-logs/sprintN_audit_metrics.json`, including repeated-unchanged counters, cache-aware token telemetry, per-cycle productivity summaries, named strategy-shift events, trailing yield, and low-yield strategy/stop metadata
     │  └─ standard/high/max use effort+complexity-aware caps (falling back to legacy defaults when complexity is unknown)
 14. Git checkpoint commit
 15. Compact sprint progress → .fry/epic-progress.txt
 16. Write rolling sprint results → .fry/rolling-results.json
 17. Optional sprint review:
     │  ├─ Review agent: CONTINUE or DEVIATE
     │  └─ If DEVIATE: replan affected sprints

Final: build audit (if full epic completed; on failure, retries with one-shot fallback engine) → deferred check re-run → build summary (on failure, retries with one-shot fallback engine) → auto-archive (if full epic succeeded). If reporting stages fail even after fallback, status is set to `completed_with_reporting_failure` (core build succeeded but narration failed).
```

### Prompt Layering (8 layers, assembled in `sprint/prompt.go`)

| Layer | Content | Notes |
|-------|---------|-------|
| 1 | Executive context | `plans/executive.md` (optional) |
| 1.25 | Media manifest | categorized list of `media/` files (optional) |
| 1.5 | User directive | `--user-prompt`, `--gh-issue` (resolved), or `.fry/user-prompt.txt` (optional) |
| 1.625 | Agent disposition | identity disposition loaded from `templates/identity/disposition.md` |
| 1.75 | Quality directive | tiered self-review + build/test verification at `standard`+ effort |
| 2 | Strategic plan | reference to `plans/plan.md` |
| 3 | Sprint instructions | `@prompt` block from epic |
| 4 | Iteration memory | links to progress files |
| 5 | Completion signal | promise token (optional) |

---

## CLI Commands and Key Flags

```
fry [run] [epic.md] [start] [end]   # Execute sprints (run is default)
fry prepare [epic_filename]          # Generate .fry/ artifacts from plans
fry init                             # Scaffold project structure; auto-detect and scan existing codebases
fry replan                           # Replan after deviation
fry exit                             # Gracefully stop at the next safe checkpoint
fry audit                            # Standalone AI-powered build audit
fry clean                            # Archive .fry/ + build outputs to .fry-archive/
fry destroy                          # Remove all fry artifacts completely
fry config get|set                   # Read or write repo-local Fry settings
fry status                           # Show current build state (no LLM call)
fry monitor [project-dir]            # Real-time build monitoring (event stream, dashboard, log tail)
fry events                           # Stream or list build events (--follow --json for real-time)
fry team start|status|scale|...      # Standalone tmux-backed team runtime
fry identity                         # Print Fry's compiled-in identity (--full for all layers)
fry reflect                          # Trigger identity reflection from accumulated memories
fry agent prompt                     # Print the agent system prompt
fry version                          # Print version

Key flags:
  --engine codex|claude|ollama       # AI engine for build
  --prepare-engine codex|claude|ollama  # AI engine for prepare phase
  --fallback-engine codex|claude|ollama  # Sticky fallback engine on transient failure
  --no-engine-failover               # Disable cross-engine failover
  --effort fast|standard|high|max       # Effort level (auto-detect if omitted)
  --model model-id                   # Override agent model (e.g. opus[1m], sonnet, haiku)
  --mode software|planning|writing   # Execution mode (default: software)
  --planning                         # Alias for --mode planning (backwards compat)
  --user-prompt "..."                # Inject directive into prompts
  --user-prompt-file path            # Load directive from file
  --gh-issue https://.../issues/N    # Fetch GitHub issue via gh CLI and use it as the task definition
  --dry-run                          # Validate without executing
  --sprint N                         # Start from sprint N
  --resume                           # Skip iterations, sanity check + align with boosted attempts
  --continue                         # LLM-assisted auto-resume from where build left off
  --git-strategy auto|current|branch|worktree  # Git isolation strategy (default: auto)
  --branch-name name                 # Explicit branch name (overrides auto-generated)
  --always-verify                    # Force sanity checks, alignment, and audit regardless of effort/complexity
  --full-prepare                     # Skip triage, run full prepare pipeline
  --triage-only                      # Classify task and exit (no artifact generation)
  --yes, -y                           # Auto-accept all confirmation prompts (triage, overview, clean)
  --no-project-overview              # Skip interactive confirmations (triage + project summary)
  --no-observer                      # Disable observer metacognitive layer
  --no-review                        # Skip mid-build sprint review
  --no-audit                         # Skip audits
  --verbose, -v                      # Verbose logging; monitor also shows granular synthetic events
  --no-color                         # Disable colored output (also: NO_COLOR env, TERM=dumb)
  --sarif                            # Write build-audit.sarif in SARIF 2.1.0 format alongside build-audit.md
  --json-report                      # Write build-report.json with structured sprint results
  --confirm-file                     # File-based interactive prompts for agent LLMs
  --show-tokens                      # Print per-sprint token usage summary to stderr after the run
  --telemetry                        # Enable experience upload to consciousness API
  --no-telemetry                     # Disable experience upload
  --simple-continue                  # Resume from first incomplete sprint without LLM analysis
  --review                           # Enable sprint review between sprints
  --simulate-review verdict          # Simulate review verdict: CONTINUE or DEVIATE
  --mcp-config path                  # MCP server configuration file (Claude engine only)
  --project-dir path                 # Working directory (default: .)
```

---

## Key Constants (`internal/config/config.go`)

| Constant | Value | Purpose |
|----------|-------|---------|
| `DefaultEngine` | `claude` | Default build engine |
| `DefaultPrepareEngine` | `claude` | Default prepare engine |
| `DefaultPlanningEngine` | `claude` | Default planning-mode engine |
| `DefaultWritingEngine` | `claude` | Default writing-mode engine |
| `WritingOutputDir` | `output` | Output directory for writing-mode deliverables |
| `DefaultMaxHealAttempts` | `3` | Alignment loop retries (fallback for auto effort) |
| `DefaultMaxFailPercent` | `20` | Max % of checks that can fail and still pass |
| `HealAttemptsHigh` | `10` | Alignment attempts for high effort |
| `HealStuckThresholdHigh` | `2` | Consecutive no-progress attempts before exit (high) |
| `HealStuckThresholdMax` | `3` | Consecutive no-progress attempts before exit (max) |
| `HealMinAttemptsMax` | `10` | Min attempts before mid-loop threshold exit (max) |
| `HealSafetyCapMax` | `50` | Hard safety cap for unlimited max-effort alignment |
| `MaxFailPercentMax` | `10` | Stricter threshold for max effort |
| `DefaultMaxOuterAuditCycles` | `3` | Outer audit cycles per sprint (standard/default) |
| `DefaultMaxInnerFixIter` | `3` | Inner fix iterations per audit report (standard/default) |
| `MaxOuterCyclesHighCap` | `12` | Outer audit cycles at high effort |
| `MaxOuterCyclesMaxCap` | `100` | Outer audit cycles at max effort (safety valve; stale detection governs actual exit) |
| `MaxInnerFixIterHigh` | `7` | Inner fix iterations at high effort |
| `MaxInnerFixIterMax` | `10` | Inner fix iterations at max effort |
| `AuditLowYieldTrailingCycles` | `2` | Recent cycles included in trailing productivity checks |
| `AuditLowYieldMinFixCalls` | `2` | Minimum fix attempts before low-yield heuristics engage |
| `AuditLowYieldVerifyYieldFloor` | `0.75` | Verify-yield floor for low-productivity detection |
| `AuditLowYieldFixYieldFloor` | `0.50` | Fix-yield floor for low-productivity detection |
| `AuditLowYieldNoOpRateFloor` | `0.50` | No-op-rate floor for low-productivity detection |
| `AuditLowYieldStopCycles` | `2` | Consecutive low-yield cycles before early stop |
| `DefaultDockerReadyTimeout` | `30` | Seconds for Docker health check |
| `DefaultMaxDeviationScope` | `3` | Max sprints affected by replan |
| `MaxAuditDiffBytes` | `100000` | Max diff size for audit context |
| `AuditSessionsDir` | `.fry/sessions` | Transient same-role audit session ID files |
| `ResumeHealMultiplier` | `2` | Heal iteration multiplier on resume |
| `ResumeMinHealAttempts` | `6` | Minimum alignment attempts on resume |
| `BuildModeFile` | `.fry/build-mode.txt` | Persisted build mode for `--continue` |
| `ValidationChecklistFile` | `.fry/validation-checklist.md` | Deferred-failure checklist emitted before deferred check replay |
| `ExitRequestFile` | `.fry/exit-request.json` | Structured graceful-exit request written by `fry exit` |
| `ResumePointFile` | `.fry/resume-point.json` | Settled resume checkpoint consumed by continue heuristics |
| `RunsDir` | `.fry/runs` | Per-run immutable status snapshot directory |
| `RunPrefix` | `run-` | Prefix for run directory names (`run-YYYYMMDD-HHMMSS`) |
| `ArchiveDir` | `.fry-archive` | Directory for archived builds |
| `ArchivePrefix` | `.fry--build--` | Prefix for archive folder names |
| `TriagePromptFile` | `.fry/triage-prompt.md` | Classifier prompt for triage gate |
| `TriageDecisionFile` | `.fry/triage-decision.txt` | Classifier output (complexity, effort, sprints, reason) |
| `DefaultGitStrategy` | `auto` | Default git isolation strategy |
| `GitWorktreeDir` | `.fry-worktrees` | Parent directory for worktree checkouts |
| `GitStrategyFile` | `.fry/git-strategy.txt` | Persisted strategy for continue/resume |
| `GitBranchPrefix` | `fry/` | Prefix for auto-generated branch names |
| `ObserverDir` | `.fry/observer` | Observer files directory |
| `ObserverEventsFile` | `.fry/observer/events.jsonl` | Structured event stream |
| `ObserverScratchpadFile` | `.fry/observer/scratchpad.md` | Per-session working memory |
| `ObserverPromptFile` | `.fry/observer/wake-prompt.md` | Transient wake-up prompt |
| `MaxObserverEvents` | `50` | Max recent events included in wake-up prompt |
| `IdentityCoreFile` | `identity/core.md` | Core identity (go:embed path) |
| `IdentityDispositionFile` | `identity/disposition.md` | Disposition (go:embed path) |
| `IdentityDomainsDir` | `identity/domains` | Domain files directory (go:embed path) |
| `IdentityJSONFile` | `identity/identity.json` | JSON identity (go:embed, produced by Reflection) |
| `ExperiencesDir` | `.fry/experiences` | Build experience records |
| `ConsciousnessDir` | `.fry/consciousness` | In-progress consciousness runtime state |
| `ConsciousnessSessionFile` | `.fry/consciousness/session.json` | Durable session state |
| `ConsciousnessCheckpointsFile` | `.fry/consciousness/checkpoints.jsonl` | Append-only checkpoint log |
| `ConsciousnessCheckpointsDir` | `.fry/consciousness/checkpoints` | Per-checkpoint records |
| `ConsciousnessScratchpadHistory` | `.fry/consciousness/scratchpad-history.jsonl` | Scratchpad delta history |
| `ConsciousnessDistilledDir` | `.fry/consciousness/distilled` | Distilled checkpoint summaries |
| `ConsciousnessUploadQueueDir` | `.fry/consciousness/upload-queue` | Pending checkpoint/lifecycle uploads |
| `ConsciousnessPromptFile` | `.fry/consciousness-prompt.md` | Experience synthesis prompt (transient, deleted after use) |
| `ConsciousnessCheckpointPromptFile` | `.fry/consciousness/checkpoint-prompt.md` | Checkpoint distillation prompt (transient) |
| `ProjectConfigFile` | `.fry-config/config.json` | Repo-local Fry settings (currently self-improve engine) |
| `SettingsFile` | `.fry/settings.json` | User settings under `~/.fry/` (telemetry enabled by default; created by `fry init`) |
| `PendingUploadsDir` | `.fry/experiences/pending` | Cached uploads for retry |
| `ConsciousnessAPIURL` | `https://fry-consciousness-api.yevgetman.workers.dev` | Consciousness API endpoint |
| `UploadTimeoutSeconds` | `10` | Background upload timeout |
| `TelemetryEnvVar` | `FRY_TELEMETRY` | Env var for telemetry opt-in |
| `ConsciousnessWriteKey` | (compiled-in) | Public write-only key for consciousness API |

---

## Asset Systems

### Text Assets (`assets/` → `internal/assets/`)
- Scans `assets/` for text files (30+ extensions: .md, .txt, .json, .yaml, .go, .py, etc.)
- Full file contents injected into prepare-phase prompts
- Limits: 512 KB/file, 2 MB total, 100 files max
- Skips hidden files, symlinks, binary files

### Media Assets (`media/` → `internal/media/`)
- Scans `media/` for binary files (images, PDFs, fonts, videos, etc.)
- Generates categorized manifest (path + size) — contents NOT read
- Limit: 10,000 files
- Manifest injected into both prepare and sprint prompts

---

## Build & Test

```bash
make build     # go build -o bin/fry ./cmd/fry
make test      # go test -race ./...
make lint      # golangci-lint run
make install   # build + cp bin/fry ~/.local/bin/fry; on Darwin also ad-hoc codesign the installed binary
make clean     # rm -rf bin/
```

**108 test files** covering all packages. Tests use temp directories, env mocking, and mock engines. Most tests call `t.Parallel()`. No CI/CD configured — local testing only.

---

## Documentation Index (`docs/`)

| File | Topic |
|------|-------|
| `getting-started.md` | Install, prerequisites, first build |
| `commands.md` | Full CLI reference |
| `effort-levels.md` | Effort triage and sprint sizing |
| `epic-format.md` | Epic syntax, directives, validation |
| `engines.md` | Codex/Claude/Ollama config, mixing, model overrides |
| `sprint-execution.md` | Agent loop, prompt assembly, progress |
| `sanity-checks.md` | Check primitives, format, outcome matrix |
| `alignment.md` | Alignment loop mechanics |
| `sprint-audit.md` | Per-sprint semantic review |
| `build-audit.md` | Final holistic audit |
| `sprint-review.md` | Mid-build review, replanning, deviations |
| `docker.md` | Docker Compose lifecycle |
| `preflight.md` | Pre-build validation |
| `planning-mode.md` | Non-code document generation |
| `writing-mode.md` | Human-language content (books, guides, reports) |
| `media-assets.md` | Binary asset handling |
| `supplementary-assets.md` | Text asset injection |
| `github-issues.md` | GitHub issue URL ingestion via `--gh-issue` |
| `user-prompt.md` | Prompt injection, hierarchy, persistence |
| `project-structure.md` | Directory layout, file reference |
| `terminal-output.md` | Output format, logging |
| `architecture.md` | Internal package structure, data flow |
| `monitor.md` | Real-time build monitoring: event stream, dashboard, log tail, NDJSON |
| `observer.md` | Metacognitive layer: events, identity, wake-ups |
| `git-strategy.md` | Branch/worktree isolation strategies |
| `self-improvement.md` | Automated self-improvement pipeline |
| `consciousness.md` | Experience synthesis and identity pipeline |
| `triage.md` | Complexity classification, interactive confirmation, effort suggestion |
| `codebase-awareness.md` | Existing codebase detection, scanning, memories, and pipeline integration |
| `agent.md` | Agent foundation: invocation, artifact schema, system prompt generation |
| `steering.md` | Mid-build human intervention: directives, holds, pauses, graceful exits |

---

## Self-Improvement Pipeline (`.self-improve/`)

Fry improves itself via an automated loop driven by `.self-improve/orchestrate.sh`:

| File | Purpose |
|------|---------|
| `orchestrate.sh` | Bash orchestrator — planning, build, merge/PR, cleanup |
| `executive.md` | Static directive copied to `plans/` before each run |
| `planning-prompt.md` | User prompt for codebase scanning (10 categories) |
| `build-prompt.md` | User prompt for implementation (Fry selects items) |
| `build-journal.json` | Build history — last 30 entries, used by planning for experience category |
| `config` | KEY=VALUE configuration (overrides script defaults) |
| `logs/` | Per-run timestamped log files |

The roadmap lives in GitHub Issues (labels: category/*, priority/*, effort/*, status/*). Pickup requires `self-improve` plus status; sparse manual issues are normalized from labels, body fields, title prefixes, and lightweight heuristics before export. Build-time effort is derived from a triage pass over approved issues rather than trusting issue-declared effort. See docs/self-improvement.md for the full architecture.

**Flow:** Planning (scan codebase + analyze build journal → create findings) → Build (export approved issues → normalize sparse metadata → triage each issue for current-codebase effort sizing → select items → worktree → implement → test → optional post-build heal loop → merge/PR → write journal entry). Planning runs only when roadmap needs replenishment (< 5 items, category gaps, or imbalance). Approved issues are exported through a normalization step so manual issues with sparse metadata still produce usable `approved-items.json` entries, then triaged so selection uses current-codebase effort rather than issue-declared effort. The self-improve orchestrator resolves its engine from `fry config get engine` (repo-local `.fry-config/config.json`) unless `.self-improve/config` explicitly overrides `PLANNING_ENGINE` or `BUILD_ENGINE`; the same build engine is also used for post-build heals and journal summarization, with optional model overrides via `HEAL_MODEL` and `JOURNAL_MODEL`. After each build, a structured journal entry is written to `build-journal.json` with outcome, items, alignment rounds, and AI observations. During planning, the journal feeds **Category J: Build Experience** for pattern-based improvements.

**Key flags:** `--auto-merge` (direct merge to master), `--skip-planning`, `--skip-build`, `--dry-run`.

---

## Epic File Format (Quick Reference)

Epics are markdown files parsed by `epic/parser.go`. Global directives appear before any sprint block:

```markdown
@epic My Project
@engine codex
@effort high
@require_tool go
@require_tool node
@require_tool docker
@preflight_cmd go version
@pre_sprint echo "starting sprint"
@docker_from_sprint 2
@max_heal_attempts 3
@review_between_sprints
@review_engine claude
@audit_after_sprint
@verification .fry/verification.md

@sprint 1
@name Foundation
@max_iterations 20
@promise FOUNDATION_DONE
@prompt
Build the project skeleton...
@end
```

Sprint prompts follow a 7-part convention: OPENER, REFERENCES, BUILD LIST, CONSTRAINTS, VERIFICATION, STUCK HINT, PROMISE.

---

## Design Patterns

- **Two-phase progress tracking:** per-sprint log (`sprint-progress.txt`) + cross-sprint compacted summaries (`epic-progress.txt`) for bounded context
- **Promise tokens:** agent writes `===PROMISE: TOKEN===` to signal sprint completion → early exit
- **No-op detection:** if git diff shows no changes for 2-3 consecutive iterations and sanity checks pass → early exit
- **Two-level audit loop:** outer cycles discover new issues, inner loops fix+verify them FIFO (old issues cleared before new ones surfaced); per-finding tracking across cycles; CRITICAL/HIGH block, MODERATE advisory, LOW included at high/max effort (non-blocking); blocker categories (`environment_blocker`, `harness_blocker`, `external_dependency_blocker`) mark sprint blocked; complexity classification adapts budgets; resolved-finding ledger prevents re-raising; fix agent receives inline target files + fix history, verify agent receives fix diff + recommended fixes; progress-based exit at high/max effort (continue while resolving findings, exit after 2 stale cycles); same-role session continuity for Claude/Codex audit and fix sessions
- **Graceful signal handling:** Ctrl+C saves partial work via git checkpoint
- **Engine abstraction:** any CLI-based AI tool can be added by implementing `Engine` interface (2 methods: `Run`, `Name`)
