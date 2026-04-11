package copilot

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func defaultBootstrapOpts(t *testing.T) BootstrapOpts {
	t.Helper()
	dir := t.TempDir()
	return BootstrapOpts{
		ProjectDir:   dir,
		FrySourceDir: makeFrySourceDir(t, t.TempDir()),
		Engine:       "claude",
		EpicName:     "Test Epic",
		EffortLevel:  "max",
		TotalSprints: 7,
		BuildPID:     os.Getpid(),
		Interval:     "10m",
		RunID:        "run-test",
		DryRun:       true, // never actually spawn in tests
		Stdout:       &bytes.Buffer{},
	}
}

func TestBootstrapDryRunWritesManifest(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	result, err := Bootstrap(opts)
	require.NoError(t, err)
	require.NotNil(t, result)

	manifest, err := ReadManifest(opts.ProjectDir)
	require.NoError(t, err)
	require.NotNil(t, manifest)
	assert.Equal(t, ModeDryRun, manifest.Mode)
	assert.Equal(t, opts.Engine, manifest.Engine)
	assert.Equal(t, opts.EpicName, manifest.EpicName)
	assert.Equal(t, opts.EffortLevel, manifest.EffortLevel)
	assert.Equal(t, opts.Interval, manifest.Interval)
	assert.Equal(t, os.Getpid(), manifest.BuildPID)
}

func TestBootstrapDryRunWritesPromptFile(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	promptPath := filepath.Join(opts.ProjectDir, config.CopilotBootstrapPromptFile)
	data, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "Copilot — The Build's Wingman")
	assert.Contains(t, string(data), opts.EpicName)
}

func TestBootstrapDryRunPrintsBanner(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	opts := defaultBootstrapOpts(t)
	opts.Stdout = &buf
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "Copilot started")
	assert.Contains(t, output, "fry copilot attach")
	assert.Contains(t, output, "fry copilot tail --follow")
	assert.Contains(t, output, "dry-run")
}

func TestBootstrapWarnsOnLeftoverCron(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	// Plant a leftover cron.id from a "previous build" that wasn't
	// cleanly torn down. No manifest exists.
	require.NoError(t, WriteCronIDFile(opts.ProjectDir, "leftover-cron-from-prior-run"))

	var buf bytes.Buffer
	opts.Stdout = &buf
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "fry: warning")
	assert.Contains(t, output, "leftover-cron-from-prior-run")
	assert.Contains(t, output, "self-prune")
}

func TestBootstrapNoWarningWhenNoLeftover(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	var buf bytes.Buffer
	opts.Stdout = &buf
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	assert.NotContains(t, buf.String(), "leftover copilot cron")
}

func TestBootstrapPassiveModeNoFrySource(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	opts.DryRun = false
	opts.FrySourceDir = "" // forces passive
	opts.Passive = true    // also forces passive (defensive)

	var buf bytes.Buffer
	opts.Stdout = &buf
	result, err := Bootstrap(opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, ModePassive, result.Manifest.Mode)
	assert.Contains(t, buf.String(), "passive")
}

func TestBootstrapInvalidIntervalRejected(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	opts.Interval = "10s" // below 1m floor
	_, err := Bootstrap(opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestBootstrapMissingProjectDir(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	opts.ProjectDir = ""
	_, err := Bootstrap(opts)
	require.Error(t, err)
}

func TestBootstrapMissingEngine(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	opts.Engine = ""
	_, err := Bootstrap(opts)
	require.Error(t, err)
}

func TestBootstrapEmitsBootstrapEvent(t *testing.T) {
	t.Parallel()

	opts := defaultBootstrapOpts(t)
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	events, err := ReadEvents(opts.ProjectDir)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	// First event should be the bootstrap event mirrored from observer.
	// Note: ReadEvents reads the copilot stream, not observer stream;
	// observer.EmitEvent is called separately. Verify events.txt has the
	// human-readable line either way.
	textPath := filepath.Join(opts.ProjectDir, config.CopilotEventsTextFile)
	data, err := os.ReadFile(textPath)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(data), "Copilot bootstrapped"))
}

