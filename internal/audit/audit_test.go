package audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
)

// --- stub engine ---

type stubEngine struct {
	name       string
	outputs    []string
	prompts    []string
	runOpts    []engine.RunOpts
	errs       []error
	sideEffect func(projectDir string, callIndex int)
	callIndex  int
}

func (s *stubEngine) Run(_ context.Context, prompt string, opts engine.RunOpts) (string, int, error) {
	s.prompts = append(s.prompts, prompt)
	s.runOpts = append(s.runOpts, opts)
	var output string
	if len(s.outputs) > 0 {
		output = s.outputs[0]
		s.outputs = s.outputs[1:]
	}
	if s.sideEffect != nil {
		s.sideEffect(opts.WorkDir, s.callIndex)
	}
	var runErr error
	if len(s.errs) > 0 {
		runErr = s.errs[0]
		s.errs = s.errs[1:]
	}
	s.callIndex++
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(output))
	}
	return output, 0, runErr
}

func (s *stubEngine) Name() string {
	return s.name
}

// --- helpers ---

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func makeOpts(t *testing.T, eng engine.Engine) AuditOpts {
	t.Helper()
	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintProgressFile), "Built the feature.\n")
	return AuditOpts{
		ProjectDir: projectDir,
		Sprint: &epic.Sprint{
			Number:        1,
			Name:          "Setup",
			MaxIterations: 2,
			Promise:       "DONE",
			Prompt:        "Build the setup sprint.",
		},
		Epic: &epic.Epic{
			TotalSprints:       3,
			AuditAfterSprint:   true,
			MaxAuditIterations: 3,
		},
		Engine:  eng,
		GitDiff: "+new line\n-old line\n",
		Verbose: false,
	}
}

// Standard audit findings content used by multiple tests.
const criticalFindings = "## Summary\nBad stuff.\n\n## Findings\n- **Location:** src/main.go:10\n- **Description:** Null pointer dereference\n- **Severity:** CRITICAL\n- **Recommended Fix:** Add nil check\n\n## Verdict\nFAIL\n"
const highFindings = "## Summary\nBugs found.\n\n## Findings\n- **Location:** src/api.go:20\n- **Description:** Missing error handling\n- **Severity:** HIGH\n- **Recommended Fix:** Handle error\n\n## Verdict\nFAIL\n"
const moderateFindings = "## Summary\nMinor issues.\n\n## Findings\n- **Location:** src/util.go:5\n- **Description:** Edge case not handled\n- **Severity:** MODERATE\n- **Recommended Fix:** Add boundary check\n\n## Verdict\nFAIL\n"
const cleanAudit = "## Summary\nAll good.\n\n## Findings\nNone.\n\n## Verdict\nPASS\n"
const reviewStyleHighFinding = "**Findings**\n\n1. High: Missing error handling in booking cancellation path.\n"
const diffStyleHighFinding = "codex\nI wrote the audit to `.fry/sprint-audit.txt`.\ndiff --git a/.fry/sprint-audit.txt b/.fry/sprint-audit.txt\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/.fry/sprint-audit.txt\n@@ -0,0 +1,8 @@\n+## Summary\n+Recovered from diff.\n+\n+## Findings\n+- **Location:** src/api.go:20\n+- **Description:** Missing error handling\n+- **Severity:** HIGH\n+\n+## Verdict\n+FAIL\n"
const resolvedVerifySummary = "All listed issues are marked `RESOLVED`."
const diffStyleResolvedVerifyOutput = "codex\nVerified the listed issues and wrote the results.\ndiff --git a/.fry/sprint-audit.txt b/.fry/sprint-audit.txt\nnew file mode 100644\nindex 0000000..2222222\n--- /dev/null\n+++ b/.fry/sprint-audit.txt\n@@ -0,0 +1,5 @@\n+- **Issue:** 1\n+- **Status:** RESOLVED\n+\n+- **Issue:** 2\n+- **Status:** RESOLVED\n"
const cleanAuditSummary = "No findings discovered. Verdict is PASS."

// --- Finding type tests ---

func TestFindingKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "sql injection", Finding{Description: "SQL Injection"}.key())
	assert.Equal(t, "sql injection", Finding{Description: "  SQL Injection  "}.key())
	assert.Equal(t, "sql injection", Finding{Description: "sql injection"}.key())
	assert.Equal(t, "src/handler.go::sql injection", Finding{
		Location:    " src/handler.go:42 ",
		Description: " SQL Injection ",
	}.key())
	assert.Equal(t, "src/handler.go::sql injection", Finding{
		Location:    "src/handler.go#L99C3",
		Description: "SQL Injection",
	}.key())
	assert.Equal(t, "service returned code: 500", Finding{Description: "Service returned code: 500"}.key())
}

func TestFindingIsActionable(t *testing.T) {
	t.Parallel()

	assert.True(t, Finding{Description: "x", Severity: "CRITICAL"}.isActionable())
	assert.True(t, Finding{Description: "x", Severity: "HIGH"}.isActionable())
	assert.True(t, Finding{Description: "x", Severity: "MODERATE"}.isActionable())
	assert.False(t, Finding{Description: "x", Severity: "LOW"}.isActionable())
	assert.False(t, Finding{Description: "x", Severity: ""}.isActionable())
	assert.False(t, Finding{Description: "x", Severity: "HIGH", Resolved: true}.isActionable())
}

// --- parseFindings tests ---

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
			name: "captures blocker category and details",
			content: "## Findings\n" +
				"- **Location:** test/bootstrap.go:12\n" +
				"- **Description:** Missing SUPABASE secrets prevent bootstrap\n" +
				"- **Severity:** HIGH\n" +
				"- **Category:** environment_blocker\n" +
				"- **Blocker Details:** missing SUPABASE_URL, SUPABASE_SERVICE_KEY\n" +
				"- **Recommended Fix:** provide the required secrets before rerunning audit\n",
			expected: []Finding{{
				Location:       "test/bootstrap.go:12",
				Description:    "Missing SUPABASE secrets prevent bootstrap",
				Severity:       "HIGH",
				Category:       FindingCategoryEnvironmentBlocker,
				BlockerDetails: "missing SUPABASE_URL, SUPABASE_SERVICE_KEY",
				RecommendedFix: "provide the required secrets before rerunning audit",
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

// --- parseVerificationStatuses tests ---

func TestParseVerificationStatuses(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "Issue A"},
		{Description: "Issue B"},
		{Description: "Issue C"},
	}

	tests := []struct {
		name     string
		content  string
		expected []bool
	}{
		{
			name:     "all resolved",
			content:  "- **Issue:** 1\n- **Status:** RESOLVED\n- **Issue:** 2\n- **Status:** RESOLVED\n- **Issue:** 3\n- **Status:** RESOLVED\n",
			expected: []bool{true, true, true},
		},
		{
			name:     "partial resolution",
			content:  "- **Issue:** 1\n- **Status:** RESOLVED\n- **Issue:** 2\n- **Status:** STILL PRESENT\n- **Issue:** 3\n- **Status:** RESOLVED\n",
			expected: []bool{true, false, true},
		},
		{
			name:     "none resolved",
			content:  "- **Issue:** 1\n- **Status:** STILL PRESENT\n- **Issue:** 2\n- **Status:** STILL PRESENT\n- **Issue:** 3\n- **Status:** STILL PRESENT\n",
			expected: []bool{false, false, false},
		},
		{
			name:     "empty content",
			content:  "",
			expected: []bool{false, false, false},
		},
		{
			name:     "no parseable format",
			content:  "## Findings\n- **Severity:** CRITICAL\n",
			expected: []bool{false, false, false},
		},
		{
			name:     "issue and status on same line",
			content:  "**Issue:** 1 **Status:** RESOLVED\n**Issue:** 2 **Status:** STILL PRESENT\n",
			expected: []bool{true, false, false},
		},
		{
			name:     "out of range issue number ignored",
			content:  "- **Issue:** 99\n- **Status:** RESOLVED\n- **Issue:** 1\n- **Status:** RESOLVED\n",
			expected: []bool{true, false, false},
		},
		{
			name:     "plain format without bold",
			content:  "- Issue: 2\n- Status: RESOLVED\n",
			expected: []bool{false, true, false},
		},
		{
			name:     "behavior unchanged does not resolve",
			content:  "- **Issue:** 2\n- **Status:** BEHAVIOR_UNCHANGED\n",
			expected: []bool{false, false, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseVerificationResults(tt.content, findings)
			resolved := make([]bool, len(result))
			for i, entry := range result {
				resolved[i] = normalizeVerificationStatus(entry.Status) == verifyStatusResolved
			}
			assert.Equal(t, tt.expected, resolved)
		})
	}
}

// --- classifyFindings tests ---

func TestClassifyFindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		known          []Finding
		current        []Finding
		wantResolved   int
		wantPersisting int
		wantNew        int
	}{
		{
			name:           "all resolved",
			known:          []Finding{{Description: "A"}, {Description: "B"}},
			current:        []Finding{},
			wantResolved:   2,
			wantPersisting: 0,
			wantNew:        0,
		},
		{
			name:           "all persisting",
			known:          []Finding{{Description: "A"}, {Description: "B"}},
			current:        []Finding{{Description: "A"}, {Description: "B"}},
			wantResolved:   0,
			wantPersisting: 2,
			wantNew:        0,
		},
		{
			name:           "all new",
			known:          []Finding{},
			current:        []Finding{{Description: "X"}, {Description: "Y"}},
			wantResolved:   0,
			wantPersisting: 0,
			wantNew:        2,
		},
		{
			name:           "mixed",
			known:          []Finding{{Description: "A", OriginCycle: 1}, {Description: "B", OriginCycle: 1}},
			current:        []Finding{{Description: "A"}, {Description: "C"}},
			wantResolved:   1, // B resolved
			wantPersisting: 1, // A persists
			wantNew:        1, // C is new
		},
		{
			name:           "case insensitive match",
			known:          []Finding{{Description: "SQL Injection", OriginCycle: 1}},
			current:        []Finding{{Description: "sql injection"}},
			wantResolved:   0,
			wantPersisting: 1,
			wantNew:        0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			classification := classifyFindings(tt.known, tt.current)
			assert.Equal(t, tt.wantResolved, len(classification.Resolved), "resolved count")
			assert.Equal(t, tt.wantPersisting, len(classification.Persisting), "persisting count")
			assert.Equal(t, tt.wantNew, len(classification.NewFindings), "new count")
		})
	}
}

