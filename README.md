# fry 🍳

![fry](trustmepro.png)

## What is Fry?

Fry is a self-improving agent orchestration tool designed for long-run coding, planning, and writing tasks. You provide some input — as little as a simple prompt or as much as a comprehensive build plan with an extensive corpus of supporting documents — and it will apply a layered system of planning, building, and checking its own work to produce a result with the level of effort of your choosing. To put it simply, **you give it as much or as little you want, and it will do as much or as little as you want it to do.**

### What does it actually do?

Fry can code, write [planning documents](docs/planning-mode.md), or write human-language content like essays, technical writing, and can even [write a complete book](docs/writing-mode.md)!

### Input

Fry takes one or all of the following as input:

- **A [user prompt](docs/user-prompt.md)** — provided as text on the command line or a path to a text file.
- **A [GitHub issue URL](docs/github-issues.md)** — fetched through the authenticated `gh` CLI and converted into Fry artifacts automatically.
- **An executive.md file** — a high-level description of the project. Think of this as the "What and Why" for the project.
- **A plan.md file** — a detailed plan to build/write the output. Think of this as the "How" for the project.

Any of the above can be something you write yourself or have an LLM write for you.

### Plan Resolution

- If only a user prompt is provided, Fry generates a `plan.md` from the user prompt.
- If an `executive.md` is provided as well, Fry uses it to inform the generated `plan.md`.
- If a `plan.md` is provided, Fry uses it directly.

In short, Fry will do its best to use whatever info you provide to generate a `plan.md` file, if one was not explicitly provided.

### Triage

Before doing any of the heavy lifting, Fry runs a [triage gate](docs/triage.md) — a single cheap LLM call that classifies the task as **simple**, **moderate**, or **complex** and suggests an effort level. This determines how much preparation is needed:

- **Simple** tasks (fix a typo, add a config flag) skip preparation entirely — Fry builds a 1-sprint epic programmatically and gets straight to work. Zero LLM calls wasted on planning.
- **Moderate** tasks (add an endpoint with tests, build a small tool) also skip LLM-based preparation — Fry builds a programmatic 1-2 sprint epic with auto-generated sanity checks. Zero LLM calls for planning.
- **Complex** tasks (multi-subsystem features, architectural changes) get the full preparation pipeline described below.

After classification, Fry shows you the triage decision (difficulty, effort, reason) and asks you to confirm, decline, or adjust before the build begins. You can override both difficulty and effort at this step. Use `--yes` / `-y` to auto-accept all confirmation prompts, or `--no-project-overview` to skip them entirely.

Both simple and moderate tasks respect the effort level (suggested by triage or overridden with `--effort`), which controls iteration budgets, alignment, and audit depth. Max effort is reserved for complex tasks. The classifier is intentionally biased toward over-classification — it's better to over-prepare a simple task than to under-prepare a complex one. Use `--full-prepare` to bypass triage and force the full pipeline.

### Preparation

For complex tasks (or when `--full-prepare` is used), Fry will:

1. Generate an `AGENTS.md` file (if one was not provided) establishing best practices for the agents
2. Decompose `plan.md` into an [epic](docs/epic-format.md), delimited by sprints with each sprint broken up by specific tasks
3. Generate a `verification.md` for [sanity checks](docs/sanity-checks.md) to run after each sprint (deep semantic checks are done as part of a separate [code review system](docs/sprint-audit.md))

### The Build

Fry deploys agents using [OpenAI Codex, Claude Code, or Ollama](docs/engines.md) — the specific models used vary by task and user-defined [effort level](docs/effort-levels.md).

A single agent carries out the work to complete a sprint (although there is a parallel mode to run multiple agents at once).

Once a sprint is complete, [sanity checks](docs/sanity-checks.md) run to verify the deliverables. If any checks fail, an [alignment system](docs/alignment.md) deploys to fix the issues.

If/when all sanity checks pass, a [code review](docs/sprint-audit.md) is deployed to ensure the work has been completed with no bugs, edge cases covered, etc. — basically that it was done well. A single agent session reviews the sprint's changes, classifies issues by severity, fixes everything above LOW, and re-reviews until clean — all within one uninterrupted session. This eliminates the context handoff overhead of the previous multi-agent pattern. CRITICAL and HIGH issues block the sprint; MODERATE is advisory.

