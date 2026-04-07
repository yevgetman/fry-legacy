package git

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
)

func TestParseGitStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    GitStrategy
		wantErr bool
	}{
		{"", StrategyAuto, false},
		{"auto", StrategyAuto, false},
		{"current", StrategyCurrent, false},
		{"branch", StrategyBranch, false},
		{"worktree", StrategyWorktree, false},
		{"invalid", "", true},
		{"BRANCH", "", true}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := ParseGitStrategy(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestGenerateBranchName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		epicName string
		want     string
	}{
		{"simple", "My Epic", "fry/my-epic"},
		{"special chars", "Epic's \"Name\" (v2)!", "fry/epic-s-name-v2"},
		{"empty", "", "fry/build"},
		{"long name", strings.Repeat("a", 100), "fry/" + strings.Repeat("a", 50)},
		{"numbers", "Sprint 42 Build", "fry/sprint-42-build"},
		{"hyphens collapse", "foo---bar", "fry/foo-bar"},
		{"leading trailing special", "  --hello--  ", "fry/hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := GenerateBranchName(tt.epicName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveAutoStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		complexity string
		want       GitStrategy
	}{
		{"SIMPLE", StrategyBranch},
		{"MODERATE", StrategyBranch},
		{"COMPLEX", StrategyWorktree},
		{"complex", StrategyWorktree},
		{"", StrategyBranch},
	}

	for _, tt := range tests {
		t.Run(tt.complexity, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ResolveAutoStrategy(tt.complexity))
		})
	}
}

func TestIsInsideGitRepo(t *testing.T) {
	t.Parallel()

	t.Run("inside repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		assert.True(t, IsInsideGitRepo(context.Background(), dir))
	})

	t.Run("outside repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.False(t, IsInsideGitRepo(context.Background(), dir))
	})

	t.Run("subdirectory of repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		sub := filepath.Join(dir, "subdir")
		require.NoError(t, os.MkdirAll(sub, 0o755))
		assert.True(t, IsInsideGitRepo(context.Background(), sub))
	})
}

func TestCurrentBranch(t *testing.T) {
	t.Parallel()

	t.Run("default branch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		branch := CurrentBranch(context.Background(), dir)
		assert.NotEmpty(t, branch)
	})

	t.Run("not a repo", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.Empty(t, CurrentBranch(context.Background(), dir))
	})
}

func TestSetupStrategy_Current(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyCurrent,
	})
	require.NoError(t, err)
	assert.Equal(t, dir, setup.WorkDir)
	assert.Equal(t, dir, setup.OriginalDir)
	assert.Equal(t, StrategyCurrent, setup.Strategy)
	assert.Empty(t, setup.BranchName)
	assert.False(t, setup.IsWorktree)
}

func TestSetupStrategy_Auto_Errors(t *testing.T) {
	t.Parallel()

	_, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: t.TempDir(),
		Strategy:   StrategyAuto,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolved before calling")
}

func TestSetupStrategy_Branch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/test-branch",
		EpicName:   "Test",
	})
	require.NoError(t, err)
	assert.Equal(t, dir, setup.WorkDir)
	assert.Equal(t, dir, setup.OriginalDir)
	assert.Equal(t, "fry/test-branch", setup.BranchName)
	assert.Equal(t, StrategyBranch, setup.Strategy)
	assert.False(t, setup.IsWorktree)

	// Verify we're on the new branch
	assert.Equal(t, "fry/test-branch", CurrentBranch(context.Background(), dir))
}

func TestSetupStrategy_Branch_AlreadyExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	// Create branch first
	_, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/existing",
	})
	require.NoError(t, err)

	// Switch back to original branch
	cmd := exec.Command("bash", "-c", "git checkout -")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	// Try to create same branch without ForceReuse
	_, err = SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/existing",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestSetupStrategy_Branch_ForceReuse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	// Create branch first
	_, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/reuse-me",
	})
	require.NoError(t, err)

	// Switch back
	cmd := exec.Command("bash", "-c", "git checkout -")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	// ForceReuse should succeed
	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/reuse-me",
		ForceReuse: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "fry/reuse-me", setup.BranchName)
	assert.Equal(t, "fry/reuse-me", CurrentBranch(context.Background(), dir))
}

func TestSetupStrategy_Branch_NoGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		BranchName: "fry/test",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "existing git repository")
}

