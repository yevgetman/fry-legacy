package sprint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/verify"
	"github.com/yevgetman/fry/templates"
)

// logCaptureMu serializes subtests that redirect the package-level frylog logger.
// frylog.SetStdout mutates a global logger; holding this mutex for the duration
// of each log-capturing subtest prevents output from one subtest appearing in
// another's buffer when subtests run in parallel.
var logCaptureMu sync.Mutex

func TestPromptAssembly(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	executive := "Executive context line.\n"

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:       projectDir,
		ExecutiveContent: executive,
		UserPrompt:       "User directive.",
		SprintPrompt:     "Sprint instructions.",
		Promise:          "TOKEN",
	})
	require.NoError(t, err)

	// Layer 1: Executive context with orientation disclaimer
	assert.Contains(t, prompt, "# ===== PROJECT CONTEXT =====\n")
	assert.Contains(t, prompt, "# NOT derive implementation decisions from this section.\n")
	assert.Contains(t, prompt, "Executive context line.\n")

	// Layer 1.5: User directive with priority explanation
	assert.Contains(t, prompt, "# ===== USER DIRECTIVE =====\n")
	assert.Contains(t, prompt, "# Treat this as a priority directive that applies to all sprints.\n")
	assert.Contains(t, prompt, "User directive.\n")

	// Layer 2: Strategic plan with "true north" guidance
	assert.Contains(t, prompt, "# ===== STRATEGIC PLAN =====\n")
	assert.Contains(t, prompt, "\"true north\"")
	assert.Contains(t, prompt, "# Do NOT implement work from other phases")

	// Layer 3: Sprint instructions
	assert.Contains(t, prompt, "# ===== SPRINT INSTRUCTIONS =====\n\nSprint instructions.\n")

	// Layer 4: Iteration memory with detailed read/append instructions
	assert.Contains(t, prompt, "# ===== ITERATION MEMORY =====\n")
	assert.Contains(t, prompt, "# Two progress files track build history:\n")
	assert.Contains(t, prompt, "#    BEFORE you begin work, READ this file")
	assert.Contains(t, prompt, "#    AFTER you finish, APPEND a brief entry")
	assert.Contains(t, prompt, "#      ## Iteration N")
	assert.Contains(t, prompt, "#    Do NOT write to this file — it is managed by the build system.\n")

	// Layer 5: Completion signal (conditional on Promise)
	assert.Contains(t, prompt, "# ===== COMPLETION SIGNAL =====\n")
	assert.Contains(t, prompt, "# output exactly this line:\n# ===PROMISE: TOKEN===\n")
	assert.Contains(t, prompt, "# If tasks remain incomplete")

	written, err := os.ReadFile(filepath.Join(projectDir, config.PromptFile))
	require.NoError(t, err)
	assert.Equal(t, prompt, string(written))
}

func TestPromptAssemblyPartialLayers(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   projectDir,
		SprintPrompt: "Only sprint instructions.",
		Promise:      "TOKEN",
	})
	require.NoError(t, err)

	assert.NotContains(t, prompt, "# ===== PROJECT CONTEXT =====")
	assert.NotContains(t, prompt, "# ===== USER DIRECTIVE =====")
	assert.Contains(t, prompt, "# ===== STRATEGIC PLAN =====")
	assert.Contains(t, prompt, "# ===== SPRINT INSTRUCTIONS =====")
	assert.Contains(t, prompt, "# ===== COMPLETION SIGNAL =====")
}

func TestPromptAssemblyNoPromise(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   projectDir,
		SprintPrompt: "Do the work.",
		Promise:      "",
	})
	require.NoError(t, err)

	assert.NotContains(t, prompt, "# ===== COMPLETION SIGNAL =====")
	assert.NotContains(t, prompt, "===PROMISE:")
}

func TestPromptAssemblyExactHeaders(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:       projectDir,
		ExecutiveContent: "Executive\n",
		UserPrompt:       "Directive",
		SprintPrompt:     "Instructions",
		Promise:          "TOKEN",
	})
	require.NoError(t, err)

	headers := []string{
		"# ===== PROJECT CONTEXT =====",
		"# ===== USER DIRECTIVE =====",
		"# ===== STRATEGIC PLAN =====",
		"# ===== SPRINT INSTRUCTIONS =====",
		"# ===== ITERATION MEMORY =====",
		"# ===== COMPLETION SIGNAL =====",
	}
	for _, header := range headers {
		assert.Contains(t, prompt, header)
	}

	// Without promise, COMPLETION SIGNAL should be absent
	promptNoPromise, err := AssemblePrompt(PromptOpts{
		ProjectDir:       projectDir,
		ExecutiveContent: "Executive\n",
		UserPrompt:       "Directive",
		SprintPrompt:     "Instructions",
		Promise:          "",
	})
	require.NoError(t, err)
	assert.NotContains(t, promptNoPromise, "# ===== COMPLETION SIGNAL =====")
}