The build continues in this manner until complete.

## How It Works

```
plans/plan.md          You write this -- what to build        (at least one of
  OR                                                           these three is
plans/executive.md     You write this -- why to build it       required)
  OR
--user-prompt "..."    You describe it -- Fry generates the rest
  OR
--user-prompt-file f   Same, but reads the prompt from a local file
  OR
--gh-issue URL         Fry fetches a GitHub issue and generates the rest
media/                 Optional binary assets (images, PDFs, fonts) referenced in plans
assets/                Optional text documents (specs, schemas) read during plan generation
        |
        v
  fry run               Triage gate: cheap LLM classifies complexity + effort
                           ↓ Interactive confirmation [Y/n/a] (adjust difficulty/effort)
                           SIMPLE   → programmatic epic (0 prep calls)
                           MODERATE → programmatic epic + auto-sanity-checks (0 prep calls)
                           COMPLEX  → full prepare (below)
                         (--full-prepare skips triage, --yes auto-accepts, --no-project-overview skips confirmation)
        |
        v
  fry prepare           Step 0 (if needed): AI generates plans/plan.md from executive.md
  (complex tasks)        Steps 1-3: AI generates .fry/AGENTS.md + .fry/epic.md + .fry/verification.md
  (pass --mode planning for documents, --mode writing for books/guides)
        |
        v
     fry run             Executes sprints via AI agent loop
        |                + runs independent sanity checks
        |                + auto-aligns on sanity check failure
        v
  Working software       Git-checkpointed after each sprint
  (or output/)           Planning: 1--research--market-landscape.md
                         Writing:  01--introduction.md → manuscript.md
```

Each sprint runs as an iterative loop where the AI agent gets a prompt, does work, and logs progress. The next iteration reads what the previous one accomplished and continues. When the agent signals completion (via a promise token), the sprint ends and the next one begins.

**Key mechanisms:**