func TestClassifyFindingsPreservesOriginCycle(t *testing.T) {
	t.Parallel()

	known := []Finding{{Description: "Old issue", OriginCycle: 1, Severity: "HIGH"}}
	current := []Finding{{Description: "Old issue", Severity: "MODERATE"}} // severity may change

	classification := classifyFindings(known, current)
	require.Len(t, classification.Persisting, 1)
	assert.Equal(t, 1, classification.Persisting[0].OriginCycle, "should preserve original cycle")
}

// --- sortFindingsFIFO tests ---

func TestSortFindingsFIFO(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "new-moderate", OriginCycle: 2, Severity: "MODERATE"},
		{Description: "old-high", OriginCycle: 1, Severity: "HIGH"},
		{Description: "new-critical", OriginCycle: 2, Severity: "CRITICAL"},
		{Description: "old-critical", OriginCycle: 1, Severity: "CRITICAL"},
		{Description: "old-moderate", OriginCycle: 1, Severity: "MODERATE"},
	}
	sortFindingsFIFO(findings)

	// Cycle 1 first (oldest), sorted by severity desc within cycle
	assert.Equal(t, "old-critical", findings[0].Description)
	assert.Equal(t, "old-high", findings[1].Description)
	assert.Equal(t, "old-moderate", findings[2].Description)
	// Cycle 2 next
	assert.Equal(t, "new-critical", findings[3].Description)
	assert.Equal(t, "new-moderate", findings[4].Description)
}

// --- mergeFindings tests ---

func TestMergeFindings(t *testing.T) {
	t.Parallel()

	persisting := []Finding{{Description: "old", OriginCycle: 1, Severity: "HIGH"}}
	newFindings := []Finding{{Description: "new", OriginCycle: 2, Severity: "CRITICAL"}}

	merged := mergeFindings(persisting, newFindings)
	require.Len(t, merged, 2)
	assert.Equal(t, "old", merged[0].Description, "oldest first")
	assert.Equal(t, "new", merged[1].Description)
}

// --- groupByCycle tests ---

func TestGroupByCycle(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "c2a", OriginCycle: 2},
		{Description: "c1a", OriginCycle: 1},
		{Description: "c1b", OriginCycle: 1},
		{Description: "c3a", OriginCycle: 3},
	}
	groups := groupByCycle(findings)

	require.Len(t, groups, 3)
	assert.Equal(t, 1, groups[0].cycle)
	assert.Len(t, groups[0].findings, 2)
	assert.Equal(t, 2, groups[1].cycle)
	assert.Len(t, groups[1].findings, 1)
	assert.Equal(t, 3, groups[2].cycle)
	assert.Len(t, groups[2].findings, 1)
}

func TestClusterFixFindingsGroupsBySharedScopeAndTheme(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{
			Location:       "internal/api/handler.go:12",
			Description:    "Missing input validation on create endpoint",
			Severity:       "HIGH",
			RecommendedFix: "Validate required fields before writing to storage.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/handler.go:44",
			Description:    "Create endpoint still accepts malformed payloads without validation",
			Severity:       "MODERATE",
			RecommendedFix: "Reuse the same validation guard before decoding.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/billing/worker.go:19",
			Description:    "Billing retry loop ignores context cancellation",
			Severity:       "HIGH",
			RecommendedFix: "Stop retrying once the worker context is canceled.",
			OriginCycle:    2,
		},
	}

	clusters := clusterFixFindings(findings)
	require.Len(t, clusters, 2)
	assert.Len(t, clusters[0].Findings, 2)
	assert.Equal(t, "internal/api/handler: validation endpoint", clusters[0].Label)
	assert.Contains(t, clusters[0].Reason, "shared scope")
	assert.Contains(t, clusters[0].Reason, "requirement theme")
	assert.Equal(t, []string{"internal/api/handler.go"}, clusters[0].TargetFiles)
	assert.Len(t, clusters[1].Findings, 1)
}

func TestOrderFindingsByClusterKeepsClusterMembersAdjacent(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{
			Location:       "internal/api/handler.go:12",
			Description:    "Missing input validation on create endpoint",
			Severity:       "HIGH",
			RecommendedFix: "Validate required fields before writing to storage.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/billing/worker.go:19",
			Description:    "Billing retry loop ignores context cancellation",
			Severity:       "HIGH",
			RecommendedFix: "Stop retrying once the worker context is canceled.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/handler.go:44",
			Description:    "Create endpoint still accepts malformed payloads without validation",
			Severity:       "MODERATE",
			RecommendedFix: "Reuse the same validation guard before decoding.",
			OriginCycle:    1,
		},
	}

	ordered := orderFindingsByCluster(findings)
	require.Len(t, ordered, 3)
	assert.Equal(t, "internal/api/handler.go:12", ordered[0].Location)
	assert.Equal(t, "internal/api/handler.go:44", ordered[1].Location)
	assert.Equal(t, "internal/billing/worker.go:19", ordered[2].Location)
}

func TestClusterFixFindingsGroupsAcrossSharedSubsystem(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{
			Location:       "internal/api/create.go:12",
			Description:    "Create flow skips tenant validation",
			Severity:       "HIGH",
			RecommendedFix: "Run tenant validation before writing the record.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/update.go:21",
			Description:    "Update flow also skips tenant validation",
			Severity:       "HIGH",
			RecommendedFix: "Reuse the tenant validation guard in the update path.",
			OriginCycle:    1,
		},
	}

	clusters := clusterFixFindings(findings)
	require.Len(t, clusters, 1)
	assert.Equal(t, "internal/api: validation tenant", clusters[0].Label)
	assert.Equal(t, []string{"internal/api/create.go", "internal/api/update.go"}, clusters[0].TargetFiles)
}

func TestClusterFixFindingsDoesNotBridgeLooseThemes(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{
			Location:       "internal/api/auth.go:12",
			Description:    "Token refresh misses expiry validation",
			Severity:       "HIGH",
			RecommendedFix: "Add expiry validation before token refresh.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/cache.go:21",
			Description:    "Refresh worker also skips expiry validation in cache path",
			Severity:       "HIGH",
			RecommendedFix: "Reuse expiry validation in the cache worker.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/cache.go:48",
			Description:    "Cache worker drops warm sessions during serialization",
			Severity:       "MODERATE",
			RecommendedFix: "Preserve warm sessions in the cache worker serializer.",
			OriginCycle:    1,
		},
	}

	clusters := clusterFixFindings(findings)
	require.Len(t, clusters, 2)
	assert.Len(t, clusters[0].Findings, 2)
	assert.Len(t, clusters[1].Findings, 1)
	assert.Equal(t, "Cache worker drops warm sessions during serialization", clusters[1].Findings[0].Description)
}

// --- filterUnresolved tests ---

func TestFilterUnresolved(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "a", Severity: "HIGH", Resolved: false},
		{Description: "b", Severity: "LOW", Resolved: false},
		{Description: "c", Severity: "CRITICAL", Resolved: true},
		{Description: "d", Severity: "MODERATE", Resolved: false},
		{Description: "e", Severity: "", Resolved: false},
	}
	result := filterUnresolved(findings)
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Description)
	assert.Equal(t, "d", result[1].Description)
}

// --- hasProgress tests ---

func TestHasProgress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		previous map[string]struct{}
		current  map[string]struct{}
		expected bool
	}{
		{
			name:     "current empty — all resolved",
			previous: map[string]struct{}{"a": {}},
			current:  map[string]struct{}{},
			expected: true,
		},
		{
			name:     "previous empty — first iteration",
			previous: map[string]struct{}{},
			current:  map[string]struct{}{"a": {}},
			expected: true,
		},
		{
			name:     "both empty",
			previous: map[string]struct{}{},
			current:  map[string]struct{}{},
			expected: true,
		},
		{
			name:     "identical findings — no progress",
			previous: map[string]struct{}{"a": {}, "b": {}},
			current:  map[string]struct{}{"a": {}, "b": {}},
			expected: false,
		},
		{
			name:     "fewer findings — progress",
			previous: map[string]struct{}{"a": {}, "b": {}, "c": {}},
			current:  map[string]struct{}{"a": {}, "b": {}},
			expected: true,
		},
		{
			name:     "different findings — progress",
			previous: map[string]struct{}{"a": {}, "b": {}},
			current:  map[string]struct{}{"c": {}, "d": {}},
			expected: true,
		},
		{
			name:     "superset of previous — no progress",
			previous: map[string]struct{}{"a": {}, "b": {}},
			current:  map[string]struct{}{"a": {}, "b": {}, "c": {}},
			expected: false,
		},
		{
			name:     "partial overlap with new — progress",
			previous: map[string]struct{}{"a": {}, "b": {}},
			current:  map[string]struct{}{"a": {}, "c": {}},
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, hasProgress(tt.previous, tt.current))
		})
	}
}

// --- effectiveOuterCycles tests ---

func TestEffectiveOuterCycles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		epic         *epic.Epic
		wantMax      int
		wantProgress bool
	}{
		{
			name:         "standard effort default",
			epic:         &epic.Epic{EffortLevel: epic.EffortStandard, MaxAuditIterations: 3},
			wantMax:      3,
			wantProgress: false,
		},
		{
			name:         "high effort not explicitly set",
			epic:         &epic.Epic{EffortLevel: epic.EffortHigh, MaxAuditIterations: 3},
			wantMax:      config.MaxOuterCyclesHighCap,
			wantProgress: true,
		},
		{
			name:         "max effort not explicitly set",
			epic:         &epic.Epic{EffortLevel: epic.EffortMax, MaxAuditIterations: 3},
			wantMax:      config.MaxOuterCyclesMaxCap,
			wantProgress: true,
		},
		{
			name:         "high effort explicitly set",
			epic:         &epic.Epic{EffortLevel: epic.EffortHigh, MaxAuditIterations: 5, MaxAuditIterationsSet: true},
			wantMax:      5,
			wantProgress: false,
		},
		{
			name:         "fast effort",
			epic:         &epic.Epic{EffortLevel: epic.EffortFast, MaxAuditIterations: 3},
			wantMax:      3,
			wantProgress: false,
		},
		{
			name:         "unset effort zero iterations",
			epic:         &epic.Epic{},
			wantMax:      config.DefaultMaxOuterAuditCycles,
			wantProgress: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			maxCycles, progressBased := effectiveOuterCycles(tt.epic, "")
			assert.Equal(t, tt.wantMax, maxCycles)
			assert.Equal(t, tt.wantProgress, progressBased)
		})
	}
}

// --- effectiveInnerIter tests ---

