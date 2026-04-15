package triage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/verify"
)

// SimpleEpicOpts configures the programmatic epic builder for simple tasks.
type SimpleEpicOpts struct {
	ProjectDir  string
	UserPrompt  string
	PlanContent string
	ExecContent string
	EngineName  string
	EffortLevel epic.EffortLevel
}

// BuildSimpleEpic constructs a minimal Epic struct for simple tasks.
// No LLM call — purely programmatic.
//
// Effort matrix for simple tasks (sprint-level settings only;
// build audit is controlled by the CLI layer):
//
//	Fast:     standard model, 12 iter, no alignment, no sprint audit
//	Standard: standard model, 20 iter, no alignment, 1 audit+fix pass
//	High:     frontier model, 25 iter, no alignment, 1 audit+fix pass
//
// Empty or max effort defaults to fast within this function.
// The CLI layer caps max→high before calling this function.
func BuildSimpleEpic(opts SimpleEpicOpts) (*epic.Epic, error) {
	prompt := opts.PlanContent
	if prompt == "" {
		prompt = opts.ExecContent
	}
	if prompt == "" {
		prompt = opts.UserPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("build simple epic: no content available for sprint prompt")
	}

	effort := opts.EffortLevel
	if effort == "" || effort == epic.EffortMax {
		effort = epic.EffortFast
	}

	auditAfterSprint := effort == epic.EffortStandard || effort == epic.EffortHigh

	ep := &epic.Epic{
		Name:                  "Simple Task",
		Engine:                opts.EngineName,
		EffortLevel:           effort,
		MaxHealAttempts:       0,
		MaxFailPercent:        config.DefaultMaxFailPercent,
		VerificationFile:      config.DefaultVerificationFile,
		AuditAfterSprint:      auditAfterSprint,
		MaxAuditIterations:    1,
		MaxAuditIterationsSet: auditAfterSprint,
		ReviewBetweenSprints:  false,
		TotalSprints:          1,
		Sprints: []epic.Sprint{{
			Number:        1,
			Name:          "Execute task",
			MaxIterations: effort.DefaultMaxIterations(),
			Promise:       "SIMPLE_TASK_COMPLETE",
			Prompt:        prompt,
		}},
	}
	return ep, nil
}

// ModerateEpicOpts configures the programmatic epic builder for moderate tasks.
type ModerateEpicOpts struct {
	ProjectDir  string
	UserPrompt  string
	PlanContent string
	ExecContent string
	EngineName  string
	EffortLevel epic.EffortLevel
	SprintCount int // 1 or 2; 0 defaults to 1
}

// BuildModerateEpic constructs an Epic struct for moderate tasks.
// No LLM call — purely programmatic, like BuildSimpleEpic.
//
// Effort matrix for moderate tasks (sprint-level settings only;
// build audit is controlled by the CLI layer):
//
//	Fast:     standard model, 12 iter, no alignment, no sprint audit, 1 sprint
//	Standard: standard model, 20 iter, 3 alignment attempts, default audit (3 outer, 3 inner), 1-2 sprints
//	High:     frontier model, 25 iter, 10 alignment + progress detection, full audit (12 outer, 7 inner), 1-2 sprints
//
// Empty effort defaults to standard. Max effort is capped to high.
func BuildModerateEpic(opts ModerateEpicOpts) (*epic.Epic, error) {
	prompt := opts.PlanContent
	if prompt == "" {
		prompt = opts.ExecContent
	}
	if prompt == "" {
		prompt = opts.UserPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("build moderate epic: no content available for sprint prompt")
	}

	effort := opts.EffortLevel
	if effort == "" {
		effort = epic.EffortStandard
	}
	if effort == epic.EffortMax {
		effort = epic.EffortHigh
	}

	sprintCount := opts.SprintCount
	if sprintCount <= 0 {
		sprintCount = 1
	}
	if sprintCount > 2 {
		sprintCount = 2
	}
	// Fast effort forces 1 sprint.
	if effort == epic.EffortFast {
		sprintCount = 1
	}

	var healAttempts int
	var auditAfterSprint bool
	switch effort {
	case epic.EffortFast:
		healAttempts = 0
		auditAfterSprint = false
	case epic.EffortStandard:
		healAttempts = config.DefaultMaxHealAttempts
		auditAfterSprint = true
	case epic.EffortHigh:
		healAttempts = config.HealAttemptsHigh
		auditAfterSprint = true
	}

	ep := &epic.Epic{
		Name:                 "Moderate Task",
		Engine:               opts.EngineName,
		EffortLevel:          effort,
		MaxHealAttempts:      healAttempts,
		MaxFailPercent:       config.DefaultMaxFailPercent,
		VerificationFile:     config.DefaultVerificationFile,
		AuditAfterSprint:     auditAfterSprint,
		ReviewBetweenSprints: false,
		TotalSprints:         sprintCount,
	}

	if sprintCount == 1 {
		ep.Sprints = []epic.Sprint{{
			Number:        1,
			Name:          "Execute task",
			MaxIterations: effort.DefaultMaxIterations(),
			Promise:       "MODERATE_TASK_COMPLETE",
			Prompt:        prompt,
		}}
	} else {
		ep.Sprints = []epic.Sprint{
			{
				Number:        1,
				Name:          "Implement core changes",
				MaxIterations: effort.DefaultMaxIterations(),
				Promise:       "CORE_CHANGES_COMPLETE",
				Prompt:        prompt,
			},
			{
				Number:        2,
				Name:          "Polish, test, and finalize",
				MaxIterations: effort.DefaultMaxIterations(),
				Promise:       "MODERATE_TASK_COMPLETE",
				Prompt:        "Continue the work from sprint 1. Polish the implementation, ensure all tests pass, handle edge cases, and finalize the deliverable.",
			},
		}
	}

	return ep, nil
}

