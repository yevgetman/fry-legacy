package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yevgetman/fry/internal/agent"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/copilot"
	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/observer"
)

// copilotCmd is the parent for all `fry copilot ...` subcommands.
var copilotCmd = &cobra.Command{
	Use:   "copilot",
	Short: "Inspect and control the build copilot session",
	Long: `Manage the parallel agent session that monitors a fry build.

Use ` + "`fry run --copilot`" + ` to start a copilot. The subcommands here let
you inspect, attach to, or stop a running copilot session.`,
}

// ----- status -----

var copilotStatusCmd = &cobra.Command{
	Use:           "status",
	Short:         "Print the current copilot session status",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		jsonOut, _ := cmd.Flags().GetBool("json")

		manifest, err := copilot.ReadManifest(dir)
		if err != nil {
			return fmt.Errorf("read copilot manifest: %w", err)
		}
		if manifest == nil {
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"status": "absent"})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Copilot status: ABSENT (no manifest found)")
			return cobraExitWithCode(1)
		}

		// Read the on-disk snapshot for fields that only fry main writes
		// (DeferredFailuresCount, RecentEventsTail, etc.). Then overlay
		// fresh build-status.json data on top so the user sees current
		// build_phase / current_sprint without depending on fry main
		// having recently rewritten the snapshot.
		//
		// IMPORTANT: this CLI must NEVER call WriteStateSnapshot or
		// ForceWriteStateSnapshot. buildSnapshot() in the copilot package
		// uses os.Getpid() to populate BuildPID — calling it from a CLI
		// helper would clobber the field with the CLI's ephemeral PID,
		// then exit, leaving the snapshot file with a dead PID and
		// build_pid_alive=true (the CLI was alive at write time). The
		// next copilot agent wake would then mis-read the build PID.
		// This is the regression that produced Bug 16. Read-only here.
		snapshot, _ := copilot.ReadStateSnapshot(dir)
		snapshot = overlayFreshBuildStatus(dir, snapshot)
		busy := copilot.IsBusy(dir)
		cronID := copilot.ReadCronIDFile(dir)
		events, _ := copilot.ReadEvents(dir)
		counts := copilot.CountEventsByType(events)
		hasCron := cronID != ""
		buildAlive := manifest.BuildPID > 0 && processAlive(manifest.BuildPID)

		if jsonOut {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
				"status":       statusVerbFromState(buildAlive, hasCron),
				"manifest":     manifest,
				"snapshot":     snapshot,
				"busy":         busy,
				"build_alive":  buildAlive,
				"has_cron":     hasCron,
				"event_counts": counts,
				"event_total":  len(events),
			})
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Copilot status: %s\n", strings.ToUpper(statusVerbFromState(buildAlive, hasCron)))
		fmt.Fprintf(out, "  Engine:              %s", manifest.Engine)
		if manifest.Model != "" {
			fmt.Fprintf(out, " (%s)", manifest.Model)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "  Mode:                %s\n", manifest.Mode)
		fmt.Fprintf(out, "  Session ID:          %s\n", displayValue(manifest.SessionID))
		fmt.Fprintf(out, "  Started:             %s\n", manifest.StartedAt)
		fmt.Fprintf(out, "  Interval:            %s\n", manifest.Interval)
		fmt.Fprintf(out, "  Cron ID:             %s\n", displayValue(cronID))
		fmt.Fprintf(out, "  Build PID:           %d", manifest.BuildPID)
		if manifest.BuildPID > 0 {
			if buildAlive {
				fmt.Fprint(out, " (alive)")
			} else {
				fmt.Fprint(out, " (dead)")
			}
		}
		fmt.Fprintln(out)
		if snapshot != nil {
			fmt.Fprintf(out, "  Build phase:         %s\n", displayValue(snapshot.BuildPhase))
			if snapshot.CurrentSprint > 0 {
				fmt.Fprintf(out, "  Current sprint:      %d/%d", snapshot.CurrentSprint, snapshot.TotalSprints)
				if snapshot.CurrentSprintName != "" {
					fmt.Fprintf(out, " (%s)", snapshot.CurrentSprintName)
				}
				fmt.Fprintln(out)
			}
		}
		fmt.Fprintf(out, "  Total events:        %d\n", len(events))
		if interventions := counts[observer.EventCopilotInterventionDone]; interventions > 0 {
			fmt.Fprintf(out, "  Interventions:       %d\n", interventions)
		}
		if commits := counts[observer.EventCopilotGitPush]; commits > 0 {
			fmt.Fprintf(out, "  Commits pushed:      %d\n", commits)
		}
		fmt.Fprintf(out, "  Busy right now:      %s\n", boolYesNo(busy))
		fmt.Fprintln(out, "  Attach command:      fry copilot attach")
		if manifest.SessionID != "" {
			sessionDir := manifest.BootstrapCWD
			if sessionDir == "" {
				sessionDir = dir
			}
			cwd, _ := os.Getwd()
			if cwd != sessionDir {
				fmt.Fprintf(out, "                       cd %s && %s --resume %s\n", sessionDir, manifest.Engine, manifest.SessionID)
			} else {
				fmt.Fprintf(out, "                       %s --resume %s\n", manifest.Engine, manifest.SessionID)
			}
		}

		// Exit non-zero on stale state for scripting: the copilot once
		// had a cron installed but it's gone now, AND the build is dead.
		// This means the copilot has fully exited. If the cron is still
		// active, the copilot is alive (possibly recovering a crashed build).
		if !hasCron && !buildAlive && len(events) > 0 {
			return cobraExitWithCode(2)
		}
		return nil
	},
}

