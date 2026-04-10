package finalize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yevgetman/fry/internal/archive"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/git"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/steering"
)

// FinalizeOpts configures the end-of-build cleanup.
type FinalizeOpts struct {
	// ProjectPath is the effective project directory (may be a worktree).
	ProjectPath string

	// OriginalProjectPath is the original project directory (for archives
	// and git operations). Equal to ProjectPath for non-worktree builds.
	OriginalProjectPath string

	// StrategySetup holds the git strategy used for the build, or nil
	// if no git strategy was configured (e.g. dry-run, early exit).
	StrategySetup *git.StrategySetup

	// BuildErr is the build's exit error, or nil on success.
	BuildErr error
}

// Finalize performs all end-of-build cleanup: archiving .fry/, cleaning up
// git state (worktrees, branches), and removing steering artifacts. All
// operations are best-effort — failures are logged as warnings, never fatal.
// After Finalize returns, the repository is ready for a new fry run.
func Finalize(ctx context.Context, opts FinalizeOpts) {
	// 1. Archive .fry/ to the original project dir's .fry-archive/.
	archiveBuildArtifacts(opts.ProjectPath, opts.OriginalProjectPath)

	// 2. Clean stale .fry/ in the original dir (worktree builds only).
	if opts.ProjectPath != opts.OriginalProjectPath {
		cleanStaleOriginalFryDir(opts.OriginalProjectPath)
	}

	// 3. Git strategy cleanup.
	if opts.StrategySetup != nil {
		gitCleanup(ctx, opts)
	}

	// 4. Steering cleanup. For worktree builds the worktree directory may
	// have been removed by git cleanup, so only clean the original dir.
	if opts.ProjectPath == opts.OriginalProjectPath {
		steering.CleanupAll(opts.ProjectPath)
	} else {
		steering.CleanupAll(opts.OriginalProjectPath)
	}
}

// archiveBuildArtifacts archives .fry/ and root-level build outputs.
func archiveBuildArtifacts(fromDir, toDir string) {
	archivePath, err := archive.ArchiveTo(fromDir, toDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fry: warning: auto-archive failed: %v\n", err)
		return
	}
	frylog.Log("  ARCHIVE  build artifacts archived to %s", archivePath)
}

// cleanStaleOriginalFryDir removes stale files written to the original
// project's .fry/ during a worktree build (git-strategy.txt, build-phase.txt,
// build-mode.txt). Removes .fry/ entirely if it becomes empty.
func cleanStaleOriginalFryDir(origDir string) {
	staleFiles := []string{
		config.GitStrategyFile,
		config.BuildPhaseFile,
		config.BuildModeFile,
	}
	for _, f := range staleFiles {
		_ = os.Remove(filepath.Join(origDir, f))
	}
	// Remove .fry/ if it's now empty.
	fryDir := filepath.Join(origDir, config.FryDir)
	entries, err := os.ReadDir(fryDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(fryDir)
	}
}

// gitCleanup dispatches strategy-specific git cleanup.
func gitCleanup(ctx context.Context, opts FinalizeOpts) {
	setup := opts.StrategySetup
	success := opts.BuildErr == nil

	switch {
	case setup.IsWorktree && success:
		frylog.Log("  GIT: merging worktree branch %s into %s...", setup.BranchName, setup.OriginalBranch)
		if err := git.MergeAndCleanupWorktree(ctx, setup); err != nil {
			frylog.Log("WARNING: worktree merge failed: %v", err)
		} else {
			frylog.Log("  GIT: worktree merged and cleaned up")
			setup.MarkCleanedUp()
		}

	case setup.IsWorktree && !success:
		frylog.Log("  GIT: removing worktree (build failed, branch %s preserved for inspection)", setup.BranchName)
		removed := git.CleanupWorktrees(ctx, setup.OriginalDir)
		if removed > 0 {
			frylog.Log("  GIT: removed %d worktree dir(s)", removed)
		}
		setup.MarkCleanedUp()

	case setup.Strategy == git.StrategyBranch && success:
		frylog.Log("  GIT: merging branch %s into %s...", setup.BranchName, setup.OriginalBranch)
		if err := git.MergeAndCleanupBranch(ctx, setup); err != nil {
			frylog.Log("WARNING: branch merge failed: %v", err)
		} else {
			frylog.Log("  GIT: branch merged and cleaned up")
		}

	case setup.Strategy == git.StrategyBranch && !success:
		frylog.Log("  GIT: restoring %s (build failed, branch %s preserved for inspection)", setup.OriginalBranch, setup.BranchName)
		if err := git.RestoreBranchAfterFailure(ctx, setup); err != nil {
			frylog.Log("WARNING: branch restore failed: %v", err)
		} else {
			frylog.Log("  GIT: restored to %s", setup.OriginalBranch)
		}
	}
}
