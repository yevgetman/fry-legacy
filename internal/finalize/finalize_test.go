package finalize

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/git"
)

// setupRepo creates a minimal git repo with one commit and returns its path.
func setupRepo(t *testing.T, ctx context.Context) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("init"), 0o644))
	run("add", "README.md")
	run("commit", "-m", "initial")
	return dir
}

// createFryDir creates a .fry/ directory with a sample file.
func createFryDir(t *testing.T, dir string) {
	t.Helper()
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("epic"), 0o644))
}

func TestFinalizeSuccess_CurrentStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)
	createFryDir(t, dir)

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup: &git.StrategySetup{
			WorkDir:     dir,
			OriginalDir: dir,
			Strategy:    git.StrategyCurrent,
		},
		BuildErr: nil,
	})

	// .fry/ should be gone (archived)
	_, err := os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be archived")

	// .fry-archive/ should exist
	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 1, len(entries), "should have one archive entry")
}

func TestFinalizeFailure_CurrentStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)
	createFryDir(t, dir)

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup: &git.StrategySetup{
			WorkDir:     dir,
			OriginalDir: dir,
			Strategy:    git.StrategyCurrent,
		},
		BuildErr: errors.New("build failed"),
	})

	// .fry/ should still be archived even on failure
	_, err := os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be archived even on failure")

	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 1, len(entries))
}

func TestFinalizeSuccess_BranchStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}

	// Create fry branch with a commit
	branchName := "fry/test-feature"
	run("checkout", "-b", branchName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main"), 0o644))
	run("add", "feature.go")
	run("commit", "-m", "add feature")

	createFryDir(t, dir)

	setup := &git.StrategySetup{
		WorkDir:        dir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       git.StrategyBranch,
	}

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup:       setup,
		BuildErr:            nil,
	})

	// .fry/ should be archived
	_, err := os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be archived")

	// Should be back on main
	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = dir
	out, _ := cmd.Output()
	assert.Equal(t, "main", strings.TrimSpace(string(out)))

	// Feature should be merged into main
	_, err = os.Stat(filepath.Join(dir, "feature.go"))
	assert.NoError(t, err, "feature.go should exist on main after merge")

	// Fry branch should be deleted
	cmd = exec.CommandContext(ctx, "git", "branch", "--list", branchName)
	cmd.Dir = dir
	out, _ = cmd.Output()
	assert.Empty(t, strings.TrimSpace(string(out)), "fry branch should be deleted")
}

func TestFinalizeFailure_BranchStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}

	branchName := "fry/broken-feature"
	run("checkout", "-b", branchName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.go"), []byte("broken"), 0o644))
	run("add", "broken.go")
	run("commit", "-m", "broken commit")

	createFryDir(t, dir)

	setup := &git.StrategySetup{
		WorkDir:        dir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       git.StrategyBranch,
	}

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup:       setup,
		BuildErr:            errors.New("build failed"),
	})

	// .fry/ should be archived
	_, err := os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err), ".fry/ should be archived")

	// Should be back on main
	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = dir
	out, _ := cmd.Output()
	assert.Equal(t, "main", strings.TrimSpace(string(out)))

	// Fry branch should be PRESERVED for inspection
	cmd = exec.CommandContext(ctx, "git", "branch", "--list", branchName)
	cmd.Dir = dir
	out, _ = cmd.Output()
	assert.NotEmpty(t, strings.TrimSpace(string(out)), "fry branch should be preserved")
}

