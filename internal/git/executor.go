package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Executor abstracts git operations that require shelling out to git.
// The default ExecExecutor uses exec.CommandContext; a future go-git
// implementation can satisfy the same interface without subprocesses.
type Executor interface {
	// Repository inspection
	IsRepo(ctx context.Context, dir string) bool
	HasHead(ctx context.Context, dir string) bool
	CurrentBranch(ctx context.Context, dir string) string
	BranchExists(ctx context.Context, dir string, name string) bool

	// Repository initialization
	Init(ctx context.Context, dir string) error
	ConfigGet(ctx context.Context, dir string, key string) (string, error)
	ConfigSet(ctx context.Context, dir string, key, value string) error

	// Staging and committing
	AddAll(ctx context.Context, dir string) error
	AddIntent(ctx context.Context, dir string, paths []string) error
	CommitAllowEmpty(ctx context.Context, dir string, message string) error
	Reset(ctx context.Context, dir string, paths []string) error

	// Branching
	Checkout(ctx context.Context, dir string, name string) error
	CreateAndCheckout(ctx context.Context, dir string, name string) error

	// Diffs and queries
	DiffHead(ctx context.Context, dir string, excludePathspecs []string) (string, error)
	DiffStat(ctx context.Context, dir string, excludePathspecs []string) (string, error)
	ListUntracked(ctx context.Context, dir string, excludePathspecs []string) ([]string, error)
	StatusPorcelain(ctx context.Context, dir string) (string, error)
	LogGrep(ctx context.Context, dir string, grepPattern string, maxCount int, format string) (string, error)

	// File restoration
	RestoreFiles(ctx context.Context, dir string, files []string) error

	// Index operations
	CommitCount(ctx context.Context, dir string) (int, error)
	ReadIndexFile(ctx context.Context, dir string, relativePath string) ([]byte, error)
	CheckoutIndexAll(ctx context.Context, dir string) error
	ListADFiles(ctx context.Context, dir string) ([]string, error)

	// Worktree operations
	WorktreeList(ctx context.Context, dir string) ([]string, error)
	WorktreeAdd(ctx context.Context, dir string, worktreePath, branchName string, createBranch bool) error
	WorktreePrune(ctx context.Context, dir string) error
}

// DefaultExecutor is the exec-based implementation used when callers
// don't specify an alternative.
var DefaultExecutor Executor = &ExecExecutor{}

// ExecExecutor implements Executor using exec.CommandContext to shell out to git.
type ExecExecutor struct{}

func (e *ExecExecutor) IsRepo(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return false
	}
	return strings.TrimSpace(stdout.String()) == "true"
}

