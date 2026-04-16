// Package agent provides the domain types and interfaces for Fry's agent
// foundation. It is consumed via CLI commands and designed so a future native
// Fry agent can import it directly.
package agent

import "time"

// BuildState is the structured representation of a running or completed build.
// Serialized as JSON by `fry status --json`.
type BuildState struct {
	Active            bool         `json:"active"`
	ProjectDir        string       `json:"project_dir"`
	Epic              string       `json:"epic"`
	Effort            string       `json:"effort"`
	Engine            string       `json:"engine"`
	Mode              string       `json:"mode,omitempty"`
	TotalSprints      int          `json:"total_sprints"`
	CurrentSprint     int          `json:"current_sprint"`
	CurrentSprintName string       `json:"current_sprint_name"`
	Status            string       `json:"status"`          // running, completed, failed, paused, idle, triaging, preparing
	Phase             string       `json:"phase,omitempty"` // triage, prepare, sprint, audit, complete, failed
	LastEvent         *BuildEvent  `json:"last_event,omitempty"`
	GitBranch         string       `json:"git_branch,omitempty"`
	WorktreeDir       string       `json:"worktree_dir,omitempty"`
	StartedAt         *time.Time   `json:"started_at,omitempty"`
	PID               int          `json:"pid,omitempty"`
	Team              *TeamSummary `json:"team,omitempty"`
}

// BuildEvent is a structured event from the observer event stream.
type BuildEvent struct {
	Type      string            `json:"type"`
	Timestamp time.Time         `json:"ts"`
	Sprint    int               `json:"sprint,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
}

type TeamSummary struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	WorkerCount    int    `json:"worker_count"`
	IdleWorkers    int    `json:"idle_workers"`
	RunningWorkers int    `json:"running_workers"`
	StalledWorkers int    `json:"stalled_workers"`
	PendingTasks   int    `json:"pending_tasks"`
	ActiveTasks    int    `json:"active_tasks"`
	CompletedTasks int    `json:"completed_tasks"`
	FailedTasks    int    `json:"failed_tasks"`
	IntegratedDir  string `json:"integrated_dir,omitempty"`
}

// ArtifactInfo describes a single Fry build artifact for agent prompt generation.
type ArtifactInfo struct {
	Path        string `json:"path"`        // relative to project dir
	Format      string `json:"format"`      // jsonl, markdown, json, text
	Description string `json:"description"` // what this artifact contains
	Lifecycle   string `json:"lifecycle"`   // per-iteration, per-sprint, per-build, static
}
