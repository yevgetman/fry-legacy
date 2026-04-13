package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
)

func TestParseVerdictContinue(t *testing.T) {
	t.Parallel()
	assert.Equal(t, VerdictContinue, ParseVerdict(`{"verdict":"CONTINUE"}`))
}

func TestParseVerdictDeviate(t *testing.T) {
	t.Parallel()
	assert.Equal(t, VerdictDeviate, ParseVerdict(`{"verdict":"DEVIATE"}`))
}

func TestParseVerdictDefault(t *testing.T) {
	t.Parallel()
	assert.Equal(t, VerdictContinue, ParseVerdict("no verdict"))
}

func TestExtractDeviationSpec(t *testing.T) {
	t.Parallel()

	output := "### Decision\n{\"verdict\": \"DEVIATE\"}\n\n### Deviation Spec\n- **Trigger**: Auth middleware built at internal/middleware/auth instead of pkg/auth\n- **Affected sprints**: 4, 5\n- **Sprint 4**: Update 3 import path references from pkg/auth to internal/middleware/auth\n- **Sprint 5**: Update 1 wiring reference\n- **Risk assessment**: Low — purely mechanical path changes\n"

	spec := ExtractDeviationSpec(output)
	require.NotNil(t, spec)
	assert.Equal(t, "Auth middleware built at internal/middleware/auth instead of pkg/auth", spec.Trigger)
	assert.Equal(t, []int{4, 5}, spec.AffectedSprints)
	assert.Contains(t, spec.Details, "Sprint 4")
	assert.Equal(t, "Low — purely mechanical path changes", spec.RiskAssessment)
}

func TestSimulationOutput(t *testing.T) {
	t.Parallel()

	continueOutput, err := simulatedReviewOutput("CONTINUE", 2, 5)
	require.NoError(t, err)
	assert.Contains(t, continueOutput, `"verdict": "CONTINUE"`)

	deviateOutput, err := simulatedReviewOutput("DEVIATE", 2, 5)
	require.NoError(t, err)
	assert.Contains(t, deviateOutput, `"verdict": "DEVIATE"`)
	assert.Contains(t, deviateOutput, "- **Affected sprints**: 3")
}

func TestValidateReplan(t *testing.T) {
	t.Parallel()

	original := mustParseEpic(t, `
@epic Demo
@review_between_sprints
@review_engine claude
@sprint 1
@name One
@max_iterations 3
@promise ONE
@prompt
Keep one.
@sprint 2
@name Two
@max_iterations 3
@promise TWO
@prompt
Keep two.
`)

	updated := mustParseEpic(t, `
@epic Demo
@review_between_sprints
@review_engine claude
@sprint 1
@name One
@max_iterations 3
@promise ONE
@prompt
Keep one.
@sprint 2
@name Two
@max_iterations 3
@promise TWO
@prompt
Update two.
`)

	require.NoError(t, ValidateReplan(original, updated, 1, 2))

	updatedBad := mustParseEpic(t, `
@epic Demo
@review_between_sprints
@review_engine claude
@sprint 1
@name One
@max_iterations 3
@promise ONE
@prompt
Changed one.
@sprint 2
@name Two
@max_iterations 3
@promise TWO
@prompt
Update two.
`)

	err := ValidateReplan(original, updatedBad, 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "completed sprint 1")
}

