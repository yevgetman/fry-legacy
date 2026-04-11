package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/copilot"
	"github.com/yevgetman/fry/internal/observer"
)

// resetCobraFlagsRecursive resets flag values for the copilot subcommand
// tree only. We deliberately do NOT walk the entire root tree because
// other parallel tests in this package read package-level flag variables
// (runDryRun, runEngine, etc.) directly — touching those from inside our
// helper would race with them.
//
// All copilot flags are stored cobra-internally (no Go-var binding) so
// scoping the reset to copilotCmd is sufficient to prevent inter-test
// flag-value leaks within the test file.
func resetCobraFlagsRecursive(_ *cobra.Command) {
	resetFlagSet(copilotCmd.PersistentFlags())
	resetFlagSet(copilotCmd.Flags())
	for _, child := range copilotCmd.Commands() {
		resetFlagSet(child.PersistentFlags())
		resetFlagSet(child.Flags())
	}
}

// resetFlagSet sets every flag in the set back to its DefValue and marks
// it as unchanged. Errors are ignored — flags whose Set rejects DefValue
// are exotic and not used in this codebase.
func resetFlagSet(fs *pflag.FlagSet) {
	if fs == nil {
		return
	}
	fs.VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

// rootCmdTestMu serializes access to the singleton rootCmd across parallel
// tests in this file. cobra's Command type is not safe for concurrent use,
// so we serialize all SetOut/SetArgs/Execute calls without sacrificing
// the t.Parallel() calls in individual tests.
var rootCmdTestMu sync.Mutex

// runRootCmd executes rootCmd with the given args, capturing combined
// stdout/stderr. Holds rootCmdTestMu for the duration of the call so that
// concurrent tests do not race on the shared cobra command state.
//
// Cobra persists flag values across Execute() calls — `--json` set in one
// test would leak into the next one. To prevent this, the helper resets
// every flag (root + all descendants) to its default value before AND
// after each invocation.
func runRootCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()

	resetCobraFlagsRecursive(rootCmd)

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	defer func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		resetCobraFlagsRecursive(rootCmd)
	}()
	err := rootCmd.Execute()
	return out.String(), err
}

// plantCopilotManifest creates a minimal valid copilot manifest in dir so
// status/attach/stop subcommands have something to read.
func plantCopilotManifest(t *testing.T, dir string, sessionID string) *copilot.Manifest {
	t.Helper()
	m := &copilot.Manifest{
		SessionID:                 sessionID,
		BuildPID:                  os.Getpid(),
		BuildDir:                  dir,
		Engine:                    "claude",
		Model:                     "opus[1m]",
		StartedAt:                 "2026-04-07T00:00:00Z",
		Interval:                  "10m",
		EpicName:                  "Test Epic",
		EffortLevel:               "max",
		MaxInterventionsPerClass:  3,
		StopOnBuildComplete:       true,
		Mode:                      copilot.ModeActive,
		EngineCapabilities:        copilot.EngineCapabilities{SessionIDFlag: true, CronCreate: true},
		SessionIDCaptureMechanism: copilot.SessionIDPreSpecified,
	}
	require.NoError(t, copilot.WriteManifest(dir, m))
	return m
}

func TestCopilotStatusAbsent(t *testing.T) {
	dir := t.TempDir()

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	require.Error(t, err, "absent copilot should return non-zero")
	assert.Contains(t, out, "ABSENT")
}

func TestCopilotStatusActive(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")
	// ACTIVE requires the build to be alive AND at least one wake event
	// to have been emitted by the in-process scheduler. The cron ID file
	// is from the deprecated CronCreate-based design and no longer drives
	// status — it's still displayed for diagnostic purposes only.
	require.NoError(t, copilot.WriteCronIDFile(dir, "cron_test"))
	require.NoError(t, copilot.EmitEvent(dir, copilot.Event{
		Type: observer.EventCopilotWakeStart,
		Data: map[string]string{"wake_number": "1"},
	}))

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "ACTIVE")
	assert.Contains(t, out, "test-session-uuid")
	assert.Contains(t, out, "cron_test")
	assert.Contains(t, out, "claude")
	assert.Contains(t, out, "opus[1m]")
}

func TestCopilotStatusStartingWhenAliveWithNoWakes(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")
	// No wake events emitted. Build PID is os.Getpid() (alive). The
	// in-process scheduler hasn't fired its first tick yet — STARTING is
	// the correct state. This is the regression case for Bug 13: the
	// original implementation looked at the cron file (which the new
	// scheduler never writes) and reported STARTING forever, even after
	// many ticks had fired.
	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "STARTING")
}

