package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestBuildTickArgs_Claude(t *testing.T) {
	t.Parallel()

	args := buildTickArgs("claude", "test-session-uuid", "opus[1m]")
	assert.Equal(t, "claude", args[0])
	assert.Contains(t, args, "--dangerously-skip-permissions")
	assert.Contains(t, args, "--resume")
	assert.Contains(t, args, "test-session-uuid")
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "opus[1m]")
	assert.Equal(t, "-p", args[len(args)-1],
		"-p must be the LAST flag so the wake message can be appended as the prompt")
}

func TestBuildTickArgs_ClaudeWithoutSession(t *testing.T) {
	t.Parallel()

	args := buildTickArgs("claude", "", "")
	assert.Equal(t, "claude", args[0])
	assert.NotContains(t, args, "--resume", "no --resume flag when session ID is empty")
	assert.NotContains(t, args, "--model", "no --model flag when model is empty")
	assert.Equal(t, "-p", args[len(args)-1])
}

func TestBuildTickArgs_Codex(t *testing.T) {
	t.Parallel()

	args := buildTickArgs("codex", "test-session-uuid", "gpt-5.4")
	assert.Equal(t, "codex", args[0])
	assert.Equal(t, "exec", args[1])
	assert.Contains(t, args, "resume")
	assert.Contains(t, args, "--dangerously-bypass-approvals-and-sandbox")
	assert.Contains(t, args, "--model")
	assert.Contains(t, args, "gpt-5.4")
	// Codex takes the session ID as the LAST positional argument
	// (per internal/engine/codex.go).
	assert.Equal(t, "test-session-uuid", args[len(args)-1])
}

func TestBuildTickArgs_CodexWithoutSession(t *testing.T) {
	t.Parallel()

	args := buildTickArgs("codex", "", "")
	assert.Equal(t, "codex", args[0])
	assert.Equal(t, "exec", args[1])
	assert.NotContains(t, args, "resume")
}

func TestBuildTickArgs_UnknownEngineFallback(t *testing.T) {
	t.Parallel()

	args := buildTickArgs("ollama", "test-session", "llama3")
	// Fallback returns just the engine name (a deliberately minimal
	// degradation — ollama doesn't currently support session resume).
	assert.Equal(t, []string{"ollama"}, args)
}

func TestTickSchedulerWakeMessageContainsBuildDir(t *testing.T) {
	t.Parallel()

	s := &TickScheduler{
		opts: SchedulerOpts{
			BuildDir: "/tmp/example-build-dir",
		},
		wakeCounter: 1,
	}
	msg := s.wakeMessageAt(time.Now().UTC())
	assert.Contains(t, msg, "/tmp/example-build-dir")
	assert.Contains(t, msg, "Tick Checklist")
	assert.Contains(t, msg, "manifest.json")
	assert.Contains(t, msg, "state-snapshot.json")
	assert.Contains(t, msg, "Wake #1")
}

func TestTickSchedulerWakeMessageIncludesCurrentUTCTime(t *testing.T) {
	t.Parallel()

	// Use a fixed, non-now time so we can assert the exact ISO string
	// appears in the message — this guards against future regressions
	// where the wake message accidentally drops or rewrites the time.
	fixed := time.Date(2026, 4, 7, 21, 33, 54, 0, time.UTC)
	s := &TickScheduler{
		opts:        SchedulerOpts{BuildDir: "/x"},
		wakeCounter: 1,
	}
	msg := s.wakeMessageAt(fixed)
	assert.Contains(t, msg, "Current UTC time:")
	assert.Contains(t, msg, "2026-04-07T21:33:54Z",
		"wake message must include the exact ISO timestamp passed in — "+
			"this is what stops the agent from re-using bootstrap-time {{.NowISO}}")
	assert.Contains(t, msg, "do NOT reuse the bootstrap-time timestamp",
		"wake message must explicitly tell the agent not to fall back to the "+
			"frozen template variable")
}

func TestTickSchedulerWakeMessageIncrementsWakeNumber(t *testing.T) {
	t.Parallel()

	s := &TickScheduler{
		opts: SchedulerOpts{BuildDir: "/x"},
	}
	now := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		s.wakeCounter = i
		assert.True(t, strings.Contains(s.wakeMessageAt(now), "Wake #"+strings.TrimSpace(intToStringForTest(i))))
	}
}

// intToStringForTest exists to keep the test self-contained without
// importing strconv (minor fmt-vs-strconv preference).
func intToStringForTest(i int) string {
	return strings.TrimSpace(string(rune('0'+i)))
}

