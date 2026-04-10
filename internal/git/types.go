package git

import (
	"fmt"
	"os"
)

// GitStrategy controls how fry manages branches during a build.
type GitStrategy string

const (
	StrategyAuto     GitStrategy = "auto"
	StrategyCurrent  GitStrategy = "current"
	StrategyBranch   GitStrategy = "branch"
	StrategyWorktree GitStrategy = "worktree"
)

// ParseGitStrategy parses a string into a GitStrategy.
// Empty string defaults to StrategyAuto.
func ParseGitStrategy(s string) (GitStrategy, error) {
	switch s {
	case "", "auto":
		return StrategyAuto, nil
	case "current":
		return StrategyCurrent, nil
	case "branch":
		return StrategyBranch, nil
	case "worktree":
		return StrategyWorktree, nil
	default:
		return "", fmt.Errorf("invalid git strategy %q: must be auto, current, branch, or worktree", s)
	}
}

// StrategySetup holds the result of setting up a git strategy.
// The caller should use WorkDir as the effective project directory
// for all subsequent operations.
type StrategySetup struct {
	// WorkDir is the directory where build operations should happen.
	// For StrategyCurrent and StrategyBranch, this equals OriginalDir.
	// For StrategyWorktree, this is the worktree path.
	WorkDir string

	// OriginalDir is the original project directory (for lock file, etc.)
	OriginalDir string

	// BranchName is the git branch created or used.
	// Empty for StrategyCurrent.
	BranchName string

	// OriginalBranch is the branch that was active before setup.
	OriginalBranch string

	// Strategy is the resolved strategy (never StrategyAuto).
	Strategy GitStrategy

	// IsWorktree is true when a worktree was created.
	IsWorktree bool

	// cleanedUp tracks whether Cleanup has been called.
	cleanedUp bool
}

// MarkCleanedUp suppresses the deferred Cleanup message. Call this after
// a successful worktree merge to prevent Cleanup from printing a stale
// "worktree preserved" message.
func (s *StrategySetup) MarkCleanedUp() {
	if s != nil {
		s.cleanedUp = true
	}
}

// IsCleanedUp reports whether MarkCleanedUp has been called.
func (s *StrategySetup) IsCleanedUp() bool {
	return s != nil && s.cleanedUp
}

// Cleanup performs any necessary teardown. For worktrees, it logs the
// worktree path for the user but does not auto-remove it. For branches,
// it is a no-op. Safe to call multiple times.
func (s *StrategySetup) Cleanup() error {
	if s == nil || s.cleanedUp {
		return nil
	}
	s.cleanedUp = true

	if s.IsWorktree {
		fmt.Fprintf(
			os.Stderr,
			"  GIT: worktree preserved at %s\n       To remove: git worktree remove %s\n",
			s.WorkDir, s.WorkDir,
		)
	}
	return nil
}
