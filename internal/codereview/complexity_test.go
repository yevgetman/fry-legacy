package codereview

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyComplexityLow(t *testing.T) {
	t.Parallel()

	diff := "Added an introduction section and clarified the rollout narrative."
	assert.Equal(t, ComplexityLow, ClassifyComplexity(diff, "writing"))
}

func TestClassifyComplexityModerate(t *testing.T) {
	t.Parallel()

	diff := strings.Repeat("The analysis explains pricing assumptions, customer behavior, retention, and rollout tradeoffs. ", 8) +
		"Revenue increased 12%, margin reached 18%, churn fell to 8%, adoption hit 21%, NPS rose 14%, and renewal reached 77%."
	assert.Equal(t, ComplexityModerate, ClassifyComplexity(diff, "writing"))
}

func TestClassifyComplexityHigh(t *testing.T) {
	t.Parallel()

	diff := strings.Join([]string{
		"| Segment | Revenue | Margin |",
		"| Enterprise | 1200 | 35% |",
		"| SMB | 800 | 18% |",
		"| Consumer | 500 | 12% |",
	}, "\n")
	assert.Equal(t, ComplexityHigh, ClassifyComplexity(diff, "writing"))
}

func TestClassifyComplexityEmptyAndUnavailable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ComplexityLow, ClassifyComplexity("", "writing"))
	assert.Equal(t, ComplexityUnknown, ClassifyComplexity("(git diff unavailable)", "writing"))
}

func TestClassifyComplexitySoftwareMode(t *testing.T) {
	t.Parallel()

	diff := strings.Repeat("benchmark latency p95 timeout workers throughput 123 456 789 ", 8)
	assert.Equal(t, ComplexityHigh, ClassifyComplexity(diff, "software"))
}
