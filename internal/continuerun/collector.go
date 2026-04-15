package continuerun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/agent"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/git"
	"github.com/yevgetman/fry/internal/severity"
	"github.com/yevgetman/fry/internal/steering"
)

var completedSprintRe = regexp.MustCompile(`(?m)^## Sprint (\d+):\s*(.+?)\s*—\s*(PASS.*)$`)
var failedSprintRe = regexp.MustCompile(`(?m)^## Sprint (\d+):\s*(.+?)\s*—\s*(FAIL.*)$`)

// CollectBuildState gathers a snapshot of the current build state from .fry/ artifacts.
// When alwaysVerify is true, fast-effort builds are not exempt from the build audit
// sentinel check (mirrors the --always-verify CLI flag).
func CollectBuildState(ctx context.Context, projectDir string, ep *epic.Epic, alwaysVerify bool) (*BuildState, error) {
	fryDir := filepath.Join(projectDir, config.FryDir)
	if _, err := os.Stat(fryDir); os.IsNotExist(err) {
		return nil, ErrNoPreviousBuild
	}

	state := &BuildState{
		EpicName:     ep.Name,
		TotalSprints: ep.TotalSprints,
		Engine:       ep.Engine,
		EffortLevel:  ep.EffortLevel.String(),
	}
	if buildStatus, err := agent.ReadBuildStatus(projectDir); err == nil && buildStatus != nil {
		state.LiveBuildStatus = buildStatus
	}

	// Sprint names list
	state.SprintNames = make([]string, ep.TotalSprints)
	for i, spr := range ep.Sprints {
		state.SprintNames[i] = spr.Name
	}

	// Parse completed and failed sprints from epic-progress.txt
	epicData := readEpicProgress(projectDir)
	state.CompletedSprints = ParseCompletedSprints(epicData)
	for _, cs := range state.CompletedSprints {
		if cs.Number > state.HighestCompleted {
			state.HighestCompleted = cs.Number
		}
	}
	state.FailedSprints = ParseFailedSprints(epicData)

	// Collect active state for all incomplete sprints that have evidence on disk
	knownSet := make(map[int]bool, len(state.CompletedSprints)+len(state.FailedSprints))
	for _, cs := range state.CompletedSprints {
		knownSet[cs.Number] = true
	}
	for _, fs := range state.FailedSprints {
		knownSet[fs.Number] = true
	}
	for i := 1; i <= ep.TotalSprints; i++ {
		if knownSet[i] {
			continue
		}
		active := collectActiveSprintState(projectDir, i, ep)
		if active != nil {
			state.ActiveSprints = append(state.ActiveSprints, *active)
		}
	}

	// Environment checks
	nextSprint := findNextSprint(state.CompletedSprints, ep.TotalSprints)
	state.DockerAvailable = checkDockerAvailable(ctx)
	state.DockerRequired = ep.DockerFromSprint > 0 && nextSprint >= ep.DockerFromSprint
	state.RequiredTools = checkRequiredTools(ep.RequiredTools)
	state.GitClean, state.GitBranch, state.LastAutoCommit = collectGitState(ctx, projectDir)

	// Build mode
	state.Mode = ReadBuildMode(projectDir)

	// Deviation history
	state.DeviationCount = countDeviations(projectDir)

	// Deferred failures
	state.DeferredFailures = collectDeferredFailures(projectDir)

	// Build exit reason
	state.ExitReason = readExitReason(projectDir)

	// Structured resume point
	resumePoint, resumeErr := steering.ReadResumePoint(projectDir)
	if resumeErr == nil {
		state.ResumePoint = resumePoint
	}

	// Build audit sentinel
	sentinelPath := filepath.Join(projectDir, config.BuildAuditCompleteFile)
	if _, err := os.Stat(sentinelPath); err == nil {
		state.BuildAuditComplete = true
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "fry: warning: unable to stat build audit sentinel: %v\n", err)
	}

	// Track whether the build audit is configured for this epic.
	auditConfigured := ep.AuditAfterSprint && !(ep.EffortLevel == epic.EffortFast && !alwaysVerify)
	state.AuditConfigured = auditConfigured

	// When the epic is configured to skip the build audit, the sentinel is never
	// written — treat as complete so HeuristicAnalyze does not return
	// VerdictAuditIncomplete for builds that intentionally omit the audit.
	if !auditConfigured {
		state.BuildAuditComplete = true
	}

	state.LatestActivityPath, state.LatestActivityAt = findLatestBuildActivity(projectDir)
	if state.LiveBuildStatus != nil && !state.LiveBuildStatus.UpdatedAt.IsZero() && !state.LatestActivityAt.IsZero() {
		const staleThreshold = 30 * time.Second
		state.LiveStatusStale = state.LatestActivityAt.After(state.LiveBuildStatus.UpdatedAt.Add(staleThreshold))
	}

	return state, nil
}

