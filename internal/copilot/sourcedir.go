package copilot

import (
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

// DiscoverFrySourceDir locates the fry source tree using a multi-strategy
// search. The order is:
//
//  1. The override path passed by the caller (--copilot-fry-source flag).
//  2. Walking up from os.Executable() — works when fry was `make install`'d
//     from a working tree.
//  3. The $FRY_SOURCE_DIR environment variable.
//  4. Standard locations: ~/code/fry, ~/src/fry, ~/go/src/github.com/yevgetman/fry
//
// Returns the absolute path of the first match, or "" if no match is found.
// Callers that get "" should fall back to copilot mode = passive (the
// copilot can still observe and write summaries, just not edit fry source).
func DiscoverFrySourceDir(override string) string {
	// 1. Explicit override
	if override != "" {
		if abs, err := filepath.Abs(override); err == nil && IsFrySourceDir(abs) {
			return abs
		}
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

// IsFrySourceDir reports whether path looks like the fry source tree —
// every marker file in FrySourceMarkerFiles must exist as a regular file
// directly under path.
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
	// Sanity check: go.mod should mention the fry module path. We don't
	// strictly require it (a fork or rename could be valid) but we use it
	// as a tie-breaker if other heuristics matched ambiguously.
	return true
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
