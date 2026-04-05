package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitGit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	info, err := os.Stat(projectDir + "/.git")
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestGitCheckpoint(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))
	require.NoError(t, os.WriteFile(projectDir+"/file.txt", []byte("data\n"), 0o644))
	require.NoError(t, GitCheckpoint(context.Background(), projectDir, "Epic Name", 2, "Add login page", "complete"))

	cmd := exec.Command("bash", "-c", "git log -1 --pretty=%s")
	cmd.Dir = projectDir
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "Epic Name — Add login page: Sprint 2 complete [automated]", strings.TrimSpace(string(output)))
}

func TestGitCheckpoint_EmptySprintName(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))
	require.NoError(t, os.WriteFile(projectDir+"/file.txt", []byte("data\n"), 0o644))
	require.NoError(t, GitCheckpoint(context.Background(), projectDir, "Epic Name", 3, "", "build-audit"))

	cmd := exec.Command("bash", "-c", "git log -1 --pretty=%s")
	cmd.Dir = projectDir
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "Epic Name: Sprint 3 build-audit [automated]", strings.TrimSpace(string(output)))
}

func TestGitCheckpoint_CleanAfterCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644))

	err := GitCheckpoint(context.Background(), dir, "Epic", 1, "Sprint", "complete")
	require.NoError(t, err)

	// Working tree should be clean after checkpoint
	ex := &ExecExecutor{}
	status, err := ex.StatusPorcelain(context.Background(), dir)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(status), "working tree should be clean after checkpoint")
}

func TestGitCheckpointWith_ADStatusWarning(t *testing.T) {
	t.Parallel()

	// Use a mock that simulates AD-status files after commit
	var commitCalled bool
	ex := &mockExecutor{
		AddAllFn: func(_ context.Context, _ string) error { return nil },
		CommitAllowEmptyFn: func(_ context.Context, _ string, _ string) error {
			commitCalled = true
			return nil
		},
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "AD app.go\nAD lib.go\n", nil
		},
	}

	err := GitCheckpointWith(context.Background(), t.TempDir(), "Epic", 1, "Sprint", "complete", ex)
	require.NoError(t, err)
	assert.True(t, commitCalled, "commit should have been called")
	// The function logs a warning but does not return an error — the commit itself succeeded.
}

func TestInitGitIdempotent(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))
	require.NoError(t, InitGit(context.Background(), projectDir))
}

func TestGitDiffForAudit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	require.NoError(t, os.WriteFile(projectDir+"/existing.txt", []byte("original\n"), 0o644))
	cmd := exec.Command("bash", "-c", "git add -A && git commit -m 'add existing'")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	require.NoError(t, os.WriteFile(projectDir+"/existing.txt", []byte("modified\n"), 0o644))
	require.NoError(t, os.WriteFile(projectDir+"/newfile.txt", []byte("new content\n"), 0o644))

	diff, err := GitDiffForAudit(context.Background(), projectDir)
	require.NoError(t, err)

	assert.Contains(t, diff, "existing.txt")
	assert.Contains(t, diff, "modified")
	assert.Contains(t, diff, "newfile.txt")
	assert.Contains(t, diff, "new content")

	statusCmd := exec.Command("bash", "-c", "git diff --cached --name-only")
	statusCmd.Dir = projectDir
	out, err := statusCmd.Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)), "no files should be staged after GitDiffForAudit")
}

func TestGitDiffForAudit_PreservesExistingStagedChanges(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	require.NoError(t, os.WriteFile(projectDir+"/tracked.txt", []byte("original\n"), 0o644))
	cmd := exec.Command("bash", "-c", "git add tracked.txt && git commit -m 'add tracked'")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	require.NoError(t, os.WriteFile(projectDir+"/tracked.txt", []byte("staged change\n"), 0o644))
	stageCmd := exec.Command("bash", "-c", "git add tracked.txt")
	stageCmd.Dir = projectDir
	require.NoError(t, stageCmd.Run())

	require.NoError(t, os.WriteFile(projectDir+"/newfile.txt", []byte("new content\n"), 0o644))

	diff, err := GitDiffForAudit(context.Background(), projectDir)
	require.NoError(t, err)
	assert.Contains(t, diff, "tracked.txt")
	assert.Contains(t, diff, "newfile.txt")

	statusCmd := exec.Command("bash", "-c", "git diff --cached --name-only")
	statusCmd.Dir = projectDir
	out, err := statusCmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "tracked.txt", strings.TrimSpace(string(out)))
}

