# Self-Improvement Pipeline

Fry is a self-improving codebase. An automated pipeline periodically scans the Fry source code for issues, creates GitHub Issues, implements approved items, and merges the results. Maintenance items (bugs, security, testing, documentation) are auto-approved and built immediately. Product items (features, improvements, refactors, sunset, UI/UX) require human approval via GitHub Issue labels before they are built.

## Overview

The self-improvement loop has two phases:

1. **Planning phase** — Fry scans its own codebase across 10 categories and produces a JSON list of new findings. The orchestrator creates GitHub Issues for each finding, with auto-approve or proposed status based on category.
2. **Build phase** — Fry reads the approved issues, triages each issue against the current codebase to size the work, selects 2-3 items based on that triaged effort balance, implements them in a git worktree, runs the full test suite, and either merges directly or opens a pull request.

An orchestrator script (`.self-improve/orchestrate.sh`) drives the loop. It can run manually, on a cron schedule via macOS launchd, or in CI via GitHub Actions.

Set the repo-local self-improve engine with `fry config set engine <name>`. The
orchestrator reads that setting from `.fry/config.json` unless `.self-improve/config`
explicitly overrides `PLANNING_ENGINE` or `BUILD_ENGINE`.

## Architecture

```
.self-improve/
├── orchestrate.sh          # Bash script that drives the full loop
├── executive.md            # Static directive — context for Fry about the loop
├── planning-prompt.md      # User prompt for the planning phase
├── build-prompt.md         # User prompt for the build phase
├── config                  # KEY=VALUE configuration (overrides script defaults)
├── build-journal.json      # Build history — last 30 entries (auto-generated)
├── .gitignore              # Excludes logs, lock, status, journal files
├── logs/                   # Per-run log files (timestamped)
└── 2026-03-19-roadmap.md   # Historical first roadmap (archive)
```

## GitHub Issues as Roadmap

GitHub Issues is the single source of truth for all open items. Each issue is managed via labels.
For pickup by the self-improvement loop, an issue only needs to be open and carry
`self-improve` plus either `status/proposed` or `status/approved`. Category,
priority labels are preferred metadata, not hard requirements. Effort is derived
at build time by Fry's triage step.

### Label Scheme

| Label | Purpose |
|---|---|
| `self-improve` | All Fry-managed issues get this label |
| `category/bug` | Bug fixes |
| `category/testing` | Test coverage and quality |
| `category/feature` | New capabilities |
| `category/improvement` | Enhancements to existing features |
| `category/sunset` | Dead code and vestigial features |
| `category/refactor` | Internal improvements (same behavior) |
| `category/security` | Security fixes |
| `category/ui_ux` | Terminal output and user flows |
| `category/documentation` | Documentation updates |
| `category/experience` | Build experience insights |
| `status/proposed` | Awaiting human approval |
| `status/approved` | Ready to build |
| `priority/high` | High priority |
| `priority/medium` | Medium priority |
| `priority/low` | Low priority |
| `max-attempts` | 3+ failed builds — skipped until manually reset |

### Required For Pickup

- Add `self-improve`
- Add one status label: `status/proposed` or `status/approved`
- Leave the issue open

If `category/*` or `priority/*` labels are missing, Fry normalizes them from
the issue body and title before export. It checks, in order:

1. Labels
2. Body fields such as `**Category:**` and `**Priority:**`
3. Title prefixes such as `[Bug]` or `[Documentation]`
4. Lightweight title/body heuristics

Missing priority falls back to `medium`. Missing category falls back to
`unknown`. Effort falls back temporarily to `medium` in exported issue metadata,
then Fry replaces it with a triage-sized effort before item selection.

### Category Tiers

| Tier | Categories | Behavior |
|---|---|---|
| **Auto-approve** | bug, security, testing, documentation | Created with `status/approved` — built immediately |
| **Needs approval** | feature, improvement, refactor, sunset, ui_ux, experience | Created with `status/proposed` — requires human to add `status/approved` |

### Preferred Issue Format

```markdown
**Category:** improvement
**Priority:** high
**Files:** `internal/cli/run.go:296`, `internal/config/config.go`

## Problem
<description>

## Fix Plan
<fix plan>

---
_Managed by [Fry Self-Improvement Pipeline](docs/self-improvement.md)_
```

