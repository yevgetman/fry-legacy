package team

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTmux struct {
	sessions map[string]map[string]string
}

func newFakeTmux() *fakeTmux {
	return &fakeTmux{sessions: map[string]map[string]string{}}
}

func (f *fakeTmux) HasSession(ctx context.Context, session string) bool {
	_, ok := f.sessions[session]
	return ok
}

func (f *fakeTmux) NewSession(ctx context.Context, session, window, command string) (string, error) {
	f.sessions[session] = map[string]string{window: command}
	return "%" + session + "-leader", nil
}

func (f *fakeTmux) NewWindow(ctx context.Context, session, window, command string) (string, error) {
	if _, ok := f.sessions[session]; !ok {
		f.sessions[session] = map[string]string{}
	}
	f.sessions[session][window] = command
	return "%" + window, nil
}

func (f *fakeTmux) KillSession(ctx context.Context, session string) error {
	delete(f.sessions, session)
	return nil
}

func (f *fakeTmux) KillWindow(ctx context.Context, session, window string) error {
	if windows, ok := f.sessions[session]; ok {
		delete(windows, window)
	}
	return nil
}

func (f *fakeTmux) Attach(ctx context.Context, session string) error { return nil }

func (f *fakeTmux) WindowAlive(ctx context.Context, session, window string) bool {
	windows, ok := f.sessions[session]
	if !ok {
		return false
	}
	_, ok = windows[window]
	return ok
}

func TestStart_InvalidExecutablePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	_, err := Start(context.Background(), StartOptions{
		ProjectDir:       dir,
		TeamID:           "test-exe-validation",
		Workers:          1,
		GitIsolationMode: IsolationShared,
		ExecutablePath:   filepath.Join(t.TempDir(), "nonexistent-binary"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestResume_InvalidExecutablePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Set up minimal team config so Resume gets past LoadConfig
	require.NoError(t, ensureTeamLayout(dir, "test-team"))
	cfg := &Config{
		TeamID:           "test-team",
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusPaused,
		TMuxSession:      "fry-test-team",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	err := Resume(context.Background(), dir, "test-team", filepath.Join(t.TempDir(), "nonexistent-binary"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestScale_InvalidExecutablePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, ensureTeamLayout(dir, "test-team"))
	cfg := &Config{
		TeamID:           "test-team",
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		TMuxSession:      "fry-test-team",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	err := Scale(context.Background(), ScaleOptions{
		ProjectDir: dir,
		TeamID:     "test-team",
		Add:        1,
	}, filepath.Join(t.TempDir(), "nonexistent-binary"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not accessible")
}

func TestStartSharedTeamRuntime(t *testing.T) {
	dir := t.TempDir()
	taskFile := filepath.Join(dir, "tasks.json")
	taskPayload := TaskFile{
		Tasks: []TaskInput{
			{ID: "001", Title: "First", Command: "echo first > first.txt", Role: "executor"},
			{ID: "002", Title: "Second", Command: "echo second > second.txt", Role: "tester"},
		},
	}
	data, err := json.Marshal(taskPayload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(taskFile, data, 0o644))

	oldTmux := DefaultTmux
	DefaultTmux = newFakeTmux()
	t.Cleanup(func() { DefaultTmux = oldTmux })

	// Create a fake executable file so the os.Stat validation passes.
	fakeExe := filepath.Join(dir, "fry-fake")
	require.NoError(t, os.WriteFile(fakeExe, []byte("#!/bin/sh\n"), 0o755))

	cfg, err := Start(context.Background(), StartOptions{
		ProjectDir:       dir,
		TeamID:           "demo-team",
		Workers:          2,
		Roles:            []string{"executor", "tester"},
		GitIsolationMode: IsolationShared,
		ExecutablePath:   fakeExe,
		TaskFile:         taskFile,
	})
	require.NoError(t, err)
	assert.Equal(t, "demo-team", cfg.TeamID)
	assert.Equal(t, StatusRunning, cfg.Status)

	snap, err := SnapshotForTeam(context.Background(), dir, cfg.TeamID)
	require.NoError(t, err)
	assert.Equal(t, 2, len(snap.Workers))
	assert.Equal(t, 2, snap.Pending)
	assert.True(t, snap.Active)

	events, err := os.ReadFile(TeamEventsPath(dir, cfg.TeamID))
	require.NoError(t, err)
	assert.Contains(t, string(events), "team_start")
	assert.Contains(t, string(events), "team_task_created")
}

func TestRunWorkerCompletesSharedTaskAndFinalizes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, ensureTeamLayout(dir, "demo"))
	cfg := &Config{
		TeamID:           "demo",
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		TMuxSession:      "demo-session",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))
	require.NoError(t, setActiveTeam(dir, "demo"))
	identity := &WorkerIdentity{
		WorkerID: "worker-1",
		Role:     "executor",
		WorkDir:  dir,
		Status:   WorkerIdle,
	}
	require.NoError(t, SaveIdentity(dir, "demo", identity))
	require.NoError(t, SaveWorkerRecord(dir, "demo", &WorkerRecord{
		WorkerID:      "worker-1",
		Status:        WorkerIdle,
		DesiredStatus: WorkerRunning,
	}))
	task := &Task{
		ID:        "001",
		Title:     "write result",
		Command:   "printf 'done' > result.txt",
		Status:    TaskPending,
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, SaveTask(dir, "demo", task))

	err := RunWorker(context.Background(), WorkerRunOptions{
		ProjectDir: dir,
		TeamID:     "demo",
		WorkerID:   "worker-1",
		Once:       true,
	})
	require.NoError(t, err)

	task, err = LoadTask(dir, "demo", "001")
	require.NoError(t, err)
	assert.Equal(t, TaskCompleted, task.Status)
	assert.FileExists(t, filepath.Join(dir, "result.txt"))

	cfg, err = LoadConfig(dir, "demo")
	require.NoError(t, err)
	assert.Equal(t, StatusComplete, cfg.Status)
	assert.FileExists(t, MergeReportPath(dir, "demo"))
}
