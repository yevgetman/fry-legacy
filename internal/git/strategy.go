package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yevgetman/fry/internal/config"
)

// StrategyOpts configures SetupStrategy.
type StrategyOpts struct {
	ProjectDir string
	Strategy   GitStrategy
	BranchName string // empty = auto-generate from EpicName
	EpicName   string // used for auto-generated branch names
	ForceReuse bool   // true when --continue/--resume (reuse existing branch/worktree)
}

// SetupStrategy configures the git branch/worktree based on the chosen strategy.
func SetupStrategy(ctx context.Context, opts StrategyOpts) (*StrategySetup, error) {
	return SetupStrategyWith(ctx, opts, DefaultExecutor)
}

// SetupStrategyWith is like SetupStrategy but uses the provided Executor.
func SetupStrategyWith(ctx context.Context, opts StrategyOpts, ex Executor) (*StrategySetup, error) {
	if opts.Strategy == StrategyAuto {
		return nil, fmt.Errorf("git strategy must be resolved before calling SetupStrategy (got auto)")
	}
	if opts.Strategy == StrategyCurrent {
		return &StrategySetup{
			WorkDir:     opts.ProjectDir,
			OriginalDir: opts.ProjectDir,
			Strategy:    StrategyCurrent,
		}, nil
	}

	if !ex.IsRepo(ctx, opts.ProjectDir) {
		return nil, fmt.Errorf("git strategy %q requires an existing git repository; run 'git init' first or use --git-strategy current", opts.Strategy)
	}

	branchName := opts.BranchName
	if branchName == "" {
		branchName = GenerateBranchName(opts.EpicName)
	}

	origBranch := ex.CurrentBranch(ctx, opts.ProjectDir)

	switch opts.Strategy {
	case StrategyBranch:
		return setupBranch(ctx, opts.ProjectDir, branchName, origBranch, opts.ForceReuse, ex)
	case StrategyWorktree:
		return setupWorktree(ctx, opts.ProjectDir, branchName, origBranch, opts.ForceReuse, ex)
	default:
		return nil, fmt.Errorf("unexpected git strategy %q", opts.Strategy)
	}
}

func setupBranch(ctx context.Context, projectDir, branchName, origBranch string, forceReuse bool, ex Executor) (*StrategySetup, error) {
	exists := ex.BranchExists(ctx, projectDir, branchName)

	// When the branch already exists, REUSE it. The branch name is
	// deterministic from the epic name, so a branch with this exact
	// name almost certainly belongs to a previous fry build of the
	// same epic. Forcing the user to delete it manually is hostile;
	// the new build's prepare artifacts will overwrite the previous
	// build's .fry/ contents, and the sprint loop will start fresh.
	//
	// `forceReuse` is preserved on StrategyOpts for back-compat but
	// no longer changes the reuse decision. Concurrent fry runs are
	// still prevented by .fry/.fry.lock acquired earlier in run.go.
	_ = forceReuse

	if exists {
		if err := ex.Checkout(ctx, projectDir, branchName); err != nil {
			return nil, fmt.Errorf("checkout existing branch: %w", err)
		}
	} else {
		if err := ex.CreateAndCheckout(ctx, projectDir, branchName); err != nil {
			return nil, fmt.Errorf("create branch: %w", err)
		}
	}

	return &StrategySetup{
		WorkDir:        projectDir,
		OriginalDir:    projectDir,
		BranchName:     branchName,
		OriginalBranch: origBranch,
		Strategy:       StrategyBranch,
	}, nil
}

