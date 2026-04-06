package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// RunType describes how a build run was initiated.
type RunType string

const (
	RunTypeFresh    RunType = "fresh"
	RunTypeContinue RunType = "continue"
	RunTypeRetry    RunType = "retry"
	RunTypeResume   RunType = "resume"
)

// RunMeta captures the lineage of a build run.
type RunMeta struct {
	RunID       string  `json:"run_id"`
	RunType     RunType `json:"run_type"`
	ParentRunID string  `json:"parent_run_id,omitempty"`
}

// BuildStatus is a machine-readable snapshot of the entire build state,
// written atomically to .fry/build-status.json after every meaningful
// state change. Designed for agent LLMs to poll instead of parsing CLI output.
type BuildStatus struct {
	Version    int               `json:"version"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Run        *RunMeta          `json:"run,omitempty"`
	Build            BuildInfo         `json:"build"`
	Sprints          []SprintStatus    `json:"sprints"`
	BuildAudit       *BuildAuditStatus `json:"build_audit,omitempty"`
	ReportingFailure *ReportingFailure `json:"reporting_failure,omitempty"`
}

// BuildInfo contains the top-level build metadata.
type BuildInfo struct {
	Epic          string    `json:"epic"`
	Effort        string    `json:"effort"`
	Engine        string    `json:"engine"`
	Mode          string    `json:"mode"`
	GitBranch     string    `json:"git_branch,omitempty"`
	TotalSprints  int       `json:"total_sprints"`
	CurrentSprint int       `json:"current_sprint"`
	Status        string    `json:"status"`          // running, completed, completed_with_reporting_failure, failed, paused, triaging, preparing
	Phase         string    `json:"phase,omitempty"` // triage, prepare, sprint, audit, complete
	StartedAt     time.Time `json:"started_at"`
}

// SprintStatus captures the state of a single sprint.
type SprintStatus struct {
	Number           int                `json:"number"`
	Name             string             `json:"name"`
	Status           string             `json:"status"` // running, PASS, PASS (aligned), FAIL, SKIPPED, etc.
	StartedAt        *time.Time         `json:"started_at,omitempty"`
	FinishedAt       *time.Time         `json:"finished_at,omitempty"`
	DurationSec      float64            `json:"duration_sec,omitempty"`
	SanityChecks     *SanityCheckStatus `json:"sanity_checks,omitempty"`
	Alignment        *AlignmentStatus   `json:"alignment,omitempty"`
	Audit            *AuditStatus       `json:"audit,omitempty"`
	Review           *ReviewStatus      `json:"review,omitempty"`
	DeferredFailures int                `json:"deferred_failures,omitempty"`
	Warnings         []string           `json:"warnings,omitempty"`
}

// SanityCheckStatus summarizes sanity check results for a sprint.
type SanityCheckStatus struct {
	Passed  int                `json:"passed"`
	Total   int                `json:"total"`
	Results []CheckResultEntry `json:"results,omitempty"`
}

// CheckResultEntry is a single sanity check outcome.
type CheckResultEntry struct {
	Type   string `json:"type"`   // FILE, FILE_CONTAINS, CMD, CMD_OUTPUT, TEST
	Target string `json:"target"` // file path or command
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"` // truncated check output (failures only)
}

// AlignmentStatus summarizes the alignment (heal) loop for a sprint.
type AlignmentStatus struct {
	Attempts int    `json:"attempts"`
	Outcome  string `json:"outcome"` // healed, exhausted, within_threshold, not_needed
}

type AuditMetricsSnapshot struct {
	TotalCalls       int     `json:"total_calls"`
	DurationMs       int64   `json:"duration_ms"`
	NoOpFixCalls     int     `json:"no_op_fix_calls"`
	SessionRefreshes int     `json:"session_refreshes"`
	NoOpRate         float64 `json:"no_op_rate"`
	VerifyCalls      int     `json:"verify_calls"`
	VerifyResolutions int    `json:"verify_resolutions"`
	VerifyYield      float64 `json:"verify_yield"`
}

type AuditBlocker struct {
	Category string `json:"category"`
	Location string `json:"location,omitempty"`
	Details  string `json:"details,omitempty"`
	Severity string `json:"severity,omitempty"`
}

// AuditStatus summarizes the per-sprint audit results.
type AuditStatus struct {
	Cycles         int                   `json:"cycles"`
	Findings       map[string]int        `json:"findings"` // severity -> count
	Outcome        string                `json:"outcome"`  // pass, failed, advisory, running
	Blocked        bool                  `json:"blocked,omitempty"`
	BlockerCounts  map[string]int        `json:"blocker_counts,omitempty"`
	Blockers       []AuditBlocker        `json:"blockers,omitempty"`
	Active         bool                  `json:"active,omitempty"`
	Stage          string                `json:"stage,omitempty"`           // auditing, fixing, verifying
	CurrentCycle   int                   `json:"current_cycle,omitempty"`   // current outer audit cycle
	MaxCycles      int                   `json:"max_cycles,omitempty"`      // configured outer audit cycle cap
	CurrentFix     int                   `json:"current_fix,omitempty"`     // current inner fix/verify iteration
	MaxFixes       int                   `json:"max_fixes,omitempty"`       // configured inner fix cap
	TargetIssues   int                   `json:"target_issues,omitempty"`   // issues currently being fixed/verified
	IssueHeadlines []string              `json:"issue_headlines,omitempty"` // compact descriptions of targeted issues
	Complexity     string                `json:"complexity,omitempty"`
	Metrics        *AuditMetricsSnapshot `json:"metrics,omitempty"`
}

// ReviewStatus captures the sprint review verdict.
type ReviewStatus struct {
	Verdict string `json:"verdict"` // CONTINUE, DEVIATE
}