- **Triage gate** -- before running the full prepare pipeline, a single cheap LLM call classifies task complexity as `simple`, `moderate`, or `complex` and suggests an effort level. After classification, an interactive confirmation prompt lets you review, accept, or adjust the difficulty and effort before the build starts (`--yes` / `-y` auto-accepts, `--no-project-overview` skips entirely). Simple and moderate tasks skip prepare entirely (zero LLM calls for planning) with effort-aware iteration budgets, alignment, and audit depth. Complex tasks get the full pipeline. Max effort is reserved for complex tasks. Biased toward over-classification to avoid wasting tokens. See [Triage](docs/triage.md). Use `--full-prepare` to bypass.
- **Project overview** -- after `plan.md` exists, Fry shows an AI-generated project summary and asks for confirmation before generating build artifacts (`--yes` / `-y` auto-accepts, `--no-project-overview` skips entirely)
- **Effort-level triage** -- `--effort fast|standard|high|max` controls sprint count, density, and rigor. Auto-detects when unspecified. See [Effort Levels](docs/effort-levels.md).
- **GitHub issue ingestion** -- `--gh-issue <url>` fetches issue metadata, body, labels, and recent comments through the authenticated `gh` CLI, persists the fetched context to `.fry/github-issue.md`, and routes the result through normal triage/prepare/build flow. See [GitHub Issues](docs/github-issues.md).
- **Media assets** -- optional `media/` directory for images, PDFs, fonts, and other files referenced in plans and copied into builds
- **Supplementary assets** -- optional `assets/` directory for text reference documents (specs, schemas, requirements) whose full contents are read during plan and epic generation
- **Layered prompts** -- assembled per sprint with codebase context, executive context, media manifest, user directives, operational disposition, plan references, sprint tasks, iteration memory, and completion signals
- **Two-file progress tracking** -- per-sprint iteration log + cross-sprint compacted summary for bounded context
- **Promise tokens** -- `===PROMISE: TOKEN===` signals sprint completion
- **Sanity checks** -- machine-executable checks run after each sprint with a configurable failure threshold (`@max_fail_percent`, default 20%) — minor failures are deferred rather than blocking the build
- **Alignment** -- automatic re-runs with targeted fix prompts on sanity check failure; `--resume` picks up where a failed build left off with boosted alignment attempts; `--continue` uses an LLM agent to analyze build state and auto-resume (automatically restores the build mode from the previous run)
- **Sprint code review** -- post-sprint single-session code review where one agent call reviews the sprint's changes, classifies issues by severity (CRITICAL/HIGH/MODERATE/LOW), fixes everything above LOW, and re-reviews until clean or the iteration limit is reached. Tracks iteration count and convergence status, deduplicates findings, and includes fallback recovery when the agent fails to write `.fry/sprint-review.txt`. CRITICAL/HIGH block the build; MODERATE is advisory.
- **Build audit** -- final holistic codebase audit after the entire epic completes, with iterative remediation and the same report-recovery fallback for missing `build-audit.md`
- **Build summary** -- comprehensive `build-summary.md` generated after all sprints, covering what was built, events, audit findings, and advisories
- **Per-run status snapshots** -- every build/continue/resume invocation creates an immutable status snapshot in `.fry/runs/<run-id>/` so that later retries cannot overwrite earlier run history. Each snapshot records run lineage (fresh, continue, resume, or retry with parent run ID). Use `fry status --runs` to list all runs and `fry status --run <id>` to inspect a specific run.
- **Build archiving** -- on successful full builds, `.fry/` and root-level outputs are auto-archived to `.fry-archive/`; run `fry clean` to archive manually
- **Git strategy** -- `--git-strategy auto|current|branch|worktree` controls build isolation. Auto mode lets triage decide: complex tasks get an isolated worktree, simpler tasks get a new branch, but a first build in a freshly initialized repo stays on the primary branch. Use `current` for the previous behavior (work on the current branch). See [Git Strategy](docs/git-strategy.md).
- **Git checkpoints** -- automatic commits after each sprint
- **Rate-limit resilience** -- automatic retry with exponential backoff when engines hit API rate limits (429, overloaded, etc.); see [Engines](docs/engines.md#rate-limit-resilience)
- **Sticky cross-engine failover** -- after same-engine retries are exhausted, Claude and Codex builds can automatically fail over to the other engine on transient failures (rate limits, timeouts, 5xx, connection errors). Once the fallback succeeds, Fry pins the build to that engine for the rest of the run and re-resolves models using the normal session-by-effort matrix for the new engine. Control with `--fallback-engine` and `--no-engine-failover`. See [Engines](docs/engines.md#cross-engine-failover).
- **MCP config passthrough** -- `--mcp-config` flag and `@mcp_config` directive pass MCP server configuration to Claude Code for extended agent capabilities (LSP, AST tools, etc.); see [Engines](docs/engines.md#mcp-server-configuration)
- **Dynamic sprint review** -- optional mid-build review with replanning
- **Observer** -- metacognitive layer that watches builds, notices patterns, writes durable checkpoints, preserves scratchpad continuity on resume, and quarantines malformed outputs instead of promoting raw transcripts. Identity is compiled into the binary and read-only during builds. Non-fatal; effort-level gated. See [Observer](docs/observer.md).
- **Copilot** -- parallel persistent agent session that monitors a build via cron-driven wakes (default every 10m) and intervenes when something goes wrong: edits the fry source tree to fix canonical bugs (test → make install → commit → push → restart), or remediates broken build artifacts to unstick the build. Auto-enables at `--effort=max`, opt-in elsewhere via `--copilot`. Attach with `fry copilot attach`. See [Copilot](docs/copilot.md).
- **Experience upload** -- telemetry sends anonymized checkpoint summaries and session lifecycle events to the central consciousness API (enabled by default; `fry init` creates `~/.fry/settings.json`). Uploads are queued locally and retried automatically; `fry status --consciousness` reports local checkpoint and upload health. Control via `--telemetry` / `--no-telemetry`, `FRY_TELEMETRY` env var, or `~/.fry/settings.json`. See [Consciousness](docs/consciousness.md).
- **Sprint preflight** -- before each sprint, Fry infers environment prerequisites (env vars, Docker refs, external tools) from the sprint prompt text and warns about missing dependencies
- **Harness self-validation** -- before the sprint loop starts, Fry validates sanity check file targets (absolute paths, path traversal, missing parent directories, empty targets) and reports issues so broken checks don't silently pass
- **Reporting failure recovery** -- if the build audit or summary generation fails (e.g. quota exhaustion), Fry automatically retries with a one-shot fallback engine and records the outcome as `completed_with_reporting_failure` rather than marking the entire build as failed
- **Writing mode** -- `--mode writing` re-orients the pipeline for books, guides, and reports with content-oriented audit criteria and a final `manuscript.md`
- **Colored output** -- terminal output is colorized for readability (phase banners in cyan, PASS in green, FAIL in red, warnings in yellow). Respects `NO_COLOR`, `TERM=dumb`, and `--no-color`. Log files are always plain text.

## Self-Improving Codebase

Fry improves itself. An automated pipeline runs daily, scanning the Fry source code for bugs, testing gaps, feature opportunities, and other improvements. It selects 2-3 items from a roadmap, implements them, runs the full test suite, and either merges directly to master or opens a pull request for human review.

The loop uses Fry's own features — planning mode for discovery, `--always-verify` for quality gates, worktrees for isolation, and the triage gate for complexity-appropriate effort levels. A bash orchestrator (`.self-improve/orchestrate.sh`) drives the cycle, and a macOS launchd agent triggers it daily. Set the repo-local self-improve engine with `fry config set engine codex`.

GitHub Issues is the source of truth for the project roadmap. To run the loop manually:

Manual issues can enter the loop too: add `self-improve` plus either
`status/proposed` or `status/approved`. Category and priority labels are
preferred, but Fry can normalize sparse issue metadata from the title and body
when those labels are missing. Effort is sized later by the triage step against
the current codebase.

```bash
./.self-improve/orchestrate.sh                  # full loop (planning if needed + build + PR)
./.self-improve/orchestrate.sh --auto-merge     # merge directly to master
./.self-improve/orchestrate.sh --skip-build     # planning only
./.self-improve/orchestrate.sh --skip-planning  # build only
./.self-improve/orchestrate.sh --dry-run        # preview without making changes
fry config set engine codex                     # use Codex for self-improve in this repo
```

See [Self-Improvement Pipeline](docs/self-improvement.md) for the full architecture, configuration, and operational guide.

## Requirements

- **Go 1.22+** — to build fry from source
- **git** — for automatic sprint checkpointing
- **tmux** (optional but required for `fry team`) — used for standalone team worker hosts
- **bash** — used by AI engine CLIs and sanity check commands
- **gh** (optional) — required only when using `--gh-issue`
- At least one AI engine CLI:
  - [Claude Code](https://www.npmjs.com/package/@anthropic-ai/claude-code): `npm i -g @anthropic-ai/claude-code` (default engine)
  - [OpenAI Codex CLI](https://www.npmjs.com/package/@openai/codex): `npm i -g @openai/codex`
  - [Ollama](https://ollama.com): `brew install ollama` — local models, no API key required (required only when using `--engine ollama`)
- Install both Claude Code and Codex CLI if you want automatic cross-engine failover between them.
- **Docker** (optional) — only needed if your project uses `@docker_from_sprint`

## Quick Start

```bash
# Install
git clone https://github.com/yevgetman/fry.git && cd fry
make install   # installs to ~/.local/bin/fry by default
# macOS: ad-hoc signs ~/.local/bin/fry after copying

# Option A: Start from just a prompt (no files needed)
fry --user-prompt "build a REST API for a todo app with PostgreSQL" --engine claude

# Option B: Start from a GitHub issue
fry --gh-issue https://github.com/yevgetman/fry/issues/74 --engine claude

# Option C: Create a plan file first
mkdir -p plans
cat > plans/plan.md << 'EOF'
# My Project -- Build Plan
**Stack:** Node 20, Express, PostgreSQL 16, TypeScript strict mode.
...
EOF
fry --engine claude

# Option D: Write a book or guide
fry --mode writing --user-prompt "Write a comprehensive guide to Go concurrency"

# Validate without running
fry --dry-run
```

See [Getting Started](docs/getting-started.md) for full setup instructions.

## Team Runtime

Fry now ships a standalone `fry team` runtime for tmux-backed parallel workers. This is intentionally separate from `fry run`: the team runtime is a durable execution subsystem you can start, inspect, pause, resume, scale, and shut down on its own before it is ever wired into the main sprint runner.

Key properties:

- Team state lives under `.fry/team/<team-id>/...`
- Workers are long-lived tmux windows that run a hidden `fry team worker` loop
- Tasks are loaded from JSON and claimed durably by role-aware workers
- Worker heartbeats, task ownership, and event emission are persisted on disk
- `shared` mode runs tasks directly in the project directory
- `per-worker-worktree` mode gives each worker an isolated git worktree and automatically builds an integrated output worktree when all tasks complete
- Team lifecycle events flow through the normal `fry events` stream, and `fry status` / `fry monitor` surface the active team summary

Basic flow:

```bash
fry team start --workers 3 --role executor --task-file ./tasks.json
fry team status
fry team pause
fry team resume
fry team scale --add 1
fry team shutdown --force
```

Task files are JSON arrays or `{ "tasks": [...] }` objects. Each task can specify `id`, `title`, `role`, `priority`, and a shell `command`. Workers execute commands inside their assigned work directory and persist logs under `.fry/team/<team-id>/artifacts/`.

## Commands

| Command | Description |
|---|---|
| `fry run` | Execute sprints from an epic file (default command) |
| `fry init` | Scaffold project structure; auto-detect and scan existing codebases |
| `fry prepare` | Generate `.fry/AGENTS.md`, `.fry/epic.md`, and `.fry/verification.md` from your plan |
| `fry replan` | Replan an epic after a deviation |
| `fry exit` | Request a graceful stop at the next safe checkpoint and persist a resumable pickup point |
| `fry identity` | Print Fry's compiled-in identity (core + disposition) |
| `fry reflect` | Trigger identity reflection from accumulated memories |
| `fry audit` | Run a standalone AI-powered build-level audit on any codebase |
| `fry team` | Start and operate the standalone tmux-backed team runtime |
| `fry status` | Show current build state, or archived/worktree build history if no active build |
| `fry monitor` | Real-time build monitoring — enriched event stream, verbose granular mode, dashboard with live audit-cycle state, or log tail |
| `fry clean` | Archive `.fry/` and build outputs to `.fry-archive/` |
| `fry destroy` | Remove all fry artifacts as if fry was never run |
| `fry config` | Read or write repo-local Fry settings (engine, etc.) |
| `fry agent prompt` | Print the agent system prompt (artifact schema, lifecycle, identity) |
| `fry events` | Stream or list build events from the observer event log |
| `fry version` | Print fry version |

```bash
fry                                    # Run all sprints (prepare: claude, build: claude)
fry --engine codex                     # Use OpenAI Codex for build stage
fry --engine claude --fallback-engine codex  # Prefer Claude, fail over to Codex if Claude is transiently unavailable
fry --engine ollama                    # Use local Ollama models (no API key)
fry --gh-issue https://github.com/yevgetman/fry/issues/74  # Start from a GitHub issue
fry --effort fast                       # Simple task: 1-2 sprints, minimal overhead
fry --effort max                       # Maximum rigor: extended prompts, thorough reviews
fry run epic.md 3 5                    # Run sprints 3-5
fry run --resume --sprint 4             # Resume failed sprint 4 (skip iterations, align only)
fry run --continue                     # Auto-detect and resume from where you left off
fry exit                               # Gracefully stop a running build and persist a resumable checkpoint
fry clean                              # Archive .fry/ and build outputs (interactive)
fry clean --force                      # Archive without confirmation prompt
fry destroy -y                         # Remove all fry artifacts completely
fry --mode planning                    # Planning mode (documents, not code) — claude for both stages
fry --mode writing --user-prompt "..."  # Writing mode (books, guides) — claude for both stages
fry --user-prompt "no ORMs, raw SQL"   # Inject a directive
fry --user-prompt "build a todo app"  # Start from just a prompt (no plan files needed)
fry --user-prompt-file ./prompt.txt   # Load a longer prompt from a file
fry --git-strategy worktree            # Force worktree isolation for the build
fry --git-strategy branch --branch-name feat/api  # Build on a named branch
fry --always-verify                    # Force sanity checks+audit on all tasks
fry --no-observer                      # Disable the observer metacognitive layer
fry prepare --effort standard            # Generate artifacts with standard effort sizing
```

See [Commands](docs/commands.md) for complete flag and argument reference.

## Documentation

| Document | Description |
|---|---|
| [Getting Started](docs/getting-started.md) | Prerequisites, installation, first build walkthrough |
| [Commands](docs/commands.md) | Full CLI reference for all commands and flags |
| [Architecture](docs/architecture.md) | Internal package map and runtime layering, including the standalone team runtime |
| [Effort Levels](docs/effort-levels.md) | Effort triage: `fast`, `standard`, `high`, `max` -- controls sprint count, density, and review rigor |
| [Epic Format](docs/epic-format.md) | Epic file syntax: global directives, sprint blocks, validation rules, sizing guidelines |
| [AI Engines](docs/engines.md) | Codex, Claude, and Ollama engine configuration, mixing engines, model overrides |
| [Sprint Execution](docs/sprint-execution.md) | Agent iteration loop, prompt assembly, progress tracking, promise tokens |
| [Sanity Checks](docs/sanity-checks.md) | Check primitives, file format, outcome matrix, graceful degradation |
| [Alignment](docs/alignment.md) | Alignment loop mechanics, configuration, diagnostics |
| [Sprint Code Review](docs/sprint-audit.md) | Post-sprint single-session code review, severity classification, iterative fix loop |
| [Build Audit](docs/build-audit.md) | Final holistic codebase audit after epic completion, iterative remediation |
| [Sprint Review](docs/sprint-review.md) | Dynamic mid-build review, replanning, deviation specs, safeguards |
| [Docker Support](docs/docker.md) | Docker Compose lifecycle, health checks, sprint scoping |
| [Preflight Checks](docs/preflight.md) | Pre-build validation, required tools, custom commands |
| [Planning Mode](docs/planning-mode.md) | Non-code project support: documents, analyses, strategies |
| [Writing Mode](docs/writing-mode.md) | Human-language content: books, guides, reports, documentation |
| [Media Assets](docs/media-assets.md) | Optional `media/` directory for images, PDFs, fonts, and other build assets |
| [Supplementary Assets](docs/supplementary-assets.md) | Optional `assets/` directory for text reference documents read during plan generation |
| [GitHub Issues](docs/github-issues.md) | Use a GitHub issue URL as the task definition via `--gh-issue` |
| [User Prompt](docs/user-prompt.md) | Injecting directives, prompt hierarchy, persistence |
| [Project Structure](docs/project-structure.md) | Directory layout, generated artifacts, file reference |
| [Terminal Output](docs/terminal-output.md) | Status banners, verbose mode, log format |
| [Triage](docs/triage.md) | Complexity classification with interactive confirmation — controls whether full prepare runs |
| [Git Strategy](docs/git-strategy.md) | Branch and worktree isolation strategies for builds |
| [Self-Improvement](docs/self-improvement.md) | Automated self-improvement pipeline: roadmap, orchestrator, planning, build, alignment |
| [Build Monitoring](docs/monitor.md) | Real-time monitoring: enriched event stream, dashboard, log tail, NDJSON output |
| [Observer](docs/observer.md) | Metacognitive layer: event stream, identity, wake-ups, effort-level gating |
| [Copilot](docs/copilot.md) | Parallel persistent agent that monitors a build and intervenes (fry-source bug fixes, artifact remediation, build restarts) |
| [Consciousness](docs/consciousness.md) | Experience synthesis and identity pipeline |
| [Codebase Awareness](docs/codebase-awareness.md) | Existing codebase detection, scanning, memories, and pipeline integration |
| [Agent Foundation](docs/agent.md) | Agent invocation, artifact schema, system prompt generation |
| [Build Steering](docs/steering.md) | Mid-build human intervention: directives, holds, pauses, graceful exits |

## License

See repository for license information.
