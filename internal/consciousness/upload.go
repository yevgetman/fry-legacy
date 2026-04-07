package consciousness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/textutil"
)

// userAgent identifies Fry to Cloudflare so requests are not blocked by bot protection.
const userAgent = "fry/" + config.Version

const pendingMaxAge = 7 * 24 * time.Hour

type UploadEventType string

const (
	UploadEventSessionStarted     UploadEventType = "session_started"
	UploadEventCheckpointSummary  UploadEventType = "checkpoint_summary"
	UploadEventSessionInterrupted UploadEventType = "session_interrupted"
	UploadEventSessionCompleted   UploadEventType = "session_completed"
)

// UploadPayload is the legacy JSON body sent to the /ingest endpoint.
type UploadPayload struct {
	ID             string        `json:"id"`
	SourceInstance string        `json:"source_instance"`
	BuildMetadata  BuildMetadata `json:"build_metadata"`
	SummaryText    string        `json:"summary_text"`
}

// BuildMetadata contains build-level metadata for upload payloads.
type BuildMetadata struct {
	Engine       string `json:"engine"`
	EffortLevel  string `json:"effort_level"`
	TotalSprints int    `json:"total_sprints"`
	Outcome      string `json:"outcome"`
}

// UploadEnvelope is the v2 checkpoint-aware upload payload.
type UploadEnvelope struct {
	SchemaVersion     int                `json:"schema_version"`
	ID                string             `json:"id"`
	EventType         UploadEventType    `json:"event_type"`
	SessionID         string             `json:"session_id"`
	Sequence          int                `json:"sequence"`
	SourceInstance    string             `json:"source_instance"`
	Timestamp         time.Time          `json:"timestamp"`
	BuildMetadata     BuildMetadata      `json:"build_metadata"`
	SummaryText       string             `json:"summary_text,omitempty"`
	CheckpointSummary *CheckpointSummary `json:"checkpoint_summary,omitempty"`
}

// UploadResult is the response from the /ingest endpoint.
type UploadResult struct {
	OK                bool   `json:"ok"`
	ID                string `json:"id"`
	GlobalBuildNumber int    `json:"global_build_number"`
	Duplicate         bool   `json:"duplicate,omitempty"`
}

// UploadExperience sends a legacy final build record to the consciousness API.
func UploadExperience(ctx context.Context, apiURL, apiToken string, record BuildRecord) (*UploadResult, error) {
	payload := UploadPayload{
		ID:             record.ID,
		SourceInstance: InstanceID(),
		BuildMetadata: BuildMetadata{
			Engine:       record.Engine,
			EffortLevel:  record.EffortLevel,
			TotalSprints: record.TotalSprints,
			Outcome:      record.Outcome,
		},
		SummaryText: record.Summary,
	}
	return uploadJSON(ctx, apiURL, apiToken, payload)
}

// UploadEvent sends a checkpoint-aware lifecycle event to the consciousness API.
func UploadEvent(ctx context.Context, apiURL, apiToken string, event UploadEnvelope) (*UploadResult, error) {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = 2
	}
	if strings.TrimSpace(event.SourceInstance) == "" {
		event.SourceInstance = InstanceID()
	}
	return uploadJSON(ctx, apiURL, apiToken, event)
}

func uploadJSON(ctx context.Context, apiURL, apiToken string, payload any) (*UploadResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("upload payload: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+"/ingest", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("upload payload: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upload payload: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("upload payload: read response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		return &UploadResult{OK: true, Duplicate: true}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := textutil.TruncateUTF8(string(respBody), 200)
		return nil, fmt.Errorf("upload payload: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var result UploadResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("upload payload: decode response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("upload payload: server returned ok=false")
	}
	return &result, nil
}

// CachePendingUpload writes a failed legacy upload to the home pending directory.
func CachePendingUpload(record BuildRecord) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cache pending upload: %w", err)
	}
	return cachePendingUploadToDir(filepath.Join(home, config.PendingUploadsDir), record)
}

func cachePendingUploadToDir(dir string, record BuildRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cache pending upload: create dir: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("cache pending upload: marshal: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("pending-%s.json", record.ID)), data, 0o644)
}

func (c *Collector) queueUploadEventLocked(event UploadEnvelope) error {
	if !c.uploadEnabled {
		return nil
	}
	dir := filepath.Join(c.projectDir, config.ConsciousnessUploadQueueDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, eventFilename(event))
	if err := writeJSONAtomic(path, event); err != nil {
		return err
	}
	c.session.PendingUploads = countJSONFiles(dir)
	c.record.UploadState = UploadStatePending
	return nil
}

// RetryPendingUploads processes legacy pending uploads from the home queue.
func RetryPendingUploads(ctx context.Context, apiURL, apiToken string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	return retryPendingFiles(ctx, apiURL, apiToken, filepath.Join(home, config.PendingUploadsDir), "")
}

func retryPendingUploadsFromDir(ctx context.Context, apiURL, apiToken, dir string) int {
	return retryPendingFiles(ctx, apiURL, apiToken, dir, "")
}

// RetryProjectUploads processes checkpoint-aware pending uploads from a project-local queue.
func RetryProjectUploads(ctx context.Context, apiURL, apiToken, projectDir string) int {
	return retryPendingFiles(ctx, apiURL, apiToken, filepath.Join(projectDir, config.ConsciousnessUploadQueueDir), projectDir)
}

func retryPendingFiles(ctx context.Context, apiURL, apiToken, dir, projectDir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	succeeded := 0
	attempts := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > pendingMaxAge {
			_ = os.Remove(path)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		attempts++

		switch payloadType(data) {
		case "event":
			var event UploadEnvelope
			if err := json.Unmarshal(data, &event); err != nil {
				_ = os.Remove(path)
				continue
			}
			if _, err := UploadEvent(ctx, apiURL, apiToken, event); err != nil {
				continue
			}
		default:
			var record BuildRecord
			if err := json.Unmarshal(data, &record); err != nil {
				_ = os.Remove(path)
				continue
			}
			if _, err := UploadExperience(ctx, apiURL, apiToken, record); err != nil {
				continue
			}
		}

		_ = os.Remove(path)
		succeeded++
	}

	if projectDir != "" {
		updateUploadCounters(projectDir, attempts, succeeded)
	}
	return succeeded
}

// UploadPendingInBackground retries project-local checkpoint uploads and home legacy uploads.
func UploadPendingInBackground(apiURL, apiToken, projectDir string, timeout time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if strings.TrimSpace(projectDir) != "" {
			RetryProjectUploads(ctx, apiURL, apiToken, projectDir)
		}
		RetryPendingUploads(ctx, apiURL, apiToken)
	}()
	return done
}

