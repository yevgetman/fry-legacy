package audit

import (
	"path/filepath"
	"strconv"
	"strings"
)

type findingClassification struct {
	Resolved    []Finding
	Persisting  []Finding
	NewFindings []Finding
}

func decorateFindings(findings []Finding, cycle int) []Finding {
	if len(findings) == 0 {
		return findings
	}
	decorated := make([]Finding, len(findings))
	for i := range findings {
		decorated[i] = findings[i]
		decorated[i].Category = decorated[i].categoryOrDefault()
		if decorated[i].OriginCycle == 0 {
			decorated[i].OriginCycle = cycle
		}
		decorated[i].LastSeenCycle = cycle
		decorated[i].AffectedFiles = targetFilesForFinding(decorated[i])
	}
	return decorated
}

func targetFilesForFinding(f Finding) []string {
	target := parseFindingTarget(f.Location)
	if target.Path == "" {
		return nil
	}
	return []string{target.Path}
}

type findingTargetRef struct {
	Path string
	Line int
}

func parseFindingTarget(location string) findingTargetRef {
	location = strings.TrimSpace(location)
	if location == "" {
		return findingTargetRef{}
	}

	parts := strings.Split(location, ":")
	ref := findingTargetRef{}
	end := len(parts)
	if end > 0 {
		if line, err := strconv.Atoi(parts[end-1]); err == nil {
			ref.Line = line
			end--
		}
	}
	for end > 0 {
		if _, err := strconv.Atoi(parts[end-1]); err == nil {
			end--
			continue
		}
		break
	}
	ref.Path = strings.TrimSpace(strings.Join(parts[:end], ":"))
	if ref.Path != "" {
		ref.Path = filepath.Clean(ref.Path)
	}
	return ref
}

func classifyFindings(known, current []Finding) findingClassification {
	if len(known) == 0 {
		return findingClassification{NewFindings: append([]Finding(nil), current...)}
	}

	var result findingClassification
	currentUsed := make([]bool, len(current))

	for _, knownFinding := range known {
		if idx := findExactCurrentFinding(knownFinding, current, currentUsed); idx >= 0 {
			currentUsed[idx] = true
			result.Persisting = append(result.Persisting, mergeExactPersistingFinding(knownFinding, current[idx]))
			continue
		}
		result.Resolved = append(result.Resolved, knownFinding)
	}

	seenNew := make(map[string]struct{})
	for i, currentFinding := range current {
		if currentUsed[i] {
			continue
		}
		key := currentFinding.key()
		if _, ok := seenNew[key]; ok {
			continue
		}
		seenNew[key] = struct{}{}
		result.NewFindings = append(result.NewFindings, currentFinding)
	}

	return result
}

func findExactCurrentFinding(known Finding, current []Finding, used []bool) int {
	knownKey := known.key()
	for i, currentFinding := range current {
		if used[i] {
			continue
		}
		if currentFinding.key() == knownKey {
			return i
		}
	}
	return -1
}

func mergeExactPersistingFinding(previous, current Finding) Finding {
	merged := current
	merged.OriginCycle = previous.OriginCycle
	merged.LastSeenCycle = current.LastSeenCycle
	if len(merged.AffectedFiles) == 0 {
		merged.AffectedFiles = append([]string(nil), previous.AffectedFiles...)
	}
	return merged
}
