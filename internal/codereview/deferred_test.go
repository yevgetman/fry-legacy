package codereview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeDeferredFailuresDetectsInteractions(t *testing.T) {
	t.Parallel()

	content := `# Deferred Sanity Check Failures

## Sprint 2: Pricing

- DEFERRED: Command failed: scripts/check-pricing.sh

## Sprint 4: Validation

- DEFERRED: Command output mismatch: scripts/check-pricing.sh (expected pattern: PASS)
`

	analysis := AnalyzeDeferredFailures(content)
	require.NotNil(t, analysis)
	require.Len(t, analysis.Entries, 2)
	require.NotEmpty(t, analysis.Interactions)
	hasHigh := false
	for _, interaction := range analysis.Interactions {
		if interaction.Escalation == "HIGH" {
			hasHigh = true
			break
		}
	}
	assert.True(t, hasHigh)
	assert.NotEmpty(t, analysis.Checklist)
}

func TestRenderValidationChecklist(t *testing.T) {
	t.Parallel()

	checklist := []ValidationItem{{
		Description: "Sprint 2 — Pricing drift",
		Source:      "Sprint 2: pricing validation",
		Rationale:   "Interacts with later deferred pricing checks.",
		Priority:    "HIGH",
	}}

	rendered := RenderValidationChecklist(checklist)
	assert.Contains(t, rendered, "Human Validation Required")
	assert.Contains(t, rendered, "Pricing drift")
	assert.Contains(t, rendered, "HIGH")
}

func TestAnalyzeDeferredFailuresEmptyInput(t *testing.T) {
	t.Parallel()

	assert.Nil(t, AnalyzeDeferredFailures(""))
	assert.Nil(t, AnalyzeDeferredFailures("   "))
}

func TestRenderValidationChecklistEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", RenderValidationChecklist(nil))
	assert.Equal(t, "", RenderValidationChecklist([]ValidationItem{}))
}
