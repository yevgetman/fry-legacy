package observer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
)

// --- stub engine ---

type stubObserverEngine struct {
	output string
	err    error
}

func (s *stubObserverEngine) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(s.output))
	}
	return s.output, 0, s.err
}

func (s *stubObserverEngine) Name() string { return "stub" }

// --- ShouldWakeUp tests ---

func TestShouldWakeUp_LowEffort(t *testing.T) {
	t.Parallel()

	assert.False(t, ShouldWakeUp(epic.EffortFast, WakeAfterSprint))
	assert.False(t, ShouldWakeUp(epic.EffortFast, WakeAfterBuildAudit))
	assert.False(t, ShouldWakeUp(epic.EffortFast, WakeBuildEnd))
}

func TestShouldWakeUp_MediumEffort(t *testing.T) {
	t.Parallel()

	assert.False(t, ShouldWakeUp(epic.EffortStandard, WakeAfterSprint))
	assert.False(t, ShouldWakeUp(epic.EffortStandard, WakeAfterBuildAudit))
	assert.True(t, ShouldWakeUp(epic.EffortStandard, WakeBuildEnd))
}

func TestShouldWakeUp_HighEffort(t *testing.T) {
	t.Parallel()

	assert.True(t, ShouldWakeUp(epic.EffortHigh, WakeAfterSprint))
	assert.True(t, ShouldWakeUp(epic.EffortHigh, WakeAfterBuildAudit))
	assert.True(t, ShouldWakeUp(epic.EffortHigh, WakeBuildEnd))
}

func TestShouldWakeUp_MaxEffort(t *testing.T) {
	t.Parallel()

	assert.True(t, ShouldWakeUp(epic.EffortMax, WakeAfterSprint))
	assert.True(t, ShouldWakeUp(epic.EffortMax, WakeAfterBuildAudit))
	assert.True(t, ShouldWakeUp(epic.EffortMax, WakeBuildEnd))
}

func TestShouldWakeUp_EmptyEffort(t *testing.T) {
	t.Parallel()

	// Empty effort is treated like standard
	assert.False(t, ShouldWakeUp("", WakeAfterSprint))
	assert.True(t, ShouldWakeUp("", WakeBuildEnd))
}

// --- InitBuild tests ---

func TestInitBuild_CreatesDirAndResetsScratchpad(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()

	err := InitBuild(dir, "TestEpic", "high", 3)
	require.NoError(t, err)

	// Verify observer directory exists
	info, err := os.Stat(filepath.Join(dir, config.ObserverDir))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	// Verify scratchpad is empty
	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Empty(t, content)

	// Verify build_start event emitted
	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, EventBuildStart, events[0].Type)
	assert.Equal(t, "TestEpic", events[0].Data["epic"])
	assert.Equal(t, "high", events[0].Data["effort"])
	assert.Equal(t, "3", events[0].Data["total_sprints"])
}

func TestInitBuild_ClearsStaleEvents(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()

	// Simulate a prior build that left events behind
	require.NoError(t, EmitEvent(dir, Event{Timestamp: "2025-01-01T00:00:00Z", Type: EventBuildStart}))
	require.NoError(t, EmitEvent(dir, Event{Timestamp: "2025-01-01T00:01:00Z", Type: EventSprintComplete, Sprint: 1}))
	staleEvents, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, staleEvents, 2)

	// Init a new build — should clear stale events and emit fresh build_start
	err = InitBuild(dir, "NewEpic", "high", 2)
	require.NoError(t, err)

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 1, "should only have the new build_start event")
	assert.Equal(t, EventBuildStart, events[0].Type)
	assert.Equal(t, "NewEpic", events[0].Data["epic"])
}

func TestInitBuild_DoesNotWriteIdentity(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()

	err := InitBuild(dir, "TestEpic", "high", 3)
	require.NoError(t, err)

	// Verify no identity file is written to the project directory
	identityPath := filepath.Join(dir, ".fry/observer/identity.md")
	_, err = os.Stat(identityPath)
	assert.True(t, os.IsNotExist(err), "identity.md should not be written to project dir")
}

func TestResumeSession_PreservesScratchpadAndEvents(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitNewSession(dir, "TestEpic", "high", 3))
	require.NoError(t, WriteScratchpad(dir, "keep this scratchpad"))
	require.NoError(t, EmitEvent(dir, Event{Type: EventSprintComplete, Sprint: 1}))

	require.NoError(t, ResumeSession(dir, "TestEpic", "high", 3))

	scratchpad, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Equal(t, "keep this scratchpad", scratchpad)

	events, err := ReadEvents(dir)
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, "resume", events[len(events)-1].Data["mode"])
}

// --- Scratchpad tests ---