// UploadInBackground preserves the legacy final-record upload path.
func UploadInBackground(apiURL, apiToken string, record BuildRecord, timeout time.Duration) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		RetryPendingUploads(ctx, apiURL, apiToken)
		if _, err := UploadExperience(ctx, apiURL, apiToken, record); err != nil {
			if cacheErr := CachePendingUpload(record); cacheErr != nil {
				fmt.Fprintf(os.Stderr, "fry: warning: failed to cache experience upload: %v\n", cacheErr)
			}
		}
	}()
	return done
}

func newLifecycleUpload(record BuildRecord, sequence int, eventType UploadEventType) UploadEnvelope {
	summary := record.Summary
	if strings.TrimSpace(summary) == "" {
		summary = lifecycleSummaryFallback(record, eventType)
	}
	return UploadEnvelope{
		SchemaVersion:  2,
		ID:             uploadEventID(record.SessionID, sequence, eventType),
		EventType:      eventType,
		SessionID:      record.SessionID,
		Sequence:       sequence,
		SourceInstance: InstanceID(),
		Timestamp:      time.Now().UTC(),
		BuildMetadata: BuildMetadata{
			Engine:       record.Engine,
			EffortLevel:  record.EffortLevel,
			TotalSprints: record.TotalSprints,
			Outcome:      record.Outcome,
		},
		SummaryText: summary,
	}
}

func lifecycleSummaryFallback(record BuildRecord, eventType UploadEventType) string {
	switch eventType {
	case UploadEventSessionStarted:
		return fmt.Sprintf("Build started: %d sprints, %s effort, %s engine", record.TotalSprints, record.EffortLevel, record.Engine)
	case UploadEventSessionInterrupted:
		return fmt.Sprintf("Build interrupted: %d sprints, %s effort, %s engine", record.TotalSprints, record.EffortLevel, record.Engine)
	case UploadEventSessionCompleted:
		return fmt.Sprintf("Build completed: %d sprints, %s effort, %s engine, outcome=%s", record.TotalSprints, record.EffortLevel, record.Engine, record.Outcome)
	default:
		return string(eventType)
	}
}

func newCheckpointUpload(record BuildRecord, summary CheckpointSummary) UploadEnvelope {
	return UploadEnvelope{
		SchemaVersion:  2,
		ID:             uploadEventID(record.SessionID, summary.Sequence, UploadEventCheckpointSummary),
		EventType:      UploadEventCheckpointSummary,
		SessionID:      record.SessionID,
		Sequence:       summary.Sequence,
		SourceInstance: InstanceID(),
		Timestamp:      summary.Timestamp,
		BuildMetadata: BuildMetadata{
			Engine:       record.Engine,
			EffortLevel:  record.EffortLevel,
			TotalSprints: record.TotalSprints,
			Outcome:      record.Outcome,
		},
		SummaryText:       summary.Summary,
		CheckpointSummary: &summary,
	}
}

func updateUploadCounters(projectDir string, attempts, successes int) {
	if attempts == 0 && successes == 0 {
		return
	}
	path := filepath.Join(projectDir, config.ConsciousnessSessionFile)
	state, err := loadSessionState(path)
	if err != nil {
		return
	}
	state.UploadAttempts += attempts
	state.UploadSuccesses += successes
	state.PendingUploads = countJSONFiles(filepath.Join(projectDir, config.ConsciousnessUploadQueueDir))
	state.LastUpdatedAt = time.Now().UTC()
	_ = writeJSONAtomic(path, state)
}

func payloadType(data []byte) string {
	var probe struct {
		SchemaVersion int    `json:"schema_version"`
		EventType     string `json:"event_type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "legacy"
	}
	if probe.SchemaVersion > 0 || strings.TrimSpace(probe.EventType) != "" {
		return "event"
	}
	return "legacy"
}

func uploadEventID(sessionID string, sequence int, eventType UploadEventType) string {
	return fmt.Sprintf("%s-%04d-%s", sessionID, sequence, eventType)
}

func eventFilename(event UploadEnvelope) string {
	return event.ID + ".json"
}

func countJSONFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			count++
		}
	}
	return count
}
