---
name: fry
description: "Fry build orchestration via CLI: start, monitor, steer, stop gracefully, and resume multi-sprint AI builds. Use when: (1) starting or setting up a build, (2) checking build status or progress, (3) reading build logs or audit findings, (4) steering a running build with directives, holds, pauses, or graceful exits, (5) resuming a stopped build, (6) preparing epics or plans, (7) running planning-mode or writing-mode builds. NOT for: editing code directly (Fry's agents do that), running tests manually, or git operations."
metadata:
  {
    "openclaw":
      {
        "emoji": "🍳",
        "requires": { "bins": ["fry"] },
      },
  }
---

# Fry Build Orchestration Skill

Fry is a Go CLI that orchestrates AI agents through multi-sprint build loops.
It decomposes tasks into sprints, runs agents, verifies output with sanity
checks, aligns failures, audits code, and reviews cross-sprint coherence.

You are the conversational interface to Fry. You help the user set up projects,
start builds, monitor progress, interpret results, and steer builds mid-flight.

## Quick Reference

| Action | Command |
|--------|---------|
| Initialize project | `fry init --project-dir <dir>` |
| Prepare artifacts only | `fry prepare --project-dir <dir>` |
| Prepare from GitHub issue | `fry prepare --gh-issue <url> --project-dir <dir>` |
| Validate prepare | `fry prepare --validate-only --project-dir <dir>` |
| Start a build | `fry run -y --project-dir <dir> --json-report --telemetry` |
| Start from GitHub issue | `fry run -y --gh-issue <url> --project-dir <dir> --json-report --telemetry` |
| Check status | `fry status --json --project-dir <dir>` |
| List run history | `fry status --runs --project-dir <dir>` |
| Show specific run | `fry status --run <run-id> --json --project-dir <dir>` |
| Graceful exit | `fry exit --project-dir <dir>` |
| Resume (LLM-driven) | `fry run -y --continue --project-dir <dir> --json-report --telemetry` |
| Resume (lightweight) | `fry run -y --simple-continue --project-dir <dir> --json-report --telemetry` |
| Resume (skip to healing) | `fry run -y --resume --project-dir <dir> --json-report --telemetry` |
| Replan after deviation | `fry replan --project-dir <dir>` |
| Stream events | `fry events --follow --json --project-dir <dir>` |
| Monitor build | `fry monitor --json --project-dir <dir>` |
| Monitor dashboard | `fry monitor --dashboard --project-dir <dir>` |
| Run with copilot | `fry run --copilot --project-dir <dir>` (auto-enabled at `--effort=max`) |
| Copilot status | `fry copilot status --json --project-dir <dir>` |
| Copilot tail | `fry copilot tail --follow --project-dir <dir>` |
| Copilot stop | `fry copilot stop --project-dir <dir>` |
| Copilot summary | `fry copilot summary --project-dir <dir>` |
| Consciousness health | `fry status --consciousness --project-dir <dir>` |
| Trigger reflection | `fry reflect` |
| Print identity | `fry identity` (or `fry identity --full`) |
| Clean/archive build | `fry clean -y --project-dir <dir>` |
| Destroy all artifacts | `fry destroy -y --project-dir <dir>` |
| Triage only | `fry run --triage-only --project-dir <dir>` |
| File-based prompts | `fry run --confirm-file --project-dir <dir>` |
| Standalone audit | `fry audit --project-dir <dir>` |
| Standalone audit (SARIF) | `fry audit --sarif --project-dir <dir>` |
| Dry run | `fry run --dry-run --project-dir <dir>` |
| Start team runtime | `fry team start --workers 3 --project-dir <dir>` |
| Team status | `fry team status --project-dir <dir>` |
| Scale team | `fry team scale --add 2 --project-dir <dir>` |
| Pause team | `fry team pause --project-dir <dir>` |
| Resume team | `fry team resume --project-dir <dir>` |
| Shutdown team | `fry team shutdown --project-dir <dir>` |
| Attach to team | `fry team attach --project-dir <dir>` |
| Assign tasks | `fry team assign --task-file tasks.json --project-dir <dir>` |
| Disable telemetry | `fry run -y --no-telemetry --project-dir <dir>` |

## How to Pass Tasks to Fry

**Let Fry handle its own planning.** Do not generate `plans/plan.md` or
`plans/executive.md` unless the user explicitly asks you to. Fry has its own
triage, prepare, and epic generation pipeline — your job is to pass the task
description and let Fry do the rest.

### GitHub-tracked tasks: prefer `--gh-issue`

When the task already exists as a GitHub issue, pass the issue URL directly:

```bash
fry run -y --project-dir /path/to/project \
  --gh-issue https://github.com/owner/repo/issues/123 \
  --json-report --telemetry
```

Fry will fetch the issue through the authenticated `gh` CLI, persist the fetched
context to `.fry/github-issue.md`, convert it into a top-level directive, and run
its normal triage/prepare/build pipeline from there.

Requirements:
- `gh` must be installed
- The current user must already be authenticated for the issue host (`gh auth login`)
- Do not combine `--gh-issue` with `--user-prompt` or `--user-prompt-file`

### Small tasks: use `--user-prompt`

For straightforward requests, pass the task description directly:

```bash
fry run -y --project-dir /path/to/project \
  --user-prompt "Add rate limiting to the API endpoints" \
  --json-report --telemetry
```

Fry will triage, prepare, and run from the prompt alone. No plan files needed.

### Larger tasks: use `--user-prompt-file`

When the task description is longer or multi-part, write it to a temp file
and pass it via `--user-prompt-file`:

```bash
cat <<'PROMPT' > /tmp/fry-task.md
## Task
Refactor the authentication system to support OAuth2.

## Requirements
- Replace session-based auth with JWT tokens
- Add Google and GitHub OAuth providers
- Migrate existing user sessions
- Update all API middleware
PROMPT

fry run -y --project-dir /path/to/project \
  --user-prompt-file /tmp/fry-task.md \
  --json-report --telemetry
```

### Complex builds: pre-scaffolded artifacts