func TestGitDiffForAuditExcludesFryDir(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	require.NoError(t, os.MkdirAll(projectDir+"/.fry", 0o755))
	require.NoError(t, os.WriteFile(projectDir+"/.fry/sprint-progress.txt", []byte("progress\n"), 0o644))
	require.NoError(t, os.WriteFile(projectDir+"/code.go", []byte("package main\n"), 0o644))

	diff, err := GitDiffForAudit(context.Background(), projectDir)
	require.NoError(t, err)

	assert.Contains(t, diff, "code.go")
	assert.NotContains(t, diff, "sprint-progress.txt")
}

func TestDiffStatForNoopDetection_ExcludesProgressFiles(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	require.NoError(t, os.MkdirAll(projectDir+"/.fry", 0o755))
	require.NoError(t, os.WriteFile(projectDir+"/tracked.txt", []byte("original\n"), 0o644))
	cmd := exec.Command("bash", "-c", "git add tracked.txt && git commit -m 'add tracked'")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	require.NoError(t, os.WriteFile(projectDir+"/tracked.txt", []byte("updated\n"), 0o644))
	require.NoError(t, os.WriteFile(projectDir+"/.fry/sprint-progress.txt", []byte("progress\n"), 0o644))
	require.NoError(t, os.WriteFile(projectDir+"/.fry/epic-progress.txt", []byte("epic progress\n"), 0o644))

	diff := DiffStatForNoopDetection(context.Background(), projectDir)
	assert.Contains(t, diff, "tracked.txt")
	assert.NotContains(t, diff, "sprint-progress.txt")
	assert.NotContains(t, diff, "epic-progress.txt")
}

func TestWorktreeFingerprintForNoopDetection_IncludesUntrackedAndExcludesProgressFiles(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	require.NoError(t, os.MkdirAll(projectDir+"/.fry", 0o755))
	require.NoError(t, os.WriteFile(projectDir+"/tracked.txt", []byte("original\n"), 0o644))
	cmd := exec.Command("bash", "-c", "git add tracked.txt && git commit -m 'add tracked'")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	require.NoError(t, os.WriteFile(projectDir+"/new-file.txt", []byte("new content\n"), 0o644))
	require.NoError(t, os.WriteFile(projectDir+"/.fry/sprint-progress.txt", []byte("progress\n"), 0o644))

	fingerprint := WorktreeFingerprintForNoopDetection(context.Background(), projectDir)
	assert.Contains(t, fingerprint, "?? new-file.txt")
	assert.NotContains(t, fingerprint, "sprint-progress.txt")
	assert.NotContains(t, fingerprint, "__git_error_")
	assert.NotContains(t, fingerprint, "__git_status_error_")
}

// P1: CommitPartialWork

func TestCommitPartialWork(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))
	require.NoError(t, os.WriteFile(projectDir+"/partial.txt", []byte("wip\n"), 0o644))
	require.NoError(t, CommitPartialWork(context.Background(), projectDir, "TestEpic", 3, "Fix auth (#42)"))

	cmd := exec.Command("bash", "-c", "git log -1 --pretty=%s")
	cmd.Dir = projectDir
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "TestEpic — Fix auth (#42): Sprint 3 failed-partial [automated]", strings.TrimSpace(string(output)))
}

// P1: ensureLocalIdentity

func TestEnsureLocalIdentity(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	initCmd := exec.Command("bash", "-c", "git init")
	initCmd.Dir = projectDir
	require.NoError(t, initCmd.Run())

	// ensureLocalIdentity should not error regardless of whether
	// global git config provides user.name/user.email or not.
	require.NoError(t, ensureLocalIdentity(context.Background(), projectDir))

	// After calling ensureLocalIdentity, at least one of local or global
	// should provide user.name — verify git agrees.
	nameVal, err := gitConfigValue(context.Background(), projectDir, "user.name")
	require.NoError(t, err)
	assert.NotEmpty(t, strings.TrimSpace(nameVal))

	emailVal, err := gitConfigValue(context.Background(), projectDir, "user.email")
	require.NoError(t, err)
	assert.NotEmpty(t, strings.TrimSpace(emailVal))
}

