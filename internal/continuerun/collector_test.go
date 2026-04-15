package continuerun

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/steering"
)

func TestParseCompletedSprints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected []CompletedSprint
	}{
		{
			name:     "empty file",
			content:  "",
			expected: nil,
		},
		{
			name:     "header only",
			content:  "# Epic Progress — TestEpic\n\n",
			expected: nil,
		},
		{
			name: "one completed sprint",
			content: "# Epic Progress — TestEpic\n\n" +
				"## Sprint 1: Setup — PASS\n\nDid the setup.\n",
			expected: []CompletedSprint{
				{Number: 1, Name: "Setup", Status: "PASS"},
			},
		},
		{
			name: "multiple completed sprints",
			content: "# Epic Progress — TestEpic\n\n" +
				"## Sprint 1: Setup — PASS\n\nDone.\n\n" +
				"## Sprint 2: Auth — PASS (aligned)\n\nFixed.\n\n" +
				"## Sprint 3: API — PASS (deferred failures)\n\nMostly done.\n",
			expected: []CompletedSprint{
				{Number: 1, Name: "Setup", Status: "PASS"},
				{Number: 2, Name: "Auth", Status: "PASS (aligned)"},
				{Number: 3, Name: "API", Status: "PASS (deferred failures)"},
			},
		},
		{
			name: "ignores non-PASS lines",
			content: "## Sprint 1: Setup — PASS\n\n" +
				"## Sprint 2: Auth — FAIL\n\n",
			expected: []CompletedSprint{
				{Number: 1, Name: "Setup", Status: "PASS"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseCompletedSprints(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseFailedSprints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected []FailedSprint
	}{
		{
			name:     "empty file",
			content:  "",
			expected: nil,
		},
		{
			name: "only PASS entries",
			content: "# Epic Progress \u2014 TestEpic\n\n" +
				"## Sprint 1: Setup \u2014 PASS\n\nDone.\n",
			expected: nil,
		},
		{
			name: "one failed sprint",
			content: "# Epic Progress \u2014 TestEpic\n\n" +
				"## Sprint 1: Setup \u2014 FAIL (audit: HIGH)\n\n",
			expected: []FailedSprint{
				{Number: 1, Name: "Setup", Status: "FAIL (audit: HIGH)"},
			},
		},
		{
			name: "mixed pass and fail",
			content: "# Epic Progress \u2014 TestEpic\n\n" +
				"## Sprint 1: Setup \u2014 PASS\n\nDone.\n\n" +
				"## Sprint 2: Auth \u2014 FAIL\n\n",
			expected: []FailedSprint{
				{Number: 2, Name: "Auth", Status: "FAIL"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ParseFailedSprints(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindNextSprint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		completed    []CompletedSprint
		totalSprints int
		expected     int
	}{
		{
			name:         "no completions",
			completed:    nil,
			totalSprints: 5,
			expected:     1,
		},
		{
			name: "first three done",
			completed: []CompletedSprint{
				{Number: 1}, {Number: 2}, {Number: 3},
			},
			totalSprints: 5,
			expected:     4,
		},
		{
			name: "gap at sprint 2",
			completed: []CompletedSprint{
				{Number: 1}, {Number: 3},
			},
			totalSprints: 5,
			expected:     2,
		},
		{
			name: "all complete",
			completed: []CompletedSprint{
				{Number: 1}, {Number: 2}, {Number: 3},
			},
			totalSprints: 3,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, findNextSprint(tt.completed, tt.totalSprints))
		})
	}
}

func TestCollectActiveSprintState(t *testing.T) {
	t.Parallel()

	ep := &epic.Epic{
		TotalSprints: 3,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Setup"},
			{Number: 2, Name: "Auth"},
			{Number: 3, Name: "API"},
		},
	}

	t.Run("no evidence", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, config.BuildLogsDir), 0o755))

		result := collectActiveSprintState(dir, 2, ep)
		assert.Nil(t, result)
	})

	t.Run("has build logs", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logsDir := filepath.Join(dir, config.BuildLogsDir)
		require.NoError(t, os.MkdirAll(logsDir, 0o755))

		// Create iteration logs
		require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_iter1_20260316_100000.log"), []byte("log1"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_iter2_20260316_100100.log"), []byte("log2\nfinal line"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_audit1_20260316_100200.log"), []byte("audit"), 0o644))

		result := collectActiveSprintState(dir, 2, ep)
		require.NotNil(t, result)
		assert.Equal(t, 2, result.Number)
		assert.Equal(t, "Auth", result.Name)
		assert.Equal(t, 2, result.IterationCount)
		assert.Equal(t, 1, result.AuditCount)
		assert.Equal(t, 0, result.HealCount)
		assert.False(t, result.HasResumeLog)
	})

	t.Run("has sprint progress mention", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, config.BuildLogsDir), 0o755))

		progress := "# Sprint 2: Auth — Progress\n\nDid some work.\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.SprintProgressFile), []byte(progress), 0o644))

		result := collectActiveSprintState(dir, 2, ep)
		require.NotNil(t, result)
		assert.Equal(t, 2, result.Number)
		assert.Contains(t, result.ProgressExcerpt, "Sprint 2")
	})

	t.Run("has resume log (old format)", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logsDir := filepath.Join(dir, config.BuildLogsDir)
		require.NoError(t, os.MkdirAll(logsDir, 0o755))

		require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_retry_20260316_100000.log"), []byte("retry"), 0o644))

		result := collectActiveSprintState(dir, 2, ep)
		require.NotNil(t, result)
		assert.True(t, result.HasResumeLog)
	})

	t.Run("has resume log (new format)", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		logsDir := filepath.Join(dir, config.BuildLogsDir)
		require.NoError(t, os.MkdirAll(logsDir, 0o755))

		require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_resume_20260316_100000.log"), []byte("resume"), 0o644))

		result := collectActiveSprintState(dir, 2, ep)
		require.NotNil(t, result)
		assert.True(t, result.HasResumeLog)
	})
}