For complex builds, the user will have already set up `plans/plan.md`,
`plans/executive.md`, `assets/`, and `media/` in the project, or will
explicitly ask you to scaffold them. In this case, just run Fry without
a user prompt — it picks up the plan files automatically:

```bash
fry run -y --project-dir /path/to/project --json-report --telemetry
```

### Decision guide

| Scenario | What to do |
|----------|-----------|
| User gives a GitHub issue URL | Use `--gh-issue` |
| User says "build X" or "fix Y" | Use `--user-prompt` with their request |
| User gives a detailed multi-part task | Write to temp file, use `--user-prompt-file` |
| `plans/plan.md` already exists in project | Run without `--user-prompt` — Fry uses the plan |
| User says "create a plan for..." | Then and only then write `plans/plan.md` |
| User says "set up the executive summary" | Then and only then write `plans/executive.md` |

**Never generate plan.md or executive.md on your own initiative.** These are
user-owned artifacts. If the user hasn't asked for them and they don't exist,
use `--user-prompt` or `--user-prompt-file` instead.

### Supplementary inputs

These directories are user-managed. Only populate them if the user asks:

| Directory | Purpose | What goes in |
|-----------|---------|--------------|
| `assets/` | Text references read in full during prepare | Specs, schemas, requirements, config files (max 512KB/file, 2MB total) |
| `media/` | Binary assets referenced by path | Images, PDFs, fonts, data files (manifest injected, not content) |

### Scaffolding with `fry init`

```bash
fry init --project-dir /path/to/project
```

Creates `plans/`, `assets/`, `media/` directories with a template plan file,
initializes git, and configures `.gitignore` for Fry artifacts.

**Existing project detection:** When run in a directory with an existing codebase
(detected via git history >1 commit, project marker files like `go.mod`/`package.json`,
or >10 non-hidden files), `fry init` automatically runs a structural scan:
- Walks the file tree (respecting `.gitignore`)
- Detects languages, frameworks, entry points, and test directories
- Parses dependency manifests (`go.mod`, `package.json`, `requirements.txt`)
- Analyzes git history (recent commits, frequently changed files, top authors)
- Writes `.fry-config/file-index.txt` with a human-readable index and statistics

On existing projects, `fry init` also runs a **semantic scan** by default using a
Sonnet-class LLM to generate `.fry-config/codebase.md` — a comprehensive document covering
architecture, conventions, key files, dependencies, and gotchas. This document is
injected into sprint prompts as Layer 0.5 context.

Use `--heuristic-only` to skip the semantic scan and only run structural heuristics.
Use `--engine` to override the engine used for the semantic scan.

```bash
fry init --project-dir /path/to/project             # Full scan (structural + semantic)
fry init --heuristic-only --project-dir /path/to/project  # Structural only
```

**Composability:** `fry init` is composable. If `.fry-config/file-index.txt` and
`.fry-config/codebase.md` already exist (from a prior init), scanning is skipped. Use
`--force` to re-index even when index files already exist.

```bash
fry init --force --project-dir /path/to/project  # Force re-index
```

**Pipeline integration:** When `.fry-config/codebase.md` exists, it is automatically used
throughout the build pipeline:
- **Sprint prompts:** Injected as Layer 0.5 (CODEBASE CONTEXT) before executive context
- **Sprint code review / build-audit prompts:** Included as architecture and conventions context during review and remediation
- **Prepare pipeline:** Included in plan, epic, and sanity check generation
- **Triage:** Included in complexity classification
- **File index:** Auto-refreshed on each `fry run` if stale (newer git commits exist)
- **Codebase memories:** After each build, Fry extracts project-specific learnings
  into `.fry-config/codebase-memories/` (Layer 0.75 in sprint prompts). Memories are
  deduplicated across builds, reinforced when confirmed, and compacted from 50+ to ~20
  via LLM when the threshold is exceeded. Memories persist across `fry clean`.

## Prepare Phase

Generate build artifacts without running a build:

```bash
fry prepare --project-dir /path/to/project
```

This runs the triage gate and generates `.fry/epic.md`, `.fry/AGENTS.md`, and
`.fry/verification.md`. Use `--validate-only` to check existing artifacts
without regenerating.

Prepare respects `--effort`, `--mode`, `--user-prompt`, `--gh-issue`, and `--engine` flags.
Use `--full-prepare` to skip triage and run the full 3-step LLM pipeline
regardless of complexity.

## Triage Gate

Before preparing or running, Fry classifies task complexity:

| Classification | LLM calls | Sprints | Git strategy |
|----------------|-----------|---------|--------------|
| SIMPLE | 0 (template) | 1 | branch |
| MODERATE | 0 (template) | 1-2 | branch |
| COMPLEX | 3-4 (full prepare) | Based on plan | worktree |

To inspect triage without building:

```bash
fry run --triage-only --project-dir /path/to/project
```

## Agent Interactive Flows

By default, Fry prompts the user interactively during the prepare phase (triage
confirmation, project overview, executive context). Agents have two options for
handling these prompts instead of auto-accepting with `-y`.

### Option A: Two-step flow (recommended)

Run triage separately, relay the result to the user, then build with their choice:

```bash
# Step 1: Classify the task (instant, non-interactive)
fry run --triage-only --project-dir /path/to/project \
  --user-prompt "Add rate limiting to API endpoints"

# Step 2: Read the triage output and present to the user:
#   "Fry classified this as MODERATE (effort: standard, 2 sprints).
#    Reason: Multi-file change with tests needed.
#    Accept, or would you like to adjust?"

# Step 3: Build with the user's chosen effort level
fry run -y --project-dir /path/to/project \
  --effort standard \
  --user-prompt "Add rate limiting to API endpoints" \
  --json-report --telemetry
```

This is the simplest approach — no PTY, no file polling. The user controls the
effort level and the build runs non-interactively.

### Option B: File-based interactive prompts

For full interactive control (including project overview and executive context
confirmation), use `--confirm-file`. Fry writes prompts to
`.fry/confirm-prompt.json` and waits for responses at `.fry/confirm-response.json`:

```bash
# Launch Fry with file-based prompts (in a subagent)
fry run --confirm-file --project-dir /path/to/project \
  --user-prompt "Add rate limiting to API endpoints" \
  --json-report --telemetry
```

