# Effort Levels

Effort levels control how many sprints Fry generates, how dense each sprint is, and how much sanity check rigor is applied. This prevents over-engineering simple tasks (e.g., 7 sprints for a basic HTML page) and lets you dial up thoroughness for mission-critical builds.

## The Four Levels

| Level | Sprint Count | Iteration Range (per sprint) | Default Iterations | Prompt Detail | Review Behavior |
|---|---|---|---|---|---|
| `fast` | 1-2 | 10-15 | 12 | Concise, essentials only | Reviews skipped |
| `standard` | 2-4 | 15-25 | 20 | Moderate, all 7 parts but brief | Reviews optional |
| `high` | 4-10 | 15-35 | 25 | Full 7-part prompts (current default) | Normal review bias |
| `max` | 4-10 | 30-50 | 40 | Extended 9-part prompts | Thorough review, lower deviation threshold |

### `fast` — Quick Tasks

For simple, well-bounded work: a single page, a config change, a small utility, 1-3 files.

- Generates at most 2 sprints
- Skips scaffolding as a separate sprint — folds it into Sprint 1
- Sprint prompts omit REFERENCES and STUCK HINT sections for brevity
- Sprint reviews are skipped entirely, even if `@review_between_sprints` is enabled
- Sprint and build audits are skipped entirely for tasks going through the full prepare pipeline. Triaged tasks (simple/moderate) still receive a fallback build audit — see [Triage](triage.md) for the full matrix
- Focus is on core deliverables only; exhaustive edge cases are omitted

```bash
fry --effort fast --engine claude
```

### `standard` — Moderate Features

For multi-component features with some integration: 4-15 files, a few interconnected pieces.

- Generates 2-4 sprints (prefers the lower end)
- Merges layers that would be separate at higher effort (e.g., schema + domain types in one sprint)
- Full 7-part prompt structure, but each section is concise
- Essential edge cases included, exhaustive coverage skipped

```bash
fry --effort standard --engine claude
```

### `high` — Complex Systems (Default)

This is the current default behavior. For complex systems with databases, APIs, extensive testing, 15+ files.

- Generates 4-10 sprints following the standard layering order
- Full 7-part prompt structure with comprehensive detail
- All standard sizing guidelines apply
- Normal review behavior when enabled
- Sprint audits use progress-based iteration: continue as long as the fix agent resolves issues or uncovers new ones. Safety caps scale with complexity (4/8/12 outer cycles, 5/7 inner fix iterations). Stops early after 2 consecutive stale outer cycles.

```bash
fry --effort high --engine claude
# or equivalently:
fry --engine claude
```

### `max` — Mission-Critical Builds

For high-stakes projects where correctness is paramount. Same sprint count as `high`, but with significantly more rigor per sprint.

- Same sprint count as `high` (4-10), but with higher iteration budgets (30-50 per sprint)
- Sprint audits use progress-based iteration (same as `high`): continue while the fix agent is making progress. Safety caps scale with complexity (6/20/100 outer cycles, 7/10 inner fix iterations). When only LOW findings remain, one fix attempt is made before accepting (prevents indefinite cycling on acknowledged LOWs)
- Sprint prompts are extended beyond the standard 7-part structure:
  - **Part 8: Analysis & Edge Cases** — enumerates every edge case, race condition, error scenario, and boundary condition
  - **Part 9: Quality Gates** — explicit quality criteria beyond sanity checks (performance targets, security considerations, code review checklist items)