func TestEnsureLocalIdentity_PreservesExisting(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	cmd := exec.Command("bash", "-c", "git init && git config user.name 'Custom User' && git config user.email 'custom@test.com'")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	require.NoError(t, ensureLocalIdentity(context.Background(), projectDir))

	name, err := gitConfigValue(context.Background(), projectDir, "user.name")
	require.NoError(t, err)
	assert.Equal(t, "Custom User", strings.TrimSpace(name))

	email, err := gitConfigValue(context.Background(), projectDir, "user.email")
	require.NoError(t, err)
	assert.Equal(t, "custom@test.com", strings.TrimSpace(email))
}

// P1: ensureGitignoreEntries

func TestEnsureGitignoreEntries(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, ensureGitignoreEntries(projectDir, []string{".fry/", ".env"}))

	data, err := os.ReadFile(projectDir + "/.gitignore")
	require.NoError(t, err)
	assert.Contains(t, string(data), ".fry/")
	assert.Contains(t, string(data), ".env")
}

func TestEnsureGitignoreEntries_Idempotent(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(projectDir+"/.gitignore", []byte(".fry/\n"), 0o644))

	require.NoError(t, ensureGitignoreEntries(projectDir, []string{".fry/", ".env"}))

	data, err := os.ReadFile(projectDir + "/.gitignore")
	require.NoError(t, err)
	content := string(data)
	assert.Equal(t, 1, strings.Count(content, ".fry/"))
	assert.Contains(t, content, ".env")
}

// P1: GitDiffForAudit with no changes

func TestGitDiffForAudit_NoChanges(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	diff, err := GitDiffForAudit(context.Background(), projectDir)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(diff))
}

// P1: gitConfigValue for missing key
// This also covers the #52 exit-code-1 → ("", nil) contract in ExecExecutor.ConfigGet.

func TestGitConfigValue_MissingKey(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	cmd := exec.Command("bash", "-c", "git init")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	val, err := gitConfigValue(context.Background(), projectDir, "nonexistent.key")
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(val))
}

// P1: hasHead

func TestHasHead_EmptyRepo(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	cmd := exec.Command("bash", "-c", "git init")
	cmd.Dir = projectDir
	require.NoError(t, cmd.Run())

	assert.False(t, hasHead(context.Background(), projectDir))
}

func TestHasHead_AfterCommit(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))

	assert.True(t, hasHead(context.Background(), projectDir))
}

// P1: GitCheckpoint with special characters in epic name

func TestGitCheckpoint_SpecialChars(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, InitGit(context.Background(), projectDir))
	require.NoError(t, os.WriteFile(projectDir+"/file.txt", []byte("data\n"), 0o644))
	require.NoError(t, GitCheckpoint(context.Background(), projectDir, "Epic's \"Name\" (v2)", 1, "Sprint One", "complete"))

	cmd := exec.Command("bash", "-c", "git log -1 --pretty=%s")
	cmd.Dir = projectDir
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Contains(t, strings.TrimSpace(string(output)), "Epic's")
}

// mockExecutor is a test double for the Executor interface.
// Set only the function fields you need; unset fields return zero values and nil errors.
type mockExecutor struct {
	IsRepoFn            func(ctx context.Context, dir string) bool
	HasHeadFn           func(ctx context.Context, dir string) bool
	CurrentBranchFn     func(ctx context.Context, dir string) string
	BranchExistsFn      func(ctx context.Context, dir string, name string) bool
	InitFn              func(ctx context.Context, dir string) error
	ConfigGetFn         func(ctx context.Context, dir string, key string) (string, error)
	ConfigSetFn         func(ctx context.Context, dir string, key, value string) error
	AddAllFn            func(ctx context.Context, dir string) error
	AddIntentFn         func(ctx context.Context, dir string, paths []string) error
	CommitAllowEmptyFn  func(ctx context.Context, dir string, message string) error
	ResetFn             func(ctx context.Context, dir string, paths []string) error
	CheckoutFn          func(ctx context.Context, dir string, name string) error
	CreateAndCheckoutFn func(ctx context.Context, dir string, name string) error
	DiffHeadFn          func(ctx context.Context, dir string, excludePathspecs []string) (string, error)
	DiffStatFn          func(ctx context.Context, dir string, excludePathspecs []string) (string, error)
	ListUntrackedFn     func(ctx context.Context, dir string, excludePathspecs []string) ([]string, error)
	StatusPorcelainFn   func(ctx context.Context, dir string) (string, error)
	LogGrepFn           func(ctx context.Context, dir string, grepPattern string, maxCount int, format string) (string, error)
	RestoreFilesFn      func(ctx context.Context, dir string, files []string) error
	CommitCountFn       func(ctx context.Context, dir string) (int, error)
	ReadIndexFileFn     func(ctx context.Context, dir string, relativePath string) ([]byte, error)
	CheckoutIndexAllFn  func(ctx context.Context, dir string) error
	ListADFilesFn       func(ctx context.Context, dir string) ([]string, error)
	WorktreeListFn      func(ctx context.Context, dir string) ([]string, error)
	WorktreeAddFn       func(ctx context.Context, dir string, worktreePath, branchName string, createBranch bool) error
	WorktreePruneFn     func(ctx context.Context, dir string) error
}

