package copilot

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/observer"
)

// BootstrapOpts is the configuration for spawning a copilot bootstrap
// subprocess. All fields are required unless documented otherwise.
type BootstrapOpts struct {
	ProjectDir   string // build directory (where .fry/copilot/ lives)
	FrySourceDir string // fry source dir (may be "" → mode=passive)
	Engine       string // copilot engine name: "claude" or "codex"
	Model        string // optional model override
	EpicName     string
	EffortLevel  string
	TotalSprints int
	BuildPID     int       // PID of the running fry main process
	Interval     string    // human form, e.g. "10m"
	RunID        string    // build run ID, used in commit message provenance
	Passive      bool      // force passive mode (no interventions)
	DryRun       bool      // skip subprocess spawn
	Stdout       io.Writer // optional; defaults to os.Stdout for the startup banner
}

// BootstrapResult is the outcome of a successful (or dry-run) bootstrap.
//
// Scheduler is the fry-main-owned tick scheduler started after the
// bootstrap subprocess completes. Callers MUST call Scheduler.Stop()
// during their cleanup (typically via a deferred call) — otherwise the
// goroutine will keep ticking and keep spawning subprocesses for the
// lifetime of the fry main process. In dry-run / passive / error
// modes, Scheduler is nil.
type BootstrapResult struct {
	Manifest         *Manifest
	BootstrapPID     int // PID of the spawned subprocess (0 in dry-run/passive). Informational only — claude -p exits after running the bootstrap prompt.
	BootstrapLogPath string
	BannerLines      []string // the lines that were printed to stdout
	Scheduler        *TickScheduler
}

