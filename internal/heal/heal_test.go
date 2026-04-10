package heal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/verify"
	"github.com/yevgetman/fry/templates"
)

func TestHealPromptStructure(t *testing.T) {
	t.Parallel()
	// Use a temp dir with no executive.md → reference should be absent
	projectDir := t.TempDir()
	opts := HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number: 3,
			Name:   "Setup Auth",
		},
	}

	report := "Sanity checks: 2/5 passed.\n\nFailed checks:\n- FAILED: File missing or empty: src/auth.ts"
	expected := "# ALIGNMENT MODE — Sprint 3: Setup Auth\n\n" +
		"## What happened\n" +
		"The sprint finished its work but FAILED independent sanity checks.\n" +
		"Your job is to fix ONLY the issues described below. Do not start the sprint over.\n" +
		"Do not refactor or reorganize. Make the minimum changes needed to pass the checks.\n\n" +
		"## Failed sanity checks\n\n" +
		report + "\n\n" +
		"## Instructions\n" +
		"1. Read .fry/sprint-progress.txt for context on what was built this sprint\n" +
		"2. Read .fry/epic-progress.txt for context on what was built in prior sprints\n" +
		"3. Read the failed checks above carefully\n" +
		"4. Fix each failure — create missing files, fix build errors, correct config\n" +
		"5. After fixing, do a final spot check (e.g., run the build command if applicable)\n" +
		"6. Append a brief note to .fry/sprint-progress.txt about what you fixed in this alignment pass\n\n" +
		"## Context files\n" +
		"- Read .fry/sprint-progress.txt for current sprint iteration history\n" +
		"- Read .fry/epic-progress.txt for prior sprint summaries\n" +
		"- Read plans/plan.md for the overall project plan\n" +
		"\n" +
		"Do NOT output any promise tokens. Just fix the issues.\n"

	assert.Equal(t, expected, buildHealPrompt(opts, report))
}

func TestHealPromptWithExecutiveAndUserDirective(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	// Create executive.md so the conditional reference appears
	writeFile(t, filepath.Join(projectDir, config.ExecutiveFile), "Executive context\n")

	opts := HealOpts{
		ProjectDir: projectDir,
		UserPrompt: "Focus on auth module",
		Sprint: &epic.Sprint{
			Number: 2,
			Name:   "Auth Layer",
		},
	}

	report := "Sanity checks: 1/3 passed."
	result := buildHealPrompt(opts, report)

	assert.Contains(t, result, "- Read plans/executive.md for executive context\n")
	assert.Contains(t, result, "- User directive: Focus on auth module\n")
	// User directive and executive should be in context files section, not separate headers
	assert.NotContains(t, result, "## User Directive")
}

func TestHealPromptWritingMode(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	opts := HealOpts{
		ProjectDir: projectDir,
		Mode:       "writing",
		Sprint: &epic.Sprint{
			Number: 1,
			Name:   "Introduction",
		},
	}

	report := "Sanity checks: 0/2 passed."
	result := buildHealPrompt(opts, report)
	assert.Contains(t, result, "create missing content files")
	assert.NotContains(t, result, "fix build errors")
	assert.Contains(t, result, "review the content for completeness")
	assert.NotContains(t, result, "run the build command")
}

func TestHealLoopMaxAttempts(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number: 1,
			Name:   "Heal",
		},
		Epic: &epic.Epic{
			TotalSprints:       2,
			MaxHealAttempts:    2,
			MaxHealAttemptsSet: true,
		},
		Engine:        mockEngine,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.Len(t, mockEngine.prompts, 2)
	for _, prompt := range mockEngine.prompts {
		healPrompt, loadErr := templates.LoadText(config.HealInvocationFile)
		require.NoError(t, loadErr)
		assert.Equal(t, healPrompt, prompt)
	}
}

func TestHealPerSprintOverride(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	override := 1
	mockEngine := &stubEngine{name: "claude"}
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number:          1,
			Name:            "Heal",
			MaxHealAttempts: &override,
		},
		Epic: &epic.Epic{
			TotalSprints:       2,
			MaxHealAttempts:    3,
			MaxHealAttemptsSet: true,
		},
		Engine:        mockEngine,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.Len(t, mockEngine.prompts, 1)
}

