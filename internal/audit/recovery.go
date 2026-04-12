package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/severity"
)

var (
	reviewFindingHeaderRe     = regexp.MustCompile(`(?im)^\d+\.\s+(CRITICAL|HIGH|MEDIUM|MODERATE|LOW):\s+`)
	bracketedSeverityHeaderRe = regexp.MustCompile(`(?im)^(?:[-*+]\s+)?(?:#{1,6}\s+)?\*{0,2}\[(CRITICAL|HIGH|MEDIUM|MODERATE|LOW)\]\*{0,2}\s+(.+?)(?:\s*\*{0,2})$`)
	markdownLinkRe            = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	markdownLinkTargetRe      = regexp.MustCompile(`\[[^\]]+\]\((/[^)]+)\)`)
	explicitPassRe            = regexp.MustCompile(`(?is)\bverdict\b.{0,40}\bpass\b|\bno findings\b|\bno issues (?:were )?found\b|\bno issues remain\b`)
	allResolvedRe             = regexp.MustCompile(`(?is)\ball\b.{0,80}\bissues?\b.{0,80}\bresolved\b`)
)

func readAuditOutput(path, displayPath, session, logPrefix, errPrefix, projectDir, output, logPath string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(content)) != "" {
		return content, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: read %s output: %w", errPrefix, session, err)
	}

	recovered, source := recoverAuditReport(displayPath, projectDir, output, logPath)
	if recovered != "" {
		if writeErr := writePromptFile(path, recovered); writeErr != nil {
			return nil, fmt.Errorf("%s: recover %s: %w", errPrefix, session, writeErr)
		}
		frylog.Log("  %s: recovered %s from %s (%s)", logPrefix, displayPath, source, session)
		return []byte(recovered), nil
	}

	if err == nil {
		return nil, fmt.Errorf("%s: %s wrote empty %s and no recoverable structured report was found in agent output", errPrefix, session, displayPath)
	}
	return nil, fmt.Errorf("%s: %s did not write %s and no recoverable structured report was found in agent output", errPrefix, session, displayPath)
}

func readVerificationOutput(path, displayPath, session, logPrefix, errPrefix, output, logPath string, issueCount int) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(content)) != "" {
		return content, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: read %s output: %w", errPrefix, session, err)
	}

	recovered, source := recoverVerificationOutput(displayPath, output, logPath, issueCount)
	if recovered != "" {
		if writeErr := writePromptFile(path, recovered); writeErr != nil {
			return nil, fmt.Errorf("%s: recover %s: %w", errPrefix, session, writeErr)
		}
		frylog.Log("  %s: recovered %s from %s (%s)", logPrefix, displayPath, source, session)
		return []byte(recovered), nil
	}

	if err == nil {
		return nil, fmt.Errorf("%s: %s wrote empty %s and no recoverable structured verification report was found in agent output", errPrefix, session, displayPath)
	}
	return nil, fmt.Errorf("%s: %s did not write %s and no recoverable structured verification report was found in agent output", errPrefix, session, displayPath)
}

func recoverAuditReport(displayPath, projectDir, output, logPath string) (content string, source string) {
	transcript := agentTranscript(output, logPath)
	if transcript == "" {
		return "", ""
	}

	if diffContent := extractLastFileDiffContent(transcript, filepath.ToSlash(displayPath)); diffContent != "" {
		return ensureTrailingNewline(diffContent), "assistant diff"
	}

	section := extractLastAssistantSection(transcript)
	if section == "" {
		return "", ""
	}

	if findings := parseFindings(section); len(findings) > 0 {
		return synthesizeAuditReport(
			fmt.Sprintf("Recovered audit report from agent output because the session did not write %s.", displayPath),
			findings,
		), "assistant response"
	}

	if findings := parseReviewStyleFindings(section, projectDir); len(findings) > 0 {
		return synthesizeAuditReport(
			fmt.Sprintf("Recovered audit findings from agent output because the session did not write %s.", displayPath),
			findings,
		), "review-style assistant response"
	}

	if findings := parseBracketedSeverityFindings(section); len(findings) > 0 {
		return synthesizeAuditReport(
			fmt.Sprintf("Recovered audit findings from agent output because the session did not write %s.", displayPath),
			findings,
		), "bracketed-severity assistant response"
	}

	if explicitPassRe.MatchString(section) {
		return synthesizeAuditReport(
			fmt.Sprintf("Recovered a clean audit result from agent output because the session did not write %s.", displayPath),
			nil,
		), "assistant summary"
	}

	return "", ""
}

