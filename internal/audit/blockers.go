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

func (f Finding) categoryOrDefault() string {
	category := normalizeFindingCategory(f.Category)
	// Only trust explicit categories set by the audit agent.
	// Inference from description keywords is too prone to false positives
	// (e.g. "Dockerfile copies devDependencies" matching "docker" → harness_blocker).
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
	// Blocker categories are informational — they help the user understand
	// the nature of the finding but do not prevent the fix agent from
	// attempting remediation. The fix agent may not be able to fix
	// environment issues, but that is handled by normal convergence
	// (stale detection, low-yield stop) rather than pre-filtering.
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
