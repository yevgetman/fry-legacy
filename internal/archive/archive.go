package archive

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// rootArtifacts lists root-level build outputs that are archived alongside .fry/.
var rootArtifacts = []string{config.BuildAuditFile, config.SummaryFile, config.BuildAuditSARIFFile}

// Archive moves .fry/ and root-level build outputs into a timestamped folder
// under .fry-archive/. Codebase awareness files (codebase.md, file-index.txt,
// codebase-memories/) and project config now live in .fry-config/ and are not
// affected by archival.
// Returns the archive destination path.
func Archive(projectDir string) (string, error) {
	return ArchiveTo(projectDir, projectDir)
}

// ArchiveTo moves .fry/ from srcDir into a timestamped folder under
// destDir/.fry-archive/. Root-level build outputs (build-audit.md,
// build-summary.md, build-audit.sarif) are also moved from srcDir.
// Use this when the build ran in a worktree (srcDir) but archives
// should live in the original project dir (destDir).
func ArchiveTo(srcDir, destDir string) (string, error) {
	fryPath := filepath.Join(srcDir, config.FryDir)
	if _, err := os.Stat(fryPath); os.IsNotExist(err) {
		return "", fmt.Errorf("archive: %s does not exist in %s", config.FryDir, srcDir)
	} else if err != nil {
		return "", fmt.Errorf("archive: stat %s: %w", config.FryDir, err)
	}

	archiveRoot := filepath.Join(destDir, config.ArchiveDir)
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		return "", fmt.Errorf("archive: create %s: %w", config.ArchiveDir, err)
	}

	archiveName := config.ArchivePrefix + time.Now().Format("20060102-150405")
	destPath := filepath.Join(archiveRoot, archiveName)

	if err := moveDir(fryPath, destPath); err != nil {
		return "", fmt.Errorf("archive: move %s: %w", config.FryDir, err)
	}

	for _, name := range rootArtifacts {
		src := filepath.Join(srcDir, name)
		if _, err := os.Stat(src); os.IsNotExist(err) {
			continue
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "fry: warning: stat %s: %v\n", name, err)
			continue
		}
		dst := filepath.Join(destPath, name)
		if err := moveFile(src, dst); err != nil {
			return "", fmt.Errorf("archive: move %s: %w", name, err)
		}
	}

	return destPath, nil
}

// moveDir renames src to dst, falling back to a recursive copy + remove
// when the rename fails (e.g. cross-device moves).
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Fallback: copy tree then remove source.
	if err := copyDir(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// moveFile renames src to dst, falling back to read+write+remove
// when the rename fails (e.g. cross-device moves).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
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
