package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const fingerprintContextRadius = 3

type findingClassification struct {
	Resolved          []Finding
	Persisting        []Finding
	RepeatedUnchanged []Finding
	NewFindings       []Finding
}

type reopeningClassification struct {
	Suppressed              []Finding
	Admitted                []Finding
	SuppressedUnchanged     int
	ReopenedWithNewEvidence int
}

func decorateFindings(projectDir string, findings []Finding, cycle int) []Finding {
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
		decorated[i].ArtifactState = fingerprintFindingArtifact(projectDir, decorated[i])
	}
	return decorated
}

func fingerprintFindingArtifact(projectDir string, finding Finding) string {
	target := parseFindingTarget(finding.Location)
	if target.Path == "" {
		return ""
	}

	data, err := readFileOrGitIndex(projectDir, target.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing:" + target.Path
		}
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if target.Line > 0 && target.Line <= len(lines) {
		start := target.Line - fingerprintContextRadius
		if start < 1 {
			start = 1
		}
		end := target.Line + fingerprintContextRadius
		if end > len(lines) {
			end = len(lines)
		}
		segment := strings.Join(lines[start-1:end], "\n")
		return hashFingerprint(fmt.Sprintf("%s:%d-%d:%s", target.Path, start, end, segment))
	}

	return hashFingerprint(fmt.Sprintf("%s:%s", target.Path, string(data)))
}

func hashFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
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

func hasExplicitNewEvidence(f Finding) bool {
	return strings.TrimSpace(f.NewEvidence) != ""
}

func sameArtifactState(a, b Finding) bool {
	if strings.TrimSpace(a.ArtifactState) == "" || strings.TrimSpace(b.ArtifactState) == "" {
		return false
	}
	return a.ArtifactState == b.ArtifactState
}

func mergeExactPersistingFinding(previous, current Finding) Finding {
	merged := current
	merged.OriginCycle = previous.OriginCycle
	merged.LastSeenCycle = current.LastSeenCycle
	if merged.ArtifactState == "" {
		merged.ArtifactState = previous.ArtifactState
	}
	if len(merged.AffectedFiles) == 0 {
		merged.AffectedFiles = append([]string(nil), previous.AffectedFiles...)
	}
	return merged
}

func mergeRepeatedPersistingFinding(previous, current Finding) Finding {
	merged := previous
	merged.LastSeenCycle = current.LastSeenCycle
	if current.Severity != "" {
		merged.Severity = current.Severity
	}
	if current.ArtifactState != "" {
		merged.ArtifactState = current.ArtifactState
	}
	if len(current.AffectedFiles) > 0 {
		merged.AffectedFiles = append([]string(nil), current.AffectedFiles...)
	}
	return merged
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
		if idx := findRepeatedUnchangedCurrentFinding(knownFinding, current, currentUsed); idx >= 0 {
			currentUsed[idx] = true
			result.Persisting = append(result.Persisting, mergeRepeatedPersistingFinding(knownFinding, current[idx]))
			result.RepeatedUnchanged = append(result.RepeatedUnchanged, current[idx])
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

func findRepeatedUnchangedCurrentFinding(known Finding, current []Finding, used []bool) int {
	for i, currentFinding := range current {
		if used[i] {
			continue
		}
		if !themeMatch(known, currentFinding) {
			continue
		}
		if !sameArtifactState(known, currentFinding) {
			continue
		}
		return i
	}
	return -1
}

func classifyReopenings(newFindings []Finding, ledger *resolvedLedger) reopeningClassification {
	if ledger == nil || ledger.len() == 0 {
		return reopeningClassification{Admitted: append([]Finding(nil), newFindings...)}
	}

	var result reopeningClassification
	for _, finding := range newFindings {
		resolved, ok := ledger.findThemeMatch(finding)
		if !ok {
			result.Admitted = append(result.Admitted, finding)
			continue
		}

		finding.ReopenOf = resolved.key()
		if sameArtifactState(resolved, finding) {
			if hasExplicitNewEvidence(finding) {
				result.Admitted = append(result.Admitted, finding)
				result.ReopenedWithNewEvidence++
				continue
			}
			result.Suppressed = append(result.Suppressed, finding)
			result.SuppressedUnchanged++
			continue
		}

		if severityRank(finding.Severity) > severityRank(resolved.Severity) {
			result.Admitted = append(result.Admitted, finding)
			continue
		}
		result.Suppressed = append(result.Suppressed, finding)
	}
	return result
}

func severityRank(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MODERATE":
		return 2
	case "LOW":
		return 1
	default:
		return 0
	}
}
