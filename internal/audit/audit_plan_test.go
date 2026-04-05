package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	frygit "github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/review"
)

func TestBuildVerifyPromptRequestsNotes(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prompt := buildVerifyPrompt(opts, []Finding{{Description: "Issue A", Severity: "HIGH"}})

	assert.Contains(t, prompt, "**Notes:**")
	assert.Contains(t, prompt, "BEHAVIOR_UNCHANGED")
	assert.Contains(t, prompt, "exact logic path")
}

func TestAuditPromptIncludesReconciliationDirective(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.Mode = "writing"
	opts.Complexity = ComplexityHigh

	prompt := buildAuditPrompt(opts, nil, nil, nil)
	assert.Contains(t, prompt, "Priority Check: Figure Reconciliation")
	assert.Contains(t, prompt, "Trace each claim to its source calculation")
}

func TestAuditPromptIncludesRelevantDeviations(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	require.NoError(t, review.AppendDeviationLog(opts.ProjectDir, review.DeviationLogEntry{
		SprintNum:       1,
		SprintName:      "Setup",
		Verdict:         review.VerdictDeviate,
		Trigger:         "The pricing appendix remains authoritative over the summary.",
		Impact:          "- Preserve the appendix numbers.\n",
		RiskAssessment:  "Low risk with a reconciliation note.",
		AffectedSprints: []int{1},
	}))

	prompt := buildAuditPrompt(opts, nil, nil, nil)
	assert.Contains(t, prompt, "Known Intentional Divergences")
	assert.Contains(t, prompt, "pricing appendix remains authoritative")
}

func TestRunAuditLoopSkipsVerifyOnNoOp(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			// Per-cluster fast-fail: 1 fix call rejected → all clusters skipped
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), highFindings)
			}
		},
	}
	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	for _, prompt := range eng.prompts {
		assert.NotEqual(t, config.AuditVerifyInvocationPrompt, prompt)
	}
}

func TestRunAuditLoopVerifiesAlreadyFixedClaimOnNoOp(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			"",
			"No changes needed. This issue is already fixed.",
			"",
			"",
		},
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), highFindings)
			case 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** RESOLVED\n- **Notes:** guard already present in the current code path\n")
			case 3:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
			}
		},
	}
	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.NotNil(t, result.Metrics)
	require.Len(t, result.Metrics.Calls, 3) // audit + fix(verify_only) + verify
	assert.Equal(t, fixValidationVerifyOnly, result.Metrics.Calls[1].ValidationResult)
	assert.True(t, result.Metrics.Calls[1].AlreadyFixedClaim)
	assert.Contains(t, eng.prompts, config.AuditVerifyInvocationPrompt)
}

func TestRunAuditLoopRejectsCommentOnlyFixBeforeVerify(t *testing.T) {
	t.Parallel()

	findings := "## Findings\n- **Location:** tracked.go:1\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Recommended Fix:** Add a nil guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			// Per-cluster fast-fail: 1 fix call rejected → cluster skipped → final audit at callIndex 2
			case 0, 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "tracked.go"), []byte("package main\n\n// comment only\n"), 0o644))
			}
		},
	}
	opts := makeOpts(t, eng)
	writeFile(t, filepath.Join(opts.ProjectDir, "tracked.go"), "package main\n")
	require.NoError(t, frygit.InitGit(context.Background(), opts.ProjectDir))
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, result.Passed)
	require.NotNil(t, result.Metrics)
	assert.Equal(t, 1, result.Metrics.Snapshot().RejectedFixCalls)
	for _, prompt := range eng.prompts {
		assert.NotEqual(t, config.AuditVerifyInvocationPrompt, prompt)
	}
}

func TestRunAuditLoopAttemptsFixForEnvironmentBlocker(t *testing.T) {
	t.Parallel()

	blockerReport := "## Findings\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			// Every audit cycle returns the same blocker — the fix agent
			// can't resolve it, so stale detection stops the loop.
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), blockerReport)
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, result.Passed)
	assert.True(t, result.Blocking)
	// Blocker categories are informational — the fix loop still ran (attempted remediation).
	// The loop converged via stale detection, not via blocker-only early exit.
	assert.Greater(t, eng.callIndex, 1, "fix loop should have been entered for environment blocker")
}

