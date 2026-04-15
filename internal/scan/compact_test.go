package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
)

type compactStubEngine struct {
	output string
}

func (s *compactStubEngine) Run(_ context.Context, _ string, _ engine.RunOpts) (string, int, error) {
	return s.output, 0, nil
}
func (s *compactStubEngine) Name() string { return "stub" }

// compactSabotageEngine replaces memoriesDir with a regular file during Run,
// causing the second os.ReadDir in CompactMemories to fail.
type compactSabotageEngine struct {
	output      string
	memoriesDir string
}

func (s *compactSabotageEngine) Run(_ context.Context, _ string, _ engine.RunOpts) (string, int, error) {
	_ = os.RemoveAll(s.memoriesDir)
	_ = os.WriteFile(s.memoriesDir, []byte("not a dir"), 0o644)
	return s.output, 0, nil
}
func (s *compactSabotageEngine) Name() string { return "sabotage-stub" }

func TestCompactMemories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	memoriesDir := filepath.Join(dir, config.CodebaseMemoriesDir)
	require.NoError(t, os.MkdirAll(memoriesDir, 0o755))

	// Create 55 memories (above MaxMemoryCount of 50).
	for i := 1; i <= 55; i++ {
		m := Memory{
			Filename:   fmt.Sprintf("%03d-test.md", i),
			Confidence: "medium",
			Body:       fmt.Sprintf("Learning number %d about the codebase.", i),
			Date:       "2026-03-29",
			Reinforced: 0,
		}
		if i <= 5 {
			m.Confidence = "high"
			m.Reinforced = 3
		}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}

	// Verify we have 55 before compaction.
	before, _ := LoadMemories(memoriesDir)
	require.Equal(t, 55, len(before))

	// Engine returns 20 compacted memories.
	var compactedOutput string
	for i := 1; i <= config.CompactedMemoryCount; i++ {
		conf := "medium"
		if i <= 5 {
			conf = "high"
		}
		compactedOutput += fmt.Sprintf("MEMORY: <confidence: %s>\nCompacted learning %d.\n---\n", conf, i)
	}

	eng := &compactStubEngine{output: compactedOutput}

	err := CompactMemories(context.Background(), dir, eng, "haiku", "")
	require.NoError(t, err)

	// Verify compacted count.
	after, _ := LoadMemories(memoriesDir)
	assert.Equal(t, config.CompactedMemoryCount, len(after))
}

func TestCompactMemories_BelowThreshold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	memoriesDir := filepath.Join(dir, config.CodebaseMemoriesDir)
	require.NoError(t, os.MkdirAll(memoriesDir, 0o755))

	// Create 10 memories (below threshold).
	for i := 1; i <= 10; i++ {
		m := Memory{
			Filename:   fmt.Sprintf("%03d-test.md", i),
			Confidence: "medium",
			Body:       fmt.Sprintf("Learning %d.", i),
			Date:       "2026-03-29",
		}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}

	eng := &compactStubEngine{output: "should not be called"}

	// Should be a no-op.
	err := CompactMemories(context.Background(), dir, eng, "haiku", "")
	assert.NoError(t, err)

	// Count unchanged.
	after, _ := LoadMemories(memoriesDir)
	assert.Equal(t, 10, len(after))
}

