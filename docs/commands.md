# CLI Commands

```
fry [command] [flags]
```

When invoked without a subcommand, `fry` is equivalent to `fry run`.

## `fry config`

Read or write repo-local Fry settings stored in `.fry-config/config.json`.

```
fry config <get|set> ...
```

Only one setting exists today:

| Key | Values | Purpose |
|---|---|---|
| `engine` | `codex`, `claude`, `ollama` | Default self-improve engine for this repository |

This setting is currently used by the self-improvement orchestrator. It does
not change the default engine resolution for normal `fry run` or `fry prepare`
invocations; those still use `--engine`, `@engine`, `FRY_ENGINE`, and the built-in defaults.

### `fry config get`

```
fry config get engine [--project-dir <path>]
```

Prints the configured value, or a blank line if unset.

### `fry config set`

```
fry config set engine <codex|claude|ollama> [--project-dir <path>]
```

Writes `.fry-config/config.json`, creating `.fry-config/` if needed.

### Examples

```bash
fry config get engine
fry config set engine codex
fry config set engine claude --project-dir /path/to/project
```

## `fry run`

Execute sprints from an epic file.

```
fry run [epic.md] [start] [end] [flags]
```

All arguments are optional. With no arguments, Fry uses `.fry/epic.md` and runs all sprints.

### Positional Arguments

Positional arguments are order-dependent: the epic file must come first, followed by sprint numbers.

| Position | Argument | Description |
|---|---|---|
| 1 | `epic_file` | Path to the epic definition file (default: `.fry/epic.md`). Auto-generated via `fry prepare` if the file doesn't exist. |
| 2 | `start_sprint` | First sprint to run (default: 1) |
| 3 | `end_sprint` | Last sprint to run (default: last sprint in epic) |

To specify a sprint range with positional arguments, you must also specify the epic file:

```bash
fry run epic.md 3 5       # Correct: run sprints 3-5
fry run 3 5               # Wrong: treats "3" as the epic filename
```

Alternatively, use `--sprint` to avoid specifying the epic file:

```bash
fry run --sprint 3         # Start from sprint 3 (uses .fry/epic.md)
```

