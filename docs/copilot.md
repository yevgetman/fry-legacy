# Copilot

The copilot is a parallel agent session that runs alongside an active fry build. Where the [observer](observer.md) passively reflects from inside the build process, the copilot actively intervenes from the outside — editing the fry source tree to fix canonical bugs, or editing the build's working tree to unstick broken state.

The copilot is **opt-in at every effort level except `max`**, where it auto-enables. It is a high-stakes feature: it can commit and push to the fry source repo and restart the build. Use it on builds where you would otherwise need to monitor manually.

## When to use it

Enable the copilot when:

- You're running a long, complex build at `--effort=max`
- You expect to need manual intervention but cannot babysit the terminal
- You want a second opinion on whether the build agents are stuck vs making progress
- You want canonical fry bugs found in your build to be auto-fixed and deployed

Skip the copilot when:

- The build is short (< 30 minutes)
- You're already monitoring manually
- You're running on shared infrastructure where the copilot's authority would be inappropriate

## Architecture

### Persistent session model

The copilot is **one long-lived agent session**, not N short sessions. fry's main process spawns one Claude Code session at build start; that session installs its own recurring schedule via `CronCreate`; the cron fires wake messages **into the same session** until the build completes.

This matters because:

- **Context is continuous.** The session remembers every previous tick — no reload from disk required.
- **Auto-compact handles growth.** A 6-hour build × 10 min ticks ≈ 180k tokens, well under 1M.
- **Attach is trivial.** One stable session ID. `fry copilot attach` resumes it.
- **Cost is lower.** No per-tick re-embedding of identity, authority, build context.

### Process flow

```
fry run --copilot
   ↓
fry main: parse flags, validate, discover fry source dir
   ↓
fry main: write .fry/copilot/manifest.json
   ↓
fry main: spawn detached `claude --session-id <uuid> -p` subprocess
   ↓
fry main: print startup banner with attach instructions
   ↓
copilot agent: read identity + authority + bootstrap prompt
copilot agent: install cron via CronCreate (every 10m)
copilot agent: write .fry/copilot/cron.id
copilot agent: go idle
   ↓
   (every 10 minutes)
   ↓
copilot agent: re-read state-snapshot.json
copilot agent: run tick checklist
copilot agent: intervene if needed (FRY-SOURCE / ARTIFACT / RESTART procedures)
copilot agent: update events.txt, scratchpad.md, interventions/
copilot agent: go idle
   ↓
   (build completes or fails)
   ↓
copilot agent: write .fry/copilot/final-summary.md
copilot agent: CronDelete + emit copilot_cron_removed
copilot agent: exit cleanly
```

### State snapshot

fry's main process writes `.fry/copilot/state-snapshot.json` at every observer wake-point (sprint start/complete, audit complete, build audit done, build end). The write is atomic via tmpfile + rename, debounced to at most one write per 10 seconds. The copilot re-reads this file on every tick to get fresh build state without re-parsing the canonical `build-status.json`.

## File layout

```
.fry/copilot/
├── manifest.json                # session config (see schema below)
├── session-id.txt               # one-line convenience copy of session UUID
├── bootstrap.pid                # PID of detached bootstrap subprocess
├── bootstrap.log                # subprocess stdout/stderr
├── cron.id                      # cron tool ID returned by CronCreate
├── tick.lock                    # session-busy indicator
├── state-snapshot.json          # rewritten by fry on build state changes
├── prompts/
│   ├── bootstrap.md             # rendered initial prompt
│   └── summary.md               # rendered recovery summary prompt
├── events.txt                   # human-readable narrative log
├── events.jsonl                 # structured event stream
├── scratchpad.md                # working memory across wakes (optional)
├── interventions/               # per-intervention markdown reports
│   ├── 0001-fry-bug-noop-detector.md
│   └── 0002-artifact-prisma-migrations.md
├── wakes/                       # one dir per cron wake (debugging)
├── final-summary.md             # written on clean exit
└── archive/                     # previous runs after fry clean
```

## CLI

### Run flags

```
fry run --copilot                      # enable, default engine claude
fry run --copilot=claude               # explicit engine
fry run --copilot=codex                # use codex (in-process scheduler fallback)
fry run --copilot-interval=15m         # change wake interval (1m–1h)
fry run --copilot-fry-source=/path     # explicit fry source dir
fry run --copilot-model=opus[1m]       # override model
fry run --copilot-passive              # observe only, no interventions
fry run --copilot-print-summary        # print final summary on fry exit
fry run --no-copilot                   # explicit opt-out (overrides max-effort auto-enable)
```

