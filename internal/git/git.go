package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	frylog "github.com/yevgetman/fry/internal/log"
)

// initialCommitMessage is the exact message used by InitGitWith for the
// automated first commit. IsFreshlyInitialized checks for this string.
const initialCommitMessage = "Initial commit [automated]"

// InitGit initializes a git repository with local identity and .gitignore entries.
func InitGit(ctx context.Context, projectDir string) error {
	return InitGitWith(ctx, projectDir, DefaultExecutor)
}

// InitGitWith is like InitGit but uses the provided Executor.
func InitGitWith(ctx context.Context, projectDir string, ex Executor) error {
	if _, err := os.Stat(filepath.Join(projectDir, ".git")); os.IsNotExist(err) {
		if err := ex.Init(ctx, projectDir); err != nil {
			return fmt.Errorf("init git: %w", err)
		}
	}

	if err := ensureLocalIdentityWith(ctx, projectDir, ex); err != nil {
		return err
	}
	if err := ensureGitignoreEntries(projectDir, []string{".fry/", ".fry-archive/", ".env", ".DS_Store", ".fry-worktrees/"}); err != nil {
		return err
	}

	if ex.HasHead(ctx, projectDir) {
		return nil
	}

	if err := ex.AddAll(ctx, projectDir); err != nil {
		return fmt.Errorf("initial commit: add: %w", err)
	}
	if err := ex.CommitAllowEmpty(ctx, projectDir, initialCommitMessage); err != nil {
		return fmt.Errorf("initial commit: %w", err)
	}
	return nil
}

// IsFreshlyInitialized returns true if the repository at projectDir was
// just initialised by fry (exactly one commit with the automated message).
// This is used to avoid worktree/branch strategies on repos with no real history.
func IsFreshlyInitialized(ctx context.Context, projectDir string) bool {
	return IsFreshlyInitializedWith(ctx, projectDir, DefaultExecutor)
}

// IsFreshlyInitializedWith is like IsFreshlyInitialized but uses the provided Executor.
func IsFreshlyInitializedWith(ctx context.Context, projectDir string, ex Executor) bool {
	count, err := ex.CommitCount(ctx, projectDir)
	if err != nil || count != 1 {
		return false
	}
	msg, err := ex.LogGrep(ctx, projectDir, "", 1, "%s")
	if err != nil {
		return false
	}
	return strings.TrimSpace(msg) == initialCommitMessage
}

// ReadIndexFile reads a file from the git staging area (index).
// This is the fallback when a file exists in the index but not on disk (AD status).
func ReadIndexFile(ctx context.Context, projectDir, relativePath string) ([]byte, error) {
	return DefaultExecutor.ReadIndexFile(ctx, projectDir, relativePath)
}

// CheckoutIndexAll materialises all index entries onto the working tree.
// This recovers files that are staged but missing from disk (AD status).
func CheckoutIndexAll(ctx context.Context, projectDir string) error {
	return DefaultExecutor.CheckoutIndexAll(ctx, projectDir)
}

// ListADFiles returns relative paths of files in AD status (added to index,
// deleted from working tree).
func ListADFiles(ctx context.Context, projectDir string) ([]string, error) {
	return DefaultExecutor.ListADFiles(ctx, projectDir)
}

// CleanNestedGitDirs finds and removes nested .git directories that are
// children of the project root. These are left behind by tools like
// create-next-app and cause the parent repo to record gitlinks (mode 160000)
// instead of tracking the files normally. This runs in fry's own process,
// bypassing engine sandbox restrictions that prevent the fix agent from
// running rm -rf on .git directories.
func CleanNestedGitDirs(projectDir string) ([]string, error) {
	var cleaned []string
	err := filepath.WalkDir(projectDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if !d.IsDir() {
			return nil
		}
		// Skip the root .git directory itself
		if path == filepath.Join(projectDir, ".git") {
			return filepath.SkipDir
		}
		// Skip .fry directories
		name := d.Name()
		if name == ".fry" || name == ".fry-worktrees" || name == ".fry-archive" || name == "node_modules" || name == ".next" {
			return filepath.SkipDir
		}
		// Detect nested .git
		if name == ".git" {
			parent := filepath.Dir(path)
			if parent != projectDir {
				if removeErr := os.RemoveAll(path); removeErr != nil {
					frylog.Log("WARNING: could not remove nested .git at %s: %v", path, removeErr)
				} else {
					rel, _ := filepath.Rel(projectDir, path)
					cleaned = append(cleaned, rel)
				}
				return filepath.SkipDir
			}
			return filepath.SkipDir
		}
		return nil
	})
	return cleaned, err
}