func TestEffectiveInnerIter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		epic *epic.Epic
		want int
	}{
		{name: "default", epic: &epic.Epic{}, want: config.DefaultMaxInnerFixIter},
		{name: "standard", epic: &epic.Epic{EffortLevel: epic.EffortStandard}, want: config.DefaultMaxInnerFixIter},
		{name: "high", epic: &epic.Epic{EffortLevel: epic.EffortHigh}, want: config.MaxInnerFixIterHigh},
		{name: "max", epic: &epic.Epic{EffortLevel: epic.EffortMax}, want: config.MaxInnerFixIterMax},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, effectiveInnerIter(tt.epic, ""))
		})
	}
}

// --- Prompt tests ---

func TestAuditPromptContainsDiff(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prompt := buildAuditPrompt(opts, nil, nil)
	assert.Contains(t, prompt, "+new line")
	assert.Contains(t, prompt, "-old line")
}

func TestAuditPromptWritingMode(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.Mode = "writing"
	prompt := buildAuditPrompt(opts, nil, nil)
	assert.Contains(t, prompt, "content auditor")
	assert.Contains(t, prompt, "Coherence")
	assert.Contains(t, prompt, "Tone & Voice")
	assert.Contains(t, prompt, "Depth")
	assert.NotContains(t, prompt, "code auditor")
	assert.NotContains(t, prompt, "Security")
}

func TestAuditPromptCondensesExecutive(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'x'
	}
	writeFile(t, filepath.Join(opts.ProjectDir, config.ExecutiveFile), string(long))

	prompt := buildAuditPrompt(opts, nil, nil)
	assert.Contains(t, prompt, "...(truncated)")
	assert.Contains(t, prompt, "## Project Context")
}

func TestAuditPromptTruncatesSprintProgress(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	large := make([]byte, 60000)
	for i := range large {
		large[i] = 'y'
	}
	writeFile(t, filepath.Join(opts.ProjectDir, config.SprintProgressFile), string(large))

	prompt := buildAuditPrompt(opts, nil, nil)
	assert.Contains(t, prompt, "...(sprint progress truncated at 50KB)")
	assert.Contains(t, prompt, "## What Was Done")
}

func TestAuditPromptWithPreviousFindings(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prev := []Finding{
		{Location: "src/main.go:10", Description: "Null pointer", Severity: "CRITICAL", OriginCycle: 1},
		{Description: "Missing validation", Severity: "HIGH", OriginCycle: 1},
	}
	prompt := buildAuditPrompt(opts, prev, nil)

	assert.Contains(t, prompt, "## Previously Identified Issues")
	assert.Contains(t, prompt, "[src/main.go:10] Null pointer (CRITICAL)")
	assert.Contains(t, prompt, "Missing validation (HIGH)")
	assert.Contains(t, prompt, "## Verified Previous Issues")
	assert.Contains(t, prompt, "RESOLVED | STILL PRESENT")
}

func TestAuditPromptIncludesCodebaseContextAndMemories(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	writeFile(t, filepath.Join(opts.ProjectDir, config.CodebaseFile), "# Codebase: Fry\n\nExisting architecture details.")
	writeFile(t, filepath.Join(opts.ProjectDir, config.CodebaseMemoriesDir, "001-memory.md"), `---
confidence: high
source: build-1
sprint: 1
date: 2026-03-31
reinforced: 0
---
Audit changes usually need matching updates in docs/sprint-audit.md.`)

	prompt := buildAuditPrompt(opts, nil, nil)

	assert.Contains(t, prompt, "## Codebase Context")
	assert.Contains(t, prompt, "Existing architecture details.")
	assert.Contains(t, prompt, "## Codebase Memories")
	assert.Contains(t, prompt, "Audit changes usually need matching updates")
}

func TestAuditPromptNoPreviousFindings(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prompt := buildAuditPrompt(opts, nil, nil)

	assert.NotContains(t, prompt, "## Previously Identified Issues")
	assert.NotContains(t, prompt, "## Verified Previous Issues")
}

func TestAuditPromptSkipsResolvedPreviousFindings(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prev := []Finding{
		{Description: "Resolved issue", Severity: "HIGH", Resolved: true},
		{Description: "Active issue", Severity: "CRITICAL", Resolved: false},
	}
	prompt := buildAuditPrompt(opts, prev, nil)

	assert.Contains(t, prompt, "## Previously Identified Issues")
	assert.NotContains(t, prompt, "Resolved issue")
	assert.Contains(t, prompt, "Active issue")
}

func TestAuditPromptIncludesSessionRefreshSummary(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.SessionCarryForward = "Session refreshed because call budget reached (3).\n- src/api.go:20: Missing error handling [HIGH]"

	prompt := buildAuditPrompt(opts, nil, nil)

	assert.Contains(t, prompt, "## Session Refresh Summary")
	assert.Contains(t, prompt, "call budget reached (3)")
	assert.Contains(t, prompt, "Missing error handling")
}

func TestUnifiedFixPromptFIFO(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	findings := []Finding{
		{Description: "Old issue", Severity: "HIGH", OriginCycle: 1},
		{Description: "New issue", Severity: "CRITICAL", OriginCycle: 2},
	}
	cluster := remediationCluster{ID: 1, Label: "test cluster", Findings: findings}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "## Issues to Fix")
	assert.Contains(t, prompt, "Old issue")
	assert.Contains(t, prompt, "New issue")
	assert.Contains(t, prompt, "- **Origin Cycle:** 1")
	assert.Contains(t, prompt, "- **Origin Cycle:** 2")
}

func TestUnifiedFixPromptListsClusterIssues(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	findings := []Finding{
		{
			Location:       "internal/api/handler.go:12",
			Description:    "Missing input validation on create endpoint",
			Severity:       "HIGH",
			RecommendedFix: "Validate required fields before writing to storage.",
			OriginCycle:    1,
		},
		{
			Location:       "internal/api/handler.go:44",
			Description:    "Create endpoint still accepts malformed payloads without validation",
			Severity:       "MODERATE",
			RecommendedFix: "Reuse the same validation guard before decoding.",
			OriginCycle:    1,
		},
	}
	cluster := remediationCluster{
		ID:          1,
		Label:       "internal/api: validation",
		Findings:    findings,
		TargetFiles: []string{"internal/api/handler.go"},
	}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "Cluster 1: internal/api: validation")
	assert.Contains(t, prompt, "## Issues to Fix")
	assert.Contains(t, prompt, "### Issue 1")
	assert.Contains(t, prompt, "### Issue 2")
	assert.Contains(t, prompt, "Missing input validation")
	assert.Contains(t, prompt, "malformed payloads")
}

func TestUnifiedFixPromptWritingMode(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.Mode = "writing"
	cluster := remediationCluster{ID: 1, Label: "content", Findings: []Finding{{Description: "weak transition", Severity: "MODERATE", OriginCycle: 1}}}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)
	assert.Contains(t, prompt, "content issues")
	assert.Contains(t, prompt, "minimal editorial changes")
}

func TestUnifiedFixPromptIncludesCodebaseContext(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	writeFile(t, filepath.Join(opts.ProjectDir, config.CodebaseFile), "# Codebase: Fry\n\nFollow grouped imports and contextual errors.")

	cluster := remediationCluster{ID: 1, Label: "errors", Findings: []Finding{{Description: "weak error context", Severity: "HIGH", OriginCycle: 1}}}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "## Codebase Context")
	assert.Contains(t, prompt, "Follow grouped imports and contextual errors.")
	assert.Contains(t, prompt, "Preserve unrelated behavior")
}

func TestUnifiedFixPromptIncludesFixContract(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	findings := []Finding{{
		Location:       "internal/audit/audit.go:123",
		Description:    "missing nil guard",
		Severity:       "HIGH",
		RecommendedFix: "Add the nil guard before dereference.",
	}}
	cluster := remediationCluster{ID: 1, Label: "audit: nil guard", Findings: findings, TargetFiles: []string{"internal/audit/audit.go"}}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "## Fix Contract")
	assert.Contains(t, prompt, "### Issue 1 Contract")
	assert.Contains(t, prompt, "**Target Files:** internal/audit/audit.go")
	assert.Contains(t, prompt, "already fixed")
	assert.Contains(t, prompt, "### Issue 1")
}

func TestUnifiedFixPromptIncludesSessionRefreshSummary(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.SessionCarryForward = "Session refreshed because token budget reached (16000 tokens).\nRecent failed fix attempts:\n- Attempt 1: no-op."

	cluster := remediationCluster{ID: 1, Label: "guard", Findings: []Finding{{
		Description: "missing nil guard",
		Severity:    "HIGH",
		OriginCycle: 1,
	}}}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "## Session Refresh Summary")
	assert.Contains(t, prompt, "token budget reached (16000 tokens)")
	assert.Contains(t, prompt, "Recent failed fix attempts")
}

func TestUnifiedFixPromptIncludesDiffAndProgress(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	opts.GitDiff = "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"

	writeFile(t, filepath.Join(opts.ProjectDir, config.SprintProgressFile), "Sprint 1 progress: implemented auth module")

	cluster := remediationCluster{ID: 1, Label: "auth", Findings: []Finding{{Description: "missing auth check", Severity: "HIGH", OriginCycle: 1}}}
	prompt := buildUnifiedFixPrompt(opts, cluster, nil, nil)

	assert.Contains(t, prompt, "## Changes Made This Sprint")
	assert.Contains(t, prompt, "diff --git a/main.go b/main.go")
	assert.Contains(t, prompt, "## What Was Done")
	assert.Contains(t, prompt, "implemented auth module")
}

func TestUnifiedFixPromptIncludesResolvedThemes(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})

	resolved := newResolvedLedger()
	resolved.add([]Finding{{Location: "src/api.go:10", Description: "SQL injection in query builder", Severity: "CRITICAL", OriginCycle: 1}})

	cluster := remediationCluster{ID: 1, Label: "api", Findings: []Finding{{Description: "missing input validation", Severity: "HIGH", OriginCycle: 2}}}
	prompt := buildUnifiedFixPrompt(opts, cluster, resolved, nil)

	assert.Contains(t, prompt, "## Resolved Themes (Do Not Re-Break)")
	assert.Contains(t, prompt, "SQL injection in query builder")
}

func TestBuildVerifyPrompt(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	findings := []Finding{
		{Location: "src/main.go:10", Description: "Null pointer", Severity: "CRITICAL"},
		{Description: "Missing validation", Severity: "HIGH"},
	}
	prompt := buildVerifyPrompt(opts, findings)

	assert.Contains(t, prompt, "# VERIFY FIXES")
	assert.Contains(t, prompt, "Do NOT look for new issues")
	assert.Contains(t, prompt, "## Issues to Verify")
	assert.Contains(t, prompt, "1. [src/main.go:10] Null pointer (CRITICAL)")
	assert.Contains(t, prompt, "2. Missing validation (HIGH)")
	assert.Contains(t, prompt, "BEHAVIOR_UNCHANGED")
}