**Prompt file** (`.fry/confirm-prompt.json`) — written by Fry when it needs input:
```json
{
  "type": "triage_confirm",
  "message": "Triage classified task as MODERATE (effort: standard, 2 sprints).",
  "data": {
    "complexity": "MODERATE",
    "effort": "standard",
    "sprints": 2,
    "reason": "Multi-file change with tests needed.",
    "git_strategy": "branch (new branch for this build)"
  },
  "options": ["accept", "adjust", "reject"]
}
```

**Response file** (`.fry/confirm-response.json`) — written by the agent after
relaying to the user:
```json
{"action": "accept"}
```

Or to adjust:
```json
{
  "action": "adjust",
  "adjustments": {"effort": "high", "git_strategy": "worktree"}
}
```

Or to reject (stops the build):
```json
{"action": "reject"}
```

**Prompt types:**

| Type | When | Data fields | Adjustable fields |
|------|------|-------------|-------------------|
| `triage_confirm` | After triage classification | complexity, effort, sprints, reason, git_strategy | complexity, effort, git_strategy |
| `project_overview` | After AI-generated project summary | project_type, goal, expected_output, key_topics, effort_estimate | user_prompt (additional text), effort, enable_review |
| `executive_context` | After AI-generated executive context | executive_text | (accept or reject only) |

**Flow:** poll `.fry/confirm-prompt.json` → relay `message` and `data` to user →
write response to `.fry/confirm-response.json` → Fry continues. Repeat for each
prompt in sequence (up to 3 during a full prepare: executive, triage, overview).

**Timeout:** Fry waits up to 5 minutes for each response. If no response arrives,
the build fails with a timeout error.

**Precedence:** `-y` overrides `--confirm-file`. If both are passed, `-y` wins.

## Starting Builds

**Before starting**, always check if a build is already running:

```bash
fry status --json --project-dir /path/to/project
```

### How to launch builds

**Always use a subagent to run Fry builds.** Do not use `nohup ... &` or plain
`cmd &` from a non-interactive shell — the exec environment's ephemeral shell
will clean up backgrounded processes unpredictably.

**Always pass `-y`** to auto-accept all interactive prompts (triage confirmation,
project overview, executive context bootstrap). Without it, the process will
block on stdin and die silently in a non-interactive shell.

**Use sessions_spawn:**

```
sessions_spawn({
  task: "Run this command and report the exit code and last 30 lines of output when done:\n\nfry run -y --project-dir /path/to/project --json-report --telemetry 2>&1 | tee /tmp/fry-out.log",
  runtime: "subagent",
  mode: "run",
  runTimeoutSeconds: 600
})
```

### Key flags

| Flag | Values | Default | Purpose |
|------|--------|---------|---------|
| `-y` / `--yes` | (flag) | off | Auto-accept all interactive prompts. Always use this. |
| `--effort` | fast, standard, high, max, auto | auto | Sprint count and rigor |
| `--engine` | claude, codex, ollama | claude | Which LLM engine to use |
| `--fallback-engine` | claude, codex, ollama | auto | Sticky fallback engine after transient primary-engine failures |
| `--no-engine-failover` | (flag) | off | Disable cross-engine failover and stay on the selected engine |
| `--mode` | software, planning, writing | software | Build mode |
| `--model` | opus[1m], sonnet, haiku | (engine default) | Override agent model |
| `--git-strategy` | auto, current, branch, worktree | auto | Git branching |
| `--review` | (flag) | off | Enable sprint review between sprints |
| `--no-audit` | (flag) | audit on | Disable sprint and build audits |
| `--always-verify` | (flag) | off | Force all quality gates |
| `--user-prompt` | string | (none) | Additional directive for the build |
| `--user-prompt-file` | path | (none) | Load user prompt from a file |
| `--gh-issue` | URL | (none) | Fetch a GitHub issue through `gh` and use it as the task definition |
| `--mcp-config` | path | (none) | MCP server config file (Claude engine only) |
| `--confirm-file` | (flag) | off | Use file-based interactive prompts instead of stdin |
| `--dry-run` | (flag) | off | Preview without executing |
| `--sarif` | (flag) | off | Write SARIF 2.1.0 audit output |
| `--show-tokens` | (flag) | off | Print per-sprint token usage |
| `--telemetry` | (flag) | on | Enable experience upload to consciousness API |
| `--no-telemetry` | (flag) | off | Disable experience upload |
| `--verbose`, `-v` | (flag) | off | Verbose logging; on `fry monitor`, include granular synthetic events |

### Effort levels

| Level | Sprints | Self-Check | Alignment | Review | Audit | Use case |
|-------|---------|------------|-----------|--------|-------|----------|
| fast | 1-2 | No | Skip | No | No | Quick fixes, one-file changes |
| standard | 2-4 | Build/test + diff review | 3 attempts | No | Sprint only | Standard features |
| high | 4-10 | Build/test + diff review + quality focus | 10 + progress detection | Yes | Both | Complex features |
| max | Max rigor | Build/test + diff review + full rigor | Unlimited + progress | Yes | Both + deep | Critical/large work |
| auto | Triage decides | Based on triage | Based on triage | Based on triage | Based on triage | Let Fry decide |

### Engine differences

| Engine | CLI | Model tiers | Notes |
|--------|-----|-------------|-------|
| claude | Claude Code | opus[1m] / sonnet / haiku | Default. Supports MCP via `--mcp-config`. Implicit sticky failover target: Codex. |
| codex | Codex CLI | gpt-5.4 / gpt-5.3-codex / gpt-5.4-mini | Alternative. Implicit sticky failover target: Claude. |
| ollama | Ollama | llama3 variants | Local, no API key, no rate-limit detection. |

Override with `--engine <name>`, `@engine` in the epic, or `FRY_ENGINE` env var.

For the Fry repository's self-improvement loop, use `fry config set engine <name>`
to set the repo-local engine that `.self-improve/orchestrate.sh` should use by default.

Fry retries the selected engine first. If Claude or Codex still fails with a transient error (rate limit, timeout, 5xx, connection failure), Fry can fail over once to the other engine and then stay there for the rest of the build. Use `--fallback-engine` to override the target or `--no-engine-failover` to disable this behavior.

