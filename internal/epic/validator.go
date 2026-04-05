package epic

import "fmt"

func ValidateEpic(e *Epic) error {
	if len(e.Sprints) == 0 {
		return fmt.Errorf("epic must contain at least one sprint")
	}

	seenNames := make(map[string]int, len(e.Sprints))
	for i, sprint := range e.Sprints {
		expected := i + 1
		if sprint.Number != expected {
			return fmt.Errorf("sprint numbering must be sequential: expected sprint %d, got %d", expected, sprint.Number)
		}
		if sprint.Name == "" {
			return fmt.Errorf("sprint %d is missing @name", sprint.Number)
		}
		if prev, dup := seenNames[sprint.Name]; dup {
			return fmt.Errorf("sprint %d has duplicate name %q (already used by sprint %d)", sprint.Number, sprint.Name, prev)
		}
		seenNames[sprint.Name] = sprint.Number
		if sprint.MaxIterations <= 0 {
			return fmt.Errorf("sprint %d must have @max_iterations greater than 0", sprint.Number)
		}
		if sprint.Promise == "" {
			return fmt.Errorf("sprint %d is missing @promise", sprint.Number)
		}
		if sprint.Prompt == "" {
			return fmt.Errorf("sprint %d is missing @prompt content", sprint.Number)
		}
	}

	if e.MaxFailPercent < 0 || e.MaxFailPercent > 100 {
		return fmt.Errorf("@max_fail_percent must be between 0 and 100, got %d", e.MaxFailPercent)
	}

	// Validate sprint count against effort level (if set)
	if e.EffortLevel != "" {
		maxSprints := e.EffortLevel.MaxSprintCount()
		if len(e.Sprints) > maxSprints {
			return fmt.Errorf("effort level %q allows at most %d sprints, but epic has %d",
				e.EffortLevel, maxSprints, len(e.Sprints))
		}
	}

	return nil
}