// --- Severity parsing tests ---

func TestParseAuditSeverity(t *testing.T) {
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
		assert.Equal(t, tt.expected, parseAuditSeverity(tt.content), "content: %q", tt.content)
	}
}

func TestIsAuditPass(t *testing.T) {
	t.Parallel()

	assert.True(t, isAuditPass(""))
	assert.True(t, isAuditPass("LOW"))
	assert.False(t, isAuditPass("MODERATE"))
	assert.False(t, isAuditPass("HIGH"))
	assert.False(t, isAuditPass("CRITICAL"))
}

func TestIsBlockingSeverity(t *testing.T) {
	t.Parallel()

	assert.True(t, isBlockingSeverity("CRITICAL"))
	assert.True(t, isBlockingSeverity("HIGH"))
	assert.False(t, isBlockingSeverity("MODERATE"))
	assert.False(t, isBlockingSeverity("LOW"))
	assert.False(t, isBlockingSeverity(""))
}

// --- countAuditSeverities tests ---

func TestCountAuditSeverities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		expected map[string]int
	}{
		{
			name:     "single CRITICAL",
			content:  "## Findings\n- **Severity:** CRITICAL\n",
			expected: map[string]int{"CRITICAL": 1},
		},
		{
			name: "mixed severities",
			content: "- **Severity:** CRITICAL\n- **Severity:** HIGH\n- **Severity:** HIGH\n" +
				"- **Severity:** MODERATE\n- **Severity:** MODERATE\n- **Severity:** MODERATE\n" +
				"- **Severity:** LOW\n",
			expected: map[string]int{"CRITICAL": 1, "HIGH": 2, "MODERATE": 3, "LOW": 1},
		},
		{
			name:     "no severity lines",
			content:  "## Summary\nAll good.\n## Verdict\nPASS\n",
			expected: map[string]int{},
		},
		{
			name:     "only LOW",
			content:  "- **Severity:** LOW\n- **Severity:** LOW\n",
			expected: map[string]int{"LOW": 2},
		},
		{
			name:     "non-label lines ignored",
			content:  "CRITICAL bug found here\nThis is HIGH priority\n",
			expected: map[string]int{},
		},
		{
			name:     "word boundary — HIGHLY should not match HIGH",
			content:  "**Severity:** LOW — HIGHLY unusual\n",
			expected: map[string]int{"LOW": 1},
		},
		{
			name:     "empty content",
			content:  "",
			expected: map[string]int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := countAuditSeverities(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- formatSeverityCounts tests ---

func TestFormatSeverityCounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		counts   map[string]int
		expected string
	}{
		{
			name:     "all levels",
			counts:   map[string]int{"CRITICAL": 1, "HIGH": 2, "MODERATE": 4, "LOW": 1},
			expected: "1 CRITICAL, 2 HIGH, 4 MODERATE, 1 LOW",
		},
		{
			name:     "only high and moderate",
			counts:   map[string]int{"HIGH": 3, "MODERATE": 1},
			expected: "3 HIGH, 1 MODERATE",
		},
		{
			name:     "single level",
			counts:   map[string]int{"LOW": 5},
			expected: "5 LOW",
		},
		{
			name:     "empty map",
			counts:   map[string]int{},
			expected: "none",
		},
		{
			name:     "nil map",
			counts:   nil,
			expected: "none",
		},
		{
			name:     "zero counts omitted",
			counts:   map[string]int{"CRITICAL": 0, "HIGH": 1},
			expected: "1 HIGH",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatSeverityCounts(tt.counts))
		})
	}
}

func TestFormatCounts(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1 CRITICAL, 2 HIGH", FormatCounts(map[string]int{"CRITICAL": 1, "HIGH": 2}))
	assert.Equal(t, "none", FormatCounts(nil))
}

// --- Cleanup tests ---

func TestCleanup(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "findings\n")
	writeFile(t, filepath.Join(projectDir, config.AuditPromptFile), "prompt\n")

	require.NoError(t, Cleanup(projectDir))

	_, err := os.Stat(filepath.Join(projectDir, config.SprintAuditFile))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(projectDir, config.AuditPromptFile))
	assert.True(t, os.IsNotExist(err))
}

func TestCleanupMissingFiles(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	require.NoError(t, Cleanup(projectDir))
}

// --- RunAuditLoop integration tests ---

func TestRunAuditLoopPassesImmediately(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
		},
	}
	opts := makeOpts(t, eng)

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
	// Only audit agent called, not fix or verify
	assert.Len(t, eng.prompts, 1)
	assert.Equal(t, config.AuditInvocationPrompt, eng.prompts[0])
}

func TestRunAuditLoopExhaustsCritical(t *testing.T) {
	t.Parallel()

	// Stub always writes CRITICAL findings. The verify parser won't find
	// Issue/Status format, so nothing gets resolved. Inner loop stales
	// after 2 fix iterations per cycle.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), criticalFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 2

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, 2, result.Iterations) // 2 outer cycles
	assert.Equal(t, "CRITICAL", result.MaxSeverity)

	// Call sequence per cycle: audit + (fix + verify)*2 = 5
	// Two cycles: 5*2 = 10
	assert.Len(t, eng.prompts, 10)
}

func TestRunAuditLoopExhaustsModerateAdvisory(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), moderateFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 2

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.False(t, result.Blocking) // MODERATE is advisory
	assert.Equal(t, 2, result.Iterations)
	assert.Equal(t, "MODERATE", result.MaxSeverity)
}

func TestRunAuditLoopExhaustsHighBlocking(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), highFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 2

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking) // HIGH is blocking
	assert.Equal(t, 2, result.Iterations)
	assert.Equal(t, "HIGH", result.MaxSeverity)
}

func TestRunAuditLoopNoFindingsFile(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{name: "codex"}
	opts := makeOpts(t, eng)

	_, err := RunAuditLoop(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit session did not write")
}

func TestRunAuditLoopRecoversMissingAuditOutputsFromAgentResponse(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			reviewStyleHighFinding,
			"Applied a targeted fix.\n",
			resolvedVerifySummary,
			cleanAuditSummary,
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
	assert.Len(t, eng.prompts, 3)
}

func TestRunAuditLoopRecoversMissingAuditOutputsFromDiffTranscript(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			diffStyleHighFinding,
			"Applied a targeted fix.\n",
			resolvedVerifySummary,
			cleanAuditSummary,
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
}

func TestRunAuditLoopContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eng := &stubEngine{name: "codex"}
	opts := makeOpts(t, eng)

	_, err := RunAuditLoop(ctx, opts)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunAuditLoopNilEpic(t *testing.T) {
	t.Parallel()

	_, err := RunAuditLoop(context.Background(), AuditOpts{
		Engine: &stubEngine{name: "codex"},
		Sprint: &epic.Sprint{Number: 1, Name: "One"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "epic and sprint are required")
}

func TestRunAuditLoopNilEngine(t *testing.T) {
	t.Parallel()

	_, err := RunAuditLoop(context.Background(), AuditOpts{
		Epic:   &epic.Epic{},
		Sprint: &epic.Sprint{Number: 1, Name: "One"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "engine is required")
}

func TestRunAuditLoopPopulatesSeverityCounts(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile),
				"## Findings\n- **Description:** A\n- **Severity:** HIGH\n- **Description:** B\n- **Severity:** MODERATE\n- **Description:** C\n- **Severity:** MODERATE\n\n## Verdict\nFAIL\n")
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.NotNil(t, result.SeverityCounts)
	assert.Equal(t, 1, result.SeverityCounts["HIGH"])
	assert.Equal(t, 2, result.SeverityCounts["MODERATE"])
}

// --- Inner loop resolution tests ---

func TestRunAuditLoopInnerLoopResolvesAll(t *testing.T) {
	t.Parallel()

	// Cycle 1: audit finds 2 issues.
	// Fix 1: verify reports both resolved.
	// Cycle 2 (re-audit): clean audit → pass.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit
				writeFile(t, path,
					"## Findings\n- **Description:** Issue A\n- **Severity:** HIGH\n- **Description:** Issue B\n- **Severity:** MODERATE\n\n## Verdict\nFAIL\n")
			case 1: // fix 1 (no write)
			case 2: // verify 1 → all resolved
				writeFile(t, path,
					"- **Issue:** 1\n- **Status:** RESOLVED\n- **Issue:** 2\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 audit → clean
				writeFile(t, path, cleanAudit)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 3

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 2, result.Iterations)
	require.Len(t, eng.prompts, 4)
	assert.Equal(t, config.AuditVerifyInvocationPrompt, eng.prompts[2])
}

func TestRunAuditLoopInnerLoopPartialResolution(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			// Always write CRITICAL findings. The verify parser won't find Issue/Status
			// format, so nothing resolves → inner loop stales after 2 fix attempts.
			writeFile(t, path,
				"## Findings\n- **Description:** Unfixable issue\n- **Severity:** CRITICAL\n\n## Verdict\nFAIL\n")
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, "CRITICAL", result.MaxSeverity)
	assert.Equal(t, 1, result.Iterations)
}

func TestRunAuditLoopVerifyRequiresOutputFile(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			if callIndex == 0 {
				writeFile(t, path,
					"## Findings\n- **Description:** Issue A\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n")
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	_, err := RunAuditLoop(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verify session did not write")
}

func TestRunAuditLoopVerifyRecoversMissingOutputFromDiffTranscript(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			highFindings,
			"Applied a targeted fix.\n",
			diffStyleResolvedVerifyOutput,
			cleanAuditSummary,
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
}

func TestRecoverAuditReportFromDiffTranscript(t *testing.T) {
	t.Parallel()

	content, source := recoverAuditReport(config.SprintAuditFile, "", diffStyleHighFinding, "")
	require.NotEmpty(t, content)
	assert.Equal(t, "assistant diff", source)
	assert.Contains(t, content, "Recovered from diff.")
	assert.Contains(t, content, "- **Severity:** HIGH")
	assert.Contains(t, content, "## Verdict")
}

func TestRecoverVerificationOutputFromDiffTranscript(t *testing.T) {
	t.Parallel()

	content, source := recoverVerificationOutput(config.SprintAuditFile, diffStyleResolvedVerifyOutput, "", 2)
	require.NotEmpty(t, content)
	assert.Equal(t, "assistant diff", source)
	assert.Contains(t, content, "- **Issue:** 1")
	assert.Contains(t, content, "- **Status:** RESOLVED")
	assert.Contains(t, content, "- **Issue:** 2")
}

func TestRunAuditLoopPropagatesAgentErrors(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		errs: []error{fmt.Errorf("engine crashed")},
	}
	opts := makeOpts(t, eng)

	_, err := RunAuditLoop(context.Background(), opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent run")
	assert.Contains(t, err.Error(), "engine crashed")
}

func TestRunAuditLoopNewIssuesInReAudit(t *testing.T) {
	t.Parallel()

	// Cycle 1: finds SQL injection. Fix resolves it.
	// Cycle 2 (re-audit): SQL injection resolved, but new memory leak found.
	// Fix resolves memory leak.
	// Cycle 3 (re-audit): all clean → pass.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit
				writeFile(t, path,
					"## Findings\n- **Location:** src/auth.go:10\n- **Description:** SQL injection in authentication query\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n")
			case 2: // verify: SQL injection resolved
				writeFile(t, path,
					"- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 audit: SQL injection resolved, memory leak new
				writeFile(t, path,
					"## Findings\n- **Location:** src/cache.go:50\n- **Description:** Memory leak in connection pool cleanup\n- **Severity:** MODERATE\n\n## Verdict\nFAIL\n")
			case 5: // verify: memory leak resolved
				writeFile(t, path,
					"- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 6: // cycle 3 audit: all clean
				writeFile(t, path, cleanAudit)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 5

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 3, result.Iterations) // 3 outer cycles
}

// --- Progress-based loop tests ---

func TestRunAuditLoopProgressStopsOnStale(t *testing.T) {
	t.Parallel()

	// High effort: same CRITICAL finding every time. Outer stale detection
	// should stop after 3 consecutive stale cycles.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), criticalFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterationsSet = false

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, "CRITICAL", result.MaxSeverity)

	// Cycle 1: baseline (no stale check on cycle 1).
	// Cycle 2: outer stale=1. Cycle 3: outer stale=2. Cycle 4: outer stale=3 → break before inner.
	// Cycles 1-3 each: audit + (fix+verify)*2 (inner stale) = 5
	// Cycle 4: audit only (breaks immediately after stale detection) = 1
	// No final pass: 3*5 + 1 = 16
	assert.Len(t, eng.prompts, 16)
}

func TestRunAuditLoopProgressContinues(t *testing.T) {
	t.Parallel()

	// Max effort with explicit cap: different findings each cycle → progress always made → runs full cap.
	// Each finding uses a distinct location so theme matching does not treat them as reopenings.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			desc := fmt.Sprintf("Unique issue %d", callIndex)
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile),
				fmt.Sprintf("## Findings\n- **Location:** src/module%d/handler.go:1\n- **Description:** %s\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n", callIndex, desc))
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortMax
	opts.Epic.MaxAuditIterationsSet = true
	opts.Epic.MaxAuditIterations = 20

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)

	// 20 explicit cycles, MaxInnerFixIterMax=10
	// Each cycle: audit + (fix+verify)*2 (inner stale since same content every call) = 5
	// Except: findings are DIFFERENT each time because callIndex varies.
	// The inner verify also writes unique findings, so verify parser finds no Issue/Status format.
	// Inner always stales after 2 fix attempts.
	// Outer: each cycle gets a different description (based on callIndex of the audit call),
	// so progress is detected (different finding keys).
	// 20 cycles * 5 calls = 100
	assert.Len(t, eng.prompts, 100)
}

func TestRunAuditLoopProgressStopsOnTurnoverChurnAfterWarmup(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			desc := fmt.Sprintf("Unique issue %d", callIndex)
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile),
				fmt.Sprintf("## Findings\n- **Location:** apps/web/src/components/booking/flow-%d.tsx:1\n- **Description:** %s\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n", callIndex, desc))
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortMax
	opts.Epic.MaxAuditIterationsSet = false

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)

	// Max effort adaptive churn detection now warms up earlier for large budgets.
	// With maxOuter=100 the warmup caps at 10, so cycles 11, 12, 13 become churn
	// 1, 2, 3 and the loop breaks before the inner loop on cycle 13.
	// Cycles 1-12: audit + (fix+verify)*2 = 5 calls each => 60
	// Cycle 13: audit only => 1
	// No final pass: 60 + 1 = 61
	assert.Len(t, eng.prompts, 61)
}