func setupWorktree(ctx context.Context, projectDir, branchName, origBranch string, forceReuse bool, ex Executor) (*StrategySetup, error) {
	slug := worktreeSlug(branchName)
	worktreeDir := filepath.Join(projectDir, config.GitWorktreeDir, slug)

	exists := worktreeExists(ctx, projectDir, worktreeDir, ex)

	if exists {
		// Validate it's still a valid worktree before deciding to reuse
		// or recreate.
		if !ex.IsRepo(ctx, worktreeDir) {
			// Worktree directory exists but is invalid (perhaps a
			// half-cleaned-up prior run). Prune and recreate from scratch.
			_ = ex.WorktreePrune(ctx, projectDir)
			exists = false
		}
	}

	// When the worktree already exists AND is a valid git repo, REUSE it.
	// The slug is deterministic from the epic name, so a worktree at this
	// exact path almost certainly belongs to a previous fry build of the
	// same epic that crashed, was killed, exited cleanly before reaching
	// the sprint loop, OR was missed by `fry clean` (which only archives
	// .fry/, not the worktree directory itself). Forcing the user to
	// manually `git worktree remove` is hostile — fry should recover
	// gracefully.
	//
	// The previous build's .fry/ artifacts inside the worktree are
	// re-seeded below from the main checkout's freshly-prepared state,
	// so the sprint loop sees the new epic.md / AGENTS.md / verification.md.
	// Source files the previous build modified stay as the previous
	// build left them, which is the expected resume semantics.
	//
	// `forceReuse` (true under --continue/--resume) used to be the only
	// path that allowed reuse; now reuse is the default. The flag is
	// preserved on the StrategyOpts for back-compat but no longer
	// changes the reuse decision. Concurrent fry runs are still
	// prevented by the .fry/.fry.lock acquired earlier in run.go.
	_ = forceReuse

	if !exists {
		if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
			return nil, fmt.Errorf("create worktree parent dir: %w", err)
		}

		createBranch := !ex.BranchExists(ctx, projectDir, branchName)
		if err := ex.WorktreeAdd(ctx, projectDir, worktreeDir, branchName, createBranch); err != nil {
			return nil, fmt.Errorf("create worktree: %w", err)
		}
	}

	// Copy/refresh .fry/ and plans/ into the worktree so the sprint
	// runner finds the latest prepare artifacts. Runs on both the
	// fresh-create and reuse paths.
	if err := copyDirIfExists(filepath.Join(projectDir, config.FryDir), filepath.Join(worktreeDir, config.FryDir)); err != nil {
		return nil, fmt.Errorf("copy .fry/ to worktree: %w", err)
	}
	if err := copyDirIfExists(filepath.Join(projectDir, config.PlansDir), filepath.Join(worktreeDir, config.PlansDir)); err != nil {
		return nil, fmt.Errorf("copy plans/ to worktree: %w", err)
	}

	// The .fry/.fry.lock file is the live process lock for the running
	// fry main process. It must NOT be carried into the worktree —
	// otherwise the worktree gets a stale lock containing the parent's
	// PID, which is never released because the deferred releaseLock()
	// in run.go uses the original project path. The new worktree starts
	// with no lock file; the build runs without a per-worktree lock.
	// Also catches stale locks left in a reused worktree from the
	// previous build (Bug 8 fix, generalized to the reuse path).
	_ = os.Remove(filepath.Join(worktreeDir, config.LockFile))

	return &StrategySetup{
		WorkDir:        worktreeDir,
		OriginalDir:    projectDir,
		BranchName:     branchName,
		OriginalBranch: origBranch,
		Strategy:       StrategyWorktree,
		IsWorktree:     true,
	}, nil
}

// ResolveAutoStrategy maps triage complexity to a concrete strategy.
// COMPLEX -> StrategyWorktree, SIMPLE/MODERATE -> StrategyBranch.
func ResolveAutoStrategy(complexity string) GitStrategy {
	switch strings.ToUpper(complexity) {
	case "COMPLEX":
		return StrategyWorktree
	default:
		return StrategyBranch
	}
}

// GenerateBranchName creates a branch name from an epic name.
// Format: "fry/<slugified-epic-name>" (lowercase, hyphens, max 54 chars).
func GenerateBranchName(epicName string) string {
	slug := slugify(epicName)
	if slug == "" {
		slug = "build"
	}
	return config.GitBranchPrefix + slug
}

// IsInsideGitRepo checks if projectDir is inside a git repository.
func IsInsideGitRepo(ctx context.Context, projectDir string) bool {
	return DefaultExecutor.IsRepo(ctx, projectDir)
}

// IsInsideGitRepoWith is like IsInsideGitRepo but uses the provided Executor.
func IsInsideGitRepoWith(ctx context.Context, projectDir string, ex Executor) bool {
	return ex.IsRepo(ctx, projectDir)
}

// CurrentBranch returns the name of the current git branch, or "" on error.
func CurrentBranch(ctx context.Context, projectDir string) string {
	return DefaultExecutor.CurrentBranch(ctx, projectDir)
}

