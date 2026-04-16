# Terminal Output

Fry provides status output at every phase so you always know what it's doing, even without `--verbose`. The `--verbose` flag adds the full agent transcript to the terminal; without it, you see progress banners and status lines only.

## Output Modes

| Mode | Behavior |
|---|---|
| Default | Status banners and progress lines at every phase; full output goes to log files only |
| `--verbose` | Everything above, plus full agent transcript streamed to terminal |

## Color

When stdout is a terminal, Fry colorizes output to improve readability:

| Element | Color |
|---|---|
| Phase banners (`▶ AGENT`, `▶ TRIAGE`, `▶ AUDIT`, etc.) | Cyan |
| `PASS` statuses | Green |
| `FAIL` statuses | Red |
| Warnings (`WARNING`, `⚠`) | Yellow |
| Sprint start banners (`STARTING SPRINT`) | Bold |
| Section dividers (`──`) | Dim |
| Git checkpoint lines (`GIT:`) | Dim |

Color is **disabled** when:

- stdout is not a TTY (piped or redirected output)
- The `NO_COLOR` environment variable is set (per [no-color.org](https://no-color.org))
- `TERM=dumb`
- `--no-color` is passed

Log files (`.fry/build-logs/`) never contain ANSI escape codes regardless of color settings.

## Prepare Phase

The prepare phase logs every input it detects and which inputs feed into each generation step.

### Input detection

Before generation begins, detected inputs are announced:

```
[2026-03-10 11:49:50] User prompt detected — will be included in generation.
[2026-03-10 11:49:50] Supplementary assets detected (3 file(s) in assets/) — will be included in generation.
[2026-03-10 11:49:50] Media assets detected (5 file(s) in media/) — manifest will be included in generation.
```

When existing files are reused (not regenerated), this is logged explicitly:

```
[2026-03-10 11:49:50] Using existing plans/executive.md.
[2026-03-10 11:49:50] Using existing plans/plan.md.
```

### Bootstrap from user prompt (no plan or executive files)

When neither `plans/plan.md` nor `plans/executive.md` exist and `--user-prompt` is provided:

```
[2026-03-10 11:49:50] User prompt detected — will be included in generation.
[2026-03-10 11:49:51] Generating plans/executive.md from user prompt (engine: claude, model: sonnet)...
── Generated executive context ──────────────────────────────────
[LLM-generated executive.md content]
─────────────────────────────────────────────────────────────────
Proceed with this executive context? [y/N] y
[2026-03-10 11:50:00] Saved plans/executive.md.
```

When assets and media are also present, the bootstrap message lists them:

```
[2026-03-10 11:49:51] Generating plans/executive.md from user prompt, assets/ assets, media/ manifest (engine: claude, model: sonnet)...
```

### Triage confirmation

When no epic file exists and `--full-prepare` is not set, Fry runs the triage classifier and then shows an interactive confirmation:

```
[2026-03-10 11:49:52] ▶ TRIAGE  classifying task complexity...  engine=claude  model=haiku
[2026-03-10 11:49:54] ▶ TRIAGE  result: MODERATE  effort=standard  sprints=2 — REST endpoint with tests across 6 files.

── Triage classification ───────────────────────────────────────
Difficulty:  MODERATE
Effort:      standard
Reason:      REST endpoint with tests across 6 files.
Action:      Build 2-sprint epic programmatically (no LLM prepare)
─────────────────────────────────────────────────────────────────
Accept this classification? [Y/n/a] (a = adjust)
```

Press **Enter** or **Y** to accept. Press **a** to adjust difficulty and/or effort:

```
Accept this classification? [Y/n/a] (a = adjust) a
Difficulty [MODERATE] (simple/moderate/complex, or Enter to keep): complex
Effort [standard] (fast/standard/high/max, or Enter to keep): high
```

The confirmation is skipped with `--no-project-overview` or `--dry-run`. See [Triage — Interactive Confirmation](triage.md#interactive-confirmation) for full details.

### Project overview

After `plan.md` is available (whether user-authored or generated), Fry shows an AI-generated project overview and asks for confirmation:

```
[2026-03-10 11:51:12] Project overview: summarizing project (engine: claude, model: haiku)...

── Project summary ─────────────────────────────────────────────
Project type:    Software (REST API)
Goal:            Build a todo app with PostgreSQL backend
Expected output: Go binary with REST API, database migrations, Docker setup
Key topics:      REST API, PostgreSQL, authentication, Docker
Effort:          standard (2-4 sprints)
─────────────────────────────────────────────────────────────────
Does this look right? [Y/n/a] (a = adjust) y
```

The default is **Yes** (press Enter to accept). Use `--no-project-overview` to skip this step for automation or CI.

### Adjusting the plan

If the summary doesn't look right but you don't want to start over, type `a` to adjust:

```
Does this look right? [Y/n/a] (a = adjust) a

Prompt adjustment (describe any change, or leave blank to skip): focus on backend, skip the frontend for now
Effort level [auto] (fast/standard/high/max, or Enter to keep): high
Enable sprint review? [n] (y/n, or Enter to keep):
[2026-03-10 11:51:30] Regenerating project summary with adjustments...
[2026-03-10 11:51:32] Project overview: summarizing project (engine: claude, model: haiku)...

── Project summary ─────────────────────────────────────────────
Project type:    Software (REST API)
Goal:            Build a backend-only todo API with PostgreSQL
Expected output: Go binary with REST API, database migrations, Docker setup
Key topics:      REST API, PostgreSQL, authentication, Docker
Effort:          high (4-10 sprints)
─────────────────────────────────────────────────────────────────
Does this look right? [Y/n/a] (a = adjust) y
```

Adjustments are appended to the user prompt (or become the user prompt if none was provided) and carried through to epic and sanity check generation. You can adjust multiple times before accepting.

### Generation steps

Each step logs its start with the full list of inputs, then a completion message:

```
[2026-03-10 11:50:00] Step 0: Generating plans/plan.md from plans/executive.md, assets/ assets, media/ manifest (engine: claude, model: sonnet)...
[2026-03-10 11:51:12] Generated plans/plan.md.
[2026-03-10 11:51:12] Step 1: Generating .fry/AGENTS.md from plans/plan.md, plans/executive.md, media/ manifest (engine: claude, model: sonnet)...
[2026-03-10 11:52:05] Generated .fry/AGENTS.md.
[2026-03-10 11:52:05] Step 2: Generating .fry/epic.md from plans/plan.md, .fry/AGENTS.md, user prompt, assets/ assets, media/ manifest (engine: claude, model: sonnet)...
[2026-03-10 11:53:30] Generated .fry/epic.md.
[2026-03-10 11:53:30] Step 3: Generating .fry/verification.md from plans/plan.md, .fry/epic.md, user prompt, media/ manifest (engine: claude, model: sonnet)...
[2026-03-10 11:54:15] Generated .fry/verification.md.
```

When no user prompt, assets, or media are present and both plan files exist:

```
[2026-03-10 11:49:50] Using existing plans/executive.md.
[2026-03-10 11:49:50] Using existing plans/plan.md.
[2026-03-10 11:50:00] Step 1: Generating .fry/AGENTS.md from plans/plan.md, plans/executive.md (engine: claude, model: sonnet)...
[2026-03-10 11:52:05] Generated .fry/AGENTS.md.
[2026-03-10 11:52:05] Step 2: Generating .fry/epic.md from plans/plan.md, .fry/AGENTS.md (engine: claude, model: sonnet)...
[2026-03-10 11:53:30] Generated .fry/epic.md.
[2026-03-10 11:53:30] Step 3: Generating .fry/verification.md from plans/plan.md, .fry/epic.md (engine: claude, model: sonnet)...
[2026-03-10 11:54:15] Generated .fry/verification.md.
```

## Preflight

```
[2026-03-10 11:54:16] Preflight checks passed.
```

## Continue Mode (`--continue`)

When `--continue` is used, Fry collects build state, runs an LLM analysis agent, and displays the decision:

```
[2026-03-10 11:55:00] ▶ CONTINUE  collecting build state...
[2026-03-10 11:55:00] ▶ CONTINUE  auto-detected mode: software
[2026-03-10 11:55:01] ▶ CONTINUE  analyzing with engine=claude  model=haiku...
[2026-03-10 11:55:10] ▶ CONTINUE  decision: RESUME sprint 4 — "sanity checks failed on 2 checks; code exists"
Decision: RESUME (sprint 4)
Reason: sanity checks failed on 2 checks; code exists
```

When the build is already complete:

```
All 6 sprints already complete. Nothing to do.
```

When the build is blocked (e.g., Docker not running):

```
continue: blocked — Docker is required for sprint 4 but is not running
```

## Sprint Execution

Each sprint gets a start banner (including sanity check count), per-iteration agent banners with no-op detection, sanity check results, and a completion line:

```
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:00] STARTING SPRINT 3: Auth & Permissions
[2026-03-10 12:00:00] Max iterations: 25
[2026-03-10 12:00:00] Sanity checks: 4 applicable to this sprint
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:01] ▶ AGENT  Sprint 3/8 "Auth & Permissions"  iter 1/25  engine=claude  model=default
[2026-03-10 12:05:12] ▶ AGENT  Sprint 3/8 "Auth & Permissions"  iter 2/25  engine=claude  model=default
[2026-03-10 12:10:30] Running sanity checks...
[2026-03-10 12:10:35] Sanity checks: 4/4 checks passed.
[2026-03-10 12:10:35] SPRINT 3 PASS (2m35s)
[2026-03-10 12:10:36]   GIT: checkpoint — sprint 3 complete
```

When the agent produces no file changes, a no-op line appears:

```
[2026-03-10 12:10:01]   ITER 3: no file changes detected (1 consecutive no-op)
```

## Alignment

```
[2026-03-10 12:10:30] ▶ AGENT  Sprint 3/8 "Auth & Permissions"  align 1/3  engine=claude  model=default
[2026-03-10 12:12:00] Re-running sanity checks after alignment attempt 1...
[2026-03-10 12:12:05] Alignment attempt 1 SUCCEEDED — all checks now pass.
```

## Resume Mode

When `--resume` is used, the sprint banner indicates resume mode and skips straight to sanity checks:

```
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:00] RESUMING SPRINT 4: API Integration
[2026-03-10 12:00:00] Skipping iterations — going straight to sanity checks + alignment
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:01]   Sanity checks: 2/5 checks passed.
[2026-03-10 12:00:01]   Entering alignment loop with 6 attempts (resume mode, was 3)...
[2026-03-10 12:00:01] ▶ AGENT  Sprint 4/8 "API Integration"  align 1/6  engine=claude  model=default
[2026-03-10 12:02:30]   Re-running sanity checks after alignment attempt 1...
[2026-03-10 12:02:35]   Alignment attempt 1 SUCCEEDED — all checks now pass.
[2026-03-10 12:02:35] SPRINT 4 RESUME PASS (aligned) (2m35s)
```

When all checks already pass on resume:

```
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:00] RESUMING SPRINT 4: API Integration
[2026-03-10 12:00:00] Skipping iterations — going straight to sanity checks + alignment
[2026-03-10 12:00:00] =========================================
[2026-03-10 12:00:01]   Sanity checks: 5/5 checks passed.
[2026-03-10 12:00:01]   All checks pass — no alignment needed.
[2026-03-10 12:00:01] SPRINT 4 RESUME PASS (1s)
```

## Sprint Code Review

Sprint code reviews run by default after each sprint passes sanity checks. A single agent session reviews the sprint's changes, fixes issues above LOW severity, and re-reviews until clean.

### Clean audit (no issues):
```
[2026-03-10 12:10:36] ▶ AUDIT  sprint 3/8 "Auth & Permissions"  cycle 1/3  engine=claude
[2026-03-10 12:12:00]   AUDIT: pass (none)
```

### Issues found and fixed:
```
[2026-03-10 12:10:36] ▶ AUDIT  sprint 3/8 "Auth & Permissions"  cycle 1/3  engine=claude
[2026-03-10 12:12:00]   AUDIT: 1 HIGH, 2 MODERATE — entering fix loop (3 issues)...
[2026-03-10 12:12:01]   AUDIT FIX  cycle 1  fix 1/3 — targeting 3 issues (oldest first)
[2026-03-10 12:14:00]   AUDIT VERIFY  cycle 1  fix 1/3 — 2 of 3 resolved
[2026-03-10 12:14:01]   AUDIT FIX  cycle 1  fix 2/3 — targeting 1 issues (oldest first)
[2026-03-10 12:16:00]   AUDIT VERIFY  cycle 1  fix 2/3 — 1 of 1 resolved
[2026-03-10 12:16:01] ▶ AUDIT  sprint 3/8 "Auth & Permissions"  cycle 2/3  engine=claude
[2026-03-10 12:18:00]   AUDIT: pass (1 LOW)
```

### Issues persist (advisory, non-blocking):
```
[2026-03-10 12:20:00]   AUDIT: 2 MODERATE remain after 3 audit cycles (advisory)
```

### Issues persist and block (CRITICAL/HIGH):
```
[2026-03-10 12:20:00]   AUDIT: FAILED — 1 CRITICAL, 1 HIGH remain after 3 audit cycles
```

## Build Audit

After all sprints complete successfully, a final holistic audit runs on the entire codebase (before the build summary is generated):

### Pass (clean or LOW only):
```
[2026-03-10 13:00:00] ▶ BUILD AUDIT  running holistic audit across all 8 sprints...  engine=claude  model=sonnet
[2026-03-10 13:15:00]   BUILD AUDIT: report written to build-audit.md
[2026-03-10 13:15:00]   BUILD AUDIT: PASS (1 LOW)
[2026-03-10 13:15:01]   GIT: checkpoint — build-audit
```

### Fail (CRITICAL or HIGH remain):
```
[2026-03-10 13:15:00]   BUILD AUDIT: FAILED — 1 HIGH, 2 MODERATE remain
```

### Advisory (MODERATE remain, no CRITICAL/HIGH):
```
[2026-03-10 13:15:00]   BUILD AUDIT: 2 MODERATE remain (advisory)
```

### Audit agent fails to produce a report:
```
[2026-03-10 13:15:00] WARNING: build audit failed: run build audit: build audit session did not write build-audit.md
```

## Sprint Review and Replan

```
[2026-03-10 12:15:00] --- SPRINT REVIEW: after Sprint 3 ---
[2026-03-10 12:16:30] Review verdict: CONTINUE
[2026-03-10 12:16:30]   REVIEW: verdict CONTINUE
```

Or when a deviation is needed:

```
[2026-03-10 12:15:00] --- SPRINT REVIEW: after Sprint 3 ---
[2026-03-10 12:16:30] Review verdict: DEVIATE
[2026-03-10 12:16:30]   REVIEW: verdict DEVIATE — replanning sprints 4, 5 (risk: medium)
[2026-03-10 12:16:31] Running replanner agent...
[2026-03-10 12:18:00] Epic updated successfully.
[2026-03-10 12:18:01]   GIT: checkpoint — sprint 3 reviewed-deviate
```

## Observer

The observer emits status lines at each wake-up point. Wake-ups are effort-level gated -- see [Observer](observer.md) for the full schedule.

### Wake-up after sprint:
```
[2026-03-10 12:10:37] ▶ OBSERVER  wake=after_sprint  sprint=3/8
[2026-03-10 12:10:37]   OBSERVER: wake-up after sprint 3...  model=sonnet
[2026-03-10 12:10:50]   OBSERVER: observation complete
```

### Wake-up after build audit:
```
[2026-03-10 13:15:02] ▶ OBSERVER  wake=after_build_audit  sprint=8/8
[2026-03-10 13:15:02]   OBSERVER: wake-up after build audit...  model=sonnet
[2026-03-10 13:15:15]   OBSERVER: identity document updated
[2026-03-10 13:15:15]   OBSERVER: observation complete
```

### Final wake-up at build end:
```
[2026-03-10 13:20:01] ▶ OBSERVER  wake=build_end  sprint=8/8
[2026-03-10 13:20:01]   OBSERVER: final wake-up...  model=sonnet
[2026-03-10 13:20:12]   OBSERVER: observation complete
```

### Observer warnings (non-fatal):
```
[2026-03-10 12:10:37]   OBSERVER: agent exited with error (non-fatal): engine=claude model=sonnet exit_code=1 err=exit status 1
[2026-03-10 12:10:37]   OBSERVER: engine output:
[2026-03-10 12:10:37]   OBSERVER: authentication expired
[2026-03-10 12:10:37]   OBSERVER: parse warning: no structured tags found in response
```

## Compaction

When `@compact_with_agent` is enabled:

```
[2026-03-10 12:18:01] Compacting sprint progress with agent...  model=sonnet
```

If compaction fails, Fry records the engine, model, exit code, and a truncated preview of the agent output in the terminal log and `.fry/build-exit-reason.txt`.

## Build Archiving

After a successful full build (all sprints from 1 to the last), Fry auto-archives `.fry/` and root-level build outputs:

```
[2026-03-10 13:20:00]   ARCHIVE  build artifacts archived to /path/to/project/.fry-archive/.fry--build--20260310-132000
```

If archiving fails (non-fatal):

```
fry: warning: auto-archive failed: archive: .fry does not exist
```

Manual archiving with `fry clean`:

```
Archive .fry/ and build outputs? [y/N] y
Archived to /path/to/project/.fry-archive/.fry--build--20260310-140000
```

## Build Summary

After all sprints complete, Fry prints a summary table with the status of each sprint, then generates a summary document:

```
Effort level: standard
SPRINT  NAME                  STATUS        DURATION
1       Scaffolding           PASS          2m35s
2       Data Layer            PASS (aligned) 5m12s
3       API Handlers          PASS          3m47s

[2026-03-10 13:00:00] ▶ BUILD SUMMARY  generating...  engine=claude  model=haiku
[2026-03-10 13:02:00]   BUILD SUMMARY: complete
```

## JSON Build Report (`--json-report`)

When `--json-report` is passed, Fry writes `build-report.json` to the project root after all sprints complete. The file contains structured sprint-level results:

```json
{
  "epic_name": "My Epic",
  "start_time": "2026-03-21T10:00:00Z",
  "end_time": "2026-03-21T10:45:00Z",
  "duration_ns": 2700000000000,
  "sprints": [
    {
      "sprint_num": 1,
      "name": "Setup",
      "start_time": "...",
      "end_time": "...",
      "passed": true,
      "heal_attempts": 0,
      "verification": {
        "total_checks": 4,
        "passed_checks": 4,
        "failed_checks": 0
      },
      "token_usage": {
        "input": 1250,
        "output": 487,
        "total": 1737
      }
    }
  ]
}
```

Terminal banner when the report is written:

```
[2026-03-21 10:45:00]   BUILD REPORT: written to build-report.json
```

`build-report.json` is listed in `.gitignore` and is never committed.

## Token Summary (`--show-tokens`)

When `--show-tokens` is passed, Fry prints a per-sprint token usage table to **stderr** at the end of the run. Token counts are parsed from the sprint log files (best-effort; zero if the engine doesn't emit usage lines).

```
Sprint  Input Tokens  Output Tokens  Total
------  ------------  -------------  -----
1       1250          487            1737
2       2100          830            2930
TOTAL   3350          1317           4667
```

Token usage is also included in `build-report.json` when `--json-report` is also set.

## SARIF Export (`--sarif`)

When `--sarif` is passed, Fry writes `build-audit.sarif` alongside `build-audit.md` after the build audit completes. The file conforms to SARIF 2.1.0 and can be imported into GitHub Advanced Security or compatible tooling.

Terminal banner when the SARIF file is written:

```
[2026-03-21 10:45:00]   SARIF: build-audit.sarif written (3 findings)
```

`build-audit.sarif` is listed in `.gitignore` and is never committed.

## Log Format

All log lines follow the format:
```
[YYYY-MM-DD HH:MM:SS] message
```

Logging is thread-safe and dual-output: messages go to stdout and optionally to log files.
