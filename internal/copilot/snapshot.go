package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yevgetman/fry/internal/agent"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/lock"
	"github.com/yevgetman/fry/internal/observer"
)

// StateSnapshot is the small JSON file fry's main process rewrites on
// meaningful build state changes. The copilot agent re-reads it on each
// wake to get fresh build context. Unlike build-status.json (which is
// agent-readable but verbose), this snapshot is intentionally compact
// and tailored to the copilot's tick checklist.
type StateSnapshot struct {
	Timestamp             string          `json:"ts"`
	BuildPhase            string          `json:"build_phase"`
	BuildMode             string          `json:"build_mode"`
	BuildStatus           string          `json:"build_status"`
	CurrentSprint         int             `json:"current_sprint"`
	CurrentSprintName     string          `json:"current_sprint_name,omitempty"`
	TotalSprints          int             `json:"total_sprints"`
	BuildPID              int             `json:"build_pid"`
	BuildPIDAlive         bool            `json:"build_pid_alive"`
	LockHeld              bool            `json:"lock_held"`
	StartedAt             string          `json:"started_at,omitempty"`
	LastUpdatedAt         string          `json:"last_updated_at,omitempty"`
	RecentEventTailLen    int             `json:"recent_event_tail_len"`
	RecentEventsTail      []SnapshotEvent `json:"recent_events_tail,omitempty"`
	DeferredFailuresCount int             `json:"deferred_failures_count"`
	ActiveHealLoop        bool            `json:"active_heal_loop"`
	CurrentIterationLog   string          `json:"current_iteration_log,omitempty"`
}

// SnapshotEvent is a trimmed event entry suitable for inlining into the
// snapshot's recent_events_tail. Same shape as observer.Event minus the
// data map (which can be large) — only type, timestamp, and sprint are
// preserved.
type SnapshotEvent struct {
	Timestamp string             `json:"ts"`
	Type      observer.EventType `json:"type"`
	Sprint    int                `json:"sprint,omitempty"`
}

// snapshotDebouncer enforces the 10s minimum-interval rule on
// state-snapshot writes per project directory. The map key is the
// absolute project path; the value is the time of the last successful
// write. Writes inside the debounce window are dropped silently.
//
// Debounce is in-memory and not persisted across fry process restarts —
// that is intentional: a fresh fry process should always get one
// immediate snapshot write so the copilot sees the new build state.
type snapshotDebouncer struct {
	mu        sync.Mutex
	lastWrite map[string]time.Time
}

var globalDebouncer = &snapshotDebouncer{
	lastWrite: make(map[string]time.Time),
}

// shouldWrite returns true if the debouncer permits a write for projectDir
// at this moment. The caller is responsible for calling recordWrite()
// after a successful write — that way a failed write does not poison the
// debounce window for subsequent retries.
func (d *snapshotDebouncer) shouldWrite(projectDir string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	last, ok := d.lastWrite[projectDir]
	if ok && now.Sub(last) < time.Duration(config.CopilotStateSnapshotDebounceSec)*time.Second {
		return false
	}
	return true
}

// recordWrite stamps the debounce window for projectDir. Called by the
// caller of shouldWrite() after the on-disk write returns nil.
func (d *snapshotDebouncer) recordWrite(projectDir string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastWrite[projectDir] = now
}

// WriteStateSnapshot rewrites .fry/copilot/state-snapshot.json from the
// canonical build state. Atomic via tmpfile + rename. Debounced to at
// most one write per CopilotStateSnapshotDebounceSec seconds per project
// directory — rapid event sequences during a sprint phase will not churn
// the file.
//
// Returns nil if the write was either successful OR debounced. Returns a
// non-nil error only on actual filesystem failures.
//
// Callers should invoke this from each observer wake-point: sprint_start,
// sprint_complete, audit_begin, audit_complete, heal_begin, heal_complete,
// phase_change, build_end. The debouncer collapses bursts.
func WriteStateSnapshot(projectDir string) error {
	// Skip silently if no copilot is configured for this build.
	if !CopilotConfigured(projectDir) {
		return nil
	}

	now := time.Now().UTC()
	if !globalDebouncer.shouldWrite(projectDir, now) {
		return nil
	}

	if err := writeSnapshotNow(projectDir, now); err != nil {
		return err
	}
	globalDebouncer.recordWrite(projectDir, now)
	return nil
}

// ForceWriteStateSnapshot bypasses the debounce and writes unconditionally.
// Used by tests and by code paths that need a guaranteed write — e.g.,
// the bootstrap flow before the copilot's first cron wake fires.
func ForceWriteStateSnapshot(projectDir string) error {
	if !CopilotConfigured(projectDir) {
		return nil
	}
	now := time.Now().UTC()
	if err := writeSnapshotNow(projectDir, now); err != nil {
		return err
	}
	globalDebouncer.recordWrite(projectDir, now)
	return nil
}