func (m *mockExecutor) IsRepo(ctx context.Context, dir string) bool {
	if m.IsRepoFn != nil {
		return m.IsRepoFn(ctx, dir)
	}
	return false
}
func (m *mockExecutor) HasHead(ctx context.Context, dir string) bool {
	if m.HasHeadFn != nil {
		return m.HasHeadFn(ctx, dir)
	}
	return false
}
func (m *mockExecutor) CurrentBranch(ctx context.Context, dir string) string {
	if m.CurrentBranchFn != nil {
		return m.CurrentBranchFn(ctx, dir)
	}
	return ""
}
func (m *mockExecutor) BranchExists(ctx context.Context, dir string, name string) bool {
	if m.BranchExistsFn != nil {
		return m.BranchExistsFn(ctx, dir, name)
	}
	return false
}
func (m *mockExecutor) Init(ctx context.Context, dir string) error {
	if m.InitFn != nil {
		return m.InitFn(ctx, dir)
	}
	return nil
}
func (m *mockExecutor) ConfigGet(ctx context.Context, dir string, key string) (string, error) {
	if m.ConfigGetFn != nil {
		return m.ConfigGetFn(ctx, dir, key)
	}
	return "", nil
}
func (m *mockExecutor) ConfigSet(ctx context.Context, dir string, key, value string) error {
	if m.ConfigSetFn != nil {
		return m.ConfigSetFn(ctx, dir, key, value)
	}
	return nil
}
func (m *mockExecutor) AddAll(ctx context.Context, dir string) error {
	if m.AddAllFn != nil {
		return m.AddAllFn(ctx, dir)
	}
	return nil
}
func (m *mockExecutor) AddIntent(ctx context.Context, dir string, paths []string) error {
	if m.AddIntentFn != nil {
		return m.AddIntentFn(ctx, dir, paths)
	}
	return nil
}
func (m *mockExecutor) CommitAllowEmpty(ctx context.Context, dir string, message string) error {
	if m.CommitAllowEmptyFn != nil {
		return m.CommitAllowEmptyFn(ctx, dir, message)
	}
	return nil
}
func (m *mockExecutor) Reset(ctx context.Context, dir string, paths []string) error {
	if m.ResetFn != nil {
		return m.ResetFn(ctx, dir, paths)
	}
	return nil
}
func (m *mockExecutor) Checkout(ctx context.Context, dir string, name string) error {
	if m.CheckoutFn != nil {
		return m.CheckoutFn(ctx, dir, name)
	}
	return nil
}
func (m *mockExecutor) CreateAndCheckout(ctx context.Context, dir string, name string) error {
	if m.CreateAndCheckoutFn != nil {
		return m.CreateAndCheckoutFn(ctx, dir, name)
	}
	return nil
}
func (m *mockExecutor) DiffHead(ctx context.Context, dir string, excludePathspecs []string) (string, error) {
	if m.DiffHeadFn != nil {
		return m.DiffHeadFn(ctx, dir, excludePathspecs)
	}
	return "", nil
}
func (m *mockExecutor) DiffStat(ctx context.Context, dir string, excludePathspecs []string) (string, error) {
	if m.DiffStatFn != nil {
		return m.DiffStatFn(ctx, dir, excludePathspecs)
	}
	return "", nil
}
func (m *mockExecutor) ListUntracked(ctx context.Context, dir string, excludePathspecs []string) ([]string, error) {
	if m.ListUntrackedFn != nil {
		return m.ListUntrackedFn(ctx, dir, excludePathspecs)
	}
	return nil, nil
}
func (m *mockExecutor) StatusPorcelain(ctx context.Context, dir string) (string, error) {
	if m.StatusPorcelainFn != nil {
		return m.StatusPorcelainFn(ctx, dir)
	}
	return "", nil
}
func (m *mockExecutor) LogGrep(ctx context.Context, dir string, grepPattern string, maxCount int, format string) (string, error) {
	if m.LogGrepFn != nil {
		return m.LogGrepFn(ctx, dir, grepPattern, maxCount, format)
	}
	return "", nil
}
func (m *mockExecutor) RestoreFiles(ctx context.Context, dir string, files []string) error {
	if m.RestoreFilesFn != nil {
		return m.RestoreFilesFn(ctx, dir, files)
	}
	return nil
}
func (m *mockExecutor) CommitCount(ctx context.Context, dir string) (int, error) {
	if m.CommitCountFn != nil {
		return m.CommitCountFn(ctx, dir)
	}
	return 0, nil
}
func (m *mockExecutor) ReadIndexFile(ctx context.Context, dir string, relativePath string) ([]byte, error) {
	if m.ReadIndexFileFn != nil {
		return m.ReadIndexFileFn(ctx, dir, relativePath)
	}
	return nil, nil
}
func (m *mockExecutor) CheckoutIndexAll(ctx context.Context, dir string) error {
	if m.CheckoutIndexAllFn != nil {
		return m.CheckoutIndexAllFn(ctx, dir)
	}
	return nil
}
func (m *mockExecutor) ListADFiles(ctx context.Context, dir string) ([]string, error) {
	if m.ListADFilesFn != nil {
		return m.ListADFilesFn(ctx, dir)
	}
	return nil, nil
}
func (m *mockExecutor) WorktreeList(ctx context.Context, dir string) ([]string, error) {
	if m.WorktreeListFn != nil {
		return m.WorktreeListFn(ctx, dir)
	}
	return nil, nil
}
func (m *mockExecutor) WorktreeAdd(ctx context.Context, dir string, worktreePath, branchName string, createBranch bool) error {
	if m.WorktreeAddFn != nil {
		return m.WorktreeAddFn(ctx, dir, worktreePath, branchName, createBranch)
	}
	return nil
}
func (m *mockExecutor) WorktreePrune(ctx context.Context, dir string) error {
	if m.WorktreePruneFn != nil {
		return m.WorktreePruneFn(ctx, dir)
	}
	return nil
}

