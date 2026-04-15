package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	frylog "github.com/yevgetman/fry/internal/log"
)

var (
	reviewFindingHeaderRe     = regexp.MustCompile(`(?im)^\d+\.\s+(CRITICAL|HIGH|MEDIUM|MODERATE|LOW):\s+`)
	bracketedSeverityHeaderRe = regexp.MustCompile(`(?im)^(?:[-*+]\s+|\d+\.\s+)?(?:#{1,6}\s+)?\*{0,2}\[(CRITICAL|HIGH|MEDIUM|MODERATE|LOW)\]\*{0,2}\s+(.+?)(?:\s*\*{0,2})$`)
	markdownLinkRe            = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	markdownLinkTargetRe      = regexp.MustCompile(`\[[^\]]+\]\((/[^)]+)\)`)
	explicitPassRe            = regexp.MustCompile(`(?is)\bverdict\b.{0,40}\bpass\b|\bno findings\b|\bno issues (?:were )?found\b|\bno issues remain\b`)
)

// readReviewOutput reads .fry/sprint-review.txt, falling back to transcript recovery.
// The recovered return value is true when the output was reconstructed from agent
// transcript rather than read from the expected file.
func readReviewOutput(reviewFilePath, displayPath, projectDir, output, logPath string) ([]byte, bool, error) {
	content, err := os.ReadFile(reviewFilePath)
	if err == nil && strings.TrimSpace(string(content)) != "" {
		return content, false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("read review output: %w", err)
	}

	recovered, source := recoverReviewReport(displayPath, projectDir, output, logPath)
	if recovered != "" {
		if writeErr := writeFile(reviewFilePath, recovered); writeErr != nil {
			return nil, false, fmt.Errorf("recover review: %w", writeErr)
		}
		recoveredFindings := parseFindings(recovered)
		frylog.Log("  REVIEW: recovered %s from %s (%d findings, max severity: %s)",
			displayPath, source, len(recoveredFindings), maxFindingSeverity(recoveredFindings))
		return []byte(recovered), true, nil
	}

	if err == nil {
		return nil, false, fmt.Errorf("review agent wrote empty %s and no recoverable report was found in agent output", displayPath)
	}
	return nil, false, fmt.Errorf("review agent did not write %s and no recoverable report was found in agent output", displayPath)
}

func recoverReviewReport(displayPath, projectDir, output, logPath string) (content string, source string) {
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

	if findings := parseFindings(section); recoveredFindingsPassQualityCheck(findings) {
		return synthesizeReviewReport(
			fmt.Sprintf("Recovered review report from agent output because the session did not write %s.", displayPath),
			findings,
		), "assistant response"
	}

	if findings := parseReviewStyleFindings(section, projectDir); recoveredFindingsPassQualityCheck(findings) {
		return synthesizeReviewReport(
			fmt.Sprintf("Recovered review findings from agent output because the session did not write %s.", displayPath),
			findings,
		), "review-style assistant response"
	}

	if findings := parseBracketedSeverityFindings(section); recoveredFindingsPassQualityCheck(findings) {
		return synthesizeReviewReport(
			fmt.Sprintf("Recovered review findings from agent output because the session did not write %s.", displayPath),
			findings,
		), "bracketed-severity assistant response"
	}

	if explicitPassRe.MatchString(section) {
		return synthesizeReviewReport(
			fmt.Sprintf("Recovered a clean review result from agent output because the session did not write %s.", displayPath),
			nil,
		), "assistant summary"
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
		if len(description) < 15 {
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

func synthesizeReviewReport(summary string, findings []Finding) string {
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

	for _, f := range findings {
		if location := sanitizeRecoveredField(f.Location); location != "" {
			fmt.Fprintf(&b, "- **Location:** %s\n", location)
		}
		fmt.Fprintf(&b, "- **Description:** %s\n", sanitizeRecoveredField(f.Description))
		if severityLabel := strings.ToUpper(sanitizeRecoveredField(f.Severity)); severityLabel != "" {
			if severityLabel == "MEDIUM" {
				severityLabel = "MODERATE"
			}
			fmt.Fprintf(&b, "- **Severity:** %s\n", severityLabel)
		}
		if fix := sanitizeRecoveredField(f.RecommendedFix); fix != "" {
			fmt.Fprintf(&b, "- **Recommended Fix:** %s\n", fix)
		}
		b.WriteString("\n")
	}

	verdict := "PASS"
	if !isReviewPass(maxFindingSeverity(findings)) {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "## Verdict\n%s\n", verdict)

	return b.String()
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

func splitBracketedDescription(raw string) (description, location string) {
	for _, sep := range []string{" — ", " – ", " -- "} {
		if idx := strings.LastIndex(raw, sep); idx >= 0 {
			left := strings.TrimSpace(raw[:idx])
			right := strings.TrimSpace(raw[idx+len(sep):])
			right = strings.Trim(right, "`")
			if looksLikeFilePath(right) {
				return left, right
			}
		}
	}
	return raw, ""
}

// recoveredFindingsPassQualityCheck returns true if the recovered findings are
// substantive enough to trust. At least one finding must have a description of
// 10 or more characters.
func recoveredFindingsPassQualityCheck(findings []Finding) bool {
	if len(findings) == 0 {
		return false
	}
	for _, f := range findings {
		if len(strings.TrimSpace(f.Description)) >= 10 {
			return true
		}
	}
	return false
}

func ensureTrailingNewline(value string) string {
	if value == "" || strings.HasSuffix(value, "\n") {
		return value
	}
	return value + "\n"
}

func writeFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
