package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func TestArchiveSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("epic content"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildAuditFile), []byte("audit"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.SummaryFile), []byte("summary"), 0o644))

	dest, err := Archive(dir)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(filepath.Base(dest), config.ArchivePrefix))

	_, err = os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be gone after archive")

	_, err = os.Stat(filepath.Join(dir, config.BuildAuditFile))
	assert.True(t, os.IsNotExist(err), "build-audit.md should be gone from root")

	_, err = os.Stat(filepath.Join(dir, config.SummaryFile))
	assert.True(t, os.IsNotExist(err), "build-summary.md should be gone from root")

	data, err := os.ReadFile(filepath.Join(dest, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "epic content", string(data))

	data, err = os.ReadFile(filepath.Join(dest, config.BuildAuditFile))
	require.NoError(t, err)
	assert.Equal(t, "audit", string(data))

	data, err = os.ReadFile(filepath.Join(dest, config.SummaryFile))
	require.NoError(t, err)
	assert.Equal(t, "summary", string(data))
}

func TestArchiveDoesNotTouchFryConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create .fry/ (build artifacts) and .fry-config/ (persistent config)
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("epic"), 0o644))

	configDir := filepath.Join(dir, config.FryConfigDir)
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.CodebaseFile), []byte("codebase"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.ProjectConfigFile), []byte(`{"engine":"claude"}`), 0o644))

	_, err := Archive(dir)
	require.NoError(t, err)

	// .fry-config/ and its contents must survive archival
	data, err := os.ReadFile(filepath.Join(dir, config.CodebaseFile))
	require.NoError(t, err)
	assert.Equal(t, "codebase", string(data))

	data, err = os.ReadFile(filepath.Join(dir, config.ProjectConfigFile))
	require.NoError(t, err)
	assert.Equal(t, `{"engine":"claude"}`, string(data))
}

func TestArchiveNoFryDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := Archive(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestArchiveNoRootFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("content"), 0o644))

	dest, err := Archive(dir)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dest, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))

	_, err = os.Stat(filepath.Join(dest, config.BuildAuditFile))
	assert.True(t, os.IsNotExist(err), "no audit file should exist in archive")

	_, err = os.Stat(filepath.Join(dest, config.SummaryFile))
	assert.True(t, os.IsNotExist(err), "no summary file should exist in archive")
}

func TestArchivePartialRootFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("epic"), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(dir, config.SummaryFile), []byte("summary only"), 0o644))

	dest, err := Archive(dir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, config.SummaryFile))
	assert.True(t, os.IsNotExist(err), "summary should be moved")

	data, err := os.ReadFile(filepath.Join(dest, config.SummaryFile))
	require.NoError(t, err)
	assert.Equal(t, "summary only", string(data))

	_, err = os.Stat(filepath.Join(dest, config.BuildAuditFile))
	assert.True(t, os.IsNotExist(err), "no audit file should exist")
}

func TestArchiveCreatesArchiveDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("content"), 0o644))

	archiveDir := filepath.Join(dir, config.ArchiveDir)
	_, err := os.Stat(archiveDir)
	require.True(t, os.IsNotExist(err), ".fry-archive/ should not exist before call")

	_, err = Archive(dir)
	require.NoError(t, err)

	info, err := os.Stat(archiveDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), ".fry-archive/ should be a directory")
}

func TestArchiveIncludesSARIF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("epic"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildAuditSARIFFile), []byte("sarif content"), 0o644))

	dest, err := Archive(dir)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, config.BuildAuditSARIFFile))
	assert.True(t, os.IsNotExist(err), "build-audit.sarif should be gone from root")

	data, err := os.ReadFile(filepath.Join(dest, config.BuildAuditSARIFFile))
	require.NoError(t, err)
	assert.Equal(t, "sarif content", string(data))
}

func TestArchiveToDifferentDir(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	destDir := t.TempDir()

	// Create .fry/ and root artifacts in srcDir
	fryDir := filepath.Join(srcDir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("worktree epic"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, config.BuildAuditFile), []byte("audit"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, config.SummaryFile), []byte("summary"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, config.BuildAuditSARIFFile), []byte("sarif"), 0o644))

	dest, err := ArchiveTo(srcDir, destDir)
	require.NoError(t, err)

	// Archive should live under destDir, not srcDir
	assert.True(t, strings.HasPrefix(dest, filepath.Join(destDir, config.ArchiveDir)))

	// srcDir's .fry/ should be gone
	_, err = os.Stat(filepath.Join(srcDir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be gone from srcDir")

	// srcDir's root artifacts should be gone
	for _, name := range []string{config.BuildAuditFile, config.SummaryFile, config.BuildAuditSARIFFile} {
		_, err = os.Stat(filepath.Join(srcDir, name))
		assert.True(t, os.IsNotExist(err), "%s should be gone from srcDir", name)
	}

	// Everything should be in the archive under destDir
	data, err := os.ReadFile(filepath.Join(dest, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "worktree epic", string(data))

	data, err = os.ReadFile(filepath.Join(dest, config.BuildAuditFile))
	require.NoError(t, err)
	assert.Equal(t, "audit", string(data))

	data, err = os.ReadFile(filepath.Join(dest, config.SummaryFile))
	require.NoError(t, err)
	assert.Equal(t, "summary", string(data))

	data, err = os.ReadFile(filepath.Join(dest, config.BuildAuditSARIFFile))
	require.NoError(t, err)
	assert.Equal(t, "sarif", string(data))
}

func TestArchiveToSameDirMatchesArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("content"), 0o644))

	dest, err := ArchiveTo(dir, dir)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(dest, filepath.Join(dir, config.ArchiveDir)))

	_, err = os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be gone")

	data, err := os.ReadFile(filepath.Join(dest, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))
}

func TestArchiveMultipleBuilds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("build 1"), 0o644))

	dest1, err := Archive(dir)
	require.NoError(t, err)

	// Small delay to ensure different timestamp
	time.Sleep(1100 * time.Millisecond)

	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("build 2"), 0o644))

	dest2, err := Archive(dir)
	require.NoError(t, err)

	assert.NotEqual(t, dest1, dest2, "archive paths should differ")

	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 2, len(entries), "should have two archive entries")

	data1, err := os.ReadFile(filepath.Join(dest1, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "build 1", string(data1))

	data2, err := os.ReadFile(filepath.Join(dest2, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "build 2", string(data2))
}
