package review

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/engine"
	"github.com/yevgetman/fry/internal/epic"
	frylog "github.com/yevgetman/fry/internal/log"
	"github.com/yevgetman/fry/internal/textutil"
)

type ReplanOpts struct {
	ProjectDir        string
	EpicPath          string
	DeviationSpecPath string
	DeviationSpec     *DeviationSpec
	CompletedSprint   int
	MaxScope          int
	Engine            engine.Engine
	Model             string
	EffortLevel       string
	DryRun            bool
	Verbose           bool
	Stdout            io.Writer // optional; defaults to os.Stdout when Verbose is true
}

func RunReplan(ctx context.Context, opts ReplanOpts) error {
	if opts.ProjectDir == "" {
		return fmt.Errorf("run replan: project dir is required")
	}

	spec, err := loadDeviationSpec(opts)
	if err != nil {
		return fmt.Errorf("run replan: %w", err)
	}

	epicPath := opts.EpicPath
	if epicPath == "" {
		epicPath = filepath.Join(opts.ProjectDir, config.FryDir, "epic.md")
	}
	if !filepath.IsAbs(epicPath) {
		epicPath = filepath.Join(opts.ProjectDir, epicPath)
	}

	originalContentBytes, err := os.ReadFile(epicPath)
	if err != nil {
		return fmt.Errorf("run replan: read epic: %w", err)
	}
	originalContent := string(originalContentBytes)

	planContent, err := readOptional(filepath.Join(opts.ProjectDir, config.PlanFile))
	if err != nil {
		return fmt.Errorf("run replan: read plan: %w", err)
	}
	if strings.TrimSpace(planContent) == "" {
		planContent = "(plan.md not found)"
	}

	deviationLogContent, err := readOptional(filepath.Join(opts.ProjectDir, config.DeviationLogFile))
	if err != nil {
		return fmt.Errorf("run replan: read deviation log: %w", err)
	}
	if strings.TrimSpace(deviationLogContent) == "" {
		deviationLogContent = "No prior deviations."
	}

	prompt := assembleReplanPrompt(spec.RawText, originalContent, planContent, deviationLogContent, opts.CompletedSprint)
	if opts.DryRun {
		promptPath := filepath.Join(opts.ProjectDir, config.FryDir, "replan-prompt.md")
		if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
			return fmt.Errorf("run replan: create fry dir: %w", err)
		}
		return os.WriteFile(promptPath, []byte(prompt), 0o644)
	}
	if opts.Engine == nil {
		return fmt.Errorf("run replan: engine is required")
	}

	promptPath := filepath.Join(opts.ProjectDir, config.FryDir, "replan-prompt.md")
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return fmt.Errorf("run replan: create fry dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("run replan: write prompt: %w", err)
	}
	defer func() {
		_ = os.Remove(promptPath)
	}()

	frylog.Log("  Running replanner agent...  engine=%s  model=%s", opts.Engine.Name(), opts.Model)
	runOpts := engine.RunOpts{
		Model:       opts.Model,
		SessionType: engine.SessionReplan,
		EffortLevel: opts.EffortLevel,
		WorkDir:     opts.ProjectDir,
	}
	if opts.Verbose {
		stdout := opts.Stdout
		if stdout == nil {
			stdout = os.Stdout
		}
		runOpts.Stdout = stdout
		runOpts.Stderr = stdout
	}
	originalParsed, err := parseEpicContent(originalContent)
	if err != nil {
		return fmt.Errorf("run replan: parse original epic: %w", err)
	}

	const maxReplanRetries = 2
	agentPrompt := "Read and execute ALL instructions in .fry/replan-prompt.md. Output ONLY the complete updated epic.md file content. No explanation, no code fences."

	var updatedContent string
	for attempt := 0; ; attempt++ {
		beforeSize := textutil.FileSize(epicPath)
		output, _, runErr := opts.Engine.Run(ctx, agentPrompt, runOpts)
		if runErr != nil && strings.TrimSpace(output) == "" {
			_ = os.WriteFile(epicPath, originalContentBytes, 0o644)
			return fmt.Errorf("run replan: replanner agent failed: %w", runErr)
		}
		if runErr != nil {
			frylog.Log("WARNING: replanner agent exited with error (non-fatal): %v", runErr)
		}

		if err := textutil.ResolveArtifact(epicPath, beforeSize, output); err != nil {
			_ = os.WriteFile(epicPath, originalContentBytes, 0o644)
			return fmt.Errorf("run replan: resolve artifact: %w", err)
		}
		data, err := os.ReadFile(epicPath)
		if err != nil {
			return fmt.Errorf("run replan: read updated epic: %w", err)
		}
		updatedContent = string(data)
		if strings.TrimSpace(updatedContent) == "" {
			_ = os.WriteFile(epicPath, originalContentBytes, 0o644)
			return fmt.Errorf("run replan: replanner produced empty output")
		}

		updatedParsed, err := parseEpicContent(updatedContent)
		if err != nil {
			if attempt >= maxReplanRetries {
				_ = os.WriteFile(epicPath, originalContentBytes, 0o644)
				return fmt.Errorf("run replan: parse updated epic after %d attempts: %w", attempt+1, err)
			}
			_ = os.WriteFile(epicPath, originalContentBytes, 0o644)
			frylog.Log("  Replan validation failed (attempt %d/%d): %v — retrying", attempt+1, maxReplanRetries+1, err)
			agentPrompt = buildReplanRetryPrompt(err, opts.CompletedSprint)
			continue
		}

		validationErr := validateReplanWithRaw(originalParsed, updatedParsed, originalContent, updatedContent, opts.CompletedSprint, opts.MaxScope)
		if validationErr == nil {
			break
		}

		if err := os.WriteFile(epicPath, originalContentBytes, 0o644); err != nil {
			return fmt.Errorf("run replan: restore epic: %w", err)
		}

		if attempt >= maxReplanRetries {
			return fmt.Errorf("run replan: validation failed after %d attempts: %w", attempt+1, validationErr)
		}

		frylog.Log("  Replan validation failed (attempt %d/%d): %v — retrying", attempt+1, maxReplanRetries+1, validationErr)
		agentPrompt = buildReplanRetryPrompt(validationErr, opts.CompletedSprint)
	}

	backupDir := filepath.Join(opts.ProjectDir, config.BuildLogsDir)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("run replan: create backup dir: %w", err)
	}
	backupPath := filepath.Join(backupDir, fmt.Sprintf("epic.md.bak.sprint%d.%s", opts.CompletedSprint, time.Now().Format("20060102_150405")))
	if err := os.WriteFile(backupPath, originalContentBytes, 0o644); err != nil {
		return fmt.Errorf("run replan: backup epic: %w", err)
	}
	if err := os.WriteFile(epicPath, []byte(updatedContent), 0o644); err != nil {
		return fmt.Errorf("run replan: write updated epic: %w", err)
	}

	frylog.Log("  Epic updated successfully.")
	return nil
}