func TestRunAuditLoopProgressStopsOnLowYield(t *testing.T) {
	t.Parallel()

	const repeatedHighFindings = "## Findings\n- **Location:** src/first.go:10\n- **Description:** Missing booking consistency guard\n- **Severity:** HIGH\n- **Recommended Fix:** Add the missing guard and keep booking state transitions consistent.\n- **Location:** src/second.go:20\n- **Description:** Missing idempotency handling for duplicate submits\n- **Severity:** HIGH\n- **Recommended Fix:** Make duplicate submits idempotent.\n\n## Verdict\nFAIL\n"
	var secondCycleFixPrompt string

	// Call indices for EffortHigh (stopThreshold=3):
	// Cycle 1: audit(0) fix(1) verify(2) fix(3) verify(4)
	// Cycle 2: audit(5) fix(6) verify(7) fix(8) verify(9)
	// Cycle 3: audit(10) fix(11) verify(12) fix(13) verify(14)
	// No final pass — result determined from tracked findings.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			auditPath := filepath.Join(projectDir, config.SprintAuditFile)
			promptPath := filepath.Join(projectDir, config.AuditPromptFile)
			switch callIndex {
			case 0, 5, 10:
				writeFile(t, auditPath, repeatedHighFindings)
			case 1, 3, 6, 8, 11, 13:
				if callIndex == 6 {
					data, err := os.ReadFile(promptPath)
					require.NoError(t, err)
					secondCycleFixPrompt = string(data)
				}
				writeFile(t, filepath.Join(projectDir, "src/first.go"), fmt.Sprintf("package main\n\nconst attempt = %d\n", callIndex))
			case 2, 4:
				writeFile(t, auditPath,
					"- **Issue:** 1\n- **Status:** STILL_PRESENT\n\n- **Issue:** 2\n- **Status:** STILL_PRESENT\n")
			case 7, 9, 12, 14:
				writeFile(t, auditPath,
					"- **Issue:** 1\n- **Status:** STILL_PRESENT\n")
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Metrics)

	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, "HIGH", result.MaxSeverity)
	assert.Contains(t, result.StopReason, "low_yield")
	assert.Equal(t, result.StopReason, result.Metrics.LowYieldStopReason)
	assert.Equal(t, 2, result.Metrics.LowYieldStrategyChanges)
	assert.Equal(t, 3, result.Metrics.StrategyShiftCount())
	assert.Contains(t, result.Metrics.LastStrategyShift(), strategyTriggerLowYield)
	require.Len(t, result.Metrics.CycleSummaries, 3)
	assert.InDelta(t, 0.0, result.Metrics.CycleSummaries[0].FixYield, 0.001)
	assert.InDelta(t, 0.0, result.Metrics.CycleSummaries[1].VerifyYield, 0.001)
	assert.InDelta(t, 0.0, result.Metrics.CycleSummaries[2].VerifyYield, 0.001)
	assert.Len(t, eng.prompts, 15)
	assert.Contains(t, secondCycleFixPrompt, "### Issue 1")
	assert.NotContains(t, secondCycleFixPrompt, "### Issue 2")
}

func TestRunAuditLoopRecordsCachePressureStrategyShift(t *testing.T) {
	t.Parallel()

	// Both findings share the same file so they cluster together under per-cluster dispatch,
	// preserving the 4-call sequence (audit, fix-cluster, verify, re-audit).
	const findings = "## Findings\n- **Location:** tracked.txt:1\n- **Description:** Missing booking validation\n- **Severity:** HIGH\n- **Recommended Fix:** Validate the booking payload.\n- **Location:** tracked.txt:10\n- **Description:** Missing duplicate-submit protection\n- **Severity:** HIGH\n- **Recommended Fix:** Make submits idempotent.\n\n## Verdict\nFAIL\n"
	eng := &stubEngine{
		name: "claude",
		outputs: []string{
			`{"usage":{"input_tokens":2000,"cache_read_input_tokens":250000,"output_tokens":100}}`,
			`{"usage":{"input_tokens":50,"output_tokens":25}}`,
			`{"usage":{"input_tokens":40,"output_tokens":20}}`,
			`{"usage":{"input_tokens":30,"output_tokens":15}}`,
		},
		sideEffect: func(projectDir string, callIndex int) {
			switch callIndex {
			case 0:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), findings)
			case 1:
				writeFile(t, filepath.Join(projectDir, "tracked.txt"), "fixed\n")
			case 2:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), "- **Issue:** 1\n- **Status:** RESOLVED\n\n- **Issue:** 2\n- **Status:** RESOLVED\n")
			case 3:
				writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	initAuditGitRepo(t, opts.ProjectDir)
	opts.Epic.EffortLevel = epic.EffortHigh

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.NotNil(t, result.Metrics)
	assert.GreaterOrEqual(t, result.Metrics.StrategyShiftCount(), 1)
	assert.Contains(t, result.Metrics.LastStrategyShift(), strategyActionRefreshAuditSession)
	require.Len(t, result.Metrics.CycleSummaries, 2)
	assert.Equal(t, 250000, result.Metrics.CycleSummaries[0].CacheReadInput)
	assert.Equal(t, 2235, result.Metrics.CycleSummaries[0].TokenTotal)
}