func TestCopilotStatusStaleAfterWakesWhenBuildDead(t *testing.T) {
	dir := t.TempDir()
	// Manifest with a deliberately dead PID — pid 1 (init/launchd) is
	// alive but not owned by this user, so processAlive returns true.
	// Use a definitely-dead pid by manifesting our own pid then mutating
	// the file with an obviously-dead value.
	m := plantCopilotManifest(t, dir, "test-session-uuid")
	m.BuildPID = 0 // 0 is treated as dead by processAlive
	require.NoError(t, copilot.WriteManifest(dir, m))
	// Plant a wake event so hasWakeEvents=true.
	require.NoError(t, copilot.EmitEvent(dir, copilot.Event{
		Type: observer.EventCopilotWakeStart,
		Data: map[string]string{"wake_number": "1"},
	}))

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	// STALE returns exit code 2 for scripting.
	require.Error(t, err)
	assert.Contains(t, out, "STALE")
}

func TestCopilotStatusDoesNotModifySnapshotFile(t *testing.T) {
	// Bug 16 regression test. The original Bug 15 fix called
	// ForceWriteStateSnapshot from the CLI's status command to surface
	// fresh data, but buildSnapshot uses os.Getpid() to populate
	// BuildPID — when called from a CLI helper, that captures the
	// CLI's ephemeral PID instead of fry main's PID, then exits,
	// leaving the snapshot file with a dead PID and build_pid_alive=true.
	// This test ensures the CLI status command never mutates the
	// snapshot file under any circumstances.
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	// Plant a snapshot with a known sentinel PID and build_phase. If
	// the CLI rewrites the file, BuildPID will become os.Getpid() and
	// the assertion below will fail with a clear diagnostic.
	snapPath := filepath.Join(dir, config.CopilotStateSnapshotFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(snapPath), 0o755))
	plantedJSON := `{
  "ts": "2026-04-07T20:00:00Z",
  "build_phase": "sprint",
  "build_pid": 99999,
  "build_pid_alive": true,
  "current_sprint": 2,
  "total_sprints": 7
}
`
	require.NoError(t, os.WriteFile(snapPath, []byte(plantedJSON), 0o644))

	beforeStat, err := os.Stat(snapPath)
	require.NoError(t, err)
	beforeMTime := beforeStat.ModTime()
	beforeBytes, err := os.ReadFile(snapPath)
	require.NoError(t, err)

	// Run the CLI status command three times in a row — each call would
	// previously rewrite the snapshot with the CLI's PID via
	// ForceWriteStateSnapshot.
	for i := 0; i < 3; i++ {
		_, _ = runRootCmd(t, "copilot", "status", "--project-dir", dir)
	}

	afterStat, err := os.Stat(snapPath)
	require.NoError(t, err)
	afterBytes, err := os.ReadFile(snapPath)
	require.NoError(t, err)

	assert.Equal(t, beforeMTime, afterStat.ModTime(),
		"fry copilot status must NOT touch the snapshot file mtime — "+
			"writing from a CLI helper clobbers BuildPID with the CLI's ephemeral PID (Bug 16)")
	assert.Equal(t, string(beforeBytes), string(afterBytes),
		"fry copilot status must NOT modify snapshot file bytes")
	// Sentinel PID must still be present.
	assert.Contains(t, string(afterBytes), `"build_pid": 99999`,
		"snapshot's planted sentinel PID must survive multiple status calls")
}

func TestCopilotStatusOverlaysFreshBuildStatus(t *testing.T) {
	// Bug 15 (CLI side) coverage. The CLI must surface fresh
	// build_phase / current_sprint values from build-status.json
	// even when the on-disk snapshot is stale, but it must do so
	// without writing back to the snapshot file (Bug 16).
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	// Plant a stale snapshot file (says sprint 1).
	snapPath := filepath.Join(dir, config.CopilotStateSnapshotFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(snapPath), 0o755))
	require.NoError(t, os.WriteFile(snapPath, []byte(`{
  "ts": "2026-04-07T20:00:00Z",
  "build_phase": "sprint",
  "build_pid": 11111,
  "current_sprint": 1,
  "total_sprints": 7
}
`), 0o644))

	// Plant a FRESH build-status.json (says audit, sprint 4).
	statusPath := filepath.Join(dir, config.BuildStatusFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(statusPath), 0o755))
	require.NoError(t, os.WriteFile(statusPath, []byte(`{
  "version": 1,
  "updated_at": "2026-04-07T21:00:00Z",
  "build": {
    "epic": "Test",
    "effort": "max",
    "engine": "claude",
    "mode": "software",
    "total_sprints": 7,
    "current_sprint": 4,
    "status": "running",
    "phase": "audit",
    "started_at": "2026-04-07T20:00:00Z"
  },
  "sprints": [
    {"number": 4, "name": "Brand Module", "status": "running"}
  ]
}
`), 0o644))

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	require.NoError(t, err)
	// Display must reflect the FRESH build-status.json values, not the
	// stale snapshot values.
	assert.Contains(t, out, "audit", "build_phase must reflect fresh build-status.json")
	assert.Contains(t, out, "4/7", "current_sprint must reflect fresh build-status.json")
	assert.Contains(t, out, "Brand Module", "sprint name must reflect fresh build-status.json")
	assert.NotContains(t, out, "1/7", "stale current_sprint=1 from snapshot must not leak through")
}