func recoverVerificationOutput(displayPath, output, logPath string, issueCount int) (content string, source string) {
	transcript := agentTranscript(output, logPath)
	if transcript == "" {
		return "", ""
	}

	if diffContent := extractLastFileDiffContent(transcript, filepath.ToSlash(displayPath)); diffContent != "" {
		return ensureTrailingNewline(diffContent), "assistant diff"
	}

	section := extractLastAssistantSection(transcript)
	if section == "" {
		return "", ""
	}

	if statuses := synthesizeVerificationStatuses(section, issueCount); statuses != "" {
		return statuses, "assistant response"
	}

	lower := strings.ToLower(section)
	if issueCount > 0 && allResolvedRe.MatchString(section) && !strings.Contains(lower, "still present") && !strings.Contains(lower, "not all") {
		return buildResolvedVerificationReport(issueCount), "assistant summary"
	}

	return "", ""
}

func agentTranscript(output, logPath string) string {
	if transcript := normalizeAgentTranscript(output); transcript != "" {
		return transcript
	}
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	return normalizeAgentTranscript(string(data))
}

func normalizeAgentTranscript(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if transcript := normalizeClaudeTranscript(raw); transcript != "" {
		return transcript
	}
	if transcript := normalizeCodexTranscript(raw); transcript != "" {
		return transcript
	}
	return raw
}

func normalizeClaudeTranscript(raw string) string {
	var payload struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || strings.TrimSpace(payload.Result) == "" {
		return ""
	}
	return "assistant\n" + strings.TrimSpace(payload.Result)
}

func normalizeCodexTranscript(raw string) string {
	var messages []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type != "item.completed" || event.Item.Type != "agent_message" || strings.TrimSpace(event.Item.Text) == "" {
			continue
		}
		messages = append(messages, strings.TrimSpace(event.Item.Text))
	}
	if len(messages) == 0 {
		return ""
	}
	return "assistant\n" + strings.Join(messages, "\n\n")
}

func extractLastAssistantSection(raw string) string {
	bestIdx := -1
	bestLen := 0
	for _, marker := range []string{"\ncodex\n", "\nassistant\n"} {
		if idx := strings.LastIndex(raw, marker); idx > bestIdx {
			bestIdx = idx
			bestLen = len(marker)
		}
	}
	if bestIdx >= 0 {
		return strings.TrimSpace(raw[bestIdx+bestLen:])
	}
	for _, prefix := range []string{"codex\n", "assistant\n"} {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(raw, prefix))
		}
	}
	return strings.TrimSpace(raw)
}

func extractLastFileDiffContent(raw, displayPath string) string {
	header := fmt.Sprintf("diff --git a/%s b/%s", displayPath, displayPath)
	lines := strings.Split(raw, "\n")
	start := -1
	for i, line := range lines {
		if line == header {
			start = i
		}
	}
	if start == -1 {
		return ""
	}

	var content []string
	sawHunk := false
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git ") {
			break
		}
		switch {
		case strings.HasPrefix(line, "@@"):
			sawHunk = true
		case strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "),
			strings.HasPrefix(line, "old mode "),
			strings.HasPrefix(line, "new mode "):
			continue
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if sawHunk {
				content = append(content, line[1:])
			}
		case strings.HasPrefix(line, " "):
			if sawHunk {
				content = append(content, line[1:])
			}
		}
	}

	return strings.TrimSpace(strings.Join(content, "\n"))
}

func parseReviewStyleFindings(content, projectDir string) []Finding {
	matches := reviewFindingHeaderRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}

	var findings []Finding
	for i, match := range matches {
		bodyEnd := len(content)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}

		severityLabel := strings.ToUpper(content[match[2]:match[3]])
		if severityLabel == "MEDIUM" {
			severityLabel = "MODERATE"
		}

		body := strings.TrimSpace(content[match[1]:bodyEnd])
		if split := strings.SplitN(body, "\n\n", 2); len(split) > 0 {
			body = split[0]
		}
		description := sanitizeRecoveredField(stripMarkdownLinks(body))
		if description == "" {
			continue
		}

		findings = append(findings, Finding{
			Location:    firstReferencedLocation(body, projectDir),
			Description: description,
			Severity:    severityLabel,
		})
	}

	return findings
}

func firstReferencedLocation(body, projectDir string) string {
	match := markdownLinkTargetRe.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}

	target := strings.TrimSpace(match[1])
	anchor := ""
	if hash := strings.Index(target, "#"); hash >= 0 {
		anchor = target[hash:]
		target = target[:hash]
	}

	target = filepath.Clean(target)
	if projectDir != "" && filepath.IsAbs(target) {
		if rel, err := filepath.Rel(projectDir, target); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			target = rel
		}
	}

	return filepath.ToSlash(target) + anchor
}

func stripMarkdownLinks(value string) string {
	return markdownLinkRe.ReplaceAllString(value, "$1")
}

