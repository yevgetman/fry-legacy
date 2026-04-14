package codereview

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToSARIFEmptyFindings(t *testing.T) {
	t.Parallel()

	data, err := ConvertToSARIF(nil)
	require.NoError(t, err)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(data, &log))

	assert.Equal(t, "2.1.0", log.Version)
	require.Len(t, log.Runs, 1)
	assert.Empty(t, log.Runs[0].Results)
	assert.Equal(t, "fry", log.Runs[0].Tool.Driver.Name)
}

func TestConvertToSARIFSingleFinding(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{
			Location:       "src/main.go:10",
			Description:    "Null pointer dereference",
			Severity:       "CRITICAL",
			RecommendedFix: "Add nil check before dereferencing",
		},
	}

	data, err := ConvertToSARIF(findings)
	require.NoError(t, err)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(data, &log))

	require.Len(t, log.Runs, 1)
	require.Len(t, log.Runs[0].Results, 1)

	result := log.Runs[0].Results[0]
	assert.Equal(t, "FRY0001", result.RuleID)
	assert.Equal(t, "error", result.Level)
	assert.Contains(t, result.Message.Text, "Null pointer dereference")
	assert.Contains(t, result.Message.Text, "Add nil check before dereferencing")
	require.Len(t, result.Locations, 1)
	assert.Equal(t, "src/main.go:10", result.Locations[0].PhysicalLocation.ArtifactLocation.URI)
}

func TestConvertToSARIFSeverityMapping(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "critical", Severity: "CRITICAL"},
		{Description: "high", Severity: "HIGH"},
		{Description: "moderate", Severity: "MODERATE"},
		{Description: "low", Severity: "LOW"},
	}
	data, err := ConvertToSARIF(findings)
	require.NoError(t, err)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(data, &log))
	results := log.Runs[0].Results
	require.Len(t, results, 4)
	assert.Equal(t, "error", results[0].Level)
	assert.Equal(t, "error", results[1].Level)
	assert.Equal(t, "warning", results[2].Level)
	assert.Equal(t, "note", results[3].Level)
}

func TestConvertToSARIFRuleIDFormat(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "first finding", Severity: "LOW"},
	}
	data, err := ConvertToSARIF(findings)
	require.NoError(t, err)

	var log SARIFLog
	require.NoError(t, json.Unmarshal(data, &log))
	assert.Equal(t, "FRY0001", log.Runs[0].Results[0].RuleID)
}
