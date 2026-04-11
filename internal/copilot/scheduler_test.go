package copilot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildTickArgs_Claude(t *testing.T) {
	t.Parallel()

	args := BuildTickArgs("claude", "test-session-uuid", "opus[1m]")
	assert.Equal(t, "claude", args[0])
	assert.Contains(t, args, "--dangerously-skip-permissions")
	assert.Contains(t, args, "--resume")
	assert.Contains(t, args, "test-session-uuid")
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "opus[1m]")
	assert.Equal(t, "-p", args[len(args)-1],
		"-p must be the LAST flag so the wake message can be appended as the prompt")
}

func TestBuildTickArgs_ClaudeWithoutSession(t *testing.T) {
	t.Parallel()

	args := BuildTickArgs("claude", "", "")
	assert.Equal(t, "claude", args[0])
	assert.NotContains(t, args, "--resume", "no --resume flag when session ID is empty")
	assert.NotContains(t, args, "--model", "no --model flag when model is empty")
	assert.Equal(t, "-p", args[len(args)-1])
}

func TestBuildTickArgs_Codex(t *testing.T) {
	t.Parallel()

	args := BuildTickArgs("codex", "test-session-uuid", "gpt-5.4")
	assert.Equal(t, "codex", args[0])
	assert.Equal(t, "exec", args[1])
	assert.Contains(t, args, "resume")
	assert.Contains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "gpt-5.4")
	// Codex takes the session ID as the LAST positional argument.
	assert.Equal(t, "test-session-uuid", args[len(args)-1])
}

func TestBuildTickArgs_CodexWithoutSession(t *testing.T) {
	t.Parallel()

	args := BuildTickArgs("codex", "", "")
	assert.Equal(t, "codex", args[0])
	assert.Equal(t, "exec", args[1])
	assert.NotContains(t, args, "resume")
}

func TestBuildTickArgs_UnknownEngineFallback(t *testing.T) {
	t.Parallel()

	args := BuildTickArgs("ollama", "test-session", "llama3")
	assert.Equal(t, []string{"ollama"}, args)
}
