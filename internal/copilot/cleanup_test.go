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

func TestLeftoverCronWarningEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.Empty(t, LeftoverCronWarning(dir))
}

func TestLeftoverCronWarningNoManifestNoCron(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create the dir but no cron.id and no manifest.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.CopilotDir), 0o755))
	assert.Empty(t, LeftoverCronWarning(dir))
}

func TestLeftoverCronWarningActiveManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// With an active manifest, the cron.id is legitimate, not a leftover.
	writeManifestForTest(t, dir)
	require.NoError(t, WriteCronIDFile(dir, "active-cron-id"))
	assert.Empty(t, LeftoverCronWarning(dir),
		"an active manifest means cron.id is legitimate, not a leftover")
}

func TestLeftoverCronWarningOrphanCronDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Cron.id present but no manifest -> leftover from a wiped previous build.
	require.NoError(t, WriteCronIDFile(dir, "orphan-cron-id-99"))
	warning := LeftoverCronWarning(dir)
	require.NotEmpty(t, warning)
	assert.Contains(t, warning, "orphan-cron-id-99")
	assert.Contains(t, warning, "self-prune")
}

func TestReadManifestHydratesCronIDFromDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a manifest with empty CronID (this is what fry main always
	// produces, because the agent installs the cron after the manifest is
	// written).
	require.NoError(t, WriteManifest(dir, &Manifest{
		SessionID: "test-session",
		BuildPID:  os.Getpid(),
		Engine:    "claude",
		Mode:      ModeActive,
	}))

	// Then write the cron.id file (this is what the agent does after
	// CronCreate succeeds).
	require.NoError(t, WriteCronIDFile(dir, "cron_test_xyz"))

	// ReadManifest should hydrate the CronID from the cron.id file.
	got, err := ReadManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "cron_test_xyz", got.CronID,
		"ReadManifest should hydrate CronID from .fry/copilot/cron.id when present")
}

func TestReadManifestPreservesEmptyCronIDWhenFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Manifest only — no cron.id file.
	require.NoError(t, WriteManifest(dir, &Manifest{
		SessionID: "test-session",
		Mode:      ModeActive,
	}))
	got, err := ReadManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "", got.CronID,
		"missing cron.id file should leave CronID as the on-disk value (empty)")
}