The generated issues follow this template, but manually written issues do not
need to match it exactly. If `## Problem` or `## Fix Plan` is missing, Fry falls
back to the remaining body text and preserves the normalized body in
`assets/approved-items.json` so the build prompt can still use the raw issue
description.

### Approval Workflow

1. Planning phase creates issues with `status/proposed` label
2. You receive a GitHub notification
3. To approve: remove `status/proposed`, add `status/approved`
4. To reject: close the issue
5. Next orchestrator run picks up approved items for building

### Manual Issues

You can add your own GitHub issue to the roadmap without waiting for the
planning phase:

1. Open a normal GitHub issue with a clear title and body
2. Add `self-improve`
3. Add `status/proposed` if you want the usual approval flow, or
   `status/approved` if it is ready for the next build
4. Optionally add `category/*` and `priority/*`
5. Prefer the generated template above, but plain prose still works

Owner comments containing `approve`, `approved`, or a thumbs-up also promote a
`status/proposed` issue to `status/approved`.

### Issue Lifecycle

```
Planning discovers item
  ├── Auto-approve category → status/approved (built on next run)
  └── Product category → status/proposed (awaiting human review)
       ├── Human approves → status/approved → built on next run
       └── Human rejects → issue closed

Build succeeds
  ├── Auto-merge → issue closed with comment
  └── PR created → issue auto-closed when PR is merged (via "Closes #N")

Build fails → failure comment added
  └── 3+ failures → max-attempts label added (skipped until reset)
```

## Planning Phase

The planning phase scans the codebase for new issues across 10 categories:

| Category | Focus |
|---|---|
| Bugs | Logic errors, race conditions, unhandled errors |
| Testing | Coverage gaps, weak tests, missing edge cases |
| Features | New capabilities compatible with architecture invariants |
| Improvements | Better defaults, robustness, ergonomics for existing features |
| Sunset | Dead code, unused exports, vestigial features |
| Refactor | Same behavior, better internals |
| Security | Injection vectors, unsafe input, secrets exposure |
| UI/UX | Terminal output, error messages, user flows |
| Documentation | Stale docs, missing sections, inaccurate references |
| Experience | Build journal patterns, effort mismatches, pipeline improvements |

### When planning runs

Planning does not run every cycle. The orchestrator evaluates roadmap health:

- **Runs if** total open issues < 5, or 2+ core categories (bug/testing/feature/improvement) are empty, or any category holds > 50% of issues
- **Skips if** total issues >= 15, last build failed, or `--skip-planning` is passed
- **Never fabricates** — the prompt explicitly allows zero findings per category

### How it works

1. The orchestrator exports all open issues to `assets/existing-issues.json` for deduplication
2. Scaffolds `plans/executive.md`
3. Fry runs with the planning prompt (`--mode planning`, `--effort standard`, `--no-audit`)
4. Fry reads existing issues to avoid re-discovering known items
5. New findings are written to `output/new-findings.json`
6. The orchestrator creates a GitHub Issue for each finding with appropriate labels

## Build Phase

The build phase implements items from approved GitHub Issues.

### Item selection

Fry triages the exported approved items and chooses 2-3 based on the resulting
effort balance:

- 1 high-effort item, or
- 1 medium + 1 low, or
- 2 medium, or
- 3 low

Fry prioritizes higher-priority items with fewer prior attempts and avoids items
with vague fix plans. Sparse manual issues remain eligible when their normalized
`raw_body` still describes a clear, bounded task, and triage decides the actual
effort for selection.

### How it works

1. The orchestrator queries GitHub for approved issues (excluding `max-attempts`)
2. Exports them to `assets/approved-items.json` in the worktree, normalizing sparse metadata from labels, body fields, and title prefixes while preserving the raw body text
3. Runs a triage pass on each approved issue to derive `triage_complexity`, `triaged_effort`, and `triage_reason`; the exported `effort` field is replaced with the triaged effort for item selection
4. Fry runs with `--full-prepare --always-verify --no-project-overview`
5. Complex tasks are auto-elevated to `effort=high` for thorough audit cycles
6. Each item is implemented as a separate commit referencing the issue number
7. Documentation updates are required for every change
8. After Fry completes, `make test && make build` runs as a post-build check
9. If tests fail, an alignment agent using the configured build engine attempts to fix the failures (up to 3 attempts)
10. On success with `--auto-merge`: pull latest from remote, merge locally, push. On success without: create a PR
11. Fry writes `output/worked-items.txt` listing the issue numbers it implemented

