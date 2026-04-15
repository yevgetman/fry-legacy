package steering

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yevgetman/fry/internal/config"
)

// IsHoldRequested checks if a hold-after-sprint sentinel file exists.
func IsHoldRequested(projectDir string) bool {
	path := filepath.Join(projectDir, config.AgentHoldFile)
	_, err := os.Stat(path)
	return err == nil
}

// ClearHold removes the hold sentinel file.
func ClearHold(projectDir string) error {
	path := filepath.Join(projectDir, config.AgentHoldFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear hold: %w", err)
	}
	return nil
}

// WriteDecisionNeeded creates the decision-needed file with the given prompt.
// This signals to the watching agent that the build is waiting for human input.
func WriteDecisionNeeded(projectDir string, prompt string) error {
	path := filepath.Join(projectDir, config.DecisionNeededFile)
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(prompt), 0o600); err != nil {
		return fmt.Errorf("write decision needed: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write decision needed: rename: %w", err)
	}
	return nil
}

// ClearDecisionNeeded removes the decision-needed file.
func ClearDecisionNeeded(projectDir string) error {
	path := filepath.Join(projectDir, config.DecisionNeededFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear decision needed: %w", err)
	}
	return nil
}

// WaitForDecision blocks until a directive file appears (the agent's response)
// or ctx is canceled. Polls every 2 seconds. Returns the directive content.
// Uses ConsumeDirective for atomic read-and-delete to avoid TOCTOU races.
func WaitForDecision(ctx context.Context, projectDir string) (string, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := ReadStopRequest(projectDir)
		if err != nil {
			return "", fmt.Errorf("wait for decision: %w", err)
		}
		if req != nil {
			return "", NewExitRequestError("sprint_boundary", "while waiting for a steering decision")
		}

		directive, err := ConsumeDirective(projectDir)
		if err != nil {
			return "", fmt.Errorf("wait for decision: %w", err)
		}
		if directive != "" {
			return directive, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