func TestIntervalMinutes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		want  int
	}{
		{"10m", 10},
		{"1m", 1},
		{"30m", 30},
		{"1h", 60},
		{"5s", 1}, // floor at 1
		{"", config.CopilotDefaultIntervalMinutes},
		{"garbage", config.CopilotDefaultIntervalMinutes},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, intervalMinutes(tc.input))
		})
	}
}

func TestBuildEngineArgsClaude(t *testing.T) {
	t.Parallel()

	opts := BootstrapOpts{Engine: "claude", Model: "opus[1m]"}
	args := buildEngineArgs(opts, "test-session-id")
	assert.Equal(t, "claude", args[0])
	assert.Contains(t, args, "-p")
	assert.Contains(t, args, "--dangerously-skip-permissions")
	assert.Contains(t, args, "--session-id")
	assert.Contains(t, args, "test-session-id")
	assert.Contains(t, args, "--output-format")
	assert.Contains(t, args, "json")
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "opus[1m]")
}

func TestBuildEngineArgsClaudeNoSessionID(t *testing.T) {
	t.Parallel()

	opts := BootstrapOpts{Engine: "claude"}
	args := buildEngineArgs(opts, "")
	assert.NotContains(t, args, "--session-id")
}

func TestBuildEngineArgsCodex(t *testing.T) {
	t.Parallel()

	opts := BootstrapOpts{Engine: "codex"}
	args := buildEngineArgs(opts, "")
	assert.Equal(t, "codex", args[0])
	assert.Contains(t, args, "exec")
}

func TestReadBootstrapPIDMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	assert.Equal(t, 0, ReadBootstrapPID(dir))
}

func TestWriteAndReadBootstrapPID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, writeBootstrapPID(dir, 12345))
	assert.Equal(t, 12345, ReadBootstrapPID(dir))
}

func TestBootstrapCWDSetAtBootstrap(t *testing.T) {
	t.Parallel()
	opts := defaultBootstrapOpts(t)
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	manifest, err := ReadManifest(opts.ProjectDir)
	require.NoError(t, err)
	assert.Equal(t, opts.ProjectDir, manifest.BootstrapCWD,
		"BootstrapCWD should be set to ProjectDir at bootstrap time")
}

func TestPromoteCopilotPreservesBootstrapCWD(t *testing.T) {
	t.Parallel()
	opts := defaultBootstrapOpts(t)
	_, err := Bootstrap(opts)
	require.NoError(t, err)

	originalDir := opts.ProjectDir

	// Promote to a new directory (simulating worktree redirect).
	worktreeDir := t.TempDir()
	require.NoError(t, PromoteCopilot(PromoteOpts{
		ProjectDir:   worktreeDir,
		OldDir:       originalDir,
		EpicName:     "Promoted Epic",
		EffortLevel:  "max",
		TotalSprints: 5,
	}))

	// BootstrapCWD must still point to the original dir, NOT the worktree.
	manifest, err := ReadManifest(worktreeDir)
	require.NoError(t, err)
	assert.Equal(t, originalDir, manifest.BootstrapCWD,
		"BootstrapCWD must survive PromoteCopilot unchanged")
	assert.Equal(t, worktreeDir, manifest.BuildDir,
		"BuildDir should be updated to the worktree")

	// Original copilot dir should still exist (not moved, copied).
	_, err = os.Stat(filepath.Join(originalDir, config.CopilotDir))
	assert.NoError(t, err,
		"original copilot dir must be preserved at BootstrapCWD for cron/session access")

	// The original dir's manifest should also be updated with promoted values.
	origManifest, err := ReadManifest(originalDir)
	require.NoError(t, err)
	assert.Equal(t, "Promoted Epic", origManifest.EpicName,
		"original dir manifest must be updated with promoted epic")
	assert.Equal(t, 5, origManifest.TotalSprints,
		"original dir manifest must be updated with promoted sprint count")
	assert.Equal(t, originalDir, origManifest.BootstrapCWD,
		"BootstrapCWD must be preserved in original dir manifest")
}