// ----- attach -----

var copilotAttachCmd = &cobra.Command{
	Use:           "attach",
	Short:         "Attach a terminal to the running copilot session (or print the attach command)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		printOnly, _ := cmd.Flags().GetBool("print-only")

		manifest, err := copilot.ReadManifest(dir)
		if err != nil {
			return fmt.Errorf("read copilot manifest: %w", err)
		}
		if manifest == nil {
			return fmt.Errorf("no copilot manifest found in %s — start one with `fry run --copilot`", dir)
		}
		if manifest.SessionID == "" {
			return fmt.Errorf("copilot session ID is not yet captured — check %s for status", filepath.Join(dir, config.CopilotBootstrapLogFile))
		}

		attachCmd := []string{manifest.Engine, "--resume", manifest.SessionID}

		if printOnly {
			fmt.Fprintln(cmd.OutOrStdout(), strings.Join(attachCmd, " "))
			return nil
		}

		// Check busy state — refuse to exec if mid-tick.
		if copilot.IsBusy(dir) {
			fmt.Fprintln(cmd.ErrOrStderr(), "copilot is actively running a tick — wait ~30s or retry with --print-only")
			return cobraExitWithCode(3)
		}

		execBin, lookErr := exec.LookPath(attachCmd[0])
		if lookErr != nil {
			return fmt.Errorf("locate %s: %w", attachCmd[0], lookErr)
		}
		// Chdir to the directory where the bootstrap subprocess was
		// originally spawned. Claude Code stores sessions under a
		// project-hash derived from CWD, so --resume must run from the
		// same directory the session was created in. BootstrapCWD is
		// set once at bootstrap and never changed — even when
		// PromoteCopilot moves artifacts to a worktree. Fall back to
		// the resolved copilot dir for manifests written before
		// BootstrapCWD existed.
		sessionDir := manifest.BootstrapCWD
		if sessionDir == "" {
			sessionDir = dir
		}
		if err := os.Chdir(sessionDir); err != nil {
			return fmt.Errorf("chdir to %s: %w", sessionDir, err)
		}
		// Replace the current process with the engine CLI.
		return syscall.Exec(execBin, attachCmd, os.Environ())
	},
}

// ----- start -----

