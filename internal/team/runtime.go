package team

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/observer"
)

func Start(ctx context.Context, opts StartOptions) (*Config, error) {
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		return nil, err
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = max(opts.Workers, 8)
	}
	if len(opts.Roles) == 0 {
		opts.Roles = []string{"executor"}
	}
	if opts.GitIsolationMode == "" {
		if opts.Workers > 1 && git.IsInsideGitRepo(ctx, projectDir) {
			opts.GitIsolationMode = IsolationPerWorkerWorktree
		} else {
			opts.GitIsolationMode = IsolationShared
		}
	}
	if opts.ExecutablePath == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			return nil, exeErr
		}
		opts.ExecutablePath = exe
	}
	if _, statErr := os.Stat(opts.ExecutablePath); statErr != nil {
		return nil, fmt.Errorf("resolved fry executable %q is not accessible: %w", opts.ExecutablePath, statErr)
	}
	if opts.TeamID == "" {
		opts.TeamID = fmt.Sprintf("team-%s", time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := ensureTeamLayout(projectDir, opts.TeamID); err != nil {
		return nil, err
	}

	session := "fry-" + sanitizeBranchComponent(opts.TeamID)
	if DefaultTmux.HasSession(ctx, session) {
		return nil, fmt.Errorf("tmux session %q already exists", session)
	}

	leaderPane, err := DefaultTmux.NewSession(ctx, session, "leader", "tail -f /dev/null")
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		Version:          1,
		TeamID:           opts.TeamID,
		ProjectDir:       projectDir,
		BuildDir:         projectDir,
		Status:           StatusRunning,
		Engine:           opts.Engine,
		TMuxSession:      session,
		LeaderPaneID:     leaderPane,
		WorkerCount:      opts.Workers,
		MaxWorkers:       opts.MaxWorkers,
		GitIsolationMode: opts.GitIsolationMode,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := SaveConfig(projectDir, cfg); err != nil {
		return nil, err
	}
	if err := setActiveTeam(projectDir, opts.TeamID); err != nil {
		return nil, err
	}
	if err := emitEvent(projectDir, opts.TeamID, Event{
		Type:   "team_start",
		TeamID: opts.TeamID,
		Data: map[string]string{
			"session": session,
			"mode":    string(opts.GitIsolationMode),
		},
	}); err != nil {
		return nil, err
	}

	for i := 1; i <= opts.Workers; i++ {
		workerID := fmt.Sprintf("worker-%d", i)
		role := opts.Roles[(i-1)%len(opts.Roles)]
		identity, err := createWorkerIdentity(ctx, cfg, workerID, role)
		if err != nil {
			return nil, err
		}
		if err := StartWorkerHost(ctx, DefaultTmux, cfg, identity, opts.ExecutablePath); err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(opts.TaskFile) != "" {
		if _, err := AssignTasksFromFile(projectDir, opts.TeamID, opts.TaskFile); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

func Pause(projectDir, teamID string) error {
	cfg, err := LoadConfig(projectDir, teamID)
	if err != nil {
		return err
	}
	cfg.Status = StatusPaused
	if err := SaveConfig(projectDir, cfg); err != nil {
		return err
	}
	return emitEvent(projectDir, teamID, Event{Type: "team_pause", TeamID: teamID})
}

func Resume(ctx context.Context, projectDir, teamID, executable string) error {
	if err := Reconcile(ctx, projectDir, teamID); err != nil {
		return err
	}
	cfg, err := LoadConfig(projectDir, teamID)
	if err != nil {
		return err
	}
	if executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		executable = exe
	}
	if _, statErr := os.Stat(executable); statErr != nil {
		return fmt.Errorf("resolved fry executable %q is not accessible: %w", executable, statErr)
	}
	cfg.Status = StatusRunning
	if err := SaveConfig(projectDir, cfg); err != nil {
		return err
	}
	if !DefaultTmux.HasSession(ctx, cfg.TMuxSession) {
		leaderPane, err := DefaultTmux.NewSession(ctx, cfg.TMuxSession, "leader", "tail -f /dev/null")
		if err != nil {
			return err
		}
		cfg.LeaderPaneID = leaderPane
		if err := SaveConfig(projectDir, cfg); err != nil {
			return err
		}
	}
	if err := RestartDeadWorkers(ctx, projectDir, teamID, executable); err != nil {
		return err
	}
	return emitEvent(projectDir, teamID, Event{Type: "team_resume", TeamID: teamID})
}

func Shutdown(ctx context.Context, projectDir, teamID string, force bool) error {
	cfg, err := LoadConfig(projectDir, teamID)
	if err != nil {
		return err
	}
	snap, err := SnapshotForTeam(ctx, projectDir, teamID)
	if err != nil {
		return err
	}
	if !force {
		for _, worker := range snap.Workers {
			if worker.Record.CurrentTask != "" {
				return fmt.Errorf("worker %s is still running task %s; use --force or pause first", worker.Record.WorkerID, worker.Record.CurrentTask)
			}
		}
	}
	cfg.Status = StatusShutdown
	if err := SaveConfig(projectDir, cfg); err != nil {
		return err
	}
	for _, worker := range snap.Workers {
		record := worker.Record
		record.DesiredStatus = WorkerStopped
		record.Status = WorkerStopped
		record.CurrentTask = ""
		if err := SaveWorkerRecord(projectDir, teamID, &record); err != nil {
			return err
		}
	}
	_ = DefaultTmux.KillSession(ctx, cfg.TMuxSession)
	if err := emitEvent(projectDir, teamID, Event{Type: "team_shutdown", TeamID: teamID}); err != nil {
		return err
	}
	return clearActiveTeam(projectDir, teamID)
}

func Attach(ctx context.Context, projectDir, teamID string) error {
	cfg, err := LoadConfig(projectDir, teamID)
	if err != nil {
		return err
	}
	if system, ok := DefaultTmux.(systemTmux); ok {
		_ = system
		cmd := exec.CommandContext(ctx, "tmux", "attach-session", "-t", cfg.TMuxSession)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return DefaultTmux.Attach(ctx, cfg.TMuxSession)
}

func Scale(ctx context.Context, opts ScaleOptions, executable string) error {
	cfg, err := LoadConfig(opts.ProjectDir, opts.TeamID)
	if err != nil {
		return err
	}
	if executable == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		executable = exe
	}
	if _, statErr := os.Stat(executable); statErr != nil {
		return fmt.Errorf("resolved fry executable %q is not accessible: %w", executable, statErr)
	}

	switch {
	case opts.Add > 0:
		ids, err := ListWorkerIDs(opts.ProjectDir, opts.TeamID)
		if err != nil {
			return err
		}
		next := len(ids) + 1
		for i := 0; i < opts.Add; i++ {
			workerID := fmt.Sprintf("worker-%d", next+i)
			identity, err := createWorkerIdentity(ctx, cfg, workerID, "executor")
			if err != nil {
				return err
			}
			if err := StartWorkerHost(ctx, DefaultTmux, cfg, identity, executable); err != nil {
				return err
			}
		}
		cfg.WorkerCount += opts.Add
		if err := SaveConfig(opts.ProjectDir, cfg); err != nil {
			return err
		}
		return emitEvent(opts.ProjectDir, opts.TeamID, Event{
			Type:   "team_scale_up",
			TeamID: opts.TeamID,
			Data: map[string]string{
				"add": fmt.Sprintf("%d", opts.Add),
			},
		})
	case strings.TrimSpace(opts.Remove) != "":
		record, err := LoadWorkerRecord(opts.ProjectDir, opts.TeamID, opts.Remove)
		if err != nil {
			return err
		}
		if record.CurrentTask != "" {
			record.DesiredStatus = WorkerDraining
			if err := SaveWorkerRecord(opts.ProjectDir, opts.TeamID, record); err != nil {
				return err
			}
			return emitEvent(opts.ProjectDir, opts.TeamID, Event{
				Type:     "team_scale_down",
				TeamID:   opts.TeamID,
				WorkerID: opts.Remove,
				Data: map[string]string{
					"mode": "drain",
				},
			})
		}
		record.DesiredStatus = WorkerStopped
		record.Status = WorkerStopped
		if err := SaveWorkerRecord(opts.ProjectDir, opts.TeamID, record); err != nil {
			return err
		}
		identity, err := LoadIdentity(opts.ProjectDir, opts.TeamID, opts.Remove)
		if err == nil && identity.WindowName != "" {
			_ = DefaultTmux.KillWindow(ctx, cfg.TMuxSession, identity.WindowName)
		}
		cfg.WorkerCount--
		if cfg.WorkerCount < 0 {
			cfg.WorkerCount = 0
		}
		if err := SaveConfig(opts.ProjectDir, cfg); err != nil {
			return err
		}
		return emitEvent(opts.ProjectDir, opts.TeamID, Event{
			Type:     "team_scale_down",
			TeamID:   opts.TeamID,
			WorkerID: opts.Remove,
		})
	default:
		return errors.New("either add or remove must be set")
	}
}

func createWorkerIdentity(ctx context.Context, cfg *Config, workerID, role string) (*WorkerIdentity, error) {
	workDir := cfg.ProjectDir
	branch := ""
	if cfg.GitIsolationMode == IsolationPerWorkerWorktree && git.IsInsideGitRepo(ctx, cfg.ProjectDir) {
		branch = fmt.Sprintf("fry/team-%s-%s", sanitizeBranchComponent(cfg.TeamID), workerID)
		worktreeDir := filepath.Join(cfg.ProjectDir, ".fry-worktrees", sanitizeBranchComponent(cfg.TeamID), workerID)
		if err := createTeamWorktree(ctx, cfg.ProjectDir, worktreeDir, branch); err != nil {
			return nil, err
		}
		workDir = worktreeDir
	}
	identity := &WorkerIdentity{
		WorkerID:       workerID,
		Role:           role,
		Engine:         cfg.Engine,
		WorkDir:        workDir,
		WorktreeBranch: branch,
		Status:         WorkerStarting,
	}
	if err := SaveIdentity(cfg.ProjectDir, cfg.TeamID, identity); err != nil {
		return nil, err
	}
	record := &WorkerRecord{
		WorkerID:      workerID,
		Status:        WorkerStarting,
		DesiredStatus: WorkerRunning,
	}
	if err := SaveWorkerRecord(cfg.ProjectDir, cfg.TeamID, record); err != nil {
		return nil, err
	}
	return identity, nil
}

func createTeamWorktree(ctx context.Context, projectDir, worktreeDir, branch string) error {
	if _, err := os.Stat(worktreeDir); err == nil {
		if git.IsInsideGitRepo(ctx, worktreeDir) {
			return nil
		}
		_ = os.RemoveAll(worktreeDir)
	}
	_ = DefaultGitExecutor.WorktreePrune(ctx, projectDir)
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		return err
	}
	return DefaultGitExecutor.WorktreeAdd(ctx, projectDir, worktreeDir, branch, !DefaultGitExecutor.BranchExists(ctx, projectDir, branch))
}

func emitEvent(projectDir, teamID string, evt Event) error {
	if evt.TeamID == "" {
		evt.TeamID = teamID
	}
	if err := appendTeamEvent(TeamEventsPath(projectDir, teamID), evt); err != nil {
		return err
	}
	data := map[string]string{
		"team_id": teamID,
	}
	for k, v := range evt.Data {
		data[k] = v
	}
	if evt.WorkerID != "" {
		data["worker_id"] = evt.WorkerID
	}
	if evt.TaskID != "" {
		data["task_id"] = evt.TaskID
	}
	return observerEmit(projectDir, evt.Type, data)
}

func observerEmit(projectDir, eventType string, data map[string]string) error {
	return observer.EmitEvent(projectDir, observer.Event{
		Type: observer.EventType(eventType),
		Data: data,
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func SortedTeamIDs(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(RootDir(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}