### Git strategies

| Strategy | Behavior | When to use |
|----------|----------|-------------|
| auto | Triage decides: complex → worktree, simple/moderate → branch; brand-new repos stay on current branch for the first build | Default. Let Fry choose. |
| branch | Creates `fry/<slug>` branch | Standard isolation |
| worktree | Creates isolated checkout at `.fry-worktrees/<slug>/` | Maximum isolation for complex work |
| current | Works on current branch | When you want changes in-place |

## Build Modes

### Software mode (default)

Standard code generation. Sprints produce code changes committed via git.

### Planning mode

Generate documents instead of code. Output goes to `output/` directory.

```bash
fry run --project-dir /path/to/project --mode planning --json-report --telemetry
```

Output files named `{seq}--{category}--{name}.md`. Sanity checks verify
document existence, required sections, and word count minimums. Audit criteria
focus on domain boundaries, analytical frameworks, and document quality.

### Writing mode

Generate long-form content (books, guides). Output goes to `output/` with
final consolidation to `manuscript.md`.

```bash
fry run --project-dir /path/to/project --mode writing --json-report --telemetry
```

Output files named `{seq}--{name}.md`. Audit criteria: coherence, accuracy,
completeness, tone/voice, structure, depth. Resume with `--continue` auto-
detects writing mode from `.fry/build-mode.txt`.

## Checking Status

```bash
fry status --json --project-dir /path/to/project
```

Returns structured JSON with build phase, sprint data, status, and timing.
Works correctly at every stage of a build:

| Status value | Meaning |
|---|---|
| `idle` | No build running or artifacts present |
| `triaging` | Triage classification in progress |
| `preparing` | Prepare pipeline running (epic generation) |
| `running` | Sprint execution, audit, or review in progress |
| `paused` | Build paused via Tier C steering |
| `completed` | Build finished successfully |
| `failed` | Build finished with errors |
| `stopped` | Build process died unexpectedly |

The JSON includes `phase` (triage, prepare, sprint, complete, failed) and
`worktree_dir` when the build uses worktree strategy.

### Error handling

- Non-zero exit code: `.fry/` may not exist or project path is wrong.
- Completed/failed state when expected running: build process died. Check
  `/tmp/fry-out.log` and `.fry/build-logs/` for details.

### Polling build status (file-based)

For continuous monitoring without running a command, read `.fry/build-status.json`
directly. This file is written atomically after every state change:

```bash
cat "/path/to/project/.fry/build-status.json"
```

The file contains:

```json
{
  "version": 1,
  "updated_at": "2026-03-29T10:05:00Z",
  "build": {
    "epic": "My Feature",
    "effort": "high",
    "engine": "claude",
    "mode": "software",
    "git_branch": "fry/my-feature",
    "total_sprints": 3,
    "current_sprint": 2,
    "status": "running",
    "started_at": "2026-03-29T10:00:00Z"
  },
  "sprints": [
    {
      "number": 1,
      "name": "Scaffolding",
      "status": "PASS (aligned)",
      "started_at": "2026-03-29T10:00:10Z",
      "finished_at": "2026-03-29T10:01:00Z",
      "duration_sec": 50.0,
      "sanity_checks": {
        "passed": 3,
        "total": 3,
        "results": [
          { "type": "FILE", "target": "main.go", "passed": true },
          { "type": "CMD", "target": "go build ./...", "passed": true },
          { "type": "TEST", "target": "go test ./...", "passed": true }
        ]
      },
      "alignment": { "attempts": 2, "outcome": "healed" },
      "audit": {
        "cycles": 2,
        "findings": { "HIGH": 1, "MODERATE": 2 },
        "outcome": "running",
        "active": true,
        "stage": "fixing",
        "current_cycle": 2,
        "max_cycles": 5,
        "current_fix": 1,
        "max_fixes": 4,
        "target_issues": 3,
        "issue_headlines": [
          "internal/api/server.go: missing request timeout",
          "internal/auth/token.go: nil dereference on refresh"
        ]
      },
      "review": { "verdict": "CONTINUE" }
    },
    {
      "number": 2,
      "name": "Core Logic",
      "status": "running",
      "started_at": "2026-03-29T10:01:30Z"
    }
  ],
  "build_audit": null
}
```

**Key fields for status reporting:**
- `build.status`: overall build state (`running`, `completed`, `completed_with_reporting_failure`, `failed`, `paused`)
- `reporting_failure`: present when build audit or summary generation failed after core build completed (fields: `stage`, `message`)
- `build.current_sprint`: which sprint is active
- `sprints[].status`: per-sprint outcome (`running`, `PASS`, `PASS (aligned)`, `FAIL`, etc.)
- `sprints[].sanity_checks`: pass/fail per check with type and target
- `sprints[].alignment.attempts`: how many alignment iterations were needed
- `sprints[].audit.outcome`: `running`, `pass`, `blocked`, `failed`, or `advisory`
- `sprints[].audit.stage`: current sprint-audit sub-phase (`auditing`, `fixing`, `verifying`) when `active=true`
- `sprints[].audit.current_cycle` / `max_cycles`: outer audit-loop progress
- `sprints[].audit.current_fix` / `max_fixes`: inner fix/verify-loop progress
- `sprints[].audit.complexity`: classified sprint-audit complexity (`low`, `moderate`, `high`, or `unknown`)
- `sprints[].audit.stop_reason`: why the audit exited early when it stopped for a non-pass reason such as low-yield termination
- `sprints[].audit.blocker_counts` / `blockers`: unresolved blocker categories and details when the sprint is blocked by missing prerequisites
- `sprints[].audit.metrics`: compact live metrics snapshot (calls, duration, no-op rate, verify yield, last-cycle/trailing productivity, repeated unchanged findings, suppressed unchanged reopenings, reopened-with-new-evidence count, strategy-shift count/last shift, low-yield strategy changes, and cache-aware token totals/details in the artifact)
- `sprints[].audit.target_issues` and `issue_headlines`: what the audit loop is currently targeting
- `build_audit`: final holistic audit result (present after build audit runs)

