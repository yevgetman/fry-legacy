package codereview

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yevgetman/fry/internal/textutil"
)

type DeferredAnalysis struct {
	Entries      []DeferredEntry       `json:"entries"`
	Interactions []DeferredInteraction `json:"interactions,omitempty"`
	Checklist    []ValidationItem      `json:"checklist,omitempty"`
}

type DeferredEntry struct {
	SprintNumber int    `json:"sprint_number"`
	SprintName   string `json:"sprint_name"`
	CheckType    string `json:"check_type"`
	Target       string `json:"target"`
	Description  string `json:"description"`
	Domain       string `json:"domain"`
	TargetKey    string `json:"target_key"`
}

type DeferredInteraction struct {
	Entries    []int  `json:"entries"`
	Reason     string `json:"reason"`
	Escalation string `json:"escalation"`
}

type ValidationItem struct {
	Description string `json:"description"`
	Source      string `json:"source"`
	Rationale   string `json:"rationale"`
	Priority    string `json:"priority"`
}

var (
	deferredSprintHeaderRe = regexp.MustCompile(`^## Sprint (\d+):\s*(.+)$`)
	deferredLineRe         = regexp.MustCompile(`^- DEFERRED:\s*(.+)$`)
	deferredQuotedPathRe   = regexp.MustCompile(`'([^']+)'`)
)

func AnalyzeDeferredFailures(deferredContent string) *DeferredAnalysis {
	deferredContent = strings.TrimSpace(deferredContent)
	if deferredContent == "" {
		return nil
	}

	lines := strings.Split(deferredContent, "\n")
	currentSprint := 0
	currentName := ""
	var entries []DeferredEntry

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if match := deferredSprintHeaderRe.FindStringSubmatch(line); len(match) == 3 {
			currentSprint = atoi(match[1])
			currentName = strings.TrimSpace(match[2])
			continue
		}
		match := deferredLineRe.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}

		checkType, target := parseDeferredLine(match[1])
		description := strings.TrimSpace(match[1])
		domain := inferDeferredDomain(description + " " + target)
		entries = append(entries, DeferredEntry{
			SprintNumber: currentSprint,
			SprintName:   currentName,
			CheckType:    checkType,
			Target:       target,
			Description:  description,
			Domain:       domain,
			TargetKey:    normalizeDeferredTarget(target),
		})
	}

	if len(entries) == 0 {
		return nil
	}

	analysis := &DeferredAnalysis{Entries: entries}
	analysis.Interactions = detectDeferredInteractions(entries)
	analysis.Checklist = buildValidationChecklist(entries, analysis.Interactions)
	return analysis
}