var copilotStartCmd = &cobra.Command{
	Use:           "start",
	Short:         "Start a copilot mid-build (if one wasn't started with --copilot)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)

		// Refuse if a copilot is already active.
		if existing, _ := copilot.ReadManifest(dir); existing != nil && existing.SessionID != "" {
			return fmt.Errorf("copilot already active (session %s) — use `fry copilot restart` to replace it", existing.SessionID)
		}

		// Read the current build state to get context.
		status, err := agent.ReadBuildStatus(dir)
		if err != nil || status == nil {
			return fmt.Errorf("read build status: no build-status.json found — is a build running in this directory?")
		}
		if status.Build.Status != "running" {
			return fmt.Errorf("build is not running (status: %s) — copilot requires an active build", status.Build.Status)
		}

		copilotEngine, _ := cmd.Flags().GetString("engine")
		if copilotEngine == "" {
			copilotEngine = "claude"
		}
		interval, _ := cmd.Flags().GetString("interval")
		if interval == "" {
			interval = "10m"
		}
		model, _ := cmd.Flags().GetString("model")

		frySrcDir := copilot.DiscoverFrySourceDir("")

		// Clear any stale stop-requested file so the supervisor doesn't
		// immediately refuse to start the scheduler.
		_ = os.Remove(filepath.Join(dir, config.CopilotStopRequestedFile))

		// Find the build PID. Try build-status first, fall back to the
		// lock file PID.
		buildPID := 0
		if lockPID := readLockPID(dir); lockPID > 0 && processAlive(lockPID) {
			buildPID = lockPID
		}

		runID := ""
		if status.Run != nil {
			runID = status.Run.RunID
		}

		bootstrapResult, bootErr := copilot.Bootstrap(copilot.BootstrapOpts{
			ProjectDir:   dir,
			FrySourceDir: frySrcDir,
			Engine:       copilotEngine,
			Model:        model,
			EpicName:     status.Build.Epic,
			EffortLevel:  status.Build.Effort,
			TotalSprints: status.Build.TotalSprints,
			BuildPID:     buildPID,
			Interval:     interval,
			RunID:        runID,
			Stdout:       cmd.OutOrStdout(),
		})
		if bootErr != nil {
			return fmt.Errorf("bootstrap copilot: %w", bootErr)
		}
		_ = bootstrapResult // copilot is independent — it installs its own cron

		fmt.Fprintln(cmd.OutOrStdout(), "Copilot started. It will install its own cron and begin ticking shortly.")
		return nil
	},
}

func init() {
	copilotStartCmd.Flags().String("engine", "claude", "Copilot engine")
	copilotStartCmd.Flags().String("interval", "10m", "Wake interval (1m–1h)")
	copilotStartCmd.Flags().String("model", "", "Override copilot agent model")
}

// readLockPID reads the build lock file PID if present.
func readLockPID(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, ".fry/.fry.lock"))
	if err != nil {
		return 0
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
		return 0
	}
	return pid
}

// ----- stop -----

var copilotStopCmd = &cobra.Command{
	Use:           "stop",
	Short:         "Request the copilot to exit cleanly (does NOT stop the build)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		keepCron, _ := cmd.Flags().GetBool("keep-cron")

		manifest, err := copilot.ReadManifest(dir)
		if err != nil {
			return fmt.Errorf("read copilot manifest: %w", err)
		}
		if manifest == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "no copilot manifest found — nothing to stop")
			return nil
		}

		// Write the stop-requested flag file. Next wake reads it and self-terminates.
		flagPath := filepath.Join(dir, config.CopilotStopRequestedFile)
		if err := os.MkdirAll(filepath.Dir(flagPath), 0o755); err != nil {
			return fmt.Errorf("create copilot dir: %w", err)
		}
		body := []byte(time.Now().UTC().Format(time.RFC3339) + "\n")
		if keepCron {
			body = append(body, []byte("keep-cron\n")...)
		}
		if err := os.WriteFile(flagPath, body, 0o644); err != nil {
			return fmt.Errorf("write stop flag: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Copilot stop requested. Next wake will exit cleanly.")
		if !keepCron {
			fmt.Fprintln(cmd.OutOrStdout(), "Cron will be deleted by the copilot's final wake.")
		}
		return nil
	},
}

// ----- restart -----

