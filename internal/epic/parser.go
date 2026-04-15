package epic

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/yevgetman/fry/internal/config"
)

type parserState int

const (
	stateGlobal parserState = iota
	stateSprintMeta
	stateSprintPrompt
)

func ParseEpic(path string) (*Epic, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open epic file: %w", err)
	}
	defer file.Close()

	ep := &Epic{
		AuditAfterSprint:  true, // on by default; use @no_review to disable
		ReviewAfterSprint: true, // on by default; use @no_review to disable
	}
	state := stateGlobal
	scanner := bufio.NewScanner(file)
	lineNo := 0
	var current Sprint
	var promptLines []string
	var maxHealAttemptsSet bool
	var maxFailPercentSet bool

	finalizeSprint := func() {
		if current.Number == 0 {
			return
		}
		current.Prompt = stripPromptBleed(strings.Join(promptLines, "\n"))
		ep.Sprints = append(ep.Sprints, current)
		current = Sprint{}
		promptLines = nil
	}

	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), " \t\r")

		switch state {
		case stateGlobal:
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if directive, value, ok := splitDirective(line); ok {
				switch directive {
				case "@epic":
					ep.Name = value
				case "@engine":
					ep.Engine = value
				case "@docker_from_sprint":
					var parseErr error
					ep.DockerFromSprint, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
				case "@docker_ready_cmd":
					ep.DockerReadyCmd = value
				case "@docker_ready_timeout":
					var parseErr error
					ep.DockerReadyTimeout, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
				case "@require_tool":
					ep.RequiredTools = append(ep.RequiredTools, value)
				case "@preflight_cmd":
					ep.PreflightCmds = append(ep.PreflightCmds, value)
				case "@pre_sprint":
					ep.PreSprintCmd = value
				case "@pre_iteration":
					ep.PreIterationCmd = value
				case "@model":
					ep.AgentModel = value
				case "@codex_model":
					fmt.Fprintf(os.Stderr, "fry: warning: @codex_model is deprecated; use @model instead\n")
					ep.AgentModel = value
				case "@engine_flags":
					ep.AgentFlags = value
				case "@codex_flags":
					fmt.Fprintf(os.Stderr, "fry: warning: @codex_flags is deprecated; use @engine_flags instead\n")
					ep.AgentFlags = value
				case "@mcp_config":
					ep.MCPConfig = value
				case "@verification": // path to sanity checks file (directive name retained for backward compat)
					ep.VerificationFile = value
				case "@max_heal_attempts": // max alignment attempts per sprint (directive name retained for backward compat)
					var parseErr error
					ep.MaxHealAttempts, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
					maxHealAttemptsSet = true
					ep.MaxHealAttemptsSet = true
				case "@max_fail_percent":
					var parseErr error
					ep.MaxFailPercent, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
					maxFailPercentSet = true
					ep.MaxFailPercentSet = true
				case "@compact_with_agent":
					ep.CompactWithAgent = true
				case "@review_between_sprints":
					ep.ReviewBetweenSprints = true
				case "@review_engine":
					ep.ReviewEngine = value
				case "@review_model":
					ep.ReviewModel = value
				case "@max_deviation_scope":
					var parseErr error
					ep.MaxDeviationScope, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
				case "@review_after_sprint":
					ep.AuditAfterSprint = true
					ep.ReviewAfterSprint = true
				case "@no_review":
					ep.AuditAfterSprint = false
					ep.ReviewAfterSprint = false
				case "@max_review_iterations":
					var parseErr error
					ep.MaxReviewIterations, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
					ep.MaxReviewIterationsSet = true
					// Keep old field in sync during transition
					ep.MaxAuditIterations = ep.MaxReviewIterations
					ep.MaxAuditIterationsSet = true
				case "@effort":
					ep.EffortLevel, err = ParseEffortLevel(value)
				case "@sprint":
					current = Sprint{}
					var parseErr error
					current.Number, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
					state = stateSprintMeta
				case "@end":
					state = stateGlobal
				default:
					warnUnknownDirective(line)
				}
				if err != nil {
					return nil, fmt.Errorf("parse epic line %d: %w", lineNo, err)
				}
				continue
			}
		case stateSprintMeta:
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if directive, value, ok := splitDirective(line); ok {
				switch directive {
				case "@name":
					current.Name = value
				case "@max_iterations":
					var parseErr error
					current.MaxIterations, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
				case "@promise":
					current.Promise = value
				case "@max_heal_attempts":
					var heal int
					var parseErr error
					heal, parseErr = parseIntDirective(directive, value)
					if parseErr != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, parseErr)
					}
					current.MaxHealAttempts = &heal
				case "@prompt":
					state = stateSprintPrompt
					promptLines = nil
				case "@end":
					finalizeSprint()
					state = stateGlobal
				default:
					warnUnknownDirective(line)
				}
				if err != nil {
					return nil, fmt.Errorf("parse epic line %d: %w", lineNo, err)
				}
				continue
			}
		case stateSprintPrompt:
			if directive, value, ok := splitDirective(line); ok {
				switch directive {
				case "@sprint":
					finalizeSprint()
					current = Sprint{}
					current.Number, err = parseIntDirective(directive, value)
					if err != nil {
						return nil, fmt.Errorf("parse epic line %d: %w", lineNo, err)
					}
					state = stateSprintMeta
					continue
				case "@end":
					finalizeSprint()
					state = stateGlobal
					continue
				}
			}
			promptLines = append(promptLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan epic file: %w", err)
	}

	if state == stateSprintPrompt || state == stateSprintMeta {
		finalizeSprint()
	}

	if ep.VerificationFile == "" {
		ep.VerificationFile = config.DefaultVerificationFile
	}
	if ep.DockerReadyTimeout == 0 {
		ep.DockerReadyTimeout = config.DefaultDockerReadyTimeout
	}
	if ep.MaxDeviationScope == 0 {
		ep.MaxDeviationScope = config.DefaultMaxDeviationScope
	}
	if ep.MaxHealAttempts == 0 && !maxHealAttemptsSet {
		ep.MaxHealAttempts = config.DefaultMaxHealAttempts
	}
	// Max effort uses unlimited progress-based alignment. If the LLM (or user)
	// explicitly set @max_heal_attempts, clear the flag so effectiveHealConfig
	// falls through to the effort-level default path instead of treating it as
	// a hard cap.
	if ep.EffortLevel == EffortMax && maxHealAttemptsSet {
		fmt.Fprintf(os.Stderr, "fry: warning: @max_heal_attempts ignored for max effort (uses unlimited progress-based alignment)\n")
		ep.MaxHealAttempts = 0
		ep.MaxHealAttemptsSet = false
	}
	if ep.MaxFailPercent == 0 && !maxFailPercentSet {
		ep.MaxFailPercent = config.DefaultMaxFailPercent
	}
	if ep.MaxAuditIterations == 0 && ep.AuditAfterSprint {
		ep.MaxAuditIterations = config.DefaultMaxAuditIterations
	}
	if ep.MaxReviewIterations == 0 && ep.ReviewAfterSprint {
		ep.MaxReviewIterations = config.DefaultMaxAuditIterations
	}
	ep.TotalSprints = len(ep.Sprints)

	// Allow deviations to touch any remaining sprint (capped at a safety
	// limit to prevent runaway replan loops). Low effort never runs reviews,
	// so it keeps the parsed/default scope as-is.
	if ep.EffortLevel != EffortFast {
		scope := ep.TotalSprints
		if scope > config.MaxDeviationScopeCap {
			scope = config.MaxDeviationScopeCap
		}
		if ep.MaxDeviationScope < scope {
			ep.MaxDeviationScope = scope
		}
	}

	return ep, nil
}

