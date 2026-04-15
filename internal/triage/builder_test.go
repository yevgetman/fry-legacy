package triage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/verify"
)

func TestBuildSimpleEpic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		opts             SimpleEpicOpts
		wantErr          bool
		wantPrompt       string
		wantSprints      int
		wantEffort       epic.EffortLevel
		wantMaxIter      int
		wantAudit        bool
		wantAuditIterSet bool
		wantAuditIter    int
	}{
		{
			name: "default effort (fast)",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Build a CLI tool",
				EngineName:  "claude",
			},
			wantPrompt:  "Build a CLI tool",
			wantSprints: 1,
			wantEffort:  epic.EffortFast,
			wantMaxIter: 12,
			wantAudit:   false,
		},
		{
			name: "fast effort explicit",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Fix a typo",
				EngineName:  "claude",
				EffortLevel: epic.EffortFast,
			},
			wantPrompt:  "Fix a typo",
			wantSprints: 1,
			wantEffort:  epic.EffortFast,
			wantMaxIter: 12,
			wantAudit:   false,
		},
		{
			name: "standard effort enables audit with cap",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Add a new config option",
				EngineName:  "claude",
				EffortLevel: epic.EffortStandard,
			},
			wantPrompt:       "Add a new config option",
			wantSprints:      1,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantAudit:        true,
			wantAuditIterSet: true,
			wantAuditIter:    1,
		},
		{
			name: "high effort enables audit with cap",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Refactor error handling in one package",
				EngineName:  "claude",
				EffortLevel: epic.EffortHigh,
			},
			wantPrompt:       "Refactor error handling in one package",
			wantSprints:      1,
			wantEffort:       epic.EffortHigh,
			wantMaxIter:      25,
			wantAudit:        true,
			wantAuditIterSet: true,
			wantAuditIter:    1,
		},
		{
			name: "max effort capped to fast",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Quick change",
				EngineName:  "claude",
				EffortLevel: epic.EffortMax,
			},
			wantPrompt:  "Quick change",
			wantSprints: 1,
			wantEffort:  epic.EffortFast,
			wantMaxIter: 12,
			wantAudit:   false,
		},
		{
			name: "plan takes precedence over user prompt",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "The plan",
				UserPrompt:  "The user prompt",
				EngineName:  "claude",
			},
			wantPrompt:  "The plan",
			wantSprints: 1,
			wantEffort:  epic.EffortFast,
			wantMaxIter: 12,
			wantAudit:   false,
		},
		{
			name: "exec content as fallback",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				ExecContent: "Executive context",
				EngineName:  "codex",
			},
			wantPrompt:  "Executive context",
			wantSprints: 1,
			wantEffort:  epic.EffortFast,
			wantMaxIter: 12,
			wantAudit:   false,
		},
		{
			name: "empty content returns error",
			opts: SimpleEpicOpts{
				ProjectDir: "/tmp/test",
				EngineName: "claude",
			},
			wantErr: true,
		},
		{
			name: "whitespace-only content returns error",
			opts: SimpleEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "   \n  \t  ",
				EngineName:  "claude",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ep, err := BuildSimpleEpic(tt.opts)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, ep)

			assert.Equal(t, "Simple Task", ep.Name)
			assert.Equal(t, tt.opts.EngineName, ep.Engine)
			assert.Equal(t, tt.wantEffort, ep.EffortLevel)
			assert.Equal(t, 0, ep.MaxHealAttempts)
			assert.Equal(t, tt.wantAudit, ep.AuditAfterSprint)
			assert.False(t, ep.ReviewBetweenSprints)
			assert.Equal(t, tt.wantSprints, len(ep.Sprints))
			assert.Equal(t, tt.wantSprints, ep.TotalSprints)

			if tt.wantAuditIterSet {
				assert.True(t, ep.MaxAuditIterationsSet)
				assert.Equal(t, tt.wantAuditIter, ep.MaxAuditIterations)
			}

			if tt.wantSprints > 0 {
				s := ep.Sprints[0]
				assert.Equal(t, 1, s.Number)
				assert.Equal(t, "Execute task", s.Name)
				assert.Equal(t, tt.wantMaxIter, s.MaxIterations)
				assert.Equal(t, "SIMPLE_TASK_COMPLETE", s.Promise)
				assert.Equal(t, tt.wantPrompt, s.Prompt)
			}

			require.NoError(t, epic.ValidateEpic(ep))
		})
	}
}