// CurrentBranchWith is like CurrentBranch but uses the provided Executor.
func CurrentBranchWith(ctx context.Context, projectDir string, ex Executor) string {
	return ex.CurrentBranch(ctx, projectDir)
}

// CheckoutBranch switches to the specified branch.
func CheckoutBranch(ctx context.Context, projectDir, branchName string) error {
	return CheckoutBranchWith(ctx, projectDir, branchName, DefaultExecutor)
}

// CheckoutBranchWith is like CheckoutBranch but uses the provided Executor.
func CheckoutBranchWith(ctx context.Context, projectDir, branchName string, ex Executor) error {
	return ex.Checkout(ctx, projectDir, branchName)
}

// DetectExistingSetup checks if a prior fry branch or worktree exists
// for the given branch name. Returns nil if nothing found.
func DetectExistingSetup(ctx context.Context, projectDir, branchName string) (*StrategySetup, error) {
	return DetectExistingSetupWith(ctx, projectDir, branchName, DefaultExecutor)
}

// DetectExistingSetupWith is like DetectExistingSetup but uses the provided Executor.
func DetectExistingSetupWith(ctx context.Context, projectDir, branchName string, ex Executor) (*StrategySetup, error) {
	slug := worktreeSlug(branchName)
	worktreeDir := filepath.Join(projectDir, config.GitWorktreeDir, slug)

	// Check for worktree first
	if worktreeExists(ctx, projectDir, worktreeDir, ex) && ex.IsRepo(ctx, worktreeDir) {
		return &StrategySetup{
			WorkDir:        worktreeDir,
			OriginalDir:    projectDir,
			BranchName:     branchName,
			OriginalBranch: ex.CurrentBranch(ctx, projectDir),
			Strategy:       StrategyWorktree,
			IsWorktree:     true,
		}, nil
	}

	// Check for branch
	if ex.BranchExists(ctx, projectDir, branchName) {
		return &StrategySetup{
			WorkDir:        projectDir,
			OriginalDir:    projectDir,
			BranchName:     branchName,
			OriginalBranch: ex.CurrentBranch(ctx, projectDir),
			Strategy:       StrategyBranch,
		}, nil
	}

	return nil, nil
}

// MergeAndCleanupWorktree merges the worktree branch into the original branch,
// removes the worktree, deletes the branch, and cleans up the strategy file.
// This should be called after a successful build that used the worktree strategy.
func MergeAndCleanupWorktree(ctx context.Context, setup *StrategySetup) error {
	return MergeAndCleanupWorktreeWith(ctx, setup, DefaultExecutor)
}

// MergeAndCleanupWorktreeWith is like MergeAndCleanupWorktree but uses the provided Executor.
func MergeAndCleanupWorktreeWith(ctx context.Context, setup *StrategySetup, ex Executor) error {
	if setup == nil || !setup.IsWorktree {
		return nil
	}

	origDir := setup.OriginalDir
	branchName := setup.BranchName
	origBranch := setup.OriginalBranch
	if origBranch == "" {
		// Fallback: detect current branch of the original repo
		origBranch = ex.CurrentBranch(ctx, origDir)
	}
	if origBranch == "" {
		origBranch = "main"
	}

	// 1. Ensure we're on the original branch in the main repo
	if current := ex.CurrentBranch(ctx, origDir); current != origBranch {
		if err := ex.Checkout(ctx, origDir, origBranch); err != nil {
			return fmt.Errorf("checkout %s before merge: %w", origBranch, err)
		}
	}

	// 2. Merge the worktree branch (fast-forward when possible)
	if err := runGit(ctx, origDir, "merge", branchName, "--no-edit"); err != nil {
		// If merge failed because untracked files would be overwritten,
		// temporarily move them aside and retry. This commonly happens
		// when plans/ (gitignored) was copied into the worktree at setup
		// and the AI agent committed those files in the worktree branch.
		if retryErr := retryMergeMovingUntracked(ctx, origDir, branchName, err); retryErr != nil {
			return fmt.Errorf("merge %s into %s: %w", branchName, origBranch, retryErr)
		}
	}

	// 3. Copy key .fry/ artifacts from worktree to original dir before removal.
	// These artifacts are gitignored and would be lost when the worktree is deleted.
	if err := copyWorktreeArtifacts(setup.WorkDir, origDir); err != nil {
		return fmt.Errorf("copy worktree artifacts: %w", err)
	}

	// 4. Remove the worktree (--force to handle untracked .fry files)
	if err := runGit(ctx, origDir, "worktree", "remove", "--force", setup.WorkDir); err != nil {
		// Non-fatal: directory may already be gone
		fmt.Fprintf(os.Stderr, "fry: warning: worktree remove: %v\n", err)
	}

	// 5. Prune stale worktree references
	_ = ex.WorktreePrune(ctx, origDir)

	// 6. Delete the branch (safe delete — it's merged)
	if err := runGit(ctx, origDir, "branch", "-d", branchName); err != nil {
		// Non-fatal: branch may not exist
		fmt.Fprintf(os.Stderr, "fry: warning: branch delete: %v\n", err)
	}

	// 7. Remove stale git-strategy.txt
	_ = os.Remove(filepath.Join(origDir, config.GitStrategyFile))

	return nil
}

