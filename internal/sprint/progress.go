package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yevgetman/fry/internal/config"
)

func InitSprintProgress(projectDir string, sprintNum int, sprintName string) error {
	path := filepath.Join(projectDir, config.SprintProgressFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create sprint progress dir: %w", err)
	}
	content := fmt.Sprintf("# Sprint %d: %s — Progress\n\n", sprintNum, sprintName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write sprint progress: %w", err)
	}
	return nil
}

func InitEpicProgress(projectDir string, epicName string) error {
	path := filepath.Join(projectDir, config.EpicProgressFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create epic progress dir: %w", err)
	}
	content := fmt.Sprintf("# Epic Progress — %s\n\n", epicName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write epic progress: %w", err)
	}
	return nil
}

func ShouldResetEpicProgress(startSprint, currentSprint, endSprint, totalSprints int) bool {
	return startSprint == 1 && currentSprint == 1 && endSprint == totalSprints
}

// MaxSprintProgressBytes is the size threshold above which sprint-progress.txt
// is auto-compacted to prevent prompt bloat. The agent reads this file every
// iteration, so keeping it bounded saves significant tokens on long sprints.
const MaxSprintProgressBytes = 20_000

// TailLinesAfterCompaction is how many lines to keep from the end of
// sprint-progress.txt after compaction. These are the most recent entries
// that the agent needs to avoid repeating work.
const TailLinesAfterCompaction = 30

// CompactSprintProgressIfNeeded checks if sprint-progress.txt exceeds
// MaxSprintProgressBytes and truncates it to a header + the last
// TailLinesAfterCompaction lines if so. This is a mechanical operation
// (no LLM call) that preserves the most recent context while discarding
// older entries the agent no longer needs.
func CompactSprintProgressIfNeeded(projectDir string) {
	path := filepath.Join(projectDir, config.SprintProgressFile)
	info, err := os.Stat(path)
	if err != nil || info.Size() <= MaxSprintProgressBytes {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) <= TailLinesAfterCompaction+5 {
		return // not enough lines to compact
	}

	// Keep a header noting compaction occurred, then the last N lines.
	header := lines[0] // preserve the "# Sprint N: ..." header
	tail := lines[len(lines)-TailLinesAfterCompaction:]

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n\n[Earlier progress entries compacted to save context. Recent entries below.]\n\n")
	b.WriteString(strings.Join(tail, "\n"))

	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

func AppendToSprintProgress(projectDir string, content string) error {
	return appendFile(filepath.Join(projectDir, config.SprintProgressFile), content)
}

func AppendToEpicProgress(projectDir string, content string) error {
	return appendFile(filepath.Join(projectDir, config.EpicProgressFile), content)
}

func ReadSprintProgress(projectDir string) (string, error) {
	return readFile(filepath.Join(projectDir, config.SprintProgressFile))
}

func ReadEpicProgress(projectDir string) (string, error) {
	return readFile(filepath.Join(projectDir, config.EpicProgressFile))
}

func appendFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open append file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("append file: %w", err)
	}
	return nil
}

func readFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(content), nil
}
