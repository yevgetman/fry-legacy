# Build Monitoring

`fry monitor` provides real-time, multi-source monitoring of active builds. It composes data from the observer event stream, build status, sprint progress, build logs, and process liveness into enriched views.

## Relationship to Existing Commands

| Command | Purpose |
|---------|---------|
| `fry status` | Point-in-time snapshot of build state (no continuous output) |
| `fry events --follow` | Raw event stream from `events.jsonl` (used by build-watcher service) |
| `fry monitor` | Enriched, multi-source, continuous monitoring with multiple view modes |

`fry monitor` complements the existing commands — it does not replace them.

## Usage

```bash
fry monitor [project-dir] [flags]
```

**Project directory resolution** (priority order):
1. Positional argument: `fry monitor /path/to/project`
2. `--project-dir` flag: `fry monitor --project-dir /path/to/project`
3. Current working directory: `fry monitor`

## View Modes

### Stream (default)

Streams enriched events with elapsed times, sprint progress fractions, and phase transitions.

```bash
fry monitor
```

Output:
```
[10:00:05]  +0s      build_start       effort=high epic=MyFeature sprints=3
[10:00:15]  +10s     sprint_start      1/3  name=Setup
[10:05:15]  +5m10s   sprint_complete   1/3  status=PASS duration=5m
[10:05:18]  +5m13s   sprint_start      2/3  name=API  [triage -> sprint]
```

Enrichments added to each event:
- **Elapsed time**: `+5m10s` — time since `build_start`
- **Sprint fraction**: `1/3` — current sprint out of total
- **Phase change**: `[triage -> sprint]` — when the build transitions between phases

### Stream (`--verbose`)

Adds granular synthetic events derived from build-log session files. This surfaces internal activity that is not persisted in `events.jsonl`, such as sprint agent deploys, audit/fix/verify session starts, review starts, observer wake-ups, and build-audit launches.

```bash
fry monitor --verbose
fry monitor -v
```

Output:
```
[10:05:19]  +5m14s   *agent_deploy        2/3  iteration=1 log=sprint2_iter1_20260331_100519.log session=sprint
[10:25:02]  +25m2s   *audit_cycle_start   2/3  cycle=1 log=sprint2_audit1_20260331_102502.log  [sprint -> audit]
[10:25:18]  +25m18s  *audit_fix_start     2/3  cycle=1 fix=1 log=sprint2_auditfix_1_1_20260331_102518.log
[10:31:44]  +31m44s  *observer_wake            log=observer_after_sprint_20260331_103144.log wake=after_sprint
```

Synthetic verbose events are prefixed with `*` in the stream renderer.

### Dashboard

Refreshing status overview that updates in-place (ANSI-aware on TTY; falls back to separator-delimited blocks on non-TTY).

```bash
fry monitor --dashboard
```

Output:
```
Fry Monitor                         PID 12345  10:05:18
────────────────────────────────────────────────────────────────
Epic: My Feature            Engine: claude
Mode: software              Effort: high
Phase: sprint               Branch: fry/my-feature
────────────────────────────────────────────────────────────────
Sprint 1/3: Setup .............. PASS         5m 10s (aligned)
Sprint 2/3: API ................ running      +2m 30s
Sprint 3/3: Polish ............. pending
────────────────────────────────────────────────────────────────
Latest: sprint_start (2/3) name=API
```