func buildReplanRetryPrompt(validationErr error, completedSprint int) string {
	return fmt.Sprintf(
		"Read and execute ALL instructions in .fry/replan-prompt.md. Output ONLY the complete updated epic.md file content. No explanation, no code fences.\n\n"+
			"CRITICAL: Your previous attempt was REJECTED because: %s\n"+
			"You MUST copy sprints 1 through %d EXACTLY as they appear in the original file — character for character, with no modifications whatsoever.",
		validationErr, completedSprint,
	)
}

func ValidateReplan(original, updated *epic.Epic, completedSprint, maxScope int) error {
	if original == nil || updated == nil {
		return fmt.Errorf("validate replan: original and updated epic are required")
	}

	for sprintNum := 1; sprintNum <= completedSprint; sprintNum++ {
		origSprint := findSprint(original, sprintNum)
		updatedSprint := findSprint(updated, sprintNum)
		if origSprint == nil || updatedSprint == nil {
			return fmt.Errorf("validate replan: missing sprint %d during completed sprint protection", sprintNum)
		}
		if origSprint.Prompt != updatedSprint.Prompt {
			return fmt.Errorf("completed sprint %d was modified by replanner", sprintNum)
		}
	}

	maxAllowedSprint := completedSprint + maxScope
	for _, origSprint := range original.Sprints {
		if origSprint.Number <= maxAllowedSprint {
			continue
		}
		updatedSprint := findSprint(updated, origSprint.Number)
		if updatedSprint == nil {
			return fmt.Errorf("validate replan: missing sprint %d in updated epic", origSprint.Number)
		}
		if origSprint.Prompt != updatedSprint.Prompt {
			return fmt.Errorf("sprint %d is outside deviation scope (max: sprint %d)", origSprint.Number, maxAllowedSprint)
		}
	}

	if len(original.Sprints) != len(updated.Sprints) {
		return fmt.Errorf("sprint count changed (was %d, now %d)", len(original.Sprints), len(updated.Sprints))
	}

	if !equalGlobalDirectives(original, updated) {
		return fmt.Errorf("global directives were modified by replanner")
	}

	if !equalStructuralDirectives(original, updated) {
		return fmt.Errorf("structural directives (@sprint/@name/@max_iterations/@promise) were modified")
	}

	return nil
}