// writeSnapshotNow assembles and writes the snapshot to disk. Caller is
// responsible for the manifest gate and debounce decisions.
func writeSnapshotNow(projectDir string, now time.Time) error {
	snap := buildSnapshot(projectDir, now)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("write state snapshot: marshal: %w", err)
	}
	return atomicWriteSnapshot(projectDir, append(data, '\n'))
}

func atomicWriteSnapshot(projectDir string, data []byte) error {
	finalPath := filepath.Join(projectDir, config.CopilotStateSnapshotFile)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return fmt.Errorf("write state snapshot: create dir: %w", err)
	}
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write state snapshot: write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write state snapshot: rename: %w", err)
	}
	return nil
}

// buildSnapshot reads canonical build state and assembles a fresh
// StateSnapshot value. Pure function — performs no writes.
//
// BuildPID is taken from the *currently running* fry process via
// os.Getpid(), NOT from the manifest. The manifest's BuildPID is set once
// at Bootstrap() time and is not refreshed when the user resumes via
// `fry run --continue`. Using the manifest's value would cause the
// state-snapshot's `build_pid_alive` field to read `false` after a
// resume, leading the copilot to incorrectly conclude the build has died.
func buildSnapshot(projectDir string, now time.Time) StateSnapshot {
	snap := StateSnapshot{
		Timestamp: now.Format(time.RFC3339),
		BuildPID:  os.Getpid(),
	}

	// Build phase / mode / status from .fry/build-status.json (canonical).
	if status, err := agent.ReadBuildStatus(projectDir); err == nil && status != nil {
		snap.BuildPhase = status.Build.Phase
		snap.BuildMode = status.Build.Mode
		snap.BuildStatus = status.Build.Status
		snap.CurrentSprint = status.Build.CurrentSprint
		snap.TotalSprints = status.Build.TotalSprints
		snap.StartedAt = status.Build.StartedAt.UTC().Format(time.RFC3339)
		snap.LastUpdatedAt = status.UpdatedAt.UTC().Format(time.RFC3339)

		// Look up the current sprint's name from the sprint list.
		for i := range status.Sprints {
			if status.Sprints[i].Number == status.Build.CurrentSprint {
				snap.CurrentSprintName = status.Sprints[i].Name
				if status.Sprints[i].DeferredFailures > 0 {
					snap.DeferredFailuresCount = status.Sprints[i].DeferredFailures
				}
				break
			}
		}
	}

	// Liveness check: is the build PID still alive AND holding the lock?
	if snap.BuildPID > 0 {
		snap.BuildPIDAlive = processAlive(snap.BuildPID)
	}
	snap.LockHeld = lock.IsLocked(projectDir)

	// Recent event tail — load the canonical observer stream.
	if events, err := observer.ReadRecentEvents(projectDir, config.CopilotSnapshotEventTailMax); err == nil {
		snap.RecentEventTailLen = len(events)
		snap.RecentEventsTail = make([]SnapshotEvent, 0, len(events))
		for _, e := range events {
			snap.RecentEventsTail = append(snap.RecentEventsTail, SnapshotEvent{
				Timestamp: e.Timestamp,
				Type:      e.Type,
				Sprint:    e.Sprint,
			})
		}
	}

	// Most recent build log path (best-effort hint for the copilot).
	if logPath := findMostRecentBuildLog(projectDir); logPath != "" {
		snap.CurrentIterationLog = logPath
	}

	return snap
}

// findMostRecentBuildLog returns the path of the newest .log file under
// .fry/build-logs, or "" if none exist. The copilot uses this to know
// where to look for the active sprint's iteration output.
func findMostRecentBuildLog(projectDir string) string {
	logsDir := filepath.Join(projectDir, config.BuildLogsDir)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return ""
	}
	var newest string
	var newestTime time.Time
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		if !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newest = filepath.Join(logsDir, ent.Name())
			newestTime = info.ModTime()
		}
	}
	return newest
}

// ReadStateSnapshot reads the snapshot file. Returns (nil, nil) if the
// file does not exist. Used by `fry copilot status` and tests.
func ReadStateSnapshot(projectDir string) (*StateSnapshot, error) {
	path := filepath.Join(projectDir, config.CopilotStateSnapshotFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state snapshot: %w", err)
	}
	var snap StateSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("read state snapshot: parse: %w", err)
	}
	return &snap, nil
}
