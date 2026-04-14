package epic

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yevgetman/fry/internal/config"
)

func TestParseBasicEpic(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Basic Epic
@sprint 1
@name Setup
@max_iterations 2
@promise BASIC_DONE
@prompt
Ship the first slice.
`)

	assert.Equal(t, "Basic Epic", ep.Name)
	assert.Equal(t, config.DefaultVerificationFile, ep.VerificationFile)
	assert.Equal(t, config.DefaultMaxHealAttempts, ep.MaxHealAttempts)
	assert.False(t, ep.MaxHealAttemptsSet)
	assert.Equal(t, config.DefaultMaxFailPercent, ep.MaxFailPercent)
	assert.False(t, ep.MaxFailPercentSet)
	assert.Equal(t, config.DefaultDockerReadyTimeout, ep.DockerReadyTimeout)
	assert.Equal(t, config.DefaultMaxDeviationScope, ep.MaxDeviationScope)
	require.Len(t, ep.Sprints, 1)
	assert.Equal(t, 1, ep.Sprints[0].Number)
	assert.Equal(t, "Setup", ep.Sprints[0].Name)
	assert.Equal(t, 2, ep.Sprints[0].MaxIterations)
	assert.Equal(t, "BASIC_DONE", ep.Sprints[0].Promise)
	assert.Equal(t, "Ship the first slice.", ep.Sprints[0].Prompt)
	assert.Equal(t, 1, ep.TotalSprints)
}

func TestParseMultiSprintEpic(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Multi
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt one.
@sprint 2
@name Two
@max_iterations 2
@promise TWO
@prompt
Prompt two.
With another line.
@sprint 3
@name Three
@max_iterations 3
@promise THREE
@prompt
Prompt three.
`)

	require.Len(t, ep.Sprints, 3)
	assert.Equal(t, []int{1, 2, 3}, []int{ep.Sprints[0].Number, ep.Sprints[1].Number, ep.Sprints[2].Number})
	assert.Equal(t, "Prompt one.", ep.Sprints[0].Prompt)
	assert.Equal(t, "Prompt two.\nWith another line.", ep.Sprints[1].Prompt)
	assert.Equal(t, "Prompt three.", ep.Sprints[2].Prompt)
}

func TestParseAllGlobalDirectives(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Full Epic
@engine claude
@docker_from_sprint 2
@docker_ready_cmd docker compose ps
@docker_ready_timeout 45
@require_tool go
@require_tool git
@preflight_cmd test -f go.mod
@preflight_cmd go test ./...
@pre_sprint ./scripts/pre-sprint.sh
@pre_iteration ./scripts/pre-iteration.sh
@model sonnet
@engine_flags --json --danger
@verification custom-verification.md
@max_heal_attempts 7
@compact_with_agent
@review_between_sprints
@review_engine claude
@review_model reviewer-x
@max_deviation_scope 5
@sprint 1
@name One
@max_iterations 2
@promise OK
@prompt
Do it.
`)

	assert.Equal(t, "Full Epic", ep.Name)
	assert.Equal(t, "claude", ep.Engine)
	assert.Equal(t, 2, ep.DockerFromSprint)
	assert.Equal(t, "docker compose ps", ep.DockerReadyCmd)
	assert.Equal(t, 45, ep.DockerReadyTimeout)
	assert.Equal(t, []string{"go", "git"}, ep.RequiredTools)
	assert.Equal(t, []string{"test -f go.mod", "go test ./..."}, ep.PreflightCmds)
	assert.Equal(t, "./scripts/pre-sprint.sh", ep.PreSprintCmd)
	assert.Equal(t, "./scripts/pre-iteration.sh", ep.PreIterationCmd)
	assert.Equal(t, "sonnet", ep.AgentModel)
	assert.Equal(t, "--json --danger", ep.AgentFlags)
	assert.Equal(t, "custom-verification.md", ep.VerificationFile)
	assert.Equal(t, 7, ep.MaxHealAttempts)
	assert.True(t, ep.MaxHealAttemptsSet)
	assert.True(t, ep.CompactWithAgent)
	assert.True(t, ep.ReviewBetweenSprints)
	assert.Equal(t, "claude", ep.ReviewEngine)
	assert.Equal(t, "reviewer-x", ep.ReviewModel)
	assert.Equal(t, 5, ep.MaxDeviationScope)
}

func TestParsePerSprintHealAttempts(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Heal
@max_heal_attempts 3
@sprint 1
@name One
@max_iterations 1
@promise ONE
@max_heal_attempts 9
@prompt
Prompt.
`)

	require.NotNil(t, ep.Sprints[0].MaxHealAttempts)
	assert.Equal(t, 9, *ep.Sprints[0].MaxHealAttempts)
	assert.Equal(t, 3, ep.MaxHealAttempts)
}