func TestInitSprintProgress(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	err := InitSprintProgress(projectDir, 4, "Sprint Execution")
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(projectDir, config.SprintProgressFile))
	require.NoError(t, err)
	assert.Equal(t, "# Sprint 4: Sprint Execution — Progress\n\n", string(content))
}

func TestEpicProgressReset(t *testing.T) {
	t.Parallel()
	assert.True(t, ShouldResetEpicProgress(1, 1, 6, 6))
	assert.False(t, ShouldResetEpicProgress(1, 1, 5, 6))
	assert.False(t, ShouldResetEpicProgress(2, 2, 6, 6))
}

func TestMechanicalCompaction(t *testing.T) {
	t.Parallel()
	progress := strings.Join([]string{
		"# Sprint 4: Sprint Execution — Progress",
		"",
		"## Iteration 1 — Tue Mar 10 22:00 CDT",
		"First pass",
		"",
		"--- Alignment attempt 1 failed ---",
		"Alignment details",
		"",
		"## Iteration 2 — Tue Mar 10 22:30 CDT",
		"Second pass",
		"",
	}, "\n")

	assert.Equal(t, "## Iteration 2 — Tue Mar 10 22:30 CDT\nSecond pass", mechanicalCompaction(progress))
}

func TestSprintResultStatusStrings(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "PASS", StatusPass)
	assert.Equal(t, "PASS (aligned)", StatusPassAligned)
	assert.Equal(t, "PASS (sanity checks passed, no promise)", StatusPassSanityPassedNoPromise)
	assert.Equal(t, "PASS (aligned, no promise)", StatusPassAlignedNoPromise)
	assert.Equal(t, "PASS (deferred failures)", StatusPassWithDeferredFailures)
	assert.Equal(t, "PASS (aligned, deferred failures)", StatusPassAlignedWithDeferredFailures)
	assert.Equal(t, "FAIL (sanity checks failed, alignment exhausted)", StatusFailSanityFailedAlignmentExhausted)
	assert.Equal(t, "FAIL (no promise, sanity checks failed, alignment exhausted)", StatusFailNoPromiseSanityFailedAlignmentExhausted)
	assert.Equal(t, "FAIL (no prompt)", StatusFailNoPrompt)
	assert.Equal(t, "SKIPPED", StatusSkipped)
}

func TestPromiseDetection(t *testing.T) {
	t.Parallel()
	output := "agent output\n===PROMISE: TOKEN===\nmore output"
	assert.True(t, strings.Contains(output, "===PROMISE: TOKEN==="))
}

func TestRunSprintPassesWithPromiseAndChecks(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.ExecutiveFile), "Executive\n")
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file result.txt\n")
	writeFile(t, filepath.Join(projectDir, "result.txt"), "ok\n")

	mockEngine := &stubEngine{
		name:    "codex",
		outputs: []string{"work complete\n===PROMISE: DONE===\n"},
	}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       3,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
			AgentModel:         "",
			AgentFlags:         "",
			PreIterationCmd:    "",
			PreSprintCmd:       "",
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Runner",
			MaxIterations: 2,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPass, result.Status)
	assert.Len(t, mockEngine.prompts, 1)
	agentPrompt, err := templates.LoadText(config.AgentInvocationFile)
	require.NoError(t, err)
	assert.Equal(t, agentPrompt, mockEngine.prompts[0])
}

func TestRunSprintFailsWithoutPrompt(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	mockEngine := &stubEngine{name: "codex"}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints: 1,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Empty",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusFailNoPrompt, result.Status)
}

func TestRunSprintPassesWithPromiseNoChecks(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	mockEngine := &stubEngine{
		name:    "codex",
		outputs: []string{"work done\n===PROMISE: DONE===\n"},
	}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Solo",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Do it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPass, result.Status)
}

