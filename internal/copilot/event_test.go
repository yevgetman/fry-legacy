package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/observer"
)

func TestEmitEventWritesBothStreams(t *testing.T) {
	t.Parallel()
	observer.SetEnabled(true)
	dir := t.TempDir()

	evt := Event{
		Type: observer.EventCopilotBootstrap,
		Data: map[string]string{
			"session_id": "test-session",
			"cron_id":    "test-cron",
		},
	}
	require.NoError(t, EmitEvent(dir, evt))

	// Copilot stream
	copilotPath := filepath.Join(dir, config.CopilotEventsJSONLFile)
	copilotData, err := os.ReadFile(copilotPath)
	require.NoError(t, err)
	assert.Contains(t, string(copilotData), "copilot_bootstrap")
	assert.Contains(t, string(copilotData), "test-session")

	// Observer canonical stream
	observerPath := filepath.Join(dir, config.ObserverEventsFile)
	observerData, err := os.ReadFile(observerPath)
	require.NoError(t, err)
	assert.Contains(t, string(observerData), "copilot_bootstrap")
	assert.Contains(t, string(observerData), "test-session")
}

func TestEmitEventTimestampDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	evt := Event{Type: observer.EventCopilotWakeStart}
	require.NoError(t, EmitEvent(dir, evt))

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotEmpty(t, events[0].Timestamp, "EmitEvent should populate empty timestamps")
}

func TestEmitEventPreservesTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const ts = "2026-04-07T00:00:00Z"
	require.NoError(t, EmitEvent(dir, Event{Type: observer.EventCopilotWakeEnd, Timestamp: ts}))

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, ts, events[0].Timestamp)
}

func TestReadEventsMissingReturnsNil(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	events, err := ReadEvents(dir)
	assert.NoError(t, err)
	assert.Nil(t, events)
}

func TestReadEventsMultiple(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, EmitEvent(dir, Event{Type: observer.EventCopilotBootstrap}))
	require.NoError(t, EmitEvent(dir, Event{Type: observer.EventCopilotWakeStart}))
	require.NoError(t, EmitEvent(dir, Event{Type: observer.EventCopilotWakeEnd}))

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	assert.Len(t, events, 3)
	assert.Equal(t, observer.EventCopilotBootstrap, events[0].Type)
	assert.Equal(t, observer.EventCopilotWakeStart, events[1].Type)
	assert.Equal(t, observer.EventCopilotWakeEnd, events[2].Type)
}

func TestAppendEventsText(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, AppendEventsText(dir, "first line"))
	require.NoError(t, AppendEventsText(dir, "second line"))
	require.NoError(t, AppendEventsText(dir, "trailing-newline\n"))

	data, err := os.ReadFile(filepath.Join(dir, config.CopilotEventsTextFile))
	require.NoError(t, err)
	assert.Equal(t, "first line\nsecond line\ntrailing-newline\n", string(data))
}

func TestCountEventsByType(t *testing.T) {
	t.Parallel()

	events := []Event{
		{Type: observer.EventCopilotBootstrap},
		{Type: observer.EventCopilotWakeStart},
		{Type: observer.EventCopilotWakeEnd},
		{Type: observer.EventCopilotWakeStart},
	}

	counts := CountEventsByType(events)
	assert.Equal(t, 1, counts[observer.EventCopilotBootstrap])
	assert.Equal(t, 2, counts[observer.EventCopilotWakeStart])
	assert.Equal(t, 1, counts[observer.EventCopilotWakeEnd])
}
