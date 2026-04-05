package epic

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeValidSprint(number int) Sprint {
	return Sprint{
		Number:        number,
		Name:          fmt.Sprintf("Sprint %d", number),
		MaxIterations: 10,
		Promise:       "something done",
		Prompt:        "do the thing",
	}
}

func TestValidateEpic_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		epic        *Epic
		wantErr     bool
		errContains string
	}{
		{
			name: "happy path",
			epic: &Epic{
				Sprints:        []Sprint{makeValidSprint(1)},
				MaxFailPercent: 50,
			},
			wantErr: false,
		},
		{
			name: "no sprints",
			epic: &Epic{
				Sprints: []Sprint{},
			},
			wantErr:     true,
			errContains: "at least one sprint",
		},
		{
			name: "non-sequential numbering",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 2, Name: "Sprint", MaxIterations: 10, Promise: "done", Prompt: "do it"},
				},
			},
			wantErr:     true,
			errContains: "sequential",
		},
		{
			name: "missing name",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 1, Name: "", MaxIterations: 10, Promise: "done", Prompt: "do it"},
				},
			},
			wantErr:     true,
			errContains: "@name",
		},
		{
			name: "max_iterations zero",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 1, Name: "Sprint", MaxIterations: 0, Promise: "done", Prompt: "do it"},
				},
			},
			wantErr:     true,
			errContains: "@max_iterations",
		},
		{
			name: "missing promise",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 1, Name: "Sprint", MaxIterations: 10, Promise: "", Prompt: "do it"},
				},
			},
			wantErr:     true,
			errContains: "@promise",
		},
		{
			name: "missing prompt",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 1, Name: "Sprint", MaxIterations: 10, Promise: "done", Prompt: ""},
				},
			},
			wantErr:     true,
			errContains: "@prompt",
		},
		{
			name: "max_fail_percent below zero",
			epic: &Epic{
				Sprints:        []Sprint{makeValidSprint(1)},
				MaxFailPercent: -1,
			},
			wantErr:     true,
			errContains: "max_fail_percent",
		},
		{
			name: "max_fail_percent above 100",
			epic: &Epic{
				Sprints:        []Sprint{makeValidSprint(1)},
				MaxFailPercent: 101,
			},
			wantErr:     true,
			errContains: "max_fail_percent",
		},
		{
			name: "duplicate sprint names",
			epic: &Epic{
				Sprints: []Sprint{
					{Number: 1, Name: "Setup", MaxIterations: 10, Promise: "done", Prompt: "do it"},
					{Number: 2, Name: "Setup", MaxIterations: 10, Promise: "done", Prompt: "do it"},
				},
			},
			wantErr:     true,
			errContains: "duplicate name",
		},
		{
			name: "sprint count exceeds effort level",
			epic: &Epic{
				// EffortStandard.MaxSprintCount() == 4, so 5 sprints exceeds it
				EffortLevel: EffortStandard,
				Sprints: []Sprint{
					makeValidSprint(1),
					makeValidSprint(2),
					makeValidSprint(3),
					makeValidSprint(4),
					makeValidSprint(5),
				},
			},
			wantErr:     true,
			errContains: "allows at most",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateEpic(tc.epic)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
