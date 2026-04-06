# Sprint Audit

The sprint audit is a semantic quality gate that runs after each sprint passes sanity checks. It uses a two-level loop: an outer loop discovers issues via fresh audit scans, and an inner loop fixes and verifies them before the next scan. Issues are tracked across cycles and prioritized FIFO (oldest first). The fix agent receives all findings at once with full context. There is no mechanical scope validation — the audit agent (AI) verifies whether fixes are adequate, not a file-path matching rule. This complements the syntactic sanity check system (`@check_file`, `@check_cmd`, etc.) with deeper, AI-driven review.

## How It Works

```
Sprint passes sanity checks
       │
       ▼
  Outer loop cycle 1 — FRESH AUDIT SCAN
       │
       ├─ Audit agent reviews codebase (read-only)
       │       │
       │       ├─ PASS (no MODERATE+ issues) → continue to git checkpoint
       │       │
       │       └─ FAIL (CRITICAL/HIGH/MODERATE found)
       │               │
       │               ▼
       │         Inner loop — FIX-THEN-VERIFY (FIFO, finding set frozen)
       │               │
       │               ├─ Fix agent receives ALL unresolved findings
       │               ├─ Verify-audit agent checks ONLY the original findings
       │               ├─ If all MODERATE+ resolved → exit inner loop
       │               └─ Repeat up to inner cap
       │
       ▼
  Outer loop cycle 2 — FRESH AUDIT SCAN for NEW issues
       │
       ├─ Classifies findings: resolved / persisting / new
       │       │
       │       ├─ Only LOWs remain → PASS
       │       └─ New MODERATE+ found → inner fix loop again
       │
       └─ Repeat until pass or outer cap reached
               │
               ├─ CRITICAL or HIGH → sprint FAILS, epic stops
               └─ MODERATE → advisory warning, build continues

  Final LOW pass (max effort only)
       │
       └─ One fix attempt for remaining LOWs — non-blocking
```

The audit runs **after** sanity checks pass but **before** the git checkpoint, so that the checkpoint commits both the sprint's work and any audit fixes in one clean commit.

## Two-Level Loop Design

### Outer loop (fresh audit scans)

Each outer cycle runs a **fresh audit agent** to scan the codebase for sprint-related issues. This is the **only** place where new issues are discovered.

On cycle 2+, findings are classified against the previous cycle's known set:
- **Resolved** — previously known issue no longer found
- **Persisting** — previously known issue still present (keeps original cycle number for FIFO ordering)
- **New** — issue not seen in previous cycles

A **resolved-finding ledger** tracks all resolved findings across the audit lifetime and is included in the audit prompt so the agent avoids re-raising fixed issues.

### Inner loop (fix-then-verify per finding set)

When the outer loop discovers MODERATE+ findings, the inner loop works to resolve them:

1. **Fix agent** receives ALL unresolved findings from the current finding set with full context (codebase description, sprint goals, progress, git diff, resolved themes, previous fix attempts). The fix agent is prompted to focus on sprint scope but is free to touch any files needed for the fix — there is no mechanical file-path restriction.

2. **Verify-audit agent** checks ONLY the original finding set (FIFO discipline). It does not scan for new issues — that's the outer loop's job. It reports each finding as `RESOLVED`, `PARTIALLY_RESOLVED`, `BEHAVIOR_UNCHANGED`, `EVIDENCE_INCONCLUSIVE`, `BLOCKED`, or `STILL_PRESENT`.

3. Resolved findings are removed from the set. If MODERATE+ remain, back to step 1.

4. When all MODERATE+ findings in the set are resolved, the inner loop exits and the outer loop runs a fresh audit scan.

**FIFO discipline:** The inner loop's finding set is frozen when it starts. New issues cannot be added mid-loop. Old issues are persistently addressed first. This prevents the churn pattern where new findings accumulate while old ones remain unresolved.

### Inner-loop efficiency

- **No-op detection** — Fry fingerprints the worktree before and after each fix pass. If the fix agent made no material file changes, Fry logs a no-op and increments the stale counter.
- **Fix attempt history** — The fix prompt includes summaries of prior attempts that targeted the same findings, including verification notes. This helps the fix agent avoid repeating failed approaches.
- **Behavior-unchanged handling** — If verify reports `BEHAVIOR_UNCHANGED` for a finding across 3+ iterations, the inner loop exits early to let the outer loop try a fresh perspective.
- **Session continuity** — On Claude and Codex, Fry reuses same-role sessions within audit cycles (audit continuity across outer cycles, fix continuity within one inner loop). Sessions are refreshed when they exceed configured call/prompt/token budgets.

### Issue tracking across cycles

Each finding has a stable identity:
- **Finding key** — normalized file location plus description
- **Affected files** — file targets derived from the finding location
- **Origin cycle** — which outer audit cycle discovered it (for FIFO ordering)
- **Last seen cycle** — the most recent audit cycle that observed it
- **Resolution status** — whether it has been verified as resolved

### Blocker categories

Findings can be classified as:

- `product_defect`
- `environment_blocker`
- `harness_blocker`
- `external_dependency_blocker`

Blocker categories are **informational only** — they help understand the nature of the finding but do not prevent the fix agent from attempting remediation. The fix loop still runs for all findings regardless of category.

## Metrics and Status

Sprint audit metrics track:

- **Total calls** — audit, fix, and verify agent invocations
- **Duration** — total wall-clock time spent in audit
- **No-op fix calls** — fix calls that produced no file changes
- **No-op rate** — proportion of fix calls that were no-ops
- **Verify calls and resolutions** — how many verify passes ran and how many findings were confirmed resolved
- **Verify yield** — resolutions per verify call
- **Session refreshes** — how many times session continuity was reset
- **Cycle summaries** — per-cycle snapshots with fix yield, verify yield, and milliseconds per resolution

These are written to `.fry/build-logs/sprintN_audit_metrics.json` and surfaced in `.fry/build-status.json` under `sprints[].audit.metrics`.

## Blocking vs Advisory

After the audit loop exhausts its cycles, the outcome depends on the remaining findings:

- **CRITICAL or HIGH** — The sprint **fails** and the epic stops
- **MODERATE** — The sprint **continues** with an advisory warning. This prevents moderate semantic disagreements from stalling the entire build.
- **LOW or none** — The audit passes cleanly. At **max** effort, LOW-only findings trigger one fix agent attempt before accepting.

## LOW-Only Exit Behavior

When an audit finds only LOW-severity issues (no CRITICAL, HIGH, or MODERATE):

| Effort | LOW-only result | Behavior |
|---|---|---|
| fast / standard / high | Immediate pass | No fix attempt; LOW findings are non-blocking |
| max | Single fix attempt, then pass | One fix agent pass targets the LOW findings. No re-audit — the result is accepted regardless. |

## Configuration

Sprint audits are **enabled by default**.

```
@max_audit_iterations 5
@audit_engine claude
@audit_model claude-sonnet-4-20250514
```

To disable audits: `@no_audit`

## Epic Directives

| Directive | Description |
|---|---|
| `@audit_after_sprint` | Enable post-sprint audit (default: enabled) |
| `@no_audit` | Disable post-sprint audit |
| `@max_audit_iterations <N>` | Maximum outer audit cycles per sprint (default: 3) |
| `@audit_engine <codex\|claude>` | AI engine for audit/fix sessions (default: same as `@engine`) |
| `@audit_model <model>` | Model override for audit/fix sessions |

## CLI Flags

| Flag | Description |
|---|---|
| `--no-audit` | Disable sprint and build audits for this run |

## Severity Classification

| Level | Description (software) | Action | If unresolved |
|---|---|---|---|
| CRITICAL | Data loss, security breach, or crash under normal use | Fix agent remediates | **Blocks** sprint |
| HIGH | Significant bug; affects core functionality | Fix agent remediates | **Blocks** sprint |
| MODERATE | Edge case gaps, poor error handling, quality concerns | Fix agent remediates | Advisory warning |
| LOW | Style, naming, cosmetic | Fix agent remediates (high/max effort) | Non-blocking |

## Audit Criteria

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

## Audit Output Format

The audit agent writes findings to `.fry/sprint-audit.txt`:

```
## Summary
Brief overview of the audit results.

## Findings
- **Location:** src/handler.go:42
- **Description:** SQL query uses string concatenation instead of parameterized queries
- **Severity:** HIGH
- **Recommended Fix:** Use db.Query with $1 placeholders

- **Location:** src/auth.go:15
- **Description:** Variable name `x` is unclear
- **Severity:** LOW
- **Recommended Fix:** Rename to `tokenExpiry`

## Verdict
FAIL (HIGH issues found)
```

If the agent forgets to write `.fry/sprint-audit.txt` but its output contains structured findings, Fry reconstructs the file and continues.

## Context Provided to Audit Agent

| Context | Source | Limit |
|---|---|---|
| Codebase context | `.fry-config/codebase.md` | First 8,000 characters |
| Codebase memories | `.fry-config/codebase-memories/*.md` | Up to 10KB total |
| Executive summary | `plans/executive.md` | First 2,000 characters |
| Sprint goals | `@prompt` block from the epic | Full content |
| What was done | `.fry/sprint-progress.txt` | First 50KB |
| Code changes | `git diff` of sprint work | First 100KB |
| Previously identified issues | Findings from prior audit cycles | Cycle 2+ only |
| Resolved themes | Previously resolved findings | Cycle 2+ only |
| Intentional divergences | `.fry/deviation-log.md` filtered to the active sprint | When relevant |

## Context Provided to Fix Agent

The fix prompt includes full audit context so the fix agent has the same understanding as the audit agent:

| Context | Source |
|---|---|
| Target file content (inlined) | Actual file content for each finding's target files (up to 8 KB per file) |
| Sprint goals | `@prompt` block from the epic |
| Issues to fix | All unresolved findings, FIFO ordered (oldest first) |
| Previous fix attempts | Prior attempts with verification notes |
| Resolved themes | Do not re-break previously resolved issues |
| Codebase context | Architecture guide and project learnings |

The fix agent is instructed to focus on the listed issues and preserve unrelated behavior.

## Context Provided to Verify Agent

After each fix, a verify agent checks resolution:

| Context | Source |
|---|---|
| Issues to verify | Numbered list of the current finding set with location and severity |
| Instructions | Check each issue and report status. Do not scan for new issues. |

The verify agent does not look for new issues and does not modify source code.

## Effort Level Interaction

- **`fast`** — Sprint audits are skipped entirely.
- **`standard`** — Bounded audit with complexity-aware caps. 2-5 outer cycles, 3-4 inner iterations depending on complexity. LOW findings excluded from fix scope.
- **`high`** — Higher caps. 4-12 outer cycles, 5-7 inner iterations. **LOW findings included** in fix scope.
- **`max`** — Largest budget. 6-100 outer cycles, 7-10 inner iterations. **LOW findings included** in fix scope.

When `@max_audit_iterations` is explicitly set, it is always respected as the outer cycle cap regardless of effort level.