func TestSetupStrategy_Worktree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	// Create .fry/ with an artifact to verify copying
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("# Epic\n"), 0o644))

	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyWorktree,
		BranchName: "fry/wt-test",
		EpicName:   "Test",
	})
	require.NoError(t, err)

	assert.NotEqual(t, dir, setup.WorkDir)
	assert.Equal(t, dir, setup.OriginalDir)
	assert.Equal(t, "fry/wt-test", setup.BranchName)
	assert.Equal(t, StrategyWorktree, setup.Strategy)
	assert.True(t, setup.IsWorktree)

	// Verify worktree directory exists and is a git repo
	assert.True(t, IsInsideGitRepo(context.Background(), setup.WorkDir))

	// Verify .fry/ was copied
	data, err := os.ReadFile(filepath.Join(setup.WorkDir, config.FryDir, "epic.md"))
	require.NoError(t, err)
	assert.Equal(t, "# Epic\n", string(data))

	// Cleanup
	require.NoError(t, setup.Cleanup())
}

func TestSetupStrategy_Worktree_LockFileNotCarried(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	// Plant a live .fry/.fry.lock containing the running process PID,
	// matching what fry main does at the start of a build before
	// SetupStrategy runs and creates the worktree.
	fryDir := filepath.Join(dir, config.FryDir)
	require.NoError(t, os.MkdirAll(fryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fryDir, "epic.md"), []byte("# Epic\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, config.LockFile), []byte("99999\n"), 0o644))

	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyWorktree,
		BranchName: "fry/wt-lock-test",
		EpicName:   "Test",
	})
	require.NoError(t, err)

	// The original .fry.lock should still be in the main checkout (the
	// fry main process owns it).
	_, err = os.Stat(filepath.Join(dir, config.LockFile))
	assert.NoError(t, err, "original lock file should still exist in main checkout")

	// The worktree's .fry/.fry.lock should NOT exist.
	_, err = os.Stat(filepath.Join(setup.WorkDir, config.LockFile))
	assert.True(t, os.IsNotExist(err),
		"worktree should NOT have a copied .fry.lock — got err: %v", err)

	// Other .fry/ files should still be copied normally.
	_, err = os.Stat(filepath.Join(setup.WorkDir, config.FryDir, "epic.md"))
	assert.NoError(t, err, "other .fry/ files should be copied")

	require.NoError(t, setup.Cleanup())
}

func TestSetupStrategy_Worktree_NoGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyWorktree,
		BranchName: "fry/test",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "existing git repository")
}

func TestSetupStrategy_BranchNameAutoGenerated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	setup, err := SetupStrategy(context.Background(), StrategyOpts{
		ProjectDir: dir,
		Strategy:   StrategyBranch,
		EpicName:   "My Cool Epic",
	})
	require.NoError(t, err)
	assert.Equal(t, "fry/my-cool-epic", setup.BranchName)
}

func TestDetectExistingSetup_Branch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	// Create a branch
	cmd := exec.Command("bash", "-c", "git checkout -b fry/detect-me && git checkout -")
	cmd.Dir = dir
	require.NoError(t, cmd.Run())

	setup, err := DetectExistingSetup(context.Background(), dir, "fry/detect-me")
	require.NoError(t, err)
	require.NotNil(t, setup)
	assert.Equal(t, StrategyBranch, setup.Strategy)
	assert.Equal(t, "fry/detect-me", setup.BranchName)
}

func TestDetectExistingSetup_Nothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))

	setup, err := DetectExistingSetup(context.Background(), dir, "fry/nonexistent")
	require.NoError(t, err)
	assert.Nil(t, setup)
}

func TestPersistAndReadStrategy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, config.FryDir), 0o755))

	original := &StrategySetup{
		WorkDir:     "/tmp/worktree",
		OriginalDir: dir,
		BranchName:  "fry/my-branch",
		Strategy:    StrategyWorktree,
		IsWorktree:  true,
	}

	require.NoError(t, PersistStrategy(dir, original))

	loaded, err := ReadPersistedStrategy(dir)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, original.Strategy, loaded.Strategy)
	assert.Equal(t, original.BranchName, loaded.BranchName)
	assert.Equal(t, original.WorkDir, loaded.WorkDir)
	assert.Equal(t, original.OriginalDir, loaded.OriginalDir)
	assert.True(t, loaded.IsWorktree)
}

