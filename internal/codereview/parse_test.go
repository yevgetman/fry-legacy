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

func TestParseReviewMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected *ReviewMetadata
	}{
		{
			name: "full metadata",
			content: "## Review Metadata\n- Iterations completed: 3\n- Convergence: CONVERGED\n\n" +
				"## Review History\n### Pass 1\nFound: 2 CRITICAL, 1 HIGH, 0 MODERATE, 0 LOW\nFixed: 2 CRITICAL, 1 HIGH\n" +
				"### Pass 2\nFound: 0 CRITICAL, 0 HIGH, 1 MODERATE, 0 LOW\nFixed: 0 CRITICAL, 0 HIGH, 1 MODERATE\n" +
				"### Pass 3\nFound: 0 CRITICAL, 0 HIGH, 0 MODERATE, 2 LOW\n",
			expected: &ReviewMetadata{
				IterationsCompleted: 3,
				Convergence:         ConvergenceConverged,
				History: []ReviewPass{
					{PassNumber: 1, FoundCounts: map[string]int{"CRITICAL": 2, "HIGH": 1, "MODERATE": 0, "LOW": 0}, FixedCounts: map[string]int{"CRITICAL": 2, "HIGH": 1}},
					{PassNumber: 2, FoundCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 1, "LOW": 0}, FixedCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 1}},
					{PassNumber: 3, FoundCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 0, "LOW": 2}, FixedCounts: map[string]int{}},
				},
			},
		},
		{
			name:    "only iterations",
			content: "## Review Metadata\n- Iterations completed: 2\n",
			expected: &ReviewMetadata{
				IterationsCompleted: 2,
			},
		},
		{
			name:    "only convergence",
			content: "## Review Metadata\n- Convergence: ITERATION_LIMIT\n",
			expected: &ReviewMetadata{
				Convergence: ConvergenceIterationLimit,
			},
		},
		{
			name:     "no metadata section",
			content:  "## Summary\nAll good.\n\n## Findings\nNone.\n\n## Verdict\nPASS\n",
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseReviewMetadata(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseReviewHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected []ReviewPass
	}{
		{
			name: "three passes",
			content: "### Pass 1\nFound: 3 CRITICAL, 1 HIGH\nFixed: 3 CRITICAL, 1 HIGH\n" +
				"### Pass 2\nFound: 0 CRITICAL, 0 HIGH, 2 MODERATE\nFixed: 2 MODERATE\n" +
				"### Pass 3\nFound: 0 CRITICAL, 0 HIGH, 0 MODERATE, 1 LOW\n",
			expected: []ReviewPass{
				{PassNumber: 1, FoundCounts: map[string]int{"CRITICAL": 3, "HIGH": 1}, FixedCounts: map[string]int{"CRITICAL": 3, "HIGH": 1}},
				{PassNumber: 2, FoundCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 2}, FixedCounts: map[string]int{"MODERATE": 2}},
				{PassNumber: 3, FoundCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 0, "LOW": 1}, FixedCounts: map[string]int{}},
			},
		},
		{
			name:    "single pass clean",
			content: "### Pass 1\nFound: 0 CRITICAL, 0 HIGH, 0 MODERATE, 0 LOW\n",
			expected: []ReviewPass{
				{PassNumber: 1, FoundCounts: map[string]int{"CRITICAL": 0, "HIGH": 0, "MODERATE": 0, "LOW": 0}, FixedCounts: map[string]int{}},
			},
		},
		{
			name:     "no passes",
			content:  "## Review Metadata\n- Iterations completed: 1\n",
			expected: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseReviewHistory(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeduplicateFindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []Finding
		expected []Finding
	}{
		{
			name: "identical findings merged",
			input: []Finding{
				{Location: "api.go:10", Description: "Missing null check", Severity: "HIGH"},
				{Location: "api.go:10", Description: "Missing null check", Severity: "MODERATE"},
			},
			expected: []Finding{
				{Location: "api.go:10", Description: "Missing null check", Severity: "HIGH"},
			},
		},
		{
			name: "different location kept separate",
			input: []Finding{
				{Location: "a.go:1", Description: "Missing null check", Severity: "HIGH"},
				{Location: "b.go:2", Description: "Missing null check", Severity: "HIGH"},
			},
			expected: []Finding{
				{Location: "a.go:1", Description: "Missing null check", Severity: "HIGH"},
				{Location: "b.go:2", Description: "Missing null check", Severity: "HIGH"},
			},
		},
		{
			name: "different description kept separate",
			input: []Finding{
				{Location: "api.go:10", Description: "Missing null check", Severity: "HIGH"},
				{Location: "api.go:10", Description: "Buffer overflow risk", Severity: "HIGH"},
			},
			expected: []Finding{
				{Location: "api.go:10", Description: "Missing null check", Severity: "HIGH"},
				{Location: "api.go:10", Description: "Buffer overflow risk", Severity: "HIGH"},
			},
		},
		{
			name: "severity promotion on dedup",
			input: []Finding{
				{Location: "db.go:5", Description: "SQL injection", Severity: "MODERATE"},
				{Location: "db.go:5", Description: "SQL injection", Severity: "CRITICAL"},
			},
			expected: []Finding{
				{Location: "db.go:5", Description: "SQL injection", Severity: "CRITICAL"},
			},
		},
		{
			name:     "empty input",
			input:    nil,
			expected: []Finding{},
		},
		{
			name: "no duplicates",
			input: []Finding{
				{Location: "a.go:1", Description: "Issue A", Severity: "HIGH"},
				{Location: "b.go:2", Description: "Issue B", Severity: "LOW"},
			},
			expected: []Finding{
				{Location: "a.go:1", Description: "Issue A", Severity: "HIGH"},
				{Location: "b.go:2", Description: "Issue B", Severity: "LOW"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := deduplicateFindings(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindingKey(t *testing.T) {
	t.Parallel()

	// Same file, different line numbers → same key (line stripped)
	k1 := findingKey(Finding{Location: "file.go:42", Description: "Missing check"})
	k2 := findingKey(Finding{Location: "file.go:99", Description: "Missing check"})
	assert.Equal(t, k1, k2)

	// Different files → different keys
	k3 := findingKey(Finding{Location: "a.go:1", Description: "Issue"})
	k4 := findingKey(Finding{Location: "b.go:1", Description: "Issue"})
	assert.NotEqual(t, k3, k4)

	// No location → description only
	k5 := findingKey(Finding{Description: "Some issue"})
	assert.NotEmpty(t, k5)
	assert.NotContains(t, k5, "::")

	// Case and whitespace normalization
	k6 := findingKey(Finding{Location: "File.Go:10", Description: "  Missing  Check  "})
	k7 := findingKey(Finding{Location: "file.go:10", Description: "missing check"})
	assert.Equal(t, k6, k7)
}