func ExtractSprintPrompt(epicContent string, sprintNum int) string {
	lines := strings.Split(epicContent, "\n")
	var current int
	inPrompt := false
	var promptLines []string

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmed, "@sprint ") {
			if inPrompt && current == sprintNum {
				break
			}
			current = parseSprintNumber(trimmed)
			inPrompt = false
			continue
		}
		if current == sprintNum && trimmed == "@prompt" {
			inPrompt = true
			continue
		}
		if inPrompt && (trimmed == "@end" || strings.HasPrefix(trimmed, "@sprint ")) {
			break
		}
		if inPrompt {
			promptLines = append(promptLines, trimmed)
		}
	}

	return strings.Join(promptLines, "\n")
}

func assembleReplanPrompt(deviationSpec, epicContents, planContents, deviationLogContents string, completedSprint int) string {
	return fmt.Sprintf(`# Epic Replanning — Mid-Build Adjustment

## Your Role
You are editing sprint prompts in an epic.md file to account for a deviation
from the original plan. Make MINIMAL, TARGETED changes.

## Rules
1. ONLY edit the @prompt blocks for the sprints specified in the Deviation Spec below
2. Do NOT change @sprint, @name, @max_iterations, @promise, or any global directives
3. Do NOT add or remove sprints
4. Do NOT rewrite prompts from scratch — make surgical edits to affected lines only
5. Preserve the 7-part prompt structure (opener, references, build list, constraints, sanity checks, stuck hint, promise)
6. Changes should be proportional: if the deviation is about import paths, only change the import paths. Don't rewrite surrounding instructions.
7. Sprints 1 through %d are COMPLETED — do NOT modify them under any circumstances
8. Only modify sprints explicitly listed in the Affected Sprints section of the Deviation Spec

## The Deviation Spec
%s

## Current epic.md (full contents)
%s

## Original Plan (plans/plan.md) — for reference
%s

## Prior Deviations (deviation-log.md)
%s

## Output
Output the COMPLETE updated epic.md file content. It must be directly saveable as a file.
Only the @prompt blocks for the affected sprints should differ from the input.
No preamble, no explanation, no markdown code fences around the output.
The output should start exactly as the input file starts.
`, completedSprint, deviationSpec, epicContents, planContents, deviationLogContents)
}

func validateReplanWithRaw(originalParsed, updatedParsed *epic.Epic, originalContent, updatedContent string, completedSprint, maxScope int) error {
	if err := ValidateReplan(originalParsed, updatedParsed, completedSprint, maxScope); err != nil {
		return err
	}

	for sprintNum := 1; sprintNum <= completedSprint; sprintNum++ {
		if ExtractSprintPrompt(originalContent, sprintNum) != ExtractSprintPrompt(updatedContent, sprintNum) {
			return fmt.Errorf("completed sprint %d was modified by replanner", sprintNum)
		}
	}

	maxAllowedSprint := completedSprint + maxScope
	totalSprints := len(originalParsed.Sprints)
	for sprintNum := maxAllowedSprint + 1; sprintNum <= totalSprints; sprintNum++ {
		if ExtractSprintPrompt(originalContent, sprintNum) != ExtractSprintPrompt(updatedContent, sprintNum) {
			return fmt.Errorf("sprint %d is outside deviation scope (max: sprint %d)", sprintNum, maxAllowedSprint)
		}
	}

	return nil
}

