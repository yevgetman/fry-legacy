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

	// Review metadata regexes.
	iterationsRe   = regexp.MustCompile(`(?im)^-?\s*Iterations\s+completed:\s*(\d+)`)
	convergenceRe  = regexp.MustCompile(`(?im)^-?\s*Convergence:\s*(CONVERGED|ITERATION_LIMIT)`)
	passHeaderRe   = regexp.MustCompile(`(?im)^###\s+Pass\s+(\d+)`)
	passFoundRe    = regexp.MustCompile(`(?im)^Found:\s*(.+)`)
	passFixedRe    = regexp.MustCompile(`(?im)^Fixed:\s*(.+)`)
	passSevCountRe = regexp.MustCompile(`(\d+)\s+(CRITICAL|HIGH|MODERATE|LOW)`)
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

// parseReviewMetadata extracts the structured metadata section from the review
// output. Returns nil if the metadata section is absent or unparseable.
func parseReviewMetadata(content string) *ReviewMetadata {
	iterMatch := iterationsRe.FindStringSubmatch(content)
	convMatch := convergenceRe.FindStringSubmatch(content)

	if len(iterMatch) < 2 && len(convMatch) < 2 {
		return nil
	}

	meta := &ReviewMetadata{}
	if len(iterMatch) >= 2 {
		meta.IterationsCompleted = atoiParse(iterMatch[1])
	}
	if len(convMatch) >= 2 {
		meta.Convergence = ConvergenceStatus(convMatch[1])
	}
	meta.History = parseReviewHistory(content)
	return meta
}

// parseReviewHistory extracts per-pass Found/Fixed counts from the Review
// History section.
func parseReviewHistory(content string) []ReviewPass {
	passMatches := passHeaderRe.FindAllStringSubmatchIndex(content, -1)
	if len(passMatches) == 0 {
		return nil
	}

	var passes []ReviewPass
	for i, match := range passMatches {
		end := len(content)
		if i+1 < len(passMatches) {
			end = passMatches[i+1][0]
		}
		section := content[match[0]:end]
		passNum := atoiParse(content[match[2]:match[3]])

		pass := ReviewPass{
			PassNumber:  passNum,
			FoundCounts: make(map[string]int),
			FixedCounts: make(map[string]int),
		}

		if m := passFoundRe.FindStringSubmatch(section); len(m) >= 2 {
			pass.FoundCounts = parseSeverityCounts(m[1])
		}
		if m := passFixedRe.FindStringSubmatch(section); len(m) >= 2 {
			pass.FixedCounts = parseSeverityCounts(m[1])
		}

		passes = append(passes, pass)
	}
	return passes
}

// parseSeverityCounts extracts severity counts from a line like "2 CRITICAL, 1 HIGH, 0 MODERATE".
func parseSeverityCounts(line string) map[string]int {
	counts := make(map[string]int)
	for _, m := range passSevCountRe.FindAllStringSubmatch(line, -1) {
		if len(m) >= 3 {
			counts[m[2]] = atoiParse(m[1])
		}
	}
	return counts
}

// atoiParse is a simple non-negative integer parser.
func atoiParse(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// findingKey returns a normalized deduplication key for a finding.
func findingKey(f Finding) string {
	desc := normalizeFindingDescription(f.Description)
	loc := normalizeFindingLocation(f.Location)
	if loc == "" {
		return desc
	}
	if desc == "" {
		return loc
	}
	return loc + "::" + desc
}

// deduplicateFindings removes duplicate findings based on normalized
// location+description keys. When duplicates exist, the higher severity is
// retained. The input slice is not modified.
func deduplicateFindings(findings []Finding) []Finding {
	// First pass: compute the winning severity for each key.
	winningSeverity := make(map[string]string, len(findings))
	for _, f := range findings {
		key := findingKey(f)
		if key == "" {
			continue
		}
		if existing, ok := winningSeverity[key]; !ok || severity.Rank(f.Severity) > severity.Rank(existing) {
			winningSeverity[key] = f.Severity
		}
	}

	// Second pass: emit each key once (first occurrence), in input order,
	// applying the winning severity. Findings with empty keys pass through.
	deduped := make([]Finding, 0, len(findings))
	emitted := make(map[string]struct{}, len(winningSeverity))
	for _, f := range findings {
		key := findingKey(f)
		if key == "" {
			deduped = append(deduped, f)
			continue
		}
		if _, done := emitted[key]; done {
			continue
		}
		emitted[key] = struct{}{}
		if winner, ok := winningSeverity[key]; ok {
			f.Severity = winner
		}
		deduped = append(deduped, f)
	}
	return deduped
}
