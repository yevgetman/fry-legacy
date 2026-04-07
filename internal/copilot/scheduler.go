package copilot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/observer"
)

// TickScheduler owns the periodic copilot wake loop. fry main starts the
// scheduler after the Bootstrap subprocess completes; the scheduler runs in
// a single goroutine for the lifetime of the build and stops on fry exit
// via the deferred Stop() call.
//
// Architectural rationale: Claude Code's CronCreate tool installs jobs that
// live only inside the parent claude session — when `claude -p` exits, the
// cron dies with it. The original copilot design relied on the bootstrap
// agent installing its own cron, but that cron died seconds after bootstrap
// because the bootstrap subprocess used `claude -p` (single-shot mode).
// fry-main-owned scheduling is the only mechanism that survives the
// bootstrap subprocess exit. Each tick spawns a fresh `claude --resume
// <session-id>` subprocess that resumes the conversation, runs one tick,
// and exits. The session ID stays stable across ticks; Claude Code
// auto-compacts the conversation as it grows.
type TickScheduler struct {
	opts SchedulerOpts

	stopCh chan struct{}
	doneCh chan struct{}
	once   sync.Once

	// inFlight is held by run() while a tick subprocess is executing.
	// Stop() takes the lock briefly to coordinate the kill on shutdown.
	inFlight sync.Mutex
	current  *exec.Cmd

	// wakeCounter monotonically increases per scheduler instance.
	// Protected by the run() goroutine; nothing else writes to it.
	wakeCounter int
}

// SchedulerOpts is the configuration for a TickScheduler. All fields are
// required.
type SchedulerOpts struct {
	ProjectDir string
	SessionID  string
	Engine     string
	Model      string
	Interval   time.Duration
	BuildDir   string // shown in the wake message; usually equal to ProjectDir
}