// BuildAuditStatus captures the final build-level audit results.
type BuildAuditStatus struct {
	Ran      bool           `json:"ran"`
	Passed   bool           `json:"passed"`
	Blocking bool           `json:"blocking"`
	Findings map[string]int `json:"findings,omitempty"`
}

// ReportingFailure captures which post-build reporting stage failed.
// This separates core build completion from reporting failures so that
// operators can tell whether Fry failed to build or failed to narrate the build.
type ReportingFailure struct {
	Stage   string `json:"stage"`             // build_audit, summary
	Message string `json:"message,omitempty"` // error description
}

// GenerateRunID creates a timestamp-based run identifier with millisecond
// precision to avoid collisions when two runs start in the same second.
func GenerateRunID() string {
	return config.RunPrefix + time.Now().Format("20060102-150405.000")
}

// WriteBuildStatus atomically writes the build status to .fry/build-status.json.
// If the status has a RunMeta with a RunID, it also writes an immutable snapshot
// to .fry/runs/<run-id>/build-status.json so that later retries cannot erase
// earlier run history.
func WriteBuildStatus(projectDir string, status *BuildStatus) error {
	status.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal build status: %w", err)
	}
	data = append(data, '\n')

	// Write the top-level status file (latest pointer).
	if err := atomicWrite(filepath.Join(projectDir, config.BuildStatusFile), data); err != nil {
		return err
	}

	// Write the per-run snapshot if a run ID is set.
	if status.Run != nil && status.Run.RunID != "" {
		runDir := filepath.Join(projectDir, config.RunsDir, status.Run.RunID)
		runPath := filepath.Join(runDir, "build-status.json")
		if err := atomicWrite(runPath, data); err != nil {
			return fmt.Errorf("write run snapshot: %w", err)
		}
	}

	return nil
}

// atomicWrite writes data to path via a temporary file and atomic rename.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for %s: %w", filepath.Base(path), err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s: %w", filepath.Base(path), err)
	}
	return nil
}

// ReadBuildStatus reads and parses .fry/build-status.json.
// Returns nil, nil if the file does not exist.
func ReadBuildStatus(projectDir string) (*BuildStatus, error) {
	return readBuildStatusFrom(filepath.Join(projectDir, config.BuildStatusFile))
}

// ReadRunStatus reads a specific run's build-status.json by run ID.
// Returns nil, nil if the run directory or status file does not exist.
func ReadRunStatus(projectDir, runID string) (*BuildStatus, error) {
	path := filepath.Join(projectDir, config.RunsDir, runID, "build-status.json")
	return readBuildStatusFrom(path)
}

func readBuildStatusFrom(path string) (*BuildStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read build status: %w", err)
	}
	var status BuildStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("parse build status: %w", err)
	}
	return &status, nil
}

// RunSummary is a lightweight snapshot of a run, extracted from the runs directory.
type RunSummary struct {
	RunID     string    `json:"run_id"`
	RunType   RunType   `json:"run_type"`
	ParentID  string    `json:"parent_run_id,omitempty"`
	Epic      string    `json:"epic"`
	Status    string    `json:"status"`
	Phase     string    `json:"phase,omitempty"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Sprints   int       `json:"sprints"`
}

// ScanRuns reads .fry/runs/ and returns a summary for each run,
// sorted newest-first. Returns nil, nil if the runs directory does not exist.
func ScanRuns(projectDir string) ([]RunSummary, error) {
	runsRoot := filepath.Join(projectDir, config.RunsDir)
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan runs: %w", err)
	}

	var summaries []RunSummary
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), config.RunPrefix) {
			continue
		}
		statusPath := filepath.Join(runsRoot, entry.Name(), "build-status.json")
		status, err := readBuildStatusFrom(statusPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fry: warning: scan run %s: %v\n", entry.Name(), err)
			continue
		}
		if status == nil {
			continue
		}
		summary := RunSummary{
			RunID:     entry.Name(),
			Epic:      status.Build.Epic,
			Status:    status.Build.Status,
			Phase:     status.Build.Phase,
			StartedAt: status.Build.StartedAt,
			UpdatedAt: status.UpdatedAt,
			Sprints:   len(status.Sprints),
		}
		if status.Run != nil {
			summary.RunType = status.Run.RunType
			summary.ParentID = status.Run.ParentRunID
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].StartedAt.After(summaries[j].StartedAt)
	})

	return summaries, nil
}

// LatestRunID returns the RunID from the current top-level build-status.json,
// or empty string if no run metadata exists.
func LatestRunID(projectDir string) string {
	status, err := ReadBuildStatus(projectDir)
	if err != nil || status == nil || status.Run == nil {
		return ""
	}
	return status.Run.RunID
}

// RollingSprintResult is a compact per-sprint outcome persisted after each
// sprint for resumable final-stage reporting.
type RollingSprintResult struct {
	Number      int     `json:"number"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	DurationSec float64 `json:"duration_sec,omitempty"`
}

// WriteRollingResults persists a compact sprint results snapshot to
// .fry/rolling-results.json so that final-stage reporting can resume
// from durable state rather than reconstructing from raw logs.
func WriteRollingResults(projectDir string, results []RollingSprintResult) error {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rolling results: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(filepath.Join(projectDir, config.RollingResultsFile), data)
}

// ReadRollingResults reads the rolling results snapshot.
// Returns nil, nil if the file does not exist.
func ReadRollingResults(projectDir string) ([]RollingSprintResult, error) {
	path := filepath.Join(projectDir, config.RollingResultsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rolling results: %w", err)
	}
	var results []RollingSprintResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("parse rolling results: %w", err)
	}
	return results, nil
}
