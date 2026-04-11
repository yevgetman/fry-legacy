package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/config"
)

func defaultBootstrapData() BootstrapData {
	return BootstrapData{
		BuildDir:        "/tmp/test-build",
		FrySourceDir:    "/tmp/test-fry",
		Engine:          "claude",
		EpicName:        "Test Epic",
		EffortLevel:     "max",
		TotalSprints:    7,
		StartedAt:       "2026-04-07T00:00:00Z",
		Interval:        "10m",
		IntervalMinutes: 10,
		SessionID:       "4a8b1c7e-8f3a-4d2b-9e1a-7c5b3f2a8d1c",
		NowISO:          "2026-04-07T00:00:00Z",
		RunID:           "run-test",
	}
}

func TestRenderBootstrapPromptIncludesIdentity(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "Copilot — The Build's Wingman")
	assert.Contains(t, prompt, "FRY-SOURCE AUTHORITY")
	assert.Contains(t, prompt, "BUILD-ARTIFACT AUTHORITY")
}

func TestRenderBootstrapPromptIncludesAuthority(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "AUTONOMOUS NON-INTERACTIVE MODE")
	assert.Contains(t, prompt, "Pre-authorized actions")
	assert.Contains(t, prompt, "Do NOT ask")
}

func TestRenderBootstrapPromptSubstitutesFields(t *testing.T) {
	t.Parallel()

	data := defaultBootstrapData()
	prompt, err := RenderBootstrapPrompt(data)
	require.NoError(t, err)

	assert.Contains(t, prompt, data.BuildDir)
	assert.Contains(t, prompt, data.FrySourceDir)
	assert.Contains(t, prompt, data.Engine)
	assert.Contains(t, prompt, data.EpicName)
	assert.Contains(t, prompt, data.EffortLevel)
	assert.Contains(t, prompt, data.SessionID)
	assert.Contains(t, prompt, data.Interval)
}

func TestRenderBootstrapPromptIncludesTickChecklist(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "Tick Checklist")
	assert.Contains(t, prompt, "IS THE BUILD MAKING PROGRESS")
	assert.Contains(t, prompt, "IS THERE A REPEATING PATTERN")
	assert.Contains(t, prompt, "IS THE BUILD STUCK")
}

func TestRenderBootstrapPromptIncludesOrphanCheck(t *testing.T) {
	t.Parallel()

	data := defaultBootstrapData()
	prompt, err := RenderBootstrapPrompt(data)
	require.NoError(t, err)

	// Step 0 of the tick checklist must instruct the agent to detect
	// orphan status (manifest absent OR session_id mismatch).
	assert.Contains(t, prompt, "ORPHAN CHECK")
	assert.Contains(t, prompt, "manifest.json does not exist")
	assert.Contains(t, prompt, "session_id field")
	assert.Contains(t, prompt, "Orphaned")
	// The session ID itself must be substituted into the prompt so the
	// agent can compare against the live manifest.
	assert.Contains(t, prompt, data.SessionID)
}

func TestRenderBootstrapPromptInstructsAgentToInstallCron(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)

	// The copilot is fully independent — it installs its own cron via
	// CronCreate so it survives fry crashes.
	assert.Contains(t, prompt, "CronCreate",
		"prompt must instruct the agent to install its own cron")
	assert.Contains(t, prompt, "cron.id",
		"prompt must instruct the agent to persist the cron ID")
	assert.Contains(t, prompt, "CronDelete",
		"prompt must instruct the agent to delete its cron on stop/orphan")
	assert.Contains(t, prompt, "scheduler=self",
		"the bootstrap events.txt line should mark the scheduler as self-owned")
	assert.Contains(t, prompt, "INDEPENDENT",
		"prompt should explain the copilot is independent of fry main")
}

func TestRenderBootstrapPromptIncludesAllInterventionProcedures(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "FRY-SOURCE INTERVENTION Procedure")
	assert.Contains(t, prompt, "ARTIFACT REMEDIATION Procedure")
	assert.Contains(t, prompt, "RESTART-WITH-NEW-BINARY Procedure")
}

func TestRenderBootstrapPromptIncludesStopConditions(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "Stop Conditions")
	assert.Contains(t, prompt, "Final Summary Procedure")
}

func TestRenderBootstrapPromptIncludesAllowedAndForbidden(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "Allowed Actions")
	assert.Contains(t, prompt, "Forbidden Actions")
	assert.Contains(t, prompt, "go test -race")
	assert.Contains(t, prompt, "make install")
}

func TestRenderBootstrapPromptDefaultsNowISO(t *testing.T) {
	t.Parallel()

	data := defaultBootstrapData()
	data.NowISO = ""
	prompt, err := RenderBootstrapPrompt(data)
	require.NoError(t, err)
	// NowISO should have been set automatically; the rendered prompt should
	// not contain a literal empty timestamp where it's substituted.
	assert.NotContains(t, prompt, "{{.NowISO}}")
}