func TestCopilotStatusJSON(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir, "--json")
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(out), &parsed))
	assert.Contains(t, parsed, "manifest")
	assert.Contains(t, parsed, "event_total")
}

func TestCopilotAttachPrintOnly(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, err := runRootCmd(t, "copilot", "attach", "--project-dir", dir, "--print-only")
	require.NoError(t, err)
	assert.Contains(t, out, "claude --resume test-session-uuid")
}

func TestCopilotAttachNoManifest(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "attach", "--project-dir", dir, "--print-only")
	require.Error(t, err)
}

func TestCopilotAttachMissingSessionID(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "") // empty session ID

	_, err := runRootCmd(t, "copilot", "attach", "--project-dir", dir, "--print-only")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session ID")
}

func TestCopilotStopWritesFlag(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, err := runRootCmd(t, "copilot", "stop", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "stop requested")

	flagPath := filepath.Join(dir, config.CopilotStopRequestedFile)
	_, statErr := os.Stat(flagPath)
	assert.NoError(t, statErr, "stop flag file should exist")
}

func TestCopilotStopKeepCron(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	_, err := runRootCmd(t, "copilot", "stop", "--project-dir", dir, "--keep-cron")
	require.NoError(t, err)

	flagPath := filepath.Join(dir, config.CopilotStopRequestedFile)
	data, readErr := os.ReadFile(flagPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(data), "keep-cron")
}

func TestCopilotStopWithoutManifest(t *testing.T) {
	dir := t.TempDir()

	out, err := runRootCmd(t, "copilot", "stop", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "nothing to stop")
}

func TestCopilotRestartImmediateRebootstrap(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, err := runRootCmd(t, "copilot", "restart", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "Copilot restarted")
	assert.Contains(t, out, "Old session: test-session-uuid")
	assert.Contains(t, out, "New session:")

	// Manifest should have a different session ID now.
	m, mErr := copilot.ReadManifest(dir)
	require.NoError(t, mErr)
	require.NotNil(t, m)
	assert.NotEqual(t, "test-session-uuid", m.SessionID,
		"manifest must have a new session ID after restart")

	// Bootstrap prompt should exist with fresh content.
	promptPath := filepath.Join(dir, config.CopilotBootstrapPromptFile)
	data, pErr := os.ReadFile(promptPath)
	require.NoError(t, pErr)
	assert.Contains(t, string(data), "What is Fry?",
		"bootstrap prompt must contain the fry executive summary")

	// Events should record the restart.
	eventsPath := filepath.Join(dir, config.CopilotEventsTextFile)
	evData, eErr := os.ReadFile(eventsPath)
	require.NoError(t, eErr)
	assert.Contains(t, string(evData), "restarted via CLI")
}

func TestCopilotStartRefusesIfAlreadyActive(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "existing-session")

	_, err := runRootCmd(t, "copilot", "start", "--project-dir", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already active")
}

func TestCopilotStartRefusesWithoutBuildStatus(t *testing.T) {
	dir := t.TempDir()
	// No build-status.json exists.
	_, err := runRootCmd(t, "copilot", "start", "--project-dir", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build status")
}

func TestCopilotRestartWithoutManifest(t *testing.T) {
	dir := t.TempDir()

	out, err := runRootCmd(t, "copilot", "restart", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "nothing to restart")
}

func TestCopilotTailMissingLog(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "tail", "--follow=false", "--project-dir", dir)
	require.Error(t, err)
}

func TestCopilotTailReadsEventsTxt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, copilot.AppendEventsText(dir, "first event"))
	require.NoError(t, copilot.AppendEventsText(dir, "second event"))

	out, err := runRootCmd(t, "copilot", "tail", "--follow=false", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "first event")
	assert.Contains(t, out, "second event")
}

