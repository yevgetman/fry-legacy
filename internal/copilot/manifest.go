// Package copilot implements the `fry run --copilot` feature: a parallel
// persistent agent session that monitors an active fry build via cron-driven
// wakes and intervenes to fix canonical fry bugs (in the fry source tree) or
// broken build artifacts (in the build's working tree).
//
// See docs/copilot.md for the full feature documentation.
package copilot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yevgetman/fry/internal/config"
)

// ManifestVersion is the schema version for manifest.json. Bump on breaking
// schema changes; readers should reject mismatched versions.
const ManifestVersion = 1

// Mode describes the active behavior of a copilot session.
type Mode string

const (
	// ModeActive means the copilot may intervene (edit fry source, edit
	// build artifacts, run shell commands, commit/push, restart builds).
	ModeActive Mode = "active"

	// ModePassive means the copilot only observes — emits events and writes
	// the final summary, but does not intervene. Used when fry source dir
	// resolution fails or when --copilot-passive is set.
	ModePassive Mode = "passive"

	// ModeDryRun means the copilot is configured but no subprocess was
	// spawned. Used by `fry run --copilot --dry-run`.
	ModeDryRun Mode = "dry_run"
)

// SessionIDCaptureMechanism records which of the three capture strategies
// produced the session ID for this run. Useful for debugging and telemetry.
type SessionIDCaptureMechanism string

const (
	// SessionIDPreSpecified — fry generated a UUID upfront and passed it via
	// `claude --session-id <uuid>`. Zero latency. Preferred when supported.
	SessionIDPreSpecified SessionIDCaptureMechanism = "pre_specified"

	// SessionIDParseStdout — fry parsed `{"type":"result","session_id":"..."}`
	// from the bootstrap subprocess's stdout (--output-format json).
	SessionIDParseStdout SessionIDCaptureMechanism = "parse_stdout"

	// SessionIDWatchDir — fry watched ~/.claude/projects/<hash>/ for new
	// JSONL files after the bootstrap subprocess started. Last-resort.
	SessionIDWatchDir SessionIDCaptureMechanism = "watch_dir"

	// SessionIDNone — no session ID has been captured yet (manifest written
	// before subprocess spawn).
	SessionIDNone SessionIDCaptureMechanism = ""
)

// EngineCapabilities records what the resolved copilot engine supports.
// Probed once at bootstrap and cached in the manifest so subcommands like
// `fry copilot status` and `fry copilot attach` know what the agent runtime
// can do without re-probing.
type EngineCapabilities struct {
	SessionIDFlag bool `json:"session_id_flag"`
	CronCreate    bool `json:"cron_create"`
	RemoteTrigger bool `json:"remote_trigger"`
}

// Manifest is the persistent record of a copilot session, written to
// .fry/copilot/manifest.json. It is the source of truth that subcommands
// (status, attach, stop, tail) read to find the running copilot.
type Manifest struct {
	Version                   int                       `json:"version"`
	SessionID                 string                    `json:"session_id"`
	CronID                    string                    `json:"cron_id"`
	BuildPID                  int                       `json:"build_pid"`
	BuildDir                  string                    `json:"build_dir"`
	FrySourceDir              string                    `json:"fry_source_dir"`
	Engine                    string                    `json:"engine"`
	Model                     string                    `json:"model"`
	StartedAt                 string                    `json:"started_at"`
	Interval                  string                    `json:"interval"`
	EpicName                  string                    `json:"epic_name"`
	EffortLevel               string                    `json:"effort_level"`
	MaxInterventionsPerClass  int                       `json:"max_interventions_per_class"`
	StopOnBuildComplete       bool                      `json:"stop_on_build_complete"`
	Mode                      Mode                      `json:"mode"`
	EngineCapabilities        EngineCapabilities        `json:"engine_capabilities"`
	SessionIDCaptureMechanism SessionIDCaptureMechanism `json:"session_id_capture_mechanism"`
}

// WriteManifest writes the manifest to .fry/copilot/manifest.json under
// projectDir, creating the directory if needed. Writes are atomic via
// rename so partial reads cannot observe a half-written file.
func WriteManifest(projectDir string, m *Manifest) error {
	if m == nil {
		return fmt.Errorf("write manifest: nil manifest")
	}
	if m.Version == 0 {
		m.Version = ManifestVersion
	}
	dir := filepath.Join(projectDir, config.CopilotDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("write manifest: create dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("write manifest: marshal: %w", err)
	}
	finalPath := filepath.Join(projectDir, config.CopilotManifestFile)
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: write tmp: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write manifest: rename: %w", err)
	}
	return nil
}

// ReadManifest reads .fry/copilot/manifest.json from projectDir. Returns
// (nil, nil) if the file does not exist — callers should treat that as
// "no copilot configured for this build."
func ReadManifest(projectDir string) (*Manifest, error) {
	path := filepath.Join(projectDir, config.CopilotManifestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("read manifest: parse: %w", err)
	}
	if m.Version != ManifestVersion {
		return nil, fmt.Errorf("read manifest: unsupported version %d (expected %d)", m.Version, ManifestVersion)
	}
	return &m, nil
}

// WriteSessionIDFile writes a one-line file containing the session UUID for
// convenience. The manifest is the source of truth; this file is a shortcut
// for shell scripts and `fry copilot attach`.
func WriteSessionIDFile(projectDir, sessionID string) error {
	path := filepath.Join(projectDir, config.CopilotSessionIDFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write session-id file: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(sessionID), 0o644); err != nil {
		return fmt.Errorf("write session-id file: %w", err)
	}
	return nil
}

// ReadSessionIDFile reads .fry/copilot/session-id.txt. Returns "" if
// missing. Trailing whitespace and newlines are stripped so the value can
// be passed directly to `claude --resume <id>`.
func ReadSessionIDFile(projectDir string) string {
	path := filepath.Join(projectDir, config.CopilotSessionIDFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteCronIDFile writes the cron-tool ID returned by CronCreate to
// .fry/copilot/cron.id. The agent calls this through `fry copilot emit-event`
// or via direct file write inside its bootstrap step.
func WriteCronIDFile(projectDir, cronID string) error {
	path := filepath.Join(projectDir, config.CopilotCronIDFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write cron-id file: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(cronID), 0o644); err != nil {
		return fmt.Errorf("write cron-id file: %w", err)
	}
	return nil
}

// ReadCronIDFile reads .fry/copilot/cron.id. Returns "" if missing.
// Trailing whitespace and newlines are stripped so a file containing
// only "\n" reads as empty (and therefore "no cron").
func ReadCronIDFile(projectDir string) string {
	path := filepath.Join(projectDir, config.CopilotCronIDFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