// #52: ConfigGet error propagation tests

func TestEnsureLocalIdentityWith_ConfigGetError(t *testing.T) {
	t.Parallel()

	configErr := errors.New("git config failed")
	ex := &mockExecutor{
		ConfigGetFn: func(_ context.Context, _ string, _ string) (string, error) {
			return "", configErr
		},
	}
	err := ensureLocalIdentityWith(context.Background(), t.TempDir(), ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get git user.name")
	assert.ErrorIs(t, err, configErr)
}

func TestEnsureLocalIdentityWith_ConfigGetEmailError(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32
	configErr := errors.New("git config email failed")
	ex := &mockExecutor{
		ConfigGetFn: func(_ context.Context, _ string, _ string) (string, error) {
			if callCount.Add(1) == 1 {
				return "existing-name", nil
			}
			return "", configErr
		},
	}
	err := ensureLocalIdentityWith(context.Background(), t.TempDir(), ex)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get git user.email")
	assert.ErrorIs(t, err, configErr)
}

// #31: DiffStatForNoopDetectionWith tests

func TestDiffStatForNoopDetectionWith_Success(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "file.go | 3 +++", nil
		},
	}
	result := DiffStatForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.Equal(t, "file.go | 3 +++", result)
}

func TestDiffStatForNoopDetectionWith_DiffStatError(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "", errors.New("git error")
		},
	}
	result := DiffStatForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.True(t, strings.HasPrefix(result, "__git_error_"), "expected __git_error_ prefix, got %q", result)
}

func TestDiffStatForNoopDetectionWith_EmptyDiff(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "", nil
		},
	}
	result := DiffStatForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.Equal(t, "", result)
}