func TestCollectBuildState_NoPreviousBuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ep := &epic.Epic{TotalSprints: 3}
	_, err := CollectBuildState(context.Background(), dir, ep, false)
	assert.ErrorIs(t, err, ErrNoPreviousBuild)
}

func TestCollectBuildStateIncludesResumePoint(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.FryDir), 0o755))
	require.NoError(t, steering.WriteResumePoint(dir, steering.ResumePoint{
		Phase:              "sprint_audit",
		Verdict:            steering.ResumeVerdictResume,
		Reason:             "after audit cycle 2",
		Sprint:             6,
		SprintName:         "Polish auth",
		RecommendedCommand: "fry run --resume --sprint 6",
	}))

	ep := &epic.Epic{
		Name:         "Resume Test",
		TotalSprints: 6,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "One"},
			{Number: 2, Name: "Two"},
			{Number: 3, Name: "Three"},
			{Number: 4, Name: "Four"},
			{Number: 5, Name: "Five"},
			{Number: 6, Name: "Six"},
		},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	require.NotNil(t, state.ResumePoint)
	assert.Equal(t, "sprint_audit", state.ResumePoint.Phase)
	assert.Equal(t, steering.ResumeVerdictResume, state.ResumePoint.Verdict)
	assert.Equal(t, 6, state.ResumePoint.Sprint)
}

func TestCollectBuildState_FreshBuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	ep := &epic.Epic{
		Name:         "TestEpic",
		TotalSprints: 3,
		Engine:       "codex",
		EffortLevel:  epic.EffortHigh,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Setup"},
			{Number: 2, Name: "Auth"},
			{Number: 3, Name: "API"},
		},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.Equal(t, "TestEpic", state.EpicName)
	assert.Equal(t, 3, state.TotalSprints)
	assert.Empty(t, state.CompletedSprints)
	assert.Equal(t, 0, state.HighestCompleted)
	assert.Empty(t, state.ActiveSprints)
}

func TestCollectBuildState_FailedSprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	epicProgress := "# Epic Progress \u2014 TestEpic\n\n## Sprint 1: Setup \u2014 FAIL (audit: HIGH)\n\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.EpicProgressFile), []byte(epicProgress), 0o644))

	ep := &epic.Epic{
		Name:         "TestEpic",
		TotalSprints: 3,
		Engine:       "claude",
		EffortLevel:  epic.EffortMax,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Setup"},
			{Number: 2, Name: "Auth"},
			{Number: 3, Name: "API"},
		},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.Empty(t, state.CompletedSprints)
	assert.Equal(t, 0, state.HighestCompleted)
	require.Len(t, state.FailedSprints, 1)
	assert.Equal(t, 1, state.FailedSprints[0].Number)
	assert.Equal(t, "Setup", state.FailedSprints[0].Name)
	assert.Equal(t, "FAIL (audit: HIGH)", state.FailedSprints[0].Status)
	assert.Empty(t, state.ActiveSprints)
}