// GitCheckpoint creates a git commit capturing all current changes.
func GitCheckpoint(ctx context.Context, projectDir, epicName string, sprintNum int, sprintName, label string) error {
	return GitCheckpointWith(ctx, projectDir, epicName, sprintNum, sprintName, label, DefaultExecutor)
}

// GitCheckpointWith is like GitCheckpoint but uses the provided Executor.
func GitCheckpointWith(ctx context.Context, projectDir, epicName string, sprintNum int, sprintName, label string, ex Executor) error {
	var message string
	if sprintName != "" {
		message = fmt.Sprintf("%s — %s: Sprint %d %s [automated]", epicName, sprintName, sprintNum, label)
	} else {
		message = fmt.Sprintf("%s: Sprint %d %s [automated]", epicName, sprintNum, label)
	}
	if err := ex.AddAll(ctx, projectDir); err != nil {
		return fmt.Errorf("git checkpoint: add: %w", err)
	}
	if err := ex.CommitAllowEmpty(ctx, projectDir, message); err != nil {
		return fmt.Errorf("git checkpoint: %w", err)
	}

	// Post-commit verification: confirm the working tree is clean.
	// A dirty tree after commit indicates files were not captured (e.g. AD-status
	// files, race with engine cleanup). Log a warning so the issue is visible
	// in build logs rather than silently propagating to the audit phase.
	status, statusErr := ex.StatusPorcelain(ctx, projectDir)
	if statusErr == nil && strings.TrimSpace(status) != "" {
		var adCount, gitlinkCount, otherCount int
		for _, line := range strings.Split(status, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			switch {
			case len(line) >= 2 && line[0] == 'A' && line[1] == 'D':
				adCount++
			case strings.HasSuffix(line, "/.git") || isGitlinkEntry(projectDir, line):
				gitlinkCount++
			default:
				otherCount++
			}
		}
		if adCount > 0 {
			frylog.Log("WARNING: git checkpoint: %d AD-status files after commit (staged but missing from disk) — possible worktree integrity issue", adCount)
		}
		if gitlinkCount > 0 {
			frylog.Log("WARNING: git checkpoint: %d nested .git gitlink(s) after commit — will be cleaned on next sprint", gitlinkCount)
		}
		if otherCount > 0 {
			frylog.Log("WARNING: git checkpoint: working tree not clean after commit — %d unexpected dirty entries", otherCount)
		}
	}

	return nil
}