func TestRunAuditLoopIncludesBlockerFindingsInFixScope(t *testing.T) {
	t.Parallel()

	report := "## Findings\n- **Location:** src/api.go:20\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Category:** product_defect\n- **Recommended Fix:** handle the returned error\n\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n"
	var allFixPrompts []string
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			// Capture every fix prompt
			if data, err := os.ReadFile(filepath.Join(projectDir, config.AuditPromptFile)); err == nil {
				allFixPrompts = append(allFixPrompts, string(data))
			}
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), report)
			default:
				// All subsequent calls: return same blocker-only findings
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "## Findings\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n")
			}
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, result.Passed)
	// Blocker categories are informational — all findings enter the fix scope.
	// Per-cluster dispatch may put them in separate clusters, so check across
	// all fix prompts that the SUPABASE finding was presented to the fix agent.
	combinedPrompts := strings.Join(allFixPrompts, "\n")
	assert.Contains(t, combinedPrompts, "SUPABASE", "blocker findings should be included in fix scope")
}

func TestRunAuditLoopFixHistoryIntegration(t *testing.T) {
	t.Parallel()

	var secondFixPrompt string
	findings := "## Findings\n- **Location:** tracked.txt:1\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Recommended Fix:** Add a nil guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				writeFile(t, filepath.Join(projectDir, "tracked.txt"), "first fix\n")
			case 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** STILL PRESENT\n- **Notes:** nil check added but the conditional is inverted\n")
			case 3:
				data, err := os.ReadFile(filepath.Join(projectDir, config.AuditPromptFile))
				require.NoError(t, err)
				secondFixPrompt = string(data)
				writeFile(t, filepath.Join(projectDir, "tracked.txt"), "second fix\n")
			case 4:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** RESOLVED\n- **Notes:** nil check now guards the panic path\n")
			case 5:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
			}
		},
	}
	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	opts.Epic.MaxAuditIterations = 1
	opts.Complexity = ComplexityModerate

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	assert.Contains(t, secondFixPrompt, "Previous Fix Attempts")
	assert.Contains(t, secondFixPrompt, "conditional is inverted")
	assert.Equal(t, ComplexityModerate, result.Complexity)
	require.NotNil(t, result.Metrics)
	assert.GreaterOrEqual(t, result.Metrics.TotalCalls(), 4)
}

func TestRunAuditLoopBehaviorUnchangedSharpensNextFixPrompt(t *testing.T) {
	t.Parallel()

	var secondFixPrompt string
	findings := "## Findings\n- **Location:** tracked.txt:1\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Recommended Fix:** Add a nil guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				writeFile(t, filepath.Join(projectDir, "tracked.txt"), "first fix\n")
			case 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** BEHAVIOR_UNCHANGED\n- **Notes:** only a comment was added; the error-handling branch is still unchanged\n")
			case 3:
				data, err := os.ReadFile(filepath.Join(projectDir, config.AuditPromptFile))
				require.NoError(t, err)
				secondFixPrompt = string(data)
				writeFile(t, filepath.Join(projectDir, "tracked.txt"), "second fix\n")
			case 4:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** RESOLVED\n- **Notes:** the nil path now returns an error\n")
			case 5:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	opts.Epic.MaxAuditIterations = 1
	opts.Complexity = ComplexityModerate

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.NotNil(t, result.Metrics)
	assert.Equal(t, 1, result.Metrics.BehaviorUnchangedOutcomes)
	assert.Contains(t, secondFixPrompt, "## Behavior-Unchanged Guidance")
	assert.Contains(t, secondFixPrompt, "Issue 1")
	assert.Contains(t, secondFixPrompt, "only a comment was added")
	assert.Contains(t, secondFixPrompt, "Do not answer with comments")
}