func TestWorktreeFingerprintForNoopDetectionWith_CombinesDiffAndStatus(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "file.go | 3 ++-", nil
		},
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "?? new-file.txt\n", nil
		},
	}

	result := WorktreeFingerprintForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.Contains(t, result, "file.go | 3 ++-")
	assert.Contains(t, result, "?? new-file.txt")
}

func TestWorktreeFingerprintForNoopDetectionWith_ExcludesProgressFiles(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "", nil
		},
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return " M .fry/sprint-progress.txt\n?? .fry/epic-progress.txt\n?? src/new-file.go\n", nil
		},
	}

	result := WorktreeFingerprintForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.Equal(t, "?? src/new-file.go", result)
}

func TestWorktreeFingerprintForNoopDetectionWith_StatusError(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		DiffStatFn: func(_ context.Context, _ string, _ []string) (string, error) {
			return "", nil
		},
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("status error")
		},
	}

	result := WorktreeFingerprintForNoopDetectionWith(context.Background(), t.TempDir(), ex)
	assert.True(t, strings.HasPrefix(result, "__git_status_error_"), "expected __git_status_error_ prefix, got %q", result)
}

// #31: CollectStateWith tests

func TestCollectStateWith_AllSucceed(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		CurrentBranchFn: func(_ context.Context, _ string) string {
			return "main"
		},
		LogGrepFn: func(_ context.Context, _ string, _ string, _ int, _ string) (string, error) {
			return "Epic: Sprint 3 complete [automated]", nil
		},
	}
	clean, branch, lastCommit := CollectStateWith(context.Background(), t.TempDir(), ex)
	assert.True(t, clean)
	assert.Equal(t, "main", branch)
	assert.Contains(t, lastCommit, "Sprint 3")
}

func TestCollectStateWith_StatusPorcelainFails(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("porcelain error")
		},
	}
	clean, _, _ := CollectStateWith(context.Background(), t.TempDir(), ex)
	assert.True(t, clean)
}

func TestCollectStateWith_LogGrepFails(t *testing.T) {
	t.Parallel()

	ex := &mockExecutor{
		StatusPorcelainFn: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
		CurrentBranchFn: func(_ context.Context, _ string) string {
			return "main"
		},
		LogGrepFn: func(_ context.Context, _ string, _ string, _ int, _ string) (string, error) {
			return "", errors.New("log error")
		},
	}
	_, _, lastCommit := CollectStateWith(context.Background(), t.TempDir(), ex)
	assert.Equal(t, "", lastCommit)
}

func TestRestoreFiles_TrackedAndUntracked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	// Initialize a git repo with a tracked file.
	require.NoError(t, InitGit(ctx, dir))
	tracked := filepath.Join(dir, "tracked.go")
	require.NoError(t, os.WriteFile(tracked, []byte("original\n"), 0o644))
	require.NoError(t, DefaultExecutor.AddAll(ctx, dir))
	require.NoError(t, DefaultExecutor.CommitAllowEmpty(ctx, dir, "init"))

	// Modify the tracked file and create a new untracked file.
	require.NoError(t, os.WriteFile(tracked, []byte("modified\n"), 0o644))
	untracked := filepath.Join(dir, "newfile.go")
	require.NoError(t, os.WriteFile(untracked, []byte("brand new\n"), 0o644))

	// RestoreFiles should revert the tracked file and remove the untracked one.
	err := RestoreFiles(ctx, dir, []string{"tracked.go", "newfile.go"})
	require.NoError(t, err)

	data, readErr := os.ReadFile(tracked)
	require.NoError(t, readErr)
	assert.Equal(t, "original\n", string(data), "tracked file should be restored to HEAD")

	_, statErr := os.Stat(untracked)
	assert.True(t, os.IsNotExist(statErr), "untracked file should be deleted")
}

func TestRestoreFiles_EmptyList(t *testing.T) {
	t.Parallel()

	err := RestoreFiles(context.Background(), t.TempDir(), nil)
	assert.NoError(t, err)
}

