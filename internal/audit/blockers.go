package audit

import (
	"regexp"
	"strings"
)

const (
	FindingCategoryProductDefect             = "product_defect"
	FindingCategoryEnvironmentBlocker        = "environment_blocker"
	FindingCategoryHarnessBlocker            = "harness_blocker"
	FindingCategoryExternalDependencyBlocker = "external_dependency_blocker"
)

var (
	categoryRe       = regexp.MustCompile(`(?i)\*?\*?Category:\*?\*?\s*(.+)`)
	blockerDetailsRe = regexp.MustCompile(`(?i)\*?\*?Blocker\s*Details:\*?\*?\s*(.+)`)

	envBlockerHints = []string{
		"env var", "environment variable", "missing secret", "missing credential", "missing credentials",
		"supabase_", "api key", "service key", "database url", ".env", "bootstrap prerequisite",
	}
	harnessBlockerHints = []string{
		"docker", "testcontainers", "harness", "fixture", "bootstrap failed", "test bootstrap",
		"localstack", "emulator", "cannot start test", "ci runtime",
	}
	externalDependencyHints = []string{
		"external service", "third-party", "dependency unavailable", "service unavailable",
		"network unreachable", "dns failure", "rate limit", "quota exceeded", "upstream",
	}
)

func normalizeFindingCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case FindingCategoryProductDefect, "":
		return FindingCategoryProductDefect
	case FindingCategoryEnvironmentBlocker:
		return FindingCategoryEnvironmentBlocker
	case FindingCategoryHarnessBlocker:
		return FindingCategoryHarnessBlocker
	case FindingCategoryExternalDependencyBlocker:
		return FindingCategoryExternalDependencyBlocker
	default:
		return FindingCategoryProductDefect
	}
}

func inferFindingCategory(f Finding) string {
	text := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(strings.Join([]string{
		f.Description,
		f.BlockerDetails,
		f.RecommendedFix,
	}, " "))), " "))
	if text == "" {
		return FindingCategoryProductDefect
	}
	if containsAnyHint(text, envBlockerHints) {
		return FindingCategoryEnvironmentBlocker
	}
	if containsAnyHint(text, harnessBlockerHints) {
		return FindingCategoryHarnessBlocker
	}
	if containsAnyHint(text, externalDependencyHints) {
		return FindingCategoryExternalDependencyBlocker
	}
	return FindingCategoryProductDefect
}

func containsAnyHint(text string, hints []string) bool {
	for _, hint := range hints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

func (f Finding) categoryOrDefault() string {
	category := normalizeFindingCategory(f.Category)
	if category == FindingCategoryProductDefect && strings.TrimSpace(f.Category) == "" {
		return inferFindingCategory(f)
	}
	return category
}

func (f Finding) isBlocker() bool {
	switch f.categoryOrDefault() {
	case FindingCategoryEnvironmentBlocker, FindingCategoryHarnessBlocker, FindingCategoryExternalDependencyBlocker:
		return true
	default:
		return false
	}
}

func (f Finding) isFixableProductDefect(includeLow bool) bool {
	if f.Resolved || f.Severity == "" {
		return false
	}
	if f.isBlocker() {
		return false
	}
	if !includeLow && f.Severity == "LOW" {
		return false
	}
	return true
}

func blockerCounts(findings []Finding) map[string]int {
	counts := make(map[string]int)
	for _, finding := range findings {
		if !finding.isBlocker() || finding.Resolved {
			continue
		}
		counts[finding.categoryOrDefault()]++
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

func filterBlockers(findings []Finding) []Finding {
	var blockers []Finding
	for _, finding := range findings {
		if finding.isBlocker() && !finding.Resolved {
			blockers = append(blockers, finding)
		}
	}
	return blockers
}

func filterFixableProductFindings(findings []Finding, includeLow bool) []Finding {
	var product []Finding
	for _, finding := range findings {
		if finding.isFixableProductDefect(includeLow) {
			product = append(product, finding)
		}
	}
	return product
}

func countFixableProductFindings(findings []Finding, includeLow bool) int {
	return len(filterFixableProductFindings(findings, includeLow))
}