func sanitizeRecoveredField(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func synthesizeAuditReport(summary string, findings []Finding) string {
	var b strings.Builder

	b.WriteString("## Summary\n")
	b.WriteString(sanitizeRecoveredField(summary))
	b.WriteString("\n\n")

	b.WriteString("## Findings\n")
	if len(findings) == 0 {
		b.WriteString("None.\n\n")
		b.WriteString("## Verdict\nPASS\n")
		return b.String()
	}

	for _, finding := range findings {
		if location := sanitizeRecoveredField(finding.Location); location != "" {
			fmt.Fprintf(&b, "- **Location:** %s\n", location)
		}
		fmt.Fprintf(&b, "- **Description:** %s\n", sanitizeRecoveredField(finding.Description))
		if severityLabel := strings.ToUpper(sanitizeRecoveredField(finding.Severity)); severityLabel != "" {
			if severityLabel == "MEDIUM" {
				severityLabel = "MODERATE"
			}
			fmt.Fprintf(&b, "- **Severity:** %s\n", severityLabel)
		}
		if recommendedFix := sanitizeRecoveredField(finding.RecommendedFix); recommendedFix != "" {
			fmt.Fprintf(&b, "- **Recommended Fix:** %s\n", recommendedFix)
		}
		b.WriteString("\n")
	}

	verdict := "PASS"
	if !isAuditPass(maxFindingSeverity(findings)) {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "## Verdict\n%s\n", verdict)

	return b.String()
}

func maxFindingSeverity(findings []Finding) string {
	maxSeverity := ""
	for _, finding := range findings {
		if severity.Rank(finding.Severity) > severity.Rank(maxSeverity) {
			maxSeverity = finding.Severity
		}
	}
	return maxSeverity
}

// synthesizeVerificationStatuses scans an unstructured agent transcript for
// verification results and rebuilds a canonical "Issue: N / Status: ..." block
// when one is missing. The parser is intentionally fail-soft: it returns ""
// (so the caller can fall back to other recovery paths) any time it cannot
// confidently match a status to every issue.
//
// Order requirement: each `**Issue:** N` line must precede the matching
// `**Status:** ...` line. A status line that appears before its issue number
// is silently dropped — when this happens the function will fail to find a
// status for one of the issues and return "".
func synthesizeVerificationStatuses(content string, issueCount int) string {
	if issueCount <= 0 {
		return ""
	}

	statuses := make([]string, issueCount)
	currentIssue := -1
	seen := 0

	for _, line := range strings.Split(content, "\n") {
		if match := issueNumberRe.FindStringSubmatch(line); len(match) >= 2 {
			issueNo, err := strconv.Atoi(strings.TrimSpace(match[1]))
			if err == nil && issueNo >= 1 && issueNo <= issueCount {
				currentIssue = issueNo - 1
			}
		}

		if match := statusRe.FindStringSubmatch(line); len(match) >= 2 && currentIssue >= 0 {
			status := strings.ToUpper(strings.Join(strings.Fields(match[1]), " "))
			if statuses[currentIssue] == "" {
				statuses[currentIssue] = status
				seen++
			}
			currentIssue = -1
		}
	}

	if seen != issueCount {
		return ""
	}

	var b strings.Builder
	for i, status := range statuses {
		fmt.Fprintf(&b, "- **Issue:** %d\n- **Status:** %s\n\n", i+1, status)
	}
	return b.String()
}

func buildResolvedVerificationReport(issueCount int) string {
	var b strings.Builder
	for i := 1; i <= issueCount; i++ {
		fmt.Fprintf(&b, "- **Issue:** %d\n- **Status:** RESOLVED\n\n", i)
	}
	return b.String()
}

// parseBracketedSeverityFindings extracts findings from output that uses
// bracketed severity in markdown headers, e.g.:
//
//	#### **[HIGH] description — `file.ts:123`**
//	#### [MODERATE] description
//
// This format is commonly produced when agents write audit reports inline
// in their response instead of to the expected output file.
func parseBracketedSeverityFindings(content string) []Finding {
	matches := bracketedSeverityHeaderRe.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}

	var findings []Finding
	for _, match := range matches {
		severityLabel := strings.ToUpper(content[match[2]:match[3]])
		if severityLabel == "MEDIUM" {
			severityLabel = "MODERATE"
		}

		raw := strings.TrimSpace(content[match[4]:match[5]])
		// Strip backticks and trailing bold markers
		raw = strings.TrimRight(raw, "*")
		raw = strings.TrimSpace(raw)

		description, location := splitBracketedDescription(raw)
		description = sanitizeRecoveredField(stripMarkdownLinks(description))
		if description == "" {
			continue
		}

		findings = append(findings, Finding{
			Location:    location,
			Description: description,
			Severity:    severityLabel,
		})
	}

	return findings
}

// splitBracketedDescription splits "description — `location`" into its parts.
// The separator is an em dash (—), en dash (–), or double hyphen (--).
func splitBracketedDescription(raw string) (description, location string) {
	for _, sep := range []string{" — ", " – ", " -- "} {
		if idx := strings.LastIndex(raw, sep); idx >= 0 {
			left := strings.TrimSpace(raw[:idx])
			right := strings.TrimSpace(raw[idx+len(sep):])
			// Only treat right side as location if it looks like a file reference
			right = strings.Trim(right, "`")
			if looksLikeFilePath(right) {
				return left, right
			}
		}
	}
	return raw, ""
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}