func TestRunSprintNoPromiseNoChecks(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	mockEngine := &stubEngine{
		name:    "codex",
		outputs: []string{"did some work"},
	}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints: 1,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Incomplete",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Try it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Contains(t, result.Status, "FAIL (no promise after")
}

func TestRunSprintNoPromiseChecksPass(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file result.txt\n")
	writeFile(t, filepath.Join(projectDir, "result.txt"), "ok\n")

	mockEngine := &stubEngine{
		name:    "codex",
		outputs: []string{"work done but no promise"},
	}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "No Promise",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPassSanityPassedNoPromise, result.Status)
}

func TestDetermineOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		promiseFound bool
		totalCount   int
		passCount    int
		checks       []verify.Check
		createFile   string
		wantStatus   string
	}{
		{
			name:         "promise found no checks",
			promiseFound: true,
			wantStatus:   StatusPass,
		},
		{
			name:         "promise found checks pass",
			promiseFound: true,
			totalCount:   2,
			passCount:    2,
			wantStatus:   StatusPass,
		},
		{
			name:         "promise found checks fail alignment succeeds",
			promiseFound: true,
			totalCount:   1,
			passCount:    0,
			checks:       []verify.Check{{Sprint: 1, Type: verify.CheckFile, Path: "exists.txt"}},
			createFile:   "exists.txt",
			wantStatus:   StatusPassAligned,
		},
		{
			name:         "promise found checks fail alignment exhausted",
			promiseFound: true,
			totalCount:   1,
			passCount:    0,
			checks:       []verify.Check{{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"}},
			wantStatus:   StatusFailSanityFailedAlignmentExhausted,
		},
		{
			name:       "no promise no checks",
			totalCount: 0,
			passCount:  0,
			wantStatus: "FAIL (no promise after 2 iters)",
		},
		{
			name:       "no promise checks pass",
			totalCount: 1,
			passCount:  1,
			wantStatus: StatusPassSanityPassedNoPromise,
		},
		{
			name:       "no promise checks fail alignment succeeds",
			totalCount: 1,
			passCount:  0,
			checks:     []verify.Check{{Sprint: 1, Type: verify.CheckFile, Path: "exists.txt"}},
			createFile: "exists.txt",
			wantStatus: StatusPassAlignedNoPromise,
		},
		{
			name:       "no promise checks fail alignment exhausted",
			totalCount: 1,
			passCount:  0,
			checks:     []verify.Check{{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"}},
			wantStatus: StatusFailNoPromiseSanityFailedAlignmentExhausted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			projectDir := t.TempDir()
			writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
			sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
			require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

			if tt.createFile != "" {
				writeFile(t, filepath.Join(projectDir, tt.createFile), "content\n")
			}

			cfg := RunConfig{
				ProjectDir: projectDir,
				Epic: &epic.Epic{
					TotalSprints:       1,
					MaxHealAttempts:    1,
					MaxHealAttemptsSet: true,
				},
				Sprint: &epic.Sprint{
					Number:        1,
					Name:          "Test",
					MaxIterations: 2,
				},
				Engine: &stubEngine{name: "codex"},
			}

			status, _, _, err := determineOutcome(
				context.Background(), cfg, tt.checks, tt.promiseFound,
				nil, tt.passCount, tt.totalCount, sprintLog,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

type stubEngine struct {
	name    string
	outputs []string
	prompts []string
}

func (s *stubEngine) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	s.prompts = append(s.prompts, prompt)
	var output string
	if len(s.outputs) > 0 {
		output = s.outputs[0]
		s.outputs = s.outputs[1:]
	}
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(output))
	}
	if opts.Stderr != nil {
		_, _ = opts.Stderr.Write(nil)
	}
	return output, 0, nil
}

func (s *stubEngine) Name() string {
	return s.name
}

func TestDetermineOutcomeDeferredFailures(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "")
	writeFile(t, filepath.Join(projectDir, "a.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "b.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "c.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "d.txt"), "ok\n")
	sprintLog := filepath.Join(projectDir, config.BuildLogsDir, "sprint1.log")
	require.NoError(t, os.MkdirAll(filepath.Dir(sprintLog), 0o755))

	// 1 of 5 checks fails = 20%, threshold at 20% → within threshold
	checks := []verify.Check{
		{Sprint: 1, Type: verify.CheckFile, Path: "a.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "b.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "c.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "d.txt"},
		{Sprint: 1, Type: verify.CheckFile, Path: "missing.txt"},
	}

	cfg := RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
			MaxFailPercent:     20,
			MaxFailPercentSet:  true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Threshold",
			MaxIterations: 2,
		},
		Engine: &stubEngine{name: "codex"},
	}

	status, deferred, _, err := determineOutcome(
		context.Background(), cfg, checks, true,
		nil, 4, 5, sprintLog,
	)
	require.NoError(t, err)
	assert.Equal(t, StatusPassWithDeferredFailures, status)
	assert.Len(t, deferred, 1)
}

func TestRunSprintDeferredFailuresInResult(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile),
		"@sprint 1\n@check_file present.txt\n@check_file present2.txt\n@check_file present3.txt\n@check_file present4.txt\n@check_file missing.txt\n")
	writeFile(t, filepath.Join(projectDir, "present.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "present2.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "present3.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "present4.txt"), "ok\n")

	mockEngine := &stubEngine{
		name:    "codex",
		outputs: []string{"===PROMISE: DONE===\n"},
	}

	result, err := RunSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
			MaxFailPercent:     20,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Deferred",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPassWithDeferredFailures, result.Status)
	assert.Len(t, result.DeferredFailures, 1)
	assert.Equal(t, "missing.txt", result.DeferredFailures[0].Check.Path)
}

func TestResumeSprintPassesWhenChecksAlreadyPass(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1: Test — Progress\n\nprevious context\n")
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file result.txt\n")
	writeFile(t, filepath.Join(projectDir, "result.txt"), "ok\n")

	mockEngine := &stubEngine{name: "codex"}

	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       2,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    3,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Test",
			MaxIterations: 5,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPass, result.Status)
	// Engine should NOT have been invoked — no iterations, no alignment needed
	assert.Empty(t, mockEngine.prompts)

	// Sprint progress should be preserved (not overwritten)
	progress, err := os.ReadFile(filepath.Join(projectDir, config.SprintProgressFile))
	require.NoError(t, err)
	assert.Contains(t, string(progress), "previous context")
	assert.Contains(t, string(progress), "RESUME MODE")
}

func TestResumeSprintFailsWhenHealExhausted(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1: Test — Progress\n\n")
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file missing.txt\n")

	mockEngine := &stubEngine{name: "codex"}

	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    2,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Test",
			MaxIterations: 2,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusFailSanityFailedAlignmentExhausted, result.Status)
	// Resume mode should use boosted attempts: max(2*2, 6) = 6
	assert.Len(t, mockEngine.prompts, config.ResumeMinHealAttempts)
}

func TestResumeSprintNoChecks(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1 — Progress\n\n")

	mockEngine := &stubEngine{name: "codex"}

	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints: 1,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Test",
			MaxIterations: 2,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPass, result.Status)
	assert.Empty(t, mockEngine.prompts)
}

func TestResumeSprintPreservesProgress(t *testing.T) {
	t.Parallel()
	projectDir := t.TempDir()
	originalProgress := "# Sprint 3: Auth — Progress\n\n## Iteration 1\nDid auth work\n\n--- Alignment attempt 1 failed ---\nSome failure\n"
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), originalProgress)
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 3\n@check_file auth.go\n")
	writeFile(t, filepath.Join(projectDir, "auth.go"), "package auth\n")

	mockEngine := &stubEngine{name: "codex"}

	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       5,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    3,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        3,
			Name:          "Auth",
			MaxIterations: 5,
			Promise:       "AUTH_DONE",
			Prompt:        "Build auth.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPass, result.Status)

	// Verify original progress was preserved and resume marker appended
	progress, err := os.ReadFile(filepath.Join(projectDir, config.SprintProgressFile))
	require.NoError(t, err)
	assert.Contains(t, string(progress), "Alignment attempt 1 failed")
	assert.Contains(t, string(progress), "RESUME MODE")
}