`--copilot` is auto-enabled at `--effort=max` unless `--no-copilot` is passed.

### Subcommands

```
fry copilot status [--json]                       # current session status
fry copilot attach [--print-only]                 # exec into session (or print attach cmd)
fry copilot stop [--keep-cron]                    # request clean exit
fry copilot tail [--follow] [--jsonl]             # tail events.txt or events.jsonl
fry copilot summary [--current]                   # print final-summary.md
fry copilot list-interventions                    # list intervention reports
fry copilot emit-event --type=<t> --data=<json>   # internal helper used BY the agent
```

`fry copilot attach` execs your terminal into the running session via `claude --resume <session-id>`. Messages you type become part of the conversation; the copilot sees them on its next reasoning step (logged as "external guidance" in the scratchpad).

## Allowed and forbidden actions

### Allowed

- **Edit fry source files** in `internal/`, `cmd/`, `templates/` (excluding `templates/identity/`)
- **Edit build artifacts** in `<build-dir>/.fry/` and `<build-dir>/.fry-config/` (with caution)
- **Run tests and install** in the fry source dir: `go test -race ./... && make install`
- **Stage specific files** (never `git add .` or `-A`)
- **Commit** with `[copilot]` prefix and provenance template
- **Push** to origin on the current branch (never `--force`)
- **Restart the build** via `fry exit` + `fry run --continue` when a fix requires it
- **Run shell commands**: git, make, npm, npx, prisma, kill, pkill, lsof, curl, ps, etc.
- **Stop the copilot itself** before build completion if the build is irrecoverably broken

### Forbidden

- New Go packages or `go get`
- Editing `CLAUDE.md`, `AGENTS.md`, `.gitignore`, `.github/`, `openclaw-skill/`, `templates/identity/`, `Makefile`, `go.mod`, `go.sum`
- `git push --force`, `git reset --hard`, `git rebase`, `git commit --amend`
- Touching files outside the build dir, fry source dir, and `/tmp/copilot-*`
- `--no-verify`, `--no-gpg-sign`
- `fry destroy`, `fry clean`, `fry init`, `fry team *`
- Spawning another copilot (no recursive `fry run --copilot`)
- Logging secrets, env vars, or API keys to events.txt

## Manifest schema

```json
{
  "version": 1,
  "session_id": "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c",
  "cron_id": "cron_01H9X...",
  "build_pid": 64128,
  "build_dir": "/Users/julie/code/meetingly3",
  "fry_source_dir": "/Users/julie/code/fry",
  "engine": "claude",
  "model": "opus[1m]",
  "started_at": "2026-04-07T00:00:00Z",
  "interval": "10m",
  "epic_name": "Meetingly Phase 1",
  "effort_level": "max",
  "max_interventions_per_class": 3,
  "stop_on_build_complete": true,
  "mode": "active",
  "engine_capabilities": {
    "session_id_flag": true,
    "cron_create": true,
    "remote_trigger": false
  },
  "session_id_capture_mechanism": "pre_specified"
}
```

`mode` is one of `active`, `passive`, or `dry_run`. Passive mode observes and writes summaries but does not intervene; it is forced when fry source dir resolution fails or when `--copilot-passive` is set.

## Stop conditions

The copilot session stops and runs the final summary when any of these are true:

| Condition | Detection |
|---|---|
| Build complete (success) | `build.status == completed` |
| Build complete with reporting failure | `build.status == completed_with_reporting_failure` |
| Build failed terminally | `build.status == failed` OR `build_phase == failed` |
| Build aborted | `lock_held == false` AND `build_phase` unchanged for 2+ wakes |
| Critically stuck | No progress for 3+ wakes despite intervention |
| User stop requested | `.fry/copilot/stop-requested` exists |
| Orphaned | manifest.json is gone |

On any stop condition the agent writes `final-summary.md`, deletes its cron, emits `copilot_cron_removed`, and exits.

## Intervention procedures

### FRY-SOURCE INTERVENTION

When the copilot identifies a canonical fry bug:

1. Read the relevant source files; write a reproducing test if reasonable
2. Make the minimal fix
3. Run scoped package tests, then full `go test -race ./...`
4. `make install`
5. `git add <specific files>`, then commit with `[copilot]` template:
   ```
   [copilot] <one-line summary>

   Generated by fry copilot during build <run-id> at <iso-ts>.
   Build dir: <path>
   Session:   <uuid>
   See .fry/copilot/interventions/<NNNN>.md for full context.
   ```
6. `git push origin HEAD` (current branch, no `--force`)
7. Emit `copilot_make_install`, `copilot_git_push`, `copilot_intervention_completed` events
8. Append to events.txt and write `interventions/NNNN-<slug>.md`
9. If the fix affects code the running build is currently using → restart with new binary

### ARTIFACT REMEDIATION

When the copilot identifies broken build state (not a fry bug):

1. Identify the smallest possible change that unsticks the build
2. Make the change (one edit, one command — no bundling)
3. Verify the broken state is resolved
4. Decide whether the build needs a manual kick (restart) or will recover on its next retry

### RESTART-WITH-NEW-BINARY

When a fry-source fix needs the running build to pick up the new binary:

1. Verify deployment: `cd <fry-source-dir> && go test -race ./... && make install`
2. Identify the build PID from `state-snapshot.build_pid`
3. Request graceful stop: `cd <build-dir> && fry exit`
4. Poll `.fry/.fry.lock` for release (3 min timeout, abort otherwise)
5. Sanity check binary: `which fry && fry --version`
6. Resume: `cd <build-dir> && fry run --continue` (detached)
7. Emit `copilot_build_restart`

## Attach flow for manual inspection

The session ID is printed in the startup banner and saved to `.fry/copilot/session-id.txt`. To attach from a separate terminal:

```bash
fry copilot attach
# or:
claude --resume <session-id>
# or, scriptable:
fry copilot attach --print-only | bash
```

If the copilot is mid-tick, `fry copilot attach` exits with code 3 and prints "wait ~30s or retry". Use `--print-only` to bypass the busy check and just emit the attach command.

Messages you send become part of the conversation. The copilot logs them under "external guidance" in the scratchpad and weighs them against its mission — they are informal hints, not authorization.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `copilot status` shows `STARTING` forever | bootstrap subprocess crashed before installing cron | check `.fry/copilot/bootstrap.log` |
| Session ID is `(pending)` | engine doesn't support `--session-id`; capture from stdout failed | check bootstrap.log for the result event; manifest will be updated when the agent emits it |
| `attach` fails with "session ID is not yet captured" | bootstrap is still running | wait 30s and retry, or check bootstrap.log |
| `attach` exits with code 3 | tick.lock is held by a live process | wait ~30s and retry, or use `--print-only` |
| No commits appearing on remote | push was rejected (non-fast-forward) | check intervention reports; copilot logs push failures explicitly |
| Build restarted but old behavior persists | `make install` did not actually replace the binary on PATH | run `which fry` and verify; the copilot logs this as a sanity check |

## Events

The copilot mirrors all of its events into the canonical observer event stream so `fry monitor` and `fry events --follow` see them:

```
copilot_bootstrap            copilot_intervention_started   copilot_make_install
copilot_cron_installed       copilot_intervention_completed copilot_git_push
copilot_cron_removed         copilot_intervention_failed    copilot_artifact_remediate
copilot_wake_start           copilot_fry_bug_fix            copilot_build_restart
copilot_wake_end             copilot_anomaly_detected       copilot_final_summary
copilot_wake_skipped         copilot_user_message           copilot_escalation
                                                             copilot_panic
```

Use `fry copilot tail --jsonl` for the structured stream or `fry copilot tail` for the human-readable narrative log.

## Relationship to the observer

| Concern | Observer | Copilot |
|---|---|---|
| When | Synchronous wake-points inside fry process | Asynchronous time interval, separate process |
| Authority | Read-only (writes thoughts/scratchpad) | Read-write (edits source, commits, pushes, restarts) |
| Engine | Same as build | Independent (may differ) |
| Cadence | Effort level + wake-point | Time interval (default 10m) |
| Failure | Non-fatal | Non-blocking |
| Persistence | `.fry/observer/` | `.fry/copilot/` |

The two are complementary, not redundant. Observer feeds the consciousness pipeline; copilot keeps the build moving.
