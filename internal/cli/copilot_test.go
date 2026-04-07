package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/copilot"
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
	require.NoError(t, copilot.WriteCronIDFile(dir, "cron_test"))

	out, err := runRootCmd(t, "copilot", "status", "--project-dir", dir)
	require.NoError(t, err)
	assert.Contains(t, out, "ACTIVE")
	assert.Contains(t, out, "test-session-uuid")
	assert.Contains(t, out, "cron_test")
	assert.Contains(t, out, "claude")
	assert.Contains(t, out, "opus[1m]")
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

func TestCopilotTailMissingLog(t *testing.T) {
	dir := t.TempDir()

	_, err := runRootCmd(t, "copilot", "tail", "--project-dir", dir)
	require.Error(t, err)
}

func TestCopilotTailReadsEventsTxt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, copilot.AppendEventsText(dir, "first event"))
	require.NoError(t, copilot.AppendEventsText(dir, "second event"))

	out, err := runRootCmd(t, "copilot", "tail", "--project-dir", dir)
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