func TestIsFreshlyInitialized(t *testing.T) {
	t.Parallel()

	t.Run("fresh init returns true", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		assert.True(t, IsFreshlyInitialized(context.Background(), dir))
	})

	t.Run("multiple commits returns false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		// Add a second commit
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0o644))
		ex := &ExecExecutor{}
		require.NoError(t, ex.AddAll(context.Background(), dir))
		require.NoError(t, ex.CommitAllowEmpty(context.Background(), dir, "second commit"))
		assert.False(t, IsFreshlyInitialized(context.Background(), dir))
	})

	t.Run("one commit with different message returns false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		ex := &ExecExecutor{}
		require.NoError(t, ex.Init(context.Background(), dir))
		require.NoError(t, ensureLocalIdentity(context.Background(), dir))
		require.NoError(t, ex.CommitAllowEmpty(context.Background(), dir, "Custom first commit"))
		assert.False(t, IsFreshlyInitialized(context.Background(), dir))
	})

	t.Run("not a repo returns false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		assert.False(t, IsFreshlyInitialized(context.Background(), dir))
	})
}

func TestIsFreshlyInitializedWith_Mock(t *testing.T) {
	t.Parallel()

	t.Run("exactly one automated commit", func(t *testing.T) {
		t.Parallel()
		ex := &mockExecutor{
			CommitCountFn: func(_ context.Context, _ string) (int, error) { return 1, nil },
			LogGrepFn: func(_ context.Context, _ string, _ string, _ int, _ string) (string, error) {
				return initialCommitMessage, nil
			},
		}
		assert.True(t, IsFreshlyInitializedWith(context.Background(), t.TempDir(), ex))
	})

	t.Run("commit count error", func(t *testing.T) {
		t.Parallel()
		ex := &mockExecutor{
			CommitCountFn: func(_ context.Context, _ string) (int, error) { return 0, errors.New("no head") },
		}
		assert.False(t, IsFreshlyInitializedWith(context.Background(), t.TempDir(), ex))
	})
}

func TestCommitCount(t *testing.T) {
	t.Parallel()

	t.Run("one commit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		count, err := DefaultExecutor.CommitCount(context.Background(), dir)
		require.NoError(t, err)
		assert.Equal(t, 1, count)
	})

	t.Run("two commits", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		ex := &ExecExecutor{}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
		require.NoError(t, ex.AddAll(context.Background(), dir))
		require.NoError(t, ex.CommitAllowEmpty(context.Background(), dir, "second"))
		count, err := ex.CommitCount(context.Background(), dir)
		require.NoError(t, err)
		assert.Equal(t, 2, count)
	})
}

func TestReadIndexFile(t *testing.T) {
	t.Parallel()

	t.Run("reads staged file missing from disk", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		// Create and stage a file
		filePath := filepath.Join(dir, "hello.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("hello world"), 0o644))
		ex := &ExecExecutor{}
		require.NoError(t, ex.AddAll(context.Background(), dir))

		// Delete from disk but keep in index
		require.NoError(t, os.Remove(filePath))

		data, err := ReadIndexFile(context.Background(), dir, "hello.txt")
		require.NoError(t, err)
		assert.Equal(t, "hello world", string(data))
	})

	t.Run("error for nonexistent file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		_, err := ReadIndexFile(context.Background(), dir, "nonexistent.txt")
		assert.Error(t, err)
	})
}

func TestCheckoutIndexAll(t *testing.T) {
	t.Parallel()

	t.Run("restores AD-status files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		// Create and stage files
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0o644))
		ex := &ExecExecutor{}
		require.NoError(t, ex.AddAll(context.Background(), dir))

		// Delete from disk
		require.NoError(t, os.Remove(filepath.Join(dir, "a.txt")))
		require.NoError(t, os.Remove(filepath.Join(dir, "b.txt")))

		// Verify they're gone
		_, err := os.Stat(filepath.Join(dir, "a.txt"))
		require.True(t, os.IsNotExist(err))

		// Recover
		require.NoError(t, CheckoutIndexAll(context.Background(), dir))

		// Verify restored
		data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		require.NoError(t, err)
		assert.Equal(t, "aaa", string(data))

		data, err = os.ReadFile(filepath.Join(dir, "b.txt"))
		require.NoError(t, err)
		assert.Equal(t, "bbb", string(data))
	})
}

func TestListADFiles(t *testing.T) {
	t.Parallel()

	t.Run("detects AD-status files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		// Create and stage a new file
		require.NoError(t, os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged"), 0o644))
		ex := &ExecExecutor{}
		require.NoError(t, ex.AddAll(context.Background(), dir))

		// Delete from disk to create AD status
		require.NoError(t, os.Remove(filepath.Join(dir, "staged.txt")))

		adFiles, err := ListADFiles(context.Background(), dir)
		require.NoError(t, err)
		assert.Contains(t, adFiles, "staged.txt")
	})

	t.Run("empty when no AD files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		adFiles, err := ListADFiles(context.Background(), dir)
		require.NoError(t, err)
		assert.Empty(t, adFiles)
	})
}