func loadDeviationSpec(opts ReplanOpts) (*DeviationSpec, error) {
	if opts.DeviationSpec != nil {
		return opts.DeviationSpec, nil
	}
	if opts.DeviationSpecPath == "" {
		return nil, fmt.Errorf("deviation spec or deviation spec path is required")
	}
	data, err := os.ReadFile(opts.DeviationSpecPath)
	if err != nil {
		return nil, fmt.Errorf("read deviation spec: %w", err)
	}
	spec := ExtractDeviationSpec("### Deviation Spec\n" + string(data))
	if spec == nil {
		return &DeviationSpec{RawText: strings.TrimSpace(string(data))}, nil
	}
	if spec.RawText == "" {
		spec.RawText = strings.TrimSpace(string(data))
	}
	return spec, nil
}

func parseEpicContent(content string) (*epic.Epic, error) {
	tmp, err := os.CreateTemp("", "fry-epic-*.md")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return epic.ParseEpic(tmp.Name())
}

func findSprint(ep *epic.Epic, sprintNum int) *epic.Sprint {
	for i := range ep.Sprints {
		if ep.Sprints[i].Number == sprintNum {
			return &ep.Sprints[i]
		}
	}
	return nil
}

func equalGlobalDirectives(a, b *epic.Epic) bool {
	return a.Name == b.Name &&
		a.Engine == b.Engine &&
		a.EffortLevel == b.EffortLevel &&
		a.DockerFromSprint == b.DockerFromSprint &&
		a.DockerReadyCmd == b.DockerReadyCmd &&
		a.DockerReadyTimeout == b.DockerReadyTimeout &&
		reflect.DeepEqual(a.RequiredTools, b.RequiredTools) &&
		reflect.DeepEqual(a.PreflightCmds, b.PreflightCmds) &&
		a.PreSprintCmd == b.PreSprintCmd &&
		a.PreIterationCmd == b.PreIterationCmd &&
		a.AgentModel == b.AgentModel &&
		a.AgentFlags == b.AgentFlags &&
		a.VerificationFile == b.VerificationFile &&
		a.MaxHealAttempts == b.MaxHealAttempts &&
		a.MaxHealAttemptsSet == b.MaxHealAttemptsSet &&
		a.MaxFailPercentSet == b.MaxFailPercentSet &&
		a.CompactWithAgent == b.CompactWithAgent &&
		a.ReviewBetweenSprints == b.ReviewBetweenSprints &&
		a.ReviewEngine == b.ReviewEngine &&
		a.ReviewModel == b.ReviewModel &&
		a.MaxDeviationScope == b.MaxDeviationScope &&
		a.AuditAfterSprint == b.AuditAfterSprint &&
		a.MaxAuditIterations == b.MaxAuditIterations &&
		a.MaxAuditIterationsSet == b.MaxAuditIterationsSet &&
		a.AuditEngine == b.AuditEngine &&
		a.AuditModel == b.AuditModel
}

func equalStructuralDirectives(a, b *epic.Epic) bool {
	if len(a.Sprints) != len(b.Sprints) {
		return false
	}
	for i := range a.Sprints {
		as := a.Sprints[i]
		bs := b.Sprints[i]
		if as.Number != bs.Number || as.Name != bs.Name || as.MaxIterations != bs.MaxIterations || as.Promise != bs.Promise {
			return false
		}
	}
	return true
}

var reSprintNumber = regexp.MustCompile(`^@sprint\s+(\d+)`)

func parseSprintNumber(line string) int {
	matches := reSprintNumber.FindStringSubmatch(line)
	if len(matches) != 2 {
		return 0
	}
	// Regex guarantees matches[1] is digits, so Atoi cannot fail.
	n, _ := strconv.Atoi(matches[1])
	return n
}