func (e *ExecExecutor) HasHead(ctx context.Context, dir string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func (e *ExecExecutor) CurrentBranch(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func (e *ExecExecutor) BranchExists(ctx context.Context, dir string, name string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "refs/heads/"+name)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

func (e *ExecExecutor) Init(ctx context.Context, dir string) error {
	return e.run(ctx, dir, "git", "init")
}

func (e *ExecExecutor) ConfigGet(ctx context.Context, dir string, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil // key not found
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git config --get %q: %s", key, msg)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (e *ExecExecutor) ConfigSet(ctx context.Context, dir string, key, value string) error {
	return e.run(ctx, dir, "git", "config", key, value)
}

func (e *ExecExecutor) AddAll(ctx context.Context, dir string) error {
	return e.run(ctx, dir, "git", "add", "-A")
}

func (e *ExecExecutor) AddIntent(ctx context.Context, dir string, paths []string) error {
	args := append([]string{"add", "-N", "--"}, paths...)
	return e.run(ctx, dir, "git", args...)
}

func (e *ExecExecutor) CommitAllowEmpty(ctx context.Context, dir string, message string) error {
	return e.run(ctx, dir, "git", "commit", "--allow-empty", "-m", message)
}

func (e *ExecExecutor) Reset(ctx context.Context, dir string, paths []string) error {
	args := append([]string{"reset", "--"}, paths...)
	return e.run(ctx, dir, "git", args...)
}

func (e *ExecExecutor) Checkout(ctx context.Context, dir string, name string) error {
	return e.run(ctx, dir, "git", "checkout", name)
}

func (e *ExecExecutor) CreateAndCheckout(ctx context.Context, dir string, name string) error {
	return e.run(ctx, dir, "git", "checkout", "-b", name)
}

func (e *ExecExecutor) DiffHead(ctx context.Context, dir string, excludePathspecs []string) (string, error) {
	args := []string{"diff", "HEAD", "--", "."}
	for _, exc := range excludePathspecs {
		args = append(args, exc)
	}
	return e.output(ctx, dir, "git", args...)
}

func (e *ExecExecutor) DiffStat(ctx context.Context, dir string, excludePathspecs []string) (string, error) {
	args := []string{"diff", "--stat", "HEAD", "--", "."}
	for _, exc := range excludePathspecs {
		args = append(args, exc)
	}
	return e.output(ctx, dir, "git", args...)
}

func (e *ExecExecutor) ListUntracked(ctx context.Context, dir string, excludePathspecs []string) ([]string, error) {
	args := []string{"ls-files", "--others", "--exclude-standard", "--", "."}
	for _, exc := range excludePathspecs {
		args = append(args, exc)
	}
	out, err := e.output(ctx, dir, "git", args...)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func (e *ExecExecutor) StatusPorcelain(ctx context.Context, dir string) (string, error) {
	return e.output(ctx, dir, "git", "status", "--porcelain")
}

func (e *ExecExecutor) LogGrep(ctx context.Context, dir string, grepPattern string, maxCount int, format string) (string, error) {
	args := []string{"log", "--grep=" + grepPattern}
	if format != "" {
		args = append(args, "--format="+format)
	} else {
		args = append(args, "--oneline")
	}
	if maxCount > 0 {
		args = append(args, "-"+strconv.Itoa(maxCount))
	}
	return e.output(ctx, dir, "git", args...)
}

func (e *ExecExecutor) RestoreFiles(ctx context.Context, dir string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	// Separate files into those tracked by HEAD (can be restored via git checkout)
	// and untracked/new files (must be deleted since HEAD has no version).
	var tracked, untracked []string
	for _, f := range files {
		checkCmd := exec.CommandContext(ctx, "git", "cat-file", "-e", "HEAD:"+f)
		checkCmd.Dir = dir
		if checkCmd.Run() == nil {
			tracked = append(tracked, f)
		} else {
			untracked = append(untracked, f)
		}
	}
	if len(tracked) > 0 {
		args := append([]string{"checkout", "HEAD", "--"}, tracked...)
		if err := e.run(ctx, dir, "git", args...); err != nil {
			return err
		}
	}
	for _, f := range untracked {
		fullPath := filepath.Join(dir, f)
		if removeErr := os.Remove(fullPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove untracked file %s: %w", f, removeErr)
		}
	}
	return nil
}

func (e *ExecExecutor) WorktreeList(ctx context.Context, dir string) ([]string, error) {
	out, err := e.output(ctx, dir, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	return paths, nil
}

func (e *ExecExecutor) WorktreeAdd(ctx context.Context, dir string, worktreePath, branchName string, createBranch bool) error {
	if createBranch {
		return e.run(ctx, dir, "git", "worktree", "add", worktreePath, "-b", branchName)
	}
	return e.run(ctx, dir, "git", "worktree", "add", worktreePath, branchName)
}

func (e *ExecExecutor) WorktreePrune(ctx context.Context, dir string) error {
	return e.run(ctx, dir, "git", "worktree", "prune")
}

func (e *ExecExecutor) CommitCount(ctx context.Context, dir string) (int, error) {
	out, err := e.output(ctx, dir, "git", "rev-list", "--count", "HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func (e *ExecExecutor) ReadIndexFile(ctx context.Context, dir string, relativePath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "show", ":"+relativePath)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("git show :%s: %s", relativePath, msg)
		}
		return nil, fmt.Errorf("git show :%s: %w", relativePath, err)
	}
	return stdout.Bytes(), nil
}

func (e *ExecExecutor) CheckoutIndexAll(ctx context.Context, dir string) error {
	return e.run(ctx, dir, "git", "checkout-index", "-a", "-f")
}

func (e *ExecExecutor) ListADFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := e.output(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var adFiles []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) >= 3 && line[0] == 'A' && line[1] == 'D' {
			path := strings.TrimSpace(line[3:])
			if path != "" {
				adFiles = append(adFiles, path)
			}
		}
	}
	return adFiles, nil
}

// run executes a command and returns an error with combined output on failure.
func (e *ExecExecutor) run(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
}

// output executes a command and returns its stdout.
func (e *ExecExecutor) output(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}
