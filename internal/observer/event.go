package observer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// enabled controls whether EmitEvent writes to disk. When false (the default),
// EmitEvent is a no-op. Call SetEnabled(true) to activate event recording.
var enabled atomic.Bool

// SetEnabled controls whether the observer event stream is active. When false,
// EmitEvent silently discards events. This should be called early in the build
// based on the --observer flag.
func SetEnabled(v bool) { enabled.Store(v) }

// EventType identifies the kind of build event recorded in the observer stream.
type EventType string

const (
	EventTriageStart       EventType = "triage_start"
	EventTriageComplete    EventType = "triage_complete"
	EventPrepareStart      EventType = "prepare_start"
	EventPrepareComplete   EventType = "prepare_complete"
	EventBuildStart        EventType = "build_start"
	EventSprintStart       EventType = "sprint_start"
	EventSprintComplete    EventType = "sprint_complete"
	EventEngineFailover    EventType = "engine_failover"
	EventAlignmentComplete EventType = "alignment_complete"
	EventAuditComplete     EventType = "audit_complete"
	EventReviewComplete    EventType = "review_complete"
	EventBuildAuditDone    EventType = "build_audit_done"
	EventBuildEnd          EventType = "build_end"

	// Build steering events (Layer 1)
	EventDirectiveReceived EventType = "directive_received"
	EventDecisionNeeded    EventType = "decision_needed"
	EventDecisionReceived  EventType = "decision_received"
	EventBuildPaused       EventType = "build_paused"

	EventTeamStart        EventType = "team_start"
	EventTeamWorkerReady  EventType = "team_worker_ready"
	EventTeamTaskCreated  EventType = "team_task_created"
	EventTeamTaskAssigned EventType = "team_task_assigned"
	EventTeamTaskStarted  EventType = "team_task_started"
	EventTeamTaskDone     EventType = "team_task_completed"
	EventTeamTaskFailed   EventType = "team_task_failed"
	EventTeamScaleUp      EventType = "team_scale_up"
	EventTeamScaleDown    EventType = "team_scale_down"
	EventTeamWorkerStall  EventType = "team_worker_stalled"
	EventTeamPause        EventType = "team_pause"
	EventTeamResume       EventType = "team_resume"
	EventTeamShutdown     EventType = "team_shutdown"
	EventTeamMergeReady   EventType = "team_merge_ready"
	EventTeamComplete     EventType = "team_complete"

	// Copilot events — emitted by the `fry run --copilot` parallel session.
	// These are added to the canonical observer event taxonomy so that
	// `fry monitor` and `fry events --follow` render them natively.
	EventCopilotBootstrap          EventType = "copilot_bootstrap"
	EventCopilotCronInstalled      EventType = "copilot_cron_installed"
	EventCopilotCronRemoved        EventType = "copilot_cron_removed"
	EventCopilotWakeStart          EventType = "copilot_wake_start"
	EventCopilotWakeEnd            EventType = "copilot_wake_end"
	EventCopilotWakeSkipped        EventType = "copilot_wake_skipped"
	EventCopilotAnomalyDetected    EventType = "copilot_anomaly_detected"
	EventCopilotInterventionStart  EventType = "copilot_intervention_started"
	EventCopilotInterventionDone   EventType = "copilot_intervention_completed"
	EventCopilotInterventionFailed EventType = "copilot_intervention_failed"
	EventCopilotFryBugFix          EventType = "copilot_fry_bug_fix"
	EventCopilotMakeInstall        EventType = "copilot_make_install"
	EventCopilotGitPush            EventType = "copilot_git_push"
	EventCopilotArtifactRemediate  EventType = "copilot_artifact_remediate"
	EventCopilotBuildRestart       EventType = "copilot_build_restart"
	EventCopilotFinalSummary       EventType = "copilot_final_summary"
	EventCopilotEscalation         EventType = "copilot_escalation"
	EventCopilotUserMessage        EventType = "copilot_user_message"
	EventCopilotPanic              EventType = "copilot_panic"
)

// Event represents a single timestamped build event in the observer stream.
type Event struct {
	Timestamp string            `json:"ts"`
	Type      EventType         `json:"type"`
	Sprint    int               `json:"sprint,omitempty"`
	Data      map[string]string `json:"data,omitempty"`
}

// EmitEvent appends a JSON-line event to the events file.
// Creates the observer directory if needed. Returns nil immediately
// if the observer is not enabled (see SetEnabled).
func EmitEvent(projectDir string, evt Event) error {
	if !enabled.Load() {
		return nil
	}
	if evt.Timestamp == "" {
		evt.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	dir := filepath.Join(projectDir, config.ObserverDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("emit event: create dir: %w", err)
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("emit event: marshal: %w", err)
	}
	line = append(line, '\n')
	f, err := os.OpenFile(filepath.Join(projectDir, config.ObserverEventsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("emit event: open: %w", err)
	}
	defer f.Close()
	_, err = f.Write(line)
	if err != nil {
		return fmt.Errorf("emit event: write: %w", err)
	}
	return nil
}

// ReadEvents reads all events from the events file.
// Returns nil, nil if the file does not exist.
func ReadEvents(projectDir string) ([]Event, error) {
	eventsPath := filepath.Join(projectDir, config.ObserverEventsFile)
	f, err := os.Open(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read events: open: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("read events: parse line: %w", err)
		}
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read events: scan: %w", err)
	}
	return events, nil
}

// ReadRecentEvents reads the last n events.
// Returns nil if n <= 0.
func ReadRecentEvents(projectDir string, n int) ([]Event, error) {
	if n <= 0 {
		return nil, nil
	}
	events, err := ReadEvents(projectDir)
	if err != nil {
		return nil, err
	}
	if len(events) <= n {
		return events, nil
	}
	return events[len(events)-n:], nil
}