func TestHealLoopMaxAttemptsOverride(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number: 1,
			Name:   "Heal",
		},
		Epic: &epic.Epic{
			TotalSprints:       2,
			MaxHealAttempts:    2, // Would normally limit to 2
			MaxHealAttemptsSet: true,
		},
		Engine:              mockEngine,
		SprintLogFile:       sprintLog,
		MaxAttemptsOverride: 5, // Override to 5
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// Should use the override (5) instead of epic default (2)
	assert.Len(t, mockEngine.prompts, 5)
}

func TestHealLoopSucceeds(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "target.txt"), "content\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number: 1,
			Name:   "Heal",
		},
		Epic: &epic.Epic{
			TotalSprints:       1,
			MaxHealAttempts:    2,
			MaxHealAttemptsSet: true,
		},
		Engine:        mockEngine,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "target.txt"},
		},
	})
	require.NoError(t, err)
	assert.True(t, result.Healed)
	assert.Empty(t, mockEngine.prompts, "engine should not run when checks already pass")
}

func TestHealLoopReloadsVerificationFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	// Write a verification file that checks for a missing file
	verificationPath := filepath.Join(projectDir, ".fry", "verification.md")
	writeFile(t, verificationPath, "@sprint 1\n@check_file missing.txt\n")

	// Engine that "fixes" the issue on first attempt by creating the file
	// AND rewrites verification to check a file that already exists
	fixEngine := &fixingEngine{
		projectDir:       projectDir,
		verificationPath: verificationPath,
	}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number: 1,
			Name:   "Reload test",
		},
		Epic: &epic.Epic{
			TotalSprints:       1,
			MaxHealAttempts:    3,
			MaxHealAttemptsSet: true,
		},
		Engine:           fixEngine,
		SprintLogFile:    sprintLog,
		VerificationFile: verificationPath,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.True(t, result.Healed, "should pass after engine rewrites verification file")
	assert.Equal(t, 1, fixEngine.calls, "should only need one alignment attempt")
}

func TestHealLoopWithinThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	// Create one file that passes, leave another missing
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// 1 of 5 checks fails = 20%, threshold is 20% → within
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Threshold"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 1, MaxHealAttemptsSet: true},
		Engine:     mockEngine,
		SprintLogFile:  sprintLog,
		MaxFailPercent: 20,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.True(t, result.WithinThreshold)
	assert.Len(t, result.DeferredFailures, 1)
}

func TestHealLoopExceedsThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// 2 of 3 checks fail = 66.7%, threshold is 20% → exceeds
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Exceed"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 1, MaxHealAttemptsSet: true},
		Engine:     mockEngine,
		SprintLogFile:  sprintLog,
		MaxFailPercent: 20,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing1.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing2.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.False(t, result.WithinThreshold)
	assert.Empty(t, result.DeferredFailures)
}

func TestHealLoopZeroThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Strict mode: any failure exceeds threshold
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Strict"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 1, MaxHealAttemptsSet: true},
		Engine:     mockEngine,
		SprintLogFile:  sprintLog,
		MaxFailPercent: 0,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.False(t, result.WithinThreshold)
}

// --- Effort-level-aware alignment tests ---

func TestHealLoopFastEffortNoAttempts(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Fast effort: 1/5 checks fail = 20%, within 20% threshold → pass with no alignment attempts
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Fast effort"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortFast,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.True(t, result.WithinThreshold, "should pass via threshold with no alignment")
	assert.Empty(t, mockEngine.prompts, "fast effort should make zero alignment attempts")
}

func TestHealLoopFastEffortExceedsThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Fast effort: 2/3 checks fail = 67%, exceeds 20% → fail with no alignment attempts
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Fast fail"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortFast,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing1.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing2.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.False(t, result.WithinThreshold, "should fail — too many failures")
	assert.Empty(t, mockEngine.prompts, "fast effort should make zero alignment attempts even on failure")
}

func TestHealLoopStandardEffort(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Standard effort: should make exactly 3 alignment attempts
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Standard"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortStandard,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.Len(t, mockEngine.prompts, 3, "standard effort should make exactly 3 attempts")
}

func TestHealLoopHighEffortStuckExit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// High effort: stuck engine (no progress) should exit after stuck threshold (2)
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "High stuck"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortHigh,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// Should exit after 2 attempts (stuck threshold = 2 for high)
	assert.Len(t, mockEngine.prompts, 2, "high effort should exit after 2 no-progress attempts")
}

