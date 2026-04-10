package sprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestCompactSprintProgressIfNeeded_SmallFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, config.SprintProgressFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	content := "# Sprint 1: Setup — Progress\n\nSmall content.\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	CompactSprintProgressIfNeeded(dir)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(data), "small file should not be modified")
}

func TestCompactSprintProgressIfNeeded_LargeFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, config.SprintProgressFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

	// Build a file that exceeds MaxSprintProgressBytes
	var b strings.Builder
	b.WriteString("# Sprint 1: Big Sprint — Progress\n\n")
	for i := 0; i < 500; i++ {
		b.WriteString("## Iteration " + strings.Repeat("x", 50) + "\n")
		b.WriteString("Did some work on iteration.\n\n")
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))

	info, _ := os.Stat(path)
	require.Greater(t, info.Size(), int64(MaxSprintProgressBytes), "test file should exceed threshold")

	CompactSprintProgressIfNeeded(dir)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Should be compacted: smaller than original
	assert.Less(t, len(data), int(info.Size()), "compacted file should be smaller")

	// Should preserve the header
	assert.True(t, strings.HasPrefix(string(data), "# Sprint 1:"), "should preserve header")

	// Should have compaction notice
	assert.Contains(t, string(data), "Earlier progress entries compacted")

	// Should have the tail lines
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Header + blank + notice + blank + TailLinesAfterCompaction
	assert.LessOrEqual(t, len(lines), TailLinesAfterCompaction+5)
}

func TestCompactSprintProgressIfNeeded_NoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Should not panic when file doesn't exist
	CompactSprintProgressIfNeeded(dir)
}
