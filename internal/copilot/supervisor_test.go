package copilot

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestSupervisorStartsAndStopsCleanly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sv := StartSupervisor(dir)
	// Stopping immediately should not panic or hang.
	sv.Stop()
	sv.Stop() // idempotent
}

func TestSupervisorDetectsManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sv := StartSupervisor(dir)
	defer sv.Stop()

	// Initially no scheduler.
	sv.mu.Lock()
	assert.Nil(t, sv.scheduler, "no scheduler before manifest")
	sv.mu.Unlock()

	// Write a manifest. The supervisor should detect it on next poll.
	m := &Manifest{
		SessionID:    "test-session",
		BuildPID:     os.Getpid(),
		BuildDir:     dir,
		Engine:       "claude",
		Interval:     "10m",
		EpicName:     "Test",
		EffortLevel:  "max",
		TotalSprints: 3,
	}
	require.NoError(t, WriteManifest(dir, m))

	// Wait for a couple poll cycles.
	time.Sleep(3 * SupervisorPollInterval)

	sv.mu.Lock()
	hasSched := sv.scheduler != nil
	sv.mu.Unlock()
	assert.True(t, hasSched, "supervisor should have started a scheduler after manifest appeared")
}

func TestSupervisorRespectsStopRequested(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-plant both manifest AND stop-requested.
	m := &Manifest{
		SessionID: "test-session",
		BuildPID:  os.Getpid(),
		BuildDir:  dir,
		Engine:    "claude",
		Interval:  "10m",
	}
	require.NoError(t, WriteManifest(dir, m))
	stopPath := filepath.Join(dir, config.CopilotStopRequestedFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(stopPath), 0o755))
	require.NoError(t, os.WriteFile(stopPath, []byte("now\n"), 0o644))

	sv := StartSupervisor(dir)
	defer sv.Stop()

	time.Sleep(3 * SupervisorPollInterval)

	sv.mu.Lock()
	hasSched := sv.scheduler != nil
	sv.mu.Unlock()
	assert.False(t, hasSched, "supervisor must not start scheduler when stop-requested exists")
}

func TestSupervisorSetScheduler(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sv := StartSupervisor(dir)
	defer sv.Stop()

	sched := &TickScheduler{
		opts:   SchedulerOpts{ProjectDir: dir, Engine: "claude", Interval: time.Minute},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go sched.run()

	sv.SetScheduler(sched)

	sv.mu.Lock()
	assert.Equal(t, sched, sv.scheduler)
	sv.mu.Unlock()
}