var copilotRestartCmd = &cobra.Command{
	Use:           "restart",
	Short:         "Restart the copilot session with fresh templates (does NOT stop the build)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)

		manifest, err := copilot.ReadManifest(dir)
		if err != nil {
			return fmt.Errorf("read copilot manifest: %w", err)
		}
		if manifest == nil {
			fmt.Fprintln(cmd.OutOrStdout(), "no copilot manifest found — nothing to restart")
			return nil
		}

		oldSession := manifest.SessionID

		// Immediate re-bootstrap: generate new session, write manifest,
		// render prompt, spawn subprocess. The new session installs its
		// own cron; the old session's cron self-prunes via orphan check.
		probe := copilot.EngineProbeResult{Engine: manifest.Engine}
		if manifest.Engine == "claude" {
			probe = copilot.ProbeClaudeCapabilities()
		}
		newSessionID, capMech, captureErr := copilot.CaptureSessionID(probe)
		if captureErr != nil || newSessionID == "" {
			return fmt.Errorf("capture new session ID: %w", captureErr)
		}

		startedAt := time.Now().UTC().Format(time.RFC3339)
		newManifest := &copilot.Manifest{
			SessionID:                 newSessionID,
			BuildPID:                  manifest.BuildPID,
			BuildDir:                  manifest.BuildDir,
			BootstrapCWD:              dir,
			FrySourceDir:              manifest.FrySourceDir,
			Engine:                    manifest.Engine,
			Model:                     manifest.Model,
			StartedAt:                 startedAt,
			Interval:                  manifest.Interval,
			EpicName:                  manifest.EpicName,
			EffortLevel:               manifest.EffortLevel,
			TotalSprints:              manifest.TotalSprints,
			RunID:                     manifest.RunID,
			MaxInterventionsPerClass:  manifest.MaxInterventionsPerClass,
			StopOnBuildComplete:       true,
			Mode:                      copilot.ModeActive,
			EngineCapabilities:        copilot.EngineCapabilities{SessionIDFlag: probe.SessionIDFlag, CronCreate: true},
			SessionIDCaptureMechanism: capMech,
		}
		if err := copilot.WriteManifest(dir, newManifest); err != nil {
			return fmt.Errorf("write manifest: %w", err)
		}
		_ = copilot.WriteSessionIDFile(dir, newSessionID)

		intervalMin := 10
		if d, pErr := time.ParseDuration(manifest.Interval); pErr == nil && d.Minutes() >= 1 {
			intervalMin = int(d.Minutes())
		}
		bootstrapData := copilot.BootstrapData{
			BuildDir:        manifest.BuildDir,
			FrySourceDir:    manifest.FrySourceDir,
			Engine:          manifest.Engine,
			EpicName:        manifest.EpicName,
			EffortLevel:     manifest.EffortLevel,
			TotalSprints:    manifest.TotalSprints,
			StartedAt:       startedAt,
			Interval:        manifest.Interval,
			IntervalMinutes: intervalMin,
			SessionID:       newSessionID,
			RunID:           manifest.RunID,
		}
		if _, promptErr := copilot.WriteBootstrapPromptFile(dir, bootstrapData); promptErr != nil {
			return fmt.Errorf("write bootstrap prompt: %w", promptErr)
		}

		_ = copilot.ForceWriteStateSnapshot(dir)

		// Spawn the bootstrap subprocess with the new session.
		spawnOpts := copilot.BootstrapOpts{
			ProjectDir:   dir,
			FrySourceDir: manifest.FrySourceDir,
			Engine:       manifest.Engine,
			Model:        manifest.Model,
			EpicName:     manifest.EpicName,
			EffortLevel:  manifest.EffortLevel,
			TotalSprints: manifest.TotalSprints,
			BuildPID:     manifest.BuildPID,
			Interval:     manifest.Interval,
			RunID:        manifest.RunID,
		}
		if spawnErr := copilot.SpawnBootstrapSubprocess(spawnOpts, newSessionID); spawnErr != nil {
			return fmt.Errorf("spawn bootstrap subprocess: %w", spawnErr)
		}

		_ = copilot.EmitEvent(dir, copilot.Event{
			Type: "copilot_restart",
			Data: map[string]string{
				"old_session_id": oldSession,
				"new_session_id": newSessionID,
				"trigger":        "cli_restart",
			},
		})
		_ = copilot.AppendEventsText(dir, fmt.Sprintf(
			"%s  Copilot restarted via CLI (new session %s, old session %s).",
			startedAt, newSessionID, oldSession))

		fmt.Fprintf(cmd.OutOrStdout(), "Copilot restarted with fresh session.\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  Old session: %s\n", oldSession)
		fmt.Fprintf(cmd.OutOrStdout(), "  New session: %s\n", newSessionID)
		fmt.Fprintln(cmd.OutOrStdout(), "  New session will install its own cron. Old session's cron will self-prune on next wake.")
		return nil
	},
}