// readExitReason reads the persisted exit reason from .fry/build-exit-reason.txt.
func readExitReason(projectDir string) string {
	path := filepath.Join(projectDir, config.BuildExitReasonFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readEpicProgress reads epic-progress.txt and returns its content.
func readEpicProgress(projectDir string) string {
	path := filepath.Join(projectDir, config.EpicProgressFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// ParseCompletedSprints extracts completed sprint entries from epic-progress content.
func ParseCompletedSprints(content string) []CompletedSprint {
	matches := completedSprintRe.FindAllStringSubmatch(content, -1)
	var completed []CompletedSprint
	for _, m := range matches {
		num, err := strconv.Atoi(m[1])
		if err != nil || num < 1 {
			continue
		}
		completed = append(completed, CompletedSprint{
			Number: num,
			Name:   strings.TrimSpace(m[2]),
			Status: strings.TrimSpace(m[3]),
		})
	}
	return completed
}

// ParseFailedSprints extracts failed sprint entries from epic-progress content.
func ParseFailedSprints(content string) []FailedSprint {
	matches := failedSprintRe.FindAllStringSubmatch(content, -1)
	var failed []FailedSprint
	for _, m := range matches {
		num, err := strconv.Atoi(m[1])
		if err != nil || num < 1 {
			continue
		}
		failed = append(failed, FailedSprint{
			Number: num,
			Name:   strings.TrimSpace(m[2]),
			Status: strings.TrimSpace(m[3]),
		})
	}
	return failed
}

// findNextSprint returns the first sprint number not in the completed set.
func findNextSprint(completed []CompletedSprint, totalSprints int) int {
	done := make(map[int]bool, len(completed))
	for _, cs := range completed {
		done[cs.Number] = true
	}
	for i := 1; i <= totalSprints; i++ {
		if !done[i] {
			return i
		}
	}
	return 0 // all complete
}

// collectActiveSprintState checks for evidence of a started-but-not-passed sprint.
func collectActiveSprintState(projectDir string, sprintNum int, ep *epic.Epic) *ActiveSprintState {
	logsDir := filepath.Join(projectDir, config.BuildLogsDir)
	pattern := filepath.Join(logsDir, fmt.Sprintf("sprint%d_*", sprintNum))
	matches, _ := filepath.Glob(pattern)

	// Check sprint-progress.txt for reference to this sprint
	progressMentions := sprintProgressMentionsSprint(projectDir, sprintNum)

	if len(matches) == 0 && !progressMentions {
		return nil
	}

	name := ""
	if sprintNum >= 1 && sprintNum <= len(ep.Sprints) {
		name = ep.Sprints[sprintNum-1].Name
	}

	active := &ActiveSprintState{
		Number: sprintNum,
		Name:   name,
	}

	// Count log types
	for _, m := range matches {
		base := filepath.Base(m)
		switch {
		case strings.Contains(base, "_iter"):
			active.IterationCount++
		case strings.Contains(base, "_audit"):
			active.AuditCount++
		case strings.Contains(base, "_align"):
			active.HealCount++
		case strings.Contains(base, "_retry") || strings.Contains(base, "_resume"):
			active.HasResumeLog = true
		}
	}

	// Read tail of most recent log
	if len(matches) > 0 {
		sort.Strings(matches) // lexicographic = chronological due to timestamp format
		active.LastLogTail = readTail(matches[len(matches)-1], 100)
	}

	// sprint-audit.txt and sprint-progress.txt are both overwritten per sprint.
	// Only read them if the progress file belongs to this sprint, avoiding
	// misattribution of shared files to the wrong sprint.
	if progressMentions {
		auditPath := filepath.Join(projectDir, config.SprintAuditFile)
		if data, err := os.ReadFile(auditPath); err == nil {
			active.AuditSeverity = extractMaxSeverity(string(data))
		}
		active.ProgressExcerpt = readSprintProgressExcerpt(projectDir, sprintNum, 50)
	}

	return active
}

// sprintProgressMentionsSprint checks if sprint-progress.txt references a specific sprint.
func sprintProgressMentionsSprint(projectDir string, sprintNum int) bool {
	path := filepath.Join(projectDir, config.SprintProgressFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	header := fmt.Sprintf("# Sprint %d:", sprintNum)
	return strings.Contains(string(data), header)
}

// checkDockerAvailable returns true if the Docker daemon is reachable.
func checkDockerAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// checkRequiredTools checks whether each required tool is available in PATH.
func checkRequiredTools(tools []string) []ToolStatus {
	statuses := make([]ToolStatus, len(tools))
	for i, tool := range tools {
		_, err := exec.LookPath(tool)
		statuses[i] = ToolStatus{Name: tool, Available: err == nil}
	}
	return statuses
}

// collectGitState returns (clean, branch, lastAutoCommit).
func collectGitState(ctx context.Context, projectDir string) (bool, string, string) {
	return git.CollectState(ctx, projectDir)
}

// countDeviations counts the number of DEVIATE verdict entries in the deviation log.
func countDeviations(projectDir string) int {
	path := filepath.Join(projectDir, config.DeviationLogFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "**Decision**: DEVIATE")
}

// collectDeferredFailures reads summary lines from deferred-failures.md.
func collectDeferredFailures(projectDir string) []string {
	path := filepath.Join(projectDir, config.DeferredFailuresFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- DEFERRED:") || strings.HasPrefix(trimmed, "## Sprint") {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// readTail reads the last n lines of a file.
func readTail(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func findLatestBuildActivity(projectDir string) (string, time.Time) {
	logsDir := filepath.Join(projectDir, config.BuildLogsDir)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return "", time.Time{}
	}

	var latestPath string
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		modTime := info.ModTime()
		if modTime.After(latestTime) {
			latestTime = modTime
			latestPath = filepath.Join(logsDir, entry.Name())
		}
	}

	return latestPath, latestTime
}

// readSprintProgressExcerpt reads sprint-progress.txt and returns only content
// relevant to the specified sprint number. The file is overwritten per sprint
// and headed by "# Sprint N: Name — Progress", so we verify the header matches
// before returning content.
func readSprintProgressExcerpt(projectDir string, sprintNum int, maxLines int) string {
	path := filepath.Join(projectDir, config.SprintProgressFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)

	// Verify the file actually belongs to the requested sprint
	expectedHeader := fmt.Sprintf("# Sprint %d:", sprintNum)
	if !strings.Contains(content, expectedHeader) {
		return "" // file belongs to a different sprint
	}

	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

var (
	severityLabelRe = regexp.MustCompile(`(?i)\bseverity\b`)
	severityWordRe  = regexp.MustCompile(`\b(CRITICAL|HIGH|MODERATE|LOW)\b`)
)

// extractMaxSeverity returns the highest severity found in audit content.
// Only matches severity keywords on lines containing a "Severity" label
// to avoid false positives from prose.
func extractMaxSeverity(content string) string {
	maxRank := 0
	maxSev := ""
	for _, line := range strings.Split(content, "\n") {
		if !severityLabelRe.MatchString(line) {
			continue
		}
		upper := strings.ToUpper(line)
		m := severityWordRe.FindString(upper)
		if m == "" {
			continue
		}
		if severity.Rank(m) > maxRank {
			maxRank = severity.Rank(m)
			maxSev = m
		}
		if maxSev == "CRITICAL" {
			return "CRITICAL"
		}
	}
	return maxSev
}

// ReadBuildMode reads the persisted build mode from .fry/build-mode.txt.
// Returns an empty string if the file does not exist or cannot be read.
func ReadBuildMode(projectDir string) string {
	path := filepath.Join(projectDir, config.BuildModeFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
