package copilot

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
