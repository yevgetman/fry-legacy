# Architecture

Fry is a single static Go binary organized into focused internal packages. This document describes the internal structure for contributors and anyone wanting to understand the codebase.

## Package Overview

```
cmd/fry/                    Entry point — calls cli.Execute()
internal/agent/             Agent foundation: domain types, build state reader, event streaming, artifact schema, system prompt generation
internal/agentrun/          Shared dual-log execution harness for sprint and heal phases
internal/archive/           Timestamped snapshots of .fry/ and build outputs into .fry-archive/
internal/assets/            Supplementary assets scanner and content builder (prepare-only)
internal/audit/             Post-build semantic audit (build audit only)
internal/codereview/        Post-sprint single-session code review (review + fix in one agent call)
internal/cli/               Cobra command definitions (run, prepare, replan, init, exit, clean, status, identity, version, agent, events, team)
internal/color/             ANSI color output with TTY detection and NO_COLOR support
internal/config/            Constants: file paths, defaults, version string
internal/confirm/           File-based interactive prompt IPC for agent LLMs
internal/consciousness/     Session-based checkpoint persistence, checkpoint distillation, final experience synthesis, upload queue
internal/continuerun/       Build state collection, LLM analysis, and resume logic for --continue
internal/docker/            Docker Compose lifecycle management
internal/engine/            AI engine abstraction (Claude, Codex, Ollama), tier-based model selection, validation
internal/epic/              Epic file parser, types (Epic, Sprint, EffortLevel), and validator
internal/git/               Git operations (init, checkpoint, commit)
internal/githubissue/       GitHub issue fetching via gh CLI and conversion to user prompts
internal/heal/              Alignment loop (re-run agent on sanity check failure)
internal/lock/              File-based concurrency lock (PID-based)
internal/log/               Timestamped logging with verbose mode
internal/media/             Media directory scanner and manifest builder
internal/metrics/           Token usage parsing for Claude and Codex engines
internal/monitor/           Real-time build monitoring (event stream, status snapshots, log tail)
internal/observer/          Metacognitive event recording, identity, scratchpad continuity, and strict wake-up parsing
internal/copilot/           Parallel persistent agent: manifest, source-dir discovery, session-ID capture, state snapshot, prompts, bootstrap launcher, lock, cleanup, events
internal/preflight/         Pre-build validation checks
internal/prepare/           Artifact generation (Steps 0-3), mode handling, project overview
internal/report/            BuildReport JSON serialization
internal/review/            Dynamic sprint review, replanning, deviation tracking
internal/scan/              Codebase scanning (structural, semantic, compact, codebase memories)
internal/settings/          User settings under ~/.fry/ (telemetry opt-in/out)
internal/severity/          Issue severity classification for audit findings
internal/shellhook/         Shell command execution for hooks
internal/sprint/            Sprint execution loop, prompt assembly, progress tracking
internal/steering/          File-based IPC for mid-build human intervention (directives, holds, pauses, graceful exits, resume points)
internal/summary/           Build summary generation (post-epic agent session)
internal/team/              Standalone tmux-backed team runtime (state, workers, tasks, liveness, fan-in)
internal/textutil/          Text utilities (markdown stripping, file timestamps, artifact resolution)
internal/triage/            Task complexity classification and programmatic epic generation
internal/verify/            Sanity check parsing, execution, and diagnostic collection
templates/                  Embedded templates (AGENTS.md, epic-example, verification-example, etc.)
```

## CLI Commands

| Command | Description |
|---|---|
| `fry run` | Execute the build: parse epic, run sprints, sanity checks, align, audit |
| `fry prepare` | Generate `.fry/AGENTS.md`, `epic.md`, and `verification.md` from `plans/` |
| `fry init` | Scaffold `plans/`, `assets/`, `media/` directories and initialize git |
| `fry exit` | Request a graceful stop and persist a deterministic resume point |
| `fry clean` | Archive `.fry/` and root-level build outputs to `.fry-archive/` |
| `fry destroy` | Remove all fry artifacts completely |
| `fry config` | Read or write repo-local Fry settings (`get`/`set`) |
| `fry status` | Show current build state (sprints, progress) without an LLM call |
| `fry monitor` | Real-time build monitor (event stream, status, log tail) |
| `fry identity` | Print Fry's current identity (core or full with `--full`) |
| `fry replan` | Re-run the sprint review and replanning pass |
| `fry audit` | Standalone AI-powered build audit |
| `fry version` | Print the Fry version |
| `fry events` | List build events; `--follow --json` for real-time streaming |
| `fry team` | Operate the standalone team runtime (tmux workers, tasks, scale, pause/resume, shutdown) |
| `fry agent prompt` | Print the agent system prompt (artifact schema, lifecycle, identity) |
| `fry status --json` | Output structured build state as JSON (for agent consumption) |
| `fry reflect` | Trigger the Reflection pipeline remotely |

## Supported Engines