func TestParseMaxHealAttemptsIgnoredForMaxEffort(t *testing.T) {
	t.Parallel()

	input := `
@epic Max Heal
@effort max
@max_heal_attempts 5
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
`
	var ep *Epic
	stderr := captureStderr(t, func() {
		ep = parseTempEpic(t, input)
	})

	// Max effort ignores explicit @max_heal_attempts — resets to 0 (unlimited).
	assert.Equal(t, 0, ep.MaxHealAttempts)
	assert.False(t, ep.MaxHealAttemptsSet)
	assert.Contains(t, stderr, "@max_heal_attempts ignored for max effort")
}

func TestParseDeviationScopeExpandsToTotalSprints(t *testing.T) {
	t.Parallel()

	fourSprints := `
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt one.

@sprint 2
@name Two
@max_iterations 1
@promise TWO
@prompt
Prompt two.

@sprint 3
@name Three
@max_iterations 1
@promise THREE
@prompt
Prompt three.

@sprint 4
@name Four
@max_iterations 1
@promise FOUR
@prompt
Prompt four.
`

	t.Run("max effort expands scope to all sprints", func(t *testing.T) {
		t.Parallel()
		var ep *Epic
		captureStderr(t, func() {
			ep = parseTempEpic(t, "@epic Max\n@effort max\n@max_deviation_scope 3\n"+fourSprints)
		})
		assert.Equal(t, 4, ep.TotalSprints)
		assert.Equal(t, 4, ep.MaxDeviationScope)
	})

	t.Run("high effort expands scope to all sprints", func(t *testing.T) {
		t.Parallel()
		ep := parseTempEpic(t, "@epic High\n@effort high\n"+fourSprints)
		assert.Equal(t, 4, ep.TotalSprints)
		assert.Equal(t, 4, ep.MaxDeviationScope)
	})

	t.Run("standard effort expands scope to all sprints", func(t *testing.T) {
		t.Parallel()
		ep := parseTempEpic(t, "@epic Med\n@effort standard\n"+fourSprints)
		assert.Equal(t, 4, ep.TotalSprints)
		assert.Equal(t, 4, ep.MaxDeviationScope)
	})

	t.Run("auto-detect effort expands scope", func(t *testing.T) {
		t.Parallel()
		ep := parseTempEpic(t, "@epic Auto\n"+fourSprints)
		assert.Equal(t, 4, ep.TotalSprints)
		assert.Equal(t, 4, ep.MaxDeviationScope)
	})

	t.Run("fast effort keeps default scope", func(t *testing.T) {
		t.Parallel()
		ep := parseTempEpic(t, "@epic Low\n@effort fast\n"+fourSprints)
		assert.Equal(t, 4, ep.TotalSprints)
		assert.Equal(t, config.DefaultMaxDeviationScope, ep.MaxDeviationScope)
	})

	t.Run("capped at safety limit for large epics", func(t *testing.T) {
		t.Parallel()
		// Build a 12-sprint epic — scope should cap at MaxDeviationScopeCap (10).
		var big string
		for i := 1; i <= 12; i++ {
			big += fmt.Sprintf("@sprint %d\n@name S%d\n@max_iterations 1\n@promise P%d\n@prompt\nDo sprint %d.\n\n", i, i, i, i)
		}
		ep := parseTempEpic(t, "@epic Big\n@effort high\n"+big)
		assert.Equal(t, 12, ep.TotalSprints)
		assert.Equal(t, config.MaxDeviationScopeCap, ep.MaxDeviationScope)
	})
}