func TestHealLoopHighEffortProgressContinues(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	// Engine that fixes one file per call, making progress each time
	eng := &progressEngine{
		projectDir: projectDir,
		// Fix check files one at a time: a.txt, b.txt, c.txt
		// After 3 calls, 3 of 5 are fixed, then stuck (d.txt, e.txt remain)
		filesToCreate: []string{"a.txt", "b.txt", "c.txt"},
	}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "High progress"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      eng,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortHigh,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "a.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "b.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "c.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "d.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "e.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// 3 calls with progress + 2 stuck = 5 total
	assert.Equal(t, 5, eng.calls, "should continue while making progress, stop after 2 stuck")
}

func TestHealLoopMaxEffortStuckExit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Max effort: no progress, should exit after stuck threshold (3)
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Max stuck"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortMax,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// Should exit after 3 attempts (stuck threshold = 3 for max)
	assert.Len(t, mockEngine.prompts, 3, "max effort should exit after 3 no-progress attempts")
}

func TestHealLoopMaxEffortMidLoopThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	// Create 10 check files. Engine will fix them one at a time.
	// After 10 attempts (minForThreshold), 10/11 pass = 9% fail < 10% threshold → exit
	checkFiles := []string{"f1.txt", "f2.txt", "f3.txt", "f4.txt", "f5.txt",
		"f6.txt", "f7.txt", "f8.txt", "f9.txt", "f10.txt"}
	eng := &progressEngine{
		projectDir:    projectDir,
		filesToCreate: checkFiles,
	}

	var checks []verify.Check
	for _, f := range checkFiles {
		checks = append(checks, verify.Check{Sprint: 1, Type: verify.CheckFile, Path: f})
	}
	// One more check that will never pass
	checks = append(checks, verify.Check{Sprint: 1, Type: verify.CheckFile, Path: "unfixable.txt"})

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Max threshold"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      eng,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortMax,
		Checks:      checks,
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.True(t, result.WithinThreshold, "should pass via mid-loop threshold at ≥10 attempts with ≤10%% fail")
	assert.Equal(t, 10, eng.calls, "should exit at attempt 10 when within 10%% threshold")
}

func TestHealLoopMaxEffortContinuesPastMinWithProgress(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	// 5 check files. Engine fixes them one at a time over 5 calls.
	// After 5 calls, 2/5 still fail = 40% > 10% threshold.
	// But the mid-loop threshold only applies at attempt ≥10, so it runs all 5
	// plus the stuck threshold (3) = 8 total (3 fix, then 2 stuck attempts don't fix more)
	// Wait: engine creates 5 files, so f1-f5 get fixed over 5 calls. Then 0 remain.
	// Let me use more unfixable files.
	eng := &progressEngine{
		projectDir:    projectDir,
		filesToCreate: []string{"f1.txt", "f2.txt", "f3.txt"},
	}

	checks := []verify.Check{
		{Sprint: 1, Type: verify.CheckFile, Path: "f1.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "f2.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "f3.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "unfixable1.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "unfixable2.txt"},
	}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Max progress"},
		Epic:        &epic.Epic{TotalSprints: 1},
		Engine:      eng,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortMax,
		Checks:      checks,
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// 3 progress attempts + 3 stuck = 6 total. Stuck threshold=3 for max.
	assert.Equal(t, 6, eng.calls, "should continue while making progress then stop after stuck threshold")
}

func TestHealLoopExplicitDirectiveOverridesEffort(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// High effort defaults to 10 attempts with progress detection,
	// but explicit @max_heal_attempts 5 overrides to 5 with no progress detection (alignment)
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Override"},
		Epic: &epic.Epic{
			TotalSprints:       1,
			MaxHealAttempts:    5,
			MaxHealAttemptsSet: true,
		},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortHigh,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	// Explicit directive: exactly 5 attempts, no progress detection
	assert.Len(t, mockEngine.prompts, 5, "explicit @max_heal_attempts should override effort-level default")
}

func TestHealLoopMaxEffortStricterThreshold(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	mockEngine := &stubEngine{name: "codex"}
	// Max effort: 1/5 = 20% fail > 10% max threshold → fail (would pass at medium/high with 20%)
	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir:  projectDir,
		Sprint:      &epic.Sprint{Number: 1, Name: "Max strict"},
		Epic:        &epic.Epic{TotalSprints: 1, MaxHealAttempts: 1, MaxHealAttemptsSet: true},
		Engine:      mockEngine,
		SprintLogFile: sprintLog,
		EffortLevel: epic.EffortMax,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "present.txt"},
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	assert.False(t, result.WithinThreshold, "20%% fail should exceed max effort's 10%% threshold")
}

func TestEffectiveHealConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		opts            HealOpts
		wantMax         int
		wantHardCap     bool
		wantProgress    bool
		wantStuck       int
		wantFailPercent int
	}{
		{
			name: "fast effort defaults",
			opts: HealOpts{
				Sprint:      &epic.Sprint{Number: 1, Name: "s"},
				Epic:        &epic.Epic{TotalSprints: 1},
				EffortLevel: epic.EffortFast,
			},
			wantMax:         0,
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 20,
		},
		{
			name: "standard effort defaults",
			opts: HealOpts{
				Sprint:      &epic.Sprint{Number: 1, Name: "s"},
				Epic:        &epic.Epic{TotalSprints: 1},
				EffortLevel: epic.EffortStandard,
			},
			wantMax:         3,
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 20,
		},
		{
			name: "high effort defaults",
			opts: HealOpts{
				Sprint:      &epic.Sprint{Number: 1, Name: "s"},
				Epic:        &epic.Epic{TotalSprints: 1},
				EffortLevel: epic.EffortHigh,
			},
			wantMax:         10,
			wantHardCap:     true,
			wantProgress:    true,
			wantStuck:       2,
			wantFailPercent: 20,
		},
		{
			name: "max effort defaults",
			opts: HealOpts{
				Sprint:      &epic.Sprint{Number: 1, Name: "s"},
				Epic:        &epic.Epic{TotalSprints: 1},
				EffortLevel: epic.EffortMax,
			},
			wantMax:         config.HealSafetyCapMax,
			wantHardCap:     false,
			wantProgress:    true,
			wantStuck:       3,
			wantFailPercent: 10,
		},
		{
			name: "explicit directive overrides effort",
			opts: HealOpts{
				Sprint:      &epic.Sprint{Number: 1, Name: "s"},
				Epic:        &epic.Epic{TotalSprints: 1, MaxHealAttempts: 7, MaxHealAttemptsSet: true},
				EffortLevel: epic.EffortHigh,
			},
			wantMax:         7,
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 20,
		},
		{
			name: "resume override takes priority",
			opts: HealOpts{
				Sprint:              &epic.Sprint{Number: 1, Name: "s"},
				Epic:                &epic.Epic{TotalSprints: 1, MaxHealAttempts: 3, MaxHealAttemptsSet: true},
				EffortLevel:         epic.EffortHigh,
				MaxAttemptsOverride: 15,
			},
			wantMax:         15,
			wantHardCap:     true,
			wantProgress:    true,
			wantStuck:       2,
			wantFailPercent: 20,
		},
		{
			name: "per-sprint override",
			opts: func() HealOpts {
				v := 4
				return HealOpts{
					Sprint:      &epic.Sprint{Number: 1, Name: "s", MaxHealAttempts: &v},
					Epic:        &epic.Epic{TotalSprints: 1},
					EffortLevel: epic.EffortHigh,
				}
			}(),
			wantMax:         4,
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 20,
		},
		{
			name: "auto effort fallback",
			opts: HealOpts{
				Sprint: &epic.Sprint{Number: 1, Name: "s"},
				Epic:   &epic.Epic{TotalSprints: 1},
			},
			wantMax:         config.DefaultMaxHealAttempts,
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 0,
		},
		{
			name: "explicit fail percent overrides effort",
			opts: HealOpts{
				Sprint:         &epic.Sprint{Number: 1, Name: "s"},
				Epic:           &epic.Epic{TotalSprints: 1, MaxFailPercentSet: true},
				EffortLevel:    epic.EffortMax,
				MaxFailPercent: 30,
			},
			wantMax:         config.HealSafetyCapMax,
			wantHardCap:     false,
			wantProgress:    true,
			wantStuck:       3,
			wantFailPercent: 30,
		},
		{
			name: "always-verify with fast effort uses explicit directive path",
			opts: HealOpts{
				Sprint: &epic.Sprint{Number: 1, Name: "s"},
				Epic: &epic.Epic{
					TotalSprints:       1,
					MaxHealAttempts:    config.DefaultMaxHealAttempts, // 3, set by --always-verify
					MaxHealAttemptsSet: true,                          // also set by --always-verify (post-fix)
				},
				EffortLevel: epic.EffortFast,
			},
			wantMax:         config.DefaultMaxHealAttempts, // 3
			wantHardCap:     true,
			wantProgress:    false,
			wantStuck:       0,
			wantFailPercent: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := effectiveHealConfig(tt.opts)
			assert.Equal(t, tt.wantMax, cfg.maxAttempts, "maxAttempts")
			assert.Equal(t, tt.wantHardCap, cfg.hardCap, "hardCap")
			assert.Equal(t, tt.wantProgress, cfg.progressBased, "progressBased")
			assert.Equal(t, tt.wantStuck, cfg.stuckThreshold, "stuckThreshold")
			assert.Equal(t, tt.wantFailPercent, cfg.maxFailPercent, "maxFailPercent")
		})
	}
}