func TestRenderBootstrapPromptIncludesTimeHandlingSection(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)

	// The Time handling section tells the agent to get fresh timestamps
	// via `date -u` rather than reusing the bootstrap-time {{.NowISO}}.
	assert.Contains(t, prompt, "Time handling",
		"prompt must include the Time handling section")
	assert.Contains(t, prompt, "date -u",
		"prompt must tell the agent how to get the current UTC time")
	assert.Contains(t, prompt, "<UTC NOW>",
		"prompt must use the <UTC NOW> placeholder so the agent knows what to substitute")
	assert.Contains(t, prompt, "frozen at session-start time",
		"prompt must explain WHY the bootstrap timestamp is unsafe to reuse")
}

func TestRenderBootstrapPromptUsesUTCNowPlaceholderForWakeEntries(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)

	// The bootstrap event line at the top of the One-Time Bootstrap
	// section is the ONLY entry that should use the rendered NowISO
	// value (it fires once at bootstrap time). Every other entry — wake
	// notes, orphan exit, intervention commits, restart logs, final
	// summary — must use the <UTC NOW> placeholder so the agent
	// substitutes the wake-message time.
	wakeTimeMarkers := []string{
		"<UTC NOW>  Orphaned",
		"<UTC NOW>  wake #N: watched",
		"build {{.RunID}}", // commit message frame keeps the run ID
		"<UTC NOW>  [intervention",
		"<UTC NOW>  build restarted with new binary",
		"<UTC NOW>  Copilot exiting cleanly",
	}
	for _, marker := range wakeTimeMarkers {
		// {{.RunID}} would have been substituted by the template engine,
		// so check the substituted form for that one.
		expected := strings.ReplaceAll(marker, "{{.RunID}}", "run-test")
		assert.Contains(t, prompt, expected,
			"wake-time entry should use <UTC NOW> placeholder, not bootstrap time")
	}
}

func TestRenderBootstrapPromptDefaultsIntervalMinutes(t *testing.T) {
	t.Parallel()

	data := defaultBootstrapData()
	data.IntervalMinutes = 0
	data.Interval = "15m"
	prompt, err := RenderBootstrapPrompt(data)
	require.NoError(t, err)
	// Match "15" plus "minutes" within a small window — they may be
	// separated by a line break in the rendered prompt template.
	assert.Contains(t, prompt, "every 15m, scheduler=self",
		"the bootstrap events.txt instruction should report the interval")
	assert.Contains(t, prompt, "every 15",
		"the schedule explanation should mention the resolved minutes")
}

func TestRenderBootstrapPromptCommitMessageTemplate(t *testing.T) {
	t.Parallel()

	prompt, err := RenderBootstrapPrompt(defaultBootstrapData())
	require.NoError(t, err)
	assert.Contains(t, prompt, "[copilot]")
	assert.Contains(t, prompt, "Generated by fry copilot")
}

func TestRenderSummaryPromptBasic(t *testing.T) {
	t.Parallel()

	data := SummaryData{
		BuildDir:     "/tmp/test-build",
		FrySourceDir: "/tmp/test-fry",
		StartedAt:    "2026-04-07T00:00:00Z",
		Outcome:      "complete",
		NowISO:       "2026-04-07T01:00:00Z",
	}
	prompt, err := RenderSummaryPrompt(data)
	require.NoError(t, err)
	assert.Contains(t, prompt, "Final Summary Pass")
	assert.Contains(t, prompt, "Outcome (rough):  complete")
	assert.Contains(t, prompt, data.BuildDir)
	assert.Contains(t, prompt, "## Outcome")
	assert.Contains(t, prompt, "## Anomalies Observed")
}

func TestRenderSummaryPromptDifferentOutcomes(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{"complete", "failed", "aborted"} {
		outcome := outcome
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			data := SummaryData{
				BuildDir: "/tmp",
				Outcome:  outcome,
			}
			prompt, err := RenderSummaryPrompt(data)
			require.NoError(t, err)
			assert.Contains(t, prompt, outcome)
		})
	}
}

func TestWriteBootstrapPromptFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	data := defaultBootstrapData()
	prompt, err := WriteBootstrapPromptFile(dir, data)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)

	path := filepath.Join(dir, config.CopilotBootstrapPromptFile)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, prompt, string(written))
	assert.True(t, strings.Contains(string(written), "Copilot — The Build's Wingman"))
}

func TestWriteSummaryPromptFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	data := SummaryData{BuildDir: "/tmp", Outcome: "failed"}
	prompt, err := WriteSummaryPromptFile(dir, data)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)

	path := filepath.Join(dir, config.CopilotSummaryPromptFile)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, prompt, string(written))
}