func RenderDeferredAnalysis(analysis *DeferredAnalysis) string {
	if analysis == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Deferred Failure Analysis\n\n")
	if len(analysis.Interactions) > 0 {
		b.WriteString("### Risk Interactions\n\n")
		b.WriteString("The following deferred failures likely interact and should be re-checked together:\n\n")
		for _, interaction := range analysis.Interactions {
			fmt.Fprintf(&b, "- **%s** (%s): ", interaction.Escalation, interaction.Reason)
			for i, idx := range interaction.Entries {
				if i > 0 {
					b.WriteString("; ")
				}
				if idx < 0 || idx >= len(analysis.Entries) {
					continue
				}
				entry := analysis.Entries[idx]
				fmt.Fprintf(&b, "Sprint %d %s", entry.SprintNumber, entry.Description)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("### All Deferred Failures\n\n")
	for _, entry := range analysis.Entries {
		fmt.Fprintf(&b, "- **Sprint %d — %s**: %s", entry.SprintNumber, entry.SprintName, entry.Description)
		if entry.Domain != "" {
			fmt.Fprintf(&b, " (`%s`)", entry.Domain)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")

	return b.String()
}

func RenderValidationChecklist(checklist []ValidationItem) string {
	if len(checklist) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Human Validation Required\n\n")
	b.WriteString("The following deferred items interacted across the build and should be manually re-checked.\n\n")
	for i, item := range checklist {
		fmt.Fprintf(&b, "## %d. %s (%s)\n", i+1, item.Description, item.Priority)
		fmt.Fprintf(&b, "- **Source:** %s\n", item.Source)
		fmt.Fprintf(&b, "- **Why this is risky now:** %s\n", item.Rationale)
	}
	return b.String()
}

func parseDeferredLine(line string) (checkType string, target string) {
	switch {
	case strings.HasPrefix(line, "File missing or empty: "):
		return "FILE", strings.TrimSpace(strings.TrimPrefix(line, "File missing or empty: "))
	case strings.HasPrefix(line, "File '"):
		if match := deferredQuotedPathRe.FindStringSubmatch(line); len(match) == 2 {
			return "FILE_CONTAINS", strings.TrimSpace(match[1])
		}
		return "FILE_CONTAINS", strings.TrimSpace(line)
	case strings.HasPrefix(line, "Command failed: "):
		return "CMD", strings.TrimSpace(strings.TrimPrefix(line, "Command failed: "))
	case strings.HasPrefix(line, "Command output mismatch: "):
		target = strings.TrimSpace(strings.TrimPrefix(line, "Command output mismatch: "))
		if idx := strings.Index(target, " (expected pattern:"); idx >= 0 {
			target = target[:idx]
		}
		return "CMD_OUTPUT", strings.TrimSpace(target)
	case strings.HasPrefix(line, "Test command failed: "):
		target = strings.TrimSpace(strings.TrimPrefix(line, "Test command failed: "))
		if idx := strings.Index(target, " (pass="); idx >= 0 {
			target = target[:idx]
		}
		return "TEST", strings.TrimSpace(target)
	default:
		return "UNKNOWN", strings.TrimSpace(line)
	}
}

func inferDeferredDomain(value string) string {
	value = strings.ToLower(value)
	switch {
	case containsAny(value, "pricing", "price", "cost", "revenue", "margin", "financial", "budget"):
		return "financial"
	case containsAny(value, "timeline", "schedule", "deadline", "date", "time", "timing"):
		return "temporal"
	case containsAny(value, "security", "auth", "permission", "credential", "token", "secret"):
		return "security"
	case containsAny(value, "latency", "throughput", "performance", "benchmark", "memory", "cpu"):
		return "performance"
	case containsAny(value, "copy", "voice", "content", "summary", "document"):
		return "content"
	default:
		return "general"
	}
}

func detectDeferredInteractions(entries []DeferredEntry) []DeferredInteraction {
	byDomain := make(map[string][]int)
	byTarget := make(map[string][]int)
	for idx, entry := range entries {
		if entry.Domain != "" {
			byDomain[entry.Domain] = append(byDomain[entry.Domain], idx)
		}
		if entry.TargetKey != "" {
			byTarget[entry.TargetKey] = append(byTarget[entry.TargetKey], idx)
		}
	}

	type interactionKey struct {
		reason string
		key    string
	}
	seen := make(map[interactionKey]struct{})
	var interactions []DeferredInteraction

	for domain, idxs := range byDomain {
		if len(idxs) < 2 {
			continue
		}
		interaction := DeferredInteraction{
			Entries:    append([]int(nil), idxs...),
			Reason:     "same domain: " + domain,
			Escalation: "MODERATE",
		}
		if len(idxs) >= 3 {
			interaction.Escalation = "HIGH"
		}
		seen[interactionKey{reason: "domain", key: domain}] = struct{}{}
		interactions = append(interactions, interaction)
	}

	for target, idxs := range byTarget {
		if len(idxs) < 2 {
			continue
		}
		key := interactionKey{reason: "target", key: target}
		if _, ok := seen[key]; ok {
			continue
		}
		interactions = append(interactions, DeferredInteraction{
			Entries:    append([]int(nil), idxs...),
			Reason:     "shared target: " + target,
			Escalation: "HIGH",
		})
	}

	sort.SliceStable(interactions, func(i, j int) bool {
		if len(interactions[i].Entries) != len(interactions[j].Entries) {
			return len(interactions[i].Entries) > len(interactions[j].Entries)
		}
		return interactions[i].Reason < interactions[j].Reason
	})
	return interactions
}

func buildValidationChecklist(entries []DeferredEntry, interactions []DeferredInteraction) []ValidationItem {
	if len(interactions) == 0 {
		return nil
	}

	var checklist []ValidationItem
	for _, interaction := range interactions {
		for _, idx := range interaction.Entries {
			if idx < 0 || idx >= len(entries) {
				continue
			}
			entry := entries[idx]
			checklist = append(checklist, ValidationItem{
				Description: fmt.Sprintf("Sprint %d — %s", entry.SprintNumber, entry.Description),
				Source:      fmt.Sprintf("Sprint %d: %s", entry.SprintNumber, entry.Description),
				Rationale:   fmt.Sprintf("Interacts with other deferred failures via %s.", interaction.Reason),
				Priority:    interaction.Escalation,
			})
		}
	}
	return dedupeValidationChecklist(checklist)
}

func dedupeValidationChecklist(items []ValidationItem) []ValidationItem {
	seen := make(map[string]struct{}, len(items))
	deduped := make([]ValidationItem, 0, len(items))
	for _, item := range items {
		key := item.Description + "::" + item.Source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func normalizeDeferredTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	target = strings.Trim(target, "`")
	if target == "" {
		return ""
	}
	target = strings.Join(strings.Fields(target), " ")
	target = textutil.TruncateUTF8(target, 160)
	return target
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func atoi(value string) int {
	n := 0
	for _, r := range strings.TrimSpace(value) {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