Prefer this file over `fry status --json` when monitoring a running build —
it requires no subprocess and updates in real time.

## Real-Time Monitoring

`fry monitor` provides continuous, multi-source monitoring with enriched output:

```bash
fry monitor --json --project-dir /path/to/project   # NDJSON snapshots
fry monitor --dashboard --project-dir /path/to/project  # Refreshing dashboard
fry monitor --logs --project-dir /path/to/project    # Tail active build log
fry monitor --verbose --project-dir /path/to/project # Include granular synthetic events
```

The monitor composes data from events, build status, sprint progress, build logs, and process liveness. It enriches events with elapsed times, sprint fractions (`2/5`), and phase transitions. With `--verbose`, it also emits synthetic granular events derived from build-log file creation, including agent deploys, audit/fix/verify session starts, review starts, observer wake-ups, and build-audit launches. The dashboard reads live sprint-audit progress from `.fry/build-status.json` and shows whether the active sprint is in audit, which stage is active, the current `cycle N/M` and `fix N/M`, how many issues are targeted, and up to three compact issue headlines. The `--json` output emits one snapshot per state change, including `new_events` with enrichment fields.

By default, the monitor waits for a build to start. Use `--no-wait` to exit immediately if no build is active.

## Reading Build Logs

Build logs are in `.fry/build-logs/`. Read the most recent:

```bash
ls -t "/path/to/project/.fry/build-logs/"*.log 2>/dev/null | head -1 | xargs -I{} tail -50 "{}"
```

Filter by type:

```bash
# Sprint logs only (exclude iteration and heal sub-logs)
ls "/path/to/project/.fry/build-logs/"sprint*.log 2>/dev/null | grep -v _iter | grep -v _heal | sort | tail -1 | xargs -I{} tail -50 "{}"

# Heal/alignment logs
ls "/path/to/project/.fry/build-logs/"*_heal*.log 2>/dev/null | sort | tail -1 | xargs -I{} tail -50 "{}"

# Audit logs
ls "/path/to/project/.fry/build-logs/"*audit*.log 2>/dev/null | sort | tail -1 | xargs -I{} tail -50 "{}"
```

## Reading Progress

```bash
# Current sprint progress (iteration-level detail)
cat "/path/to/project/.fry/sprint-progress.txt"

# Epic progress (compacted summaries of completed sprints)
cat "/path/to/project/.fry/epic-progress.txt"
```

If these files don't exist, the build hasn't produced progress output yet.

## Understanding Build Outputs

### Sanity checks

Fry verifies sprint output with five check types defined in `.fry/verification.md`:

| Check | Syntax | What it verifies |
|-------|--------|-----------------|
| File exists | `@check_file <path>` | File exists and is non-empty |
| File contains | `@check_file_contains <path> <pattern>` | Grep -E pattern matches |
| Command succeeds | `@check_cmd <command>` | Exit code 0 |
| Command output | `@check_cmd_output <cmd> \| <pattern>` | Stdout matches pattern |
| Tests pass | `@check_test <command>` | Exit 0 and zero test failures |

Before the sprint loop begins, Fry runs **harness self-validation** on all sanity check file targets — checking for absolute paths, path traversal (`../`), missing parent directories, and empty targets. Issues are reported as warnings so broken checks don't silently pass.

When checks fail, Fry runs an alignment loop to auto-fix. If alignment stalls,
the sprint may pass with deferred failures (below `@max_fail_percent` threshold).

### Sprint code review

After each sprint (standard effort and above), Fry runs a single-session code review:

- A single agent session reviews the sprint's changes, classifies issues by severity, fixes everything above LOW, and re-reviews until clean — all within one uninterrupted session.
- **CRITICAL/HIGH** findings block the sprint.
- **MODERATE** findings are advisory (non-blocking).
- **LOW** findings are accepted as-is.
- **Complexity classification:** Fry classifies the sprint diff as low, moderate, or high complexity. Moderate and high complexity sprints receive an additional figure reconciliation check.
- **Iteration tracking:** The agent reports how many review passes it performed and whether it converged (exit condition met) or hit the iteration limit.
- **Finding deduplication:** Findings are deduplicated by normalized location + description. When duplicates exist, the higher severity is kept.
- When `.fry-config/codebase.md` exists, the review prompt uses it as ground-truth architecture context.
- Relevant intentional divergences from `.fry/deviation-log.md` are injected into review prompts so the reviewer does not flag accepted design differences as defects.
- If the agent forgets to write `.fry/sprint-review.txt`, Fry attempts to recover findings from the agent's transcript output (with a quality gate to prevent false positives).
- Review metrics are written to `.fry/build-logs/sprintN_review_TIMESTAMP.log`.

Read audit findings:

```bash
cat "/path/to/project/.fry/sprint-audit.txt"
```

### Build audit

After all sprints complete, Fry runs a holistic audit of the entire codebase.
Output is committed to the project:

```bash
cat "/path/to/project/build-audit.md"
```

Use `--sarif` to also generate `build-audit.sarif` in SARIF 2.1.0 format.
If the agent forgets to write `build-audit.md`, Fry attempts the same structured-output recovery before treating the build audit as failed.
When deferred sanity check failures exist, Fry injects a grouped deferred-failure analysis plus intentional deviations into the build-audit prompt and writes `.fry/validation-checklist.md` before replaying deferred checks.

### Sprint review

When `--review` is enabled, Fry reviews cross-sprint coherence between sprints.
The reviewer issues one of:

- **CONTINUE** — proceed as planned.
- **DEVIATE** — replan remaining sprints. Fry auto-replans the epic.

Deviation history is logged in `.fry/deviation-log.md`. You can also manually
trigger replanning:

```bash
fry replan --project-dir /path/to/project
```

### Build summary

After a build completes, Fry generates `build-summary.md` at the project root
with a sprint status table, key findings, and overall outcome.

## Standalone Audit

Run `fry audit` for an on-demand AI-powered code audit on any codebase — no
build required:

```bash
# Audit current project
fry audit --project-dir /path/to/project

# With SARIF output for tooling
fry audit --sarif --project-dir /path/to/project

# Maximum rigor
fry audit --effort max --project-dir /path/to/project

# Content audit (writing mode criteria)
fry audit --mode writing --project-dir /path/to/project
```

**Key flags:**

| Flag | Default | Purpose |
|------|---------|---------|
| `--effort` | `high` | Audit rigor: fast (quick), standard, high, max (thorough) |
| `--engine` | `claude` | AI engine |
| `--fallback-engine` | auto | Sticky fallback engine after transient engine failures |
| `--no-engine-failover` | off | Disable cross-engine failover |
| `--model` | (auto) | Override agent model |
| `--mode` | `software` | Audit criteria: software (code quality) or writing (content quality) |
| `--sarif` | off | Write `build-audit.sarif` in SARIF 2.1.0 format |
| `--mcp-config` | (none) | MCP server config (Claude only) |

**Behavior:**
- Works on any directory — completed Fry builds, partial builds, or non-Fry projects
- Uses existing `.fry/epic.md` for context when available; creates synthetic context otherwise
- Runs the same two-level audit loop as the build pipeline (find → fix → verify → re-audit)
- Writes `build-audit.md` to the project root
- Git-checkpoints the results
- Returns non-zero exit code when blocking (CRITICAL/HIGH) findings remain

**Use cases:**
- Re-run the audit after a build was interrupted during the audit phase
- Quality gate in CI pipelines
- Post-edit review after manual changes
- Audit any codebase that was never built with Fry

## Epic Format

Fry uses epic files (`.fry/epic.md`) to define sprint structure. These are
auto-generated by `fry prepare` from your plan, but you can also write or edit
them manually.

### Structure

```markdown
@epic My Feature
@engine claude
@effort high
@verification .fry/verification.md

@sprint 1
@name Scaffolding
@max_iterations 20
@promise All foundation files exist and compile
@prompt
Build the project scaffolding:
- Initialize module
- Create config package
- Set up database schema
@end

@sprint 2
@name Core Logic
@max_iterations 25
@prompt
Implement the business logic layer...
@end
```

### Key global directives

| Directive | Purpose |
|-----------|---------|
| `@epic <name>` | Epic title |
| `@engine <claude\|codex\|ollama>` | LLM engine |
| `@effort <fast\|standard\|high\|max>` | Effort level |
| `@verification <path>` | Sanity checks file |
| `@model <name>` | Override agent model |
| `@mcp_config <path>` | MCP server config (Claude only) |
| `@review_between_sprints` | Enable sprint review |
| `@review_after_sprint` | Enable sprint code review (default) |
| `@no_review` | Disable sprint code review |
| `@max_heal_attempts <N>` | Alignment attempt limit |
| `@max_fail_percent <N>` | Deferred failure threshold (default 20%) |
| `@docker_from_sprint <N>` | Start Docker from sprint N |
| `@require_tool <name>` | Preflight tool check |
| `@pre_sprint <cmd>` | Run command before each sprint |
| `@pre_iteration <cmd>` | Run command before each iteration |

### Sprint directives

| Directive | Purpose |
|-----------|---------|
| `@sprint <N>` | Sprint number (sequential) |
| `@name <text>` | Sprint name |
| `@max_iterations <N>` | Iteration limit |
| `@promise <text>` | Success criteria (checked by agent) |
| `@prompt` | Start of sprint prompt (multi-line) |
| `@end` | End of sprint block |

## Build Steering (Layer 1)

Fry supports mid-flight steering through file-based IPC in `.fry/`.

### Tier A: Whisper (non-stopping directive)

Inject guidance into the next iteration without stopping:

```bash
cat <<'DIRECTIVE' > "/path/to/project/.fry/agent-directive.md.tmp"
Focus on error handling in the auth module.
Make sure all database calls have proper timeout handling.
DIRECTIVE
mv "/path/to/project/.fry/agent-directive.md.tmp" "/path/to/project/.fry/agent-directive.md"
```

Picked up on the next iteration. Keep under 10KB. Atomic write prevents
partial reads.

### Tier B: Hold (pause after sprint for decision)

```bash
touch "/path/to/project/.fry/agent-hold-after-sprint"
```

When the build holds, it writes `.fry/decision-needed.md`. Read it, then
respond:

```bash
cat "/path/to/project/.fry/decision-needed.md"

# Continue as planned
cat <<'DIRECTIVE' > "/path/to/project/.fry/agent-directive.md.tmp"
continue
DIRECTIVE
mv "/path/to/project/.fry/agent-directive.md.tmp" "/path/to/project/.fry/agent-directive.md"
rm -f "/path/to/project/.fry/decision-needed.md"

# Or provide new direction
cat <<'DIRECTIVE' > "/path/to/project/.fry/agent-directive.md.tmp"
Refactor the database layer before adding the API endpoints.
DIRECTIVE
mv "/path/to/project/.fry/agent-directive.md.tmp" "/path/to/project/.fry/agent-directive.md"
rm -f "/path/to/project/.fry/decision-needed.md"

# Or replan remaining sprints
cat <<'DIRECTIVE' > "/path/to/project/.fry/agent-directive.md.tmp"
replan: Split the remaining work into smaller sprints.
DIRECTIVE
mv "/path/to/project/.fry/agent-directive.md.tmp" "/path/to/project/.fry/agent-directive.md"
rm -f "/path/to/project/.fry/decision-needed.md"
```

**Always verify `.fry/decision-needed.md` exists before responding.** If it
doesn't, the build is not holding — use Tier A directive instead.

### Tier C: Pause (graceful stop)

```bash
fry exit --project-dir /path/to/project
```

Legacy fallback when the CLI command is unavailable:

```bash
touch "/path/to/project/.fry/agent-pause"
```

Fry writes `.fry/exit-request.json`, settles the next safe checkpoint, then
persists `.fry/resume-point.json` with the sprint, phase, verdict, and
recommended resume command. Resume with `fry run --continue` by default, or
use the explicit command recorded in `resume-point.json`. Prefer the command
because it resolves the canonical worktree build directory automatically.

## Resuming Builds