func TestCollectBuildState_PartialBuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	logsDir := filepath.Join(dir, config.BuildLogsDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	// Write epic progress with sprint 1 done
	epicProgress := "# Epic Progress — TestEpic\n\n## Sprint 1: Setup — PASS\n\nCompleted setup.\n\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.EpicProgressFile), []byte(epicProgress), 0o644))

	// Write sprint 2 iteration logs (partial work)
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint2_iter1_20260316_100000.log"), []byte("working..."), 0o644))

	ep := &epic.Epic{
		Name:         "TestEpic",
		TotalSprints: 3,
		Engine:       "codex",
		EffortLevel:  epic.EffortHigh,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Setup"},
			{Number: 2, Name: "Auth"},
			{Number: 3, Name: "API"},
		},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.Len(t, state.CompletedSprints, 1)
	assert.Equal(t, 1, state.HighestCompleted)
	require.Len(t, state.ActiveSprints, 1)
	assert.Equal(t, 2, state.ActiveSprints[0].Number)
	assert.Equal(t, "Auth", state.ActiveSprints[0].Name)
	assert.Equal(t, 1, state.ActiveSprints[0].IterationCount)
}

func TestCollectBuildState_MultipleActiveSprints(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	logsDir := filepath.Join(dir, config.BuildLogsDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.MkdirAll(logsDir, 0o755))

	// Sprints 2-5 completed, Sprint 1 unrecorded, Sprint 6 failed
	epicProgress := "# Epic Progress\n\n" +
		"## Sprint 2: Services — PASS\n\nDone.\n\n" +
		"## Sprint 3: Projections — PASS\n\nDone.\n\n" +
		"## Sprint 4: Pages — PASS (aligned)\n\nDone.\n\n" +
		"## Sprint 5: Auth — PASS\n\nDone.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.EpicProgressFile), []byte(epicProgress), 0o644))

	// Sprint 1 has build logs (completed but unrecorded)
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint1_iter1_20260316_130000.log"), []byte("sprint 1 done"), 0o644))

	// Sprint 6 has build logs (failed audit)
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint6_iter1_20260316_190000.log"), []byte("sprint 6 work"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(logsDir, "sprint6_audit1_20260316_190100.log"), []byte("audit 1"), 0o644))

	// Sprint progress file belongs to sprint 6
	progress := "# Sprint 6: Dashboard — Progress\n\nDoing dashboard work.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.SprintProgressFile), []byte(progress), 0o644))

	// Audit file belongs to sprint 6 (shared file, overwritten per sprint)
	auditContent := "## Finding 1\n**Severity:** HIGH\n**Description:** Some issue\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.SprintAuditFile), []byte(auditContent), 0o644))

	ep := &epic.Epic{
		Name:         "TestEpic",
		TotalSprints: 7,
		Engine:       "claude",
		EffortLevel:  epic.EffortHigh,
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Scaffold"},
			{Number: 2, Name: "Services"},
			{Number: 3, Name: "Projections"},
			{Number: 4, Name: "Pages"},
			{Number: 5, Name: "Auth"},
			{Number: 6, Name: "Dashboard"},
			{Number: 7, Name: "MCP"},
		},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)

	// Should find two active sprints: 1 and 6 (7 has no evidence)
	require.Len(t, state.ActiveSprints, 2)

	// Sprint 1: has logs but progress file belongs to sprint 6
	assert.Equal(t, 1, state.ActiveSprints[0].Number)
	assert.Equal(t, "Scaffold", state.ActiveSprints[0].Name)
	assert.Equal(t, 1, state.ActiveSprints[0].IterationCount)
	assert.Empty(t, state.ActiveSprints[0].ProgressExcerpt) // filtered out — belongs to sprint 6
	assert.Empty(t, state.ActiveSprints[0].AuditSeverity)   // filtered out — audit file belongs to sprint 6

	// Sprint 6: has logs, progress file, and audit file
	assert.Equal(t, 6, state.ActiveSprints[1].Number)
	assert.Equal(t, "Dashboard", state.ActiveSprints[1].Name)
	assert.Equal(t, 1, state.ActiveSprints[1].IterationCount)
	assert.Equal(t, 1, state.ActiveSprints[1].AuditCount)
	assert.Contains(t, state.ActiveSprints[1].ProgressExcerpt, "Sprint 6")
	assert.Equal(t, "HIGH", state.ActiveSprints[1].AuditSeverity) // correctly attributed
}