func TestCopilotEmitEventAppendsBoth(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "emit-event",
		"--project-dir", dir,
		"--type", "copilot_intervention_started",
		"--data", `{"id":"0001","kind":"fry_bug_fix"}`,
	)
	require.NoError(t, err)

	// Both streams should have the event
	copilotPath := filepath.Join(dir, config.CopilotEventsJSONLFile)
	copilotData, readErr := os.ReadFile(copilotPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(copilotData), "copilot_intervention_started")
	assert.Contains(t, string(copilotData), "0001")

	observerPath := filepath.Join(dir, config.ObserverEventsFile)
	observerData, readErr := os.ReadFile(observerPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(observerData), "copilot_intervention_started")
}

func TestCopilotEmitEventInvalidJSONData(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "emit-event",
		"--project-dir", dir,
		"--type", "copilot_test",
		"--data", "this is not json",
	)
	require.Error(t, err)
}

func TestCopilotEmitEventRequiresType(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "emit-event", "--project-dir", dir)
	require.Error(t, err)
}

func TestCopilotSummaryMissingNoCurrent(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	_, err := runRootCmd(t, "copilot", "summary", "--project-dir", dir)
	require.Error(t, err)
}

func TestCopilotSummaryMissingWithCurrentSynthesizes(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, err := runRootCmd(t, "copilot", "summary", "--project-dir", dir, "--current")
	require.NoError(t, err)
	assert.Contains(t, out, "Copilot In-Progress Summary")
	assert.Contains(t, out, "test-session-uuid")
}

func TestCopilotSummaryFinalSummaryFile(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "x")

	finalPath := filepath.Join(dir, config.CopilotFinalSummaryFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(finalPath), 0o755))
	require.NoError(t, os.WriteFile(finalPath, []byte("# Final\nThe build passed.\n"), 0o644))

	out, err := runRootCmd(t, "copilot", "summary", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "Final")
	assert.Contains(t, out, "passed")
}

func TestCopilotListInterventionsEmpty(t *testing.T) {
	dir := t.TempDir()

	out, err := runRootCmd(t, "copilot", "list-interventions", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "no interventions yet")
}

func TestCopilotListInterventionsWithEntries(t *testing.T) {
	dir := t.TempDir()
	intervDir := filepath.Join(dir, config.CopilotInterventionsDir)
	require.NoError(t, os.MkdirAll(intervDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(intervDir, "0001-fry-bug.md"), []byte("intervention"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(intervDir, "0002-artifact.md"), []byte("intervention"), 0o644))

	out, err := runRootCmd(t, "copilot", "list-interventions", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "0001-fry-bug.md")
	assert.Contains(t, out, "0002-artifact.md")
}

// ----- worktree redirect tests (Bug 9) -----

func TestResolveCopilotProjectDir_NoWorktree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No .fry/git-strategy.txt and no .fry-worktrees/ → return dir unchanged.
	got := resolveCopilotProjectDir(dir)
	assert.Equal(t, dir, got)
}

func TestResolveCopilotProjectDir_EmptyDir(t *testing.T) {
	t.Parallel()
	got := resolveCopilotProjectDir("")
	assert.Equal(t, "", got)
}

func TestResolveCopilotProjectDir_FallbackScansWorktrees(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Plant a fake worktree under .fry-worktrees/wt1/ with a .fry/copilot/ dir.
	wt := filepath.Join(dir, ".fry-worktrees", "wt1")
	require.NoError(t, os.MkdirAll(filepath.Join(wt, ".fry", "copilot"), 0o755))

	// No git strategy file. The fallback scan should find wt and return it.
	got := resolveCopilotProjectDir(dir)
	assert.Equal(t, wt, got)
}

func TestResolveCopilotProjectDir_FallbackPicksMostRecent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	wt1 := filepath.Join(dir, ".fry-worktrees", "older")
	wt2 := filepath.Join(dir, ".fry-worktrees", "newer")
	require.NoError(t, os.MkdirAll(filepath.Join(wt1, ".fry", "copilot"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(wt2, ".fry", "copilot"), 0o755))

	// Push wt1 mtime back so wt2 is unambiguously newer.
	pastTime := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(wt1, pastTime, pastTime))

	got := resolveCopilotProjectDir(dir)
	assert.Equal(t, wt2, got, "should pick the most recently modified worktree")
}

func TestResolveCopilotProjectDir_FallbackSkipsWorktreesWithoutCopilotDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// One worktree with no copilot/ dir, one with.
	wtBare := filepath.Join(dir, ".fry-worktrees", "bare")
	wtCopilot := filepath.Join(dir, ".fry-worktrees", "has-copilot")
	require.NoError(t, os.MkdirAll(filepath.Join(wtBare, ".fry"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(wtCopilot, ".fry", "copilot"), 0o755))

	got := resolveCopilotProjectDir(dir)
	assert.Equal(t, wtCopilot, got, "should skip worktrees without .fry/copilot/")
}

