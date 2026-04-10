package config_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/templates"
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

func TestInvocationFilePathsNonEmpty(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, config.AgentInvocationFile)
	assert.NotEmpty(t, config.HealInvocationFile)
}

func TestInvocationPromptsLoadable(t *testing.T) {
	t.Parallel()

	paths := []string{
		config.AgentInvocationFile,
		config.HealInvocationFile,
		config.AuditInvocationFile,
		config.AuditVerifyInvocationFile,
		config.AuditFixInvocationFile,
		config.BuildAuditInvocationFile,
		config.ContinueInvocationFile,
		config.TriageInvocationFile,
		config.ObserverInvocationFile,
	}
	for _, p := range paths {
		text, err := templates.LoadText(p)
		require.NoError(t, err, "failed to load %s", p)
		assert.NotEmpty(t, text, "%s loaded empty content", p)
	}
}

func TestAgentInvocationPromptIncludesBuildTestVerification(t *testing.T) {
	t.Parallel()

	prompt, err := templates.LoadText(config.AgentInvocationFile)
	require.NoError(t, err)
	assert.Contains(t, prompt, "verify your work",
		"agent invocation prompt must instruct the agent to verify its work")
	assert.Contains(t, prompt, "build and test commands",
		"agent invocation prompt must instruct the agent to run build/test commands")
}