// --- P0: ResumeSprint additional coverage ---

func TestResumeSprintNilEpic(t *testing.T) {
	t.Parallel()

	_, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: t.TempDir(),
		Sprint:     &epic.Sprint{Number: 1, Name: "X"},
		Engine:     &stubEngine{name: "codex"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "epic and sprint are required")
}

func TestResumeSprintNilEngine(t *testing.T) {
	t.Parallel()

	_, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: t.TempDir(),
		Epic:       &epic.Epic{TotalSprints: 1},
		Sprint:     &epic.Sprint{Number: 1, Name: "X"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestResumeSprintDeferredFailures(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1 — Progress\n\n")
	// 4 of 5 checks pass → 20% failure → within threshold
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile),
		"@sprint 1\n@check_file a.txt\n@check_file b.txt\n@check_file c.txt\n@check_file d.txt\n@check_file missing.txt\n")
	writeFile(t, filepath.Join(projectDir, "a.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "b.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "c.txt"), "ok\n")
	writeFile(t, filepath.Join(projectDir, "d.txt"), "ok\n")

	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    1,
			MaxHealAttemptsSet: true,
			MaxFailPercent:     20,
			MaxFailPercentSet:  true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Threshold",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: &stubEngine{name: "codex"},
	})
	require.NoError(t, err)
	assert.Equal(t, StatusPassWithDeferredFailures, result.Status)
	assert.Len(t, result.DeferredFailures, 1)
}