| Engine | Description |
|---|---|
| `claude` | Anthropic Claude Code CLI (`claude`). Default engine. |
| `codex` | OpenAI Codex CLI (`codex`). |
| `ollama` | Local Ollama server. Requires `ollama` running locally; defaults to `llama3`. |

## Key Interfaces

### Engine Interface

All AI engines implement a common interface, making them interchangeable:

```go
type Engine interface {
    Run(ctx context.Context, prompt string, opts RunOpts) (output string, exitCode int, err error)
    Name() string
}
```

`RunOpts` includes model override, extra flags, working directory, output streams, and log file paths.

All engines are wrapped in a `ResilientEngine` decorator at creation time, adding automatic retry with exponential backoff on rate-limit errors (429, overloaded, etc.). The decorator is transparent to callers. See [engines: Rate-Limit Resilience](engines.md#rate-limit-resilience).

## Data Flow

```
User Input (plans/, media/, assets/, or --user-prompt)
       │
       ▼
   Triage gate ──► Classify complexity (1 cheap LLM call)
               ──► Interactive confirmation [Y/n/a] (user can adjust difficulty/effort)
               ──► SIMPLE/MODERATE: programmatic epic (no LLM prepare)
               ──► COMPLEX or --full-prepare: full prepare (below)
       │
       ▼
   fry prepare ──► Bootstrap: --user-prompt → plans/executive.md (interactive review)
               ──► Project overview: AI-generated project summary for user confirmation
               ──► Step 0: plans/executive.md → plans/plan.md
               ──► Steps 1-3: .fry/AGENTS.md, epic.md, verification.md
                   (scans media/ for asset manifest, reads assets/ for supplementary context)
       │
       ▼
   fry run
       │
       ├─ Parse & validate epic (epic/)
       ├─ --continue: collect build state + structured resume point + LLM analysis (continuerun/)
       ├─ Acquire lock (lock/)
       ├─ Preflight checks (preflight/)
       ├─ Init git (git/)
       │
       ├─ For each sprint:
       │   ├─ Docker up (docker/)
       │   ├─ Shell hooks (shellhook/)
       │   ├─ Assemble prompt (sprint/prompt.go)
       │   ├─ Agent loop (sprint/runner.go → agentrun/ → engine/)
       │   ├─ Observer event recording (observer/)
       │   ├─ Sanity checks (verify/)
       │   ├─ Alignment loop (heal/ → agentrun/ → engine/ → verify/)
       │   ├─ Resume mode (sprint/runner.go → verify/ → heal/, --resume flag)
       │   ├─ Sprint code review (codereview/ → engine/)
       │   ├─ Git checkpoint (git/)
       │   ├─ Compact progress (sprint/compactor.go)
       │   └─ Sprint review (review/)
       │
       ├─ Build summary (summary/)
       ├─ Build audit (audit/ → engine/)
       ├─ Consciousness checkpoint distillation + final synthesis (consciousness/ → engine/) — non-fatal
       ├─ Build report generation (report/) — writes .fry/build-report.json
       ├─ Archive on success (archive/) — writes .fry-archive/
       └─ Release lock
```

## Epic Parsing

The epic parser (`internal/epic/parser.go`) is a line-by-line state machine with three states:

| State | Description |
|---|---|
| `stateGlobal` | Parsing global directives before any `@sprint` block |
| `stateSprintMeta` | Parsing sprint metadata (`@name`, `@max_iterations`, etc.) |
| `stateSprintPrompt` | Collecting multi-line prompt text between `@prompt` and `@end` |

## Embedded Templates

Templates are embedded in the binary via Go's `embed` package (`templates/embed.go`). They include:

- `AGENTS.md` — example operational rules
- `epic-example.md` — epic format reference
- `verification-example.md` — sanity check examples
- `GENERATE_EPIC.md` — instructions for epic generation

During `fry prepare`, templates are extracted to a temp directory and referenced by the AI engine.

## Build System

The `Makefile` provides standard targets:

| Target | Command |
|---|---|
| `build` | `go build -o bin/fry ./cmd/fry` |
| `test` | `go test -race ./...` |
| `lint` | `golangci-lint run` |
| `clean` | `rm -rf bin/` |
| `install` | `cp bin/fry /usr/local/bin/fry` |

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/stretchr/testify` — Testing utilities (test-only)

No other external dependencies. The binary is fully self-contained.

## Team Runtime Layer

The standalone team runtime lives under `internal/team/` and follows the same artifact-first pattern as the main build runner. A team persists:

- config and lifecycle state in `.fry/team/<team-id>/config.json`
- tasks as one JSON file per task under `.fry/team/<team-id>/tasks/`
- worker identity, status, and heartbeat files under `.fry/team/<team-id>/workers/<worker-id>/`
- locks, logs, and merge artifacts under `.fry/team/<team-id>/locks/` and `artifacts/`

Lifecycle events are mirrored into the shared observer stream, which lets existing `fry events`, `fry status`, and `fry monitor` surfaces consume team activity without a separate transport.