func TestIsTurnoverChurn(t *testing.T) {
	t.Parallel()

	prevHigh := []Finding{{
		Location:    "apps/web/src/components/booking/a.tsx:10",
		Description: "Old high issue",
		Severity:    "HIGH",
	}}
	prevThreeModerate := []Finding{
		{Location: "apps/web/src/components/booking/a.tsx:10", Description: "A", Severity: "MODERATE"},
		{Location: "apps/web/src/components/booking/b.tsx:10", Description: "B", Severity: "MODERATE"},
		{Location: "apps/web/src/components/booking/c.tsx:10", Description: "C", Severity: "MODERATE"},
	}

	t.Run("same severity and count with full turnover is churn", func(t *testing.T) {
		current := []Finding{{
			Location:    "apps/web/src/components/booking/d.tsx:10",
			Description: "New high issue",
			Severity:    "HIGH",
		}}
		assert.True(t, isTurnoverChurn(prevHigh, nil, current, current))
	})

	t.Run("improved severity is not churn", func(t *testing.T) {
		current := []Finding{{
			Location:    "apps/web/src/components/booking/d.tsx:10",
			Description: "New moderate issue",
			Severity:    "MODERATE",
		}}
		assert.False(t, isTurnoverChurn(prevHigh, nil, current, current))
	})

	t.Run("fewer actionable findings is not churn", func(t *testing.T) {
		current := []Finding{{
			Location:    "apps/web/src/components/booking/d.tsx:10",
			Description: "Only one issue left",
			Severity:    "MODERATE",
		}}
		assert.False(t, isTurnoverChurn(prevThreeModerate, nil, current, current))
	})

	t.Run("persisting actionable findings is not churn", func(t *testing.T) {
		persisting := []Finding{prevHigh[0]}
		current := append([]Finding(nil), persisting...)
		assert.False(t, isTurnoverChurn(prevHigh, persisting, current, nil))
	})
}

func TestRunAuditLoopExplicitCapAtHighEffort(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), highFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 2
	opts.Epic.MaxAuditIterationsSet = true

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.True(t, result.Blocking)
	assert.Equal(t, 2, result.Iterations)
}

func TestRunAuditLoopMediumEffortBounded(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), moderateFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortStandard
	opts.Epic.MaxAuditIterations = 3

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.False(t, result.Blocking)
	assert.Equal(t, 3, result.Iterations)
}

// --- applyResolutionsByKey tests ---

func TestApplyResolutionsByKey(t *testing.T) {
	t.Parallel()

	all := []Finding{
		{Description: "Issue A", Severity: "HIGH"},
		{Description: "Issue B", Severity: "MODERATE"},
		{Description: "Issue C", Severity: "CRITICAL"},
	}
	checked := []Finding{
		{Description: "Issue A", Severity: "HIGH"},
		{Description: "Issue C", Severity: "CRITICAL"},
	}
	resolved := []verificationResult{{Status: verifyStatusResolved}, {Status: verifyStatusStillPresent}}

	applyResolutionsByKey(all, checked, resolved)

	assert.True(t, all[0].Resolved, "Issue A should be resolved")
	assert.False(t, all[1].Resolved, "Issue B was not checked")
	assert.False(t, all[2].Resolved, "Issue C was not resolved")
}

func TestApplyResolutionsByKeyUsesLocationAwareIdentity(t *testing.T) {
	t.Parallel()

	all := []Finding{
		{Location: "a.go:10", Description: "Duplicate issue", Severity: "HIGH"},
		{Location: "b.go:20", Description: "Duplicate issue", Severity: "HIGH"},
	}
	checked := []Finding{
		{Location: "a.go:10", Description: "Duplicate issue", Severity: "HIGH"},
	}

	applyResolutionsByKey(all, checked, []verificationResult{{Status: verifyStatusResolved}})

	assert.True(t, all[0].Resolved)
	assert.False(t, all[1].Resolved)
}

// --- findingKeySet tests ---

func TestFindingKeySet(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Location: "a.go:1", Description: "Active HIGH", Severity: "HIGH"},
		{Description: "Active MODERATE", Severity: "MODERATE"},
		{Description: "Low Issue", Severity: "LOW"},
		{Description: "Resolved", Severity: "HIGH", Resolved: true},
		{Description: "No Severity", Severity: ""},
	}

	keys := findingKeySet(findings)
	assert.Len(t, keys, 2)
	assert.Contains(t, keys, "a.go::active high")
	assert.Contains(t, keys, "active moderate")
}

func TestClassifyFindingsKeepsSameDescriptionDifferentLocationsDistinct(t *testing.T) {
	t.Parallel()

	known := []Finding{
		{Location: "a.go:10", Description: "Nil check missing", Severity: "HIGH", OriginCycle: 1},
		{Location: "b.go:20", Description: "Nil check missing", Severity: "HIGH", OriginCycle: 1},
	}
	current := []Finding{
		{Location: "b.go:20", Description: "Nil check missing", Severity: "HIGH"},
	}

	classification := classifyFindings(known, current)

	require.Len(t, classification.Resolved, 1)
	require.Len(t, classification.Persisting, 1)
	assert.Empty(t, classification.NewFindings)
	assert.Equal(t, "a.go:10", classification.Resolved[0].Location)
	assert.Equal(t, "b.go:20", classification.Persisting[0].Location)
}

func TestClassifyFindingsIgnoresLineNumberChurnForSameFile(t *testing.T) {
	t.Parallel()

	known := []Finding{
		{Location: "a.go:10", Description: "Nil check missing", Severity: "HIGH", OriginCycle: 1},
	}
	current := []Finding{
		{Location: "a.go:24", Description: "Nil check missing", Severity: "HIGH"},
	}

	classification := classifyFindings(known, current)

	assert.Empty(t, classification.Resolved)
	assert.Len(t, classification.Persisting, 1)
	assert.Empty(t, classification.NewFindings)
	assert.Equal(t, 1, classification.Persisting[0].OriginCycle)
}

// --- UnresolvedFindings in result test ---

func TestRunAuditLoopUnresolvedFindingsInResult(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), criticalFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 1

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed)
	assert.NotEmpty(t, result.UnresolvedFindings)
	assert.Equal(t, "Null pointer dereference", result.UnresolvedFindings[0].Description)
}

// --- filterFixable tests ---

func TestFilterFixable(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "Critical bug", Severity: "CRITICAL"},
		{Description: "High bug", Severity: "HIGH"},
		{Description: "Moderate issue", Severity: "MODERATE"},
		{Description: "Missing SUPABASE_URL", Severity: "HIGH", Category: FindingCategoryEnvironmentBlocker},
		{Description: "Low style", Severity: "LOW"},
		{Description: "No severity", Severity: ""},
		{Description: "Resolved high", Severity: "HIGH", Resolved: true},
		{Description: "Resolved low", Severity: "LOW", Resolved: true},
	}

	// Without LOW: same as filterUnresolved
	withoutLow := filterFixable(findings, false)
	assert.Len(t, withoutLow, 3)
	assert.Equal(t, "Critical bug", withoutLow[0].Description)
	assert.Equal(t, "High bug", withoutLow[1].Description)
	assert.Equal(t, "Moderate issue", withoutLow[2].Description)

	// With LOW: includes unresolved LOW
	withLow := filterFixable(findings, true)
	assert.Len(t, withLow, 4)
	assert.Equal(t, "Critical bug", withLow[0].Description)
	assert.Equal(t, "High bug", withLow[1].Description)
	assert.Equal(t, "Moderate issue", withLow[2].Description)
	assert.Equal(t, "Low style", withLow[3].Description)
}

func TestCountFixable(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "A", Severity: "HIGH"},
		{Description: "Harness blocker", Severity: "HIGH", Category: FindingCategoryHarnessBlocker},
		{Description: "B", Severity: "LOW"},
		{Description: "C", Severity: "MODERATE", Resolved: true},
		{Description: "D", Severity: "LOW", Resolved: true},
	}

	// countFixable counts ALL findings matching severity filter (resolved or not)
	// because it serves as the total denominator in progress logs, but it excludes blocker categories.
	assert.Equal(t, 2, countFixable(findings, false)) // HIGH + resolved MODERATE
	assert.Equal(t, 4, countFixable(findings, true))  // all four
}

func TestCountUnresolvedLow(t *testing.T) {
	t.Parallel()

	findings := []Finding{
		{Description: "A", Severity: "HIGH"},
		{Description: "B", Severity: "LOW"},
		{Description: "C", Severity: "LOW", Resolved: true},
		{Description: "D", Severity: "LOW"},
	}

	assert.Equal(t, 2, countUnresolvedLow(findings))
}

func TestFixIncludesLow(t *testing.T) {
	t.Parallel()

	assert.False(t, fixIncludesLow(&epic.Epic{EffortLevel: epic.EffortFast}))
	assert.False(t, fixIncludesLow(&epic.Epic{EffortLevel: epic.EffortStandard}))
	assert.False(t, fixIncludesLow(&epic.Epic{EffortLevel: ""}))
	assert.True(t, fixIncludesLow(&epic.Epic{EffortLevel: epic.EffortHigh}))
	assert.True(t, fixIncludesLow(&epic.Epic{EffortLevel: epic.EffortMax}))
}

// --- Effort-aware LOW fix integration tests ---

func TestRunAuditLoopHighEffortFixesLow(t *testing.T) {
	t.Parallel()

	// High effort: audit finds MODERATE + LOW. Fix agent should receive both.
	// Verify resolves both. Re-audit is clean → pass.
	mixedFindings := "## Findings\n- **Description:** Edge case gap\n- **Severity:** MODERATE\n- **Description:** Variable naming\n- **Severity:** LOW\n\n## Verdict\nFAIL\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit
				writeFile(t, path, mixedFindings)
			case 1: // fix 1 (no write needed)
			case 2: // verify 1 → both resolved
				writeFile(t, path,
					"- **Issue:** 1\n- **Status:** RESOLVED\n- **Issue:** 2\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 audit → clean
				writeFile(t, path, cleanAudit)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterationsSet = true
	opts.Epic.MaxAuditIterations = 5

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)

	// Fix prompt should have been called and should contain both issues
	require.True(t, len(eng.prompts) >= 2, "expected at least audit + fix prompts")
	// The fix agent prompt (index 1) is AuditFixInvocationPrompt
	assert.Equal(t, config.AuditFixInvocationPrompt, eng.prompts[1])
}

func TestRunAuditLoopMediumEffortIgnoresLow(t *testing.T) {
	t.Parallel()

	// Medium effort: audit finds MODERATE + LOW. Fix agent should receive only MODERATE.
	// Verify resolves it. Re-audit finds only LOW → pass (LOW-only = pass).
	mixedFindings := "## Findings\n- **Description:** Edge case gap\n- **Severity:** MODERATE\n- **Description:** Variable naming\n- **Severity:** LOW\n\n## Verdict\nFAIL\n"
	lowOnlyFindings := "## Findings\n- **Description:** Variable naming\n- **Severity:** LOW\n\n## Verdict\nPASS\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit
				writeFile(t, path, mixedFindings)
			case 1: // fix 1
			case 2: // verify 1 → MODERATE resolved
				writeFile(t, path,
					"- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 audit → only LOW remains
				writeFile(t, path, lowOnlyFindings)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortStandard
	opts.Epic.MaxAuditIterations = 5

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 2, result.Iterations)
}

