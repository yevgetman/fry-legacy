package codereview

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecoveredFindingsPassQualityCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		findings []Finding
		expected bool
	}{
		{
			name:     "empty findings",
			findings: nil,
			expected: false,
		},
		{
			name:     "all short descriptions",
			findings: []Finding{{Description: "bug"}, {Description: "fix"}},
			expected: false,
		},
		{
			name:     "one substantive description",
			findings: []Finding{{Description: "SQL injection in login handler"}},
			expected: true,
		},
		{
			name:     "mixed short and substantive",
			findings: []Finding{{Description: "bug"}, {Description: "Missing null check on response body"}},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, recoveredFindingsPassQualityCheck(tt.findings))
		})
	}
}

func TestParseReviewStyleFindings_ShortBodySkipped(t *testing.T) {
	t.Parallel()

	// Body under 15 chars should be skipped
	short := "1. CRITICAL: Bug\n"
	findings := parseReviewStyleFindings(short, "")
	assert.Empty(t, findings)

	// Body at or above 15 chars should be kept
	long := "1. HIGH: Missing input validation on the login form\n"
	findings = parseReviewStyleFindings(long, "")
	assert.Len(t, findings, 1)
	assert.Equal(t, "HIGH", findings[0].Severity)
}

func TestRecoverReviewReport_ExplicitPass(t *testing.T) {
	t.Parallel()

	// When the transcript contains an explicit pass verdict, recovery should
	// synthesize a clean report.
	transcript := "assistant\nI reviewed the code and found no issues. Verdict: PASS. No findings remain."
	content, source := recoverReviewReport(".fry/sprint-review.txt", "", transcript, "")
	assert.NotEmpty(t, content)
	assert.Equal(t, "assistant summary", source)
	assert.Contains(t, content, "PASS")
}