func TestBuildModerateEpic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		opts             ModerateEpicOpts
		wantErr          bool
		wantSprints      int
		wantEffort       epic.EffortLevel
		wantMaxIter      int
		wantHealAttempts int
		wantAudit        bool
	}{
		{
			name: "default effort (standard) 1 sprint",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Add REST endpoint",
				EngineName:  "claude",
				SprintCount: 1,
			},
			wantSprints:      1,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantHealAttempts: config.DefaultMaxHealAttempts,
			wantAudit:        true,
		},
		{
			name: "standard effort 2 sprints",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Build a small tool",
				EngineName:  "claude",
				EffortLevel: epic.EffortStandard,
				SprintCount: 2,
			},
			wantSprints:      2,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantHealAttempts: config.DefaultMaxHealAttempts,
			wantAudit:        true,
		},
		{
			name: "fast effort forces 1 sprint no audit no alignment",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Quick fix",
				EngineName:  "claude",
				EffortLevel: epic.EffortFast,
				SprintCount: 2, // should be clamped to 1
			},
			wantSprints:      1,
			wantEffort:       epic.EffortFast,
			wantMaxIter:      12,
			wantHealAttempts: 0,
			wantAudit:        false,
		},
		{
			name: "high effort 2 sprints",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Feature with thorough testing",
				EngineName:  "claude",
				EffortLevel: epic.EffortHigh,
				SprintCount: 2,
			},
			wantSprints:      2,
			wantEffort:       epic.EffortHigh,
			wantMaxIter:      25,
			wantHealAttempts: config.HealAttemptsHigh,
			wantAudit:        true,
		},
		{
			name: "max effort capped to high",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Important feature",
				EngineName:  "claude",
				EffortLevel: epic.EffortMax,
				SprintCount: 2,
			},
			wantSprints:      2,
			wantEffort:       epic.EffortHigh,
			wantMaxIter:      25,
			wantHealAttempts: config.HealAttemptsHigh,
			wantAudit:        true,
		},
		{
			name: "sprint count 0 defaults to 1",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Some task",
				EngineName:  "claude",
				EffortLevel: epic.EffortStandard,
				SprintCount: 0,
			},
			wantSprints:      1,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantHealAttempts: config.DefaultMaxHealAttempts,
			wantAudit:        true,
		},
		{
			name: "sprint count clamped to 2",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "Some task",
				EngineName:  "claude",
				EffortLevel: epic.EffortStandard,
				SprintCount: 5,
			},
			wantSprints:      2,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantHealAttempts: config.DefaultMaxHealAttempts,
			wantAudit:        true,
		},
		{
			name: "plan takes precedence",
			opts: ModerateEpicOpts{
				ProjectDir:  "/tmp/test",
				PlanContent: "The plan",
				UserPrompt:  "The user prompt",
				EngineName:  "claude",
				SprintCount: 1,
			},
			wantSprints:      1,
			wantEffort:       epic.EffortStandard,
			wantMaxIter:      20,
			wantHealAttempts: config.DefaultMaxHealAttempts,
			wantAudit:        true,
		},
		{
			name: "empty content returns error",
			opts: ModerateEpicOpts{
				ProjectDir: "/tmp/test",
				EngineName: "claude",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ep, err := BuildModerateEpic(tt.opts)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, ep)

			assert.Equal(t, "Moderate Task", ep.Name)
			assert.Equal(t, tt.opts.EngineName, ep.Engine)
			assert.Equal(t, tt.wantEffort, ep.EffortLevel)
			assert.Equal(t, tt.wantHealAttempts, ep.MaxHealAttempts)
			assert.Equal(t, tt.wantAudit, ep.AuditAfterSprint)
			assert.False(t, ep.ReviewBetweenSprints)
			require.Equal(t, tt.wantSprints, len(ep.Sprints))
			assert.Equal(t, tt.wantSprints, ep.TotalSprints)

			for i, s := range ep.Sprints {
				assert.Equal(t, i+1, s.Number)
				assert.Equal(t, tt.wantMaxIter, s.MaxIterations)
				assert.NotEmpty(t, s.Promise)
				assert.NotEmpty(t, s.Prompt)
				assert.NotEmpty(t, s.Name)
			}

			if tt.wantSprints == 2 {
				assert.Equal(t, "Implement core changes", ep.Sprints[0].Name)
				assert.Equal(t, "Polish, test, and finalize", ep.Sprints[1].Name)
			}

			require.NoError(t, epic.ValidateEpic(ep))
		})
	}
}

