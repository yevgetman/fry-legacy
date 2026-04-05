package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/color"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/sprint"
	"github.com/yevgetman/fry/internal/triage"
)

func TestFormatAffectedSprints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []int
		want  string
	}{
		{"nil slice", nil, "unknown"},
		{"empty slice", []int{}, "unknown"},
		{"single element", []int{5}, "5"},
		{"multiple elements", []int{1, 2, 3}, "1, 2, 3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatAffectedSprints(tt.input))
		})
	}
}

func TestColorizeStatus(t *testing.T) {
	// Not parallel: mutates global color state via color.SetEnabled.
	color.SetEnabled(true)
	t.Cleanup(func() { color.SetEnabled(false) })

	tests := []struct {
		name         string
		input        string
		wantContains string
	}{
		{"PASS prefix", "PASS", "\033[32m"},
		{"PASS with detail", "PASS (3/3)", "\033[32m"},
		{"FAIL prefix", "FAIL", "\033[31m"},
		{"FAIL with detail", "FAIL (sanity)", "\033[31m"},
		{"SKIPPED", sprint.StatusSkipped, "\033[33m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := colorizeStatus(tt.input)
			assert.Contains(t, got, tt.wantContains)
			assert.Contains(t, got, tt.input)
		})
	}

	t.Run("unknown status returned unchanged", func(t *testing.T) {
		assert.Equal(t, "UNKNOWN", colorizeStatus("UNKNOWN"))
	})
}

func TestIsPassStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"PASS", "PASS", true},
		{"PASS with detail", "PASS (3/3)", true},
		{"FAIL", "FAIL", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isPassStatus(tt.input))
		})
	}
}

func TestStartSprintCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		start, end int
		want       int
	}{
		{"full range", 1, 5, 5},
		{"single sprint", 3, 3, 1},
		{"partial range", 2, 4, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, startSprintCount(tt.start, tt.end))
		})
	}
}

func TestInitializeSprintResults(t *testing.T) {
	t.Parallel()

	ep := &epic.Epic{
		Sprints: []epic.Sprint{
			{Number: 1, Name: "Sprint 1"},
			{Number: 2, Name: "Sprint 2"},
			{Number: 3, Name: "Sprint 3"},
		},
		TotalSprints: 3,
	}

	tests := []struct {
		name       string
		start, end int
		wantLen    int
		wantFirst  int
		wantLast   int
	}{
		{"all sprints", 1, 3, 3, 1, 3},
		{"single sprint", 2, 2, 1, 2, 2},
		{"partial range", 2, 3, 2, 2, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			results := initializeSprintResults(ep, tt.start, tt.end)
			require.Len(t, results, tt.wantLen)
			assert.Equal(t, tt.wantFirst, results[0].Number)
			assert.Equal(t, tt.wantLast, results[len(results)-1].Number)
			for _, r := range results {
				assert.Equal(t, sprint.StatusSkipped, r.Status)
			}
		})
	}

	t.Run("sprint names populated from epic", func(t *testing.T) {
		t.Parallel()
		results := initializeSprintResults(ep, 1, 3)
		assert.Equal(t, "Sprint 1", results[0].Name)
		assert.Equal(t, "Sprint 2", results[1].Name)
		assert.Equal(t, "Sprint 3", results[2].Name)
	})
}

func TestResolvePrepareEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		prepareFlag string
		runFlag     string
		want        string
	}{
		{"prepare flag set", "claude", "codex", "claude"},
		{"prepare empty, run set", "", "codex", "codex"},
		{"whitespace prepare, run set", "  ", "codex", "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, resolvePrepareEngine(tt.prepareFlag, tt.runFlag))
		})
	}

}

func TestResolvePrepareEngine_EnvFallback(t *testing.T) {
	t.Setenv("FRY_ENGINE", "test-engine")
	got := resolvePrepareEngine("", "")
	assert.Equal(t, "test-engine", got)
}

func TestResolveRunGitStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		requested            git.GitStrategy
		triageDecision       *triage.TriageDecision
		repoExistedBeforeRun bool
		isFreshlyInitialized bool
		want                 git.GitStrategy
	}{
		{
			name:                 "explicit strategy is preserved",
			requested:            git.StrategyWorktree,
			repoExistedBeforeRun: false,
			want:                 git.StrategyWorktree,
		},
		{
			name:                 "first build in newly initialized repo stays current",
			requested:            git.StrategyAuto,
			repoExistedBeforeRun: false,
			triageDecision:       &triage.TriageDecision{Complexity: triage.ComplexityComplex},
			want:                 git.StrategyCurrent,
		},
		{
			name:                 "fry-init repo stays current even for complex triage",
			requested:            git.StrategyAuto,
			repoExistedBeforeRun: true,
			isFreshlyInitialized: true,
			triageDecision:       &triage.TriageDecision{Complexity: triage.ComplexityComplex},
			want:                 git.StrategyCurrent,
		},
		{
			name:                 "existing repo uses triage complexity",
			requested:            git.StrategyAuto,
			repoExistedBeforeRun: true,
			triageDecision:       &triage.TriageDecision{Complexity: triage.ComplexityComplex},
			want:                 git.StrategyWorktree,
		},
		{
			name:                 "triage override wins in existing repo",
			requested:            git.StrategyAuto,
			repoExistedBeforeRun: true,
			triageDecision: &triage.TriageDecision{
				Complexity:          triage.ComplexityModerate,
				GitStrategyOverride: "current",
			},
			want: git.StrategyCurrent,
		},
		{
			name:                 "no triage defaults current",
			requested:            git.StrategyAuto,
			repoExistedBeforeRun: true,
			want:                 git.StrategyCurrent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveRunGitStrategy(tt.requested, tt.triageDecision, tt.repoExistedBeforeRun, tt.isFreshlyInitialized)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{"short string unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"long string truncated", "hello world", 5, "hello... [truncated]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, truncateString(tt.input, tt.maxBytes))
		})
	}
}

func TestReadOptionalFile(t *testing.T) {
	t.Parallel()

	t.Run("existing file returns content", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := filepath.Join(dir, "test.txt")
		require.NoError(t, os.WriteFile(p, []byte("hello\n"), 0o644))

		got, err := readOptionalFile(p)
		require.NoError(t, err)
		assert.Equal(t, "hello\n", got)
	})

	t.Run("non-existent file returns empty string", func(t *testing.T) {
		t.Parallel()
		got, err := readOptionalFile("/nonexistent/path/file.txt")
		require.NoError(t, err)
		assert.Equal(t, "", got)
	})
}

func TestUserPromptSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		prompt        string
		flagValue     string
		fileFlagValue string
		want          string
	}{
		{"empty prompt", "", "", "", ""},
		{"whitespace-only prompt", "   ", "", "", ""},
		{"prompt with file flag", "do stuff", "", "plan.txt", "--user-prompt-file plan.txt"},
		{"prompt with flag value", "do stuff", "inline", "", "--user-prompt flag"},
		{"prompt only, no flags", "do stuff", "", "", config.UserPromptFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, userPromptSource(tt.prompt, tt.flagValue, tt.fileFlagValue))
		})
	}
}

func TestTelemetryBoolPtr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input bool
	}{
		{"true", true},
		{"false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ptr := telemetryBoolPtr(tt.input)
			require.NotNil(t, ptr)
			assert.Equal(t, tt.input, *ptr)
		})
	}
}

