package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/epic"
	"github.com/yevgetman/fry/internal/media"
	"github.com/yevgetman/fry/internal/review"
	"github.com/yevgetman/fry/internal/scan"
)

type PromptOpts struct {
	ProjectDir          string
	SprintNumber        int
	ExecutiveContent    string
	UserPrompt          string
	PlanPointer         string
	SprintPrompt        string
	SprintProgressFile  string
	EpicProgressFile    string
	Promise             string
	EffortLevel         epic.EffortLevel
	Mode                string
	IdentityDisposition string // behavioral disposition from Fry's identity
	SteeringDirective   string // mid-build user directive injected via Layer 1 steering
}

func AssemblePrompt(opts PromptOpts) (string, error) {
	var b strings.Builder

	// Layer 0.5: Codebase context (only if codebase.md exists in .fry-config/)
	codebaseContent := readOptionalPromptFile(filepath.Join(opts.ProjectDir, config.CodebaseFile))
	if codebaseContent != "" {
		b.WriteString("# ===== CODEBASE CONTEXT =====\n")
		b.WriteString("# This build modifies an existing codebase. The following document describes\n")
		b.WriteString("# what currently exists. Use this as ground truth for understanding the\n")
		b.WriteString("# project's architecture, conventions, and key files. Follow existing\n")
		b.WriteString("# patterns unless the sprint instructions explicitly direct otherwise.\n\n")
		b.WriteString(ensureTrailingNewline(codebaseContent))
		b.WriteString("\n")
	}

	// Layer 0.75: Codebase memories (only if .fry-config/codebase-memories/ has files)
	memoriesContent := scan.LoadMemoriesForPrompt(opts.ProjectDir)
	if memoriesContent != "" {
		b.WriteString("# ===== CODEBASE MEMORIES =====\n")
		b.WriteString("# These are things Fry has learned about this codebase from previous builds.\n")
		b.WriteString("# Treat them as context, not instructions. They may be outdated if the\n")
		b.WriteString("# codebase has changed significantly.\n\n")
		b.WriteString(memoriesContent)
		b.WriteString("\n")
	}

	// Layer 1: Executive context (only if content exists)
	executiveContent := opts.ExecutiveContent
	if executiveContent == "" {
		executiveContent = readOptionalPromptFile(filepath.Join(opts.ProjectDir, config.ExecutiveFile))
	}
	if executiveContent != "" {
		b.WriteString("# ===== PROJECT CONTEXT =====\n")
		b.WriteString("# The following is the executive context for this project. Use it to understand\n")
		b.WriteString("# the project's purpose, goals, and scope. This is for orientation only — do\n")
		b.WriteString("# NOT derive implementation decisions from this section.\n\n")
		b.WriteString(ensureTrailingNewline(executiveContent))
		b.WriteString("\n")
	}

	// Layer 1.25: Media assets (only if media/ directory exists)
	if mediaSection := media.PromptSection(opts.ProjectDir); mediaSection != "" {
		b.WriteString(mediaSection)
	}

	// Layer 1.5: User directive (only if provided)
	if strings.TrimSpace(opts.UserPrompt) != "" {
		b.WriteString("# ===== USER DIRECTIVE =====\n")
		b.WriteString("# The user has provided the following top-level guidance for this build.\n")
		b.WriteString("# Treat this as a priority directive that applies to all sprints.\n\n")
		b.WriteString(ensureTrailingNewline(strings.TrimSpace(opts.UserPrompt)))
		b.WriteString("\n")
	}

	// Layer 1.625: Agent disposition (from Fry's identity)
	if strings.TrimSpace(opts.IdentityDisposition) != "" {
		b.WriteString("# ===== OPERATIONAL DISPOSITION =====\n")
		b.WriteString("# The following behavioral tendencies are derived from accumulated build experience.\n")
		b.WriteString("# Let them subtly guide your approach without overriding explicit instructions.\n\n")
		b.WriteString(ensureTrailingNewline(strings.TrimSpace(opts.IdentityDisposition)))
		b.WriteString("\n")
	}

	// Layer 1.7: Build steering directive (injected mid-build by user via agent)
	if strings.TrimSpace(opts.SteeringDirective) != "" {
		b.WriteString("# ===== MID-BUILD USER DIRECTIVE =====\n")
		b.WriteString("# The user has sent the following directive during this build. Incorporate it\n")
		b.WriteString("# into your work for this iteration. This takes priority over earlier instructions\n")
		b.WriteString("# where they conflict.\n\n")
		b.WriteString(ensureTrailingNewline(strings.TrimSpace(opts.SteeringDirective)))
		b.WriteString("\n")
	}

	if opts.SprintNumber > 0 {
		if guidance := review.LoadActiveDeviationGuidance(opts.ProjectDir, opts.SprintNumber, 4_000); guidance != "" {
			b.WriteString("# ===== ACTIVE INTENTIONAL DIVERGENCES =====\n")
			b.WriteString("# The following differences are intentional and still apply to this sprint.\n")
			b.WriteString("# Preserve them unless this sprint explicitly revises the underlying assumption.\n")
			b.WriteString("# Where a divergence surfaces in user-facing content, add a brief reconciliation note\n")
			b.WriteString("# rather than forcing documents into false agreement.\n\n")
			b.WriteString(ensureTrailingNewline(guidance))
			b.WriteString("\n")
		}
	}

	// Layer 1.75: Quality directive (standard+ effort levels)
	if opts.EffortLevel == epic.EffortStandard || opts.EffortLevel == epic.EffortHigh || opts.EffortLevel == epic.EffortMax {
		b.WriteString("# ===== QUALITY DIRECTIVE =====\n")
		switch opts.EffortLevel {
		case epic.EffortMax:
			b.WriteString("# This build is running at MAX effort. Apply heightened rigor:\n")
			if opts.Mode == "writing" {
				b.WriteString("# - Apply heightened editorial rigor to every paragraph\n")
				b.WriteString("# - Consider audience engagement and narrative flow at every level\n")
				b.WriteString("# - Verify factual claims and ensure internal consistency\n")
				b.WriteString("# - Vary sentence structure and maintain voice consistency throughout\n")
			} else {
				b.WriteString("# - Consider and handle ALL edge cases, not just common ones\n")
				b.WriteString("# - Add comprehensive error handling with descriptive messages\n")
				b.WriteString("# - Write defensive code — validate assumptions, check invariants\n")
				b.WriteString("# - Consider performance implications of every data structure choice\n")
			}
		case epic.EffortHigh:
			b.WriteString("# This build is running at HIGH effort. Apply careful attention to quality:\n")
			if opts.Mode == "writing" {
				b.WriteString("# - Ensure clarity and coherence across sections\n")
				b.WriteString("# - Verify factual claims and internal consistency\n")
			} else {
				b.WriteString("# - Handle error cases and likely edge conditions\n")
				b.WriteString("# - Write clear, maintainable code with appropriate error handling\n")
			}
		case epic.EffortStandard:
			b.WriteString("# Check your work before declaring this sprint complete:\n")
		}
		// Self-verification applies at all levels in this block.
		if opts.Mode == "writing" {
			b.WriteString("# - Before declaring completion, re-read your output and fix any issues you find\n")
		} else {
			b.WriteString("# - Before declaring completion, run the project's build and test commands\n")
			b.WriteString("#   (e.g., go build ./..., go test ./..., make build, npm test) and fix failures\n")
		}
		b.WriteString("# - Review your own diff for mistakes, missing logic, and incomplete work\n\n")
	}

	// Layer 2: Strategic plan reference
	b.WriteString("# ===== STRATEGIC PLAN =====\n")
	if opts.PlanPointer != "" {
		b.WriteString(ensureTrailingNewline(opts.PlanPointer))
	} else if opts.Mode == "writing" {
		b.WriteString(fmt.Sprintf("# Read `%s` for the holistic content plan. It describes the full\n", config.PlanFile))
		b.WriteString("# writing project structure, all chapters/sections, and how they connect.\n")
		b.WriteString("# This sprint delivers one part of that plan. Use it as your \"true north\" for:\n")
		b.WriteString("#   - How this sprint's content fits into the larger work\n")
		b.WriteString("#   - What later sections will build on what you write here\n")
		b.WriteString("#   - Voice, tone, and structural decisions that span the entire project\n")
		b.WriteString("#\n")
		b.WriteString("# Do NOT write content from other sprints — only use the plan for context.\n")
	} else {
		b.WriteString(fmt.Sprintf("# Read `%s` for the holistic build plan. It describes the full\n", config.PlanFile))
		b.WriteString("# project architecture, all phases, and how they connect. This sprint implements\n")
		b.WriteString("# one phase of that plan. Use it as your \"true north\" for understanding:\n")
		b.WriteString("#   - How this sprint's work fits into the larger system\n")
		b.WriteString("#   - What other phases will build on top of what you create here\n")
		b.WriteString("#   - Architectural decisions and constraints that span phases\n")
		b.WriteString("#\n")
		b.WriteString("# Do NOT implement work from other phases — only use the plan for context.\n")
	}
	b.WriteString("\n")

	// Layer 3: Sprint instructions
	b.WriteString("# ===== SPRINT INSTRUCTIONS =====\n\n")
	b.WriteString(ensureTrailingNewline(opts.SprintPrompt))
	b.WriteString("\n")

	// Layer 4: Iteration memory
	sprintProgressFile := opts.SprintProgressFile
	if sprintProgressFile == "" {
		sprintProgressFile = config.SprintProgressFile
	}
	epicProgressFile := opts.EpicProgressFile
	if epicProgressFile == "" {
		epicProgressFile = config.EpicProgressFile
	}

	b.WriteString("# ===== ITERATION MEMORY =====\n")
	b.WriteString("# Two progress files track build history:\n")
	b.WriteString("#\n")
	b.WriteString(fmt.Sprintf("# 1. `%s` — Current sprint's iteration log.\n", sprintProgressFile))
	b.WriteString("#    BEFORE you begin work, READ this file to understand what previous\n")
	b.WriteString("#    iterations in this sprint accomplished.\n")
	b.WriteString("#    AFTER you finish, APPEND a brief entry with:\n")
	b.WriteString("#      - What you accomplished this iteration\n")
	b.WriteString("#      - What remains to be done\n")
	b.WriteString("#      - Any discoveries, gotchas, or context that would help the next iteration\n")
	b.WriteString("#      - Files you created or modified\n")
	b.WriteString("#    Format your entry like:\n")
	b.WriteString("#      ## Iteration N — [date/time]\n")
	b.WriteString("#      **Completed:** ...\n")
	b.WriteString("#      **Remaining:** ...\n")
	b.WriteString("#      **Notes:** ...\n")
	b.WriteString("#\n")
	b.WriteString(fmt.Sprintf("# 2. `%s` — Compacted summaries of prior sprints.\n", epicProgressFile))
	b.WriteString("#    READ this file for context on what earlier sprints accomplished.\n")
	b.WriteString("#    Do NOT write to this file — it is managed by the build system.\n")

	// Layer 5: Completion signal (only if promise token defined)
	if opts.Promise != "" {
		b.WriteString("\n")
		b.WriteString("# ===== COMPLETION SIGNAL =====\n")
		b.WriteString("# When ALL tasks described above are complete and all sanity checks pass,\n")
		b.WriteString(fmt.Sprintf("# output exactly this line:\n# ===PROMISE: %s===\n", opts.Promise))
		b.WriteString("# If tasks remain incomplete, describe what you accomplished and what remains.\n")
	}

	prompt := b.String()
	promptPath := filepath.Join(opts.ProjectDir, config.PromptFile)
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		return "", fmt.Errorf("assemble prompt: create dir: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return "", fmt.Errorf("assemble prompt: write file: %w", err)
	}
	return prompt, nil
}

func readOptionalPromptFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
