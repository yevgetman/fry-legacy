# Triage Gate

The **triage gate** is a lightweight classifier that runs before the full [prepare pipeline](../README.LLM.md) when no epic file exists. It uses a single cheap LLM call to assess task complexity **and** suggest an effort level, then routes to one of three execution paths. Simple and moderate tasks skip LLM-based preparation entirely.

Triage is the **default behavior** when `fry run` auto-prepares. Use `--full-prepare` to bypass triage and force the full pipeline.

## How It Works

When you run `fry run` and no `.fry/epic.md` exists:

1. **Collect inputs** — reads `plans/plan.md`, `plans/executive.md`, and `--user-prompt` (same prerequisites as prepare)
2. **Classify** — sends a single LLM call to a cheap model (haiku/gpt-5.4-mini) with the task description
3. **Confirm** — displays the classification and asks the user to accept, decline, or adjust (see [Interactive Confirmation](#interactive-confirmation)). Skipped with `--no-project-overview` or `--dry-run`. Auto-accepted with `--yes`.
4. **Route** — based on the (possibly adjusted) classification:

| Classification | Execution path | LLM calls | Output |
|---|---|---|---|
| **SIMPLE** | Build epic programmatically (no LLM). 1 sprint, effort-aware settings. | 0 (prepare) | `.fry/epic.md` |
| **MODERATE** | Build epic programmatically (no LLM). 1-2 sprints, auto-generated sanity checks. | 0 (prepare) | `.fry/epic.md`, `.fry/verification.md` |
| **COMPLEX** | Full prepare pipeline (unchanged). | 3-4 | `.fry/epic.md`, `.fry/AGENTS.md`, `.fry/verification.md` |

## Effort Suggestion

The triage classifier suggests an effort level (`fast`, `standard`, or `high`) alongside the complexity classification. The effort resolution order is:

1. `--effort` CLI flag (if set)
2. User adjustment via [interactive confirmation](#interactive-confirmation) (if the user overrides effort during the `[Y/n/a]` prompt)
3. Triage suggestion (from classifier output)
4. Default per difficulty (fast for simple, standard for moderate)

Simple and moderate tasks are **capped at high** — `--effort max` is automatically reduced to `high` with a log warning. Max effort is reserved for complex tasks.

## Difficulty × Effort Matrices

### Simple (no prepare, 1 sprint)

| | Fast | Standard | High |
|---|---|---|---|
| **Model** | Standard | Standard | Frontier |
| **Max iterations** | 12 | 20 | 25 |
| **Alignment** | None | None | None |
| **Sprint code review** | None | 1 review pass | 1 review pass |
| **Build audit** | Single pass | Single pass | Single pass |

### Moderate (no prepare, auto-gen sanity checks, 1-2 sprints)

| | Fast | Standard | High |
|---|---|---|---|
| **Model** | Standard | Standard | Frontier |
| **Max iterations** | 12 | 20 | 25 |
| **Alignment** | None | 3 attempts | 10 + progress detection |
| **Sprint code review** | None | Default (3 iterations) | Default (3 iterations) |
| **Build audit** | Single pass | Single pass | Full |
| **Sprints** | 1 | 1-2 | 1-2 |

### Complex — unchanged, all effort levels including max

See [Effort Levels](effort-levels.md) for full complex task behavior.

## Interactive Confirmation

After classification, Fry displays the triage decision and asks the user to confirm before proceeding:

```
── Triage classification ───────────────────────────────────────
Difficulty:  MODERATE
Effort:      standard
Git:         branch
Reason:      REST endpoint with tests across 6 files.
Action:      Build 2-sprint epic programmatically (no LLM prepare)
─────────────────────────────────────────────────────────────────
Accept this classification? [Y/n/a] (a = adjust)
```

The **Git:** line shows the resolved [git strategy](git-strategy.md) (`branch`, `worktree`, or `current`). When `--git-strategy auto` (the default), the strategy is derived from the triage classification: COMPLEX -> `worktree`, SIMPLE/MODERATE -> `branch`.

- **Y / Enter** — accept the classification and continue
- **n** — decline and abort the build
- **a** — adjust difficulty and/or effort before continuing

When adjusting, you can change difficulty, effort, and/or git strategy:

```
Difficulty [MODERATE] (simple/moderate/complex, or Enter to keep):
Effort [standard] (fast/standard/high, or Enter to keep):
Git strategy [branch] (auto/current/branch/worktree, or Enter to keep):
```

Changing difficulty to COMPLEX causes the full prepare pipeline to run. Effort `max` is only allowed when difficulty is COMPLEX — typing it on SIMPLE or MODERATE triggers a warning and keeps the previous value. If you downgrade difficulty from COMPLEX to SIMPLE/MODERATE but keep `max` effort (by pressing Enter), Fry automatically downgrades the effort to `high` with a warning.

### Non-interactive mode

Use `--yes` (or `-y`) to auto-accept the triage classification without prompting. The summary is still displayed for logging purposes. Use `--no-project-overview` to skip the confirmation entirely (no display, no prompt).

The confirmation is skipped when `--no-project-overview` or `--dry-run` is passed.

## Bias Toward Complex

The classifier is intentionally biased toward COMPLEX. A complex task misidentified as simple wastes far more tokens (the build fails and must be restarted) than a simple task getting full preparation (a few extra tokens on prepare).

Signals that push toward COMPLEX:
- Databases, ORMs, migrations, or schemas
- Docker, CI/CD, or deployment
- Authentication, authorization, or security
- Multiple services or microservices
- Vague or underspecified task descriptions
- More than 10 files expected
- Multiple programming languages
- New packages with public APIs
- Architecture or design work

On any classifier failure (LLM error, unparseable output, timeout), triage defaults to COMPLEX.

## Simple Path Details

For tasks classified as SIMPLE:
- **Epic**: built programmatically in Go — no LLM call
- **Sprint**: 1 sprint, iterations set by effort level (12/20/25)
- **Alignment**: disabled (`@max_heal_attempts 0`) at all effort levels
- **Sanity checks**: skipped (no `verification.md` generated)
- **Sprint code review**: skipped at fast; 1 review pass at standard/high (`@max_review_iterations 1`)
- **Build audit**: single pass after the sprint completes
- **Git checkpoint**: runs normally

The sprint prompt is the content of `plans/plan.md` (or `plans/executive.md`, or `--user-prompt` as fallback).

## Moderate Path Details

For tasks classified as MODERATE:
- **Epic**: built programmatically in Go — no LLM call (previously used 1 LLM call)
- **Sprints**: 1-2 sprints (fast effort forces 1 sprint)
- **Sanity checks**: auto-generated from detected build system (e.g. `go build && go test`, `npm test`, `cargo test`)
- **Alignment**: effort-aware (none at fast, 3 at standard, 10 + progress detection at high)
- **Sprint code review**: effort-aware (skipped at fast, default iterations at standard, default iterations at high)
- **Build audit**: runs after sprint completion

The auto-generated sanity checks are heuristic-only — they detect `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `setup.py`, and `Makefile` and generate appropriate build+test commands.

## CLI Flags

| Flag | Description |
|---|---|
| `--full-prepare` | Skip triage and run full prepare pipeline (equivalent to pre-triage behavior) |
| `--triage-only` | Run triage classification and exit without generating any artifacts. Prints the classification result. |
| `--no-project-overview` | Skip the interactive triage confirmation (and the prepare project overview on the complex path) |

## Mode-Aware Classification

The classifier adjusts its criteria based on `--mode`:

- **software** (default): simple = 1-3 files, no integrations; moderate = 4-10 files; complex = everything else
- **planning**: simple = one-page brief; moderate = multi-section document; complex = multi-document suite
- **writing**: simple = blog post or README; moderate = article series; complex = book or documentation site

## Artifacts

| File | Purpose |
|---|---|
| `.fry/triage-prompt.md` | The classifier prompt (for inspection) |
| `.fry/triage-decision.txt` | The classifier's output (complexity, effort, sprint count, reason) |
| `.fry/build-logs/triage_*.log` | Classifier session log |

## Interaction with Other Flags

- `--effort`: takes precedence over triage suggestion. Capped to `high` for simple/moderate tasks (max reserved for complex).
- `--continue` / `--resume`: require an existing epic — triage never runs.
- `--dry-run`: skips the interactive triage confirmation. Triage runs, classification is logged, then dry-run proceeds.
- `--triage-only`: runs only the classification (and optional interactive confirmation), prints the result, and exits. No epic, sanity checks, or AGENTS.md files are generated. Cannot be combined with `--full-prepare`, `--continue`, `--resume`, or `--simple-continue`. Triage diagnostic files (`.fry/triage-prompt.md`, `.fry/triage-decision.txt`) are still written.
- `--no-audit`: disables the triage-path build audit too.
- `--no-project-overview`: skips the interactive triage confirmation and the prepare project overview on the complex path.
- `--copilot` / `--no-copilot`: when triage classifies a build as `max` effort, the [copilot](copilot.md) auto-enables on the resulting build run (the generated epic gets `@effort max` baked in, and the run-time check at the start of the sprint loop fires the bootstrap). Pass `--no-copilot` if you want triage to proceed at max effort without the copilot. Pass `--copilot` explicitly if you want the copilot regardless of triage's effort classification.

## Triage and the Copilot

If you do not pass `--effort` and let triage decide, a `complex` classification typically yields `max` effort, which in turn auto-enables the [copilot](copilot.md). This means a single `fry run` invocation against a complex project produces both:

1. A `max`-effort epic with extended sprints, thorough reviews, and full audit cycles.
2. A parallel copilot session that monitors the build and intervenes for canonical fry bugs or broken artifact state.

If you want this combination explicitly without trusting triage:

```bash
fry run --effort=max --copilot
```

If you want to opt out of the copilot even when triage decides max:

```bash
fry run --no-copilot
# or, if you also want to override the effort classification:
fry run --effort=high --no-copilot
```

See [Effort Levels](effort-levels.md#max-effort-and-the-copilot) for the full auto-enable matrix.
