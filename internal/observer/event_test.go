package observer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestEmitEvent_DisabledIsNoop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	SetEnabled(false)
	err := EmitEvent(dir, Event{Timestamp: "2026-01-01T00:00:00Z", Type: EventBuildStart})
	require.NoError(t, err)

	// No directory or file should have been created
	_, err = os.Stat(filepath.Join(dir, config.ObserverDir))
	assert.True(t, os.IsNotExist(err), "observer dir should not exist when disabled")

	// Re-enable for subsequent tests
	SetEnabled(true)
}

func TestEmitEvent_CreatesDirectory(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	evt := Event{
		Timestamp: "2026-01-01T00:00:00Z",
		Type:      EventBuildStart,
	}

	err := EmitEvent(dir, evt)
	require.NoError(t, err)

	// Verify directory was created
	info, err := os.Stat(filepath.Join(dir, config.ObserverDir))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEmitEvent_AppendsMultiple(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()

	err := EmitEvent(dir, Event{Timestamp: "2026-01-01T00:00:00Z", Type: EventBuildStart})
	require.NoError(t, err)

	err = EmitEvent(dir, Event{Timestamp: "2026-01-01T00:01:00Z", Type: EventSprintStart, Sprint: 1})
	require.NoError(t, err)

	err = EmitEvent(dir, Event{Timestamp: "2026-01-01T00:02:00Z", Type: EventSprintComplete, Sprint: 1})
	require.NoError(t, err)

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	assert.Len(t, events, 3)
	assert.Equal(t, EventBuildStart, events[0].Type)
	assert.Equal(t, EventSprintStart, events[1].Type)
	assert.Equal(t, EventSprintComplete, events[2].Type)
	assert.Equal(t, 1, events[1].Sprint)
}

func TestEmitEvent_ValidJSON(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	evt := Event{
		Timestamp: "2026-01-01T00:00:00Z",
		Type:      EventBuildStart,
		Data:      map[string]string{"epic": "TestEpic", "effort": "high"},
	}

	err := EmitEvent(dir, evt)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, config.ObserverEventsFile))
	require.NoError(t, err)

	var parsed Event
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, EventBuildStart, parsed.Type)
	assert.Equal(t, "TestEpic", parsed.Data["epic"])
}

func TestEmitEvent_SetsTimestamp(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	evt := Event{
		Type: EventBuildStart,
		// No timestamp — should be auto-filled
	}

	err := EmitEvent(dir, evt)
	require.NoError(t, err)

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].Timestamp)
}

func TestReadEvents_MissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	events, err := ReadEvents(dir)
	assert.NoError(t, err)
	assert.Nil(t, events)
}

func TestReadEvents_MultipleEvents(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		err := EmitEvent(dir, Event{
			Timestamp: "2026-01-01T00:00:00Z",
			Type:      EventSprintStart,
			Sprint:    i + 1,
		})
		require.NoError(t, err)
	}

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	assert.Len(t, events, 5)
	for i, evt := range events {
		assert.Equal(t, i+1, evt.Sprint)
	}
}

func TestReadRecentEvents_LimitsCount(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		err := EmitEvent(dir, Event{
			Timestamp: "2026-01-01T00:00:00Z",
			Type:      EventSprintComplete,
			Sprint:    i + 1,
		})
		require.NoError(t, err)
	}

	events, err := ReadRecentEvents(dir, 3)
	require.NoError(t, err)
	assert.Len(t, events, 3)
	// Should be the last 3 events (sprints 8, 9, 10)
	assert.Equal(t, 8, events[0].Sprint)
	assert.Equal(t, 9, events[1].Sprint)
	assert.Equal(t, 10, events[2].Sprint)
}

func TestReadRecentEvents_ZeroN(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	err := EmitEvent(dir, Event{Timestamp: "2026-01-01T00:00:00Z", Type: EventBuildStart})
	require.NoError(t, err)

	events, err := ReadRecentEvents(dir, 0)
	require.NoError(t, err)
	assert.Nil(t, events)
}

func TestReadRecentEvents_NegativeN(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	err := EmitEvent(dir, Event{Timestamp: "2026-01-01T00:00:00Z", Type: EventBuildStart})
	require.NoError(t, err)

	events, err := ReadRecentEvents(dir, -5)
	require.NoError(t, err)
	assert.Nil(t, events)
}

func TestReadRecentEvents_FewerThanN(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	err := EmitEvent(dir, Event{
		Timestamp: "2026-01-01T00:00:00Z",
		Type:      EventBuildStart,
	})
	require.NoError(t, err)

	events, err := ReadRecentEvents(dir, 100)
	require.NoError(t, err)
	assert.Len(t, events, 1)
}