- Automatically enables `@review_between_sprints` and `@compact_with_agent`
- Alignment uses unlimited progress-based attempts (no hard cap; exits when stuck after 3 consecutive no-progress attempts or when ≤10% of checks fail after ≥10 attempts). See [Alignment](alignment.md) for full effort-level alignment behavior
- Deviation scope covers the entire epic (same as standard and high — all non-fast effort levels expand `@max_deviation_scope` to `totalSprints`, capped at 10)
- Review bias shifts from CONTINUE to THOROUGH REVIEW — the reviewer applies heightened scrutiny and recommends DEVIATE for any deviation that could affect system correctness
- No-op detection threshold is raised from 2 to 3 consecutive iterations, giving the agent more room to iterate
- A quality directive is injected into every sprint prompt at `standard`+ effort levels. At `max`, this instructs the agent to handle all edge cases, write defensive code, and validate assumptions. At all levels, the agent is told to run build/test commands and review its own diff before declaring completion (see [Sprint Execution — Quality Directive](sprint-execution.md#quality-directive-layer-175))

```bash
fry --effort max --engine claude
```

## Auto-Detection

When no `--effort` flag is specified, Fry instructs the AI to analyze the plan document and select the appropriate level automatically:

- **1-3 files** described in the plan → `fast`
- **4-15 files** → `standard`
- **15+ files** → `high`

The auto-detected level is written to the epic as an `@effort` directive so it persists across runs.

```bash
# Let the AI decide
fry --engine claude

# Check what it chose
fry --dry-run
# Output includes: Effort: standard
```

Auto-detection watches for common over-engineering signals:
- Creating separate scaffolding sprints for projects that need no scaffolding
- Splitting 3-file changes across 3+ sprints
- Adding schema/migration sprints for projects with no database
- Creating separate "wiring" sprints for simple, flat architectures

## Triage Effort Integration

When the [triage gate](triage.md) classifies a task, it also suggests an effort level. The effort resolution order is:

1. `--effort` CLI flag (if set by the user)
2. User adjustment via [interactive confirmation](triage.md#interactive-confirmation) (if the user overrides effort during the `[Y/n/a]` prompt)
3. Triage classifier suggestion
4. Default per difficulty: `fast` for simple, `standard` for moderate

**Simple and moderate tasks are capped at `high`** — if `--effort max` is passed or the triage classifier suggests max, it is automatically reduced to `high` with a log warning. Max effort is reserved for complex tasks that go through the full prepare pipeline.


This means effort level now affects behavior within each difficulty grade. For example, a simple task at standard effort gets sprint auditing (1 audit+fix pass), while a simple task at fast effort skips auditing entirely. See [Triage — Difficulty × Effort Matrices](triage.md#difficulty--effort-matrices) for the full matrix.

## Model Selection

Effort level directly affects which AI model is used for each session type. Higher effort levels use more capable (and more expensive) models. See [AI Engines — Automatic Model Selection](engines.md#automatic-model-selection-tier-system) for the full session × effort rules matrix.

In summary: `fast`/`standard` builds use **Standard**-tier models (e.g., Sonnet, gpt-5.3-codex) for most sessions, while `high`/`max` builds upgrade to **Frontier**-tier models (e.g., Opus, gpt-5.4). Lightweight tasks like project overviews always use cheaper models regardless of effort level; compaction stays cheap for `fast`/`standard`/`high`, but `max` effort upgrades compaction to the **Standard** tier (`sonnet` for Claude, `gpt-5.3-codex` for Codex).

## Usage

### CLI Flag

The `--effort` flag is available on both `fry run` and `fry prepare`:

```bash
# During a full run (prepare + execute)
fry --effort fast
fry run --effort standard --engine claude

# During preparation only
fry prepare --effort high --engine claude

# Auto-detect (default when flag is omitted)
fry --engine claude
```

### Epic Directive

The effort level is stored in the epic file as a global directive:

```
@epic My Project Phase 1
@engine claude
@effort standard
@docker_from_sprint 2
```

When both `--effort` flag and `@effort` directive are present, the **epic directive takes precedence** and the CLI flag is ignored with a warning. To change the effort level, re-run `fry prepare` with the new `--effort` value so it is baked into the epic.

### Dry Run

The dry-run report includes the effort level:

```bash
fry --dry-run
```

```
Epic: My Project Phase 1
Project dir: /path/to/project
Epic file: .fry/epic.md
Engine: claude
Effort: standard
Sprints: 1-3 of 3
```

## Validation

The epic validator enforces sprint count limits based on effort level:

| Level | Max Sprints |
|---|---|
| `fast` | 2 |
| `standard` | 4 |
| `high` | 10 |
| `max` | 10 |

If an epic has `@effort fast` but contains 5 sprints, validation will fail:

```
error: effort level "fast" allows at most 2 sprints, but epic has 5
```

This validation only applies when `@effort` is explicitly set. Epics without an `@effort` directive have no sprint count limit.

When no effort level is set (auto-detect or unset), the default max iterations per sprint is 25 (same as `high`).

## Effort Level Effects Summary

| Aspect | `fast` | `standard` | `high` | `max` |
|---|---|---|---|---|
| Sprint count cap | 2 | 4 | 10 | 10 |
| Default max iterations | 12 | 20 | 25 | 40 |
| Prompt structure | Concise | Moderate 7-part | Full 7-part | Extended 9-part |
| Scaffolding sprint | Merged into Sprint 1 | Normal | Normal | Normal |
| Review behavior | Skipped | Normal | Normal | Thorough (lower DEVIATE threshold) |
| Sprint audit | Skipped | Bounded (2-5 outer, 3 inner by complexity); LOW ignored | Progress-based (4-12 outer, 5-7 inner by complexity); LOW included | Progress-based (6-100 outer, 7-10 inner by complexity); LOW included; LOW-only → 1 fix then accept |
| Build audit | Skipped | Runs on full epic completion | Runs on full epic completion | Runs on full epic completion |
| No-op threshold | 2 iterations | 2 iterations | 2 iterations | 3 iterations |
| Quality directive | No | Yes (check work + run build/test) | Yes (quality focus + run build/test) | Yes (full rigor + run build/test) |
| Alignment attempts | 0 (skip) | 3 (fixed) | Up to 10 (progress-based, exits after 2 stuck attempts) | Unlimited up to 50 safety cap (progress-based, exits after 3 stuck attempts; mid-loop threshold check after min 10 attempts) |
| Alignment fail threshold | 20% | 20% | 20% | 10% |
| Resume alignment attempts | 6 | 6 | 20 | 6 (min) |
| Compact with agent | Default | Default | Default | Enabled |
| Observer wake-ups | Disabled | Build end only | After sprint + build audit + build end | After sprint + build audit + build end |

## Writing Mode

Effort levels apply identically in [writing mode](writing-mode.md). A `fast` effort run produces 1-2 sprints for a short document or essay; `max` produces extended sprints with thorough review and higher word-count expectations. The sprint count caps, iteration ranges, review behavior, and audit settings from the table above all apply regardless of mode.

```bash
fry --mode writing --effort fast --user-prompt "Write a short essay on testing"
fry --mode writing --effort max --user-prompt "Write a comprehensive guide to distributed systems"
```

## Backward Compatibility

- Existing epics without `@effort` work exactly as before — no behavior changes
- The `--effort` flag is entirely optional
- Auto-detection only activates during `fry prepare` (Step 2 epic generation), not on existing epics
- The `high` effort level produces identical behavior to the pre-effort-level system

## Examples

### Simple static site

```bash
# Plan describes 2 HTML pages and a CSS file
fry --effort fast --engine claude
# Result: 1 sprint, ~12 iterations, done in minutes
```

### REST API with database

```bash
# Plan describes Express + PostgreSQL + 5 endpoints
fry --effort standard --engine claude
# Result: 3 sprints (scaffold+schema, handlers, integration), ~20 iterations each
```

### Full-stack application

```bash
# Plan describes React frontend + Go backend + PostgreSQL + Docker + CI/CD
fry --engine claude
# Auto-detects: high
# Result: 6 sprints, standard sizing
```

### Payment processing system

```bash
# Correctness is critical — financial transactions, compliance requirements
fry --effort max --engine claude
# Result: 7 sprints with extended prompts, thorough reviews, 40+ iterations each
```
