package copilot

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// NewSessionUUID returns a freshly-generated v4 UUID suitable for passing
// to `claude --session-id <uuid>`. Uses crypto/rand directly to avoid
// adding a UUID library dependency (per CLAUDE.md: minimal deps).
//
// The generated UUID conforms to RFC 4122 v4 format:
//   xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx where y ∈ {8,9,a,b}
func NewSessionUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}
	// Set version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122

	hexBuf := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexBuf[0:8],
		hexBuf[8:12],
		hexBuf[12:16],
		hexBuf[16:20],
		hexBuf[20:32],
	), nil
}

// EngineProbeResult records what an engine CLI supports. Cached at the
// process level since the underlying engine binary is unlikely to change
// during a single fry run.
type EngineProbeResult struct {
	Engine        string
	Version       string
	SessionIDFlag bool
	OutputJSON    bool
}

// ProbeClaudeCapabilities runs `claude --help` and inspects its output to
// determine which session-related features are supported by the installed
// Claude Code binary. The result is cached in-memory.
//
// This is best-effort: if `claude --help` fails or its output cannot be
// parsed, all flags default to false and the caller falls back to the
// next session-ID capture mechanism.
func ProbeClaudeCapabilities() EngineProbeResult {
	result := EngineProbeResult{Engine: "claude"}

	cmd := exec.Command("claude", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return result
	}

	helpText := string(out)
	if strings.Contains(helpText, "--session-id") {
		result.SessionIDFlag = true
	}
	if strings.Contains(helpText, "--output-format") {
		result.OutputJSON = true
	}

	// Best-effort version detection.
	versionCmd := exec.Command("claude", "--version")
	if vOut, vErr := versionCmd.CombinedOutput(); vErr == nil {
		result.Version = strings.TrimSpace(string(vOut))
	}

	return result
}

// CaptureSessionID resolves a stable Claude Code session UUID using the
// three-mechanism fallback described in the implementation plan §2.4:
//
//  1. Pre-specified UUID — if --session-id is supported, generate a v4
//     UUID upfront and return it. The caller passes it to `claude
//     --session-id <uuid>` when spawning the bootstrap subprocess.
//
//  2. Parse from stdout — read the bootstrap subprocess's JSON stream
//     for a `{"type":"result","session_id":"..."}` event.
//
//  3. Watch ~/.claude/projects/ — record directory mtime before spawn,
//     poll for new .jsonl files after spawn, take the newest basename.
//
// CaptureSessionID itself only handles mechanism (1). Mechanisms (2) and (3)
// are exposed as separate helpers (ParseSessionIDFromStdout, WatchProjectsForNewSession)
// so the bootstrap launcher can invoke them at the right point in the spawn
// flow.
func CaptureSessionID(probe EngineProbeResult) (string, SessionIDCaptureMechanism, error) {
	if probe.SessionIDFlag {
		uuid, err := NewSessionUUID()
		if err != nil {
			return "", SessionIDNone, err
		}
		return uuid, SessionIDPreSpecified, nil
	}
	// Caller will use ParseSessionIDFromStdout or WatchProjectsForNewSession.
	return "", SessionIDNone, nil
}

// streamJSONEvent is the minimal shape we need to extract a session ID from
// the Claude Code --output-format json stream. The result event always
// contains a session_id field.
type streamJSONEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

// ParseSessionIDFromStdout reads from r line-by-line looking for a Claude
// Code stream-json event that includes a session_id field. Returns the
// session ID and stops reading once found, or returns "" and io.EOF if
// the stream ends without finding one.
//
// timeout bounds how long the parse can wait for input. The caller is
// expected to invoke ParseSessionIDFromStdout in a goroutine.
func ParseSessionIDFromStdout(r io.Reader, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	dec := json.NewDecoder(r)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("parse session id from stdout: timed out after %s", timeout)
		}
		var evt streamJSONEvent
		if err := dec.Decode(&evt); err != nil {
			if err == io.EOF {
				return "", io.EOF
			}
			// Skip malformed records — they may be partial writes.
			continue
		}
		if evt.SessionID != "" {
			return evt.SessionID, nil
		}
	}
}

// WatchProjectsForNewSession polls ~/.claude/projects/ recursively for
// .jsonl files that did not exist (or whose mtime was older than `since`)
// at the time the bootstrap subprocess was launched. The newest matching
// file's basename (without extension) is returned as the session UUID.
//
// projectsRoot defaults to "$HOME/.claude/projects" if empty.
// timeout bounds the polling. Polls every 250ms.
func WatchProjectsForNewSession(projectsRoot string, since time.Time, timeout time.Duration) (string, error) {
	if projectsRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("watch projects: %w", err)
		}
		projectsRoot = filepath.Join(home, ".claude", "projects")
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("watch projects: timed out after %s", timeout)
		}
		candidates, err := scanForNewSessionFiles(projectsRoot, since)
		if err == nil && len(candidates) > 0 {
			// Sort by mtime descending; newest wins.
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].modTime.After(candidates[j].modTime)
			})
			base := filepath.Base(candidates[0].path)
			ext := filepath.Ext(base)
			return strings.TrimSuffix(base, ext), nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

type sessionFileCandidate struct {
	path    string
	modTime time.Time
}

func scanForNewSessionFiles(root string, since time.Time) ([]sessionFileCandidate, error) {
	var found []sessionFileCandidate
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// Don't fail the walk on per-entry errors — projects dir may
			// have stale symlinks or unreadable subdirs.
			return nil
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info.ModTime().After(since) {
			found = append(found, sessionFileCandidate{path: path, modTime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}