### Post-build alignment

If `make test && make build` fails after Fry completes:

1. The orchestrator captures the failure output and the git diff
2. An agent using the configured build engine runs in the worktree with the failure context and instructions to fix
3. Tests are re-run
4. Repeats up to 3 times
5. If aligned: the fix is committed and the build proceeds to merge/PR
6. If exhausted: the build is marked as failed

## Build Journal

After every build (success or failure), the orchestrator generates a structured journal entry capturing what happened. The journal serves two purposes:

1. **Operational record** — track build outcomes, alignment rounds, and merge methods over time
2. **Pattern source** — during planning, the AI analyzes the journal to discover experience-based improvements

### Journal file

`.self-improve/build-journal.json` — A JSON array of entries, newest first. Bounded to the last 30 entries (configurable via `MAX_JOURNAL_ENTRIES`). Not committed to git.

### Entry structure

Each entry contains:

| Field | Type | Description |
|---|---|---|
| `run_id` | string | Orchestrator run ID (e.g., `20260322-131636`) |
| `date` | string | Build date (YYYY-MM-DD) |
| `outcome` | string | `success` or `failure` |
| `items_attempted` | array | Per-item details: issue number, title, category, effort, result, alignment rounds |
| `heal_rounds_total` | number | Total post-build alignment attempts used |
| `merge_method` | string | `auto-merge`, `pr`, or `none` |
| `files_changed` | array | List of files modified in the build |
| `tests_passed` | boolean | Whether `make test` passed |
| `observations` | string | AI-generated summary of notable patterns |

### AI summarization

After mechanical extraction, the orchestrator runs the configured build engine with an optional `JOURNAL_MODEL` override to analyze the build log and produce a brief observations field. The AI looks for recurring patterns, fragility, effort mismatches, and anything surprising. If summarization fails, a fallback message is used — journal generation never blocks the build.

### Experience category

During planning, if the journal exists, it is exported to `assets/build-journal.json`. The planning prompt includes **Category J: Build Experience**, which instructs the AI to analyze the journal for patterns and propose experience-based improvements. Experience findings are always created with `status/proposed` (never auto-approved) to ensure human review.

## The Orchestrator

`.self-improve/orchestrate.sh` is the glue script. It handles:

- **Lock file** — prevents concurrent runs
- **Label validation** — checks that required GitHub labels exist on startup
- **Planning trigger logic** — evaluates issue distribution to decide if planning is needed
- **Last-build-status tracking** — skips planning after a failed build to focus on building
- **Issue creation** — creates GitHub Issues with category-based auto-approve/proposed labels
- **Issue export** — exports approved issues to JSON for Fry to read, filling missing metadata from issue body/title fallbacks
- **Duplicate detection** — skips findings that match existing open `self-improve` issues by title
- **Worktree lifecycle** — create, scaffold, run, cleanup
- **Item tracking** — reads `output/worked-items.txt` manifest (issue numbers)
- **PR creation** — with `Closes #N` for auto-closing issues on merge
- **Auto-merge** — pulls latest from remote, merges locally, pushes; falls back to PR on conflict
- **Failure tracking** — comments on issues on failure, adds `max-attempts` label at threshold
- **Build journal** — generates structured journal entries after every build for experience-based planning
- **Per-run logging** — timestamped log files in `.self-improve/logs/`

### Flags

| Flag | Description |
|---|---|
| `--skip-planning` | Skip the planning phase |
| `--skip-build` | Skip the build phase |
| `--dry-run` | Run all logic except Fry invocations and git mutations |
| `--auto-merge` | Merge directly to master instead of creating a PR |

### Running manually

The orchestrator is invoked via the bash script at `.self-improve/orchestrate.sh` (run from the fry repo root):