func TestFinalizeSuccess_WorktreeStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}

	// Create worktree
	branchName := "fry/wt-feature"
	wtDir := filepath.Join(dir, config.GitWorktreeDir, "wt-feature")
	require.NoError(t, os.MkdirAll(filepath.Dir(wtDir), 0o755))
	run("worktree", "add", "-b", branchName, wtDir)

	// Add commit in worktree
	require.NoError(t, os.WriteFile(filepath.Join(wtDir, "output.md"), []byte("essay"), 0o644))
	wtRun := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = wtDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}
	wtRun("add", "output.md")
	wtRun("commit", "-m", "add output")

	// Create .fry/ in worktree
	createFryDir(t, wtDir)
	// Also create stale .fry/ in original dir (simulating worktree build writes)
	origFryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(origFryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.GitStrategyFile), []byte("strategy=worktree"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.BuildPhaseFile), []byte("sprint:worktree"), 0o644))

	setup := &git.StrategySetup{
		WorkDir:        wtDir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       git.StrategyWorktree,
		IsWorktree:     true,
	}

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         wtDir,
		OriginalProjectPath: dir,
		StrategySetup:       setup,
		BuildErr:            nil,
	})

	// Archive should be in original dir
	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 1, len(entries), "archive should be in original dir")

	// Stale original .fry/ files should be cleaned
	_, err = os.Stat(filepath.Join(dir, config.GitStrategyFile))
	assert.True(t, os.IsNotExist(err), "stale git-strategy.txt should be removed")
	_, err = os.Stat(filepath.Join(dir, config.BuildPhaseFile))
	assert.True(t, os.IsNotExist(err), "stale build-phase.txt should be removed")

	// output.md should exist on main after merge
	_, err = os.Stat(filepath.Join(dir, "output.md"))
	assert.NoError(t, err, "output.md should exist on main after merge")

	// Strategy should be marked cleaned up
	assert.True(t, setup.IsCleanedUp(), "strategy should be marked cleaned up")
}

func TestFinalizeFailure_WorktreeStrategy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := setupRepo(t, ctx)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}

	branchName := "fry/broken-wt"
	wtDir := filepath.Join(dir, config.GitWorktreeDir, "broken-wt")
	require.NoError(t, os.MkdirAll(filepath.Dir(wtDir), 0o755))
	run("worktree", "add", "-b", branchName, wtDir)

	createFryDir(t, wtDir)

	setup := &git.StrategySetup{
		WorkDir:        wtDir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       git.StrategyWorktree,
		IsWorktree:     true,
	}

	Finalize(ctx, FinalizeOpts{
		ProjectPath:         wtDir,
		OriginalProjectPath: dir,
		StrategySetup:       setup,
		BuildErr:            errors.New("build failed"),
	})

	// Archive should be in original dir
	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 1, len(entries))

	// Worktree dir should be removed
	_, err = os.Stat(wtDir)
	assert.True(t, os.IsNotExist(err), "worktree dir should be removed")

	// Branch should be preserved
	cmd := exec.CommandContext(ctx, "git", "branch", "--list", branchName)
	cmd.Dir = dir
	out, _ := cmd.Output()
	assert.NotEmpty(t, strings.TrimSpace(string(out)), "branch should be preserved for inspection")

	// Strategy should be marked cleaned up
	assert.True(t, setup.IsCleanedUp(), "strategy should be marked cleaned up")
}

func TestFinalizeNilStrategy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	createFryDir(t, dir)

	Finalize(context.Background(), FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup:       nil,
		BuildErr:            nil,
	})

	// .fry/ should be archived
	_, err := os.Stat(filepath.Join(dir, config.FryDir))
	assert.True(t, os.IsNotExist(err))

	entries, err := os.ReadDir(filepath.Join(dir, config.ArchiveDir))
	require.NoError(t, err)
	assert.Equal(t, 1, len(entries))
}

func TestFinalizeNoFryDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Should not panic when .fry/ doesn't exist
	Finalize(context.Background(), FinalizeOpts{
		ProjectPath:         dir,
		OriginalProjectPath: dir,
		StrategySetup:       nil,
		BuildErr:            errors.New("early exit"),
	})

	// No archive created
	_, err := os.Stat(filepath.Join(dir, config.ArchiveDir))
	assert.True(t, os.IsNotExist(err))
}
