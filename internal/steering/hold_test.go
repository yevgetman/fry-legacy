package steering

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestWriteDecisionNeeded_RenameFailureCleansUpTmp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	fryDir := filepath.Join(dir, ".fry")
	require.NoError(t, os.MkdirAll(fryDir, 0o755))

	// Create a directory at the destination path so os.Rename fails
	// (cannot rename a file over a directory).
	decisionPath := filepath.Join(dir, config.DecisionNeededFile)
	require.NoError(t, os.MkdirAll(decisionPath, 0o755))

	err := WriteDecisionNeeded(dir, "prompt text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rename")

	// The .tmp file must not remain after the failed rename.
	tmpPath := decisionPath + ".tmp"
	_, statErr := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(statErr), "tmp file should be cleaned up after rename failure")
}