// ----- tail -----

var copilotTailCmd = &cobra.Command{
	Use:           "tail",
	Short:         "Tail the copilot event log",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		follow, _ := cmd.Flags().GetBool("follow")
		jsonl, _ := cmd.Flags().GetBool("jsonl")

		var path string
		if jsonl {
			path = filepath.Join(dir, config.CopilotEventsJSONLFile)
		} else {
			path = filepath.Join(dir, config.CopilotEventsTextFile)
		}

		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(cmd.ErrOrStderr(), "no copilot event log at %s\n", path)
				return cobraExitWithCode(1)
			}
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			fmt.Fprintln(cmd.OutOrStdout(), scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return err
		}

		if !follow {
			return nil
		}

		// Follow mode: poll for new content.
		ctx := cmd.Context()
		offset, _ := f.Seek(0, 2)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if _, err := f.Seek(offset, 0); err != nil {
					return err
				}
				newScanner := bufio.NewScanner(f)
				newScanner.Buffer(make([]byte, 64*1024), 1024*1024)
				for newScanner.Scan() {
					fmt.Fprintln(cmd.OutOrStdout(), newScanner.Text())
				}
				if newOffset, err := f.Seek(0, 1); err == nil {
					offset = newOffset
				}
			}
		}
	},
}

// ----- summary -----

var copilotSummaryCmd = &cobra.Command{
	Use:           "summary",
	Short:         "Print the copilot's final summary report",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		current, _ := cmd.Flags().GetBool("current")

		path := filepath.Join(dir, config.CopilotFinalSummaryFile)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				if current {
					return synthesizeCurrentSummary(cmd, dir)
				}
				return fmt.Errorf("no final summary at %s — pass --current for an in-progress synthesis", path)
			}
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), string(data))
		return nil
	},
}

func synthesizeCurrentSummary(cmd *cobra.Command, dir string) error {
	manifest, err := copilot.ReadManifest(dir)
	if err != nil || manifest == nil {
		return fmt.Errorf("no copilot configured in %s", dir)
	}
	events, _ := copilot.ReadEvents(dir)
	counts := copilot.CountEventsByType(events)

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "# Copilot In-Progress Summary\n\n")
	fmt.Fprintf(out, "Started:     %s\n", manifest.StartedAt)
	fmt.Fprintf(out, "Engine:      %s\n", manifest.Engine)
	fmt.Fprintf(out, "Mode:        %s\n", manifest.Mode)
	fmt.Fprintf(out, "Session:     %s\n\n", displayValue(manifest.SessionID))
	fmt.Fprintf(out, "## Activity counts\n\n")
	for _, k := range sortedEventTypeKeys(counts) {
		fmt.Fprintf(out, "  %-40s %d\n", k, counts[k])
	}
	fmt.Fprintln(out)
	if intervDir := filepath.Join(dir, config.CopilotInterventionsDir); fileExists(intervDir) {
		entries, _ := os.ReadDir(intervDir)
		fmt.Fprintf(out, "## Intervention reports (%d)\n\n", len(entries))
		for _, ent := range entries {
			fmt.Fprintf(out, "  - %s\n", ent.Name())
		}
	}
	return nil
}

// ----- list-interventions -----

var copilotListInterventionsCmd = &cobra.Command{
	Use:           "list-interventions",
	Short:         "List all copilot intervention reports",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		intervDir := filepath.Join(dir, config.CopilotInterventionsDir)
		entries, err := os.ReadDir(intervDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(cmd.OutOrStdout(), "no interventions yet")
				return nil
			}
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no interventions yet")
			return nil
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, ent := range entries {
			info, err := ent.Info()
			if err != nil {
				continue
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-40s  %s\n", ent.Name(), info.ModTime().Format(time.RFC3339))
		}
		return nil
	},
}

