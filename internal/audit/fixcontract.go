package audit

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/yevgetman/fry/internal/textutil"
)

const (
	diffClassificationBehavioral  = "behavioral"
	diffClassificationCommentOnly = "comment_only"
	diffClassificationEmpty       = "empty"
	diffClassificationOutOfScope  = "out_of_scope"

	fixValidationAccepted        = "accepted"
	fixValidationAcceptedPartial = "accepted_partial"
	fixValidationRejected        = "rejected"
	fixValidationVerifyOnly      = "verify_only"
)

type FixContractIssue struct {
	ID               int
	FindingKey       string
	Label            string
	TargetFiles      []string
	ExpectedEvidence string
}

type FixContract struct {
	Issues []FixContractIssue
}

type FixDiffAssessment struct {
	ChangedFiles       []string
	OutOfScopeFiles    []string
	DiffSummary        string
	DiffClassification string
	ValidationResult   string
	AlreadyFixedClaim  bool
}

func (c FixContract) IssueIDs() []int {
	ids := make([]int, 0, len(c.Issues))
	for _, issue := range c.Issues {
		ids = append(ids, issue.ID)
	}
	return ids
}

func (c FixContract) TargetFiles() []string {
	seen := make(map[string]struct{}, len(c.Issues))
	files := make([]string, 0, len(c.Issues))
	for _, issue := range c.Issues {
		for _, target := range issue.TargetFiles {
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			files = append(files, target)
		}
	}
	sort.Strings(files)
	return files
}

func (c FixContract) ScopeRestricted() bool {
	return len(c.TargetFiles()) > 0
}

func newFixContract(findings []Finding) FixContract {
	issues := make([]FixContractIssue, 0, len(findings))
	for i, finding := range findings {
		issues = append(issues, FixContractIssue{
			ID:               i + 1,
			FindingKey:       finding.key(),
			Label:            findingLabel(finding),
			TargetFiles:      targetFilesForFinding(finding),
			ExpectedEvidence: expectedEvidenceForFinding(finding),
		})
	}
	return FixContract{Issues: issues}
}

func targetFilesForFinding(f Finding) []string {
	path := findingTargetPath(f.Location)
	if path == "" {
		return nil
	}
	return []string{path}
}

func findingTargetPath(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	parts := strings.Split(location, ":")
	end := len(parts)
	for end > 0 {
		if _, err := strconv.Atoi(parts[end-1]); err == nil {
			end--
			continue
		}
		break
	}
	location = strings.Join(parts[:end], ":")
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	return filepath.Clean(location)
}

func expectedEvidenceForFinding(f Finding) string {
	if fix := strings.TrimSpace(f.RecommendedFix); fix != "" {
		return textutil.TruncateUTF8(fix, 180)
	}
	if location := findingTargetPath(f.Location); location != "" {
		return fmt.Sprintf("Behavioral changes in %s that resolve the finding without placeholder edits.", location)
	}
	return "Behavioral changes that resolve the finding without placeholder edits."
}

func assessFixDiff(contract FixContract, diff, fallbackFingerprint, agentOutput string) FixDiffAssessment {
	assessment := FixDiffAssessment{
		AlreadyFixedClaim: detectAlreadyFixedClaim(agentOutput),
	}

	diff = strings.TrimSpace(diff)
	if diff == "" {
		assessment.DiffClassification = diffClassificationEmpty
		assessment.ValidationResult = fixValidationRejected
		assessment.DiffSummary = "no changes"
		if assessment.AlreadyFixedClaim {
			assessment.ValidationResult = fixValidationVerifyOnly
			assessment.DiffSummary = "no changes (already-fixed claim; verify instead of counting this as a remediation)"
		}
		if strings.TrimSpace(fallbackFingerprint) != "" {
			assessment.DiffSummary = summarizeNoopFingerprint(fallbackFingerprint)
		}
		return assessment
	}

	changedFiles, substantive, commentOnly := parseDiffSignals(diff)
	assessment.ChangedFiles = changedFiles

	switch {
	case contract.ScopeRestricted() && len(changedFiles) > 0 && !changedFilesIntersect(contract.TargetFiles(), changedFiles):
		assessment.DiffClassification = diffClassificationOutOfScope
		assessment.ValidationResult = fixValidationRejected
		assessment.DiffSummary = fmt.Sprintf("changed files outside contract: %s", strings.Join(changedFiles, ", "))
	case !substantive && commentOnly:
		assessment.DiffClassification = diffClassificationCommentOnly
		assessment.ValidationResult = fixValidationRejected
		assessment.DiffSummary = fmt.Sprintf("comment-only changes in %s", strings.Join(changedFiles, ", "))
		if assessment.AlreadyFixedClaim {
			assessment.ValidationResult = fixValidationVerifyOnly
			assessment.DiffSummary += " (already-fixed claim; verify instead of counting this as a remediation)"
		}
	default:
		assessment.DiffClassification = diffClassificationBehavioral
		assessment.ValidationResult = fixValidationAccepted
		if len(changedFiles) > 0 {
			assessment.DiffSummary = fmt.Sprintf("behavioral changes in %s", strings.Join(changedFiles, ", "))
		} else {
			assessment.DiffSummary = "behavioral changes detected"
		}
		// Detect out-of-scope files in an otherwise accepted diff.
		// The caller uses this to selectively roll back non-target files.
		if contract.ScopeRestricted() && len(changedFiles) > 0 {
			assessment.OutOfScopeFiles = outOfScopeFiles(contract.TargetFiles(), changedFiles)
		}
	}

	return assessment
}