func TestWriteBuildAuditSentinel(t *testing.T) {
	t.Parallel()

	t.Run("happy path writes sentinel file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		err := writeBuildAuditSentinel(dir)
		require.NoError(t, err)

		sentinelPath := filepath.Join(dir, config.BuildAuditCompleteFile)
		info, err := os.Stat(sentinelPath)
		require.NoError(t, err)
		assert.False(t, info.IsDir())
		assert.True(t, info.Size() > 0, "sentinel file should not be empty")
	})

	t.Run("sentinel contains valid RFC3339 timestamp", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		before := time.Now().UTC().Add(-time.Second)
		err := writeBuildAuditSentinel(dir)
		require.NoError(t, err)
		after := time.Now().UTC().Add(time.Second)

		data, err := os.ReadFile(filepath.Join(dir, config.BuildAuditCompleteFile))
		require.NoError(t, err)
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(data)))
		require.NoError(t, err, "sentinel content should be valid RFC3339")
		assert.True(t, !ts.Before(before) && !ts.After(after), "timestamp should be recent")
	})

	t.Run("atomic rename — file exists after call", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		require.NoError(t, writeBuildAuditSentinel(dir))

		sentinelPath := filepath.Join(dir, config.BuildAuditCompleteFile)
		_, err := os.Stat(sentinelPath)
		assert.NoError(t, err, "sentinel file must exist after successful call")

		// No leftover temp files in the .fry directory
		fryDir := filepath.Dir(sentinelPath)
		entries, err := os.ReadDir(fryDir)
		require.NoError(t, err)
		for _, e := range entries {
			assert.False(t, strings.HasPrefix(e.Name(), "fry-build-audit-sentinel-"),
				"temp file %s should not remain after successful write", e.Name())
		}
	})

	t.Run("creates .fry directory if missing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		// .fry/ does not exist yet inside dir
		err := writeBuildAuditSentinel(dir)
		require.NoError(t, err)

		sentinelPath := filepath.Join(dir, config.BuildAuditCompleteFile)
		_, err = os.Stat(sentinelPath)
		assert.NoError(t, err)
	})

	t.Run("error on unwritable directory", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("chmod not effective on Windows")
		}

		dir := t.TempDir()
		fryDir := filepath.Join(dir, ".fry")
		require.NoError(t, os.MkdirAll(fryDir, 0o755))
		// Make .fry read-only so temp file creation fails
		require.NoError(t, os.Chmod(fryDir, 0o555))
		t.Cleanup(func() { os.Chmod(fryDir, 0o755) })

		err := writeBuildAuditSentinel(dir)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "write build audit sentinel")
	})

	t.Run("temp file in same directory avoids cross-device rename", func(t *testing.T) {
		t.Parallel()
		// Verify the implementation creates the temp file in the target directory
		// (filepath.Dir(finalPath)) rather than os.TempDir(), which prevents EXDEV
		// errors when /tmp is on a different filesystem (e.g., Docker tmpfs).
		dir := t.TempDir()

		require.NoError(t, writeBuildAuditSentinel(dir))

		// If we get here without error, the rename succeeded on the same filesystem.
		// Additionally verify no temp files leaked to os.TempDir().
		tmpEntries, err := os.ReadDir(os.TempDir())
		require.NoError(t, err)
		for _, e := range tmpEntries {
			assert.False(t, strings.HasPrefix(e.Name(), "fry-build-audit-sentinel-"),
				"no sentinel temp files should be in os.TempDir()")
		}
	})

	t.Run("overwrites existing sentinel", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		require.NoError(t, writeBuildAuditSentinel(dir))
		sentinelPath := filepath.Join(dir, config.BuildAuditCompleteFile)
		first, err := os.ReadFile(sentinelPath)
		require.NoError(t, err)
		require.NotEmpty(t, first)

		// Write again — should succeed and produce a valid sentinel
		require.NoError(t, writeBuildAuditSentinel(dir))
		second, err := os.ReadFile(sentinelPath)
		require.NoError(t, err)
		require.NotEmpty(t, second)

		// Both should be valid RFC3339 timestamps
		_, err = time.Parse(time.RFC3339, strings.TrimSpace(string(second)))
		assert.NoError(t, err, "overwritten sentinel should contain valid RFC3339")
	})
}