func TestResolveCopilotProjectDir_FallbackReturnsInputWhenNoWorktreesHaveCopilot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// .fry-worktrees/ exists but no copilot dirs inside.
	wt := filepath.Join(dir, ".fry-worktrees", "no-copilot")
	require.NoError(t, os.MkdirAll(filepath.Join(wt, ".fry"), 0o755))

	got := resolveCopilotProjectDir(dir)
	assert.Equal(t, dir, got, "should return input dir when no worktree has copilot state")
}

// ----- run flag plumbing tests -----
//
// These tests mutate the package-level runCopilot and runEngine flag
// variables. They cannot run in parallel with each other, so they
// acquire rootCmdTestMu (which serializes any test that touches the
// shared cobra command/flag state).

func TestResolveCopilotEngineDefaults(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()

	saved := runCopilot
	defer func() { runCopilot = saved }()

	runCopilot = ""
	assert.Equal(t, "claude", resolveCopilotEngine())

	runCopilot = "claude"
	assert.Equal(t, "claude", resolveCopilotEngine())

	runCopilot = "codex"
	assert.Equal(t, "codex", resolveCopilotEngine())

	runCopilot = "CLAUDE"
	assert.Equal(t, "claude", resolveCopilotEngine())
}

func TestResolveCopilotEngineUnknownFallsBackToClaude(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()

	saved := runCopilot
	defer func() { runCopilot = saved }()

	runCopilot = "ollama"
	assert.Equal(t, "claude", resolveCopilotEngine())
}

func TestResolveCopilotEngineAuto(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()

	savedCop := runCopilot
	savedEng := runEngine
	defer func() {
		runCopilot = savedCop
		runEngine = savedEng
	}()

	runCopilot = "auto"
	runEngine = "codex"
	assert.Equal(t, "codex", resolveCopilotEngine())

	runEngine = "claude"
	assert.Equal(t, "claude", resolveCopilotEngine())

	runEngine = "ollama"
	assert.Equal(t, "claude", resolveCopilotEngine(), "auto with ollama build engine should fall back to claude")
}

// ----- exit code helper -----

func TestCobraExitWithCode(t *testing.T) {
	t.Parallel()
	assert.NoError(t, cobraExitWithCode(0))
	err := cobraExitWithCode(2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exit code 2")
}

// Ensure copilot command is registered.
//
// These cobra-introspection tests must NOT call t.Parallel() because the
// shared rootCmd / runCmd / copilotCmd are not safe for concurrent access
// (cobra's Commands() lazily sorts the slice in place).
func TestCopilotCommandIsRegistered(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Use == "copilot" {
			found = true
			break
		}
	}
	assert.True(t, found, "copilot command should be registered on root")
}

// Ensure all expected subcommands are registered under copilot.
func TestCopilotSubcommandsRegistered(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()
	expected := []string{"status", "attach", "stop", "tail", "summary", "list-interventions", "emit-event"}
	have := make(map[string]bool)
	for _, c := range copilotCmd.Commands() {
		have[c.Use] = true
	}
	for _, name := range expected {
		assert.True(t, have[name], "copilot subcommand %q should be registered", name)
	}
}

// Ensure --copilot flag exists with the hybrid pattern (NoOptDefVal=claude).
func TestCopilotFlagHasNoOptDefVal(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()
	flag := runCmd.Flags().Lookup("copilot")
	require.NotNil(t, flag)
	assert.Equal(t, "claude", flag.NoOptDefVal)
}

func TestCopilotIntervalFlagDefault(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()
	flag := runCmd.Flags().Lookup("copilot-interval")
	require.NotNil(t, flag)
	assert.Equal(t, "10m", flag.DefValue)
}

func TestNoCopilotFlagExists(t *testing.T) {
	rootCmdTestMu.Lock()
	defer rootCmdTestMu.Unlock()
	assert.NotNil(t, runCmd.Flags().Lookup("no-copilot"))
}

// Ensure copilot CLI doesn't accidentally print the prompt template literal.
func TestCopilotStatusDoesNotLeakBracketLiteral(t *testing.T) {
	dir := t.TempDir()
	plantCopilotManifest(t, dir, "test-session-uuid")

	out, _ := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	assert.False(t, strings.Contains(out, "{{"), "rendered status should not contain template placeholders")
}
