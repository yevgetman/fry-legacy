package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// HarnessIssue describes a single harness configuration problem found
// during self-validation.
type HarnessIssue struct {
	Sprint  int
	Type    string // "path_mismatch", "suspicious_glob", "missing_parent_dir"
	Target  string
	Message string
}

// HarnessCheckResult captures the outcome of the harness self-check.
type HarnessCheckResult struct {
	Issues []HarnessIssue
}

// HasIssues returns true if any harness problems were found.
func (r *HarnessCheckResult) HasIssues() bool {
	return len(r.Issues) > 0
}

// Summary returns a compact human-readable summary of harness issues.
func (r *HarnessCheckResult) Summary() string {
	if len(r.Issues) == 0 {
		return "harness self-check passed"
	}
	var parts []string
	for _, issue := range r.Issues {
		parts = append(parts, fmt.Sprintf("sprint %d: %s (%s: %s)", issue.Sprint, issue.Message, issue.Type, issue.Target))
	}
	return strings.Join(parts, "; ")
}

// ValidateHarness checks that sanity check targets make sense from the
// project directory. FILE and FILE_CONTAINS targets are validated for
// path syntax, parent directory existence, and suspicious patterns.
// CMD/TEST checks are validated for basic syntax.
// This runs before the main build loop to catch harness mismatches early.
func ValidateHarness(projectDir string, checks []Check) *HarnessCheckResult {
	result := &HarnessCheckResult{}

	for _, c := range checks {
		switch c.Type {
		case CheckFile, CheckFileContains:
			validateFileTarget(projectDir, c, result)
		case CheckCmd, CheckCmdOutput, CheckTest:
			validateCmdTarget(c, result)
		}
		// Validate regex patterns for check types that use grep -E.
		if c.Type == CheckFileContains || c.Type == CheckCmdOutput {
			validateRegexPattern(c, result)
		}
	}

	return result
}

func validateFileTarget(projectDir string, c Check, result *HarnessCheckResult) {
	target := strings.TrimSpace(c.Path)
	if target == "" {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "path_mismatch",
			Target:  c.Path,
			Message: "empty file target",
		})
		return
	}

	// Absolute paths are suspicious in a portable build
	if filepath.IsAbs(target) {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "path_mismatch",
			Target:  target,
			Message: "absolute path in file check — should be relative to project root",
		})
		return
	}

	// Check for path traversal outside project
	cleaned := filepath.Clean(target)
	if strings.HasPrefix(cleaned, "..") {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "path_mismatch",
			Target:  target,
			Message: "path traverses outside project directory",
		})
		return
	}

	// Check that the parent directory exists (the file itself may not
	// exist yet — the sprint is supposed to create it — but the parent
	// directory should at least be plausible).
	fullPath := filepath.Join(projectDir, cleaned)
	parentDir := filepath.Dir(fullPath)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		// Only flag this for paths with at least one directory component.
		// A bare filename like "main.go" resolves to the project root which always exists.
		if strings.Contains(cleaned, string(filepath.Separator)) {
			result.Issues = append(result.Issues, HarnessIssue{
				Sprint:  c.Sprint,
				Type:    "missing_parent_dir",
				Target:  target,
				Message: fmt.Sprintf("parent directory %s does not exist", filepath.Dir(cleaned)),
			})
		}
	}
}

// validateRegexPattern checks that patterns used in grep -E checks are
// valid regex and flags likely unescaped ERE metacharacters that look like
// literal code patterns (e.g. function calls with parentheses). This catches
// the class of bugs where @check_file_contains uses patterns like
// "@id @default(uuid())" which grep -E interprets differently than intended.
func validateRegexPattern(c Check, result *HarnessCheckResult) {
	pattern := strings.TrimSpace(c.Pattern)
	if pattern == "" {
		return
	}

	// Check 1: Is the pattern valid regex at all?
	if _, err := regexp.Compile(pattern); err != nil {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "invalid_regex",
			Target:  pattern,
			Message: fmt.Sprintf("pattern is not valid ERE regex: %v", err),
		})
		return
	}

	// Check 2: Detect likely unescaped literal parentheses.
	// Unescaped () in ERE are grouping operators. If the pattern contains
	// something that looks like a function call — word( — it's almost certainly
	// meant to be a literal match, not a regex group.
	if hasUnescapedLiteralParens(pattern) {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "unescaped_regex_metachar",
			Target:  pattern,
			Message: "pattern contains unescaped parentheses that look like a literal function call — use \\( and \\) for literal matching in grep -E",
		})
	}
}

// hasUnescapedLiteralParens detects patterns where ( or ) appear unescaped
// in a context that looks like a literal string rather than a regex group.
// Heuristic: if a word character immediately precedes ( it looks like a
// function call (e.g. "default(uuid())", "min(8)") rather than a regex group.
func hasUnescapedLiteralParens(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '(' && i > 0 {
			// Check not escaped
			backslashes := 0
			for j := i - 1; j >= 0 && pattern[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 1 {
				continue // escaped \(
			}
			// Check if preceded by a word character (letter, digit, underscore)
			prev := pattern[i-1]
			if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') ||
				(prev >= '0' && prev <= '9') || prev == '_' {
				return true
			}
		}
	}
	return false
}

func validateCmdTarget(c Check, result *HarnessCheckResult) {
	cmd := strings.TrimSpace(c.Command)
	if cmd == "" {
		result.Issues = append(result.Issues, HarnessIssue{
			Sprint:  c.Sprint,
			Type:    "path_mismatch",
			Target:  "(empty)",
			Message: "empty command in check",
		})
	}
}