```bash
# Full loop (planning if needed + build + PR)
./.self-improve/orchestrate.sh

# Full loop with auto-merge
./.self-improve/orchestrate.sh --auto-merge

# Build only (skip planning)
./.self-improve/orchestrate.sh --skip-planning

# Planning only (skip build)
./.self-improve/orchestrate.sh --skip-build

# Preview what would happen (no Fry invocations or git mutations)
./.self-improve/orchestrate.sh --dry-run
```

### Running on a schedule

A macOS launchd agent runs the orchestrator daily:

```
~/Library/LaunchAgents/com.fry.self-improve.plist
```

The agent runs every 24 hours when the user is logged in. If the Mac is asleep at the scheduled time, the job runs when it wakes up (with no overlap — `StartInterval` guarantees at least 24 hours between runs). The orchestrator's lock file provides an additional guard.

**Useful commands:**

```bash
# Check status
launchctl list | grep fry

# Trigger immediately
launchctl start com.fry.self-improve

# Stop
launchctl unload ~/Library/LaunchAgents/com.fry.self-improve.plist

# Restart
launchctl unload ~/Library/LaunchAgents/com.fry.self-improve.plist
launchctl load ~/Library/LaunchAgents/com.fry.self-improve.plist

# View logs
tail -f .self-improve/cron.log           # launchd output
tail -f .self-improve/orchestrate.log    # aggregate orchestrator log
ls .self-improve/logs/                   # per-run logs
```

## Fry Features Used

The self-improvement loop uses several Fry features:

| Feature | Usage |
|---|---|
| `--mode planning` | Planning phase runs in planning mode (output to `output/`) |
| `--always-verify` | Build phase forces sanity checks on all tasks regardless of effort |
| `--full-prepare` | Build phase forces full prepare so the LLM reads the approved items |
| `--no-project-overview` | Both phases skip interactive confirmation (automated) |
| `--no-audit` | Planning phase skips audit (analysis-only, no code to audit) |
| `--effort standard` | Planning phase uses standard effort (sufficient for discovery) |
| `--git-strategy current` | Both phases work on the current branch/worktree (orchestrator manages worktrees externally) |
| Worktrees | Orchestrator creates worktrees for build isolation |
| Triage gate | Auto-classifies build complexity; complex tasks auto-elevate to `effort=high` |

## Configuration

Constants at the top of `orchestrate.sh`, overridable via `.self-improve/config`:

| Constant | Default | Description |
|---|---|---|
| `MAX_BUILD_ITEMS` | 3 | Maximum items Fry can select per build |
| `MAX_ATTEMPTS` | 3 | Skip items that have failed this many times |
| `MAX_POST_BUILD_HEALS` | 3 | Alignment attempts for post-build test/build failures |
| `PLANNING_THRESHOLD` | 15 | Skip planning if this many open issues exist |
| `PLANNING_ENGINE` | repo config, else claude | Engine for the planning phase |
| `PLANNING_MODEL` | empty | Optional planning-model override |
| `BUILD_ENGINE` | repo config, else claude | Engine for the build phase, post-build heals, and journal summarization |
| `HEAL_MODEL` | empty | Optional model override for post-build heals |
| `JOURNAL_MODEL` | empty | Optional model override for build journal summarization |
| `MAX_JOURNAL_ENTRIES` | 30 | Maximum entries retained in build journal |

## Safety

- **Tests always gate merges** — `make test && make build` must pass before any merge or PR
- **Post-build alignment** — test failures get 3 attempts at automated repair before giving up
- **Lock file** — prevents concurrent orchestrator runs
- **Max attempts** — issues that fail 3 times get the `max-attempts` label and are skipped
- **Auto-merge safety** — pulls latest from remote before merging; falls back to PR on conflicts or pull failures
- **Cleanup trap** — worktrees, branches, temp files, and scaffolding are cleaned up on any exit
- **Manifest-based tracking** — only items Fry explicitly reports as worked on get status updates
- **Human approval gate** — product-direction items require explicit human approval via GitHub Issue labels
- **Duplicate detection** — planning phase checks existing issues before creating new ones