func TestCollectBuildState_SentinelPresent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Create sentinel file
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildAuditCompleteFile), []byte("done\n"), 0o644))

	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortHigh,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.True(t, state.BuildAuditComplete)
}

func TestCollectBuildState_SentinelAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortHigh,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.False(t, state.BuildAuditComplete)
}

func TestCollectBuildState_SentinelAbsentNoAudit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Epic with @no_audit — sentinel absent but audit was intentionally skipped.
	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: false,
		EffortLevel:      epic.EffortHigh,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.True(t, state.BuildAuditComplete, "should be true when audit is disabled")
}

func TestCollectBuildState_SentinelAbsentEffortFast(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Low-effort build — audit gate never fires, so sentinel is never written.
	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortFast,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	assert.True(t, state.BuildAuditComplete, "should be true for low-effort builds")
}

func TestCollectBuildState_SentinelAbsentEffortFastAlwaysVerify(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Low-effort build with --always-verify: audit gate fires, so sentinel
	// absence must be reported as BuildAuditComplete = false.
	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortFast,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, true)
	require.NoError(t, err)
	assert.False(t, state.BuildAuditComplete, "should be false for low-effort builds with always-verify")
}

func TestCollectBuildState_SentinelStatError(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("test requires non-root user (root bypasses permission checks)")
	}

	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Remove execute permission from .fry/ so os.Stat on files inside it
	// returns EACCES (not IsNotExist), exercising the warning branch.
	require.NoError(t, os.Chmod(fryDir, 0o644))
	t.Cleanup(func() {
		_ = os.Chmod(fryDir, 0o755) // restore so TempDir cleanup works
	})

	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortHigh,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	state, err := CollectBuildState(context.Background(), dir, ep, false)
	require.NoError(t, err)
	// Sentinel stat failed with permission error → defaults to false
	assert.False(t, state.BuildAuditComplete)
}

func TestCollectBuildState_SentinelStatPermissionError(t *testing.T) {
	// Not parallel: mutates os.Stderr to capture warning output.
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user (root bypasses permission checks)")
	}

	dir := t.TempDir()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Create the sentinel file, then make the parent directory non-traversable
	// so os.Stat returns a permission error (not IsNotExist).
	sentinelPath := filepath.Join(dir, config.BuildAuditCompleteFile)
	require.NoError(t, os.WriteFile(sentinelPath, []byte("done\n"), 0o644))
	require.NoError(t, os.Chmod(fryDir, 0o644))
	t.Cleanup(func() {
		_ = os.Chmod(fryDir, 0o755)
	})

	ep := &epic.Epic{
		Name:             "TestEpic",
		TotalSprints:     1,
		AuditAfterSprint: true,
		EffortLevel:      epic.EffortHigh,
		Sprints:          []epic.Sprint{{Number: 1, Name: "Setup"}},
	}

	// Capture stderr to verify warning is logged
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	state, collectErr := CollectBuildState(context.Background(), dir, ep, false)

	_ = w.Close()
	os.Stderr = oldStderr

	stderrBuf := make([]byte, 4096)
	n, _ := r.Read(stderrBuf)
	_ = r.Close()
	stderrOutput := string(stderrBuf[:n])

	require.NoError(t, collectErr)
	assert.False(t, state.BuildAuditComplete)
	assert.Contains(t, stderrOutput, "unable to stat build audit sentinel")
}

