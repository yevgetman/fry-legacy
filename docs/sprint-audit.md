# Sprint Code Review

The sprint code review is a semantic quality gate that runs after each sprint passes sanity checks. A single AI agent session reviews the sprint's changes, classifies issues by severity, fixes everything above LOW, and re-reviews until clean — all within one uninterrupted session. This eliminates the context handoff overhead of the multi-agent pattern and gives the reviewing agent full continuity across review and fix passes. This complements the syntactic sanity check system (`@check_file`, `@check_cmd`, etc.) with deeper, AI-driven review.

## How It Works

```
Sprint passes sanity checks
       │
       ▼
  Classify sprint diff complexity (low/moderate/high)
       │
       ▼
  Build review prompt with sprint context
       │
       ▼
  Single agent session (one engine call)
       │
       ├─── Pass 1: Review ────────────────────────┐
       │  Read diff, apply review criteria           │
       │  Classify findings: CRITICAL/HIGH/MOD/LOW   │
       │  Write to .fry/sprint-review.txt            │
       │  Exit check: nothing above LOW? → STOP      │
       └────────────┬────────────────────────────────┘
                    │ findings above LOW exist
                    ▼
       ┌─── Fix ────────────────────────────────────┐
       │  Fix all CRITICAL/HIGH/MODERATE issues       │
       │  Minimal targeted changes only               │
       │  Do not touch LOW issues                     │
       └────────────┬───────────────────────────────┘
                    │
                    ▼
       ┌─── Pass 2+: Re-Review (Verification) ─────┐
       │  Re-review codebase including fixes          │
       │  Catch regressions introduced by fixes       │
       │  Catch new issues exposed by fixes           │
       │  Update .fry/sprint-review.txt               │
       │  Exit check again                            │
       └────────────┬───────────────────────────────┘
                    │
                    ▼
       Pass N or Convergence
       │
       ├─ CONVERGED: no issues above LOW → STOP
       └─ ITERATION_LIMIT: max iterations reached → STOP
              │
              ▼
       Final .fry/sprint-review.txt written with:
       • Remaining findings
       • Review history (per-pass found/fixed counts)
       • Metadata (iterations, convergence status)
       • Verdict
```

The review runs **after** sanity checks pass but **before** the git checkpoint, so that the checkpoint commits both the sprint's work and any review fixes in one clean commit.

## Single-Session Design

The code review uses a **single agent session** that handles the full review-fix-verify cycle internally. The agent:

1. **Reviews** the sprint diff against mode-specific criteria
2. **Classifies** every finding as CRITICAL, HIGH, MODERATE, or LOW
3. **Reports** findings to `.fry/sprint-review.txt`
4. **Checks** the exit condition — if no findings above LOW remain, stops
5. **Fixes** all CRITICAL, HIGH, and MODERATE issues with minimal, targeted changes
6. **Loops** back to step 1 to re-review (which serves as verification)

The re-review pass acts as verification: it confirms fixes worked, catches regressions from fixes, and catches new issues exposed by the changes. This continues until convergence (nothing above LOW) or the iteration limit.

### Why single-session

The previous multi-agent design (separate auditor → fixer → verifier) suffered from context loss during handoffs between sessions. The single-session approach gives the agent full continuity — it remembers what it found, what it fixed, and why, without needing compressed carry-forward summaries.

### Iteration tracking

The agent reports structured metadata in its output:

- **Iterations completed** — how many review passes were performed
- **Convergence status** — `CONVERGED` (exit condition met) or `ITERATION_LIMIT` (max iterations exhausted)
- **Review history** — per-pass breakdown of found and fixed counts by severity

Fry parses this metadata to populate the review result. When metadata is absent (e.g., agent omitted it), Fry falls back gracefully.

### Finding deduplication

Findings are deduplicated by normalized location + description keys. When duplicates exist, the higher severity is kept. This prevents the same issue from being counted multiple times across review passes.

## Blocking vs Advisory

After the review session completes, the outcome depends on the remaining findings:

- **CRITICAL or HIGH** — The sprint **fails** and the epic stops
- **MODERATE** — The sprint **continues** with an advisory warning
- **LOW or none** — The review passes cleanly

## Configuration

Sprint code reviews are **enabled by default**.

```
@max_review_iterations 5
@review_engine claude
@review_model claude-sonnet-4-20250514
```

To disable code reviews: `@no_review`

## Epic Directives

| Directive | Description |
|---|---|
| `@review_after_sprint` | Enable post-sprint code review (default: enabled) |
| `@no_review` | Disable post-sprint code review |
| `@max_review_iterations <N>` | Maximum review iterations per sprint (default: 3) |
| `@review_engine <codex\|claude\|ollama>` | AI engine for code review session (default: same as `@engine`) |
| `@review_model <model>` | Model override for code review session |

## CLI Flags

| Flag | Description |
|---|---|
| `--no-audit` | Disable sprint code review and build audit for this run |

## Severity Classification

| Level | Description (software) | Action | If unresolved |
|---|---|---|---|
| CRITICAL | Data loss, security breach, or crash under normal use | Agent fixes in-session | **Blocks** sprint |
| HIGH | Significant bug; affects core functionality | Agent fixes in-session | **Blocks** sprint |
| MODERATE | Edge case gaps, poor error handling, quality concerns | Agent fixes in-session | Advisory warning |
| LOW | Style, naming, cosmetic | Not fixed (accepted as-is) | Non-blocking |