func TestCompactMemories_NoEngine(t *testing.T) {
	t.Parallel()

	err := CompactMemories(context.Background(), t.TempDir(), nil, "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestCompactMemories_ReadDirError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	memoriesDir := filepath.Join(dir, config.CodebaseMemoriesDir)
	require.NoError(t, os.MkdirAll(memoriesDir, 0o755))

	// Create >MaxMemoryCount memories so LoadMemories succeeds and the
	// threshold check passes.
	for i := 1; i <= config.MaxMemoryCount+5; i++ {
		m := Memory{
			Filename:   fmt.Sprintf("%03d-test.md", i),
			Confidence: "medium",
			Body:       fmt.Sprintf("Learning %d.", i),
			Date:       "2026-03-29",
		}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}

	// Build valid compacted output so the engine succeeds.
	var compactedOutput string
	for i := 1; i <= config.CompactedMemoryCount; i++ {
		compactedOutput += fmt.Sprintf("MEMORY: <confidence: medium>\nCompacted %d.\n---\n", i)
	}

	// Use an engine that replaces the memoriesDir with a regular file during
	// Run. This happens after LoadMemories (line 23) but before the second
	// os.ReadDir (line 64), so only line 64 sees the error.
	eng := &compactSabotageEngine{output: compactedOutput, memoriesDir: memoriesDir}
	err := CompactMemories(context.Background(), dir, eng, "haiku", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compact memories: list dir:")
}

func TestCompactMemories_WriteError(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("test requires non-root user (root bypasses permission checks)")
	}

	dir := t.TempDir()
	memoriesDir := filepath.Join(dir, config.CodebaseMemoriesDir)
	require.NoError(t, os.MkdirAll(memoriesDir, 0o755))

	// Create >MaxMemoryCount memories so LoadMemories succeeds and threshold passes.
	for i := 1; i <= config.MaxMemoryCount+5; i++ {
		m := Memory{
			Filename:   fmt.Sprintf("%03d-test.md", i),
			Confidence: "medium",
			Body:       fmt.Sprintf("Learning %d.", i),
			Date:       "2026-03-29",
		}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}

	// Build valid compacted output.
	var compactedOutput string
	for i := 1; i <= config.CompactedMemoryCount; i++ {
		compactedOutput += fmt.Sprintf("MEMORY: <confidence: medium>\nCompacted %d.\n---\n", i)
	}

	// Restore permissions for cleanup.
	t.Cleanup(func() {
		_ = os.Chmod(memoriesDir, 0o755)
	})

	// Use a sabotage engine that also makes the dir read-only after clearing,
	// so the write phase fails.
	eng := &compactWriteFailEngine{output: compactedOutput, memoriesDir: memoriesDir}
	err := CompactMemories(context.Background(), dir, eng, "haiku", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory writes failed")
}

// compactWriteFailEngine deletes all memory files then makes the directory
// read-only, causing the write phase to fail.
type compactWriteFailEngine struct {
	output      string
	memoriesDir string
}

func (s *compactWriteFailEngine) Run(_ context.Context, _ string, _ engine.RunOpts) (string, int, error) {
	// Make dir read-only so writes will fail after the delete phase runs.
	entries, _ := os.ReadDir(s.memoriesDir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(s.memoriesDir, e.Name()))
	}
	_ = os.Chmod(s.memoriesDir, 0o555)
	return s.output, 0, nil
}
func (s *compactWriteFailEngine) Name() string { return "write-fail-stub" }

func TestNeedsCompaction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	memoriesDir := filepath.Join(dir, config.CodebaseMemoriesDir)
	require.NoError(t, os.MkdirAll(memoriesDir, 0o755))

	// Below threshold.
	for i := 1; i <= 10; i++ {
		m := Memory{Filename: fmt.Sprintf("%03d-test.md", i), Confidence: "medium", Body: "x"}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}
	assert.False(t, NeedsCompaction(dir))

	// Push above threshold.
	for i := 11; i <= 55; i++ {
		m := Memory{Filename: fmt.Sprintf("%03d-test.md", i), Confidence: "medium", Body: "x"}
		require.NoError(t, writeMemoryFile(filepath.Join(memoriesDir, m.Filename), m))
	}
	assert.True(t, NeedsCompaction(dir))
}

func TestBuildCompactionPrompt(t *testing.T) {
	t.Parallel()

	memories := []Memory{
		{Confidence: "high", Reinforced: 3, Body: "Important fact."},
		{Confidence: "low", Reinforced: 0, Body: "Trivial detail."},
	}

	prompt := buildCompactionPrompt(memories)

	assert.Contains(t, prompt, "Memory Compaction")
	assert.Contains(t, prompt, "Important fact")
	assert.Contains(t, prompt, "Trivial detail")
	assert.Contains(t, prompt, "confidence: high")
	assert.Contains(t, prompt, "reinforced: 3")
	assert.Contains(t, prompt, fmt.Sprintf("%d", config.CompactedMemoryCount))
}