// MergeAndCleanupBranch merges the fry branch into the original branch,
// checks out the original branch, and deletes the fry branch. This is the
// branch-strategy equivalent of MergeAndCleanupWorktree.
func MergeAndCleanupBranch(ctx context.Context, setup *StrategySetup) error {
	return MergeAndCleanupBranchWith(ctx, setup, DefaultExecutor)
}

// MergeAndCleanupBranchWith is like MergeAndCleanupBranch but uses the provided Executor.
func MergeAndCleanupBranchWith(ctx context.Context, setup *StrategySetup, ex Executor) error {
	if setup == nil || setup.Strategy != StrategyBranch {
		return nil
	}

	origDir := setup.OriginalDir
	branchName := setup.BranchName
	origBranch := setup.OriginalBranch
	if origBranch == "" {
		origBranch = ex.CurrentBranch(ctx, origDir)
	}
	if origBranch == "" {
		origBranch = "main"
	}

	// 1. Checkout the original branch
	if current := ex.CurrentBranch(ctx, origDir); current != origBranch {
		if err := ex.Checkout(ctx, origDir, origBranch); err != nil {
			return fmt.Errorf("checkout %s before merge: %w", origBranch, err)
		}
	}

	// 2. Merge the fry branch (fast-forward when possible)
	if err := runGit(ctx, origDir, "merge", branchName, "--no-edit"); err != nil {
		if retryErr := retryMergeMovingUntracked(ctx, origDir, branchName, err); retryErr != nil {
			return fmt.Errorf("merge %s into %s: %w", branchName, origBranch, retryErr)
		}
	}

	// 3. Delete the branch (safe delete — it's merged)
	if err := runGit(ctx, origDir, "branch", "-d", branchName); err != nil {
		fmt.Fprintf(os.Stderr, "fry: warning: branch delete: %v\n", err)
	}

	// 4. Remove stale git-strategy.txt
	_ = os.Remove(filepath.Join(origDir, config.GitStrategyFile))

	return nil
}

// RestoreBranchAfterFailure checks out the original branch without merging.
// The fry branch is preserved for inspection. Note: checkout may fail if the
// failed build left uncommitted changes that conflict with the original branch;
// this is caught and returned as an error for the caller to log as a warning.
func RestoreBranchAfterFailure(ctx context.Context, setup *StrategySetup) error {
	return RestoreBranchAfterFailureWith(ctx, setup, DefaultExecutor)
}

// RestoreBranchAfterFailureWith is like RestoreBranchAfterFailure but uses the provided Executor.
func RestoreBranchAfterFailureWith(ctx context.Context, setup *StrategySetup, ex Executor) error {
	if setup == nil || setup.Strategy != StrategyBranch {
		return nil
	}

	origBranch := setup.OriginalBranch
	if origBranch == "" {
		origBranch = ex.CurrentBranch(ctx, setup.OriginalDir)
	}
	if origBranch == "" {
		origBranch = "main"
	}

	if current := ex.CurrentBranch(ctx, setup.OriginalDir); current == origBranch {
		return nil
	}

	if err := ex.Checkout(ctx, setup.OriginalDir, origBranch); err != nil {
		return fmt.Errorf("restore branch after failure: checkout %s: %w", origBranch, err)
	}

	// Remove stale git-strategy.txt
	_ = os.Remove(filepath.Join(setup.OriginalDir, config.GitStrategyFile))

	return nil
}

