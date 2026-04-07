package copilot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// TickLockInfo describes the holder of the tick lock.
type TickLockInfo struct {
	PID       int
	StartedAt time.Time
}

// AcquireTickLock claims the tick lock for the given PID. If the lock file
// already exists and the recorded PID is alive, returns an error. If the
// recorded PID is dead, the lock is silently stolen.
//
// Acquisition uses O_CREATE|O_EXCL for atomic claim — see
// internal/lock/lock.go for the same pattern. Two competing processes
// cannot both succeed, even if they observe a stale lock simultaneously.
//
// The lock file content is two lines:
//
//	<pid>
//	<RFC3339 timestamp>
func AcquireTickLock(projectDir string, pid int) error {
	lockPath := filepath.Join(projectDir, config.CopilotTickLockFile)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("acquire tick lock: create dir: %w", err)
	}

	content := fmt.Sprintf("%d\n%s\n", pid, time.Now().UTC().Format(time.RFC3339))

	// First try the atomic create. If the file exists, we fall through to
	// the stale-detection branch.
	if err := writeLockExcl(lockPath, content); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return fmt.Errorf("acquire tick lock: %w", err)
	}

	// Lock exists. Check if the holder is alive.
	existing, readErr := readTickLockFile(lockPath)
	if readErr == nil && existing.PID > 0 && processAlive(existing.PID) {
		return fmt.Errorf("tick lock held by live PID %d (started %s)", existing.PID, existing.StartedAt.Format(time.RFC3339))
	}

	// Stale (or unreadable). Remove and retry the atomic create exactly
	// once. Another process racing us to remove + recreate may win the
	// O_EXCL — in that case we report "still held" and let the caller
	// retry on its own schedule.
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("acquire tick lock: remove stale: %w", err)
	}
	if err := writeLockExcl(lockPath, content); err != nil {
		if os.IsExist(err) {
			// Lost the race; surface the new owner.
			if existing, readErr := readTickLockFile(lockPath); readErr == nil && existing.PID > 0 {
				return fmt.Errorf("tick lock raced; now held by PID %d", existing.PID)
			}
			return fmt.Errorf("tick lock raced; held by another process")
		}
		return fmt.Errorf("acquire tick lock: %w", err)
	}
	return nil
}

// writeLockExcl creates path with O_EXCL semantics, writing content. The
// returned error matches os.IsExist when the file already exists.
func writeLockExcl(path, content string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(content)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}

// ReleaseTickLock removes the tick lock file. Missing file is not an error.
func ReleaseTickLock(projectDir string) error {
	lockPath := filepath.Join(projectDir, config.CopilotTickLockFile)
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("release tick lock: %w", err)
	}
	return nil
}

// IsBusy reports whether the tick lock is currently held by a live process.
// Stale locks (PID dead) return false. Missing lock returns false.
func IsBusy(projectDir string) bool {
	lockPath := filepath.Join(projectDir, config.CopilotTickLockFile)
	info, err := readTickLockFile(lockPath)
	if err != nil {
		return false
	}
	return info.PID > 0 && processAlive(info.PID)
}

// ReadTickLock returns the lock holder info, or (zero value, error) if the
// lock cannot be read. Useful for `fry copilot status` reporting.
func ReadTickLock(projectDir string) (TickLockInfo, error) {
	lockPath := filepath.Join(projectDir, config.CopilotTickLockFile)
	return readTickLockFile(lockPath)
}

// readTickLockFile parses a two-line lock file:
//
//	<pid>
//	<RFC3339 timestamp>
//
// The timestamp is optional — older lock files may only contain the PID.
func readTickLockFile(path string) (TickLockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TickLockInfo{}, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return TickLockInfo{}, fmt.Errorf("empty lock file")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 {
		return TickLockInfo{}, fmt.Errorf("invalid pid in lock file: %q", lines[0])
	}
	info := TickLockInfo{PID: pid}
	if len(lines) >= 2 {
		if ts, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(lines[1])); parseErr == nil {
			info.StartedAt = ts
		}
	}
	return info, nil
}

// processAlive uses signal 0 to check whether a process exists. The
// process is treated as alive iff:
//   - syscall.Kill returns nil (process exists, signalable), OR
//   - syscall.Kill returns EPERM (process exists, not signalable by us).
//
// Any other error (ESRCH, EINVAL, …) means dead-or-unknown.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