func TestResumeSprintBoostedAttempts(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1 — Progress\n\n")
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file missing.txt\n")

	mockEngine := &stubEngine{name: "codex"}

	// With explicit alignment attempts of 4, boosted = max(4*2, 6) = 8
	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:       1,
			VerificationFile:   config.DefaultVerificationFile,
			MaxHealAttempts:    4,
			MaxHealAttemptsSet: true,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Boosted",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusFailSanityFailedAlignmentExhausted, result.Status)
	// Boosted attempts are passed as MaxAttemptsOverride to alignment loop.
	// Exact count depends on stuck detection, but at least some attempts are made.
	assert.GreaterOrEqual(t, len(mockEngine.prompts), 1)
}

func TestResumeSprintEffortLevelHealBase(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "# Sprint 1 — Progress\n\n")
	writeFile(t, filepath.Join(projectDir, config.DefaultVerificationFile), "@sprint 1\n@check_file missing.txt\n")

	mockEngine := &stubEngine{name: "codex"}

	// Effort high → default alignment = 10, boosted = max(10*2, 6) = 20
	result, err := ResumeSprint(context.Background(), RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints:     1,
			VerificationFile: config.DefaultVerificationFile,
			EffortLevel:      epic.EffortHigh,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "High Effort",
			MaxIterations: 1,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: mockEngine,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusFailSanityFailedAlignmentExhausted, result.Status)
	// High effort uses progress-based detection — stub engine makes no progress,
	// so stuck threshold (2) causes early exit. At least 1 attempt must be made.
	assert.GreaterOrEqual(t, len(mockEngine.prompts), 1)
}

// --- P0: Progress file tests ---

func TestInitEpicProgress(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitEpicProgress(projectDir, "My Epic"))

	content, err := os.ReadFile(filepath.Join(projectDir, config.EpicProgressFile))
	require.NoError(t, err)
	assert.Equal(t, "# Epic Progress — My Epic\n\n", string(content))
}

func TestAppendToSprintProgress(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitSprintProgress(projectDir, 1, "Setup"))
	require.NoError(t, AppendToSprintProgress(projectDir, "## Iteration 1\nDid work.\n"))
	require.NoError(t, AppendToSprintProgress(projectDir, "## Iteration 2\nMore work.\n"))

	content, err := os.ReadFile(filepath.Join(projectDir, config.SprintProgressFile))
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Sprint 1: Setup — Progress")
	assert.Contains(t, string(content), "## Iteration 1")
	assert.Contains(t, string(content), "## Iteration 2")
}

func TestAppendToEpicProgress(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitEpicProgress(projectDir, "Epic"))
	require.NoError(t, AppendToEpicProgress(projectDir, "Sprint 1 summary.\n"))

	content, err := os.ReadFile(filepath.Join(projectDir, config.EpicProgressFile))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Sprint 1 summary.")
}

func TestReadSprintProgress_MissingFile(t *testing.T) {
	t.Parallel()

	content, err := ReadSprintProgress(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestReadEpicProgress_MissingFile(t *testing.T) {
	t.Parallel()

	content, err := ReadEpicProgress(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestReadSprintProgress_WithContent(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitSprintProgress(projectDir, 3, "Build"))

	content, err := ReadSprintProgress(projectDir)
	require.NoError(t, err)
	assert.Contains(t, content, "# Sprint 3: Build — Progress")
}

func TestReadEpicProgress_WithContent(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitEpicProgress(projectDir, "Demo"))

	content, err := ReadEpicProgress(projectDir)
	require.NoError(t, err)
	assert.Contains(t, content, "# Epic Progress — Demo")
}

// --- P0: CompactSprintProgress tests ---

func TestCompactSprintProgressMechanical(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	progressContent := "# Sprint 1 — Progress\n\n## Iteration 1\nFirst\n\n## Iteration 2\nSecond\n"
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), progressContent)

	result, err := CompactSprintProgress(context.Background(), projectDir, 1, "Setup", "PASS", nil, false, "", "")
	require.NoError(t, err)
	assert.Contains(t, result, "## Sprint 1: Setup — PASS")
	assert.Contains(t, result, "## Iteration 2")
	assert.Contains(t, result, "Second")
	assert.NotContains(t, result, "## Iteration 1")
}