// isGitlinkEntry checks if a git status porcelain line refers to a path
// that contains a nested .git directory (gitlink/submodule entry).
func isGitlinkEntry(projectDir, line string) bool {
	// Porcelain v1 format: XY <space> path (or XY <space> path -> path for renames)
	if len(line) < 4 {
		return false
	}
	relPath := strings.TrimSpace(line[2:])
	if idx := strings.Index(relPath, " -> "); idx >= 0 {
		relPath = relPath[idx+4:]
	}
	if relPath == "" {
		return false
	}
	// Check if the path is a directory containing .git
	info, err := os.Stat(filepath.Join(projectDir, relPath, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CommitPartialWork commits partial work from a failed sprint.
func CommitPartialWork(ctx context.Context, projectDir, epicName string, sprintNum int, sprintName string) error {
	return GitCheckpoint(ctx, projectDir, epicName, sprintNum, sprintName, "failed-partial")
}

// CommitPartialWorkWith is like CommitPartialWork but uses the provided Executor.
func CommitPartialWorkWith(ctx context.Context, projectDir, epicName string, sprintNum int, sprintName string, ex Executor) error {
	return GitCheckpointWith(ctx, projectDir, epicName, sprintNum, sprintName, "failed-partial", ex)
}

// GitDiffForAudit returns a full diff including untracked files, excluding .fry/.
func GitDiffForAudit(ctx context.Context, projectDir string) (string, error) {
	return GitDiffForAuditWith(ctx, projectDir, DefaultExecutor)
}

// GitDiffForAuditWith is like GitDiffForAudit but uses the provided Executor.
func GitDiffForAuditWith(ctx context.Context, projectDir string, ex Executor) (string, error) {
	untrackedPaths, err := ex.ListUntracked(ctx, projectDir, []string{":(exclude).fry/"})
	if err != nil {
		untrackedPaths = nil
	}

	if len(untrackedPaths) > 0 {
		if addErr := ex.AddIntent(ctx, projectDir, untrackedPaths); addErr != nil {
			untrackedPaths = nil
		}
	}

	diff, diffErr := ex.DiffHead(ctx, projectDir, []string{":(exclude).fry/"})

	// Undo only the temporary intent-to-add entries we created for untracked files.
	if len(untrackedPaths) > 0 {
		if resetErr := ex.Reset(ctx, projectDir, untrackedPaths); resetErr != nil {
			fmt.Fprintf(os.Stderr, "fry: warning: git reset after diff failed: %v\n", resetErr)
		}
	}

	if diffErr != nil {
		return "", fmt.Errorf("git diff for audit: %w", diffErr)
	}
	return diff, nil
}

// DiffStatForNoopDetection returns git diff --stat output excluding progress files,
// for use in no-op detection. Returns a unique error string on failure to prevent
// false-positive no-op matching.
func DiffStatForNoopDetection(ctx context.Context, projectDir string) string {
	return DiffStatForNoopDetectionWith(ctx, projectDir, DefaultExecutor)
}

// DiffStatForNoopDetectionWith is like DiffStatForNoopDetection but uses the provided Executor.
func DiffStatForNoopDetectionWith(ctx context.Context, projectDir string, ex Executor) string {
	out, err := ex.DiffStat(ctx, projectDir, []string{
		":(exclude).fry/sprint-progress.txt",
		":(exclude).fry/epic-progress.txt",
	})
	if err != nil {
		frylog.Log("WARNING: git diff --stat failed: %v", err)
		return fmt.Sprintf("__git_error_%d__", time.Now().UnixNano())
	}
	return out
}

// WorktreeFingerprintForNoopDetection returns a fingerprint of the working tree
// suitable for no-op detection. It combines diff-stat output with filtered
// porcelain status so both tracked AND untracked file changes count as real
// work, while progress file writes remain excluded.
//
// Untracked files require special handling: `git status --porcelain` (default
// mode) collapses untracked directories to a single `?? dir/` line and never
// reports content changes inside them. Two compensating measures are applied
// here so the fingerprint actually reflects content edits:
//   - The porcelain call is made with --untracked-files=all so each untracked
//     file gets its own line.
//   - Each `?? path` line is enriched with the file's size and mtime, so an
//     edit to the file changes the fingerprint even though the porcelain
//     status code itself is unchanged.
func WorktreeFingerprintForNoopDetection(ctx context.Context, projectDir string) string {
	return WorktreeFingerprintForNoopDetectionWith(ctx, projectDir, DefaultExecutor)
}

// WorktreeFingerprintForNoopDetectionWith is like WorktreeFingerprintForNoopDetection
// but uses the provided Executor.
func WorktreeFingerprintForNoopDetectionWith(ctx context.Context, projectDir string, ex Executor) string {
	diff := DiffStatForNoopDetectionWith(ctx, projectDir, ex)
	status, err := ex.StatusPorcelainUntrackedAll(ctx, projectDir)
	if err != nil {
		frylog.Log("WARNING: git status --porcelain failed: %v", err)
		return fmt.Sprintf("__git_status_error_%d__", time.Now().UnixNano())
	}

	filteredStatus := filterStatusForNoopDetection(status)
	enrichedStatus := enrichUntrackedWithSizeMtime(projectDir, filteredStatus)
	diff = strings.TrimSpace(diff)
	enrichedStatus = strings.TrimSpace(enrichedStatus)

	switch {
	case diff == "" && enrichedStatus == "":
		return ""
	case diff == "":
		return enrichedStatus
	case enrichedStatus == "":
		return diff
	default:
		return diff + "\n--status--\n" + enrichedStatus
	}
}

// enrichUntrackedWithSizeMtime appends size and mtime suffixes to untracked
// porcelain lines (`?? path`). This is what makes the no-op fingerprint
// sensitive to content changes inside untracked files — without it, an edit
// to an untracked file leaves the porcelain output identical and the fix
// agent's real work gets misclassified as a no-op.
//
// Other porcelain lines (modified, added, deleted, renamed) are left
// unchanged because git diff already covers their content state via
// DiffStatForNoopDetectionWith.
func enrichUntrackedWithSizeMtime(projectDir, status string) string {
	if status == "" {
		return ""
	}
	var b strings.Builder
	for i, line := range strings.Split(status, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		// Lstat so symlinks are fingerprinted by the link itself, not its
		// target — agents editing the link target shouldn't false-no-op,
		// and agents editing the link itself shouldn't either.
		info, statErr := os.Lstat(filepath.Join(projectDir, path))
		if statErr != nil {
			// File vanished between status and stat — record that fact in
			// the fingerprint so it changes if the file reappears.
			b.WriteString(" stat=err")
			continue
		}
		fmt.Fprintf(&b, " size=%d mtime=%d", info.Size(), info.ModTime().UnixNano())
	}
	return b.String()
}

// RestoreFiles restores the given files to their HEAD state, discarding
// worktree modifications. This is used by the audit-fix loop to roll back
// rejected or out-of-scope changes.
func RestoreFiles(ctx context.Context, projectDir string, files []string) error {
	return RestoreFilesWith(ctx, projectDir, files, DefaultExecutor)
}

// RestoreFilesWith is like RestoreFiles but uses the provided Executor.
func RestoreFilesWith(ctx context.Context, projectDir string, files []string, ex Executor) error {
	if len(files) == 0 {
		return nil
	}
	return ex.RestoreFiles(ctx, projectDir, files)
}

// CollectState returns git working tree state for build resumption reporting.
func CollectState(ctx context.Context, projectDir string) (clean bool, branch string, lastAutoCommit string) {
	return CollectStateWith(ctx, projectDir, DefaultExecutor)
}

// CollectStateWith is like CollectState but uses the provided Executor.
func CollectStateWith(ctx context.Context, projectDir string, ex Executor) (bool, string, string) {
	clean := true
	status, err := ex.StatusPorcelain(ctx, projectDir)
	if err == nil {
		clean = strings.TrimSpace(status) == ""
	} else {
		frylog.Log("WARNING: git status --porcelain failed: %v", err)
	}

	branch := ex.CurrentBranch(ctx, projectDir)

	lastCommit := ""
	out, err := ex.LogGrep(ctx, projectDir, `\[automated\]`, 1, "%s")
	if err == nil {
		lastCommit = strings.TrimSpace(out)
	}

	return clean, branch, lastCommit
}

func filterStatusForNoopDetection(status string) string {
	var kept []string
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, ".fry/sprint-progress.txt") || strings.Contains(line, ".fry/epic-progress.txt") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// gitConfigValue is an unexported convenience that uses DefaultExecutor.
// Used by tests in the same package.
func gitConfigValue(ctx context.Context, projectDir, key string) (string, error) {
	return DefaultExecutor.ConfigGet(ctx, projectDir, key)
}

// hasHead is an unexported convenience that uses DefaultExecutor.
func hasHead(ctx context.Context, projectDir string) bool {
	return DefaultExecutor.HasHead(ctx, projectDir)
}

// ensureLocalIdentity uses DefaultExecutor. Called by tests in the same package.
func ensureLocalIdentity(ctx context.Context, projectDir string) error {
	return ensureLocalIdentityWith(ctx, projectDir, DefaultExecutor)
}

func ensureLocalIdentityWith(ctx context.Context, projectDir string, ex Executor) error {
	name, err := ex.ConfigGet(ctx, projectDir, "user.name")
	if err != nil {
		return fmt.Errorf("get git user.name: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		if err := ex.ConfigSet(ctx, projectDir, "user.name", "fry"); err != nil {
			return fmt.Errorf("set git user.name: %w", err)
		}
	}
	email, err := ex.ConfigGet(ctx, projectDir, "user.email")
	if err != nil {
		return fmt.Errorf("get git user.email: %w", err)
	}
	if strings.TrimSpace(email) == "" {
		if err := ex.ConfigSet(ctx, projectDir, "user.email", "fry@automated"); err != nil {
			return fmt.Errorf("set git user.email: %w", err)
		}
	}
	return nil
}

func ensureGitignoreEntries(projectDir string, entries []string) error {
	path := filepath.Join(projectDir, ".gitignore")
	existing := map[string]bool{}

	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			existing[strings.TrimSpace(line)] = true
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	var toAppend []string
	for _, entry := range entries {
		if !existing[entry] {
			toAppend = append(toAppend, entry)
		}
	}
	if len(toAppend) == 0 {
		return nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open .gitignore: %w", err)
	}
	defer file.Close()

	if info, err := file.Stat(); err == nil && info.Size() > 0 {
		if _, err := file.WriteString("\n"); err != nil {
			return fmt.Errorf("separate .gitignore entries: %w", err)
		}
	}
	if _, err := file.WriteString(strings.Join(toAppend, "\n") + "\n"); err != nil {
		return fmt.Errorf("write .gitignore entries: %w", err)
	}
	return nil
}