// --- Test engines ---

// progressEngine creates files on successive calls, simulating gradual fix progress.
type progressEngine struct {
	projectDir    string
	calls         int
	filesToCreate []string // create filesToCreate[i] on call i+1
}

func (p *progressEngine) Run(_ context.Context, _ string, opts engine.RunOpts) (string, int, error) {
	if p.calls < len(p.filesToCreate) {
		path := filepath.Join(p.projectDir, p.filesToCreate[p.calls])
		_ = os.WriteFile(path, []byte("fixed"), 0o644)
	}
	p.calls++
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("heal output\n"))
	}
	return "heal output\n", 0, nil
}

func (p *progressEngine) Name() string { return "progress-stub" }

// fixingEngine rewrites the verification file on each call to point to a file
// that exists, simulating an agent that fixes a bad verification check.
type fixingEngine struct {
	projectDir       string
	verificationPath string
	calls            int
}

func (f *fixingEngine) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	f.calls++
	// Create a file and rewrite verification to check for it
	existing := filepath.Join(f.projectDir, "exists.txt")
	_ = os.WriteFile(existing, []byte("ok"), 0o644)
	_ = os.WriteFile(f.verificationPath, []byte("@sprint 1\n@check_file exists.txt\n"), 0o644)
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("fixed verification\n"))
	}
	return "fixed verification\n", 0, nil
}

func (f *fixingEngine) Name() string { return "stub" }

type stubEngine struct {
	name    string
	prompts []string
}

func (s *stubEngine) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	s.prompts = append(s.prompts, prompt)
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("heal output\n"))
	}
	return "heal output\n", 0, nil
}

func (s *stubEngine) Name() string {
	return s.name
}

// --- D3: Targeted alignment unit tests ---

func TestHealGroupSeverity(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, healGroupSeverity(verify.CheckFile))
	assert.Equal(t, 1, healGroupSeverity(verify.CheckFileContains))
	assert.Equal(t, 2, healGroupSeverity(verify.CheckCmd))
	assert.Equal(t, 2, healGroupSeverity(verify.CheckCmdOutput))
	assert.Equal(t, 2, healGroupSeverity(verify.CheckTest))
}

func TestGroupFailedChecks(t *testing.T) {
	t.Parallel()

	results := []verify.CheckResult{
		{Check: verify.Check{Type: verify.CheckFile}, Passed: false},
		{Check: verify.Check{Type: verify.CheckCmd}, Passed: false},
		{Check: verify.Check{Type: verify.CheckFile}, Passed: true}, // passing — not counted
	}
	groups := groupFailedChecks(results)
	assert.Len(t, groups, 2)
	assert.Len(t, groups[0], 1, "one CheckFile failure in sev 0")
	assert.Len(t, groups[2], 1, "one CheckCmd failure in sev 2")
}

func TestHighestPriorityGroup(t *testing.T) {
	t.Parallel()

	groups := map[int][]verify.CheckResult{
		0: {{Check: verify.Check{Type: verify.CheckFile}}},
		2: {{Check: verify.Check{Type: verify.CheckCmd}}},
	}
	group := highestPriorityGroup(groups)
	require.Len(t, group, 1)
	assert.Equal(t, verify.CheckFile, group[0].Check.Type)
}

func TestHighestPriorityGroupEmpty(t *testing.T) {
	t.Parallel()

	assert.Nil(t, highestPriorityGroup(nil))
	assert.Nil(t, highestPriorityGroup(map[int][]verify.CheckResult{}))
}