// StartTickScheduler launches the scheduler goroutine and returns
// immediately. The first tick fires after CopilotFirstTickWarmupSec
// seconds (Bug 6 fix); subsequent ticks fire every opts.Interval.
//
// Stop() is called by fry main's deferred cleanup on exit. After Stop
// returns, no further ticks will fire.
func StartTickScheduler(opts SchedulerOpts) *TickScheduler {
	s := &TickScheduler{
		opts:   opts,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	go s.run()
	return s
}

// Stop signals the scheduler to halt. Waits up to CopilotStopGraceSec
// seconds for an in-flight tick subprocess to finish; after that, kills
// the subprocess and returns. Safe to call multiple times.
func (s *TickScheduler) Stop() {
	s.once.Do(func() {
		close(s.stopCh)
		select {
		case <-s.doneCh:
			// run() exited cleanly
		case <-time.After(time.Duration(config.CopilotStopGraceSec) * time.Second):
			// In-flight tick is taking too long. Kill it.
			s.killCurrent()
			<-s.doneCh
		}
	})
}

// killCurrent terminates an in-flight tick subprocess if there is one.
// Used by Stop when the grace period expires.
func (s *TickScheduler) killCurrent() {
	s.inFlight.Lock()
	defer s.inFlight.Unlock()
	if s.current != nil && s.current.Process != nil {
		_ = s.current.Process.Kill()
	}
}

// run is the scheduler goroutine. Fires one immediate tick after the
// warmup, then ticks every interval until Stop() is called.
func (s *TickScheduler) run() {
	defer close(s.doneCh)

	// Warm-up: short delay so fry main can finish sprint-1 setup before
	// the first tick. Without this delay, the tick would immediately
	// race against fry main's docker setup / preflight.
	warmup := time.NewTimer(time.Duration(config.CopilotFirstTickWarmupSec) * time.Second)
	defer warmup.Stop()
	select {
	case <-s.stopCh:
		return
	case <-warmup.C:
	}
	s.tick()

	// Steady state: tick every interval until stopped.
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick executes one wake: spawns a fresh engine subprocess that resumes
// the copilot session, sends the wake message, waits for the subprocess
// to exit, and writes a per-tick result log to .fry/copilot/wakes/.
//
// Errors during a tick are non-fatal — fry main's job is to keep the
// build moving, not to fail the build because the copilot's tick
// subprocess hit an error. Per-tick errors are recorded in the wake's
// result.log and as observer events; the scheduler keeps running.
func (s *TickScheduler) tick() {
	s.wakeCounter++
	startedAt := time.Now().UTC()
	wakeID := startedAt.Format("20060102-150405")
	wakeDir := filepath.Join(s.opts.ProjectDir, config.CopilotWakesDir, wakeID)
	if err := os.MkdirAll(wakeDir, 0o755); err != nil {
		// Best-effort; if the dir can't be created we still try the tick
		// without a log file.
		_ = err
	}

	resultPath := filepath.Join(wakeDir, "result.log")
	resultFile, _ := os.OpenFile(resultPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if resultFile != nil {
		defer resultFile.Close()
	}

	// Refresh the state snapshot before the agent reads it. Without this
	// call the snapshot is only rewritten on observer event boundaries
	// (sprint_start, sprint_complete, audit_*, etc.), so during a long
	// in-progress sprint the agent would see stale build_phase /
	// current_sprint values. Force bypasses the 10s debounce — ticks
	// happen at minute-scale cadence so debounce is irrelevant here.
	_ = ForceWriteStateSnapshot(s.opts.ProjectDir)

	_ = EmitEvent(s.opts.ProjectDir, Event{
		Type: observer.EventCopilotWakeStart,
		Data: map[string]string{
			"wake_id":     wakeID,
			"wake_number": fmt.Sprintf("%d", s.wakeCounter),
			"session_id":  s.opts.SessionID,
		},
	})

	wakeMsg := s.wakeMessageAt(startedAt)
	ctx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(config.CopilotTickSubprocessTimeoutSec)*time.Second,
	)
	defer cancel()

	args := buildTickArgs(s.opts.Engine, s.opts.SessionID, s.opts.Model)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = s.opts.ProjectDir
	cmd.Stdin = nil // wake message is passed via -p arg, not stdin
	if resultFile != nil {
		cmd.Stdout = resultFile
		cmd.Stderr = resultFile
	}
	// New process group so killing the cmd doesn't take fry main with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Append the wake message as the final positional argument (-p value)
	// so the engine treats it as the prompt for this single-shot run.
	cmd.Args = append(cmd.Args, wakeMsg)

	s.inFlight.Lock()
	s.current = cmd
	s.inFlight.Unlock()

	runErr := cmd.Run()

	s.inFlight.Lock()
	s.current = nil
	s.inFlight.Unlock()

	finishedAt := time.Now().UTC()
	durationMs := finishedAt.Sub(startedAt).Milliseconds()

	verdict := "completed"
	if ctx.Err() == context.DeadlineExceeded {
		verdict = "timeout"
	} else if runErr != nil {
		verdict = "error"
	}

	endData := map[string]string{
		"wake_id":     wakeID,
		"wake_number": fmt.Sprintf("%d", s.wakeCounter),
		"session_id":  s.opts.SessionID,
		"duration_ms": fmt.Sprintf("%d", durationMs),
		"verdict":     verdict,
	}
	if runErr != nil {
		endData["error"] = runErr.Error()
	}
	_ = EmitEvent(s.opts.ProjectDir, Event{
		Type: observer.EventCopilotWakeEnd,
		Data: endData,
	})
}

// wakeMessageAt returns the prompt that fry main passes to each tick
// subprocess. The message is intentionally short — the full bootstrap
// context already lives in the resumed session.
//
// The message includes "Current UTC time: <ISO>" so the agent has a
// fresh ground-truth timestamp for events.txt and scratchpad entries.
// Without this, the agent would substitute the bootstrap-time
// {{.NowISO}} value (frozen at session start) into every wake's notes,
// producing entries that drift further from reality with every tick.
func (s *TickScheduler) wakeMessageAt(now time.Time) string {
	return fmt.Sprintf(
		"Wake up and run your tick procedure. Current UTC time: %s. "+
			"Re-read .fry/copilot/manifest.json and .fry/copilot/state-snapshot.json "+
			"for current config and build state, then follow the Tick Checklist in "+
			"your bootstrap prompt. Use the Current UTC time above for any events.txt, "+
			"scratchpad, or intervention timestamps you write — do NOT reuse the "+
			"bootstrap-time timestamp from the original prompt. Build dir: %s. Wake #%d.",
		now.UTC().Format(time.RFC3339), s.opts.BuildDir, s.wakeCounter)
}

// buildTickArgs builds the argv for a single tick subprocess. The wake
// message is appended by tick() as the final argument.
//
// claude:  claude --dangerously-skip-permissions --resume <id> [--model X] -p
// codex:   codex exec resume --dangerously-bypass-approvals-and-sandbox [--model X] <id>
//
// For codex, the session ID comes BEFORE -p / prompt arg per the codex
// CLI surface (see internal/engine/codex.go). Both engines accept a final
// positional prompt argument which tick() appends.
func buildTickArgs(engine, sessionID, model string) []string {
	switch engine {
	case "claude":
		args := []string{"claude", "--dangerously-skip-permissions"}
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, "-p")
		return args
	case "codex":
		args := []string{"codex", "exec"}
		if sessionID != "" {
			args = append(args, "resume")
		}
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		if model != "" {
			args = append(args, "--model", model)
		}
		if sessionID != "" {
			args = append(args, sessionID)
		}
		return args
	default:
		return []string{engine}
	}
}