When the active sprint is in a code review cycle, the dashboard adds a compact review panel sourced from `.fry/build-status.json`:

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
────────────────────────────────────────────────────────────────
Latest: *audit_fix_start (2/3) cycle=2 fix=1
```

The audit panel answers four questions directly:
- Whether the sprint is currently in an audit cycle
- Which audit stage is active (`Audit pass`, `Fixing`, or `Verifying`)
- How far along the loop is (`cycle N/M`, `fix N/M`)
- How many issues are currently targeted, with a compact severity breakdown and up to three issue headlines

### Log Tail

Follow the most recently modified build log in `.fry/build-logs/`.

```bash
fry monitor --logs
```

### JSON (NDJSON)

Machine-readable output — one JSON snapshot per line. Suitable for piping to `jq` or consuming from scripts.

```bash
fry monitor --json | jq '.new_events[]'
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dashboard` | `false` | Refreshing dashboard view |
| `--logs` | `false` | Tail active build log |
| `--json` | `false` | NDJSON snapshot output |
| `--no-wait` | `false` | Exit immediately if no active build |
| `--interval` | `2s` | Polling interval (e.g. `1s`, `500ms`) |
| `--verbose`, `-v` | `false` | Include synthetic granular events derived from build-log file creation |

## Data Sources

The monitor polls these artifacts with change detection to minimize syscalls:

| Source | File | Detection |
|--------|------|-----------|
| Events | `.fry/observer/events.jsonl` | Byte offset tracking |
| Build phase | `.fry/build-phase.txt` | File mtime |
| Build status | `.fry/build-status.json` | File mtime; also carries live audit-cycle stage, cycle/fix counters, targeted issue counts, and issue headlines |
| Process lock | `.fry/.fry.lock` | PID liveness (signal-0) |
| Sprint progress | `.fry/sprint-progress.txt` | File size |
| Epic progress | `.fry/epic-progress.txt` | File size |
| Build logs | `.fry/build-logs/*.log` | Directory scan + size |
| Verbose log events | `.fry/build-logs/*.log` | New-file detection + filename parsing |
| Exit reason | `.fry/build-exit-reason.txt` | File existence |

**Polling cost when idle**: ~9 syscalls per tick (mostly `Stat` calls). After 10 unchanged ticks the interval slows from 2s to 5s automatically.

## Copilot Events

When [the copilot](copilot.md) is enabled (via `--copilot` or auto-enabled at `--effort=max`), the copilot agent emits structured events that are mirrored into the canonical observer stream at `.fry/observer/events.jsonl`. The monitor renders them natively alongside normal build events:

| Event type | When emitted |
|---|---|
| `copilot_bootstrap` | Initial spawn — copilot session created |
| `copilot_cron_installed` | Bootstrap subprocess installed its recurring schedule |
| `copilot_wake_start` / `copilot_wake_end` | Per-tick begin/end (emitted by the agent via `fry copilot emit-event`, every 10m by default) |
| `copilot_anomaly_detected` | The copilot identified a pattern worth investigating |
| `copilot_intervention_started` / `copilot_intervention_completed` / `copilot_intervention_failed` | Per-intervention lifecycle |
| `copilot_fry_bug_fix` | The copilot edited the fry source tree |
| `copilot_make_install` | The copilot ran `make install` after a fry-source fix |
| `copilot_git_push` | The copilot pushed a `[copilot]` commit to origin |
| `copilot_artifact_remediate` | The copilot edited a build artifact (Prisma, decision-needed, etc.) |
| `copilot_build_restart` | The copilot called `fry exit` + `fry run --continue` to deploy a new binary |
| `copilot_final_summary` | Final summary written; copilot session ending |
| `copilot_cron_removed` | Cron deleted (clean exit) |
| `copilot_escalation` | The copilot is stuck after multiple intervention attempts |

To watch only copilot activity:

```bash
fry copilot tail --follow                  # human-readable narrative
fry copilot tail --follow --jsonl          # structured event stream
fry monitor --json | jq 'select(.event.type | startswith("copilot_"))'
```

See [Copilot](copilot.md) for the full event taxonomy and intervention procedures.

## Lifecycle Handling

| Build State | Monitor Behavior |
|-------------|-----------------|
| No `.fry/` directory | Waits (or exits immediately with `--no-wait`) |
| Early phase (triage/prepare) | Shows phase, waits for events |
| Running | All views active, enriched output |
| Paused | Dashboard shows PAUSED status |
| Ended normally | Final snapshot, summary line, exit |
| Process crashed | Detects PID death, reports "exited unexpectedly", exit |

## Worktree Builds

For builds using the worktree git strategy, the monitor automatically resolves the worktree directory. Events are always emitted to the original project directory; build artifacts (status, logs, progress) may reside in the worktree. The monitor checks both locations.

## Architecture

The monitoring logic lives in `internal/monitor/` as a composable library package:

- **`snapshot.go`** — `Snapshot` and `EnrichedEvent` types
- **`source.go`** — `Source` interface with the core artifact pollers
- **`logevents.go`** — Verbose synthetic events derived from build-log filenames
- **`enrichment.go`** — Pure functions for event enrichment
- **`stream.go`** — `Monitor` orchestrator with `Run()` (continuous) and `Snapshot()` (one-shot)
- **`render.go`** — Rendering functions for each view mode

The CLI command in `internal/cli/monitor.go` is a thin wrapper. Future consumers (TUI, websocket server, external tools) can import `internal/monitor` directly.