func TestParsePromptBleedStripping(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Bleed
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Keep this.

# =====
## ====

`)

	assert.Equal(t, "Keep this.", ep.Sprints[0].Prompt)
}

func TestParseUnknownDirectiveWarning(t *testing.T) {
	t.Parallel()

	output := captureStderr(t, func() {
		parseTempEpic(t, `
@epic Warn
@mystery value
@sprint 1
@name One
@bogus nope
@max_iterations 1
@promise ONE
@prompt
Prompt.
`)
	})

	assert.Contains(t, output, "fry: warning: unrecognized directive: @mystery value")
	assert.Contains(t, output, "fry: warning: unrecognized directive: @bogus nope")
}

func TestParseBooleanFlags(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Flags
@compact_with_agent
@review_between_sprints
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
`)

	assert.True(t, ep.CompactWithAgent)
	assert.True(t, ep.ReviewBetweenSprints)
}

func TestParseModelAliases(t *testing.T) {
	t.Parallel()

	var ep *Epic
	captureStderr(t, func() {
		ep = parseTempEpic(t, `
@epic Aliases
@codex_model gpt-5
@codex_flags --profile fast
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
`)
	})

	assert.Equal(t, "gpt-5", ep.AgentModel)
	assert.Equal(t, "--profile fast", ep.AgentFlags)
}