func TestWriteAndReadScratchpad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := WriteScratchpad(dir, "# Build Notes\nSprint 1 was interesting.\n")
	require.NoError(t, err)

	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Equal(t, "# Build Notes\nSprint 1 was interesting.\n", content)
}

func TestReadScratchpad_Missing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Empty(t, content)
}

func TestAppendScratchpad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := WriteScratchpad(dir, "line1\n")
	require.NoError(t, err)

	err = AppendScratchpad(dir, "line2\n")
	require.NoError(t, err)

	err = AppendScratchpad(dir, "line3\n")
	require.NoError(t, err)

	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3\n", content)
}

func TestAppendScratchpad_CreatesMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := AppendScratchpad(dir, "first entry\n")
	require.NoError(t, err)

	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Equal(t, "first entry\n", content)
}

// --- WakeUp tests ---

func TestWakeUp_Success(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	eng := &stubObserverEngine{
		output: `{"thoughts":"The build started cleanly. No issues observed in sprint 1.","scratchpad":"Sprint 1 completed in 3 iterations. Watch for test flakiness in sprint 2."}`,
	}

	obs, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       eng,
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})

	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.Contains(t, obs.Thoughts, "build started cleanly")
	assert.Contains(t, obs.ScratchpadDelta, "Sprint 1 completed")
	assert.Equal(t, "ok", string(obs.ParseStatus))
}

func TestWakeUp_UpdatesScratchpad(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	eng := &stubObserverEngine{
		output: `{"thoughts":"Observation 1.","scratchpad":"Note from wake 1."}`,
	}

	_, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       eng,
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})
	require.NoError(t, err)

	content, err := ReadScratchpad(dir)
	require.NoError(t, err)
	assert.Contains(t, content, "Note from wake 1")
}

func TestWakeUp_IdentityReadOnly(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	// Even if the LLM returns extra fields, no identity file should be written
	eng := &stubObserverEngine{
		output: `{"thoughts":"Significant learning happened.","scratchpad":"Updating scratchpad."}`,
	}

	_, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       eng,
		EpicName:     "TestEpic",
		WakePoint:    WakeBuildEnd,
		SprintNum:    3,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})
	require.NoError(t, err)

	// Verify no identity file exists in the project directory
	identityPath := filepath.Join(dir, ".fry/observer/identity.md")
	_, statErr := os.Stat(identityPath)
	assert.True(t, os.IsNotExist(statErr), "identity.md should not be written during builds")
}

func TestWakeUp_EngineFailure(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	eng := &stubObserverEngine{
		output: "",
		err:    fmt.Errorf("engine crashed"),
	}

	obs, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       eng,
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})

	// Should not return error for non-fatal engine failure
	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.Equal(t, "failed", string(obs.ParseStatus))
	assert.Empty(t, obs.Thoughts)
}

func TestWakeUp_ContextCancelled(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eng := &stubObserverEngine{
		output: "",
		err:    ctx.Err(),
	}

	_, err := WakeUp(ctx, ObserverOpts{
		ProjectDir:   dir,
		Engine:       eng,
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestWakeUp_NilEngine(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()

	_, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       nil,
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestWakeUp_ParseFailureQuarantinesTranscript(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))
	require.NoError(t, WriteScratchpad(dir, "existing scratchpad"))

	obs, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir:   dir,
		Engine:       &stubObserverEngine{output: "Reading prompt from stdin...\nthis is not json"},
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})
	require.NotNil(t, obs)
	require.NoError(t, err)
	assert.Equal(t, "failed", string(obs.ParseStatus))
	assert.Empty(t, obs.Thoughts)
	assert.NotEmpty(t, obs.RawOutputPath)

	scratchpad, readErr := ReadScratchpad(dir)
	require.NoError(t, readErr)
	assert.Equal(t, "existing scratchpad", scratchpad)
}

func TestWakeUp_RepairsTranscriptPreamble(t *testing.T) {
	t.Parallel()
	SetEnabled(true)

	dir := t.TempDir()
	require.NoError(t, InitBuild(dir, "TestEpic", "high", 3))

	obs, err := WakeUp(context.Background(), ObserverOpts{
		ProjectDir: dir,
		Engine: &stubObserverEngine{
			output: "Reading prompt from stdin...\n```json\n{\"thoughts\":\"Recovered structured output.\",\"scratchpad\":\"Carry this forward.\"}\n```",
		},
		EpicName:     "TestEpic",
		WakePoint:    WakeAfterSprint,
		SprintNum:    1,
		TotalSprints: 3,
		EffortLevel:  epic.EffortHigh,
	})
	require.NoError(t, err)
	require.NotNil(t, obs)
	assert.Equal(t, "repaired", string(obs.ParseStatus))
	assert.Equal(t, "Recovered structured output.", obs.Thoughts)
}
