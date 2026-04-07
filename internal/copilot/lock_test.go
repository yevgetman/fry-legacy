package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestAcquireReleaseTickLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, AcquireTickLock(dir, os.Getpid()))
	assert.True(t, IsBusy(dir))

	require.NoError(t, ReleaseTickLock(dir))
	assert.False(t, IsBusy(dir))
}

func TestReleaseTickLockMissingNoError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.NoError(t, ReleaseTickLock(dir))
}

func TestAcquireTickLockBlockedByLivePID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Hold the lock with our own PID
	require.NoError(t, AcquireTickLock(dir, os.Getpid()))

	// Second acquire with a different (also live) PID is rejected.
	err := AcquireTickLock(dir, os.Getpid())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "live PID")
}

func TestStaleTickLockStolen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Plant a lock file with an unlikely-to-exist PID.
	lockPath := filepath.Join(dir, config.CopilotTickLockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte("999999\n2000-01-01T00:00:00Z\n"), 0o644))

	// Acquire should silently steal it.
	require.NoError(t, AcquireTickLock(dir, os.Getpid()))

	info, err := ReadTickLock(dir)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
}

func TestReadTickLockReturnsTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, AcquireTickLock(dir, os.Getpid()))
	info, err := ReadTickLock(dir)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.False(t, info.StartedAt.IsZero())
}

func TestIsBusyMissingLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.False(t, IsBusy(dir))
}

func TestIsBusyStaleLock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lockPath := filepath.Join(dir, config.CopilotTickLockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte("999999\n"), 0o644))

	assert.False(t, IsBusy(dir), "stale PID should not register as busy")
}
