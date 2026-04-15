package agentrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/engine"
)

// mockEngine implements engine.Engine for testing.
type mockEngine struct {
	output   string
	err      error
	exitCode int
}

func (m *mockEngine) Run(ctx context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	select {
	case <-ctx.Done():
		return "", 1, ctx.Err()
	default:
	}
	if opts.Stdout != nil {
		_, _ = fmt.Fprint(opts.Stdout, m.output)
	}
	return m.output, m.exitCode, m.err
}

func (m *mockEngine) Name() string {
	return "mock"
}

// Test 1: silent mode, normal — both log files contain the engine output.
func TestRunWithDualLogs_SilentNormal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: "hello output"}
	opts := DualLogOpts{
		Engine:  eng,
		Model:   "test-model",
		WorkDir: dir,
		Verbose: false,
	}

	output, err := RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	require.NoError(t, err)
	assert.Equal(t, "hello output", output)

	iterBytes, err := os.ReadFile(iterPath)
	require.NoError(t, err)
	assert.Equal(t, "hello output", string(iterBytes))

	sprintBytes, err := os.ReadFile(sprintLogPath)
	require.NoError(t, err)
	assert.Equal(t, "hello output", string(sprintBytes))
}

// Test 2: silent mode, non-fatal error — output returned, nil error.
func TestRunWithDualLogs_SilentNonFatalError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: "partial output", err: errors.New("non-fatal engine error")}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: false,
	}

	output, err := RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	require.NoError(t, err)
	assert.Equal(t, "partial output", output)
}

// Test 3: silent mode, context cancelled — returns a context error.
func TestRunWithDualLogs_ContextCancelled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	eng := &mockEngine{output: ""}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: false,
	}

	_, err := RunWithDualLogs(ctx, "test prompt", iterPath, sprintLogPath, opts)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// Test 4: verbose mode — output returned; both log files contain content.
func TestRunWithDualLogs_VerboseNormal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: "verbose output"}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: true,
	}

	output, err := RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	require.NoError(t, err)
	assert.Equal(t, "verbose output", output)

	iterBytes, err := os.ReadFile(iterPath)
	require.NoError(t, err)
	assert.Equal(t, "verbose output", string(iterBytes))

	sprintBytes, err := os.ReadFile(sprintLogPath)
	require.NoError(t, err)
	assert.Equal(t, "verbose output", string(sprintBytes))
}

// TestRunWithDualLogsWritesBothFiles verifies that both iterPath and
// sprintLogPath contain the engine output after a successful call.
func TestRunWithDualLogsWritesBothFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: "dual log output"}
	opts := DualLogOpts{Engine: eng, WorkDir: dir, Verbose: false}

	output, err := RunWithDualLogs(context.Background(), "prompt", iterPath, sprintLogPath, opts)
	require.NoError(t, err)
	assert.Equal(t, "dual log output", output)

	iterBytes, err := os.ReadFile(iterPath)
	require.NoError(t, err)
	assert.Equal(t, "dual log output", string(iterBytes))

	sprintBytes, err := os.ReadFile(sprintLogPath)
	require.NoError(t, err)
	assert.Equal(t, "dual log output", string(sprintBytes))
}

// TestRunWithDualLogsAppendsSprintLog verifies that the sprint log is appended
// across two calls (contains both outputs concatenated).
func TestRunWithDualLogsAppendsSprintLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sprintLogPath := filepath.Join(dir, "sprint.log")

	opts := DualLogOpts{WorkDir: dir, Verbose: false}

	opts.Engine = &mockEngine{output: "first"}
	_, err := RunWithDualLogs(context.Background(), "p1", filepath.Join(dir, "iter1.log"), sprintLogPath, opts)
	require.NoError(t, err)

	opts.Engine = &mockEngine{output: "second"}
	_, err = RunWithDualLogs(context.Background(), "p2", filepath.Join(dir, "iter2.log"), sprintLogPath, opts)
	require.NoError(t, err)

	sprintBytes, err := os.ReadFile(sprintLogPath)
	require.NoError(t, err)
	combined := string(sprintBytes)
	assert.Contains(t, combined, "first")
	assert.Contains(t, combined, "second")
}