func TestRunAuditLoopLowOnlyMaxEffortRunsOneFix(t *testing.T) {
	t.Parallel()

	lowOnlyFindings := "## Findings\n- **Description:** Variable naming\n- **Severity:** LOW\n\n## Verdict\nPASS\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit → LOW only
				writeFile(t, path, lowOnlyFindings)
			case 1: // fix pass (single LOW fix attempt)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortMax

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
	// Expect: audit + fix = 2 calls. No re-audit after fix.
	assert.Len(t, eng.prompts, 2)
	assert.Equal(t, config.AuditInvocationPrompt, eng.prompts[0])
	assert.Equal(t, config.AuditFixInvocationPrompt, eng.prompts[1])
}

func TestRunAuditLoopLowOnlyNonMaxExitsImmediately(t *testing.T) {
	t.Parallel()

	lowOnlyFindings := "## Findings\n- **Description:** Variable naming\n- **Severity:** LOW\n\n## Verdict\nPASS\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), lowOnlyFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortStandard

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, 1, result.Iterations)
	// Only audit called, no fix agent
	assert.Len(t, eng.prompts, 1)
	assert.Equal(t, config.AuditInvocationPrompt, eng.prompts[0])
}

func TestRunAuditLoopMaxEffortHighFindingsStillLoops(t *testing.T) {
	t.Parallel()

	// Max effort with MODERATE findings should loop normally, not exit early.
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			writeFile(t, filepath.Join(projectDir, config.SprintAuditFile), moderateFindings)
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortMax
	opts.Epic.MaxAuditIterationsSet = true
	opts.Epic.MaxAuditIterations = 2

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.False(t, result.Passed, "MODERATE findings should not pass")
}

func TestRunAuditLoopConvergesToLowPassesWithoutFinalPass(t *testing.T) {
	t.Parallel()

	// Cycle 1: HIGH finding. Fix+verify resolves it.
	// Cycle 2: only LOW remaining. Should PASS from tracked state.
	// No additional agent call should occur after cycle 2.
	lowOnlyFindings := "## Findings\n- **Description:** Minor naming convention\n- **Severity:** LOW\n\n## Verdict\nPASS\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, callIndex int) {
			path := filepath.Join(projectDir, config.SprintAuditFile)
			switch callIndex {
			case 0: // cycle 1 audit: HIGH
				writeFile(t, path, highFindings)
			case 2: // verify: resolved
				writeFile(t, path, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 audit: LOW only
				writeFile(t, path, lowOnlyFindings)
			}
		},
	}
	opts := makeOpts(t, eng)
	opts.Epic.MaxAuditIterations = 3

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed)
	assert.Equal(t, "LOW", result.MaxSeverity)
	assert.Equal(t, 2, result.Iterations)
	// Cycle 1: audit(0) + fix(1) + verify(2) = 3
	// Cycle 2: audit(3) → LOW only → immediate pass = 1
	// Total: 4 calls, no final pass
	assert.Len(t, eng.prompts, 4)
}

// --- Theme matching and reopen detection tests ---

func TestFileFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		location string
		want     string
	}{
		{"src/handler.go:42", "src/handler"},
		{"internal/api/server.go", "internal/api/server"},
		{"handler.go", "handler"},
		{"src/handler.go#L99C3", "src/handler"},
		{"src/handler.go:99:3", "src/handler"},
		{"", ""},
		{"  src/handler.go  ", "src/handler"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, fileFamily(tt.location), "fileFamily(%q)", tt.location)
	}
}

func TestDescriptionTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		want []string
	}{
		{
			"Stale slug canonicalization not implemented",
			[]string{"canonicalization", "implemented", "slug", "stale"},
		},
		{
			"The SQL injection is not handled properly",
			[]string{"handled", "injection", "properly", "sql"},
		},
		{
			"Missing slug canonicalization for stale entries",
			[]string{"canonicalization", "entries", "missing", "slug", "stale"},
		},
		{"", nil},
		{"a", nil},      // single char removed
		{"is the", nil}, // all stop words
	}
	for _, tt := range tests {
		got := descriptionTokens(tt.desc)
		assert.Equal(t, tt.want, got, "descriptionTokens(%q)", tt.desc)
	}
}

func TestJaccardSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b []string
		want float64
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 1.0},
		{[]string{"a", "b"}, []string{"c", "d"}, 0.0},
		{nil, nil, 0.0},
		{[]string{"a", "b", "c", "d"}, []string{"a", "b", "e", "f"}, 0.333},
		{[]string{"a", "b", "c"}, []string{"a", "b", "d"}, 0.5},
	}
	for _, tt := range tests {
		got := jaccardSimilarity(tt.a, tt.b)
		assert.InDelta(t, tt.want, got, 0.01, "jaccardSimilarity(%v, %v)", tt.a, tt.b)
	}
}

func TestThemeMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b Finding
		want bool
	}{
		{
			"same file family similar description",
			Finding{Location: "src/handler.go:10", Description: "SQL injection vulnerability in handler"},
			Finding{Location: "src/handler.go:50", Description: "SQL injection in request handler"},
			true,
		},
		{
			"different file families",
			Finding{Location: "src/auth.go:10", Description: "Missing validation"},
			Finding{Location: "src/payment.go:20", Description: "Missing validation"},
			false,
		},
		{
			"no location on both similar desc",
			Finding{Description: "Stale slug canonicalization not implemented"},
			Finding{Description: "Missing slug canonicalization for stale entries"},
			true,
		},
		{
			"same file family completely different desc",
			Finding{Location: "src/handler.go:10", Description: "SQL injection"},
			Finding{Location: "src/handler.go:50", Description: "Memory leak in goroutine pool"},
			false,
		},
		{
			"one has location other does not similar desc",
			Finding{Location: "src/handler.go:10", Description: "SQL injection vulnerability"},
			Finding{Description: "SQL injection vulnerability found"},
			true,
		},
		{
			"both empty",
			Finding{},
			Finding{},
			false, // empty descriptions have no tokens, jaccard returns 0
		},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, themeMatch(tt.a, tt.b), tt.name)
	}
}

func TestResolvedLedger(t *testing.T) {
	t.Parallel()

	ledger := newResolvedLedger()
	assert.Equal(t, 0, ledger.len())

	f1 := Finding{Location: "src/handler.go:10", Description: "SQL injection", Severity: "HIGH", OriginCycle: 1}
	f2 := Finding{Description: "Missing error handling", Severity: "MODERATE", OriginCycle: 1}

	ledger.add([]Finding{f1, f2})
	assert.Equal(t, 2, ledger.len())

	// Theme match should find f1
	match, ok := ledger.findThemeMatch(Finding{Location: "src/handler.go:50", Description: "SQL injection vulnerability in handler"})
	assert.True(t, ok)
	assert.Equal(t, f1.key(), match.key())

	// No match for unrelated finding
	_, ok = ledger.findThemeMatch(Finding{Location: "src/payment.go:10", Description: "Payment processing timeout"})
	assert.False(t, ok)
}

func TestClassifyReopenings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		resolved       []Finding
		newFindings    []Finding
		wantReopen     int
		wantNew        int
		wantReopenKeys []string // ReopenOf values on reopenings
	}{
		{
			"exact repeated finding is reopening",
			[]Finding{{Location: "src/handler.go:10", Description: "SQL injection", Severity: "HIGH", OriginCycle: 1}},
			[]Finding{{Location: "src/handler.go:50", Description: "SQL injection vulnerability in handler", Severity: "HIGH", OriginCycle: 2}},
			1, 0,
			[]string{"src/handler.go::sql injection"},
		},
		{
			"same theme different wording is reopening",
			[]Finding{{Description: "Stale slug canonicalization not implemented", Severity: "HIGH", OriginCycle: 1}},
			[]Finding{{Description: "Missing slug canonicalization for stale entries", Severity: "MODERATE", OriginCycle: 3}},
			1, 0, nil,
		},
		{
			"same theme genuine regression admitted",
			[]Finding{{Location: "src/api.go:10", Description: "Error handling incomplete", Severity: "MODERATE", OriginCycle: 1}},
			[]Finding{{Location: "src/api.go:10", Description: "Error handling completely absent", Severity: "HIGH", OriginCycle: 2}},
			0, 1, nil,
		},
		{
			"same area genuinely new issue",
			[]Finding{{Location: "src/handler.go:10", Description: "SQL injection", Severity: "HIGH", OriginCycle: 1}},
			[]Finding{{Location: "src/handler.go:50", Description: "CSRF token missing on form endpoint", Severity: "HIGH", OriginCycle: 2}},
			0, 1, nil,
		},
		{
			"same theme lower severity after fix is reopening",
			[]Finding{{Location: "src/handler.go:10", Description: "Null pointer crash in request handler", Severity: "HIGH", OriginCycle: 1}},
			[]Finding{{Location: "src/handler.go:20", Description: "Possible null pointer crash in handler edge case", Severity: "MODERATE", OriginCycle: 2}},
			1, 0, nil,
		},
		{
			"different file families are genuinely new",
			[]Finding{{Location: "src/auth.go:10", Description: "Missing input validation", Severity: "HIGH", OriginCycle: 1}},
			[]Finding{{Location: "src/payment.go:20", Description: "Missing input validation", Severity: "HIGH", OriginCycle: 2}},
			0, 1, nil,
		},
		{
			"empty ledger returns all as new",
			nil,
			[]Finding{{Description: "Some new finding", Severity: "HIGH", OriginCycle: 1}},
			0, 1, nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ledger := newResolvedLedger()
			ledger.add(tt.resolved)
			classification := classifyReopenings(tt.newFindings, ledger)
			assert.Equal(t, tt.wantReopen, len(classification.Suppressed), "reopening count")
			assert.Equal(t, tt.wantNew, len(classification.Admitted), "genuinely new count")
			if tt.wantReopenKeys != nil {
				for i, k := range tt.wantReopenKeys {
					assert.Equal(t, k, classification.Suppressed[i].ReopenOf, "ReopenOf key")
				}
			}
		})
	}
}

func TestClassifyReopeningsNilLedger(t *testing.T) {
	t.Parallel()

	findings := []Finding{{Description: "Some finding", Severity: "HIGH"}}
	classification := classifyReopenings(findings, nil)
	assert.Empty(t, classification.Suppressed)
	assert.Equal(t, findings, classification.Admitted)
}