| Mode | Flag | When to use |
|------|------|-------------|
| LLM-driven | `--continue` | Default. Analyzes build state, decides where to resume. |
| Lightweight | `--simple-continue` | Skip LLM analysis, resume from first incomplete sprint. |
| Heal-only | `--resume` | Skip iterations, go straight to sanity checks + alignment. |
| From sprint N | `--sprint N` | Fresh start from specific sprint number. |

Use `sessions_spawn` to resume, same as starting a build:

```
sessions_spawn({
  task: "Run this command and report the exit code and last 30 lines of output when done:\n\nfry run -y --continue --project-dir /path/to/project --json-report --telemetry 2>&1 | tee /tmp/fry-out.log",
  runtime: "subagent",
  mode: "run",
  runTimeoutSeconds: 600
})
```

Resume auto-detects the build mode (software/planning/writing) and git
strategy (branch/worktree) from `.fry/build-mode.txt` and `.fry/git-strategy.txt`.

## Team Runtime

Fry includes a standalone tmux-backed team runtime for multi-worker parallel builds. The team runtime manages workers, assigns tasks, handles liveness, and integrates outputs.

### Starting a Team

```bash
fry team start --workers 3 --project-dir /path/to/project
fry team start --workers 2 --role executor,executor --task-file tasks.json --project-dir /path/to/project
fry team start --workers 4 --git-isolation per-worker-worktree --project-dir /path/to/project
```

Key flags:
- `--workers N` — number of workers to spawn
- `--role role1,role2` — worker roles (repeat or comma-separate; default: executor)
- `--task-file path` — JSON task file to load at startup
- `--git-isolation shared|per-worker-worktree` — how workers share the codebase (default: shared for single worker, per-worker-worktree for multiple workers in git repos)
- `--team id` — explicit team identifier (auto-generated if omitted)
- `--executable-path path` — override fry binary path for worker hosts

### Managing a Team

```bash
fry team status                          # Show team status (human-readable)
fry team status --json                   # Show team status (JSON)
fry team assign --task-file tasks.json   # Load new tasks
fry team pause                           # Pause task claiming
fry team resume                          # Resume + reconcile dead workers
fry team scale --add 2                   # Add workers
fry team scale --remove worker-1         # Drain/remove a worker
fry team attach                          # Attach to tmux session
fry team shutdown                        # Graceful shutdown
fry team shutdown --force                # Force kill tmux session
```

### Task File Format

Task files are JSON arrays or `{ "tasks": [...] }` objects:

```json
[
  {"id": "001", "title": "Build API", "role": "executor", "priority": 1, "command": "fry run -y"},
  {"id": "002", "title": "Build UI", "role": "executor", "priority": 2, "command": "fry run -y"}
]
```

### Git Isolation Modes

- **shared** — all workers share the same working directory. Suitable for single-worker teams or non-git projects.
- **per-worker-worktree** — each worker gets its own git worktree. Outputs are auto-merged on completion; merge results are summarized in `.fry/team/<team-id>/artifacts/merge-report.md`.

### Team State

Team state lives under `.fry/team/<team-id>/`:
- `config.json` — team configuration and lifecycle state
- `tasks/` — per-task state files
- `workers/<worker-id>/` — worker identity, status, heartbeat
- `locks/` — worker-level task locks
- `artifacts/` — worker logs and task outputs
- `events.jsonl` — team-specific event log

Team lifecycle events are mirrored into the shared observer stream, so `fry events`, `fry status`, and `fry monitor` consume team activity without a separate transport.

---

## Consciousness Pipeline

Fry has an introspective system that synthesizes build experiences:

```bash
# View local consciousness session health
fry status --consciousness --project-dir /path/to/project

# View remote consciousness pipeline stats
fry status --consciousness-remote --project-dir /path/to/project

# Print Fry's compiled identity
fry identity
fry identity --full

# Trigger reflection from accumulated experiences
fry reflect
```

Experiences are stored in `~/.fry/experiences/`. During a build, in-progress
checkpoint state lives under `.fry/consciousness/`. The `--telemetry` flag
enables checkpoint-summary and lifecycle upload; failed uploads stay queued for
retry. Reflection runs weekly and updates Fry's identity.

## Copilot Mode

The copilot is a parallel persistent agent session that monitors a build via a
self-installed cron and intervenes when something goes wrong. It edits the fry
source tree to fix canonical bugs (test → make install → commit → push →
restart) or remediates broken build artifacts to unstick the build.

```bash
# Enable for any build
fry run --copilot --project-dir /path/to/project

# Choose engine explicitly
fry run --copilot=claude --project-dir /path/to/project
fry run --copilot=codex --project-dir /path/to/project    # in-process scheduler fallback

# Customize cadence (default 10m, range 1m–1h)
fry run --copilot --copilot-interval=15m --project-dir /path/to/project

# Passive observation only (no interventions)
fry run --copilot --copilot-passive --project-dir /path/to/project

# Auto-enabled at max effort; opt-out:
fry run --effort=max --no-copilot --project-dir /path/to/project
```

### When to suggest enabling the copilot

Recommend `--copilot` to the user when:
- The build is long (multiple sprints, complex epic)
- The user wants the build to recover from canonical fry bugs without manual intervention
- The user is running unattended (CI, overnight, agent-driven)
- The effort level is `high` or `max`

Skip the suggestion when:
- The build is short (< 30 min) or has 1–2 sprints
- The user is monitoring manually
- The build runs on shared infrastructure where commit/push authority would be inappropriate

### Inspecting a running copilot

```bash
# What's the copilot doing right now?
fry copilot status --project-dir /path/to/project
fry copilot status --json --project-dir /path/to/project

# Watch live activity (human-readable narrative log)
fry copilot tail --follow --project-dir /path/to/project

# Watch the structured event stream
fry copilot tail --follow --jsonl --project-dir /path/to/project
```

### Attaching to the copilot session manually

The copilot session is stable for the lifetime of the build. The session UUID is
printed at startup, saved to `.fry/copilot/session-id.txt`, and shown by
`fry copilot status`. To attach a separate terminal:

```bash
# Exec the current shell into the session
fry copilot attach --project-dir /path/to/project

# Or just print the command (for tmux / IDE integration)
fry copilot attach --print-only --project-dir /path/to/project
# claude --resume 4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c
```