// Bootstrap spawns the copilot subprocess (or skips spawning in dry-run/
// passive mode), captures the session ID, writes the manifest, and prints
// the startup banner. Returns once the subprocess has been launched and
// the session ID is captured (or after the dry-run path completes).
//
// In active mode the subprocess runs in its own process group (Setpgid)
// so that signals to the parent fry process do not propagate to the
// copilot. The subprocess's stdout/stderr are redirected to
// .fry/copilot/bootstrap.log.
func Bootstrap(opts BootstrapOpts) (*BootstrapResult, error) {
	if err := validateBootstrapOpts(opts); err != nil {
		return nil, err
	}

	// Detect any leftover copilot state from a previous build that wasn't
	// cleanly torn down. fry cannot cancel external crons; the orphan must
	// self-prune via Tick Checklist step 0 in templates/copilot/bootstrap.md.
	// We surface a warning here so users know why a phantom session may
	// briefly continue ticking after `fry clean`.
	if warning := LeftoverCronWarning(opts.ProjectDir); warning != "" {
		stdout := opts.Stdout
		if stdout == nil {
			stdout = os.Stdout
		}
		fmt.Fprintf(stdout, "fry: warning: %s\n", warning)
	}

	// Determine mode and force-passive resolution.
	mode := ModeActive
	if opts.DryRun {
		mode = ModeDryRun
	} else if opts.Passive || opts.FrySourceDir == "" {
		mode = ModePassive
	}

	// Engine capability probe + session ID capture.
	probe := EngineProbeResult{Engine: opts.Engine}
	if opts.Engine == "claude" {
		probe = ProbeClaudeCapabilities()
	}

	sessionID, capMech, err := CaptureSessionID(probe)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: capture session id: %w", err)
	}

	// Write the initial manifest BEFORE spawning so subcommands can find
	// the run state even if the spawn fails.
	manifest := &Manifest{
		SessionID:                 sessionID,
		BuildPID:                  opts.BuildPID,
		BuildDir:                  opts.ProjectDir,
		FrySourceDir:              opts.FrySourceDir,
		Engine:                    opts.Engine,
		Model:                     opts.Model,
		StartedAt:                 time.Now().UTC().Format(time.RFC3339),
		Interval:                  opts.Interval,
		EpicName:                  opts.EpicName,
		EffortLevel:               opts.EffortLevel,
		MaxInterventionsPerClass:  config.CopilotMaxInterventionsPerClass,
		StopOnBuildComplete:       true,
		Mode:                      mode,
		EngineCapabilities:        EngineCapabilities{SessionIDFlag: probe.SessionIDFlag, CronCreate: true, RemoteTrigger: false},
		SessionIDCaptureMechanism: capMech,
	}
	if err := WriteManifest(opts.ProjectDir, manifest); err != nil {
		return nil, fmt.Errorf("bootstrap: write initial manifest: %w", err)
	}

	result := &BootstrapResult{Manifest: manifest}

	// Render bootstrap prompt to disk regardless of mode — useful for
	// later inspection and for the agent itself to re-read.
	bootstrapData := BootstrapData{
		BuildDir:        opts.ProjectDir,
		FrySourceDir:    opts.FrySourceDir,
		Engine:          opts.Engine,
		EpicName:        opts.EpicName,
		EffortLevel:     opts.EffortLevel,
		TotalSprints:    opts.TotalSprints,
		StartedAt:       manifest.StartedAt,
		Interval:        opts.Interval,
		IntervalMinutes: intervalMinutes(opts.Interval),
		SessionID:       sessionID,
		RunID:           opts.RunID,
	}
	if _, err := WriteBootstrapPromptFile(opts.ProjectDir, bootstrapData); err != nil {
		return nil, fmt.Errorf("bootstrap: write prompt file: %w", err)
	}

	// Initial state-snapshot write so the copilot's first wake has data.
	_ = ForceWriteStateSnapshot(opts.ProjectDir)

	// Bootstrap event for both the copilot stream AND the canonical
	// observer stream (so `fry monitor` and `fry events` see it).
	_ = EmitEvent(opts.ProjectDir, Event{
		Type: observer.EventCopilotBootstrap,
		Data: map[string]string{
			"session_id":                  sessionID,
			"engine":                      opts.Engine,
			"mode":                        string(mode),
			"interval":                    opts.Interval,
			"session_id_capture":          string(capMech),
			"fry_source_dir":              opts.FrySourceDir,
			"engine_session_id_supported": strconv.FormatBool(probe.SessionIDFlag),
		},
	})
	_ = AppendEventsText(opts.ProjectDir, fmt.Sprintf("%s  Copilot bootstrapped (session %s, mode %s, interval %s).",
		manifest.StartedAt, sessionID, mode, opts.Interval))

	// Dry-run / passive: skip subprocess spawn entirely.
	if mode == ModeDryRun {
		result.BannerLines = writeBanner(opts.Stdout, manifest, "(dry-run, no subprocess spawned)")
		return result, nil
	}
	if mode == ModePassive {
		result.BannerLines = writeBanner(opts.Stdout, manifest, "(passive mode — no interventions)")
		return result, nil
	}

	// Active mode: spawn the bootstrap subprocess detached from the
	// parent fry process.
	cmd, logPath, err := spawnBootstrapSubprocess(opts, sessionID)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: spawn subprocess: %w", err)
	}
	result.BootstrapPID = cmd.Process.Pid
	result.BootstrapLogPath = logPath

	// Persist the PID file so `fry copilot stop` can find it.
	if err := writeBootstrapPID(opts.ProjectDir, cmd.Process.Pid); err != nil {
		return nil, fmt.Errorf("bootstrap: write pid file: %w", err)
	}

	// If session ID was not pre-specified, fall back to parsing stdout.
	// We do not block forever — the subprocess is detached and may not
	// surface a session ID for several seconds. The startup banner shows
	// "(pending)" and the actual ID will be written by the agent itself
	// once it's known.
	// TODO(v1.1): wait briefly and update manifest if found.

	_ = WriteSessionIDFile(opts.ProjectDir, sessionID)

	// Start the fry-main-owned tick scheduler. Each tick spawns a fresh
	// `claude --resume <session-id> -p "<wake msg>"` subprocess that runs
	// one tick and exits. The scheduler runs for the lifetime of fry
	// main and is stopped by the caller via Scheduler.Stop() in their
	// deferred cleanup. This replaces the previous CronCreate-based
	// design which died when the bootstrap subprocess exited.
	if sessionID != "" {
		intervalDur, _ := time.ParseDuration(opts.Interval)
		if intervalDur <= 0 {
			intervalDur = time.Duration(config.CopilotDefaultIntervalMinutes) * time.Minute
		}
		result.Scheduler = StartTickScheduler(SchedulerOpts{
			ProjectDir: opts.ProjectDir,
			SessionID:  sessionID,
			Engine:     opts.Engine,
			Model:      opts.Model,
			Interval:   intervalDur,
			BuildDir:   opts.ProjectDir,
		})
	}

	result.BannerLines = writeBanner(opts.Stdout, manifest, "")

	return result, nil
}

// validateBootstrapOpts checks the required fields and returns a clear
// error if any are missing or out of range.
func validateBootstrapOpts(opts BootstrapOpts) error {
	if opts.ProjectDir == "" {
		return fmt.Errorf("bootstrap: ProjectDir is required")
	}
	if opts.Engine == "" {
		return fmt.Errorf("bootstrap: Engine is required")
	}
	if opts.Interval == "" {
		return fmt.Errorf("bootstrap: Interval is required")
	}
	d, err := time.ParseDuration(opts.Interval)
	if err != nil {
		return fmt.Errorf("bootstrap: invalid Interval %q: %w", opts.Interval, err)
	}
	minDur := time.Duration(config.CopilotMinIntervalSeconds) * time.Second
	maxDur := time.Duration(config.CopilotMaxIntervalSeconds) * time.Second
	if d < minDur || d > maxDur {
		return fmt.Errorf("bootstrap: Interval %s out of range [%s, %s]", d, minDur, maxDur)
	}
	return nil
}

