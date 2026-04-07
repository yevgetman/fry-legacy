# Build Audit

The build audit is a final holistic quality gate that runs once the entire epic has completed successfully. A single AI agent session iteratively audits the full codebase, classifies issues by severity, remediates them, and re-audits until the codebase is clean. The prompt is intentionally framed as a fresh, adversarial review so the agent does not anchor on sprint-level progress notes or assume prior reviewers were correct. This complements the per-sprint audit with a cross-cutting review of the finished product.

## How It Works

```
All sprints complete successfully
       |
       v
  Build audit agent launched (single session)
       |
       +-- Audit entire codebase
       +-- Classify findings by severity
       +-- Report to build-audit.md
       +-- If clean (no issues or all LOW) --> stop
       +-- If issues remain --> remediate, then re-audit
       |       (loop up to 12 iterations at high, 100 at max effort)
       |
       v
  Git checkpoint ("build-audit")
       |
       v
  Re-run deferred sanity checks
       |
       v
  Build summary generated (includes audit results)
```

The build audit runs **before** the build summary so that audit results (pass/fail, severity counts, unresolved findings) are included in the summary. Any code changes made during remediation are committed in a git checkpoint before the summary is generated.

## Single-Agent Iterative Design

Unlike the sprint audit (which uses separate audit and fix agents), the build audit uses a **single agent session** that handles the full cycle:

1. **Audit** -- read the entire codebase and evaluate against six criteria
2. **Classify** -- assign severity to each finding
3. **Report** -- write findings to `build-audit.md` in the project root
4. **Evaluate** -- if all issues are LOW or none exist, stop
5. **Remediate** -- fix all issues (including LOW), then re-audit

The agent repeats this cycle up to 12 iterations (high effort) or 100 iterations (max effort). This design gives the agent full context across audit and fix passes, enabling more coherent remediation of cross-cutting issues.

## Deferred Failure Resolution

When sprints pass with deferred sanity check failures (checks that failed below the `@max_fail_percent` threshold), those failures are accumulated in `.fry/deferred-failures.md` and included in the build audit prompt. Fry analyzes the deferrals before the build audit runs, groups interacting failures, and tells the audit agent which failures likely share the same root cause.

Fry also writes a validation checklist to `.fry/validation-checklist.md`. This checklist captures the concrete follow-up checks the build audit should satisfy when it claims to have fixed deferred failures or cross-document inconsistencies.

After the build audit completes, Fry automatically re-runs the deferred sanity checks to determine which ones the audit agent fixed. Results are logged:

```
[2026-03-10 13:15:00]   Re-running deferred sanity checks after build audit...
[2026-03-10 13:15:01]   Sprint 3 deferred failures: ALL FIXED by build audit
[2026-03-10 13:15:01]   Sprint 5 deferred failures: 1/2 still failing
```

## When It Runs

The build audit runs only when **all** of these conditions are met:

- All sprints completed successfully (no failures)
- The full epic was executed (sprint 1 through the last sprint)
- Audit is enabled (`@audit_after_sprint` or default; not `@no_audit`)
- `--no-audit` flag was not passed
- Effort level is not `fast`

Partial sprint ranges (e.g., `fry run epic.md 3 5`) do **not** trigger the build audit, since the full epic has not been executed.

## Audit Criteria

The build audit evaluates the entire output against six criteria. The criteria vary by mode.

### Software and planning modes (default)

1. **Correctness** -- Code is coherent with the aim and function of the application; no bugs.
2. **Usability** -- No UX friction, confusing flows, or accessibility gaps.
3. **Edge Cases** -- Boundary conditions, empty states, invalid input, and race conditions are handled.
4. **Security** -- No vulnerabilities (injection, auth flaws, data exposure, etc.).
5. **Performance** -- No bottlenecks, memory leaks, or unnecessary complexity.
6. **Code Quality** -- Clean style, consistent patterns, clear naming, appropriate abstractions.

### Writing mode (`--mode writing`)

1. **Coherence** -- Content flows logically and tells a consistent story throughout.
2. **Accuracy** -- Factual claims are correct and properly supported.
3. **Completeness** -- All required topics are covered at sufficient depth.
4. **Tone & Voice** -- Writing voice is consistent and appropriate for the audience.
5. **Structure** -- Sections are well-organized with clear headings and transitions.
6. **Depth** -- Content is substantive rather than superficial or padded.

See [Writing Mode](writing-mode.md) for the full writing-mode reference.

## Severity Classification

The severity levels vary by mode.

### Software and planning modes (default)

| Level | Definition |
|---|---|
| CRITICAL | Data loss, security breach, or crash under normal use |
| HIGH | Significant bug or vulnerability; affects core functionality |
| MODERATE | Noticeable issue; degraded experience or maintainability risk |
| LOW | Minor style, naming, or cosmetic concern |

### Writing mode (`--mode writing`)