// CleanupWorktrees removes all directories under .fry-worktrees/ via
// `git worktree remove --force`, falling back to os.RemoveAll. Returns the
// number of directories removed. Errors are reported as warnings to stderr.
func CleanupWorktrees(ctx context.Context, projectDir string) int {
	return CleanupWorktreesWith(ctx, projectDir, DefaultExecutor)
}

// CleanupWorktreesWith is like CleanupWorktrees but uses the provided Executor.
func CleanupWorktreesWith(ctx context.Context, projectDir string, ex Executor) int {
	root := filepath.Join(projectDir, config.GitWorktreeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		wtPath := filepath.Join(root, ent.Name())
		cmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtPath)
		cmd.Dir = projectDir
		if err := cmd.Run(); err != nil {
			if rmErr := os.RemoveAll(wtPath); rmErr != nil {
				fmt.Fprintf(os.Stderr, "fry: warning: could not remove worktree %s: %v\n", wtPath, rmErr)
				continue
			}
		}
		removed++
	}
	if removed > 0 {
		_ = ex.WorktreePrune(ctx, projectDir)
	}
	return removed
}

// worktreeArtifacts lists the .fry/ files that should be copied from the worktree
// to the original project dir before worktree removal. These are gitignored artifacts
// that would otherwise be lost.
var worktreeArtifacts = []string{
	config.BuildStatusFile,
	config.BuildPhaseFile,
	config.BuildModeFile,
	config.EpicProgressFile,
	config.SprintProgressFile,
	config.BuildAuditCompleteFile,
	config.DeferredFailuresFile,
	config.BuildReportFile,
}

// copyWorktreeArtifacts copies key .fry/ artifacts from the worktree to the
// original project dir so that fry status and fry run --continue work after
// the worktree is removed.
func copyWorktreeArtifacts(worktreeDir, origDir string) error {
	for _, relPath := range worktreeArtifacts {
		src := filepath.Join(worktreeDir, relPath)
		data, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // file may not exist in the worktree
			}
			return fmt.Errorf("read %s: %w", relPath, err)
		}
		dst := filepath.Join(origDir, relPath)
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return fmt.Errorf("create directory for %s: %w", relPath, mkErr)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", relPath, err)
		}
	}
	return nil
}

// runGit is a thin helper for one-off git commands not in the Executor interface.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
}

// PersistStrategy writes the strategy setup to a file for --continue detection.
func PersistStrategy(originalDir string, setup *StrategySetup) error {
	path := filepath.Join(originalDir, config.GitStrategyFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir for git strategy file: %w", err)
	}
	content := fmt.Sprintf("strategy=%s\nbranch=%s\nworkdir=%s\noriginaldir=%s\noriginalbranch=%s\n",
		setup.Strategy, setup.BranchName, setup.WorkDir, setup.OriginalDir, setup.OriginalBranch)
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadPersistedStrategy reads a previously persisted strategy setup.
// Returns nil, nil if the file does not exist.
func ReadPersistedStrategy(projectDir string) (*StrategySetup, error) {
	path := filepath.Join(projectDir, config.GitStrategyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read git strategy file: %w", err)
	}

	setup := &StrategySetup{}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "strategy":
			parsed, parseErr := ParseGitStrategy(val)
			if parseErr != nil {
				return nil, parseErr
			}
			setup.Strategy = parsed
		case "branch":
			setup.BranchName = val
		case "workdir":
			setup.WorkDir = val
		case "originaldir":
			setup.OriginalDir = val
		case "originalbranch":
			setup.OriginalBranch = val
		}
	}

	if setup.Strategy == "" || setup.WorkDir == "" {
		return nil, fmt.Errorf("invalid git strategy file at %s", path)
	}

	setup.IsWorktree = setup.Strategy == StrategyWorktree
	return setup, nil
}