func TestGenerateVerificationChecks(t *testing.T) {
	t.Parallel()

	t.Run("go project", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Equal(t, 1, checks[0].Sprint)
		assert.Equal(t, verify.CheckCmd, checks[0].Type)
		assert.Contains(t, checks[0].Command, "go build")
		assert.Contains(t, checks[0].Command, "go test")
	})

	t.Run("node project", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Command, "npm")
	})

	t.Run("rust project", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Command, "cargo build")
	})

	t.Run("python project with pyproject.toml", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Command, "pytest")
	})

	t.Run("python project with setup.py", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "setup.py"), []byte("setup()"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Command, "pytest")
	})

	t.Run("makefile project", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 1)
		assert.Contains(t, checks[0].Command, "make")
	})

	t.Run("no recognized build system", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		checks := GenerateVerificationChecks(dir, 1)
		assert.Nil(t, checks)
	})

	t.Run("multi-sprint duplicates checks", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o644))

		checks := GenerateVerificationChecks(dir, 2)
		require.Len(t, checks, 2)
		assert.Equal(t, 1, checks[0].Sprint)
		assert.Equal(t, 2, checks[1].Sprint)
	})

	t.Run("multiple build systems detected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build:"), 0o644))

		checks := GenerateVerificationChecks(dir, 1)
		require.Len(t, checks, 2)
	})
}

func TestWriteVerificationFile(t *testing.T) {
	t.Parallel()

	t.Run("roundtrip through ParseVerification", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		verifyPath := filepath.Join(dir, ".fry", "verification.md")

		checks := []verify.Check{
			{Sprint: 1, Type: verify.CheckCmd, Command: "go build ./..."},
			{Sprint: 1, Type: verify.CheckCmd, Command: "go test ./..."},
			{Sprint: 2, Type: verify.CheckCmd, Command: "go build ./..."},
		}

		err := WriteVerificationFile(verifyPath, checks)
		require.NoError(t, err)

		parsed, parseErr := verify.ParseVerification(verifyPath)
		require.NoError(t, parseErr)
		require.Len(t, parsed, 3)

		assert.Equal(t, 1, parsed[0].Sprint)
		assert.Equal(t, "go build ./...", parsed[0].Command)
		assert.Equal(t, verify.CheckCmd, parsed[0].Type)

		assert.Equal(t, 1, parsed[1].Sprint)
		assert.Equal(t, "go test ./...", parsed[1].Command)

		assert.Equal(t, 2, parsed[2].Sprint)
		assert.Equal(t, "go build ./...", parsed[2].Command)
	})

	t.Run("empty checks skips file creation", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		verifyPath := filepath.Join(dir, ".fry", "verification.md")

		err := WriteVerificationFile(verifyPath, nil)
		require.NoError(t, err)

		_, statErr := os.Stat(verifyPath)
		assert.True(t, os.IsNotExist(statErr))
	})
}