| Level | Definition |
|---|---|
| CRITICAL | Factual errors, contradictions, or missing core content |
| HIGH | Major structural problems or significant gaps in coverage |
| MODERATE | Weak transitions, inconsistent voice, or shallow treatment |
| LOW | Minor style, formatting, or word choice issues |

## Context Provided to the Audit Agent

The build audit prompt includes selected artifacts as inline context:

| Context | Source | Limit |
|---|---|---|
| Executive summary | `plans/executive.md` | First 5,000 characters |
| Implementation plan | `plans/plan.md` | First 50KB |
| Epic definition | `.fry/epic.md` | First 50KB |
| Original user prompt | `.fry/user-prompt.txt` | First 2,000 characters |
| Sprint results | In-memory results table | Full content |
| Deferred failure analysis | `.fry/deferred-failures.md` synthesized into interaction groups | When deferred failures exist |
| Validation checklist | `.fry/validation-checklist.md` | When deferred failures exist |
| Intentional divergences | `.fry/deviation-log.md` | First 10KB when present |

Artifacts deliberately excluded from the prompt:

| Artifact | Reason |
|---|---|
| Build logs (`.fry/build-logs/`) | Noisy; agent reads the codebase directly |
| Epic progress (`.fry/epic-progress.txt`) | Redundant with the epic definition and codebase |
| Build summary (`build-summary.md`) | Not yet generated (build audit runs before summary) |
| Raw deferred failure log details | Replaced by structured interaction analysis and validation checklist |

The agent reads the actual codebase directly during its audit passes, so it has full access to all source files. The prompt also includes a dedicated cross-document integrity section that explicitly asks the auditor to reconcile summary claims, tables, assumptions, and conclusions across the provided artifacts.

## Configuration

The build audit shares configuration with the sprint audit. The same directives and flags control both:

| Directive / Flag | Effect on build audit |
|---|---|
| `@no_audit` | Disables both sprint and build audits |
| `--no-audit` | Disables both sprint and build audits for this run |
| `@audit_engine` | Engine used for the build audit agent |
| `@audit_model` | Model used for the build audit agent |

No additional directives are needed -- the build audit runs automatically when the epic completes successfully with auditing enabled.

## Output

The build audit agent writes its report to `build-audit.md` in the project root. This file persists after the build and is committed in the git checkpoint.

If the agent forgets to write `build-audit.md` but its final stdout/log output still contains a structured report or review-style findings, Fry reconstructs `build-audit.md` from that output and continues. If no structured recovery is possible, the build audit fails.

The report includes:
- Location, description, severity, and recommended fix for each finding
- A verdict indicating whether the codebase passed or issues remain
- If the agent exhausted all iterations, an explanation of why issues persist

When deferred failures are present, the build also emits `.fry/validation-checklist.md` so follow-up tooling and humans can see which deferred checks the audit claimed to address.

### SARIF export (`--sarif`)

Pass `--sarif` to `fry run` to write a machine-readable `build-audit.sarif` alongside `build-audit.md`. The file conforms to [SARIF 2.1.0](https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html) and can be uploaded to GitHub Advanced Security, VS Code's SARIF viewer, or any compatible tool.

```bash
fry run --sarif
```

SARIF severity mapping:

| Audit severity | SARIF level |
|---|---|
| CRITICAL | `error` |
| HIGH | `error` |
| MODERATE | `warning` |
| LOW | `note` |

`build-audit.sarif` is listed in `.gitignore` and is not committed. It is only written when the build audit runs (i.e., the full epic completes with auditing enabled) and `--sarif` is set.

## Terminal Output

### Successful audit (pass):
```
[2026-03-10 13:00:00] ▶ BUILD AUDIT  running holistic audit across all 8 sprints...  engine=claude  model=sonnet
[2026-03-10 13:15:00]   BUILD AUDIT: report written to build-audit.md
[2026-03-10 13:15:00]   BUILD AUDIT: PASS (none)
[2026-03-10 13:15:01]   GIT: checkpoint — build-audit
```

### Issues found (advisory -- MODERATE or below):
```
[2026-03-10 13:15:00]   BUILD AUDIT: 2 MODERATE, 1 LOW remain (advisory)
```

### Issues found (blocking -- CRITICAL or HIGH):
```
[2026-03-10 13:15:00]   BUILD AUDIT: FAILED — 1 HIGH, 2 MODERATE remain
```

### Agent fails to produce the report and recovery is impossible:
```
[2026-03-10 13:15:00]   BUILD AUDIT: WARNING -- agent did not produce build-audit.md
```

## Completion Sentinel

When the build audit completes without blocking issues, Fry writes a sentinel file at `.fry/build-audit-complete`. This file enables `--continue` to detect whether a build was interrupted during the audit phase and resume from there instead of incorrectly reporting `ALL_COMPLETE`.

**How it works:**