func detectAlreadyFixedClaim(output string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(output)), " "))
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, "already fixed") ||
		strings.Contains(normalized, "already resolved") ||
		strings.Contains(normalized, "already addressed") ||
		strings.Contains(normalized, "no changes needed") ||
		strings.Contains(normalized, "no changes required") ||
		strings.Contains(normalized, "all listed audit issues have been addressed")
}

func parseDiffSignals(diff string) ([]string, bool, bool) {
	changedFiles := make([]string, 0)
	seenFiles := make(map[string]struct{})
	sawCommentChange := false
	sawSubstantiveChange := false

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				path := strings.TrimPrefix(parts[3], "b/")
				if _, ok := seenFiles[path]; !ok && path != "" {
					seenFiles[path] = struct{}{}
					changedFiles = append(changedFiles, path)
				}
			}
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
			content := strings.TrimSpace(line[1:])
			if content == "" {
				continue
			}
			if isCommentOnlyLine(content) {
				sawCommentChange = true
				continue
			}
			sawSubstantiveChange = true
		}
	}

	sort.Strings(changedFiles)
	return changedFiles, sawSubstantiveChange, sawCommentChange
}

func isCommentOnlyLine(line string) bool {
	return strings.HasPrefix(line, "//") ||
		strings.HasPrefix(line, "/*") ||
		strings.HasPrefix(line, "*") ||
		strings.HasPrefix(line, "*/") ||
		strings.HasPrefix(line, "<!--") ||
		strings.HasPrefix(line, "-->") ||
		strings.HasPrefix(line, "-- ") ||
		strings.HasPrefix(line, ";")
}

func changedFilesIntersect(targets, changed []string) bool {
	if len(targets) == 0 || len(changed) == 0 {
		return false
	}
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	for _, path := range changed {
		if _, ok := targetSet[path]; ok {
			return true
		}
	}
	return false
}

// outOfScopeFiles returns the subset of changed files that are not in the target set.
func outOfScopeFiles(targets, changed []string) []string {
	targetSet := make(map[string]struct{}, len(targets))
	for _, t := range targets {
		targetSet[t] = struct{}{}
	}
	var out []string
	for _, f := range changed {
		if _, ok := targetSet[f]; !ok {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func buildRejectedOutcomes(findings []Finding, assessment FixDiffAssessment) []FixOutcome {
	outcomes := make([]FixOutcome, 0, len(findings))
	for _, finding := range findings {
		reason := assessment.DiffClassification
		switch assessment.DiffClassification {
		case diffClassificationEmpty:
			reason = "empty diff (no file changes)"
		case diffClassificationCommentOnly:
			reason = "comment-only diff"
		case diffClassificationOutOfScope:
			reason = "changed files outside declared target files"
		}
		outcomes = append(outcomes, FixOutcome{
			FindingKey: finding.key(),
			Label:      findingLabel(finding),
			Status:     "REJECTED",
			Reason:     reason,
		})
	}
	return outcomes
}

// isMigrationFile returns true if a file path looks like a database migration.
// Used to warn when rolled-back fix diffs included migration files — the file
// rollback cannot undo database state changes from commands like prisma migrate.
func isMigrationFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "/migrations/") ||
		strings.Contains(lower, "/migrate/") ||
		strings.HasSuffix(lower, ".migration.ts") ||
		strings.HasSuffix(lower, ".migration.js") ||
		strings.HasSuffix(lower, "_migration.rb") ||
		strings.HasSuffix(lower, "_migration.py")
}