func TestSnapshotAndRestoreFiles(t *testing.T) {
	t.Parallel()

	t.Run("restores modified file to pre-fix content", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "app.ts"), []byte("original"), 0o644))

		snap := SnapshotFiles(dir, []string{"src/app.ts"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "app.ts"), []byte("modified by fix"), 0o644))

		unhandled, err := RestoreFromSnapshot(dir, snap, []string{"src/app.ts"})
		require.NoError(t, err)
		assert.Empty(t, unhandled)

		data, readErr := os.ReadFile(filepath.Join(dir, "src", "app.ts"))
		require.NoError(t, readErr)
		assert.Equal(t, "original", string(data))
	})

	t.Run("deletes file that was absent in snapshot", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		snap := SnapshotFiles(dir, []string{"new-file.ts"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "new-file.ts"), []byte("created by fix"), 0o644))

		unhandled, err := RestoreFromSnapshot(dir, snap, []string{"new-file.ts"})
		require.NoError(t, err)
		assert.Empty(t, unhandled)

		_, statErr := os.Stat(filepath.Join(dir, "new-file.ts"))
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("preserves sprint files not in HEAD", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "sprint-file.ts"), []byte("sprint work"), 0o644))

		snap := SnapshotFiles(dir, []string{"src/sprint-file.ts"})
		require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "sprint-file.ts"), []byte("fix changed this"), 0o644))

		unhandled, err := RestoreFromSnapshot(dir, snap, []string{"src/sprint-file.ts"})
		require.NoError(t, err)
		assert.Empty(t, unhandled)

		data, readErr := os.ReadFile(filepath.Join(dir, "src", "sprint-file.ts"))
		require.NoError(t, readErr)
		assert.Equal(t, "sprint work", string(data), "sprint file should be restored to pre-fix content, not deleted")
	})

	t.Run("returns unhandled files not in snapshot", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "other.ts"), []byte("untouched"), 0o644))

		snap := SnapshotFiles(dir, []string{"unrelated.ts"})

		unhandled, err := RestoreFromSnapshot(dir, snap, []string{"other.ts"})
		require.NoError(t, err)
		assert.Equal(t, []string{"other.ts"}, unhandled)

		data, readErr := os.ReadFile(filepath.Join(dir, "other.ts"))
		require.NoError(t, readErr)
		assert.Equal(t, "untouched", string(data))
	})

	t.Run("nil snapshot returns all files as unhandled", func(t *testing.T) {
		t.Parallel()
		unhandled, err := RestoreFromSnapshot(t.TempDir(), nil, []string{"anything.ts"})
		require.NoError(t, err)
		assert.Equal(t, []string{"anything.ts"}, unhandled)
	})
}

func TestCleanNestedGitDirs(t *testing.T) {
	t.Parallel()

	t.Run("removes nested .git directories", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		// Create a nested .git directory (simulating create-next-app)
		nestedGit := filepath.Join(dir, "apps", "web", ".git")
		require.NoError(t, os.MkdirAll(nestedGit, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(nestedGit, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

		cleaned, err := CleanNestedGitDirs(dir)
		require.NoError(t, err)
		assert.Len(t, cleaned, 1)
		assert.Equal(t, filepath.Join("apps", "web", ".git"), cleaned[0])

		// Verify it's gone
		_, statErr := os.Stat(nestedGit)
		assert.True(t, os.IsNotExist(statErr))
	})

	t.Run("preserves root .git", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))

		cleaned, err := CleanNestedGitDirs(dir)
		require.NoError(t, err)
		assert.Empty(t, cleaned)

		// Root .git still exists
		_, statErr := os.Stat(filepath.Join(dir, ".git"))
		assert.NoError(t, statErr)
	})

	t.Run("no nested .git returns empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, InitGit(context.Background(), dir))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "apps", "api", "src"), 0o755))

		cleaned, err := CleanNestedGitDirs(dir)
		require.NoError(t, err)
		assert.Empty(t, cleaned)
	})
}
