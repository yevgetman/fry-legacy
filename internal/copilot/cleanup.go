package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// CleanupOnExit is called from fry's main exit handler. It is best-effort:
// nothing in this function should panic or block fry's exit. Errors are
// silently swallowed (callers can wrap if they want logging).
//
// What it does:
//   - Removes the bootstrap PID file if the recorded PID is dead
//   - Removes the tick.lock file if the holder is dead
//   - Touches state-snapshot.json one last time (with current build state)
//
// What it does NOT do:
//   - Kill the copilot subprocess (it has its own lifecycle)
//   - Delete the cron (the copilot does that itself in its final wake)
//   - Archive the dir (caller decides via ArchiveCopilotDirIfDone)
func CleanupOnExit(projectDir string) {
	manifest, err := ReadManifest(projectDir)
	if err != nil || manifest == nil {
		return
	}

	// Drop bootstrap PID file if dead — keeps `fry copilot status` honest.
	if pid := ReadBootstrapPID(projectDir); pid > 0 && !processAlive(pid) {
		_ = os.Remove(filepath.Join(projectDir, config.CopilotBootstrapPIDFile))
	}

	// Drop tick lock if stale.
	if info, err := ReadTickLock(projectDir); err == nil && info.PID > 0 && !processAlive(info.PID) {
		_ = ReleaseTickLock(projectDir)
	}

	// Final state-snapshot write so the next wake (if any) sees the
	// post-exit state.
	_ = ForceWriteStateSnapshot(projectDir)
}

// ArchiveCopilotDirIfDone moves .fry/copilot to .fry/copilot/archive/<runID>/
// if the build is fully complete and the copilot has cleanly exited.
//
// "Cleanly exited" means:
//   - final-summary.md exists, AND
//   - cron.id is empty or refers to a removed cron, AND
//   - tick.lock is not held by a live process
//
// Returns the archive path on success, "" if no archive was performed,
// and an error only on filesystem failures.
func ArchiveCopilotDirIfDone(projectDir, runID string) (string, error) {
	manifest, err := ReadManifest(projectDir)
	if err != nil || manifest == nil {
		return "", nil
	}

	if !cleanlyExited(projectDir) {
		return "", nil
	}

	srcDir := filepath.Join(projectDir, config.CopilotDir)
	if _, err := os.Stat(srcDir); err != nil {
		return "", nil // nothing to archive
	}

	if runID == "" {
		runID = time.Now().UTC().Format("20060102-150405")
	}

	archiveBase := filepath.Join(projectDir, config.CopilotArchiveDir)
	if err := os.MkdirAll(archiveBase, 0o755); err != nil {
		return "", fmt.Errorf("archive copilot: create base: %w", err)
	}
	dstDir := filepath.Join(archiveBase, runID)

	// Move the dir contents EXCEPT for the archive subdirectory itself.
	// We do this by reading entries and renaming each one — this avoids
	// the recursive case where we'd try to move archive/ into itself.
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return "", fmt.Errorf("archive copilot: read src: %w", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return "", fmt.Errorf("archive copilot: create dst: %w", err)
	}
	for _, ent := range entries {
		if ent.Name() == "archive" {
			continue
		}
		from := filepath.Join(srcDir, ent.Name())
		to := filepath.Join(dstDir, ent.Name())
		if err := os.Rename(from, to); err != nil {
			return "", fmt.Errorf("archive copilot: move %s: %w", ent.Name(), err)
		}
	}
	return dstDir, nil
}

// cleanlyExited returns true if the copilot session appears to have
// finished cleanly (final-summary.md exists, cron.id is empty, no live
// tick lock).
func cleanlyExited(projectDir string) bool {
	// final-summary.md must exist
	summaryPath := filepath.Join(projectDir, config.CopilotFinalSummaryFile)
	if _, err := os.Stat(summaryPath); err != nil {
		return false
	}
	// cron.id should be empty or absent
	if id := ReadCronIDFile(projectDir); id != "" {
		return false
	}
	// tick lock must not be held by a live process
	if IsBusy(projectDir) {
		return false
	}
	return true
}

// CopilotConfigured reports whether a copilot manifest exists for projectDir.
// Used by external callers (CLI status, monitor) as a quick gate.
func CopilotConfigured(projectDir string) bool {
	manifest, _ := ReadManifest(projectDir)
	return manifest != nil
}

// LeftoverCronWarning returns a non-empty user-facing warning string when a
// previous copilot's cron.id file exists in projectDir but no current
// manifest exists (i.e. fry clean / fry destroy / manual rm has wiped the
// manifest but the cron itself still lives in Claude Code's session storage).
//
// fry CANNOT cancel external crons — they live in the agent runtime, not
// the project dir. The orphan agent is expected to self-prune on its next
// tick via Tick Checklist step 0 in templates/copilot/bootstrap.md.
//
// Returns "" when no leftover state is detected, or when there's an active
// manifest (in which case cron.id is legitimate, not a leftover).
func LeftoverCronWarning(projectDir string) string {
	if CopilotConfigured(projectDir) {
		return ""
	}
	cronID := ReadCronIDFile(projectDir)
	if cronID == "" {
		return ""
	}
	return fmt.Sprintf(
		"leftover copilot cron %q detected in %s/.fry/copilot/cron.id but no manifest is present. "+
			"This usually means a previous build's copilot session is still scheduled in Claude Code's runtime — "+
			"fry cannot cancel it directly. The orphan should self-prune on its next wake (the bootstrap "+
			"prompt instructs the agent to detect this case and call CronDelete). If it persists, you can clear "+
			"it manually by resuming the orphan with `claude --resume <session-id>` and asking it to delete its cron.",
		cronID, projectDir)
}