func TestValidateReplanScopeCap(t *testing.T) {
	t.Parallel()

	original := mustParseEpic(t, baseEpicForValidation("Third"))
	updated := mustParseEpic(t, strings.Replace(baseEpicForValidation("Third"), "Third prompt.", "Third changed.", 1))

	err := ValidateReplan(original, updated, 1, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside deviation scope")
}

func TestValidateReplanStructuralPreservation(t *testing.T) {
	t.Parallel()

	original := mustParseEpic(t, baseEpicForValidation("Third"))
	updated := mustParseEpic(t, strings.Replace(baseEpicForValidation("Third"), "@name Two", "@name Two Updated", 1))

	err := ValidateReplan(original, updated, 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "structural directives")
}

func mustParseEpic(t *testing.T, content string) *epic.Epic {
	t.Helper()

	file := t.TempDir() + "/epic.md"
	require.NoError(t, osWriteFile(file, []byte(strings.TrimSpace(content)+"\n"), 0o644))
	ep, err := epic.ParseEpic(file)
	require.NoError(t, err)
	return ep
}

func baseEpicForValidation(thirdPrompt string) string {
	return `
@epic Demo
@review_between_sprints
@review_engine claude
@sprint 1
@name One
@max_iterations 3
@promise ONE
@prompt
First prompt.
@sprint 2
@name Two
@max_iterations 3
@promise TWO
@prompt
Second prompt.
@sprint 3
@name Three
@max_iterations 3
@promise THREE
@prompt
` + thirdPrompt + ` prompt.
`
}

func osWriteFile(name string, data []byte, perm uint32) error {
	return os.WriteFile(name, data, os.FileMode(perm))
}

func TestExtractSprintPrompt(t *testing.T) {
	t.Parallel()

	epicContent := `@epic Demo
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
First prompt line.
Second prompt line.
@sprint 2
@name Two
@max_iterations 1
@promise TWO
@prompt
Second sprint prompt.
@end
@sprint 3
@name Three
@max_iterations 1
@promise THREE
@prompt
Third sprint.`

	tests := []struct {
		name      string
		sprintNum int
		want      string
	}{
		{"first sprint multi-line", 1, "First prompt line.\nSecond prompt line."},
		{"second sprint with end directive", 2, "Second sprint prompt."},
		{"third sprint at end of file", 3, "Third sprint."},
		{"missing sprint", 99, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ExtractSprintPrompt(epicContent, tt.sprintNum))
		})
	}
}

func TestAssembleReviewPrompt(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	prompt, err := AssembleReviewPrompt(ReviewPromptOpts{
		ProjectDir:             projectDir,
		SprintNum:              3,
		TotalSprints:           5,
		SprintName:             "Build API",
		RemainingSprintPrompts: []string{"### Sprint 4: Tests\n\nWrite tests.", "### Sprint 5: Deploy\n\nDeploy."},
		EpicProgressContent:    "Sprint 1 done. Sprint 2 done.",
		SprintProgressContent:  "Iteration 1: built endpoints.",
		DeviationLogContent:    "",
	})
	require.NoError(t, err)

	assert.Contains(t, prompt, "# Sprint Review — After Sprint 3: Build API")
	assert.Contains(t, prompt, "Sprint 4 through 5")
	assert.Contains(t, prompt, "## Bias: CONTINUE")
	assert.Contains(t, prompt, "Sprint 1 done. Sprint 2 done.")
	assert.Contains(t, prompt, "Iteration 1: built endpoints.")
	assert.Contains(t, prompt, "### Sprint 4: Tests")
	assert.Contains(t, prompt, "### Sprint 5: Deploy")
	assert.Contains(t, prompt, "None — this is the first review.")
	assert.Contains(t, prompt, `"verdict": "CONTINUE"`)

	_, err = os.Stat(filepath.Join(projectDir, config.ReviewPromptFile))
	assert.NoError(t, err)
}

