package codereview

import (
	"regexp"
	"strings"
)

type ComplexityTier string

const (
	ComplexityUnknown  ComplexityTier = "unknown"
	ComplexityLow      ComplexityTier = "low"
	ComplexityModerate ComplexityTier = "moderate"
	ComplexityHigh     ComplexityTier = "high"
)

var (
	numericTokenRe    = regexp.MustCompile(`\b\d[\d,.]*\b`)
	currencyTokenRe   = regexp.MustCompile(`\$[\d,.]+|\b\d+(?:\.\d+)?\s*(?:USD|EUR|GBP)\b`)
	percentageTokenRe = regexp.MustCompile(`\b\d+(?:\.\d+)?\s*%`)
	tableRowRe        = regexp.MustCompile(`\|.*\d.*\|`)
	softwareMetricRe  = regexp.MustCompile(`(?i)\b(?:ns/op|allocs/op|MB/s|GB/s|qps|rps|latency|throughput|benchmark|p95|p99|timeout|ttl|retries|workers|pool size|concurrency)\b`)
)

// ClassifyComplexity applies fast heuristics to the sprint diff to decide whether
// review prompting and budgets should treat the sprint as low, moderate, or high complexity.
func ClassifyComplexity(diffContent string, mode string) ComplexityTier {
	diffContent = strings.TrimSpace(diffContent)
	if diffContent == "" {
		return ComplexityLow
	}

	lower := strings.ToLower(diffContent)
	if strings.Contains(lower, "git diff unavailable") {
		return ComplexityUnknown
	}

	tokens := strings.Fields(diffContent)
	if len(tokens) == 0 {
		return ComplexityLow
	}

	lines := strings.Split(diffContent, "\n")
	totalLines := len(lines)
	if totalLines == 0 {
		totalLines = 1
	}

	numericCount := len(numericTokenRe.FindAllString(diffContent, -1))
	signalCount := len(currencyTokenRe.FindAllString(diffContent, -1)) + len(percentageTokenRe.FindAllString(diffContent, -1))
	if strings.EqualFold(mode, "software") {
		signalCount = len(softwareMetricRe.FindAllString(diffContent, -1))
	}
	tableRows := len(tableRowRe.FindAllString(diffContent, -1))

	numericRatio := float64(numericCount+signalCount) / float64(len(tokens))
	tableDensity := float64(tableRows) / float64(totalLines)

	switch {
	case numericRatio > 0.15 || tableDensity > 0.10 || signalCount > 20:
		return ComplexityHigh
	case numericRatio > 0.05 || tableDensity > 0.03 || signalCount > 5:
		return ComplexityModerate
	default:
		return ComplexityLow
	}
}
