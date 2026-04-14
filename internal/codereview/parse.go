package codereview

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/yevgetman/fry/internal/severity"
)

// Regexes for structured finding fields.
var (
	locationRe       = regexp.MustCompile(`(?i)\*?\*?Location:\*?\*?\s*(.+)`)
	descriptionRe    = regexp.MustCompile(`(?i)\*?\*?Description:\*?\*?\s*(.+)`)
	recommendedFixRe = regexp.MustCompile(`(?i)\*?\*?Recommended\s*Fix:\*?\*?\s*(.+)`)
	severityLabelRe  = regexp.MustCompile(`(?i)\bseverity\b`)
	severityWordRe   = regexp.MustCompile(`\b(CRITICAL|HIGH|MODERATE|LOW)\b`)
	categoryRe       = regexp.MustCompile(`(?i)\*?\*?Category:\*?\*?\s*(.+)`)

	locationHashLineRe  = regexp.MustCompile(`(?i)#l\d+(?:c\d+)?$`)
	locationColonLineRe = regexp.MustCompile(`:\d+(?::\d+)?$`)
)

var severityOrder = []string{"CRITICAL", "HIGH", "MODERATE", "LOW"}

// parseFindings extracts structured findings from review output. Each finding
// is delimited by a new Location or Description line. Findings without a
// Description are discarded.
func parseFindings(content string) []Finding {
	var findings []Finding
	var current Finding
	hasCurrent := false

	emit := func() {
		if hasCurrent && strings.TrimSpace(current.Description) != "" {
			findings = append(findings, current)
		}
	}

	for _, line := range strings.Split(content, "\n") {
		if m := locationRe.FindStringSubmatch(line); len(m) >= 2 {
			emit()
			current = Finding{Location: strings.TrimSpace(m[1])}
			hasCurrent = true
			continue
		}

		if m := descriptionRe.FindStringSubmatch(line); len(m) >= 2 {
			if hasCurrent && strings.TrimSpace(current.Description) != "" {
				emit()
				current = Finding{}
			}
			if !hasCurrent {
				current = Finding{}
				hasCurrent = true
			}
			current.Description = strings.TrimSpace(m[1])
			continue
		}

		if hasCurrent && severityLabelRe.MatchString(line) {
			upper := strings.ToUpper(line)
			if m := severityWordRe.FindString(upper); m != "" {
				current.Severity = m
			}
			continue
		}

		if hasCurrent {
			if m := categoryRe.FindStringSubmatch(line); len(m) >= 2 {
				current.Category = normalizeFindingCategory(strings.TrimSpace(m[1]))
				continue
			}
		}

		if hasCurrent {
			if m := recommendedFixRe.FindStringSubmatch(line); len(m) >= 2 {
				current.RecommendedFix = strings.TrimSpace(m[1])
				continue
			}
		}
	}

	emit()
	return findings
}

func parseReviewSeverity(content string) string {
	maxSev := ""
	for _, line := range strings.Split(content, "\n") {
		if !severityLabelRe.MatchString(line) {
			continue
		}
		upper := strings.ToUpper(line)
		m := severityWordRe.FindString(upper)
		if m == "" {
			continue
		}
		if severity.Rank(m) > severity.Rank(maxSev) {
			maxSev = m
		}
		if maxSev == "CRITICAL" {
			return "CRITICAL"
		}
	}
	return maxSev
}

func countSeverities(findings []Finding) map[string]int {
	counts := make(map[string]int)
	for _, f := range findings {
		if f.Severity != "" {
			counts[f.Severity]++
		}
	}
	return counts
}

func maxFindingSeverity(findings []Finding) string {
	maxSev := ""
	for _, f := range findings {
		if severity.Rank(f.Severity) > severity.Rank(maxSev) {
			maxSev = f.Severity
		}
	}
	return maxSev
}

func isReviewPass(maxSeverity string) bool {
	return maxSeverity == "" || maxSeverity == "LOW"
}

func isBlockingSeverity(maxSeverity string) bool {
	return maxSeverity == "CRITICAL" || maxSeverity == "HIGH"
}

// FormatCounts formats a severity count map for display.
func FormatCounts(counts map[string]int) string {
	var parts []string
	for _, sev := range severityOrder {
		if n, ok := counts[sev]; ok && n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func normalizeFindingDescription(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func normalizeFindingLocation(value string) string {
	value = strings.TrimSpace(value)
	value = locationHashLineRe.ReplaceAllString(value, "")
	value = locationColonLineRe.ReplaceAllString(value, "")
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

const (
	FindingCategoryProductDefect             = "product_defect"
	FindingCategoryEnvironmentBlocker        = "environment_blocker"
	FindingCategoryHarnessBlocker            = "harness_blocker"
	FindingCategoryExternalDependencyBlocker = "external_dependency_blocker"
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

// looksLikeFilePath returns true if s is plausibly a POSIX file path.
func looksLikeFilePath(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r == '(', r == ')', r == ',', r == '"', r == '\'':
			return false
		case unicode.IsSpace(r):
			return false
		}
	}
	return true
}