### Flags

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to operate on (default: current directory) |
| `--engine <codex\|claude\|ollama>` | AI engine to use (default: claude) |
| `--fallback-engine <codex\|claude\|ollama>` | Sticky fallback engine to use if the primary engine exhausts retries on a transient failure. Default: Claude fails over to Codex, Codex fails over to Claude. See [AI Engines](engines.md#cross-engine-failover). |
| `--no-engine-failover` | Disable cross-engine failover and stay on the selected engine. |
| `--effort <fast\|standard\|high\|max>` | Effort level — controls sprint count, density, and review rigor (default: auto-detect). Ignored with a warning if the epic already has an `@effort` directive. See [Effort Levels](effort-levels.md). |
| `--model <model>` | Override the agent model for sprints, alignment, review, and replan sessions (e.g. `opus[1m]`, `sonnet`, `haiku`). Takes precedence over `@model` in the epic and the effort-based automatic model selection. Use this to pair a lower effort level (fewer sprints) with a more capable model. |
| `--mode <software\|planning\|writing>` | Execution mode (default: `software`). `planning` generates structured documents; `writing` generates human-language content (books, guides, reports). See [Planning Mode](planning-mode.md), [Writing Mode](writing-mode.md). |
| `--prepare-engine <codex\|claude\|ollama>` | Engine for auto-generating the epic (defaults to `--engine`, `FRY_ENGINE`, or claude) |
| `--planning` | Alias for `--mode planning`. Kept for backwards compatibility. |
| `--user-prompt <text>` | Top-level directive injected into every sprint prompt. When no `plan.md` or `executive.md` exists, bootstraps the entire project from this prompt (interactive review). |
| `--user-prompt-file <path>` | Path to a file containing the user prompt. Alternative to `--user-prompt` for longer prompts. Cannot be combined with `--user-prompt`. |
| `--gh-issue <url>` | GitHub issue URL to use as the task definition. Requires authenticated `gh` CLI access to the issue host. Cannot be combined with `--user-prompt` or `--user-prompt-file`. See [GitHub Issues](github-issues.md). |
| `--review` | Enable sprint review between sprints. Instructs the epic generator to include `@review_between_sprints`. Also offered interactively during the adjust flow for standard/high effort builds. Max effort auto-enables review. |
| `--no-review` | Disable sprint review even if the epic enables `@review_between_sprints` |
| `--yes` / `-y` | Auto-accept all interactive confirmation prompts (triage, project overview, executive bootstrap). For CI/CD and AI agent automation. |
| `--confirm-file` | Use file-based interactive prompts instead of stdin. Writes prompts to `.fry/confirm-prompt.json`, waits for responses at `.fry/confirm-response.json`. For AI agent automation where the user should review and confirm each step. |
| `--no-project-overview` | Skip interactive confirmations (triage classification and project overview) |
| `--no-audit` | Disable sprint and build audits for this run |
| `--no-observer` | Disable the observer metacognitive layer (event stream and wake-ups). Observer is also disabled at `fast` effort and during `--dry-run`. See [Observer](observer.md). |
| `--copilot[=<engine>]` | Enable the build copilot. Default engine is `claude`. Use `--copilot=codex` for codex. The copilot runs independently — it installs its own cron and survives fry crashes. Auto-enabled at `--effort=max` unless `--no-copilot` is set. See [Copilot](copilot.md). |
| `--copilot-interval <duration>` | Copilot wake interval (default `10m`, range `1m`–`1h`). |
| `--copilot-fry-source <path>` | Path to the fry source tree the copilot may edit. Default: auto-detected from the running binary, then `$FRY_SOURCE_DIR`, then standard locations. |
| `--copilot-model <model>` | Override the copilot agent model. |
| `--copilot-passive` | Disable copilot interventions; events and final summary only. |
| `--copilot-print-summary` | Print the copilot final summary to stdout when fry exits. |
| `--no-copilot` | Explicitly disable the copilot. Overrides the `--effort=max` auto-enable rule. |
| `--simulate-review <verdict>` | Test the review pipeline without LLM calls. Verdict: `CONTINUE` or `DEVIATE` |
| `--verbose` | Stream full agent output to terminal (default: status banners only) |
| `--sprint <N>` | Start from sprint N. Alternative to the positional start sprint argument — no need to specify the epic file path. Cannot be combined with positional sprint arguments. |
| `--resume` | Resume a failed sprint: skip iterations, go straight to sanity checks + alignment with boosted attempts (2x normal, minimum 6). Preserves existing progress for full context. Only applies to the first sprint in the range; subsequent sprints run normally. |
| `--continue` | Auto-detect where a previous build left off and resume. Uses an LLM agent to analyze `.fry/` build artifacts, determine the next sprint, and decide whether to resume or start fresh. Automatically restores the build mode (`software`, `planning`, or `writing`) from the previous run unless `--mode` is explicitly passed. If both the original project and a persisted worktree contain build state, Fry compares them and uses the more advanced/newer state instead of blindly trusting the persisted worktree. Cannot be combined with `--sprint`, `--resume`, or positional sprint arguments. |
| `--simple-continue` | Resume from the first incomplete sprint without LLM analysis. Scans sprint completion markers in the build state and resumes from the first sprint without a marker. Lightweight alternative to `--continue` — no LLM call, no cost. Cannot be combined with `--continue`, `--resume`, or `--sprint`. |
| `--full-prepare` | Skip triage and run the full prepare pipeline when no epic exists. Equivalent to the pre-triage behavior. See [Triage](triage.md). |
| `--triage-only` | Run the triage classifier and exit without generating any artifacts (no epic, sanity checks, or AGENTS.md). Prints the classification result. Cannot be combined with `--full-prepare`, `--continue`, `--resume`, or `--simple-continue`. See [Triage](triage.md). |
| `--git-strategy <auto\|current\|branch\|worktree>` | Git isolation strategy (default: `auto`). `auto` lets triage decide (complex -> worktree, simple/moderate -> branch). `current` works on the current branch (previous behavior). See [Git Strategy](git-strategy.md). |
| `--branch-name <name>` | Explicit branch name for `branch` or `worktree` strategies. Overrides the auto-generated `fry/<slug>` name. |
| `--always-verify` | Force sanity checks, alignment, and audit to run regardless of effort level or triage complexity. Generates heuristic sanity checks if none exist. Useful for CI/CD and automated builds. |
| `--sarif` | Write `build-audit.sarif` in SARIF 2.1.0 format alongside `build-audit.md`. Only written when the build audit runs. See [Build Audit](build-audit.md). |
| `--json-report` | Write `.fry/build-report.json` containing sprint results, sanity check outcomes, and timing data. |
| `--show-tokens` | Print a per-sprint token usage table to stderr at the end of the run. When using the Ollama engine, token counts are always 0 (Ollama does not report token usage); the column displays `-`. |
| `--telemetry` | Enable experience upload to the consciousness API for this run. See [Consciousness](consciousness.md). |
| `--no-telemetry` | Disable experience upload. Takes precedence over `--telemetry` if both set. |
| `--no-color` | Disable colored terminal output. Color is also disabled by setting the `NO_COLOR` environment variable or when stdout is not a TTY. |
| `--mcp-config <path>` | Path to MCP server configuration file (Claude engine only). See [Engines: MCP](engines.md#mcp-server-configuration). |
| `--dry-run` | Parse epic and show plan without running anything |

### Examples

```bash
fry                                               # Run all sprints (.fry/epic.md default)
fry --dry-run                                     # Validate and preview
fry --triage-only --user-prompt "add a CLI flag"  # Preview triage classification only
fry --engine claude                               # Run all sprints with Claude Code
fry --engine claude --fallback-engine codex      # Prefer Claude, pin to Codex if Claude hits a transient failure
fry --gh-issue https://github.com/yevgetman/fry/issues/74  # Start directly from a GitHub issue
fry --effort fast                                  # Quick task: 1-2 sprints, minimal overhead
fry --effort standard --engine claude             # Moderate task: 2-4 sprints
fry --effort max --engine claude                  # Maximum rigor: extended prompts, thorough reviews
fry run epic.md                                   # Run all sprints with Codex
fry run epic-phase2.md                            # Use a custom epic filename
fry run epic.md 4                                 # Resume from sprint 4
fry run --sprint 4                                # Same, without specifying the epic file
fry run epic.md 4 4                               # Run only sprint 4
fry run epic.md 3 5                               # Run sprints 3 through 5
fry run --resume --sprint 4                        # Resume failed sprint 4 (sanity checks + alignment only)
fry run --resume --sprint 4 --planning             # Resume with planning mode
fry run --continue                                # Auto-detect and resume from where you left off
fry run --continue --dry-run                      # Preview what --continue would do
fry run --continue --engine claude                # Resume with a different engine
fry --verbose                                     # Print agent output to terminal
fry --prepare-engine codex                        # Override: use Codex for generation, Codex for build
fry --planning                                    # Planning project (documents, not code) — uses Claude for both stages
fry --mode writing --user-prompt "Write a guide"  # Writing project (books, guides) — uses Claude for both stages
fry --user-prompt "focus on backend API, skip frontend"
fry --user-prompt "build a todo app" --engine claude  # Start from just a prompt
fry --user-prompt-file ./prompt.txt --engine claude   # Load prompt from a file
fry -y --user-prompt "add rate limiting"              # Fully automated: no interactive prompts
fry --no-project-overview                             # Skip triage confirmation and project overview
fry --git-strategy worktree                       # Force worktree isolation
fry --git-strategy branch --branch-name feat/auth # Branch with explicit name
fry --git-strategy current                        # Work on current branch (previous behavior)
fry --always-verify                               # Force sanity checks+alignment+audit on all tasks
fry --model opus[1m] --effort standard             # Standard sprint count with frontier model
fry --no-observer                                 # Disable the observer metacognitive layer
fry --project-dir /path/to/project                # Operate on a different project
FRY_ENGINE=claude fry                             # Set engine via environment variable
```

---

## `fry team`

Operate Fry's standalone tmux-backed team runtime. This runtime is intentionally independent from `fry run`: it gives you a durable parallel worker subsystem first, with explicit operator control and on-disk state under `.fry/team/<team-id>/...`.

### `fry team start`

```
fry team start [flags]
```

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to operate on (default: current directory) |
| `--team <id>` | Explicit team identifier (default: auto-generated timestamped ID) |
| `--workers <N>` | Number of worker hosts to create (default: `1`) |
| `--role <role>` | Worker role list. Repeat or comma-separate to cycle roles across workers (default: `executor`) |
| `--task-file <path>` | Optional JSON task file loaded immediately after startup |
| `--git-isolation <shared\|per-worker-worktree>` | Team workdir mode. `shared` executes in the project directory. `per-worker-worktree` allocates one git worktree per worker when possible. |
| `--executable-path <path>` | Override the fry executable path used inside tmux worker hosts |

Behavior:

- Creates `.fry/team/<team-id>/...` with config, task, worker, lock, and artifact directories
- Starts a tmux session with one leader window plus one worker window per worker
- Persists worker identities, statuses, and heartbeats
- Emits team lifecycle events into both `.fry/team/<team-id>/events.jsonl` and the shared observer stream consumed by `fry events`
- Marks the team as the active runtime so `fry status` and `fry monitor` can surface it

Examples:

```bash
fry team start --workers 3 --role executor --task-file ./tasks.json
fry team start --team auth-refactor --workers 2 --role executor --role tester
fry team start --workers 4 --git-isolation per-worker-worktree
```

### `fry team assign`

```
fry team assign --task-file ./tasks.json [--team <id>]
```

Loads tasks from a JSON file into the team runtime. The file may be either a JSON array or an object with a top-level `tasks` array.

Supported task fields:

- `id`
- `title`
- `description`
- `role`
- `priority`
- `command`
- `blocked_by`
- `acceptance_hints`

Tasks are claimed durably by workers. Workers only claim tasks whose `role` matches their own role, and they will wait until `blocked_by` dependencies are complete before claiming a task.

Example:

```json
{
  "tasks": [
    {
      "id": "001",
      "title": "Generate fixtures",
      "role": "executor",
      "command": "make fixtures"
    },
    {
      "id": "002",
      "title": "Run regression suite",
      "role": "tester",
      "blocked_by": ["001"],
      "command": "go test ./..."
    }
  ]
}
```

### `fry team status`

```
fry team status [--team <id>] [--json]
```

Shows the active team by default, or a specific team when `--team` is passed. The human-readable view summarizes:

- team ID and lifecycle status
- tmux session name
- task counts by state
- worker counts by state
- integrated output directory, when available
- per-worker role/status lines

### `fry team pause`

```
fry team pause [--team <id>]
```

Pauses new task claiming. Running tasks are allowed to finish; idle workers remain alive and heartbeat while waiting for a resume.

### `fry team resume`

```
fry team resume [--team <id>] [--executable-path <path>]
```

Resumes task claiming and runs liveness reconciliation first:

- stale or dead workers are marked dead
- owned tasks are requeued
- missing worker hosts are restarted in tmux

### `fry team scale`

```
fry team scale --add <N> [--team <id>]
fry team scale --remove <worker-id> [--team <id>]
```

Scale-up creates new worker hosts. Scale-down removes idle workers immediately or marks active workers as draining so they stop taking new work after their current task completes.

### `fry team attach`

```
fry team attach [--team <id>]
```

Attaches your terminal to the underlying tmux session for live inspection.

### `fry team shutdown`

```
fry team shutdown [--team <id>] [--force]
```

Gracefully shuts down the runtime. Without `--force`, Fry refuses to shut down while tasks are still running. With `--force`, the tmux session is terminated immediately and the team is marked shutdown.

### Team output and integration

- Worker logs are written under `.fry/team/<team-id>/artifacts/`
- In `shared` mode, the canonical output is the project directory itself
- In `per-worker-worktree` mode, successful worker tasks are auto-committed in their worktrees and Fry produces an integrated output worktree under `.fry/team/<team-id>/artifacts/integrated-output/` once all tasks reach a terminal state
- Merge results are summarized in `.fry/team/<team-id>/artifacts/merge-report.md`

### Related commands

- `fry status` includes the active team summary when present
- `fry monitor` shows the team summary in dashboard mode
- `fry events` streams team lifecycle events alongside normal build events

---

## `fry exit`

Request a graceful stop for a running build. Fry settles the current safe seam,
writes `.fry/resume-point.json`, and leaves the build in a paused state for
`fry run --continue`, `fry run --simple-continue`, or `fry run --resume --sprint N`.

```
fry exit [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to operate on (default: current directory) |
| `--no-color` | Disable colored terminal output |
| `--verbose` | Enable verbose logging |

### Behavior

- Writes a structured exit request to `.fry/exit-request.json`
- The running build honors that request at the next safe checkpoint:
  current sprint iteration, alignment attempt boundary, sprint-audit seam, sprint boundary after compaction (including holds and reviews), or the final build-audit/build-summary seam before finalization
- When the request is settled, Fry writes `.fry/resume-point.json` with the exact phase, sprint, verdict, reason, and recommended resume command
- `fry run --continue` and `fry run --simple-continue` read the structured resume point before falling back to artifact heuristics
- On worktree builds, `fry exit` writes the request into the canonical worktree state directory automatically
- If the build is already paused, Fry prints the stored resume command instead of writing a second request

### Examples

```bash
fry exit                                    # Gracefully stop the active build
fry exit --project-dir /path/to/project     # Stop a build in another directory
```

---

## `fry audit`

Run a standalone AI-powered build-level audit on the current project. Works on any codebase -- a completed Fry build, a partially built project, or code that was never built with Fry. Finds issues, attempts fixes, and re-audits until clean or the cycle limit is reached.

Results are written to `build-audit.md` in the project root.

### Flags

| Flag | Description |
|---|---|
| `--engine <claude\|codex\|ollama>` | Execution engine (default: `claude`) |
| `--fallback-engine <codex\|claude\|ollama>` | Sticky fallback engine for transient audit-engine failures. See [AI Engines](engines.md#cross-engine-failover). |
| `--no-engine-failover` | Disable cross-engine failover and stay on the selected engine. |
| `--effort <fast\|standard\|high\|max>` | Effort level controlling audit rigor — higher effort means more audit cycles and fix iterations (default: `high`) |
| `--model <name>` | Override the agent model (e.g. `opus`, `sonnet`, `haiku`) |
| `--mode <software\|planning\|writing>` | Audit mode — controls the audit criteria (code quality vs content quality). Default: `software`. |
| `--sarif` | Write `build-audit.sarif` in SARIF 2.1.0 format alongside `build-audit.md` |
| `--mcp-config <path>` | Path to MCP server configuration file (Claude engine only) |

### Examples

```bash
fry audit                                    # Audit current project (high effort, claude)
fry audit --effort max                       # Maximum rigor audit
fry audit --effort fast                      # Quick single-pass audit
fry audit --engine codex                     # Audit with Codex engine
fry audit --engine claude --fallback-engine codex  # Prefer Claude, fail over to Codex on transient failures
fry audit --sarif                            # Also write SARIF output for tooling
fry audit --mode writing                     # Audit content quality (writing mode criteria)
fry audit --project-dir /path/to/project     # Audit a different directory
```

### Behavior

- If `.fry/epic.md` exists, the audit uses it for context (epic name, effort directives, audit model). CLI flags override epic directives.
- If no `.fry/` artifacts exist, the audit creates a minimal synthetic context and runs against the raw codebase.
- The audit follows the same two-level loop as the build audit: discover issues, fix, verify, re-audit. See [Build Audit](build-audit.md) for details on the loop mechanics.
- On completion, results are committed via git checkpoint.
- Non-zero exit code if blocking (CRITICAL/HIGH) findings remain unresolved.

---

## `fry prepare`

Generates `.fry/AGENTS.md`, `.fry/epic.md`, and `.fry/verification.md` from your plan. Requires at least one of `plans/plan.md`, `plans/executive.md`, `--user-prompt`, `--user-prompt-file`, or `--gh-issue`.

```
fry prepare [epic_filename] [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to operate on (default: current directory) |
| `--engine <codex\|claude\|ollama>` | AI engine for generation (default: claude, or `FRY_ENGINE`) |
| `--fallback-engine <codex\|claude\|ollama>` | Sticky fallback engine for transient prepare-engine failures. See [AI Engines](engines.md#cross-engine-failover). |
| `--no-engine-failover` | Disable cross-engine failover and stay on the selected engine. |
| `--effort <fast\|standard\|high\|max>` | Effort level — controls sprint count and density in the generated epic (default: auto-detect). See [Effort Levels](effort-levels.md). |
| `--user-prompt <text>` | Top-level directive to guide artifact generation. Can bootstrap the entire project when no plan files exist (interactive review). |
| `--user-prompt-file <path>` | Path to a file containing the user prompt. Alternative to `--user-prompt` for longer prompts. Cannot be combined with `--user-prompt`. |
| `--gh-issue <url>` | GitHub issue URL to use as the task definition. Requires authenticated `gh` CLI access to the issue host. Cannot be combined with `--user-prompt` or `--user-prompt-file`. See [GitHub Issues](github-issues.md). |
| `--mode <software\|planning\|writing>` | Execution mode (default: `software`). See [Planning Mode](planning-mode.md), [Writing Mode](writing-mode.md). |
| `--validate-only` | Check that the epic is valid, then exit |
| `--review` | Enable sprint review between sprints. Instructs the epic generator to include `@review_between_sprints`. |
| `--yes` / `-y` | Auto-accept all interactive confirmation prompts (project overview, executive bootstrap). |
| `--confirm-file` | Use file-based interactive prompts (`.fry/confirm-prompt.json` / `.fry/confirm-response.json`) instead of stdin. |
| `--no-project-overview` | Skip the interactive project overview confirmation |
| `--planning` | Alias for `--mode planning`. Kept for backwards compatibility. |
| `--mcp-config <path>` | Path to MCP server configuration file (Claude engine only). See [Engines: MCP](engines.md#mcp-server-configuration). |
| `--no-color` | Disable colored terminal output |
| `--verbose` | Stream full agent output to terminal (default: status banners only) |

All artifacts are **always regenerated** (overwritten) on each run.

### Generation Steps

| Step | Condition | Output |
|---|---|---|
| Bootstrap | Both files missing, `--user-prompt` or `--gh-issue` provided | Generates `plans/executive.md` from the resolved prompt (interactive review) |
| Step 0 | `plan.md` missing, `executive.md` exists | Generates `plans/plan.md` from executive context |
| Project Overview | `plan.md` exists, `--no-project-overview` not set | Displays AI-generated project overview for user confirmation |
| Step 1 | Always | Generates `.fry/AGENTS.md` (numbered operational rules) |
| Step 2 | Always | Generates `.fry/epic.md` (sprint definitions) |
| Step 3 | Always | Generates `.fry/verification.md` (sanity check primitives) |

### Examples

```bash
fry prepare                                        # Generate all with Claude (default)
fry prepare --engine codex                         # Generate all with Codex
fry prepare --engine claude --fallback-engine codex  # Prefer Claude, pin to Codex if Claude is transiently unavailable
fry prepare --effort fast                          # Generate a compact 1-2 sprint epic
fry prepare --effort max --engine claude           # Generate with maximum detail
fry prepare epic-phase1.md                         # Custom epic filename
fry prepare --project-dir /path                    # Operate on a different project
fry prepare --user-prompt "no ORMs, use raw SQL only"
fry prepare --user-prompt "build a blog engine" --engine claude  # Bootstrap from prompt
fry prepare --user-prompt-file ./requirements.txt --engine claude # Prompt from file
fry prepare --gh-issue https://github.com/yevgetman/fry/issues/74 # Bootstrap from a GitHub issue
fry prepare --mode writing --user-prompt "Write a guide to Go concurrency"  # Writing mode
fry prepare --no-project-overview                   # Skip project overview confirmation
fry prepare --validate-only                        # Validate existing epic only
```

---

## `fry replan`

Replan an epic after a deviation. Used by the dynamic sprint review system, or invoked directly.

```
fry replan <deviation_spec> [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--epic <path>` | Epic file to update (default: `.fry/epic.md`) |
| `--completed <N>` | Completed sprint count |
| `--max-scope <N>` | Maximum deviation scope (default: 3) |
| `--engine <codex\|claude\|ollama>` | Replanning engine |
| `--fallback-engine <codex\|claude\|ollama>` | Sticky fallback engine for transient replanning failures. See [AI Engines](engines.md#cross-engine-failover). |
| `--no-engine-failover` | Disable cross-engine failover and stay on the selected engine. |
| `--model <model>` | Model override |
| `--mcp-config <path>` | Path to MCP server configuration file (Claude engine only). See [Engines: MCP](engines.md#mcp-server-configuration). |
| `--dry-run` | Preview replanning prompt without executing |
| `--verbose` | Stream full agent output to terminal (default: status banners only) |

---

## `fry clean`

Archive build artifacts from `.fry/` and root-level build outputs (`build-audit.md`, `build-summary.md`) into a timestamped folder under `.fry-archive/`. Persistent artifacts (`.fry-config/codebase.md`, `.fry-config/file-index.txt`, `.fry-config/codebase-memories/`) live in `.fry-config/` which is not affected by archive/clean.

```
fry clean [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--force` | Skip the confirmation prompt |
| `--yes`, `-y` | Auto-accept confirmation prompts |
| `--project-dir <path>` | Project directory to operate on (default: current directory) |

### Behavior

1. Checks if a build is currently running (lock file active) and warns if so.
2. Prompts for confirmation unless `--force` or `--yes` is passed.
3. Moves `.fry/` into `.fry-archive/.fry--build--YYYYMMDD-HHMMSS`.
4. Moves `build-audit.md` and `build-summary.md` from the project root into the same archive folder (skips silently if they don't exist).
5. Restores persistent artifacts (codebase index files and memories) into a fresh `.fry/`.

Auto-archiving also happens automatically after a successful full build (all sprints from 1 to the last). The `fry clean` command is for manual archiving between builds.

To completely remove all fry artifacts instead of archiving them, use `fry destroy`.

### Examples

```bash
fry clean                              # Archive with confirmation prompt
fry clean --force                      # Archive without confirmation
fry clean --project-dir /path/to/proj  # Archive a different project
```

---

## `fry destroy`

Completely remove all fry-generated directories and files as if fry was never run. Unlike `fry clean` which archives build artifacts and preserves codebase index files, `fry destroy` wipes everything.

```
fry destroy [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--force` | Skip the confirmation prompt |
| `--yes`, `-y` | Auto-accept confirmation prompts |
| `--project-dir <path>` | Project directory to operate on (default: current directory) |

### What gets removed

| Artifact | Description |
|---|---|
| `.fry/` | All build artifacts, codebase index, memories |
| `.fry-archive/` | All archived builds |
| `.fry-worktrees/` | All fry-managed git worktrees |
| `plans/` | Plan directory and all plan files |
| `assets/` | Assets directory |
| `media/` | Media directory |
| `build-audit.md` | Root-level build audit |
| `build-summary.md` | Root-level build summary |
| `build-audit.sarif` | Root-level SARIF audit |

Only artifacts that exist are listed and removed. If no fry artifacts exist, the command reports nothing to destroy.

### Behavior

1. Checks if a build is currently running (lock file active) and warns if so.
2. Lists all fry artifacts that exist in the project directory.
3. Prompts for confirmation unless `--force` or `--yes` is passed.
4. Permanently deletes all listed artifacts.

### Examples

```bash
fry destroy                              # Destroy with confirmation prompt
fry destroy --force                      # Destroy without confirmation
fry destroy -y --project-dir /path/to/proj  # Destroy a different project
```

---

## `fry init`

Scaffold the fry project structure. In an empty directory, creates standard scaffolding. In an existing project, also runs a structural codebase scan.

```
fry init [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to operate on (default: current directory) |
| `--engine <name>` | Override engine for semantic codebase scan (`claude`, `codex`, `ollama`). Default: auto-resolved via `FRY_ENGINE` or config default. |
| `--fallback-engine <codex\|claude\|ollama>` | Sticky fallback engine for transient semantic-scan failures. See [AI Engines](engines.md#cross-engine-failover). |
| `--no-engine-failover` | Disable cross-engine failover and stay on the selected engine. |
| `--heuristic-only` | Skip semantic LLM scan; only run structural heuristics (file tree, language detection, dependency parsing). |
| `--force` | Force re-index even if codebase index files already exist. |

### Behavior

**All directories:**

1. Creates `plans/`, `assets/`, and `media/` directories if they don't exist.
2. Writes `plans/plan.example.md` with a starter template for reference.
3. Initializes a git repository if one doesn't exist.
4. Adds `.fry/`, `.fry-archive/`, `.env`, `.DS_Store`, and `.fry-worktrees/` to `.gitignore`.

**Existing projects** (auto-detected via git history, project markers, or file count):

5. Runs a structural scan: file tree, language/framework detection, dependency parsing, entry point identification, git history analysis.
6. Writes `.fry-config/file-index.txt` with a human-readable file index and project stats.
7. Prints a scan summary (files, languages, frameworks, dependencies, git commits).
8. Runs a semantic scan using a Sonnet-class LLM to generate `.fry-config/codebase.md` — a comprehensive document describing the project's architecture, conventions, key files, dependencies, and gotchas. Use `--heuristic-only` to skip this step.

When `.fry-config/codebase.md` exists, it is automatically used by:
- **Sprint prompts** — injected as Layer 0.5 (CODEBASE CONTEXT) before the project context
- **Sprint audit/fix/build-audit prompts** — included as architecture and conventions context for audit remediation
- **Prepare pipeline** — included in plan, epic, and sanity check generation so sprints are decomposed with awareness of existing code
- **Triage classification** — included in complexity assessment to account for existing code patterns

`fry init` does **not** create `plans/plan.md`. You write that yourself using `plan.example.md` as a reference, or provide a `--user-prompt` to `fry prepare` / `fry run` and fry will generate the plan for you.

### Composability

`fry init` is composable — running it multiple times is safe and efficient. If both `.fry-config/file-index.txt` and `.fry-config/codebase.md` already exist (from a prior init), the structural and semantic scans are skipped. Directory scaffolding and git initialization still run (both are already idempotent).

Use `--force` to re-index even when index files already exist.

### Existing project detection

A directory is considered an existing project when **any** of these hold:
- Git history has more than 1 commit (beyond fry init's own initial commit)
- A known project marker exists (`go.mod`, `package.json`, `Cargo.toml`, `requirements.txt`, `pyproject.toml`, `Gemfile`, `pom.xml`, `build.gradle`, `CMakeLists.txt`, `composer.json`, `mix.exs`, `Package.swift`, `pubspec.yaml`, `*.sln`)
- The directory contains more than 10 non-hidden files

### Examples

```bash
fry init                                # Initialize in the current directory
fry init --project-dir /path/to/proj    # Initialize a different directory
cd existing-project && fry init         # Scan existing codebase and scaffold fry
fry init --engine claude                # Override engine for semantic scan
fry init --engine claude --fallback-engine codex  # Prefer Claude, pin to Codex if the scan fails transiently
fry init --heuristic-only               # Structural scan only, no LLM call
fry init --force                        # Re-index even if already indexed
```

---

## `fry status`

Show the current build state without making an LLM call. Reads `.fry/` artifacts and displays a summary of completed sprints, active/partial sprint work, environment checks, and deferred failures.

```
fry status [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--project-dir <path>` | Project directory to inspect (default: current directory) |
| `--json` | Output build state as JSON for agent consumption |
| `--runs` | List all stored run snapshots in `.fry/runs/` |
| `--run <id>` | Show detailed status for a specific run (accepts full ID or timestamp suffix) |
| `--consciousness` | Show local consciousness session health (checkpoints, parse failures, uploads, resumes) |
| `--consciousness-remote` | Show remote consciousness pipeline stats (memory count, transmutation, reflection) |

### Behavior

- If a `.fry/epic.md` exists, prints a full build state report including sprint completion, environment readiness, deferred failures, and deviation count.
- `--consciousness` reads project-local runtime state from `.fry/consciousness/` and reports checkpoint persistence, parse failures, checkpoint distillation health, upload queue state, and resume count.
- `--consciousness-remote` queries the hosted consciousness API for global memory-store statistics.
- If no active build exists, scans for archived builds in `.fry-archive/` and worktree builds in `.fry-worktrees/`, displaying a summary of each (epic name, sprint progress, exit reason). Shows up to 10 most recent archives.
- If a worktree strategy is persisted but the worktree directory is missing, prints a message suggesting `fry run --continue`.
- `--runs` lists all per-run status snapshots in `.fry/runs/`, showing run ID, type (fresh/continue/resume/retry), status, sprint count, start time, and parent run ID. Each build invocation creates an immutable snapshot so that later retries cannot erase earlier run history.
- `--run <id>` shows the full build status for a specific run. Accepts the full run ID (e.g., `run-20260402-100000`) or just the timestamp suffix (e.g., `20260402-100000`).

### Examples

```bash
fry status                              # Show build state for current directory
fry status --project-dir /path/to/proj  # Show build state for a different project
fry status --runs                       # List all run snapshots
fry status --run 20260402-100000        # Show status for a specific run
fry status --consciousness              # Show local consciousness session health
fry status --consciousness-remote       # Show remote consciousness pipeline stats
```

### Example output (no active build)

```
No active build found in /path/to/project

Archived Builds (3)
  2026-03-27 07:46  My Epic      (2/3 sprints passed, software)
  2026-03-26 17:26  Other Epic   (3/4 sprints passed, 1 failed, software)
    Exit: sprint 4 audit failed
  2026-03-25 10:09  Planning     (1/1 sprints passed, planning)

Worktree Builds (1)
  .fry-worktrees/website/  Website Epic  (0/6 sprints passed, 1 failed, software)
    Exit: sprint 1 audit failed

Run 'fry run' to start a new build.
```

---

## `fry monitor`

Real-time build monitoring with enriched event stream. Composes data from events, build status, sprint progress, build logs, and process liveness into a unified view. The dashboard view also shows live sprint-audit progress for the active sprint, including audit stage, cycle/fix counters, targeted issue counts, and compact issue headlines. See [Monitor](monitor.md) for full details.

```
fry monitor [project-dir] [flags]
```

### Positional Arguments

| Position | Argument | Description |
|---|---|---|
| 1 | `project-dir` | Project directory to monitor (default: current directory or `--project-dir` value) |

### Flags

| Flag | Description |
|---|---|
| `--dashboard` | Show a refreshing status dashboard instead of the event stream |
| `--logs` | Tail the active build log |
| `--json` | Output snapshots as NDJSON (one JSON object per line) |
| `--no-wait` | Exit immediately if no active build (default: wait for build to start) |
| `--interval <duration>` | Polling interval (e.g. `1s`, `500ms`; default: `2s`) |
| `--verbose`, `-v` | Include granular synthetic events such as agent deploys, audit cycles, review starts, and observer wake-ups |

### Examples

```bash
fry monitor                                 # Stream enriched events for current directory
fry monitor /path/to/project                # Monitor a build in another directory
fry monitor --dashboard                     # Refreshing dashboard view
fry monitor --logs                          # Tail the active build log
fry monitor --json                          # Machine-readable NDJSON output
fry monitor --verbose                      # Include granular log-derived events
fry monitor --no-wait                       # Exit if no active build
fry monitor --interval 500ms                # Faster polling
```

### Example output (stream mode)

```
[10:00:05]  +0s      build_start       effort=high epic=MyFeature sprints=3
[10:00:15]  +10s     sprint_start      1/3  name=Setup
[10:05:15]  +5m10s   sprint_complete   1/3  status=PASS duration=5m
[10:05:18]  +5m13s   sprint_start      2/3  name=API  [triage -> sprint]
```

Example output (`--verbose`):

```
[10:05:19]  +5m14s   *agent_deploy        2/3  iteration=1 log=sprint2_iter1_20260331_100519.log session=sprint
[10:25:02]  +25m2s   *audit_cycle_start   2/3  cycle=1 log=sprint2_audit1_20260331_102502.log  [sprint -> audit]
[10:25:18]  +25m18s  *audit_fix_start     2/3  cycle=1 fix=1 log=sprint2_auditfix_1_1_20260331_102518.log
[10:31:44]  +31m44s  *observer_wake            log=observer_after_sprint_20260331_103144.log wake=after_sprint
```

Example output (`--dashboard` during sprint audit):

```
Fry Monitor                         PID 12345  10:27:18
────────────────────────────────────────────────────────────────
Epic: My Feature            Engine: claude
Mode: software              Effort: high
Phase: audit                Branch: fry/my-feature
────────────────────────────────────────────────────────────────
Sprint 1/3: Setup .............. PASS
Sprint 2/3: API ................ running      +22m 30s
Sprint 3/3: Polish ............. pending
────────────────────────────────────────────────────────────────
Audit: Sprint 2/3 API
State: Fixing  cycle 2/5  fix 1/4
Issues: targeting 3 issues  (HIGH:1 MODERATE:2)
Working: internal/api/server.go: missing request timeout
Working: internal/auth/token.go: nil dereference on refresh
```

---

## `fry version`

Print the Fry version string.

```bash
fry version
```

---

## `fry identity`

Print Fry's current compiled-in identity.

```
fry identity [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--full` | Print all identity layers including domain-specific files |

### Behavior

- Default: prints core identity + disposition (~1000 tokens total)
- With `--full`: also includes any domain-specific identity files (e.g., iOS/Swift, API backend)
- Identity is compiled into the binary via `go:embed` and updated only by the Reflection process between builds

### Examples

```bash
fry identity                         # Print core identity + disposition
fry identity --full                  # Print all layers including domains
```

---

## `fry reflect`

Trigger the Reflection pipeline on the consciousness API server. Reflection reads all memories, computes effective weights, synthesizes an updated `identity.json` via Claude, prunes decayed memories, and commits the result to the GitHub repository.

```
fry reflect
```

### Behavior

- Sends a POST request to the Worker's `/reflect` endpoint
- Requires at least 50 memories in the store before reflection runs
- If insufficient memories, prints the skip reason and exits successfully
- On success, prints stats: memories considered, integrated, pruned, identity version, commit SHA, and changes
- Times out after 120 seconds

### Examples

```bash
fry reflect                          # Trigger reflection
```

---

## `fry agent`

Agent foundation commands for Fry's conversational interface.

### `fry agent prompt`

```
fry agent prompt
```

Print the agent system prompt, including artifact schema, lifecycle instructions, and identity. This is a read-only command that outputs the prompt used when Fry is invoked as a conversational agent.

### Examples

```bash
fry agent prompt                     # Print the full agent system prompt
```

---

## `fry events`

Stream or list build events from the observer event log (`.fry/observer/events.jsonl`).

```
fry events [flags]
```

### Flags

| Flag | Description |
|---|---|
| `--follow` | Follow the event stream in real time (tail -f style) |
| `--json` | Output events as JSON lines (one JSON object per line) |
| `--project-dir <path>` | Project directory to read events from (default: current directory) |

### Behavior

- Without `--follow`: prints all recorded events and exits
- With `--follow`: tails the event log, printing new events as they are emitted (Ctrl+C to stop)
- Events include build phase transitions, sprint starts/completions, audit cycles, team lifecycle events, and observer wake-ups

### Examples

```bash
fry events                           # List all recorded events
fry events --follow                  # Stream events in real time
fry events --follow --json           # Stream as JSON lines (for programmatic consumption)
```

---

## `fry copilot`

Inspect and control the parallel build copilot session. See [Copilot](copilot.md) for the full feature documentation.

```
fry copilot status [--project-dir <dir>] [--json]
fry copilot attach [--project-dir <dir>] [--print-only]
fry copilot stop   [--project-dir <dir>] [--keep-cron]
fry copilot tail   [--project-dir <dir>] [--follow] [--jsonl]
fry copilot summary [--project-dir <dir>] [--current]
fry copilot list-interventions [--project-dir <dir>]
fry copilot emit-event --type=<type> --data=<json>   # internal helper used BY the agent
```

### Subcommands

| Subcommand | Description |
|---|---|
| `status` | Print current copilot session status: engine, mode, session ID, cron ID, build phase, intervention counts. `--json` for machine-readable output. Exit code 0 active, 1 absent, 2 stale. |
| `attach` | Exec the current terminal into the running copilot session via `claude --resume <session-id>`. Refuses (exit 3) if the copilot is mid-tick. `--print-only` prints the command without exec'ing. |
| `stop` | Request the copilot to exit cleanly. Writes `.fry/copilot/stop-requested`; the next wake reads it, deletes the cron, writes the final summary, and exits. Does NOT stop the build itself. `--keep-cron` skips cron deletion. |
| `tail` | Tail the copilot event log. Default is `events.txt` (human-readable narrative); `--jsonl` for the structured stream. `--follow` for tail -f behavior. |
| `summary` | Print the copilot's `final-summary.md`. `--current` synthesizes an in-progress summary from event counts and intervention reports if no final summary exists yet. |
| `list-interventions` | List all intervention reports in `.fry/copilot/interventions/`. |
| `emit-event` | Append a structured event to both the copilot stream and the canonical observer stream. Used by the copilot agent itself, not by humans. |

### Examples

#### Starting a build with the copilot

```bash
# Force copilot on at any effort level (default engine: claude)
fry run --copilot

# Pick a specific engine (claude is default; codex also supported)
fry run --copilot=claude
fry run --copilot=codex

# Trust triage to decide — copilot auto-enables iff triage classifies as max
fry run

# Force max effort + copilot explicitly (belt and braces)
fry run --effort=max --copilot

# Run at max effort but explicitly opt OUT of copilot
fry run --effort=max --no-copilot

# Custom wake interval (default 10m, range 1m–1h)
fry run --copilot --copilot-interval=15m

# Explicit fry source dir (default: auto-detected from $FRY_SOURCE_DIR or standard locations)
fry run --copilot --copilot-fry-source=/Users/julie/code/fry

# Override the copilot agent model
fry run --copilot --copilot-model=opus[1m]

# Passive mode: events + summary only, no interventions
fry run --copilot --copilot-passive

# Print the final summary to stdout when fry exits
fry run --copilot --copilot-print-summary

# Realistic unattended invocation
fry run --copilot --effort=max -y --json-report --project-dir /path/to/project
```

#### Inspecting and controlling a running copilot

```bash
# Check what the copilot is up to
fry copilot status
fry copilot status --json | jq .manifest.session_id

# Watch live activity
fry copilot tail --follow                  # human-readable narrative log
fry copilot tail --follow --jsonl          # structured event stream

# Open a Claude Code session connected to the copilot
fry copilot attach
fry copilot attach --print-only | bash    # for tmux/IDE integration

# Ask the copilot to wind down without stopping the build
fry copilot stop
fry copilot stop --keep-cron              # leave the cron in place

# Read the final report after the build ends
fry copilot summary
fry copilot summary --current             # synthesize an in-progress summary if no final yet
fry copilot list-interventions            # list every intervention report
```

See [Copilot](copilot.md) for the full feature documentation, intervention procedures, and architecture.

---

## Environment Variables

| Variable | Description |
|---|---|
| `FRY_ENGINE` | Default engine (`codex`, `claude`, or `ollama`). Overridden by `--engine` flag and `@engine` directive. |
