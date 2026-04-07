package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeFrySourceDir builds a fake fry source tree under root with the marker
// files in place.
func makeFrySourceDir(t *testing.T, root string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/yevgetman/fry\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "cli", "run.go"), []byte("package cli\n"), 0o644))
	return root
}

func TestIsFrySourceDirHappyPath(t *testing.T) {
	t.Parallel()
	root := makeFrySourceDir(t, t.TempDir())
	assert.True(t, IsFrySourceDir(root))
}

func TestIsFrySourceDirMissingMarker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Only go.mod, no internal/cli/run.go
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644))

	assert.False(t, IsFrySourceDir(root))
}

func TestIsFrySourceDirWrongModulePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Marker files exist but the module path is not fry — should be rejected.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "cli"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/somebody/else\n\ngo 1.22\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "cli", "run.go"), []byte("package cli\n"), 0o644))

	assert.False(t, IsFrySourceDir(root), "wrong module path should be rejected even when marker files exist")
}

func TestIsFrySourceDirEmptyPath(t *testing.T) {
	t.Parallel()
	assert.False(t, IsFrySourceDir(""))
}

func TestIsFrySourceDirNonexistent(t *testing.T) {
	t.Parallel()
	assert.False(t, IsFrySourceDir(filepath.Join(t.TempDir(), "does-not-exist")))
}

func TestDiscoverFrySourceDirOverride(t *testing.T) {
	t.Parallel()
	root := makeFrySourceDir(t, t.TempDir())

	got := DiscoverFrySourceDir(root)
	assert.Equal(t, root, got)
}

func TestDiscoverFrySourceDirOverrideInvalid(t *testing.T) {
	t.Parallel()
	root := t.TempDir() // empty dir, not a valid fry source

	// Invalid override falls through to other strategies. We can't reliably
	// test "no other strategy matches" because the test runner itself lives
	// in a fry source tree. So we just verify the override is not used:
	got := DiscoverFrySourceDir(root)
	assert.NotEqual(t, root, got, "invalid override should not be returned")
}

func TestDiscoverFrySourceDirFindsRealTree(t *testing.T) {
	t.Parallel()
	// The test binary itself runs from inside the fry source tree, so
	// DiscoverFrySourceDir("") should always find SOMETHING via walk-up.
	got := DiscoverFrySourceDir("")
	assert.NotEqual(t, "", got, "should find a fry source dir from the test binary's location")
	assert.True(t, IsFrySourceDir(got))
}

func TestWalkUpFindsAncestor(t *testing.T) {
	t.Parallel()
	root := makeFrySourceDir(t, t.TempDir())
	deep := filepath.Join(root, "internal", "cli")

	got := walkUp(deep)
	assert.Equal(t, root, got)
}

func TestWalkUpReachesRoot(t *testing.T) {
	t.Parallel()
	// Walking up from a tmp directory with no fry source should reach the
	// filesystem root and return "".
	dir := t.TempDir()
	got := walkUp(dir)
	// We may or may not find a fry source tree depending on where t.TempDir
	// is rooted on the test machine. The important assertion is that
	// walkUp doesn't panic and returns either "" or a valid fry source dir.
	if got != "" {
		assert.True(t, IsFrySourceDir(got))
	}
}
