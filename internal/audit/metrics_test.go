package audit

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/engine"
	tokenmetrics "github.com/yevgetman/fry/internal/metrics"
)

func TestAuditMetricsRecordAndSummary(t *testing.T) {
	t.Parallel()

	metrics := &AuditMetrics{}
	metrics.Record(CallMetric{SessionType: engine.SessionAudit, PromptBytes: 100, DurationMs: 10})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditFix, Cycle: 1, PromptBytes: 120, DurationMs: 20, WasNoOp: true})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditFix, Cycle: 1, PromptBytes: 140, DurationMs: 25})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditVerify, Cycle: 1, PromptBytes: 80, DurationMs: 30, Resolutions: 2})
	metrics.RecordCycleSummary(1)

	assert.Equal(t, 4, metrics.TotalCalls())
	assert.Equal(t, int64(85), metrics.TotalDurationMs())
	assert.InDelta(t, 0.5, metrics.NoOpRate(), 0.001)
	assert.InDelta(t, 2.0, metrics.VerifyYield(), 0.001)
	assert.Equal(t, 110, metrics.AvgPromptBytes())

	snapshot := metrics.Snapshot()
	assert.Equal(t, 4, snapshot.TotalCalls)
	assert.Equal(t, 1, snapshot.NoOpFixCalls)
	assert.Equal(t, 1, snapshot.VerifyCalls)
	assert.Equal(t, 2, snapshot.VerifyResolutions)
	assert.Equal(t, 0, snapshot.SessionRefreshes)
}

func TestAuditMetricsMarshalJSON(t *testing.T) {
	t.Parallel()

	metrics := &AuditMetrics{
		Calls: []CallMetric{
			{
				SessionType:          engine.SessionAuditFix,
				PromptBytes:          120,
				DurationMs:           20,
				WasNoOp:              true,
				SessionRefreshReason: "call budget reached (4)",
			},
		},
		OuterCycles:       2,
		ContentComplexity: ComplexityModerate,
		FinalFindingCount: 1,
		SessionRefreshes:  1,
	}

	data, err := json.Marshal(metrics)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	summary, ok := payload["summary"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), summary["total_calls"])
	assert.Equal(t, float64(1), summary["no_op_fix_calls"])
	assert.Equal(t, float64(1), payload["session_refreshes"])
}

func TestCallMetricTokenParsing(t *testing.T) {
	t.Parallel()

	claude := tokenmetrics.ParseTokens("claude", "input_tokens: 10\noutput_tokens: 4\n")
	codex := tokenmetrics.ParseTokens("codex", "\"prompt_tokens\": 7\n\"completion_tokens\": 3\n")
	ollama := tokenmetrics.ParseTokens("ollama", "tokens unavailable")

	assert.Equal(t, 14, claude.Total)
	assert.Equal(t, 10, codex.Total)
	assert.Equal(t, 0, ollama.Total)
}

func TestAuditMetricsRecordTracksSessionRefreshes(t *testing.T) {
	t.Parallel()

	metrics := &AuditMetrics{}

	metrics.Record(CallMetric{SessionType: engine.SessionAudit, SessionRefreshReason: "call budget reached (3)"})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditFix, SessionRefreshReason: "call budget reached (3)"})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditVerify})

	snapshot := metrics.Snapshot()

	assert.Equal(t, 2, metrics.SessionRefreshes)
	assert.Equal(t, 2, snapshot.SessionRefreshes)
}

func TestAuditMetricsCycleSummariesAndYield(t *testing.T) {
	t.Parallel()

	metrics := &AuditMetrics{}

	metrics.Record(CallMetric{SessionType: engine.SessionAudit, Cycle: 1, DurationMs: 10})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditFix, Cycle: 1, DurationMs: 20, WasNoOp: true, Tokens: tokenmetrics.TokenUsage{Input: 5, Output: 3, Total: 8}})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditVerify, Cycle: 1, DurationMs: 30, Resolutions: 1, Tokens: tokenmetrics.TokenUsage{Input: 4, Output: 2, Total: 6}})
	metrics.RecordCycleSummary(1)

	metrics.Record(CallMetric{SessionType: engine.SessionAudit, Cycle: 2, DurationMs: 15, Tokens: tokenmetrics.TokenUsage{Input: 2, Output: 1, Total: 3}})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditFix, Cycle: 2, DurationMs: 25, Tokens: tokenmetrics.TokenUsage{Input: 7, Output: 4, Total: 11}})
	metrics.Record(CallMetric{SessionType: engine.SessionAuditVerify, Cycle: 2, DurationMs: 35, Resolutions: 0, Tokens: tokenmetrics.TokenUsage{Input: 3, Output: 1, Total: 4}})
	metrics.RecordCycleSummary(2)

	require.Len(t, metrics.CycleSummaries, 2)
	assert.Equal(t, 1, metrics.CycleSummaries[0].NoOpFixCalls)
	assert.Equal(t, 14, metrics.CycleSummaries[0].TokenTotal)
	assert.InDelta(t, 0.0, metrics.CycleSummaries[1].FixYield, 0.001)
	assert.InDelta(t, 0.0, metrics.CycleSummaries[1].VerifyYield, 0.001)
	assert.Equal(t, 18, metrics.CycleSummaries[1].TokenTotal)
}