func TestReadPersistedStrategy_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	setup, err := ReadPersistedStrategy(dir)
	require.NoError(t, err)
	assert.Nil(t, setup)
}

func TestSlugify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "hello world", "hello-world"},
		{"special chars", "Hello's World! (v2)", "hello-s-world-v2"},
		{"empty", "", ""},
		{"hyphens", "foo---bar", "foo-bar"},
		{"long", strings.Repeat("x", 100), strings.Repeat("x", 50)},
		{"trailing hyphens on truncation", strings.Repeat("ab-", 30), strings.TrimRight(strings.Repeat("ab-", 30)[:50], "-")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, slugify(tt.input))
		})
	}
}

func TestCopyDirIfExists(t *testing.T) {
	t.Parallel()

	t.Run("copies files recursively", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dest")

		require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0o644))

		require.NoError(t, copyDirIfExists(src, dst))

		data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "aaa", string(data))

		data, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
		require.NoError(t, err)
		assert.Equal(t, "bbb", string(data))
	})

	t.Run("src does not exist", func(t *testing.T) {
		t.Parallel()
		dst := filepath.Join(t.TempDir(), "dest")
		require.NoError(t, copyDirIfExists("/nonexistent/path", dst))
		_, err := os.Stat(dst)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestCleanup_Idempotent(t *testing.T) {
	t.Parallel()

	setup := &StrategySetup{
		WorkDir:    "/tmp/test",
		IsWorktree: true,
	}
	require.NoError(t, setup.Cleanup())
	require.NoError(t, setup.Cleanup()) // second call is no-op
}

func TestCleanup_Nil(t *testing.T) {
	t.Parallel()

	var setup *StrategySetup
	require.NoError(t, setup.Cleanup())
}

// #32: CheckoutBranchWith tests

func TestCheckoutBranchWith_Success(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		CheckoutFn: func(_ context.Context, _ string, _ string) error {
			return nil
		},
	}
	err := CheckoutBranchWith(context.Background(), t.TempDir(), "fry/my-branch", ex)
	require.NoError(t, err)
}

func TestCheckoutBranchWith_Error(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		CheckoutFn: func(_ context.Context, _ string, _ string) error {
			return errors.New("branch not found")
		},
	}
	err := CheckoutBranchWith(context.Background(), t.TempDir(), "fry/nonexistent", ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch not found")
}

func TestMergeAndCleanupWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	// Initialize a real git repo with an initial commit
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

	// Create a worktree with a branch
	branchName := "fry/test-feature"
	worktreeDir := filepath.Join(dir, ".fry-worktrees", "test-feature")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0o755))
	run("worktree", "add", "-b", branchName, worktreeDir)

	// Add a commit in the worktree
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, "output.md"), []byte("essay"), 0o644))
	wtRun := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = worktreeDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}
	wtRun("add", "output.md")
	wtRun("commit", "-m", "add output")

	// Create .fry/ artifacts in the worktree (simulating build completion)
	wtFryDir := filepath.Join(worktreeDir, ".fry")
	require.NoError(t, os.MkdirAll(wtFryDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, config.BuildStatusFile), []byte(`{"version":1,"build":{"status":"completed"}}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, config.EpicProgressFile), []byte("## Sprint 1: done\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, config.BuildPhaseFile), []byte("complete\n"), 0o644))

	// Write strategy file
	setup := &StrategySetup{
		WorkDir:        worktreeDir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       StrategyWorktree,
		IsWorktree:     true,
	}
	require.NoError(t, PersistStrategy(dir, setup))

	// Merge and cleanup
	err := MergeAndCleanupWorktree(ctx, setup)
	require.NoError(t, err)

	// Verify: output.md now exists on main
	_, err = os.Stat(filepath.Join(dir, "output.md"))
	assert.NoError(t, err, "output.md should exist on main after merge")

	// Verify: worktree is removed
	_, err = os.Stat(worktreeDir)
	assert.True(t, os.IsNotExist(err), "worktree directory should be removed")

	// Verify: branch is deleted
	cmd := exec.CommandContext(ctx, "git", "branch", "--list", branchName)
	cmd.Dir = dir
	out, _ := cmd.Output()
	assert.Empty(t, strings.TrimSpace(string(out)), "branch should be deleted")

	// Verify: strategy file is removed
	_, err = os.Stat(filepath.Join(dir, config.GitStrategyFile))
	assert.True(t, os.IsNotExist(err), "git-strategy.txt should be removed")

	// Verify: .fry/ artifacts were copied from worktree to original dir
	statusData, err := os.ReadFile(filepath.Join(dir, config.BuildStatusFile))
	require.NoError(t, err, "build-status.json should be copied to original dir")
	assert.Contains(t, string(statusData), "completed")

	progressData, err := os.ReadFile(filepath.Join(dir, config.EpicProgressFile))
	require.NoError(t, err, "epic-progress.txt should be copied to original dir")
	assert.Contains(t, string(progressData), "Sprint 1")

	phaseData, err := os.ReadFile(filepath.Join(dir, config.BuildPhaseFile))
	require.NoError(t, err, "build-phase.txt should be copied to original dir")
	assert.Contains(t, string(phaseData), "complete")
}

func TestMergeAndCleanupWorktree_UntrackedConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	// Initialize a real git repo with an initial commit
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

	// Create a worktree with a branch
	branchName := "fry/untracked-conflict"
	worktreeDir := filepath.Join(dir, ".fry-worktrees", "untracked-conflict")
	require.NoError(t, os.MkdirAll(filepath.Dir(worktreeDir), 0o755))
	run("worktree", "add", "-b", branchName, worktreeDir)

	// In the worktree, commit files that also exist as untracked in the main dir.
	// This simulates fry copying plans/ to the worktree at setup, then the AI
	// agent committing those files in the worktree branch.
	plansDir := filepath.Join(worktreeDir, "plans")
	require.NoError(t, os.MkdirAll(plansDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plansDir, "plan.md"), []byte("plan from branch"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(plansDir, "prompt.md"), []byte("prompt from branch"), 0o644))

	wtRun := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = worktreeDir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), string(out))
	}
	wtRun("add", "plans/plan.md", "plans/prompt.md")
	wtRun("commit", "-m", "add plans")

	// Create the same files as untracked in the main worktree (the conflict trigger)
	mainPlansDir := filepath.Join(dir, "plans")
	require.NoError(t, os.MkdirAll(mainPlansDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainPlansDir, "plan.md"), []byte("plan local"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainPlansDir, "prompt.md"), []byte("prompt local"), 0o644))

	setup := &StrategySetup{
		WorkDir:        worktreeDir,
		OriginalDir:    dir,
		BranchName:     branchName,
		OriginalBranch: "main",
		Strategy:       StrategyWorktree,
		IsWorktree:     true,
	}

	// This would fail without the untracked-conflict retry logic
	err := MergeAndCleanupWorktree(ctx, setup)
	require.NoError(t, err)

	// Verify: plans from the branch are now on main (tracked versions win)
	data, err := os.ReadFile(filepath.Join(dir, "plans", "plan.md"))
	require.NoError(t, err)
	assert.Equal(t, "plan from branch", string(data))

	data, err = os.ReadFile(filepath.Join(dir, "plans", "prompt.md"))
	require.NoError(t, err)
	assert.Equal(t, "prompt from branch", string(data))

	// Verify: backup directory is cleaned up
	_, err = os.Stat(filepath.Join(dir, ".fry-merge-backup"))
	assert.True(t, os.IsNotExist(err), ".fry-merge-backup should be removed")
}

func TestParseUntrackedConflicts(t *testing.T) {
	t.Parallel()

	t.Run("parses git error", func(t *testing.T) {
		t.Parallel()
		errMsg := `git merge fry/my-branch --no-edit: error: The following untracked working tree files would be overwritten by merge:
        plans/executive.md
        plans/plan.md
        plans/prompt.md
Please move or remove them before you merge.
Aborting`
		files := parseUntrackedConflicts(errMsg)
		assert.Equal(t, []string{"plans/executive.md", "plans/plan.md", "plans/prompt.md"}, files)
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		files := parseUntrackedConflicts("some other error")
		assert.Nil(t, files)
	})

	t.Run("single file", func(t *testing.T) {
		t.Parallel()
		errMsg := `error: The following untracked working tree files would be overwritten by merge:
        foo.txt
Please move or remove them before you merge.`
		files := parseUntrackedConflicts(errMsg)
		assert.Equal(t, []string{"foo.txt"}, files)
	})
}

func TestMergeAndCleanupWorktree_NilSetup(t *testing.T) {
	t.Parallel()
	assert.NoError(t, MergeAndCleanupWorktree(context.Background(), nil))
}

func TestMergeAndCleanupWorktree_NonWorktree(t *testing.T) {
	t.Parallel()
	setup := &StrategySetup{IsWorktree: false}
	assert.NoError(t, MergeAndCleanupWorktree(context.Background(), setup))
}

func TestCopyWorktreeArtifactsReturnsErrorOnWriteFailure(t *testing.T) {
	t.Parallel()

	worktreeDir := t.TempDir()
	origDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(worktreeDir, config.FryDir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktreeDir, config.BuildStatusFile), []byte(`{"version":1}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(origDir, config.FryDir), []byte("blocked"), 0o644))

	err := copyWorktreeArtifacts(worktreeDir, origDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), config.BuildStatusFile)
}

