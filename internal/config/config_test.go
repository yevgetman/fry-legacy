package config_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yevgetman/fry/internal/config"
)

func TestFryDirPathConsistency(t *testing.T) {
	t.Parallel()

	// All .fry/ paths should be rooted under FryDir
	fryPaths := map[string]string{
		"BuildLogsDir":           config.BuildLogsDir,
		"DefaultVerificationFile": config.DefaultVerificationFile,
		"PromptFile":             config.PromptFile,
		"SprintProgressFile":     config.SprintProgressFile,
		"EpicProgressFile":       config.EpicProgressFile,
		"ReviewPromptFile":       config.ReviewPromptFile,
		"DeviationLogFile":       config.DeviationLogFile,
		"LockFile":               config.LockFile,
		"UserPromptFile":         config.UserPromptFile,
		"AgentsFile":             config.AgentsFile,
	}

	for name, path := range fryPaths {
		assert.True(t, strings.HasPrefix(path, config.FryDir+"/"),
			"%s (%q) should start with %q", name, path, config.FryDir+"/")
	}
}

func TestFryConfigDirPathConsistency(t *testing.T) {
	t.Parallel()

	// Persistent paths should be rooted under FryConfigDir
	configPaths := map[string]string{
		"ProjectConfigFile":  config.ProjectConfigFile,
		"CodebaseFile":       config.CodebaseFile,
		"FileIndexFile":      config.FileIndexFile,
		"CodebaseMemoriesDir": config.CodebaseMemoriesDir,
	}

	for name, path := range configPaths {
		assert.True(t, strings.HasPrefix(path, config.FryConfigDir+"/"),
			"%s (%q) should start with %q", name, path, config.FryConfigDir+"/")
	}
}

func TestPlansDirPathConsistency(t *testing.T) {
	t.Parallel()

	plansPaths := map[string]string{
		"PlanFile":      config.PlanFile,
		"ExecutiveFile": config.ExecutiveFile,
	}

	for name, path := range plansPaths {
		assert.True(t, strings.HasPrefix(path, config.PlansDir+"/"),
			"%s (%q) should start with %q", name, path, config.PlansDir+"/")
	}
}

func TestInvocationPromptsNonEmpty(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, config.AgentInvocationPrompt)
	assert.NotEmpty(t, config.HealInvocationPrompt)
}

func TestAgentInvocationPromptIncludesBuildTestVerification(t *testing.T) {
	t.Parallel()

	// The agent invocation prompt must instruct the sprint agent to verify
	// its work by running build/test commands before declaring completion.
	// This is the baseline self-verification that applies regardless of
	// effort level (the tiered quality directive in prompt.go layers
	// additional rigor on top of this).
	assert.Contains(t, config.AgentInvocationPrompt, "verify your work",
		"AgentInvocationPrompt must instruct the agent to verify its work")
	assert.Contains(t, config.AgentInvocationPrompt, "build and test commands",
		"AgentInvocationPrompt must instruct the agent to run build/test commands")
}
