package copilot

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// FrySourceMarkerFiles are the files that must exist in a directory for it
// to be recognised as the fry source tree. Both must be present — go.mod
// alone is not enough (any Go project has go.mod).
var FrySourceMarkerFiles = []string{
	"go.mod",
	filepath.Join("internal", "cli", "run.go"),
}

// expectedModulePath is the Go module path declared in fry's go.mod. We
// require it as a content check inside IsFrySourceDir so that arbitrary
// directories with a go.mod and an internal/cli/run.go cannot masquerade
// as a fry source tree.
const expectedModulePath = "github.com/yevgetman/fry"

// DiscoverFrySourceDir locates the fry source tree using a multi-strategy
// search. The order is:
//
//  1. The override path passed by the caller (--copilot-fry-source flag).
//  2. Walking up from os.Executable() — works when fry was `make install`'d
//     from a working tree.
//  3. Walking up from the current working directory.
//  4. The $FRY_SOURCE_DIR environment variable.
//  5. Standard locations: ~/code/fry, ~/src/fry, ~/go/src/github.com/yevgetman/fry
//
// Returns the absolute path of the first match, or "" if no match is found.
// Callers that get "" should fall back to copilot mode = passive (the
// copilot can still observe and write summaries, just not edit fry source).
//
// If the explicit override is non-empty but does not satisfy IsFrySourceDir,
// the function reports a non-fatal warning to stderr so the user knows
// their flag was rejected before falling through.
func DiscoverFrySourceDir(override string) string {
	// 1. Explicit override
	if override != "" {
		if abs, err := filepath.Abs(override); err == nil && IsFrySourceDir(abs) {
			return abs
		}
		fmt.Fprintf(os.Stderr, "fry: warning: --copilot-fry-source=%q is not a valid fry source tree, falling back to auto-detection\n", override)
	}

	// 2. Walk up from executable
	if exe, err := os.Executable(); err == nil {
		// Resolve symlinks so $GOPATH/bin/fry pointing into a build dir works.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if found := walkUp(filepath.Dir(exe)); found != "" {
			return found
		}
	}

	// 3. Walk up from current working directory
	if cwd, err := os.Getwd(); err == nil {
		if found := walkUp(cwd); found != "" {
			return found
		}
	}

	// 4. $FRY_SOURCE_DIR environment variable
	if env := os.Getenv("FRY_SOURCE_DIR"); env != "" {
		if abs, err := filepath.Abs(env); err == nil && IsFrySourceDir(abs) {
			return abs
		}
	}

	// 5. Standard locations
	home, err := os.UserHomeDir()
	if err == nil {
		candidates := []string{
			filepath.Join(home, "code", "fry"),
			filepath.Join(home, "src", "fry"),
			filepath.Join(home, "go", "src", "github.com", "yevgetman", "fry"),
		}
		for _, candidate := range candidates {
			if IsFrySourceDir(candidate) {
				return candidate
			}
		}
	}

	return ""
}

// IsFrySourceDir reports whether path looks like the fry source tree.
// Every marker file in FrySourceMarkerFiles must exist as a regular file
// directly under path, AND go.mod must declare expectedModulePath. The
// content check prevents arbitrary projects with the same file layout
// from being misidentified as fry.
func IsFrySourceDir(path string) bool {
	if path == "" {
		return false
	}
	for _, marker := range FrySourceMarkerFiles {
		full := filepath.Join(path, marker)
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			return false
		}
	}
	gomod, err := os.ReadFile(filepath.Join(path, "go.mod"))
	if err != nil {
		return false
	}
	return bytes.Contains(gomod, []byte("module "+expectedModulePath))
}

// walkUp climbs the directory tree from start, returning the first ancestor
// that satisfies IsFrySourceDir. Returns "" on filesystem root.
func walkUp(start string) string {
	dir := start
	for {
		if IsFrySourceDir(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