// TestRetryMergeMovingUntracked_RestoreError verifies that when a restore
// fails during rollback, the returned error contains both the merge failure
// context and the rollback failure context, and that the backup directory is
// preserved for manual recovery.
//
// Setup: two branches diverge on "other.md" (content conflict). test-branch
// also commits a regular file named "a". Main has an untracked directory "a/"
// containing "plan.md". retryMergeMovingUntracked backs up "a/plan.md", retries
// the merge, and git applies the non-conflicting "a" file change — replacing
// the now-empty directory "a/" with a regular file. When the restore runs,
// os.MkdirAll("a") fails (ENOTDIR) because "a" is now a file, triggering the
// rollback-incomplete error path.
func TestRetryMergeMovingUntracked_RestoreError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

	// Initial commit — common ancestor for both branches.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.md"), []byte("base"), 0o644))
	run("add", "other.md")
	run("commit", "-m", "initial")

	// Capture default branch name (may be "main" or "master").
	headCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	headCmd.Dir = dir
	headOut, headErr := headCmd.Output()
	require.NoError(t, headErr)
	mainBranch := strings.TrimSpace(string(headOut))

	// test-branch: commit regular file "a" and a conflicting change to "other.md".
	// During the retry merge, git applies the non-conflicting "a" file change
	// (replacing the empty directory "a/") before failing on the "other.md"
	// content conflict.
	run("checkout", "-b", "test-branch")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), []byte("from branch"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.md"), []byte("branch other"), 0o644))
	run("add", "a", "other.md")
	run("commit", "-m", "branch changes")

	// Back on the default branch: change "other.md" to produce a content conflict.
	run("checkout", mainBranch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.md"), []byte("main other"), 0o644))
	run("add", "other.md")
	run("commit", "-m", "main changes")

	// Create untracked "a/plan.md". retryMergeMovingUntracked backs this up, then
	// retries the merge. The retry replaces the now-empty "a/" with file "a",
	// then fails on "other.md". The restore then hits os.MkdirAll("a") with "a"
	// as a regular file, producing the rollback-incomplete error.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "plan.md"), []byte("local"), 0o644))

	initialMergeErr := errors.New("error: The following untracked working tree files would be overwritten by merge:\n\ta/plan.md\nPlease move or remove them before you merge.\nAborting")

	retryErr := retryMergeMovingUntracked(ctx, dir, "test-branch", initialMergeErr)

	require.Error(t, retryErr)
	assert.Contains(t, retryErr.Error(), "merge failed")
	assert.Contains(t, retryErr.Error(), "rollback incomplete")
	assert.Contains(t, retryErr.Error(), "restore mkdir")

	// backupDir must be preserved when any restore fails.
	backupDir := filepath.Join(dir, ".fry-merge-backup")
	_, statErr := os.Stat(backupDir)
	assert.NoError(t, statErr, "backupDir should be preserved when restore fails")
	_, fileStatErr := os.Stat(filepath.Join(backupDir, "a", "plan.md"))
	assert.NoError(t, fileStatErr, "backed-up file should still exist for manual recovery")
}

func TestMarkCleanedUp(t *testing.T) {
	t.Parallel()

	setup := &StrategySetup{IsWorktree: true, WorkDir: "/tmp/fake"}
	setup.MarkCleanedUp()
	// Cleanup should be a no-op after MarkCleanedUp
	err := setup.Cleanup()
	assert.NoError(t, err)
}