func TestReadSprintProgressExcerpt_FiltersBySprint(t *testing.T) {
	t.Parallel()

	t.Run("returns content for matching sprint", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))

		content := "# Sprint 6: Dashboard — Progress\n\nSome work done.\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.SprintProgressFile), []byte(content), 0o644))

		result := readSprintProgressExcerpt(dir, 6, 50)
		assert.Contains(t, result, "Sprint 6")
		assert.Contains(t, result, "Some work done")
	})

	t.Run("returns empty for non-matching sprint", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))

		content := "# Sprint 6: Dashboard — Progress\n\nSome work done.\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.SprintProgressFile), []byte(content), 0o644))

		result := readSprintProgressExcerpt(dir, 1, 50)
		assert.Empty(t, result)
	})

	t.Run("returns empty for missing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		result := readSprintProgressExcerpt(dir, 1, 50)
		assert.Empty(t, result)
	})
}

func TestCollectDeferredFailures(t *testing.T) {
	t.Parallel()

	t.Run("no file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.Nil(t, collectDeferredFailures(dir))
	})

	t.Run("with entries", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))

		content := "# Deferred Sanity Check Failures\n\n" +
			"## Sprint 2: Auth\n\n" +
			"- DEFERRED: File missing or empty: src/lib/auth.ts\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.DeferredFailuresFile), []byte(content), 0o644))

		lines := collectDeferredFailures(dir)
		assert.Len(t, lines, 2)
		assert.Equal(t, "## Sprint 2: Auth", lines[0])
		assert.Contains(t, lines[1], "DEFERRED:")
	})
}

func TestReadBuildMode(t *testing.T) {
	t.Parallel()

	t.Run("no file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.Equal(t, "", ReadBuildMode(dir))
	})

	t.Run("writing mode", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildModeFile), []byte("writing\n"), 0o644))

		assert.Equal(t, "writing", ReadBuildMode(dir))
	})

	t.Run("planning mode no trailing newline", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildModeFile), []byte("planning"), 0o644))

		assert.Equal(t, "planning", ReadBuildMode(dir))
	})

	t.Run("software mode", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildModeFile), []byte("software\n"), 0o644))

		assert.Equal(t, "software", ReadBuildMode(dir))
	})
}

func TestCollectBuildState_Mode(t *testing.T) {
	t.Parallel()

	t.Run("collects mode from file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildModeFile), []byte("writing\n"), 0o644))

		ep := &epic.Epic{
			Name:         "TestEpic",
			TotalSprints: 2,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Chapter 1"},
				{Number: 2, Name: "Chapter 2"},
			},
		}

		state, err := CollectBuildState(context.Background(), dir, ep, false)
		require.NoError(t, err)
		assert.Equal(t, "writing", state.Mode)
	})

	t.Run("empty when no mode file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))

		ep := &epic.Epic{
			Name:         "TestEpic",
			TotalSprints: 1,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup"},
			},
		}

		state, err := CollectBuildState(context.Background(), dir, ep, false)
		require.NoError(t, err)
		assert.Equal(t, "", state.Mode)
	})
}

func TestReadExitReason(t *testing.T) {
	t.Parallel()

	t.Run("no file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.Equal(t, "", readExitReason(dir))
	})

	t.Run("with reason", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fryDir := filepath.Join(dir, config.FryDir)
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildExitReasonFile), []byte("After sprint 2: sprint 6 is outside deviation scope (max: sprint 5)\n"), 0o644))

		assert.Equal(t, "After sprint 2: sprint 6 is outside deviation scope (max: sprint 5)", readExitReason(dir))
	})
}

func TestExtractMaxSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{"empty", "", ""},
		{"low only", "**Severity:** LOW", "LOW"},
		{"mixed", "**Severity:** LOW\n**Severity:** HIGH\n**Severity:** MODERATE", "HIGH"},
		{"critical", "Severity: CRITICAL", "CRITICAL"},
		{"no label no match", "This is HIGH priority work", ""},
		{"label required", "Severity: MODERATE\nHIGH effort task", "MODERATE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, extractMaxSeverity(tt.content))
		})
	}
}

func TestReadTail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")

	var lines []string
	for i := 1; i <= 200; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("%s\n", joinLines(lines))), 0o644))

	tail := readTail(path, 10)
	assert.Contains(t, tail, "line 200")
	assert.Contains(t, tail, "line 195")
	assert.NotContains(t, tail, "line 100")
}

func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}