func TestHighestPriorityGroupAllResolved(t *testing.T) {
	t.Parallel()

	// Simulate the "all groups resolved" path: start with a failing check that
	// produces a non-empty groups map, then re-run groupFailedChecks after the
	// check passes — the resulting map is empty and highestPriorityGroup returns nil.
	initially := []verify.CheckResult{
		{Check: verify.Check{Type: verify.CheckFile}, Passed: false},
	}
	groups := groupFailedChecks(initially)
	require.NotEmpty(t, groups, "precondition: initial failing check should produce non-empty groups")

	// After alignment, the same check now passes.
	afterHeal := []verify.CheckResult{
		{Check: verify.Check{Type: verify.CheckFile}, Passed: true},
	}
	resolvedGroups := groupFailedChecks(afterHeal)
	assert.Nil(t, highestPriorityGroup(resolvedGroups))
}

func TestTargetedHealSingleGroupResolves(t *testing.T) {
	t.Parallel()

	// progressEngine creates missing.txt on first call → single-group alignment succeeds
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	eng := &progressEngine{
		projectDir:    projectDir,
		filesToCreate: []string{"target.txt"},
	}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Targeted"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 3, MaxHealAttemptsSet: true},
		Engine:     eng,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "target.txt"},
		},
	})
	require.NoError(t, err)
	assert.True(t, result.Healed, "single-group alignment should resolve on first attempt")
	assert.Equal(t, 1, eng.calls, "should only need one alignment attempt")
}

func TestTargetedHealFallbackAfterStall(t *testing.T) {
	t.Parallel()

	// Engine never fixes anything → both severity groups stall → fallback to all-at-once.
	// With maxAttempts=4 and stall threshold=2, fallback triggers at iteration 2.
	// Loop continues to exhaustion (4 attempts total).
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	eng := &recordingHealEngine{projectDir: projectDir}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Stall"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 4, MaxHealAttemptsSet: true},
		Engine:     eng,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
			{Sprint: 1, Type: verify.CheckCmd, Command: "false"},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	require.Len(t, eng.prompts, 4, "all 4 attempts should run")

	// First attempt: targeted — only CheckFile (sev 0) in prompt
	assert.Contains(t, eng.prompts[0], "missing.txt", "first prompt should mention CheckFile")
	assert.NotContains(t, eng.prompts[0], "Command failed", "first prompt should not mention CheckCmd")

	// After fallback (iteration 2 onwards): all failures in prompt
	assert.Contains(t, eng.prompts[1], "missing.txt", "post-fallback prompt should mention CheckFile")
	assert.Contains(t, eng.prompts[1], "Command failed", "post-fallback prompt should mention CheckCmd")
}

func TestTargetedHealMixedSeverityOrder(t *testing.T) {
	t.Parallel()

	// CheckFile (sev 0) and CheckFileContains (sev 1) both failing.
	// First 2 attempts: sev 0 targeted. After stall: fallback to all-at-once.
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))
	// Create a file with wrong content so CheckFileContains fails
	writeFile(t, filepath.Join(projectDir, "config.txt"), "no match here\n")

	eng := &recordingHealEngine{projectDir: projectDir}

	result, err := RunHealLoop(context.Background(), HealOpts{
		ProjectDir: projectDir,
		Sprint:     &epic.Sprint{Number: 1, Name: "Mixed"},
		Epic:       &epic.Epic{TotalSprints: 1, MaxHealAttempts: 3, MaxHealAttemptsSet: true},
		Engine:     eng,
		SprintLogFile: sprintLog,
		Checks: []verify.Check{
			{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},              // sev 0
			{Sprint: 1, Type: verify.CheckFileContains, Path: "config.txt", Pattern: "required"}, // sev 1
		},
	})
	require.NoError(t, err)
	assert.False(t, result.Healed)
	require.GreaterOrEqual(t, len(eng.prompts), 1)

	// First prompt must target sev 0 (CheckFile) only
	assert.Contains(t, eng.prompts[0], "missing.txt")
	assert.NotContains(t, eng.prompts[0], "config.txt")
}

// recordingHealEngine records the contents of .fry/prompt.md on each Run call.
type recordingHealEngine struct {
	projectDir string
	prompts    []string
}

func (r *recordingHealEngine) Run(_ context.Context, _ string, opts engine.RunOpts) (string, int, error) {
	promptPath := filepath.Join(r.projectDir, config.PromptFile)
	data, _ := os.ReadFile(promptPath)
	r.prompts = append(r.prompts, string(data))
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("heal output\n"))
	}
	return "heal output\n", 0, nil
}

func (r *recordingHealEngine) Name() string { return "recording" }

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
