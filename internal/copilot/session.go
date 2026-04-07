package copilot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// claudeProbeTimeout bounds how long we wait for `claude --help` /
// `claude --version` to respond. A misbehaving binary should not be able
// to hang fry's bootstrap path.
const claudeProbeTimeout = 5 * time.Second

// NewSessionUUID returns a freshly-generated v4 UUID suitable for passing
// to `claude --session-id <uuid>`. Uses crypto/rand directly to avoid
// adding a UUID library dependency (per CLAUDE.md: minimal deps).
//
// The generated UUID conforms to RFC 4122 v4 format:
//
//	xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx where y ∈ {8,9,a,b}
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

	helpCtx, helpCancel := context.WithTimeout(context.Background(), claudeProbeTimeout)
	defer helpCancel()
	out, err := exec.CommandContext(helpCtx, "claude", "--help").CombinedOutput()
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
	verCtx, verCancel := context.WithTimeout(context.Background(), claudeProbeTimeout)
	defer verCancel()
	if vOut, vErr := exec.CommandContext(verCtx, "claude", "--version").CombinedOutput(); vErr == nil {
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
//
// Persistent decode errors (e.g., malformed JSON) cause a brief sleep
// rather than a busy loop, so a misbehaving stream cannot pin a CPU.
func ParseSessionIDFromStdout(r io.Reader, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	dec := json.NewDecoder(r)
	consecutiveErrors := 0
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("parse session id from stdout: timed out after %s", timeout)
		}
		var evt streamJSONEvent
		if err := dec.Decode(&evt); err != nil {
			if errors.Is(err, io.EOF) {
				return "", io.EOF
			}
			// Skip malformed records, but back off so we don't burn CPU
			// on a stream that's emitting nothing but errors.
			consecutiveErrors++
			if consecutiveErrors >= 8 {
				return "", fmt.Errorf("parse session id from stdout: too many consecutive decode errors: %w", err)
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		consecutiveErrors = 0
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
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Don't fail the walk on per-entry errors — projects dir may
			// have stale symlinks or unreadable subdirs.
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
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
