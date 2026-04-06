package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yevgetman/fry/internal/textutil"
)

const (
	verifyStatusResolved             = "RESOLVED"
	verifyStatusPartiallyResolved    = "PARTIALLY_RESOLVED"
	verifyStatusStillPresent         = "STILL_PRESENT"
	verifyStatusBehaviorUnchanged    = "BEHAVIOR_UNCHANGED"
	verifyStatusEvidenceInconclusive = "EVIDENCE_INCONCLUSIVE"
	verifyStatusBlocked              = "BLOCKED"
	verifyStatusNoOp                 = "NO-OP"
)

type verificationResult struct {
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

type FixAttempt struct {
	Cycle       int          `json:"cycle"`
	Iteration   int          `json:"iteration"`
	Targeted    []string     `json:"targeted"`
	DiffSummary string       `json:"diff_summary"`
	Outcomes    []FixOutcome `json:"outcomes"`
}

type FixOutcome struct {
	FindingKey string `json:"finding_key"`
	Label      string `json:"label"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type FixHistory struct {
	attempts []FixAttempt
}

type BehaviorUnchangedSignal struct {
	FindingKey string
	Label      string
	Count      int
	LatestNote string
}

func (h *FixHistory) Record(a FixAttempt) {
	if h == nil {
		return
	}
	if len(a.Targeted) == 0 && len(a.Outcomes) == 0 {
		return
	}
	h.attempts = append(h.attempts, a)
}

func (h *FixHistory) PruneResolved(active []Finding) {
	if h == nil || len(h.attempts) == 0 {
		return
	}

	activeKeys := findingSet(active)
	var pruned []FixAttempt
	for _, attempt := range h.attempts {
		filtered := attempt.Outcomes[:0]
		for _, outcome := range attempt.Outcomes {
			if _, ok := activeKeys[outcome.FindingKey]; ok {
				filtered = append(filtered, outcome)
			}
		}
		if len(filtered) == 0 {
			continue
		}

		attempt.Outcomes = append([]FixOutcome(nil), filtered...)
		attempt.Targeted = targetedLabels(attempt.Outcomes)
		pruned = append(pruned, attempt)
	}
	h.attempts = pruned
}

func (h *FixHistory) ForPrompt(findings []Finding, maxBytes int) string {
	if h == nil || len(h.attempts) == 0 || len(findings) == 0 {
		return ""
	}

	relevantKeys := findingSet(findings)
	var relevant []FixAttempt
	for _, attempt := range h.attempts {
		if attemptTargetsRelevantFinding(attempt, relevantKeys) {
			relevant = append(relevant, attempt)
		}
	}
	if len(relevant) == 0 {
		return ""
	}

	rendered := renderFixAttempts(relevant)
	for len(rendered) > maxBytes && len(relevant) > 1 {
		relevant = relevant[1:]
		rendered = renderFixAttempts(relevant)
	}
	if len(rendered) > maxBytes {
		rendered = textutil.TruncateUTF8(rendered, maxBytes) + "\n...(older attempts omitted)\n"
	}
	return rendered
}

func attemptTargetsRelevantFinding(attempt FixAttempt, relevantKeys map[string]struct{}) bool {
	for _, outcome := range attempt.Outcomes {
		if _, ok := relevantKeys[outcome.FindingKey]; ok {
			return true
		}
	}
	return false
}

func renderFixAttempts(attempts []FixAttempt) string {
	var b strings.Builder
	for i, attempt := range attempts {
		fmt.Fprintf(&b, "### Attempt %d (cycle %d, iteration %d)\n", i+1, attempt.Cycle, attempt.Iteration)
		if len(attempt.Targeted) > 0 {
			fmt.Fprintf(&b, "- Targeted: %s\n", strings.Join(attempt.Targeted, ", "))
		}
		diffSummary := strings.TrimSpace(attempt.DiffSummary)
		if diffSummary == "" {
			diffSummary = "no changes"
		}
		fmt.Fprintf(&b, "- Changes: %s\n", diffSummary)
		b.WriteString("- Result: ")
		for j, outcome := range attempt.Outcomes {
			if j > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(&b, "%s -> %s", outcome.Label, outcome.Status)
			if strings.TrimSpace(outcome.Reason) != "" {
				fmt.Fprintf(&b, " (%s)", strings.TrimSpace(outcome.Reason))
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

func (h *FixHistory) BehaviorUnchangedSignals(findings []Finding) []BehaviorUnchangedSignal {
	if h == nil || len(h.attempts) == 0 || len(findings) == 0 {
		return nil
	}

	relevant := make(map[string]BehaviorUnchangedSignal, len(findings))
	for _, finding := range findings {
		key := finding.key()
		relevant[key] = BehaviorUnchangedSignal{
			FindingKey: key,
			Label:      findingLabel(finding),
		}
	}

	for i := len(h.attempts) - 1; i >= 0; i-- {
		attempt := h.attempts[i]
		for _, outcome := range attempt.Outcomes {
			signal, ok := relevant[outcome.FindingKey]
			if !ok || normalizeVerificationStatus(outcome.Status) != verifyStatusBehaviorUnchanged {
				continue
			}
			signal.Count++
			if signal.LatestNote == "" {
				signal.LatestNote = strings.TrimSpace(outcome.Reason)
			}
			relevant[outcome.FindingKey] = signal
		}
	}

	signals := make([]BehaviorUnchangedSignal, 0, len(relevant))
	for _, signal := range relevant {
		if signal.Count == 0 {
			continue
		}
		signals = append(signals, signal)
	}
	sort.SliceStable(signals, func(i, j int) bool {
		if signals[i].Count != signals[j].Count {
			return signals[i].Count > signals[j].Count
		}
		return signals[i].Label < signals[j].Label
	})
	return signals
}

func buildOutcomes(findings []Finding, results []verificationResult) []FixOutcome {
	outcomes := make([]FixOutcome, 0, len(findings))
	for i, finding := range findings {
		status := verifyStatusStillPresent
		notes := ""
		if i < len(results) {
			if normalized := normalizeVerificationStatus(results[i].Status); normalized != "" {
				status = normalized
			}
			notes = strings.TrimSpace(results[i].Notes)
		}
		outcomes = append(outcomes, FixOutcome{
			FindingKey: finding.key(),
			Label:      findingLabel(finding),
			Status:     status,
			Reason:     notes,
		})
	}
	return outcomes
}

func findingSet(findings []Finding) map[string]struct{} {
	keys := make(map[string]struct{}, len(findings))
	for _, finding := range findings {
		keys[finding.key()] = struct{}{}
	}
	return keys
}

func targetedLabels(outcomes []FixOutcome) []string {
	seen := make(map[string]struct{}, len(outcomes))
	labels := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Label == "" {
			continue
		}
		if _, ok := seen[outcome.Label]; ok {
			continue
		}
		seen[outcome.Label] = struct{}{}
		labels = append(labels, outcome.Label)
	}
	return labels
}

func findingLabel(f Finding) string {
	description := strings.TrimSpace(f.Description)
	location := strings.TrimSpace(f.Location)
	if location == "" {
		return description
	}
	if description == "" {
		return location
	}
	return fmt.Sprintf("[%s] %s", location, description)
}

func normalizeVerificationStatus(status string) string {
	normalized := strings.ToUpper(strings.TrimSpace(status))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.Join(strings.Fields(normalized), "_")
	switch normalized {
	case verifyStatusResolved,
		verifyStatusPartiallyResolved,
		verifyStatusStillPresent,
		verifyStatusBehaviorUnchanged,
		verifyStatusEvidenceInconclusive,
		verifyStatusBlocked:
		return normalized
	case "NO_OP", verifyStatusNoOp:
		return verifyStatusNoOp
	case "STILLPRESENT":
		return verifyStatusStillPresent
	case "PARTIALLYRESOLVED":
		return verifyStatusPartiallyResolved
	case "BEHAVIORUNCHANGED":
		return verifyStatusBehaviorUnchanged
	case "EVIDENCEINCONCLUSIVE":
		return verifyStatusEvidenceInconclusive
	default:
		return ""
	}
}
