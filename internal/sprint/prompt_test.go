package sprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
)

func TestAssemblePrompt_MaxEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build the thing.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortMax,
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "MAX effort")
	assert.Contains(t, prompt, "heightened rigor")
	assert.Contains(t, prompt, "run the project's build and test commands")
	assert.Contains(t, prompt, "Review your own diff")
}

func TestAssemblePrompt_HighEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build the thing.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortHigh,
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "HIGH effort")
	assert.Contains(t, prompt, "Handle error cases")
	assert.Contains(t, prompt, "run the project's build and test commands")
	assert.NotContains(t, prompt, "ALL edge cases")
}

func TestAssemblePrompt_StandardEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build the thing.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortStandard,
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "Check your work")
	assert.Contains(t, prompt, "run the project's build and test commands")
	assert.NotContains(t, prompt, "heightened rigor")
	assert.NotContains(t, prompt, "HIGH effort")
}

func TestAssemblePrompt_FastEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build the thing.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortFast,
	})
	require.NoError(t, err)
	assert.NotContains(t, prompt, "QUALITY DIRECTIVE")
}

func TestAssemblePrompt_NoEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build the thing.",
		Promise:      "DONE",
		EffortLevel:  "",
	})
	require.NoError(t, err)
	assert.NotContains(t, prompt, "QUALITY DIRECTIVE")
}

func TestAssemblePrompt_WritingMode_MaxEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Write the introduction.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortMax,
		Mode:         "writing",
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "editorial rigor")
	assert.Contains(t, prompt, "audience engagement")
	assert.Contains(t, prompt, "re-read your output")
	assert.NotContains(t, prompt, "defensive code")
	assert.NotContains(t, prompt, "build and test commands")
}

func TestAssemblePrompt_WritingMode_HighEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Write chapter two.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortHigh,
		Mode:         "writing",
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "clarity and coherence")
	assert.Contains(t, prompt, "re-read your output")
	assert.NotContains(t, prompt, "build and test commands")
}

func TestAssemblePrompt_WritingMode_StandardEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Write the appendix.",
		Promise:      "DONE",
		EffortLevel:  epic.EffortStandard,
		Mode:         "writing",
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "QUALITY DIRECTIVE")
	assert.Contains(t, prompt, "Check your work")
	assert.Contains(t, prompt, "re-read your output")
	assert.NotContains(t, prompt, "build and test commands")
}

func TestAssemblePrompt_WritingMode_PlanReference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Write chapter one.",
		Promise:      "DONE",
		Mode:         "writing",
	})
	require.NoError(t, err)
	assert.Contains(t, prompt, "content plan")
	assert.Contains(t, prompt, "chapters/sections")
	assert.NotContains(t, prompt, "project architecture")
}

func TestAssemblePrompt_CodebaseContext_Present(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Write a codebase.md file in .fry-config/.
	codebasePath := filepath.Join(dir, config.CodebaseFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(codebasePath), 0o755))
	require.NoError(t, os.WriteFile(codebasePath,
		[]byte("# Codebase: Test\n\n## Summary\nA test project with Go and React.\n"), 0o644))

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Add a new feature.",
	})
	require.NoError(t, err)

	// Layer 0.5 should appear.
	assert.Contains(t, prompt, "CODEBASE CONTEXT")
	assert.Contains(t, prompt, "A test project with Go and React")

	// Layer 0.5 should appear BEFORE Layer 1 (PROJECT CONTEXT) and Layer 2 (STRATEGIC PLAN).
	codebaseIdx := strings.Index(prompt, "CODEBASE CONTEXT")
	strategicIdx := strings.Index(prompt, "STRATEGIC PLAN")
	assert.True(t, codebaseIdx < strategicIdx,
		"Layer 0.5 (CODEBASE CONTEXT) should appear before Layer 2 (STRATEGIC PLAN)")
}

func TestAssemblePrompt_CodebaseContext_Sprint2_Pointer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	codebasePath := filepath.Join(dir, config.CodebaseFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(codebasePath), 0o755))
	require.NoError(t, os.WriteFile(codebasePath,
		[]byte("# Codebase: Test\n\n## Summary\nA test project.\n"), 0o644))

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintNumber: 2,
		EffortLevel:  epic.EffortStandard,
		SprintPrompt: "Continue the feature.",
	})
	require.NoError(t, err)

	// Sprint 2 at standard effort should get a lightweight pointer
	assert.Contains(t, prompt, "CODEBASE CONTEXT (reference)")
	assert.NotContains(t, prompt, "A test project")
	assert.NotContains(t, prompt, "CODEBASE MEMORIES")
}

func TestAssemblePrompt_CodebaseContext_Sprint2_FullAtMaxEffort(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	codebasePath := filepath.Join(dir, config.CodebaseFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(codebasePath), 0o755))
	require.NoError(t, os.WriteFile(codebasePath,
		[]byte("# Codebase: Test\n\n## Summary\nA max effort project.\n"), 0o644))

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintNumber: 3,
		EffortLevel:  epic.EffortMax,
		SprintPrompt: "Handle all edge cases.",
	})
	require.NoError(t, err)

	// Sprint 3 at max effort should get full codebase context
	assert.Contains(t, prompt, "CODEBASE CONTEXT =====")
	assert.NotContains(t, prompt, "(reference)")
	assert.Contains(t, prompt, "A max effort project")
}

func TestAssemblePrompt_CodebaseContext_Absent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prompt, err := AssemblePrompt(PromptOpts{
		ProjectDir:   dir,
		SprintPrompt: "Build from scratch.",
	})
	require.NoError(t, err)

	// No codebase.md → no Layer 0.5.
	assert.NotContains(t, prompt, "CODEBASE CONTEXT")
}
