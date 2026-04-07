package copilot

import (
	"os"
	"path/filepath"
)

// Test-only helpers wrapping os.* so the test files can be terse without
// importing os everywhere. These exist only to make the test files easier
// to read; they're not part of the public API.

func filepathStat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func mkdirAllForFile(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