func TestRunAuditLoopPerClusterFastFailSkipsRejectedCluster(t *testing.T) {
	t.Parallel()

	// Two findings in different files → two clusters.
	// Cluster 1 fix rejected (no-op), cluster 2 fix accepted → only cluster 2 reaches verify.
	findings := "## Findings\n- **Location:** alpha.go:1\n- **Description:** Missing nil guard in alpha handler\n- **Severity:** HIGH\n- **Recommended Fix:** Add guard\n\n- **Location:** beta.go:1\n- **Description:** Missing bounds check in beta parser\n- **Severity:** HIGH\n- **Recommended Fix:** Add bounds check\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				// Audit: produce 2 findings
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				// Fix cluster 1: no changes (no-op) → will be rejected and skipped
			case 2:
				// Fix cluster 2: make a behavioral change
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "beta.go"), []byte("package main\nfunc parse() { /* bounded */ }\n"), 0o644))
			case 3:
				// Verify: confirm resolution of cluster 2's finding
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** STILL_PRESENT\n\n- **Issue:** 2\n- **Status:** RESOLVED\n")
			case 4:
				// Final re-audit: one finding remains
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "## Findings\n- **Location:** alpha.go:1\n- **Description:** Missing nil guard in alpha handler\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n")
			}
		},
	}

	opts := makeOpts(t, eng)
	writeFile(t, filepath.Join(opts.ProjectDir, "alpha.go"), "package main\nfunc handle() {}\n")
	writeFile(t, filepath.Join(opts.ProjectDir, "beta.go"), "package main\nfunc parse() {}\n")
	require.NoError(t, frygit.InitGit(context.Background(), opts.ProjectDir))
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result.Metrics)

	// Cluster 1 rejected (no-op), cluster 2 accepted
	assert.Equal(t, 1, result.Metrics.Snapshot().RejectedFixCalls, "cluster 1 should be rejected")
	assert.Equal(t, 1, result.Metrics.Snapshot().AcceptedFixCalls, "cluster 2 should be accepted")
	// Verify should have run for cluster 2
	assert.Equal(t, 1, result.Metrics.Snapshot().VerifyCalls, "verify should run after accepted cluster fix")
	// FixStrategy should be per_cluster
	assert.Equal(t, config.AuditFixStrategyPerCluster, result.Metrics.FixStrategy)
}

func TestBuildUnifiedFixPromptInlinesTargetFiles(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	writeFile(t, filepath.Join(opts.ProjectDir, "handler.go"), "package main\nfunc Handle() error { return nil }\n")

	cluster := remediationCluster{
		ID:    1,
		Label: "handler: nil guard",
		Findings: []Finding{
			{Location: "handler.go:2", Description: "Missing nil check", Severity: "HIGH", RecommendedFix: "Add nil guard"},
		},
		TargetFiles: []string{"handler.go"},
	}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "Cluster 1: handler: nil guard")
	assert.Contains(t, prompt, "## Target File: handler.go")
	assert.Contains(t, prompt, "func Handle()")
	assert.Contains(t, prompt, "## Fix Contract")
	assert.Contains(t, prompt, "Missing nil check")
}

func TestRunAuditLoopRollsBackRejectedOutOfScopeDiff(t *testing.T) {
	t.Parallel()

	findings := "## Findings\n- **Location:** target.go:1\n- **Description:** Missing nil guard\n- **Severity:** HIGH\n- **Recommended Fix:** Add guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				// Fix writes to a DIFFERENT file (out-of-scope) → should be rejected and rolled back
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "unrelated.go"), []byte("package main\n// rogue change\n"), 0o644))
			}
		},
	}

	opts := makeOpts(t, eng)
	writeFile(t, filepath.Join(opts.ProjectDir, "target.go"), "package main\nfunc handle() {}\n")
	writeFile(t, filepath.Join(opts.ProjectDir, "unrelated.go"), "package main\n")
	require.NoError(t, frygit.InitGit(context.Background(), opts.ProjectDir))
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result.Metrics)
	assert.Equal(t, 1, result.Metrics.Snapshot().RejectedFixCalls)

	// The out-of-scope file should have been rolled back to its original content
	data, readErr := os.ReadFile(filepath.Join(opts.ProjectDir, "unrelated.go"))
	require.NoError(t, readErr)
	assert.Equal(t, "package main\n", string(data), "rejected out-of-scope change should be rolled back")

	// Metrics should record the rollback
	require.Len(t, result.Metrics.Calls, 2) // audit + fix
	assert.Equal(t, []string{"unrelated.go"}, result.Metrics.Calls[1].RolledBackFiles)
}

