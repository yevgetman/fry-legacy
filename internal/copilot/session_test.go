package copilot

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var uuidV4Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewSessionUUIDFormat(t *testing.T) {
	t.Parallel()

	for i := 0; i < 32; i++ {
		uuid, err := NewSessionUUID()
		require.NoError(t, err)
		assert.True(t, uuidV4Regex.MatchString(uuid), "uuid %q should be a valid v4 UUID", uuid)
	}
}

func TestNewSessionUUIDUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		uuid, err := NewSessionUUID()
		require.NoError(t, err)
		assert.False(t, seen[uuid], "uuid %q should not be a duplicate", uuid)
		seen[uuid] = true
	}
}

func TestCaptureSessionIDPreSpecified(t *testing.T) {
	t.Parallel()

	probe := EngineProbeResult{Engine: "claude", SessionIDFlag: true}
	uuid, mech, err := CaptureSessionID(probe)
	require.NoError(t, err)
	assert.Equal(t, SessionIDPreSpecified, mech)
	assert.True(t, uuidV4Regex.MatchString(uuid))
}

func TestCaptureSessionIDFallbackEmpty(t *testing.T) {
	t.Parallel()

	probe := EngineProbeResult{Engine: "claude", SessionIDFlag: false}
	uuid, mech, err := CaptureSessionID(probe)
	require.NoError(t, err)
	assert.Equal(t, "", uuid)
	assert.Equal(t, SessionIDNone, mech)
}

func TestParseSessionIDFromStdout(t *testing.T) {
	t.Parallel()

	stream := strings.NewReader(`{"type":"start"}
{"type":"progress","data":"thinking"}
{"type":"result","session_id":"4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c","result":"done"}
`)

	id, err := ParseSessionIDFromStdout(stream, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c", id)
}

func TestParseSessionIDFromStdoutEmptyEOF(t *testing.T) {
	t.Parallel()

	stream := strings.NewReader(`{"type":"start"}
{"type":"end"}
`)

	id, err := ParseSessionIDFromStdout(stream, 5*time.Second)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, "", id)
}

func TestParseSessionIDFromStdoutFirstWins(t *testing.T) {
	t.Parallel()

	stream := strings.NewReader(`{"type":"chunk","session_id":"first-id"}
{"type":"chunk","session_id":"second-id"}
`)

	id, err := ParseSessionIDFromStdout(stream, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "first-id", id)
}

func TestWatchProjectsForNewSessionFindsFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	subdir := filepath.Join(root, "fake-project-hash")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	// Mark the "before spawn" time, then create the new file.
	since := time.Now().Add(-1 * time.Second)

	sessionUUID := "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c"
	jsonlPath := filepath.Join(subdir, sessionUUID+".jsonl")
	require.NoError(t, os.WriteFile(jsonlPath, []byte(`{"type":"start"}`), 0o644))

	got, err := WatchProjectsForNewSession(root, since, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, sessionUUID, got)
}

func TestWatchProjectsForNewSessionPicksNewest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	subdir := filepath.Join(root, "fake-project-hash")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	since := time.Now().Add(-1 * time.Second)

	older := filepath.Join(subdir, "older-uuid.jsonl")
	require.NoError(t, os.WriteFile(older, []byte(`{}`), 0o644))
	// Set older mtime to be slightly in the past.
	pastTime := time.Now().Add(-100 * time.Millisecond)
	require.NoError(t, os.Chtimes(older, pastTime, pastTime))

	// Sleep just enough that the second file's mtime is unambiguously newer.
	time.Sleep(50 * time.Millisecond)
	newer := filepath.Join(subdir, "newer-uuid.jsonl")
	require.NoError(t, os.WriteFile(newer, []byte(`{}`), 0o644))

	got, err := WatchProjectsForNewSession(root, since, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "newer-uuid", got)
}

func TestWatchProjectsForNewSessionTimesOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	since := time.Now()

	_, err := WatchProjectsForNewSession(root, since, 200*time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestProbeClaudeCapabilities_UnexpectedOutput(t *testing.T) {
	// Cannot use t.Parallel() — t.Setenv (required for PATH injection to
	// provide a fake "claude" binary) is incompatible with t.Parallel() in Go.

	// Create a fake "claude" script that emits unexpected garbage output.
	tmpBin := t.TempDir()
	scriptPath := filepath.Join(tmpBin, "claude")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho \"unexpected garbage output xyz 999\"\nexit 0\n"), 0o755)
	require.NoError(t, err)

	// Prepend fake binary directory to PATH so ProbeClaudeCapabilities finds it.
	t.Setenv("PATH", tmpBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Must not panic; all capability flags should be false (safe defaults).
	var result EngineProbeResult
	require.NotPanics(t, func() {
		result = ProbeClaudeCapabilities()
	})

	assert.Equal(t, "claude", result.Engine)
	assert.False(t, result.SessionIDFlag, "unexpected output should not set SessionIDFlag")
	assert.False(t, result.OutputJSON, "unexpected output should not set OutputJSON")
}

func TestWatchProjectsIgnoresOldFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	subdir := filepath.Join(root, "fake-project-hash")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	// File created BEFORE the spawn time should not be picked up.
	old := filepath.Join(subdir, "stale-uuid.jsonl")
	require.NoError(t, os.WriteFile(old, []byte(`{}`), 0o644))
	pastTime := time.Now().Add(-1 * time.Hour)
	require.NoError(t, os.Chtimes(old, pastTime, pastTime))

	since := time.Now() // spawn happens now

	_, err := WatchProjectsForNewSession(root, since, 200*time.Millisecond)
	require.Error(t, err, "should not find stale files")
}