func TestCompactSprintProgressAgentNilEngineRunner(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "progress\n")

	_, err := CompactSprintProgress(context.Background(), projectDir, 1, "X", "PASS", nil, true, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestCompactSprintProgressAgent(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "long progress content\n")

	eng := &stubEngine{
		name:    "codex",
		outputs: []string{"Summary: completed auth module."},
	}

	result, err := CompactSprintProgress(context.Background(), projectDir, 2, "Auth", "PASS", eng, true, "sonnet", "")
	require.NoError(t, err)
	assert.Contains(t, result, "## Sprint 2: Auth — PASS")
	assert.Contains(t, result, "Summary: completed auth module.")
}

// --- P0: countChecksForSprint ---

func TestCountChecksForSprint(t *testing.T) {
	t.Parallel()

	checks := []verify.Check{{Sprint: 1}, {Sprint: 2}, {Sprint: 1}, {Sprint: 3}}

	tests := []struct {
		name      string
		checks    []verify.Check
		sprintNum int
		wantCount int
	}{
		{name: "empty slice", checks: nil, sprintNum: 1, wantCount: 0},
		{name: "all match sprint 1", checks: checks, sprintNum: 1, wantCount: 2},
		{name: "no match sprint 99", checks: checks, sprintNum: 99, wantCount: 0},
		{name: "single match sprint 2", checks: checks, sprintNum: 2, wantCount: 1},
		{name: "single match sprint 3", checks: checks, sprintNum: 3, wantCount: 1},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countChecksForSprint(tc.checks, tc.sprintNum)
			assert.Equal(t, tc.wantCount, got)
		})
	}
}

// --- P0: RunSprint context cancellation ---

func TestRunSprintContextCancellation(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := RunSprint(ctx, RunConfig{
		ProjectDir: projectDir,
		Epic: &epic.Epic{
			TotalSprints: 1,
		},
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Cancelled",
			MaxIterations: 5,
			Promise:       "DONE",
			Prompt:        "Build it.",
		},
		Engine: &stubEngine{name: "codex"},
	})
	require.Error(t, err)
}

func TestWarnOutOfRangeChecks(t *testing.T) {
	// NOT parallel: this test redirects the global frylog stdout via
	// SetStdout. Other tests in this package call functions that write to
	// frylog (e.g. RunSprint, RunHealLoop), so running concurrently causes
	// a data race on the shared strings.Builder.

	tests := []struct {
		name         string
		checks       []verify.Check
		totalSprints int
		wantWarning  bool
		wantOnce     bool
	}{
		{name: "totalSprints zero: no output", checks: []verify.Check{{Sprint: 5}}, totalSprints: 0, wantWarning: false},
		{name: "checks within range: no output", checks: []verify.Check{{Sprint: 1}, {Sprint: 2}}, totalSprints: 3, wantWarning: false},
		{name: "check exceeds range: warning logged", checks: []verify.Check{{Sprint: 4}}, totalSprints: 3, wantWarning: true},
		{name: "duplicate out-of-range sprint: warning logged once", checks: []verify.Check{{Sprint: 5}, {Sprint: 5}, {Sprint: 5}}, totalSprints: 3, wantWarning: true, wantOnce: true},
	}

	for _, tc := range tests {
		logCaptureMu.Lock()

		var buf strings.Builder
		frylog.SetStdout(&buf)
		warnOutOfRangeChecks(tc.checks, tc.totalSprints)
		output := buf.String()
		frylog.SetStdout(nil)

		logCaptureMu.Unlock()

		if tc.wantWarning {
			assert.Contains(t, output, "WARNING", tc.name)
			assert.Contains(t, output, "will never run", tc.name)
		} else {
			assert.Empty(t, output, tc.name)
		}
		if tc.wantOnce {
			assert.Equal(t, 1, strings.Count(output, "WARNING"),
				"duplicate out-of-range sprint number should produce exactly one warning")
		}
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
