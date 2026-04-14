package codereview

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseFindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected []Finding
	}{
		{
			name:    "standard format with location",
			content: "## Findings\n- **Location:** src/handler.go:42\n- **Description:** SQL injection\n- **Severity:** HIGH\n- **Recommended Fix:** Use parameterized queries\n",
			expected: []Finding{
				{Location: "src/handler.go:42", Description: "SQL injection", Severity: "HIGH", RecommendedFix: "Use parameterized queries"},
			},
		},
		{
			name:    "multiple findings",
			content: "- **Location:** a.go:1\n- **Description:** Issue A\n- **Severity:** HIGH\n- **Location:** b.go:2\n- **Description:** Issue B\n- **Severity:** MODERATE\n",
			expected: []Finding{
				{Location: "a.go:1", Description: "Issue A", Severity: "HIGH"},
				{Location: "b.go:2", Description: "Issue B", Severity: "MODERATE"},
			},
		},
		{
			name:    "no location",
			content: "- **Description:** Missing validation\n- **Severity:** MODERATE\n",
			expected: []Finding{
				{Description: "Missing validation", Severity: "MODERATE"},
			},
		},
		{
			name:    "description only no severity",
			content: "- **Description:** Some issue\n",
			expected: []Finding{
				{Description: "Some issue"},
			},
		},
		{
			name:     "no findings",
			content:  "## Summary\nAll good.\n## Verdict\nPASS\n",
			expected: nil,
		},
		{
			name:     "empty content",
			content:  "",
			expected: nil,
		},
		{
			name:    "consecutive descriptions without location",
			content: "- **Description:** Issue A\n- **Severity:** HIGH\n- **Description:** Issue B\n- **Severity:** LOW\n",
			expected: []Finding{
				{Description: "Issue A", Severity: "HIGH"},
				{Description: "Issue B", Severity: "LOW"},
			},
		},
		{
			name:    "plain format without bold",
			content: "- Location: file.go:10\n- Description: Buffer overflow\n- Severity: CRITICAL\n- Recommended Fix: Bounds check\n",
			expected: []Finding{
				{Location: "file.go:10", Description: "Buffer overflow", Severity: "CRITICAL", RecommendedFix: "Bounds check"},
			},
		},
		{
			name:    "word boundary severity parsing",
			content: "- **Description:** HIGHLY unusual pattern\n- **Severity:** LOW\n",
			expected: []Finding{
				{Description: "HIGHLY unusual pattern", Severity: "LOW"},
			},
		},
		{
			name: "captures category",
			content: "## Findings\n" +
				"- **Location:** test/bootstrap.go:12\n" +
				"- **Description:** Missing SUPABASE secrets prevent bootstrap\n" +
				"- **Severity:** HIGH\n" +
				"- **Category:** environment_blocker\n" +
				"- **Recommended Fix:** provide the required secrets before rerunning\n",
			expected: []Finding{{
				Location:       "test/bootstrap.go:12",
				Description:    "Missing SUPABASE secrets prevent bootstrap",
				Severity:       "HIGH",
				Category:       FindingCategoryEnvironmentBlocker,
				RecommendedFix: "provide the required secrets before rerunning",
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseFindings(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseReviewSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content  string
		expected string
	}{
		{"## Findings\n- **Severity:** CRITICAL\n", "CRITICAL"},
		{"Severity: HIGH\nSeverity: MODERATE\n", "HIGH"},
		{"- **Severity:** MODERATE\nedge case\n", "MODERATE"},
		{"- **Severity:** LOW\nstyle issue\n", "LOW"},
		{"## Verdict\nPASS\n", ""},
		{"No issues found.", ""},
		{"CRITICAL bug found here", ""},
		{"This is HIGH priority work", ""},
		{"- **Severity:** LOW\n- **Severity:** HIGH\n- **Severity:** MODERATE\n", "HIGH"},
		{"Severity: CRITICAL\nSeverity: LOW\n", "CRITICAL"},
		{"**Severity:** LOW — HIGHLY unusual but cosmetic\n", "LOW"},
		{"**Severity:** LOW — HIGHLIGHTED concern\n", "LOW"},
		{"**Severity:** LOW — CRITICALLY important style\n", "LOW"},
		{"**Severity:** MODERATE — ALLOW this pattern\n", "MODERATE"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, parseReviewSeverity(tt.content), "content: %q", tt.content)
	}
}

func TestIsReviewPass(t *testing.T) {
	t.Parallel()

	assert.True(t, isReviewPass(""))
	assert.True(t, isReviewPass("LOW"))
	assert.False(t, isReviewPass("MODERATE"))
	assert.False(t, isReviewPass("HIGH"))
	assert.False(t, isReviewPass("CRITICAL"))
}

func TestIsBlockingSeverity(t *testing.T) {
	t.Parallel()

	assert.True(t, isBlockingSeverity("CRITICAL"))
	assert.True(t, isBlockingSeverity("HIGH"))
	assert.False(t, isBlockingSeverity("MODERATE"))
	assert.False(t, isBlockingSeverity("LOW"))
	assert.False(t, isBlockingSeverity(""))
}

func TestCountSeverities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		findings []Finding
		expected map[string]int
	}{
		{
			name:     "single CRITICAL",
			findings: []Finding{{Severity: "CRITICAL"}},
			expected: map[string]int{"CRITICAL": 1},
		},
		{
			name: "mixed severities",
			findings: []Finding{
				{Severity: "CRITICAL"}, {Severity: "HIGH"}, {Severity: "HIGH"},
				{Severity: "MODERATE"}, {Severity: "MODERATE"}, {Severity: "MODERATE"},
				{Severity: "LOW"},
			},
			expected: map[string]int{"CRITICAL": 1, "HIGH": 2, "MODERATE": 3, "LOW": 1},
		},
		{
			name:     "empty",
			findings: nil,
			expected: map[string]int{},
		},
		{
			name:     "only LOW",
			findings: []Finding{{Severity: "LOW"}, {Severity: "LOW"}},
			expected: map[string]int{"LOW": 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := countSeverities(tt.findings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatCounts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1 CRITICAL, 2 HIGH", FormatCounts(map[string]int{"CRITICAL": 1, "HIGH": 2}))
	assert.Equal(t, "none", FormatCounts(nil))
}
