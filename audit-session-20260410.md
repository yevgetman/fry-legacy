# Audit Report — Session Changes (Final)

> Scope: auto-finalize, observer opt-in, token efficiency changes from 2026-04-10 session.
> Iterations: 2 (all MODERATE+ fixed in iteration 1, verified clean in iteration 2)

## Findings

### 1. `moveDir`/`moveFile` return error on source cleanup even when archive succeeded

**Location:** `internal/archive/archive.go:69-82, 84-101`
**Severity:** MODERATE
**Description:** In the cross-filesystem fallback path, if `copyDir` succeeds but `os.RemoveAll(src)` fails, the function returned an error. The caller treats this as "auto-archive failed" but the archive IS there. This could block composability.
**Status:** FIXED — source removal failure is now a logged warning, not a returned error.

### 2. No-op early exit does not activate for sprints with zero sanity checks

**Location:** `internal/sprint/runner.go:235-248`
**Severity:** MODERATE
**Description:** Early exit required `len(checks) > 0`. Sprints without sanity checks ran the full iteration budget even with no file changes.
**Status:** FIXED — added parallel exit path for zero-check sprints with `noopThreshold+1` (one extra iteration of safety margin since we can't validate completion via checks).

### 3. Unnecessary `fmt.Sprintf` without format arguments

**Location:** `internal/sprint/prompt.go:76`
**Severity:** LOW
**Description:** `fmt.Sprintf` called with no format arguments.
**Status:** FIXED — replaced with plain string literal.

### 4. Steering cleanup called on potentially-removed worktree path

**Location:** `internal/finalize/finalize.go:51-57`
**Severity:** LOW
**Description:** After git cleanup removes a worktree, steering cleanup still targeted the removed path.
**Status:** FIXED — worktree builds now only clean the original project dir.

### 5. Branch checkout may fail with uncommitted changes on failed builds

**Location:** `internal/git/strategy.go:384-416`
**Severity:** LOW
**Description:** `RestoreBranchAfterFailure` may fail if a failed build left uncommitted changes conflicting with the original branch. Narrow edge case since fry commits after each iteration.
**Status:** ACCEPTED — documented as known limitation in godoc. Error is caught and logged as warning; fry branch preserved for inspection.