func intervalMinutes(interval string) int {
	d, err := time.ParseDuration(interval)
	if err != nil {
		return config.CopilotDefaultIntervalMinutes
	}
	mins := int(d.Minutes())
	if mins < 1 {
		mins = 1
	}
	return mins
}

// spawnBootstrapSubprocess fork-execs the engine CLI with the bootstrap
// prompt as input. The subprocess runs in its own process group so it
// outlives the parent fry process if necessary.
func spawnBootstrapSubprocess(opts BootstrapOpts, sessionID string) (*exec.Cmd, string, error) {
	logPath := filepath.Join(opts.ProjectDir, config.CopilotBootstrapLogFile)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, "", fmt.Errorf("create bootstrap log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open bootstrap log: %w", err)
	}

	args := buildEngineArgs(opts, sessionID)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = opts.ProjectDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Pipe the bootstrap prompt content to the subprocess stdin.
	promptPath := filepath.Join(opts.ProjectDir, config.CopilotBootstrapPromptFile)
	promptFile, err := os.Open(promptPath)
	if err != nil {
		_ = logFile.Close()
		return nil, "", fmt.Errorf("open bootstrap prompt: %w", err)
	}
	cmd.Stdin = promptFile

	// Detach: own process group so signals to fry don't propagate.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		_ = promptFile.Close()
		return nil, "", fmt.Errorf("start bootstrap subprocess: %w", err)
	}

	// Reap the subprocess in a background goroutine so we don't accumulate
	// zombies if it exits while fry is still running. Wait() blocks until
	// the process is done; we don't care about the exit code here because
	// the copilot session manages its own lifecycle.
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		_ = promptFile.Close()
	}()

	return cmd, logPath, nil
}

// buildEngineArgs constructs the argv for the bootstrap subprocess based
// on engine and capabilities.
func buildEngineArgs(opts BootstrapOpts, sessionID string) []string {
	switch opts.Engine {
	case "claude":
		args := []string{"claude", "-p", "--dangerously-skip-permissions"}
		if sessionID != "" {
			args = append(args, "--session-id", sessionID)
		}
		args = append(args, "--output-format", "json")
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		}
		return args
	case "codex":
		// Codex copilot uses an in-process tick scheduler instead of
		// CronCreate. The bootstrap subprocess just runs the bootstrap
		// prompt; subsequent ticks are spawned by fry's main process.
		args := []string{"codex", "exec"}
		if opts.Model != "" {
			args = append(args, "--model", opts.Model)
		}
		return args
	default:
		// Fallback: try to invoke as a plain command.
		return []string{opts.Engine}
	}
}

// writeBootstrapPID writes the spawned subprocess PID to
// .fry/copilot/bootstrap.pid so cleanup and `fry copilot stop` can find it.
func writeBootstrapPID(projectDir string, pid int) error {
	path := filepath.Join(projectDir, config.CopilotBootstrapPIDFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

// ReadBootstrapPID reads the bootstrap subprocess PID, or 0 if missing.
func ReadBootstrapPID(projectDir string) int {
	path := filepath.Join(projectDir, config.CopilotBootstrapPIDFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// writeBanner prints the startup banner to w (default os.Stdout). Returns
// the lines that were written so callers can capture them.
func writeBanner(w io.Writer, m *Manifest, suffix string) []string {
	if w == nil {
		w = os.Stdout
	}
	idDisplay := m.SessionID
	if idDisplay == "" {
		idDisplay = "(pending — see bootstrap.log)"
	}

	lines := []string{
		fmt.Sprintf("✓ Copilot started (%s%s, every %s)", m.Engine, modelSuffix(m.Model), m.Interval),
		fmt.Sprintf("  Session ID:  %s", idDisplay),
		"  Attach:      fry copilot attach",
	}
	if m.SessionID != "" {
		lines = append(lines, fmt.Sprintf("               %s --resume %s", m.Engine, m.SessionID))
	}
	lines = append(lines,
		"  Events:      fry copilot tail --follow",
		"  Status:      fry copilot status",
	)
	if suffix != "" {
		lines = append(lines, "  "+suffix)
	}

	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
	return lines
}

func modelSuffix(model string) string {
	if model == "" {
		return ""
	}
	return " " + model
}