func splitDirective(line string) (directive string, value string, ok bool) {
	if line == "" || line[0] != '@' {
		return "", "", false
	}
	parts := strings.SplitN(line, " ", 2)
	directive = parts[0]
	if len(parts) == 2 {
		value = strings.TrimSpace(parts[1])
	}
	return directive, value, true
}

func parseIntDirective(name, value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("%s requires an integer value", name)
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s requires an integer value: %w", name, err)
	}
	return n, nil
}

func stripPromptBleed(prompt string) string {
	lines := strings.Split(prompt, "\n")
	end := len(lines)
	for end > 0 {
		trimmed := strings.TrimSpace(lines[end-1])
		if trimmed == "" || isMarkdownDivider(trimmed) {
			end--
			continue
		}
		break
	}
	return strings.Join(lines[:end], "\n")
}

func isMarkdownDivider(line string) bool {
	for _, r := range line {
		if r != '#' && r != '=' && r != ' ' {
			return false
		}
	}
	return line != ""
}

func warnUnknownDirective(line string) {
	if isWarnableDirective(line) {
		fmt.Fprintf(os.Stderr, "fry: warning: unrecognized directive: %s\n", line)
	}
}

func isWarnableDirective(line string) bool {
	if len(line) < 2 || line[0] != '@' {
		return false
	}
	return unicode.IsLower(rune(line[1]))
}
