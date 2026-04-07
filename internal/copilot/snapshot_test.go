package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/agent"
	"github.com/yevgetman/fry/internal/config"
)

// writeManifestForTest creates a minimal valid manifest in projectDir so the
// snapshot writer doesn't bail out early.
func writeManifestForTest(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, WriteManifest(dir, &Manifest{
		SessionID: "test-session",
		BuildPID:  os.Getpid(),
		BuildDir:  dir,
		Engine:    "claude",
		Mode:      ModeActive,
	}))
}

func TestWriteStateSnapshotNoManifestSkips(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// No manifest → should skip without error
	require.NoError(t, WriteStateSnapshot(dir))

	_, err := os.Stat(filepath.Join(dir, config.CopilotStateSnapshotFile))
	assert.True(t, os.IsNotExist(err), "no snapshot should be written when no manifest exists")
}

func TestForceWriteStateSnapshotWritesAtomically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	require.NoError(t, ForceWriteStateSnapshot(dir))

	snap, err := ReadStateSnapshot(dir)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.NotEmpty(t, snap.Timestamp)
	assert.Equal(t, os.Getpid(), snap.BuildPID)
	assert.True(t, snap.BuildPIDAlive)
}

func TestForceWriteStateSnapshotPopulatesBuildStatusFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// Plant a canonical build-status.json so buildSnapshot has something to read.
	startedAt := time.Date(2026, 4, 7, 0, 0, 0, 0, time.UTC)
	bs := &agent.BuildStatus{
		Version:   1,
		UpdatedAt: time.Now(),
		Build: agent.BuildInfo{
			Epic:          "Test Epic",
			Effort:        "max",
			Engine:        "claude",
			Mode:          "build",
			TotalSprints:  7,
			CurrentSprint: 5,
			Status:        "running",
			Phase:         "sprint",
			StartedAt:     startedAt,
		},
		Sprints: []agent.SprintStatus{
			{Number: 5, Name: "Billing Integration", Status: "running"},
		},
	}
	require.NoError(t, agent.WriteBuildStatus(dir, bs))

	require.NoError(t, ForceWriteStateSnapshot(dir))

	snap, err := ReadStateSnapshot(dir)
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.Equal(t, "sprint", snap.BuildPhase)
	assert.Equal(t, "build", snap.BuildMode)
	assert.Equal(t, "running", snap.BuildStatus)
	assert.Equal(t, 5, snap.CurrentSprint)
	assert.Equal(t, "Billing Integration", snap.CurrentSprintName)
	assert.Equal(t, 7, snap.TotalSprints)
}

func TestStateSnapshotDebounce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	// Reset debouncer state for this directory
	globalDebouncer.mu.Lock()
	delete(globalDebouncer.lastWrite, dir)
	globalDebouncer.mu.Unlock()

	// First write must succeed.
	require.NoError(t, WriteStateSnapshot(dir))

	// Capture the file mtime.
	path := filepath.Join(dir, config.CopilotStateSnapshotFile)
	stat1, err := os.Stat(path)
	require.NoError(t, err)
	mtime1 := stat1.ModTime()

	// Second immediate write should be debounced (no file change).
	time.Sleep(50 * time.Millisecond) // small wait so a real write would change mtime
	require.NoError(t, WriteStateSnapshot(dir))
	stat2, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, mtime1, stat2.ModTime(), "second write within debounce window should not change file")
}

func TestForceWriteIgnoresDebounce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	require.NoError(t, ForceWriteStateSnapshot(dir))
	stat1, err := os.Stat(filepath.Join(dir, config.CopilotStateSnapshotFile))
	require.NoError(t, err)
	mtime1 := stat1.ModTime()

	// Need at least 1s gap on file systems with second-resolution mtime
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, ForceWriteStateSnapshot(dir))
	stat2, err := os.Stat(filepath.Join(dir, config.CopilotStateSnapshotFile))
	require.NoError(t, err)
	assert.True(t, stat2.ModTime().After(mtime1), "Force should bypass debounce")
}

func TestReadStateSnapshotMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, err := ReadStateSnapshot(dir)
	assert.NoError(t, err)
	assert.Nil(t, got)
}

func TestStateSnapshotIsValidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestForTest(t, dir)

	require.NoError(t, ForceWriteStateSnapshot(dir))

	data, err := os.ReadFile(filepath.Join(dir, config.CopilotStateSnapshotFile))
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Contains(t, raw, "ts")
	assert.Contains(t, raw, "build_pid")
}
