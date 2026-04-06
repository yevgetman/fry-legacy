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
}

func TestRunAuditLoopAttemptsFixForEnvironmentBlocker(t *testing.T) {
	t.Parallel()

	blockerReport := "## Findings\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), blockerReport)
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Greater(t, eng.callIndex, 1, "fix loop should have been entered for environment blocker")
}

func TestRunAuditLoopIncludesBlockerFindingsInFixScope(t *testing.T) {
	t.Parallel()

	report := "## Findings\n- **Location:** src/api.go:20\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Category:** product_defect\n- **Recommended Fix:** handle the returned error\n\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n"
	var allFixPrompts []string
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			if data, err := os.ReadFile(filepath.Join(projectDir, config.AuditPromptFile)); err == nil {
				allFixPrompts = append(allFixPrompts, string(data))
			}
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), report)
			default:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "## Findings\n- **Location:** test/bootstrap.go:12\n- **Description:** Missing SUPABASE secrets prevent integration bootstrap\n- **Severity:** HIGH\n- **Category:** environment_blocker\n- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n- **Recommended Fix:** set the required secrets before rerunning audit\n\n## Verdict\nFAIL\n")
			}
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, result.Passed)
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

func TestBuildFixPromptInlinesTargetFiles(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	writeFile(t, filepath.Join(opts.ProjectDir, "handler.go"), "package main\nfunc Handle() error { return nil }\n")

	findings := []Finding{
		{Location: "handler.go:2", Description: "Missing nil check", Severity: "HIGH", RecommendedFix: "Add nil guard", AffectedFiles: []string{"handler.go"}},
	}
	prompt := buildFixPrompt(opts, findings, nil, nil)

	assert.Contains(t, prompt, "## Target File: handler.go")
	assert.Contains(t, prompt, "func Handle()")
	assert.Contains(t, prompt, "Missing nil check")
	assert.NotContains(t, prompt, "Fix Contract", "fix contract section should not exist")
}

func TestBuildFixPromptNoContractSection(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	findings := []Finding{
		{Location: "foo.go:1", Description: "Issue A", Severity: "HIGH"},
	}
	prompt := buildFixPrompt(opts, findings, nil, nil)
	assert.NotContains(t, prompt, "Fix Contract")
	assert.NotContains(t, prompt, "Expected evidence")
	assert.NotContains(t, prompt, "Target files:")
}

func initAuditGitRepo(t *testing.T, projectDir string) {
	t.Helper()
	writeFile(t, filepath.Join(projectDir, "tracked.txt"), "base\n")
	require.NoError(t, frygit.InitGit(context.Background(), projectDir))
}