// retryMergeMovingUntracked handles a merge failure caused by untracked working
// tree files that would be overwritten. It parses the conflicting file paths from
// the git error, moves them to a temporary backup directory, retries the merge,
// and cleans up. If the original error is not an untracked-file conflict, or if the
// retry fails, it returns the relevant error. On retry success it returns nil.
func retryMergeMovingUntracked(ctx context.Context, dir, branchName string, mergeErr error) error {
	files := parseUntrackedConflicts(mergeErr.Error())
	if len(files) == 0 {
		return mergeErr
	}

	backupDir := filepath.Join(dir, ".fry-merge-backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return mergeErr
	}

	type movedFile struct{ orig, backup string }
	var moved []movedFile
	for _, f := range files {
		orig := filepath.Join(dir, f)
		backup := filepath.Join(backupDir, f)
		if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
			continue
		}
		if err := os.Rename(orig, backup); err != nil {
			continue
		}
		moved = append(moved, movedFile{orig, backup})
	}

	// Retry the merge
	if err := runGit(ctx, dir, "merge", branchName, "--no-edit"); err != nil {
		// Restore files on failure
		var restoreErrs []error
		for _, m := range moved {
			if mkErr := os.MkdirAll(filepath.Dir(m.orig), 0o755); mkErr != nil {
				restoreErrs = append(restoreErrs, fmt.Errorf("restore mkdir %s: %w", m.orig, mkErr))
				continue
			}
			if rnErr := os.Rename(m.backup, m.orig); rnErr != nil {
				restoreErrs = append(restoreErrs, fmt.Errorf("restore rename %s: %w", m.orig, rnErr))
			}
		}
		if len(restoreErrs) == 0 {
			_ = os.RemoveAll(backupDir)
		}
		if len(restoreErrs) > 0 {
			return fmt.Errorf("merge failed: %w; rollback incomplete: %w", err, errors.Join(restoreErrs...))
		}
		return err
	}

	// Merge succeeded — remove backups
	_ = os.RemoveAll(backupDir)
	return nil
}

// parseUntrackedConflicts extracts file paths from a git merge error that
// contains "untracked working tree files would be overwritten by merge".
func parseUntrackedConflicts(errMsg string) []string {
	const startMarker = "untracked working tree files would be overwritten by merge:"
	const endMarker = "Please move or remove them before you merge."

	startIdx := strings.Index(errMsg, startMarker)
	if startIdx < 0 {
		return nil
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(errMsg[startIdx:], endMarker)
	if endIdx < 0 {
		endIdx = len(errMsg)
	} else {
		endIdx += startIdx
	}

	var files []string
	for _, line := range strings.Split(errMsg[startIdx:endIdx], "\n") {
		f := strings.TrimSpace(line)
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// --- helpers ---

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	slug := strings.ToLower(s)
	slug = slugRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 50 {
		slug = slug[:50]
		slug = strings.TrimRight(slug, "-")
	}
	return slug
}

// worktreeSlug strips the branch prefix (e.g. "fry/") before slugifying
// to avoid redundant directory names like .fry-worktrees/fry-my-epic.
func worktreeSlug(branchName string) string {
	name := strings.TrimPrefix(branchName, config.GitBranchPrefix)
	return slugify(name)
}

// worktreeExists reports whether worktreeDir is registered as a git
// worktree of projectDir, OR exists on disk as a (possibly orphaned)
// directory that the caller would need to deal with anyway.
//
// Two-stage check:
//
//  1. EvalSymlinks-aware comparison against `git worktree list`. macOS
//     in particular reports tempdir paths via /private/var/folders/...
//     while filepath.Abs returns /var/folders/... — without symlink
//     resolution the string comparison spuriously fails. The same
//     problem can affect production users whose project root is
//     reachable through a symlink.
//
//  2. Fallback: if the dir exists on disk, return true even if git
//     doesn't know about it. The caller's reuse/recreate logic
//     downstream will then handle the "is it actually a valid worktree"
//     question (via IsRepo).
//
// This is broader than "is it a registered worktree" and that's
// intentional — fry needs to know "should I treat this dir as
// already-present" to decide whether to reuse vs create.
func worktreeExists(ctx context.Context, projectDir, worktreeDir string, ex Executor) bool {
	absWT, err := filepath.Abs(worktreeDir)
	if err != nil {
		return false
	}
	resolvedWT := absWT
	if r, err := filepath.EvalSymlinks(absWT); err == nil {
		resolvedWT = r
	}
	if paths, err := ex.WorktreeList(ctx, projectDir); err == nil {
		for _, wt := range paths {
			if wt == absWT || wt == resolvedWT {
				return true
			}
			if r, err := filepath.EvalSymlinks(wt); err == nil && (r == absWT || r == resolvedWT) {
				return true
			}
		}
	}
	// Fallback: dir exists on disk even if git doesn't list it.
	if _, statErr := os.Stat(absWT); statErr == nil {
		return true
	}
	return false
}

func copyDirIfExists(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