func TestRunReplanEndToEnd(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	epicContent := "@epic Demo\n@review_between_sprints\n@review_engine claude\n" +
		"@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst prompt.\n" +
		"@sprint 2\n@name Two\n@max_iterations 3\n@promise TWO\n@prompt\nSecond prompt.\n"

	epicPath := filepath.Join(projectDir, config.FryDir, "epic.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(epicPath), 0o755))
	require.NoError(t, os.WriteFile(epicPath, []byte(epicContent), 0o644))

	planPath := filepath.Join(projectDir, config.PlanFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(planPath), 0o755))
	require.NoError(t, os.WriteFile(planPath, []byte("Plan content\n"), 0o644))

	modifiedEpic := strings.Replace(epicContent, "Second prompt.", "Second prompt updated.", 1)
	mockEngine := &stubReplanEngine{output: modifiedEpic}

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir: projectDir,
		EpicPath:   epicPath,
		DeviationSpec: &DeviationSpec{
			Trigger:         "Test trigger",
			AffectedSprints: []int{2},
			RiskAssessment:  "Low",
			RawText:         "Test deviation",
		},
		CompletedSprint: 1,
		MaxScope:        2,
		Engine:          mockEngine,
	})
	require.NoError(t, err)

	updated, err := os.ReadFile(epicPath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "Second prompt updated.")
	assert.Contains(t, string(updated), "First prompt.")

	backups, err := filepath.Glob(filepath.Join(projectDir, config.BuildLogsDir, "epic.md.bak.*"))
	require.NoError(t, err)
	assert.Len(t, backups, 1)
}

type stubReplanEngine struct {
	output string
}

func (s *stubReplanEngine) Run(_ context.Context, _ string, opts engine.RunOpts) (string, int, error) {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(s.output))
	}
	return s.output, 0, nil
}

func (s *stubReplanEngine) Name() string {
	return "stub"
}

func TestAssembleReviewPromptWritingMode(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	prompt, err := AssembleReviewPrompt(ReviewPromptOpts{
		ProjectDir:   projectDir,
		SprintNum:    2,
		TotalSprints: 4,
		SprintName:   "Chapter One",
		Mode:         "writing",
	})
	require.NoError(t, err)

	assert.Contains(t, prompt, "content plan reviewer")
	assert.NotContains(t, prompt, "build plan reviewer")
}

func TestAssembleReviewPromptDefaults(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	prompt, err := AssembleReviewPrompt(ReviewPromptOpts{
		ProjectDir:   projectDir,
		SprintNum:    1,
		TotalSprints: 1,
		SprintName:   "Solo",
	})
	require.NoError(t, err)

	assert.Contains(t, prompt, "(No epic progress recorded yet.)")
	assert.Contains(t, prompt, "(No sprint progress recorded.)")
	assert.Contains(t, prompt, "(No remaining sprints.)")
}

// --- P0: RunSprintReview tests ---

