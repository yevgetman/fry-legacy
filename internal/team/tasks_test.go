package team

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimNextTask_ConcurrentRace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "race-team"

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		TMuxSession:      "race-session",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	task := &Task{
		ID:        "only-task",
		Title:     "the one task",
		Status:    TaskPending,
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, SaveTask(dir, teamID, task))

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]*Task, 2)
	errs := make([]error, 2)

	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			results[i], errs[i] = ClaimNextTask(ctx, dir, teamID,
				"worker-"+string(rune('A'+i)), "", filepath.Join(dir, "work"))
		}()
	}
	wg.Wait()

	claimed := 0
	for i := 0; i < 2; i++ {
		if results[i] != nil {
			claimed++
			assert.Equal(t, "only-task", results[i].ID)
		}
	}
	assert.Equal(t, 1, claimed, "exactly one goroutine should claim the task")
}

func TestMarkTaskRunning_StateTransition(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "running-team"

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	task := &Task{
		ID:        "task-run",
		Title:     "run me",
		Status:    TaskPending,
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, SaveTask(dir, teamID, task))

	err := MarkTaskRunning(dir, teamID, task, "worker-1")
	require.NoError(t, err)

	loaded, err := LoadTask(dir, teamID, "task-run")
	require.NoError(t, err)
	assert.Equal(t, TaskInProgress, loaded.Status)
	assert.Equal(t, "worker-1", loaded.Owner)
	assert.Equal(t, 1, loaded.Attempts)
	assert.NotNil(t, loaded.StartedAt)
}

func TestMarkTaskFinished_StateTransition(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "finished-team"

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	task := &Task{
		ID:        "task-fin",
		Title:     "finish me",
		Status:    TaskPending,
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, SaveTask(dir, teamID, task))

	require.NoError(t, MarkTaskRunning(dir, teamID, task, "worker-1"))
	assert.Equal(t, TaskInProgress, task.Status)

	err := MarkTaskFinished(dir, teamID, task, "worker-1", 0, "", "/tmp/log")
	require.NoError(t, err)

	loaded, err := LoadTask(dir, teamID, "task-fin")
	require.NoError(t, err)
	assert.Equal(t, TaskCompleted, loaded.Status)
	assert.NotNil(t, loaded.FinishedAt)
	assert.Equal(t, 0, loaded.ExitCode)
}

func TestRequeueOwnedTasks_ReturnsToPending(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "requeue-team"

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	// Create two tasks owned by worker-1 in assigned and in-progress states.
	task1 := &Task{
		ID:        "task-a",
		Title:     "assigned task",
		Status:    TaskAssigned,
		Owner:     "worker-1",
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	task2 := &Task{
		ID:        "task-b",
		Title:     "running task",
		Status:    TaskInProgress,
		Owner:     "worker-1",
		Priority:  2,
		CreatedAt: time.Now().UTC(),
	}
	// A third task owned by a different worker should not be requeued.
	task3 := &Task{
		ID:        "task-c",
		Title:     "other worker task",
		Status:    TaskInProgress,
		Owner:     "worker-2",
		Priority:  1,
		CreatedAt: time.Now().UTC(),
	}
	require.NoError(t, SaveTask(dir, teamID, task1))
	require.NoError(t, SaveTask(dir, teamID, task2))
	require.NoError(t, SaveTask(dir, teamID, task3))

	err := RequeueOwnedTasks(dir, teamID, "worker-1", "worker crashed")
	require.NoError(t, err)

	loaded1, err := LoadTask(dir, teamID, "task-a")
	require.NoError(t, err)
	assert.Equal(t, TaskPending, loaded1.Status)
	assert.Empty(t, loaded1.Owner)

	loaded2, err := LoadTask(dir, teamID, "task-b")
	require.NoError(t, err)
	assert.Equal(t, TaskPending, loaded2.Status)
	assert.Empty(t, loaded2.Owner)

	// Worker-2's task should remain in-progress.
	loaded3, err := LoadTask(dir, teamID, "task-c")
	require.NoError(t, err)
	assert.Equal(t, TaskInProgress, loaded3.Status)
	assert.Equal(t, "worker-2", loaded3.Owner)
}

func TestPause_SetsStatusPaused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "pause-team"

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		TMuxSession:      "pause-session",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	err := Pause(dir, teamID)
	require.NoError(t, err)

	loaded, err := LoadConfig(dir, teamID)
	require.NoError(t, err)
	assert.Equal(t, StatusPaused, loaded.Status)
}

func TestShutdown_SetsStatusShutdown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	teamID := "shutdown-team"

	oldTmux := DefaultTmux
	DefaultTmux = newFakeTmux()
	t.Cleanup(func() { DefaultTmux = oldTmux })

	require.NoError(t, ensureTeamLayout(dir, teamID))
	cfg := &Config{
		TeamID:           teamID,
		ProjectDir:       dir,
		BuildDir:         dir,
		Status:           StatusRunning,
		TMuxSession:      "shutdown-session",
		GitIsolationMode: IsolationShared,
	}
	require.NoError(t, SaveConfig(dir, cfg))

	err := Shutdown(context.Background(), dir, teamID, true)
	require.NoError(t, err)

	loaded, err := LoadConfig(dir, teamID)
	require.NoError(t, err)
	assert.Equal(t, StatusShutdown, loaded.Status)
}

func TestSortedTeamIDs_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	ids, err := SortedTeamIDs(dir)
	require.NoError(t, err)
	assert.Empty(t, ids)
}

func TestSortedTeamIDs_MultipleTeams(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// SortedTeamIDs reads directories under RootDir = <projectDir>/.fry/team/
	root := RootDir(dir)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "charlie"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "alpha"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bravo"), 0o755))
	// Add a regular file that should be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(root, "not-a-team.txt"), []byte("x"), 0o644))

	ids, err := SortedTeamIDs(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "bravo", "charlie"}, ids)
}
