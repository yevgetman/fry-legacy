// Package steering provides file-based IPC for mid-build human intervention.
// External agents or humans write files; the sprint loop reads them.
package steering

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yevgetman/fry/internal/config"
)

// ReadDirective reads and returns the content of the agent directive file.
// Returns empty string if no directive is pending.
func ReadDirective(projectDir string) (string, error) {
	path := filepath.Join(projectDir, config.AgentDirectiveFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read directive: %w", err)
	}
	return string(data), nil
}

// ConsumeDirective atomically reads and removes the agent directive file.
// Returns empty string if no directive is pending. This avoids the TOCTOU
// race between ReadDirective and ClearDirective.
func ConsumeDirective(projectDir string) (string, error) {
	path := filepath.Join(projectDir, config.AgentDirectiveFile)
	consumed := path + ".consumed"
	// Rename atomically removes the original — a new write won't be lost.
	if err := os.Rename(path, consumed); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("consume directive: rename: %w", err)
	}
	data, err := os.ReadFile(consumed)
	if err != nil {
		return "", fmt.Errorf("consume directive: read: %w", err)
	}
	_ = os.Remove(consumed)
	return string(data), nil
}

// ClearDirective removes the agent directive file.
func ClearDirective(projectDir string) error {
	path := filepath.Join(projectDir, config.AgentDirectiveFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear directive: %w", err)
	}
	return nil
}