func TestRunSprintReview_EmptyProjectDir(t *testing.T) {
	t.Parallel()

	_, err := RunSprintReview(context.Background(), RunReviewOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project dir is required")
}

func TestRunSprintReview_SimulateContinue(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	result, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:      projectDir,
		SprintNum:       1,
		TotalSprints:    3,
		SprintName:      "Setup",
		SimulateVerdict: "CONTINUE",
		Epic: &epic.Epic{
			TotalSprints: 3,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup", Prompt: "Build setup."},
				{Number: 2, Name: "Core", Prompt: "Build core."},
				{Number: 3, Name: "Polish", Prompt: "Polish."},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictContinue, result.Verdict)
	assert.Nil(t, result.Deviation)
}

func TestRunSprintReview_SimulateDeviate(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	result, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:      projectDir,
		SprintNum:       1,
		TotalSprints:    3,
		SprintName:      "Setup",
		SimulateVerdict: "DEVIATE",
		Epic: &epic.Epic{
			TotalSprints: 3,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup", Prompt: "Build setup."},
				{Number: 2, Name: "Core", Prompt: "Build core."},
				{Number: 3, Name: "Polish", Prompt: "Polish."},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictDeviate, result.Verdict)
	assert.NotNil(t, result.Deviation)
	assert.Contains(t, result.Deviation.RawText, "Simulated deviation")
}

func TestRunSprintReview_SimulateInvalidVerdict(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	_, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:      projectDir,
		SprintNum:       1,
		TotalSprints:    2,
		SprintName:      "Setup",
		SimulateVerdict: "INVALID",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown simulation verdict")
}

func TestRunSprintReview_NilEngineWithoutSimulation(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	_, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:   projectDir,
		SprintNum:    1,
		TotalSprints: 2,
		SprintName:   "Setup",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestRunSprintReview_WithEngine(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	eng := &stubReplanEngine{
		output: "### Analysis\nAll good.\n\n### Decision\n{\"verdict\":\"CONTINUE\"}\n",
	}

	result, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:   projectDir,
		SprintNum:    1,
		TotalSprints: 2,
		SprintName:   "Setup",
		Engine:       eng,
		Epic: &epic.Epic{
			TotalSprints: 2,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup", Prompt: "Build setup."},
				{Number: 2, Name: "Core", Prompt: "Build core."},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictContinue, result.Verdict)
}

func TestRunSprintReview_WritesReviewLog(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	engineOutput := "### Analysis\nAll good.\n\n### Decision\n{\"verdict\":\"CONTINUE\"}\n"
	eng := &stubReplanEngine{output: engineOutput}

	result, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:      projectDir,
		SprintNum:       2,
		TotalSprints:    3,
		SprintName:      "Core",
		Engine:          eng,
		SimulateVerdict: "",
		Epic: &epic.Epic{
			TotalSprints: 3,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup", Prompt: "Build setup."},
				{Number: 2, Name: "Core", Prompt: "Build core."},
				{Number: 3, Name: "Polish", Prompt: "Polish."},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictContinue, result.Verdict)

	matches, err := filepath.Glob(filepath.Join(projectDir, ".fry/build-logs", "sprint*_review_*.log"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), engineOutput)
}

func TestRunSprintReview_EngineReturnsDeviate(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".fry"), 0o755))

	eng := &stubReplanEngine{
		output: "### Analysis\nDeviated.\n\n### Decision\n{\"verdict\":\"DEVIATE\"}\n\n### Deviation Spec\n- **Trigger**: path changed\n- **Affected sprints**: 2\n- **Risk assessment**: Low\n",
	}

	result, err := RunSprintReview(context.Background(), RunReviewOpts{
		ProjectDir:   projectDir,
		SprintNum:    1,
		TotalSprints: 2,
		SprintName:   "Setup",
		Engine:       eng,
		Epic: &epic.Epic{
			TotalSprints: 2,
			Sprints: []epic.Sprint{
				{Number: 1, Name: "Setup", Prompt: "Build setup."},
				{Number: 2, Name: "Core", Prompt: "Build core."},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictDeviate, result.Verdict)
	require.NotNil(t, result.Deviation)
	assert.Equal(t, "path changed", result.Deviation.Trigger)
}

// --- AssembleReviewPrompt max effort ---

func TestAssembleReviewPromptMaxEffort(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	prompt, err := AssembleReviewPrompt(ReviewPromptOpts{
		ProjectDir:   projectDir,
		SprintNum:    1,
		TotalSprints: 3,
		SprintName:   "Setup",
		EffortLevel:  epic.EffortMax,
	})
	require.NoError(t, err)

	assert.Contains(t, prompt, "## Bias: THOROUGH REVIEW")
	assert.NotContains(t, prompt, "## Bias: CONTINUE")
	assert.Contains(t, prompt, "heightened scrutiny")
}

// --- ExtractDeviationSpec edge cases ---

func TestExtractDeviationSpec_NoSection(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ExtractDeviationSpec("### Decision\n{\"verdict\":\"CONTINUE\"}\n"))
}

func TestExtractDeviationSpec_EmptyBody(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ExtractDeviationSpec("### Deviation Spec\n\n"))
}

// --- RunReplan additional tests ---