- The sentinel is written **atomically** (write to a temp file in the same directory, then `os.Rename` to the final path) to prevent partial writes from being mistaken for a completed audit.
- The file contains an RFC 3339 timestamp recording when the audit finished.
- It is written for both the **full build audit** (all sprints complete) and the **triage single-pass audit**.
- It **is** written when the audit finds only advisory (non-blocking) issues, since those do not prevent the build from being considered audited.
- It is **not** written if the audit errors out, finds blocking issues, or is skipped with `--no-audit`.
- When the epic is configured to skip auditing (e.g., `@no_audit` directive, or fast effort without `--always-verify`), the collector sets `BuildAuditComplete = true` so the analyzer never returns `AUDIT_INCOMPLETE` in the first place.
- When `--no-audit` is passed as a CLI flag to `fry run --continue`, the collector still sees the audit as configured (because `@audit_after_sprint` is set in the epic) and the sentinel file is absent, so `HeuristicAnalyze` returns `AUDIT_INCOMPLETE`. The run command then handles this by skipping the audit phase and returning complete, rather than re-running the audit.

**Resume behavior:**

When `fry run --continue` finds all sprints complete but the sentinel file is absent, the analyzer returns `AUDIT_INCOMPLETE` instead of `ALL_COMPLETE`. The run command then resumes from the build audit phase without re-running any sprints.

**Backward compatibility:**

A `.fry/` directory from a build that predates this feature will not contain the sentinel file. This is treated as audit-incomplete (safe default), so `--continue` will re-run the build audit rather than incorrectly declaring the build complete.

## Build Logs

The build audit session is logged to `.fry/build-logs/`:

```
build_audit_20060102_150405.log
```

## Effort Level Interaction

- **`fast`** -- Build audit is skipped entirely, matching sprint audit behavior.
- **`standard`**, **`high`**, **`max`** -- Build audit runs when the epic completes successfully.
- **`max`** also auto-enables the [copilot](copilot.md). When the build audit runs, the copilot's most recent state-snapshot tick will see the `build_audit_done` event and may decide to intervene if the audit reveals canonical fry bugs that the copilot can fix in the fry source tree itself. Build-audit findings about the project's own code are not in the copilot's authority — only fry-source bugs and broken artifact state.

## Relationship to Sprint Audit

| Aspect | Sprint Audit | Build Audit |
|---|---|---|
| Scope | Single sprint's changes | Entire codebase |
| Timing | After each sprint passes sanity checks | After all sprints complete |
| Agent design | Two agents (audit + fix) | Single agent (audit + fix in one session) |
| Iterations | Up to `@max_audit_iterations` (default: 3) | Up to 12 (standard/high) or 100 (max) |
| Blocking | CRITICAL/HIGH block the sprint | Non-blocking (advisory) |
| Output file | `.fry/sprint-audit.txt` (transient) | `build-audit.md` (persisted) |
| Context | Sprint diff + sprint progress | Full codebase + plan artifacts |

Both audits use the same six criteria (mode-dependent) and four severity levels. The sprint audit catches issues incrementally during the build; the build audit catches cross-cutting issues that only become visible when viewing the completed project as a whole.

## Reporting Failure Distinction

When the core build completes successfully but post-build reporting (build audit or summary generation) fails, Fry sets the build status to `completed_with_reporting_failure` instead of `completed`. This distinguishes:

- **`completed`** — All sprints passed and all post-build reporting succeeded.
- **`completed_with_reporting_failure`** — All sprints passed but build audit and/or summary generation failed (e.g., quota exhaustion, model unavailability).
- **`failed`** — A sprint failed during execution.

The `reporting_failure` field in `build-status.json` records which stage failed (`build_audit`, `summary`, or `build_audit+summary` if both) and the error message. This allows operators and automation to immediately see whether Fry failed to build or failed to narrate the build.

## Quota-Aware Fallback

When the build audit or summary generation fails with a transient engine error (quota exhaustion, rate limit, server error, timeout, or network failure), Fry automatically retries once with the fallback engine before recording a reporting failure. The fallback uses the same engine pairing as sprint execution (Claude &harr; Codex by default, or the `--fallback-engine` override).

The fallback is one-shot and does not permanently pin the engine — it only affects the current reporting stage. If both primary and fallback fail, the original error is recorded in the `reporting_failure` field.

## Resumable Reporting State

Fry persists durable artifacts so that failed final-stage reporting can be retried without reconstructing everything from raw logs:

- **`.fry/build-audit-prompt.md`** — The exact assembled prompt is preserved after invocation (not deleted). A later retry can re-read and re-invoke this prompt directly.
- **`.fry/summary-prompt.md`** — Same: preserved for the summary generation stage.
- **`.fry/rolling-results.json`** — A compact JSON array of per-sprint outcomes (number, name, status, duration) updated after every sprint completion. Provides durable structured input for final-stage prompt assembly even if the build is interrupted and resumed.