func TestRunAuditLoopRollsBackCommentOnlyDiff(t *testing.T) {
	t.Parallel()

	findings := "## Findings\n- **Location:** tracked.go:1\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Recommended Fix:** Add nil guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0, 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				// Fix writes only a comment change → should be rejected and rolled back
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "tracked.go"), []byte("package main\n\n// comment only\n"), 0o644))
			}
		},
	}
	opts := makeOpts(t, eng)
	writeFile(t, filepath.Join(opts.ProjectDir, "tracked.go"), "package main\n")
	require.NoError(t, frygit.InitGit(context.Background(), opts.ProjectDir))
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Metrics.Snapshot().RejectedFixCalls)

	// The comment-only change should have been rolled back
	data, readErr := os.ReadFile(filepath.Join(opts.ProjectDir, "tracked.go"))
	require.NoError(t, readErr)
	assert.Equal(t, "package main\n", string(data), "rejected comment-only change should be rolled back")
	assert.Equal(t, []string{"tracked.go"}, result.Metrics.Calls[1].RolledBackFiles)
}

func TestRunAuditLoopSelectiveRollbackOnPartialOutOfScope(t *testing.T) {
	t.Parallel()

	findings := "## Findings\n- **Location:** target.go:1\n- **Description:** Missing nil guard\n- **Severity:** HIGH\n- **Recommended Fix:** Add guard\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				// Fix modifies BOTH target file and an unrelated file
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "target.go"), []byte("package main\nfunc handle() error { return nil }\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, "extra.go"), []byte("package main\n// extra change\n"), 0o644))
			case 2:
				// Verify: confirm resolution
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3:
				// Final re-audit: pass
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	writeFile(t, filepath.Join(opts.ProjectDir, "target.go"), "package main\nfunc handle() {}\n")
	writeFile(t, filepath.Join(opts.ProjectDir, "extra.go"), "package main\n")
	require.NoError(t, frygit.InitGit(context.Background(), opts.ProjectDir))
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.NotNil(t, result.Metrics)

	// The fix should be accepted_partial — in-scope changes kept, out-of-scope rolled back
	fixCall := result.Metrics.Calls[1]
	assert.Equal(t, fixValidationAcceptedPartial, fixCall.ValidationResult)
	assert.Equal(t, []string{"extra.go"}, fixCall.RolledBackFiles)

	// Target file should retain the fix
	targetData, _ := os.ReadFile(filepath.Join(opts.ProjectDir, "target.go"))
	assert.Contains(t, string(targetData), "error", "in-scope fix should be preserved")

	// Extra file should be rolled back
	extraData, _ := os.ReadFile(filepath.Join(opts.ProjectDir, "extra.go"))
	assert.Equal(t, "package main\n", string(extraData), "out-of-scope change should be rolled back")

	// TotalAcceptedFixCalls should count partial as accepted
	assert.Equal(t, 1, result.Metrics.TotalAcceptedFixCalls())
	assert.Equal(t, 1, result.Metrics.TotalPartialFixCalls())
}

func initAuditGitRepo(t *testing.T, projectDir string) {
	t.Helper()
	writeFile(t, filepath.Join(projectDir, "tracked.txt"), "base\n")
	require.NoError(t, frygit.InitGit(context.Background(), projectDir))
}