func TestWriteEpicFile(t *testing.T) {
	t.Parallel()

	t.Run("roundtrip simple epic", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		epicPath := filepath.Join(dir, ".fry", "epic.md")

		original := &epic.Epic{
			Name:              "Test Epic",
			Engine:            "claude",
			EffortLevel:       epic.EffortFast,
			MaxHealAttempts:   0,
			AuditAfterSprint:  false,
			ReviewAfterSprint: false,
			TotalSprints:      1,
			Sprints: []epic.Sprint{{
				Number:        1,
				Name:          "Do the thing",
				MaxIterations: 12,
				Promise:       "DONE",
				Prompt:        "Build a widget that does stuff.\nIt should have tests.",
			}},
		}

		err := WriteEpicFile(epicPath, original)
		require.NoError(t, err)

		_, statErr := os.Stat(epicPath)
		require.NoError(t, statErr)

		parsed, parseErr := epic.ParseEpic(epicPath)
		require.NoError(t, parseErr)
		require.NoError(t, epic.ValidateEpic(parsed))

		assert.Equal(t, original.Name, parsed.Name)
		assert.Equal(t, original.Engine, parsed.Engine)
		assert.Equal(t, original.EffortLevel, parsed.EffortLevel)
		assert.False(t, parsed.ReviewAfterSprint)
		require.Len(t, parsed.Sprints, 1)

		s := parsed.Sprints[0]
		assert.Equal(t, 1, s.Number)
		assert.Equal(t, "Do the thing", s.Name)
		assert.Equal(t, 12, s.MaxIterations)
		assert.Equal(t, "DONE", s.Promise)
		assert.Contains(t, s.Prompt, "Build a widget that does stuff.")
		assert.Contains(t, s.Prompt, "It should have tests.")
	})

	t.Run("roundtrip multi-sprint epic", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		epicPath := filepath.Join(dir, ".fry", "epic.md")

		original := &epic.Epic{
			Name:              "Multi Sprint",
			Engine:            "codex",
			EffortLevel:       epic.EffortStandard,
			MaxHealAttempts:   3,
			AuditAfterSprint:  true,
			ReviewAfterSprint: true,
			TotalSprints:      2,
			Sprints: []epic.Sprint{
				{
					Number:        1,
					Name:          "Sprint one",
					MaxIterations: 20,
					Promise:       "SPRINT1_DONE",
					Prompt:        "First sprint instructions.",
				},
				{
					Number:        2,
					Name:          "Sprint two",
					MaxIterations: 20,
					Promise:       "SPRINT2_DONE",
					Prompt:        "Second sprint instructions.",
				},
			},
		}

		err := WriteEpicFile(epicPath, original)
		require.NoError(t, err)

		parsed, parseErr := epic.ParseEpic(epicPath)
		require.NoError(t, parseErr)
		require.NoError(t, epic.ValidateEpic(parsed))

		assert.Equal(t, "Multi Sprint", parsed.Name)
		assert.Equal(t, "codex", parsed.Engine)
		assert.Equal(t, epic.EffortStandard, parsed.EffortLevel)
		require.Len(t, parsed.Sprints, 2)
		assert.Equal(t, "Sprint one", parsed.Sprints[0].Name)
		assert.Equal(t, "Sprint two", parsed.Sprints[1].Name)
	})

	t.Run("roundtrip with max_review_iterations", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		epicPath := filepath.Join(dir, ".fry", "epic.md")

		original := &epic.Epic{
			Name:                   "Reviewed Task",
			Engine:                 "claude",
			EffortLevel:            epic.EffortStandard,
			MaxHealAttempts:        0,
			ReviewAfterSprint:      true,
			MaxReviewIterations:    1,
			MaxReviewIterationsSet: true,
			TotalSprints:           1,
			Sprints: []epic.Sprint{{
				Number:        1,
				Name:          "Do work",
				MaxIterations: 20,
				Promise:       "DONE",
				Prompt:        "Do the work.",
			}},
		}

		err := WriteEpicFile(epicPath, original)
		require.NoError(t, err)

		// Verify the file contains @max_review_iterations.
		content, readErr := os.ReadFile(epicPath)
		require.NoError(t, readErr)
		assert.Contains(t, string(content), "@max_review_iterations 1")

		parsed, parseErr := epic.ParseEpic(epicPath)
		require.NoError(t, parseErr)
		assert.True(t, parsed.MaxReviewIterationsSet)
		assert.Equal(t, 1, parsed.MaxReviewIterations)
		assert.True(t, parsed.ReviewAfterSprint)
	})
}