## Review Criteria

### Software and planning modes (default)

1. **Correctness** — Does the code do what the sprint goals require?
2. **Usability** — Are APIs, CLIs, and interfaces intuitive and consistent?
3. **Edge Cases** — Are boundary conditions and error paths handled?
4. **Security** — Are there injection, auth, or data-exposure risks?
5. **Performance** — Are there obvious bottlenecks or resource leaks?
6. **Code Quality** — Is the code readable, well-structured, and idiomatic?

### Writing mode (`--mode writing`)

1. **Coherence** — Logical flow between sections
2. **Accuracy** — Factual correctness
3. **Completeness** — All topics covered at appropriate depth
4. **Tone & Voice** — Consistent register
5. **Structure** — Clear headings, logical ordering
6. **Depth** — Sufficient detail and analysis

## Review Output Format

The agent writes findings to `.fry/sprint-review.txt`:

```
## Summary
Brief overview of the review results.

## Findings
- **Location:** src/handler.go:42
- **Description:** SQL query uses string concatenation instead of parameterized queries
- **Severity:** HIGH
- **Category:** product_defect
- **Recommended Fix:** Use db.Query with $1 placeholders

- **Location:** src/auth.go:15
- **Description:** Variable name `x` is unclear
- **Severity:** LOW
- **Category:** product_defect
- **Recommended Fix:** Rename to `tokenExpiry`

## Verdict
PASS (no issues or all LOW) or FAIL (CRITICAL/HIGH/MODERATE found)

## Review Metadata
- Iterations completed: 3
- Convergence: CONVERGED | ITERATION_LIMIT

## Review History
### Pass 1
Found: 2 CRITICAL, 1 HIGH, 0 MODERATE, 0 LOW
Fixed: 2 CRITICAL, 1 HIGH, 0 MODERATE
### Pass 2
Found: 0 CRITICAL, 0 HIGH, 1 MODERATE, 0 LOW
Fixed: 0 CRITICAL, 0 HIGH, 1 MODERATE
### Pass 3
Found: 0 CRITICAL, 0 HIGH, 0 MODERATE, 1 LOW
```

If the agent fails to write `.fry/sprint-review.txt`, Fry attempts to recover findings from the agent's raw output. A quality gate prevents false positives from being accepted during recovery. When recovery is used, the result is flagged as recovered.

## Complexity Classification

Before the review begins, Fry classifies the sprint diff complexity as `low`, `moderate`, or `high` based on heuristic analysis (numeric token density, table-row density, domain-specific signal keywords). Moderate and high complexity sprints receive an additional **figure reconciliation** check in the review prompt that instructs the agent to verify numerical claims against their source calculations.

## Context Provided to the Review Agent

| Context | Source | Limit |
|---|---|---|
| Codebase context | `.fry-config/codebase.md` | First 8,000 characters |
| Codebase memories | `.fry-config/codebase-memories/*.md` | Up to 10KB total |
| Executive summary | `plans/executive.md` | First 2,000 characters |
| Sprint goals | `@prompt` block from the epic | Full content |
| What was done | `.fry/sprint-progress.txt` | First 50KB |
| Code changes | `git diff` of sprint work | First 100KB |
| Intentional divergences | `.fry/deviation-log.md` filtered to the active sprint | When relevant |

## Effort Level Interaction

- **`fast`** — Sprint code reviews are skipped entirely (unless `--always-verify` is passed).
- **`standard`** — Default review iterations (3). LOW findings are non-blocking.
- **`high`** — Default review iterations (3). LOW findings are noted at high effort.
- **`max`** — Default review iterations (3). LOW findings are noted.

When `@max_review_iterations` is explicitly set, it overrides the default regardless of effort level.

## Metrics

Review metrics track:

- **Total calls** — 1 (single-session design)
- **Duration** — wall-clock time for the review session
- **Prompt bytes** — size of the assembled review prompt
- **Output bytes** — size of the agent's response
- **Model** — which model was used
- **Final finding count** — number of findings after deduplication
- **Content complexity** — classified complexity tier

These are written to `.fry/build-logs/sprintN_review_TIMESTAMP.log` and surfaced in `.fry/build-status.json` under `sprints[].audit`.

## Relationship to Build Audit

| Aspect | Sprint Code Review | Build Audit |
|---|---|---|
| Scope | Single sprint's changes | Entire codebase |
| Timing | After each sprint passes sanity checks | After all sprints complete |
| Agent design | Single session (review + fix in one call) | Single session (audit + fix in one call) |
| Iterations | Up to `@max_review_iterations` (default: 3) | Up to 12 (standard/high) or 100 (max) |
| Blocking | CRITICAL/HIGH block the sprint | Non-blocking (advisory) |
| Output file | `.fry/sprint-review.txt` (transient) | `build-audit.md` (persisted) |
| Context | Sprint diff + sprint progress | Full codebase + plan artifacts |

Both use the same six criteria (mode-dependent) and four severity levels. The sprint code review catches issues incrementally during the build; the build audit catches cross-cutting issues that only become visible when viewing the completed project as a whole.
