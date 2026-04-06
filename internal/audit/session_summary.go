package audit

import (
	"fmt"
	"strings"
)

func buildSessionCarryForwardSummary(reason string, findings []Finding, history *FixHistory) string {
	reason = strings.TrimSpace(reason)
	if reason == "" && len(findings) == 0 {
		return ""
	}

	var b strings.Builder
	if reason != "" {
		fmt.Fprintf(&b, "Session refreshed because %s.\n", reason)
	}
	if len(findings) > 0 {
		b.WriteString("Unresolved findings carried forward:\n")
		for i, finding := range findings {
			label := findingLabel(finding)
			if label == "" {
				label = finding.Description
			}
			fmt.Fprintf(&b, "- %s [%s]", label, finding.Severity)
			if finding.LastSeenCycle > 0 {
				fmt.Fprintf(&b, " last_seen_cycle=%d", finding.LastSeenCycle)
			}
			b.WriteString("\n")
			if i >= 5 {
				b.WriteString("- ...(additional findings omitted)\n")
				break
			}
		}
	}
	historyText := ""
	if history != nil {
		historyText = strings.TrimSpace(history.ForPrompt(findings, 1200))
	}
	if historyText != "" {
		b.WriteString("\nRecent failed fix attempts:\n")
		b.WriteString(historyText)
	}
	return strings.TrimSpace(b.String())
}