func TestParseEndDirective(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic End
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
@end
@review_engine claude
`)

	require.Len(t, ep.Sprints, 1)
	assert.Equal(t, "claude", ep.ReviewEngine)
}

func TestValidateEpic(t *testing.T) {
	t.Parallel()

	valid := &Epic{
		Sprints: []Sprint{{
			Number:        1,
			Name:          "One",
			MaxIterations: 1,
			Promise:       "ONE",
			Prompt:        "Prompt.",
		}},
	}
	assert.NoError(t, ValidateEpic(valid))

	err := ValidateEpic(&Epic{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one sprint")

	err = ValidateEpic(&Epic{Sprints: []Sprint{
		{Number: 1, Name: "One", MaxIterations: 1, Promise: "ONE", Prompt: "Prompt."},
		{Number: 3, Name: "Three", MaxIterations: 1, Promise: "THREE", Prompt: "Prompt."},
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected sprint 2, got 3")

	err = ValidateEpic(&Epic{Sprints: []Sprint{{Number: 1, MaxIterations: 1, Promise: "ONE", Prompt: "Prompt."}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing @name")

	err = ValidateEpic(&Epic{Sprints: []Sprint{{Number: 1, Name: "One", MaxIterations: 1, Promise: "ONE"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing @prompt")
}

func TestParseEpicBadSprintNumber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "epic.md")
	require.NoError(t, os.WriteFile(path, []byte("@epic Bad\n@sprint abc\n"), 0o600))

	_, err := ParseEpic(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires an integer")
}

func TestParseEpicBadMaxIterations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "epic.md")
	content := "@epic Bad\n@sprint 1\n@name One\n@max_iterations xyz\n@promise ONE\n@prompt\nDo it.\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	_, err := ParseEpic(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires an integer")
}

func TestParseEpicFileNotFound(t *testing.T) {
	t.Parallel()

	_, err := ParseEpic("/nonexistent/path/epic.md")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open epic file")
}

func TestParseEpic_EffortDirective(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Effort Test
@effort standard
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, EffortStandard, ep.EffortLevel)
}

func TestParseEpic_EffortDirectiveInvalid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "epic.md")
	require.NoError(t, os.WriteFile(path, []byte("@epic Bad\n@effort extreme\n@sprint 1\n@name One\n@max_iterations 1\n@promise ONE\n@prompt\nDo it.\n"), 0o600))

	_, err := ParseEpic(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid effort level")
}

func TestParseEpic_EffortDirectiveMissing(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic No Effort
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, EffortLevel(""), ep.EffortLevel)
}

func TestParseEpic_ReviewDirectives(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Review Test
@review_after_sprint
@max_review_iterations 5
@review_engine claude
@review_model reviewer-v1
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.True(t, ep.ReviewAfterSprint)
	assert.Equal(t, 5, ep.MaxReviewIterations)
	assert.True(t, ep.MaxReviewIterationsSet)
	assert.Equal(t, "claude", ep.ReviewEngine)
	assert.Equal(t, "reviewer-v1", ep.ReviewModel)
	// Old fields stay in sync during transition
	assert.True(t, ep.AuditAfterSprint)
	assert.Equal(t, 5, ep.MaxAuditIterations)
}

func TestParseEpic_ReviewDefaultIterations(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Review Default
@review_after_sprint
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.True(t, ep.ReviewAfterSprint)
	assert.Equal(t, config.DefaultMaxAuditIterations, ep.MaxReviewIterations)
	assert.False(t, ep.MaxReviewIterationsSet)
}

func TestParseEpic_ReviewDefaultEnabled(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic No Review Directive
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.True(t, ep.ReviewAfterSprint)
	assert.Equal(t, config.DefaultMaxAuditIterations, ep.MaxReviewIterations)
	assert.False(t, ep.MaxReviewIterationsSet)
	assert.True(t, ep.AuditAfterSprint)
}

func TestParseEpic_NoReviewDirective(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic No Review
@no_review
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.False(t, ep.ReviewAfterSprint)
	assert.Equal(t, 0, ep.MaxReviewIterations)
	assert.False(t, ep.AuditAfterSprint)
}

func TestParseEpic_EffortDirectiveCaseInsensitive(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Case Test
@effort FAST
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, EffortFast, ep.EffortLevel)
}

func TestValidateEpic_EffortFast_TooManySprints(t *testing.T) {
	t.Parallel()

	ep := &Epic{
		EffortLevel: EffortFast,
		Sprints: []Sprint{
			{Number: 1, Name: "One", MaxIterations: 1, Promise: "ONE", Prompt: "Prompt."},
			{Number: 2, Name: "Two", MaxIterations: 1, Promise: "TWO", Prompt: "Prompt."},
			{Number: 3, Name: "Three", MaxIterations: 1, Promise: "THREE", Prompt: "Prompt."},
		},
	}
	err := ValidateEpic(ep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "effort level \"fast\" allows at most 2 sprints, but epic has 3")
}

func TestValidateEpic_EffortFast_Valid(t *testing.T) {
	t.Parallel()

	ep := &Epic{
		EffortLevel: EffortFast,
		Sprints: []Sprint{
			{Number: 1, Name: "One", MaxIterations: 1, Promise: "ONE", Prompt: "Prompt."},
			{Number: 2, Name: "Two", MaxIterations: 1, Promise: "TWO", Prompt: "Prompt."},
		},
	}
	assert.NoError(t, ValidateEpic(ep))
}

func TestValidateEpic_EffortStandard_TooManySprints(t *testing.T) {
	t.Parallel()

	sprints := make([]Sprint, 5)
	for i := range sprints {
		sprints[i] = Sprint{Number: i + 1, Name: fmt.Sprintf("Sprint %d", i+1), MaxIterations: 1, Promise: fmt.Sprintf("S%d", i+1), Prompt: "Prompt."}
	}
	ep := &Epic{
		EffortLevel: EffortStandard,
		Sprints:     sprints,
	}
	err := ValidateEpic(ep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "effort level \"standard\" allows at most 4 sprints, but epic has 5")
}

func TestValidateEpic_EffortUnset_AnySprints(t *testing.T) {
	t.Parallel()

	sprints := make([]Sprint, 10)
	for i := range sprints {
		sprints[i] = Sprint{Number: i + 1, Name: fmt.Sprintf("Sprint %d", i+1), MaxIterations: 1, Promise: fmt.Sprintf("S%d", i+1), Prompt: "Prompt."}
	}
	ep := &Epic{
		EffortLevel: "",
		Sprints:     sprints,
	}
	assert.NoError(t, ValidateEpic(ep))
}

func TestParseEpic_MaxFailPercent(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Fail Percent
@max_fail_percent 30
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, 30, ep.MaxFailPercent)
	assert.True(t, ep.MaxFailPercentSet)
}

func TestParseEpic_MaxFailPercentDefault(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic No Percent
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, config.DefaultMaxFailPercent, ep.MaxFailPercent)
	assert.False(t, ep.MaxFailPercentSet)
}

func TestParseEpic_MaxFailPercentZero(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Strict
@max_fail_percent 0
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, 0, ep.MaxFailPercent)
	assert.True(t, ep.MaxFailPercentSet)
}

func TestParseEpic_MaxFailPercentHundred(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Lenient
@max_fail_percent 100
@sprint 1
@name One
@max_iterations 2
@promise ONE
@prompt
Do it.
`)

	assert.Equal(t, 100, ep.MaxFailPercent)
}