// GenerateVerificationChecks produces heuristic sanity checks based on
// recognized build systems in the project directory. No LLM call.
// Checks are duplicated for each sprint number up to sprintCount.
func GenerateVerificationChecks(projectDir string, sprintCount int) []verify.Check {
	type buildSystem struct {
		marker  string
		command string
	}

	systems := []buildSystem{
		{"go.mod", "go build ./... && go test ./..."},
		{"package.json", "npm run build --if-present && npm test --if-present"},
		{"Cargo.toml", "cargo build && cargo test"},
		{"Makefile", "make build 2>/dev/null || make 2>/dev/null || true"},
	}

	// pyproject.toml and setup.py share the same command.
	pyFiles := []string{"pyproject.toml", "setup.py"}

	var commands []string

	for _, sys := range systems {
		if _, err := os.Stat(filepath.Join(projectDir, sys.marker)); err == nil {
			commands = append(commands, sys.command)
		}
	}
	for _, pf := range pyFiles {
		if _, err := os.Stat(filepath.Join(projectDir, pf)); err == nil {
			commands = append(commands, "python -m pytest --tb=short 2>/dev/null || true")
			break
		}
	}

	if len(commands) == 0 {
		return nil
	}

	var checks []verify.Check
	for sprint := 1; sprint <= sprintCount; sprint++ {
		for _, cmd := range commands {
			checks = append(checks, verify.Check{
				Sprint:  sprint,
				Type:    verify.CheckCmd,
				Command: cmd,
			})
		}
	}
	return checks
}

// WriteVerificationFile serializes sanity checks to the @sprint/@check_cmd
// format readable by verify.ParseVerification. Skips file creation if checks is empty.
func WriteVerificationFile(path string, checks []verify.Check) error {
	if len(checks) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("# Auto-generated sanity checks\n\n")

	currentSprint := 0
	for _, c := range checks {
		if c.Sprint != currentSprint {
			if currentSprint > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "@sprint %d\n", c.Sprint)
			currentSprint = c.Sprint
		}
		fmt.Fprintf(&b, "@check_cmd %s\n", c.Command)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write sanity checks file: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write sanity checks file: %w", err)
	}
	return nil
}

// WriteEpicFile serializes an Epic struct into the @epic/@sprint file format
// and writes it to the given path. The output can be parsed back by epic.ParseEpic.
func WriteEpicFile(path string, ep *epic.Epic) error {
	var b strings.Builder

	fmt.Fprintf(&b, "@epic %s\n", ep.Name)
	if ep.Engine != "" {
		fmt.Fprintf(&b, "@engine %s\n", ep.Engine)
	}
	if ep.EffortLevel != "" {
		fmt.Fprintf(&b, "@effort %s\n", ep.EffortLevel)
	}
	if ep.MaxHealAttempts > 0 {
		fmt.Fprintf(&b, "@max_heal_attempts %d\n", ep.MaxHealAttempts)
	} else if ep.MaxHealAttempts == 0 {
		b.WriteString("@max_heal_attempts 0\n")
	}
	if ep.MaxFailPercent > 0 && ep.MaxFailPercent != config.DefaultMaxFailPercent {
		fmt.Fprintf(&b, "@max_fail_percent %d\n", ep.MaxFailPercent)
	}
	if ep.ReviewAfterSprint && ep.MaxReviewIterationsSet && ep.MaxReviewIterations > 0 {
		fmt.Fprintf(&b, "@max_review_iterations %d\n", ep.MaxReviewIterations)
	}
	if !ep.ReviewAfterSprint {
		b.WriteString("@no_review\n")
	}
	b.WriteString("\n")

	for _, s := range ep.Sprints {
		fmt.Fprintf(&b, "@sprint %d\n", s.Number)
		fmt.Fprintf(&b, "@name %s\n", s.Name)
		fmt.Fprintf(&b, "@max_iterations %d\n", s.MaxIterations)
		fmt.Fprintf(&b, "@promise %s\n", s.Promise)
		b.WriteString("@prompt\n")
		b.WriteString(s.Prompt)
		if !strings.HasSuffix(s.Prompt, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("@end\n\n")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write epic file: create dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write epic file: %w", err)
	}
	return nil
}