func TestRunReplan_EmptyProjectDir(t *testing.T) {
	t.Parallel()

	err := RunReplan(context.Background(), ReplanOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project dir is required")
}

func TestRunReplan_MissingDeviationSpec(t *testing.T) {
	t.Parallel()

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir: t.TempDir(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deviation spec")
}

func TestRunReplan_DryRun(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	epicContent := "@epic Demo\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n" +
		"@sprint 2\n@name Two\n@max_iterations 3\n@promise TWO\n@prompt\nSecond.\n"
	epicPath := filepath.Join(projectDir, config.FryDir, "epic.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(epicPath), 0o755))
	require.NoError(t, os.WriteFile(epicPath, []byte(epicContent), 0o644))

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir:      projectDir,
		EpicPath:        epicPath,
		DeviationSpec:   &DeviationSpec{Trigger: "test", RawText: "test deviation"},
		CompletedSprint: 1,
		MaxScope:        2,
		DryRun:          true,
	})
	require.NoError(t, err)

	promptPath := filepath.Join(projectDir, config.FryDir, "replan-prompt.md")
	_, err = os.Stat(promptPath)
	assert.NoError(t, err)
}

func TestRunReplan_NilEngine(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	epicContent := "@epic Demo\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n"
	epicPath := filepath.Join(projectDir, config.FryDir, "epic.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(epicPath), 0o755))
	require.NoError(t, os.WriteFile(epicPath, []byte(epicContent), 0o644))

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir:      projectDir,
		EpicPath:        epicPath,
		DeviationSpec:   &DeviationSpec{RawText: "test"},
		CompletedSprint: 0,
		MaxScope:        1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

// --- ValidateReplan additional ---

func TestValidateReplan_NilArgs(t *testing.T) {
	t.Parallel()

	err := ValidateReplan(nil, nil, 1, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "original and updated epic are required")
}

func TestValidateReplan_SprintCountChanged(t *testing.T) {
	t.Parallel()

	original := mustParseEpic(t, "@epic Demo\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n@sprint 2\n@name Two\n@max_iterations 3\n@promise TWO\n@prompt\nSecond.\n")
	updated := mustParseEpic(t, "@epic Demo\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n")

	err := ValidateReplan(original, updated, 0, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sprint count changed")
}

func TestValidateReplan_GlobalDirectivesChanged(t *testing.T) {
	t.Parallel()

	original := mustParseEpic(t, "@epic Demo\n@engine claude\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n")
	updated := mustParseEpic(t, "@epic Demo\n@engine codex\n@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst.\n")

	err := ValidateReplan(original, updated, 0, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "global directives")
}

// --- Helper function tests ---

func TestDefaultIfEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "fallback", defaultIfEmpty("", "fallback"))
	assert.Equal(t, "fallback", defaultIfEmpty("   ", "fallback"))
	assert.Equal(t, "value", defaultIfEmpty("value\n", "fallback"))
}

func TestAfterColon(t *testing.T) {
	t.Parallel()

	assert.Equal(t, " value", afterColon("key: value"))
	assert.Equal(t, "", afterColon("no colon here"))
	assert.Equal(t, " a:b", afterColon("key: a:b"))
}

func TestSimulationOutput_LastSprint(t *testing.T) {
	t.Parallel()

	output, err := simulatedReviewOutput("DEVIATE", 5, 5)
	require.NoError(t, err)
	assert.Contains(t, output, "- **Affected sprints**: 5")
}

func TestParseSprintNumber(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 3, parseSprintNumber("@sprint 3"))
	assert.Equal(t, 12, parseSprintNumber("@sprint 12"))
	assert.Equal(t, 0, parseSprintNumber("not a sprint"))
	assert.Equal(t, 0, parseSprintNumber("@sprint"))
}

// --- Replan retry tests ---

// stubReplanEngineMulti returns successive outputs on each call.
type stubReplanEngineMulti struct {
	outputs []string
	prompts []string
	call    int
}

func (s *stubReplanEngineMulti) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	s.prompts = append(s.prompts, prompt)
	idx := s.call
	if idx >= len(s.outputs) {
		idx = len(s.outputs) - 1
	}
	out := s.outputs[idx]
	s.call++
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(out))
	}
	return out, 0, nil
}

func (s *stubReplanEngineMulti) Name() string {
	return "stub"
}