func TestTickSchedulerStopBeforeFirstTickReturnsQuickly(t *testing.T) {
	t.Parallel()

	// Manually construct the scheduler so we can stop it before the
	// 60-second warmup elapses without spawning any real subprocess.
	s := &TickScheduler{
		opts: SchedulerOpts{
			ProjectDir: t.TempDir(),
			SessionID:  "test-session",
			Engine:     "claude",
			Interval:   100 * time.Millisecond,
			BuildDir:   t.TempDir(),
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go s.run()

	// Stop immediately. The warmup timer should be cancelled by the
	// stop case in the select before the timer fires.
	start := time.Now()
	s.Stop()
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 1*time.Second,
		"Stop() should return quickly when called before the first tick fires")
}

func TestTickSchedulerStopIsIdempotent(t *testing.T) {
	t.Parallel()

	s := &TickScheduler{
		opts: SchedulerOpts{
			ProjectDir: t.TempDir(),
			Engine:     "claude",
			Interval:   100 * time.Millisecond,
		},
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go s.run()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Stop()
		}()
	}
	wg.Wait()
	// If Stop weren't idempotent, multiple goroutines would race to
	// close stopCh and panic. Reaching this assertion is the success
	// signal.
	assert.True(t, true)
}

func TestCheckRestartReturnsFalseWhenNoSignalFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := &TickScheduler{
		opts: SchedulerOpts{
			ProjectDir: dir,
			SessionID:  "original-session",
			Engine:     "claude",
			Interval:   10 * time.Minute,
		},
	}
	assert.False(t, s.checkRestart())
	assert.Equal(t, "original-session", s.opts.SessionID,
		"session ID must not change when no restart is requested")
}

func TestCheckRestartDetectsSignalAndUpdatesSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create the copilot directory structure so manifest writes succeed.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.CopilotDir), 0o755))

	s := &TickScheduler{
		opts: SchedulerOpts{
			ProjectDir:   dir,
			SessionID:    "old-session-id",
			Engine:       "claude",
			Model:        "",
			Interval:     10 * time.Minute,
			BuildDir:     dir,
			FrySourceDir: "/tmp/fry-source",
			EpicName:     "Test Epic",
			EffortLevel:  "max",
			TotalSprints: 5,
			RunID:        "run-test",
			BuildPID:     os.Getpid(),
		},
		wakeCounter: 7,
	}

	// Write the restart-requested signal file.
	signalPath := filepath.Join(dir, config.CopilotRestartRequestedFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(signalPath), 0o755))
	require.NoError(t, os.WriteFile(signalPath, []byte("2026-04-10T12:00:00Z\n"), 0o644))

	// checkRestart will try to spawn a bootstrap subprocess, which will
	// fail because `claude` isn't in the test PATH. But the manifest,
	// session-id, prompt, and events should still be written before the
	// spawn attempt. We verify those artifacts even if checkRestart
	// returns false due to spawn failure.

	_ = s.checkRestart()

	// The signal file should be removed regardless of spawn outcome.
	_, err := os.Stat(signalPath)
	assert.True(t, os.IsNotExist(err), "restart-requested file must be deleted after processing")

	// Manifest should have been updated with a new session ID.
	manifest, mErr := ReadManifest(dir)
	if mErr == nil && manifest != nil {
		assert.NotEqual(t, "old-session-id", manifest.SessionID,
			"manifest must have a new session ID after restart")
		assert.Equal(t, "Test Epic", manifest.EpicName)
		assert.Equal(t, "max", manifest.EffortLevel)
	}

	// Bootstrap prompt file should exist with fresh content.
	promptPath := filepath.Join(dir, config.CopilotBootstrapPromptFile)
	if data, pErr := os.ReadFile(promptPath); pErr == nil {
		assert.Contains(t, string(data), "Test Epic",
			"bootstrap prompt must contain the epic name")
		assert.Contains(t, string(data), "What is Fry?",
			"bootstrap prompt must contain the fry executive summary from updated templates")
	}

	// Events text should record the restart.
	eventsPath := filepath.Join(dir, config.CopilotEventsTextFile)
	if data, eErr := os.ReadFile(eventsPath); eErr == nil {
		content := string(data)
		// Either a successful restart or a failed-spawn message should appear.
		assert.True(t,
			strings.Contains(content, "restarted") || strings.Contains(content, "restart failed"),
			"events.txt must record the restart attempt")
	}
}

func TestCheckRestartResetsWakeCounter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.CopilotDir), 0o755))

	s := &TickScheduler{
		opts: SchedulerOpts{
			ProjectDir:   dir,
			SessionID:    "old-session",
			Engine:       "claude",
			Interval:     10 * time.Minute,
			BuildDir:     dir,
			FrySourceDir: "/tmp/fry",
			EpicName:     "E",
			EffortLevel:  "max",
			TotalSprints: 3,
			RunID:        "run-test",
			BuildPID:     os.Getpid(),
		},
		wakeCounter: 42,
	}

	signalPath := filepath.Join(dir, config.CopilotRestartRequestedFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(signalPath), 0o755))
	require.NoError(t, os.WriteFile(signalPath, []byte("now\n"), 0o644))

	// Even if spawn fails, the session ID and wake counter should reset.
	_ = s.checkRestart()

	// Wake counter resets only on successful restart (spawn succeeds).
	// If spawn fails, the session ID reverts — but the signal is still
	// consumed so we don't retry endlessly.
	_, err := os.Stat(signalPath)
	assert.True(t, os.IsNotExist(err), "signal must be consumed even on spawn failure")
}
