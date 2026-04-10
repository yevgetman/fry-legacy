package consciousness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func writeSettings(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, config.SettingsFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestLoadSettings_NoFile(t *testing.T) {
	t.Parallel()

	s := loadSettingsFromDir(t.TempDir())
	assert.Nil(t, s.Telemetry)
}

func TestLoadSettings_ValidFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `{"telemetry": true}`)

	s := loadSettingsFromDir(dir)
	require.NotNil(t, s.Telemetry)
	assert.True(t, *s.Telemetry)
}

func TestLoadSettings_TelemetryFalse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `{"telemetry": false}`)

	s := loadSettingsFromDir(dir)
	require.NotNil(t, s.Telemetry)
	assert.False(t, *s.Telemetry)
}

func TestLoadSettings_EmptyJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `{}`)

	s := loadSettingsFromDir(dir)
	assert.Nil(t, s.Telemetry)
}

func TestLoadSettings_MalformedJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `not json at all`)

	s := loadSettingsFromDir(dir)
	assert.Nil(t, s.Telemetry) // zero value, no panic
}

func TestLoadSettings_ExtraFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `{"telemetry": true, "other_key": 42}`)

	s := loadSettingsFromDir(dir)
	require.NotNil(t, s.Telemetry)
	assert.True(t, *s.Telemetry)
}

// Tests that use t.Setenv cannot use t.Parallel (Go restriction).

func TestTelemetryEnabled_CLIOverrides(t *testing.T) {
	t.Setenv("FRY_TELEMETRY", "0")

	result := TelemetryEnabled(boolPtr(true), Settings{Telemetry: boolPtr(false)})
	assert.True(t, result)
}

func TestTelemetryEnabled_CLIFalseWins(t *testing.T) {
	t.Setenv("FRY_TELEMETRY", "1")

	result := TelemetryEnabled(boolPtr(false), Settings{Telemetry: boolPtr(true)})
	assert.False(t, result)
}

func TestTelemetryEnabled_EnvVar1(t *testing.T) {
	t.Setenv("FRY_TELEMETRY", "1")

	result := TelemetryEnabled(nil, Settings{})
	assert.True(t, result)
}

func TestTelemetryEnabled_EnvVar0(t *testing.T) {
	t.Setenv("FRY_TELEMETRY", "0")

	result := TelemetryEnabled(nil, Settings{Telemetry: boolPtr(true)})
	assert.False(t, result)
}

func TestTelemetryEnabled_EnvVarGarbage(t *testing.T) {
	t.Setenv("FRY_TELEMETRY", "maybe")

	// Unrecognized env value → falls through to settings
	result := TelemetryEnabled(nil, Settings{Telemetry: boolPtr(true)})
	assert.True(t, result)
}

func TestTelemetryEnabled_SettingsFile(t *testing.T) {
	t.Parallel()

	result := TelemetryEnabled(nil, Settings{Telemetry: boolPtr(true)})
	assert.True(t, result)
}

func TestTelemetryEnabled_DefaultOff(t *testing.T) {
	t.Parallel()

	result := TelemetryEnabled(nil, Settings{})
	assert.False(t, result)
}

func TestEnsureSettings_CreatesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, config.SettingsFile)

	err := ensureSettingsInDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"telemetry": false`)
}

func TestEnsureSettings_DoesNotOverwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSettings(t, dir, `{"telemetry": false}`)

	err := ensureSettingsInDir(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, config.SettingsFile))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"telemetry": false`, "existing settings should not be overwritten")
}
