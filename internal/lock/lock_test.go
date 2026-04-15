package lock

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yevgetman/fry/internal/config"
)

func TestAcquireRelease(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	require.NoError(t, Acquire(projectDir))
	lockPath := filepath.Join(projectDir, config.LockFile)
	data, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(data))

	require.NoError(t, Release(projectDir))
	_, err = os.Stat(lockPath)
	assert.True(t, os.IsNotExist(err), "lock file should be removed after release")
}

func TestAcquireStale(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lockPath := filepath.Join(projectDir, config.LockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte("999999"), 0o644))

	require.NoError(t, Acquire(projectDir))
	require.NoError(t, Release(projectDir))
}

func TestAcquireDryRunSkips(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	require.NoError(t, AcquireIfNotDryRun(projectDir, true))
	_, err := os.Stat(filepath.Join(projectDir, config.LockFile))
	assert.True(t, os.IsNotExist(err), "lock file should not be created in dry-run mode")
}

func TestAcquireIfNotDryRun_AcquiresWhenNotDryRun(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	require.NoError(t, AcquireIfNotDryRun(projectDir, false))
	lockPath := filepath.Join(projectDir, config.LockFile)
	data, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), string(data))

	require.NoError(t, Release(projectDir))
}

func TestAcquire_LiveProcessBlocks(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	require.NoError(t, Acquire(projectDir))
	defer func() { _ = Release(projectDir) }()

	// Second acquire should fail with "another fry instance" message
	err := Acquire(projectDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another fry instance is running")
}

func TestRelease_NonexistentLock(t *testing.T) {
	t.Parallel()

	require.NoError(t, Release(t.TempDir()))
}

func TestAcquire_CorruptedLockFile(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lockPath := filepath.Join(projectDir, config.LockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte("not-a-pid"), 0o644))

	// Should treat corrupted PID as stale and acquire
	require.NoError(t, Acquire(projectDir))
	require.NoError(t, Release(projectDir))
}

func TestIsLockedNoFile(t *testing.T) {
	t.Parallel()

	assert.False(t, IsLocked(t.TempDir()))
}

func TestIsLockedStalePID(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lockPath := filepath.Join(projectDir, config.LockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte("999999999"), 0o644))

	assert.False(t, IsLocked(projectDir))
}

func TestIsLockedActivePID(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lockPath := filepath.Join(projectDir, config.LockFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(lockPath), 0o755))
	require.NoError(t, os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())), 0o644))

	assert.True(t, IsLocked(projectDir))
}

func TestProcessAlive_ZeroPID(t *testing.T) {
	t.Parallel()

	assert.False(t, processAlive(0))
}

func TestProcessAlive_NegativePID(t *testing.T) {
	t.Parallel()

	assert.False(t, processAlive(-1))
}

// P3: concurrent lock contention
func TestAcquire_ConcurrentContention(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	require.NoError(t, Acquire(projectDir))
	defer func() { _ = Release(projectDir) }()

	// Multiple goroutines try to acquire concurrently — all should fail
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			errs <- Acquire(projectDir)
		}()
	}

	for i := 0; i < 5; i++ {
		err := <-errs
		require.Error(t, err)
		assert.Contains(t, err.Error(), "another fry instance")
	}
}