func TestRunReplan_RetryOnCompletedSprintModification(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	epicContent := "@epic Demo\n@review_between_sprints\n@review_engine claude\n" +
		"@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst prompt.\n" +
		"@sprint 2\n@name Two\n@max_iterations 3\n@promise TWO\n@prompt\nSecond prompt.\n"

	epicPath := filepath.Join(projectDir, config.FryDir, "epic.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(epicPath), 0o755))
	require.NoError(t, os.WriteFile(epicPath, []byte(epicContent), 0o644))

	planPath := filepath.Join(projectDir, config.PlanFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(planPath), 0o755))
	require.NoError(t, os.WriteFile(planPath, []byte("Plan content\n"), 0o644))

	// First attempt: modify completed sprint 1 (invalid).
	// Second attempt: only modify sprint 2 (valid).
	badOutput := strings.Replace(epicContent, "First prompt.", "First prompt CHANGED.", 1)
	badOutput = strings.Replace(badOutput, "Second prompt.", "Second prompt updated.", 1)
	goodOutput := strings.Replace(epicContent, "Second prompt.", "Second prompt updated.", 1)

	eng := &stubReplanEngineMulti{outputs: []string{badOutput, goodOutput}}

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir: projectDir,
		EpicPath:   epicPath,
		DeviationSpec: &DeviationSpec{
			Trigger:         "Test trigger",
			AffectedSprints: []int{2},
			RiskAssessment:  "Low",
			RawText:         "Test deviation",
		},
		CompletedSprint: 1,
		MaxScope:        2,
		Engine:          eng,
	})
	require.NoError(t, err)

	// Verify the final epic has the good output.
	updated, err := os.ReadFile(epicPath)
	require.NoError(t, err)
	assert.Contains(t, string(updated), "Second prompt updated.")
	assert.Contains(t, string(updated), "First prompt.")
	assert.NotContains(t, string(updated), "First prompt CHANGED.")

	// Verify engine was called twice and retry prompt includes error feedback.
	require.Len(t, eng.prompts, 2)
	assert.Contains(t, eng.prompts[1], "CRITICAL")
	assert.Contains(t, eng.prompts[1], "REJECTED")
}

func TestRunReplan_ExhaustedRetriesRestoresEpic(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	epicContent := "@epic Demo\n@review_between_sprints\n@review_engine claude\n" +
		"@sprint 1\n@name One\n@max_iterations 3\n@promise ONE\n@prompt\nFirst prompt.\n" +
		"@sprint 2\n@name Two\n@max_iterations 3\n@promise TWO\n@prompt\nSecond prompt.\n"

	epicPath := filepath.Join(projectDir, config.FryDir, "epic.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(epicPath), 0o755))
	require.NoError(t, os.WriteFile(epicPath, []byte(epicContent), 0o644))

	planPath := filepath.Join(projectDir, config.PlanFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(planPath), 0o755))
	require.NoError(t, os.WriteFile(planPath, []byte("Plan content\n"), 0o644))

	// All attempts modify the completed sprint — always invalid.
	badOutput := strings.Replace(epicContent, "First prompt.", "First prompt CHANGED.", 1)
	eng := &stubReplanEngineMulti{outputs: []string{badOutput, badOutput, badOutput}}

	err := RunReplan(context.Background(), ReplanOpts{
		ProjectDir: projectDir,
		EpicPath:   epicPath,
		DeviationSpec: &DeviationSpec{
			Trigger:         "Test trigger",
			AffectedSprints: []int{2},
			RiskAssessment:  "Low",
			RawText:         "Test deviation",
		},
		CompletedSprint: 1,
		MaxScope:        2,
		Engine:          eng,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed after 3 attempts")

	// Verify the epic was restored to its original content.
	restored, err := os.ReadFile(epicPath)
	require.NoError(t, err)
	assert.Equal(t, epicContent, string(restored))

	// Verify engine was called 3 times (initial + 2 retries).
	assert.Len(t, eng.prompts, 3)
}
