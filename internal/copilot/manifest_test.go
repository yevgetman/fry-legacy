package copilot

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestWriteAndReadManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	original := &Manifest{
		SessionID:                 "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c",
		CronID:                    "cron_01H9X",
		BuildPID:                  64128,
		BuildDir:                  "/tmp/test-build",
		FrySourceDir:              "/tmp/test-fry",
		Engine:                    "claude",
		Model:                     "opus[1m]",
		StartedAt:                 "2026-04-07T00:00:00Z",
		Interval:                  "10m",
		EpicName:                  "Test Epic",
		EffortLevel:               "max",
		MaxInterventionsPerClass:  3,
		StopOnBuildComplete:       true,
		Mode:                      ModeActive,
		EngineCapabilities:        EngineCapabilities{SessionIDFlag: true, CronCreate: true},
		SessionIDCaptureMechanism: SessionIDPreSpecified,
	}

	require.NoError(t, WriteManifest(dir, original))

	// File exists in expected location
	manifestPath := filepath.Join(dir, config.CopilotManifestFile)
	_, err := filepathStat(manifestPath)
	require.NoError(t, err, "manifest file should exist after WriteManifest")

	// Round-trip
	got, err := ReadManifest(dir)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, ManifestVersion, got.Version, "version should be set automatically")
	assert.Equal(t, original.SessionID, got.SessionID)
	assert.Equal(t, original.CronID, got.CronID)
	assert.Equal(t, original.BuildPID, got.BuildPID)
	assert.Equal(t, original.BuildDir, got.BuildDir)
	assert.Equal(t, original.FrySourceDir, got.FrySourceDir)
	assert.Equal(t, original.Engine, got.Engine)
	assert.Equal(t, original.Model, got.Model)
	assert.Equal(t, original.StartedAt, got.StartedAt)
	assert.Equal(t, original.Interval, got.Interval)
	assert.Equal(t, original.EpicName, got.EpicName)
	assert.Equal(t, original.EffortLevel, got.EffortLevel)
	assert.Equal(t, original.MaxInterventionsPerClass, got.MaxInterventionsPerClass)
	assert.Equal(t, original.StopOnBuildComplete, got.StopOnBuildComplete)
	assert.Equal(t, original.Mode, got.Mode)
	assert.Equal(t, original.EngineCapabilities, got.EngineCapabilities)
	assert.Equal(t, original.SessionIDCaptureMechanism, got.SessionIDCaptureMechanism)
}

func TestReadManifestMissingReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	got, err := ReadManifest(dir)
	assert.NoError(t, err)
	assert.Nil(t, got, "missing manifest should return (nil, nil)")
}

func TestWriteManifestSetsVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	m := &Manifest{
		SessionID: "test",
		Mode:      ModeActive,
	}
	require.NoError(t, WriteManifest(dir, m))
	assert.Equal(t, ManifestVersion, m.Version, "WriteManifest should populate Version")
}

func TestWriteManifestNilReturnsError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	err := WriteManifest(dir, nil)
	assert.Error(t, err)
}

func TestReadManifestVersionMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Manually write a manifest with wrong version
	manifestPath := filepath.Join(dir, config.CopilotManifestFile)
	require.NoError(t, mkdirAllForFile(manifestPath))
	require.NoError(t, writeFile(manifestPath, []byte(`{"version":99,"session_id":"x"}`)))

	_, err := ReadManifest(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported version")
}

func TestSessionIDFileRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const id = "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c"
	require.NoError(t, WriteSessionIDFile(dir, id))
	assert.Equal(t, id, ReadSessionIDFile(dir))
}

func TestSessionIDFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	assert.Equal(t, "", ReadSessionIDFile(dir))
}

func TestCronIDFileRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const id = "cron_01H9XYZ"
	require.NoError(t, WriteCronIDFile(dir, id))
	assert.Equal(t, id, ReadCronIDFile(dir))
}

func TestCronIDFileMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	assert.Equal(t, "", ReadCronIDFile(dir))
}