// ----- emit-event -----

// copilotEmitEventCmd is invoked BY the copilot agent itself (not by humans)
// to append structured events to both the copilot stream and the canonical
// observer stream. It exists so the agent does not have to hand-roll JSONL
// formatting.
var copilotEmitEventCmd = &cobra.Command{
	Use:           "emit-event",
	Short:         "Emit a structured copilot event (used by the copilot agent itself)",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("project-dir")
		dir = resolveCopilotProjectDir(dir)
		typeStr, _ := cmd.Flags().GetString("type")
		dataStr, _ := cmd.Flags().GetString("data")

		if typeStr == "" {
			return fmt.Errorf("--type is required")
		}
		var dataMap map[string]string
		if dataStr != "" {
			if err := json.Unmarshal([]byte(dataStr), &dataMap); err != nil {
				return fmt.Errorf("--data must be a JSON object: %w", err)
			}
		}

		// Copilot events always write to the observer stream — the copilot
		// is independently opt-in via --copilot, not gated by --observer.
		observer.SetEnabled(true)

		evt := copilot.Event{
			Type: observer.EventType(typeStr),
			Data: dataMap,
		}
		if err := copilot.EmitEvent(dir, evt); err != nil {
			return fmt.Errorf("emit event: %w", err)
		}
		return nil
	},
}

// ----- helpers -----

// resolveCopilotProjectDir returns the directory where the copilot's
// .fry/copilot/ artifacts actually live. fry runs in worktree mode for
// COMPLEX builds, in which case the artifacts live in the worktree dir,
// NOT the main checkout. Without this redirect, `fry copilot tail` (and
// every other copilot subcommand) would look in the wrong place and
// report "no event log".
//
// Resolution order:
//
//  1. git.ReadPersistedStrategy(dir) — the strategy file written by
//     fry main during run setup. Authoritative when present.
//
//  2. Scan dir/.fry-worktrees/* for a worktree subdir that has a
//     .fry/copilot/ directory. Used as a fallback when the strategy
//     file hasn't been persisted yet (e.g., the user runs
//     `fry copilot tail` against a build that's still in early
//     prepare). If multiple match, pick the most recently modified.
//
//  3. Otherwise return dir unchanged.
func resolveCopilotProjectDir(dir string) string {
	if dir == "" {
		return dir
	}
	if setup, err := git.ReadPersistedStrategy(dir); err == nil && setup != nil && setup.WorkDir != "" && setup.WorkDir != dir {
		if _, statErr := os.Stat(setup.WorkDir); statErr == nil {
			return setup.WorkDir
		}
	}
	// Fallback: scan .fry-worktrees/* for a single, recent worktree
	// that has copilot state. Catches the early-prepare race where
	// the strategy hasn't been persisted yet.
	wtRoot := filepath.Join(dir, config.GitWorktreeDir)
	entries, err := os.ReadDir(wtRoot)
	if err != nil {
		return dir
	}
	type candidate struct {
		path  string
		mtime time.Time
	}
	var cands []candidate
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		full := filepath.Join(wtRoot, ent.Name())
		if !fileExists(filepath.Join(full, config.CopilotDir)) {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		cands = append(cands, candidate{path: full, mtime: info.ModTime()})
	}
	if len(cands) == 0 {
		return dir
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.mtime.After(best.mtime) {
			best = c
		}
	}
	return best.path
}

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