func TestAuditPromptIncludesResolvedThemes(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	ledger := newResolvedLedger()
	ledger.add([]Finding{
		{Location: "src/handler.go:10", Description: "SQL injection", Severity: "HIGH", OriginCycle: 1},
		{Description: "Missing validation", Severity: "MODERATE", OriginCycle: 2},
	})

	prompt := buildAuditPrompt(opts, nil, ledger)

	assert.Contains(t, prompt, "## Resolved Themes (Do Not Reopen)")
	assert.Contains(t, prompt, "SQL injection")
	assert.Contains(t, prompt, "Missing validation")
	assert.Contains(t, prompt, "automatically suppressed")
}

func TestAuditPromptNoResolvedThemesOnCycle1(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prompt := buildAuditPrompt(opts, nil, nil)

	assert.NotContains(t, prompt, "## Resolved Themes")
}

func TestAuditPromptAntiReopenInstruction(t *testing.T) {
	t.Parallel()

	opts := makeOpts(t, &stubEngine{name: "codex"})
	prompt := buildAuditPrompt(opts, nil, nil)

	assert.Contains(t, prompt, "previously resolved issue seems to recur under different wording")
	assert.Contains(t, prompt, "New Evidence")
}

func TestRunAuditLoopSuppressesReopenings(t *testing.T) {
	t.Parallel()

	// Cycle 1: finds HIGH issue "SQL injection in handler"
	// Inner fix: resolves it
	// Cycle 2 re-audit: same theme different wording -> should be suppressed, pass
	findingsReport := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/handler.go:10\n- **Description:** SQL injection in request handler\n- **Severity:** HIGH\n- **Recommended Fix:** Use parameterized queries\n\n## Verdict\nFAIL\n"
	reopenedReport := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/handler.go:50\n- **Description:** SQL injection vulnerability in handler endpoint\n- **Severity:** HIGH\n- **Recommended Fix:** Sanitize inputs\n\n## Verdict\nFAIL\n"

	callCount := 0
	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, idx int) {
			callCount++
			auditFile := filepath.Join(projectDir, config.SprintAuditFile)
			switch idx {
			case 0: // cycle 1 audit
				writeFile(t, auditFile, findingsReport)
			case 1: // cycle 1 fix — no file needed
			case 2: // cycle 1 verify
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 re-audit — same theme, different wording
				writeFile(t, auditFile, reopenedReport)
			default: // cycle 2 should detect reopening and suppress; then final pass
				writeFile(t, auditFile, cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 5
	opts.Epic.MaxAuditIterationsSet = true

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, result.Passed, "should pass after reopening is suppressed")
	assert.Equal(t, 1, result.SuppressedReopenings, "one reopening should be suppressed")
}

func TestRunAuditLoopAllowsUnchangedReopeningWithNewEvidence(t *testing.T) {
	t.Parallel()

	findingsReport := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/handler.go:10\n- **Description:** SQL injection in request handler\n- **Severity:** HIGH\n- **Recommended Fix:** Use parameterized queries\n\n## Verdict\nFAIL\n"
	reopenedWithEvidence := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/handler.go:10\n- **Description:** SQL injection still present in request handler\n- **Severity:** HIGH\n- **Recommended Fix:** Remove string-built queries\n- **New Evidence:** The prior fix only covered the list endpoint; this unchanged handler path still concatenates SQL directly.\n\n## Verdict\nFAIL\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, idx int) {
			auditFile := filepath.Join(projectDir, config.SprintAuditFile)
			switch idx {
			case 0:
				writeFile(t, auditFile, findingsReport)
			case 1:
			case 2:
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3:
				writeFile(t, auditFile, reopenedWithEvidence)
			case 4:
			case 5:
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n- **Notes:** uncovered endpoint now uses parameterized queries too\n")
			default:
				writeFile(t, auditFile, cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 5
	opts.Epic.MaxAuditIterationsSet = true

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	assert.Equal(t, 1, result.ReopenedWithEvidence)
	require.NotNil(t, result.Metrics)
	assert.Equal(t, 1, result.Metrics.Snapshot().ReopenedWithNewEvidence)
}

func TestRunAuditLoopAllowsGenuineRegression(t *testing.T) {
	t.Parallel()

	// Cycle 1: MODERATE issue
	// Fix resolves it
	// Cycle 2: same theme at HIGH severity -> should be admitted as genuine regression
	moderateReport := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/api.go:10\n- **Description:** Error handling incomplete in API\n- **Severity:** MODERATE\n- **Recommended Fix:** Add error checks\n\n## Verdict\nFAIL\n"
	highReport := "## Summary\nIssues found.\n\n## Findings\n- **Location:** src/api.go:10\n- **Description:** Error handling completely absent in API\n- **Severity:** HIGH\n- **Recommended Fix:** Implement full error handling\n\n## Verdict\nFAIL\n"

	eng := &stubEngine{
		name: "codex",
		sideEffect: func(projectDir string, idx int) {
			auditFile := filepath.Join(projectDir, config.SprintAuditFile)
			apiPath := filepath.Join(projectDir, "src", "api.go")
			switch idx {
			case 0: // cycle 1 audit
				require.NoError(t, os.MkdirAll(filepath.Dir(apiPath), 0o755))
				writeFile(t, apiPath, "package src\n\nfunc call() error {\n\tif err := work(); err != nil {\n\t\treturn nil\n\t}\n\treturn nil\n}\n")
				writeFile(t, auditFile, moderateReport)
			case 1: // cycle 1 fix
				writeFile(t, apiPath, "package src\n\nfunc call() error {\n\tif err := work(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n")
			case 2: // cycle 1 verify
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3: // cycle 2 re-audit — same theme but escalated severity
				writeFile(t, apiPath, "package src\n\nfunc call() error {\n\t_ = work()\n\treturn nil\n}\n")
				writeFile(t, auditFile, highReport)
			default:
				writeFile(t, auditFile, cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 3
	opts.Epic.MaxAuditIterationsSet = true

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	// The escalated finding should NOT be suppressed — audit should still have it
	assert.Equal(t, 0, result.SuppressedReopenings, "escalated severity should not be suppressed")
}

func TestRunAuditLoopUsesSameRoleSessionContinuity(t *testing.T) {
	t.Parallel()

	const auditSessionID = "019d5066-9bd1-7cb1-b421-e73a9d1d0f67"
	const fixSessionID = "019d5066-f512-7bc1-aba8-e45cf2fb9a84"

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			fmt.Sprintf("{\"type\":\"thread.started\",\"thread_id\":\"%s\"}\n{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"audit\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n", auditSessionID),
			fmt.Sprintf("{\"type\":\"thread.started\",\"thread_id\":\"%s\"}\n{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"fix\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":8,\"output_tokens\":4}}\n", fixSessionID),
			"verify one",
			"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"fix retry\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n",
			"verify two",
			"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"audit clean\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":6,\"output_tokens\":2}}\n",
		},
		sideEffect: func(projectDir string, idx int) {
			auditFile := filepath.Join(projectDir, config.SprintAuditFile)
			switch idx {
			case 0:
				writeFile(t, auditFile, highFindings)
			case 2:
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** STILL PRESENT\n")
			case 4:
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 5:
				writeFile(t, auditFile, cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 3
	opts.Epic.MaxAuditIterationsSet = true

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Len(t, eng.runOpts, 6)

	assert.True(t, eng.runOpts[0].StructuredOutput)
	assert.Equal(t, "", eng.runOpts[0].SessionID)
	assert.True(t, eng.runOpts[1].StructuredOutput)
	assert.Equal(t, "", eng.runOpts[1].SessionID)
	assert.False(t, eng.runOpts[2].StructuredOutput)
	assert.Equal(t, "", eng.runOpts[2].SessionID)
	assert.True(t, eng.runOpts[3].StructuredOutput)
	assert.Equal(t, fixSessionID, eng.runOpts[3].SessionID)
	assert.False(t, eng.runOpts[4].StructuredOutput)
	assert.Equal(t, "", eng.runOpts[4].SessionID)
	assert.True(t, eng.runOpts[5].StructuredOutput)
	assert.Equal(t, auditSessionID, eng.runOpts[5].SessionID)

	assert.NoFileExists(t, auditSessionPath(opts.ProjectDir, opts.Sprint.Number))
	assert.NoFileExists(t, fixSessionPath(opts.ProjectDir, opts.Sprint.Number, 1))
}

func TestRunAuditLoopRefreshesSameRoleSessionsWhenBudgetExceeded(t *testing.T) {
	t.Parallel()

	const auditSessionID = "019d5066-9bd1-7cb1-b421-e73a9d1d0f67"
	const fixSessionID = "019d5066-f512-7bc1-aba8-e45cf2fb9a84"

	eng := &stubEngine{
		name: "codex",
		outputs: []string{
			fmt.Sprintf("{\"type\":\"thread.started\",\"thread_id\":\"%s\"}\n{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"audit 1\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n", auditSessionID),
			fmt.Sprintf("{\"type\":\"thread.started\",\"thread_id\":\"%s\"}\n{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"fix 1\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":8,\"output_tokens\":4}}\n", fixSessionID),
			"verify one",
			"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"audit clean\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":6,\"output_tokens\":2}}\n",
		},
		sideEffect: func(projectDir string, idx int) {
			auditFile := filepath.Join(projectDir, config.SprintAuditFile)
			switch idx {
			case 0:
				writeFile(t, auditFile, highFindings)
			case 2:
				writeFile(t, auditFile, "- **Issue:** 1\n- **Status:** RESOLVED\n")
			case 3:
				writeFile(t, auditFile, cleanAudit)
			}
		},
	}

	opts := makeOpts(t, eng)
	opts.Epic.EffortLevel = epic.EffortHigh
	opts.Epic.MaxAuditIterations = 3
	opts.Epic.MaxAuditIterationsSet = true
	opts.Sprint.Prompt = strings.Repeat("x", config.FixSessionMaxPromptBytes+2_000)

	result, err := RunAuditLoop(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, result.Passed)
	require.Len(t, eng.runOpts, 4)

	assert.Equal(t, "", eng.runOpts[0].SessionID)
	assert.Equal(t, "", eng.runOpts[1].SessionID)
	assert.Equal(t, "", eng.runOpts[3].SessionID)
	assert.Equal(t, 1, result.Metrics.SessionRefreshes)
	assert.Equal(t, 1, result.Metrics.Snapshot().SessionRefreshes)

	promptBytes, err := os.ReadFile(filepath.Join(opts.ProjectDir, config.AuditPromptFile))
	require.NoError(t, err)
	prompt := string(promptBytes)
	assert.Contains(t, prompt, "## Session Refresh Summary")
	assert.Contains(t, prompt, "prompt budget reached")
	assert.Contains(t, prompt, "Missing error handling")
}