Messages typed into an attached session become part of the conversation; the
copilot sees them on its next reasoning step and logs them under "external
guidance" in its scratchpad.

### Stopping the copilot without stopping the build

```bash
fry copilot stop --project-dir /path/to/project
```

This writes `.fry/copilot/stop-requested`. The next wake reads it, deletes the
cron, writes the final summary, and exits cleanly. The build continues
unaffected.

### Reading the copilot's final summary

```bash
fry copilot summary --project-dir /path/to/project       # final-summary.md
fry copilot summary --current --project-dir /path/to/project  # in-progress synthesis
fry copilot list-interventions --project-dir /path/to/project # list intervention reports
```

### Copilot artifact paths

| Path | What it is |
|---|---|
| `.fry/copilot/manifest.json` | Session config: engine, session ID, cron ID, mode, capabilities |
| `.fry/copilot/session-id.txt` | One-line session UUID for `claude --resume` |
| `.fry/copilot/cron.id` | Cron tool ID returned by `CronCreate` |
| `.fry/copilot/state-snapshot.json` | Compact build state — atomically rewritten by fry on every observer wake-point (10s debounce) |
| `.fry/copilot/events.txt` | Human-readable narrative log |
| `.fry/copilot/events.jsonl` | Structured event stream (mirrored into `.fry/observer/events.jsonl`) |
| `.fry/copilot/scratchpad.md` | Working memory across wakes |
| `.fry/copilot/interventions/<NNNN>-<slug>.md` | Per-intervention reports |
| `.fry/copilot/final-summary.md` | Final summary written on clean exit |
| `.fry/copilot/bootstrap.log` | Bootstrap subprocess stdout/stderr |

Copilot events use the `copilot_*` prefix and are visible in `fry events
--follow`, `fry monitor`, and `fry copilot tail`.

## Build Events

Stream live events from a running build:

```bash
fry events --follow --json --project-dir /path/to/project
```

Key event types: `sprint_start`, `sprint_complete`, `alignment_complete`,
`audit_complete`, `review_complete`, `build_end`, `directive_received`,
`decision_needed`, `build_paused`.

## Artifact Paths

| Path | Content |
|------|---------|
| `plans/plan.md` | Build plan (user input) |
| `plans/executive.md` | Strategic context (user input, optional) |
| `assets/` | Text reference documents (user input, optional) |
| `media/` | Binary assets (user input, optional) |
| `.fry/epic.md` | Generated epic with sprint definitions |
| `.fry/AGENTS.md` | Generated agent instructions |
| `.fry/verification.md` | Sanity check definitions |
| `.fry/sprint-progress.txt` | Current sprint iteration log |
| `.fry/epic-progress.txt` | Compacted summaries of completed sprints |
| `.fry/build-logs/` | Sprint, heal, and audit log files |
| `.fry/build-report.json` | Structured build report (when `--json-report`) |
| `.fry/build-status.json` | Machine-readable build status snapshot (updated every state change, including live code review progress) |
| `.fry/sprint-review.txt` | Current sprint code review findings (transient) |
| `.fry/validation-checklist.md` | Deferred-failure validation checklist for build audit follow-up |
| `.fry/sessions/` | Transient session IDs (Claude/Codex only) |
| `.fry/observer/events.jsonl` | Full event stream |
| `.fry/deviation-log.md` | Sprint review deviation history |
| `.fry/build-mode.txt` | Active build mode (software/planning/writing) |
| `.fry/git-strategy.txt` | Active git strategy |
| `build-summary.md` | Build summary (project root, committed) |
| `build-audit.md` | Build audit findings (project root, committed) |
| `output/` | Planning/writing mode output directory |
| `.fry/confirm-prompt.json` | File-based interactive prompt (transient) |
| `.fry/confirm-response.json` | File-based interactive response (transient) |
| `.fry/agent-directive.md` | Active directive (Layer 1 steering) |
| `.fry/agent-hold-after-sprint` | Hold flag (Layer 1 steering) |
| `.fry/agent-pause` | Pause flag (Layer 1 steering) |
| `.fry/exit-request.json` | Structured graceful-exit request written by `fry exit` |
| `.fry/resume-point.json` | Settled resume checkpoint used by `--continue` / `--simple-continue` |
| `.fry/decision-needed.md` | Decision request from held build |

## Cleaning Up

Archive completed build artifacts to `.fry-archive/`:

```bash
fry clean -y --project-dir /path/to/project
```

Creates a timestamped snapshot at `.fry-archive/.fry--build--YYYYMMDD-HHMMSS/`
and removes the `.fry/` directory. Persistent artifacts (`.fry-config/codebase.md`,
`.fry-config/file-index.txt`, `.fry-config/codebase-memories/`) live in `.fry-config/` which is not affected by archive/clean.

To completely remove all fry artifacts (as if fry was never run):

```bash
fry destroy -y --project-dir /path/to/project
```

Removes `.fry/`, `.fry-archive/`, `.fry-worktrees/`, `plans/`, `assets/`, `media/`,
and root-level build outputs. Unlike `clean`, nothing is preserved or archived.

## Behavior Guidelines

- **Always use `sessions_spawn`** to run Fry builds — never `nohup` or `&`.
- **Always pass `-y` or `--confirm-file`** — use `-y` to auto-accept all prompts,
  or `--confirm-file` to relay prompts to the user via file-based IPC.
  Prefer the **two-step flow** (Option A) when possible: run `--triage-only`,
  relay to user, then build with `-y --effort <choice>`.
- **Check status before starting** — verify no build is active first.
- **Use `fry status --json`** as the primary monitoring tool.
- **Atomic file writes** for directives: write to `.tmp` then `mv`.
- **Quote all paths** to handle directories with spaces.
- **Don't modify `.fry/` files** other than the steering files.
- **One build per project directory** at a time.
- **Report build outcomes**: sprint pass/fail, alignment attempts, audit
  findings, review verdicts, total duration.
- When a build finishes, read `build-summary.md` and `build-audit.md` and
  summarize the key findings for the user.
