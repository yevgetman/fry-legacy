package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestCleanupOnExitNoManifestNoOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Should not panic, should not create files
	CleanupOnExit(dir)
	_, err := os.Stat(filepath.Join(dir, config.CopilotDir))
	assert.True(t, os.IsNotExist(err), "no copilot dir should exist after cleanup with no manifest")
}

func TestCleanupOnExitClearsStaleBootstrapPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// Plant a stale bootstrap PID file
	require.NoError(t, writeBootstrapPID(dir, 999999))

	CleanupOnExit(dir)

	// Stale PID file should be removed
	_, err := os.Stat(filepath.Join(dir, config.CopilotBootstrapPIDFile))
	assert.True(t, os.IsNotExist(err))
}

func TestCleanupOnExitKeepsLiveBootstrapPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// Use our own PID — guaranteed to be alive.
	require.NoError(t, writeBootstrapPID(dir, os.Getpid()))

	CleanupOnExit(dir)

	// Live PID file should be preserved
	pid := ReadBootstrapPID(dir)
	assert.Equal(t, os.Getpid(), pid)
}

func TestArchiveCopilotDirIfDoneNothingToArchive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// No manifest → no archive
	archived, err := ArchiveCopilotDirIfDone(dir, "test-run")
	require.NoError(t, err)
	assert.Equal(t, "", archived)
}

func TestArchiveCopilotDirIfDoneNotCleanlyExited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// No final-summary.md → not cleanly exited → no archive
	archived, err := ArchiveCopilotDirIfDone(dir, "test-run")
	require.NoError(t, err)
	assert.Equal(t, "", archived)
}

func TestArchiveCopilotDirIfDoneSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// Plant a final-summary.md (cleanly exited markers)
	summaryPath := filepath.Join(dir, config.CopilotFinalSummaryFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(summaryPath), 0o755))
	require.NoError(t, os.WriteFile(summaryPath, []byte("# Final Summary\n"), 0o644))

	// Plant some additional files to verify they get moved
	require.NoError(t, AppendEventsText(dir, "test event"))

	archived, err := ArchiveCopilotDirIfDone(dir, "test-run-001")
	require.NoError(t, err)
	require.NotEqual(t, "", archived)

	// Manifest should be inside the archive directory
	assert.FileExists(t, filepath.Join(archived, "manifest.json"))
	assert.FileExists(t, filepath.Join(archived, "final-summary.md"))
	assert.FileExists(t, filepath.Join(archived, "events.txt"))
}

func TestCopilotConfigured(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	assert.False(t, CopilotConfigured(dir))

	writeManifestForTest(t, dir)
	assert.True(t, CopilotConfigured(dir))
}

func TestCleanlyExitedNoSummary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)
	assert.False(t, cleanlyExited(dir))
}

func TestCleanlyExitedWithCronStillSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	summaryPath := filepath.Join(dir, config.CopilotFinalSummaryFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(summaryPath), 0o755))
	require.NoError(t, os.WriteFile(summaryPath, []byte("done"), 0o644))
	require.NoError(t, WriteCronIDFile(dir, "still-active-cron"))

	assert.False(t, cleanlyExited(dir))
}