func displayValue(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func boolYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// statusVerbFromState returns the human-readable session state.
//
//   - "absent"    : no build alive AND no copilot cron installed
//   - "starting"  : build is alive but copilot hasn't installed its cron yet
//   - "recovering": build PID is dead but copilot's cron is active
//     (copilot will attempt BUILD CRASH RECOVERY on its next wake)
//   - "active"    : build is alive and copilot's cron is installed
//
// The cron.id file is the authoritative copilot liveness signal — if it
// exists and is non-empty, the copilot has an active cron regardless of
// whether the build PID is alive.
func statusVerbFromState(buildAlive bool, hasCron bool) string {
	switch {
	case !buildAlive && !hasCron:
		return "absent"
	case !buildAlive && hasCron:
		return "recovering"
	case buildAlive && !hasCron:
		return "starting"
	default:
		return "active"
	}
}

// overlayFreshBuildStatus returns a copy of the on-disk snapshot with
// build_phase / current_sprint / current_sprint_name / build_status /
// total_sprints fields refreshed from build-status.json. This is the
// CLI-side fix for Bug 15 (snapshot staleness): rather than calling
// ForceWriteStateSnapshot from the CLI (which would clobber BuildPID
// with the CLI's own ephemeral PID — Bug 16), we read the canonical
// build-status.json directly and overlay the fresh fields onto an
// in-memory copy of the snapshot. The on-disk snapshot file is NEVER
// modified by this helper.
//
// If reading build-status.json fails, the original snapshot is returned
// unchanged. If the snapshot is nil but build-status.json reads
// successfully, a new snapshot is constructed with only the overlay
// fields set. PID-bearing fields (BuildPID, BuildPIDAlive) are NEVER
// touched here — they remain whatever the on-disk snapshot recorded
// (or zero if the snapshot was nil).
func overlayFreshBuildStatus(projectDir string, snap *copilot.StateSnapshot) *copilot.StateSnapshot {
	status, err := agent.ReadBuildStatus(projectDir)
	if err != nil || status == nil {
		return snap
	}
	if snap == nil {
		snap = &copilot.StateSnapshot{}
	}
	out := *snap // copy so we never mutate the cached pointer
	out.BuildPhase = status.Build.Phase
	out.BuildMode = status.Build.Mode
	out.BuildStatus = status.Build.Status
	out.CurrentSprint = status.Build.CurrentSprint
	out.TotalSprints = status.Build.TotalSprints
	for i := range status.Sprints {
		if status.Sprints[i].Number == status.Build.CurrentSprint {
			out.CurrentSprintName = status.Sprints[i].Name
			break
		}
	}
	return &out
}

func sortedEventTypeKeys(m map[observer.EventType]int) []observer.EventType {
	keys := make([]observer.EventType, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return string(keys[i]) < string(keys[j]) })
	return keys
}

// cobraExitWithCode is used by status/attach/tail to signal a non-zero
// exit while still letting cobra render the error message correctly.
func cobraExitWithCode(code int) error {
	if code == 0 {
		return nil
	}
	return &exitError{code: code}
}

// ExitError is an error returned by a cobra subcommand to request a
// specific process exit code. cmd/fry/main.go type-asserts against
// ExitError to honour the requested code instead of always exiting with 1.
type ExitError interface {
	error
	ExitCode() int
}

type exitError struct{ code int }

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}

func (e *exitError) ExitCode() int {
	return e.code
}

func init() {
	// status
	copilotStatusCmd.Flags().Bool("json", false, "Emit machine-readable JSON")

	// attach
	copilotAttachCmd.Flags().Bool("print-only", false, "Print the attach command without exec'ing it")

	// stop
	copilotStopCmd.Flags().Bool("keep-cron", false, "Skip cron deletion (leave the cron in place)")

	// tail
	copilotTailCmd.Flags().Bool("follow", true, "Follow the log (tail -f style); use --follow=false to dump and exit")
	copilotTailCmd.Flags().Bool("jsonl", false, "Tail events.jsonl instead of events.txt")

	// summary
	copilotSummaryCmd.Flags().Bool("current", false, "Synthesize an in-progress summary if no final-summary.md exists")

	// emit-event
	copilotEmitEventCmd.Flags().String("type", "", "Event type (e.g., copilot_intervention_started)")
	copilotEmitEventCmd.Flags().String("data", "", "Event data as a JSON object")

	copilotCmd.AddCommand(copilotStatusCmd)
	copilotCmd.AddCommand(copilotAttachCmd)
	copilotCmd.AddCommand(copilotStartCmd)
	copilotCmd.AddCommand(copilotStopCmd)
	copilotCmd.AddCommand(copilotRestartCmd)
	copilotCmd.AddCommand(copilotTailCmd)
	copilotCmd.AddCommand(copilotSummaryCmd)
	copilotCmd.AddCommand(copilotListInterventionsCmd)
	copilotCmd.AddCommand(copilotEmitEventCmd)
}