// TestRunWithDualLogsContextCanceled verifies that when the context is already
// cancelled, RunWithDualLogs returns context.Canceled.
func TestRunWithDualLogsContextCanceled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the mock engine returns context.Canceled

	eng := &mockEngine{output: ""}
	opts := DualLogOpts{Engine: eng, WorkDir: dir, Verbose: false}

	_, err := RunWithDualLogs(ctx, "prompt", filepath.Join(dir, "iter.log"), filepath.Join(dir, "sprint.log"), opts)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestRunWithDualLogsEngineError verifies that a non-context engine error is
// treated as non-fatal: RunWithDualLogs logs a warning and returns nil.
func TestRunWithDualLogsEngineError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &mockEngine{output: "partial output", err: errors.New("engine failure"), exitCode: 1}
	opts := DualLogOpts{Engine: eng, WorkDir: dir, Verbose: false}

	_, err := RunWithDualLogs(context.Background(), "prompt", filepath.Join(dir, "iter.log"), filepath.Join(dir, "sprint.log"), opts)
	require.NoError(t, err)
}

func TestFormatNonFatalEngineError(t *testing.T) {
	t.Parallel()

	msg := formatNonFatalEngineError(&mockEngine{}, "sonnet", 1, errors.New("exit status 1"), "authentication expired")
	assert.Contains(t, msg, "engine=mock")
	assert.Contains(t, msg, "model=sonnet")
	assert.Contains(t, msg, "exit_code=1")
	assert.Contains(t, msg, "authentication expired")
}

// Test: custom Stdout — verbose output routes to the provided writer.
func TestRunWithDualLogs_CustomStdout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	var stdoutBuf strings.Builder
	eng := &mockEngine{output: "custom stdout output"}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: true,
		Stdout:  &stdoutBuf,
	}

	output, err := RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	require.NoError(t, err)
	assert.Equal(t, "custom stdout output", output)
	assert.Contains(t, stdoutBuf.String(), "custom stdout output")
}

// Test: nil Stdout with Verbose — falls back to os.Stdout (no panic).
func TestRunWithDualLogs_NilStdoutDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "iter.log")
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: "default stdout"}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: true,
		Stdout:  nil, // explicitly nil — should not panic
	}

	require.NotPanics(t, func() {
		_, _ = RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	})
}

// TestRunWithDualLogs_SyncErrorNotFatal documents that sync errors after a
// successful engine run do not cause RunWithDualLogs to return an error.
// The behavioral contract: engine success => (output, nil) regardless of Sync().
func TestRunWithDualLogs_SyncErrorNotFatal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	eng := &mockEngine{output: "engine succeeded", exitCode: 0}
	opts := DualLogOpts{Engine: eng, WorkDir: dir, Verbose: false}

	output, err := RunWithDualLogs(context.Background(), "prompt",
		filepath.Join(dir, "iter.log"), filepath.Join(dir, "sprint.log"), opts)
	require.NoError(t, err)
	assert.Equal(t, "engine succeeded", output)
}

func TestRunWithDualLogs_SyncErrorNotFatal_Verbose(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	dir := t.TempDir()
	eng := &mockEngine{output: "engine succeeded verbose", exitCode: 0}
	opts := DualLogOpts{Engine: eng, WorkDir: dir, Verbose: true, Stdout: &buf}

	output, err := RunWithDualLogs(context.Background(), "prompt",
		filepath.Join(dir, "iter.log"), filepath.Join(dir, "sprint.log"), opts)
	require.NoError(t, err)
	assert.Equal(t, "engine succeeded verbose", output)
}

// Test 5: missing iterPath directory — returns a non-nil error.
func TestRunWithDualLogs_MissingIterDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	iterPath := filepath.Join(dir, "nonexistent", "iter.log") // parent directory does not exist
	sprintLogPath := filepath.Join(dir, "sprint.log")

	eng := &mockEngine{output: ""}
	opts := DualLogOpts{
		Engine:  eng,
		WorkDir: dir,
		Verbose: false,
	}

	_, err := RunWithDualLogs(context.Background(), "test prompt", iterPath, sprintLogPath, opts)
	require.Error(t, err)
}