func TestValidateEpic_MaxFailPercentOutOfRange(t *testing.T) {
	t.Parallel()

	ep := &Epic{
		MaxFailPercent: 101,
		Sprints: []Sprint{
			{Number: 1, Name: "One", MaxIterations: 1, Promise: "ONE", Prompt: "Prompt."},
		},
	}
	err := ValidateEpic(ep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@max_fail_percent must be between 0 and 100")
}

func TestParseEpicDeprecatedCodexModelWarning(t *testing.T) {
	t.Parallel()

	var ep *Epic
	output := captureStderr(t, func() {
		ep = parseTempEpic(t, `
@epic Deprecated Model
@codex_model gpt-5
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
`)
	})

	assert.Contains(t, output, "fry: warning: @codex_model is deprecated; use @model instead")
	assert.Equal(t, "gpt-5", ep.AgentModel)
}

func TestParseEpicDeprecatedCodexFlagsWarning(t *testing.T) {
	t.Parallel()

	var ep *Epic
	output := captureStderr(t, func() {
		ep = parseTempEpic(t, `
@epic Deprecated Flags
@codex_flags --profile fast
@sprint 1
@name One
@max_iterations 1
@promise ONE
@prompt
Prompt.
`)
	})

	assert.Contains(t, output, "fry: warning: @codex_flags is deprecated; use @engine_flags instead")
	assert.Equal(t, "--profile fast", ep.AgentFlags)
}

func TestParseMCPConfig(t *testing.T) {
	t.Parallel()

	ep := parseTempEpic(t, `
@epic Test
@mcp_config /path/to/mcp.json

@sprint 1
@name One
@max_iterations 1
@promise DONE
@prompt
Do stuff.
`)

	assert.Equal(t, "/path/to/mcp.json", ep.MCPConfig)
}

func parseTempEpic(t *testing.T, contents string) *Epic {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "epic.md")
	err := os.WriteFile(path, []byte(strings.TrimLeft(contents, "\n")), 0o600)
	require.NoError(t, err)

	ep, err := ParseEpic(path)
	require.NoError(t, err)
	return ep
}

// stderrMu serializes tests that redirect os.Stderr to avoid data races
// when parallel tests concurrently modify the global os.Stderr pointer.
var stderrMu sync.Mutex

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	stderrMu.Lock()
	defer stderrMu.Unlock()

	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	fn()

	require.NoError(t, w.Close())
	os.Stderr = old

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(data)
}
