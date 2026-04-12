package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/yevgetman/fry/internal/engine"
	frylog "github.com/yevgetman/fry/internal/log"
)

const llmRecoveryAuditPrompt = `You are a structured-output extraction tool. Your ONLY job is to read the
raw audit agent output below and rewrite it into the canonical format. Do NOT
add opinions, do NOT re-audit the code, do NOT run any commands or read any
files. Just extract what the agent already found.

Write ONLY the canonical report to stdout — no preamble, no explanation.

## Required output format

## Summary
<1-2 sentence overview extracted from the agent output>

## Findings
<For each finding the agent reported, emit exactly this block>
- **Location:** <file:line if mentioned, otherwise omit this line>
- **Description:** <what the agent said is wrong>
- **Severity:** <CRITICAL | HIGH | MODERATE | LOW — normalize "medium" to MODERATE>
- **Recommended Fix:** <if the agent suggested one, otherwise omit this line>

## Verdict
<PASS if no findings or all LOW, otherwise FAIL>

If the agent explicitly said the audit passed or found no issues, output:

## Summary
No issues found.

## Findings
None.

## Verdict
PASS

## Raw agent output to extract from

`

const llmRecoveryVerifyPrompt = `You are a structured-output extraction tool. Your ONLY job is to read the
raw verification agent output below and rewrite it into the canonical format.
Do NOT add opinions, do NOT re-verify anything, do NOT run any commands or
read any files. Just extract what the agent already reported.

Write ONLY the canonical report to stdout — no preamble, no explanation.

## Required output format

For each issue numbered 1 through N, emit exactly:

- **Issue:** <number>
- **Status:** <RESOLVED | STILL PRESENT>
- **Notes:** <brief notes if the agent provided any, otherwise omit>

If the agent said all issues are resolved, emit a RESOLVED block for each.

## Raw agent output to extract from

`

// recoverAuditReportWithLLM asks a lightweight LLM to reformat raw agent output
// into the canonical audit format. This is the last-resort recovery path, called
// only after all regex-based recovery strategies have failed.
func recoverAuditReportWithLLM(ctx context.Context, eng engine.Engine, effortLevel, projectDir, output, logPath string) (string, error) {
	transcript := agentTranscript(output, logPath)
	if transcript == "" {
		return "", fmt.Errorf("no transcript available for LLM recovery")
	}

	section := extractLastAssistantSection(transcript)
	if section == "" {
		section = transcript
	}

	// Truncate to avoid blowing context on a cheap model.
	const maxInputChars = 12000
	if len(section) > maxInputChars {
		section = section[len(section)-maxInputChars:]
	}

	prompt := llmRecoveryAuditPrompt + section

	model := engine.ResolveModelForSession(eng.Name(), effortLevel, engine.SessionCompaction)
	recovered, _, err := eng.Run(ctx, prompt, engine.RunOpts{
		Model:       model,
		SessionType: engine.SessionCompaction,
		WorkDir:     projectDir,
	})
	if err != nil {
		return "", fmt.Errorf("LLM recovery call failed: %w", err)
	}

	recovered = strings.TrimSpace(recovered)
	if recovered == "" {
		return "", fmt.Errorf("LLM recovery returned empty output")
	}

	// Validate that the LLM produced something that looks like an audit report.
	if !strings.Contains(recovered, "## Verdict") && !strings.Contains(recovered, "## Findings") {
		return "", fmt.Errorf("LLM recovery output does not contain expected audit format markers")
	}

	frylog.Log("  AUDIT: recovered audit report via LLM fallback")
	return ensureTrailingNewline(recovered), nil
}

// recoverVerificationWithLLM asks a lightweight LLM to reformat raw agent
// output into the canonical verification format. Same last-resort pattern as
// recoverAuditReportWithLLM.
func recoverVerificationWithLLM(ctx context.Context, eng engine.Engine, effortLevel, projectDir, output, logPath string, issueCount int) (string, error) {
	transcript := agentTranscript(output, logPath)
	if transcript == "" {
		return "", fmt.Errorf("no transcript available for LLM recovery")
	}

	section := extractLastAssistantSection(transcript)
	if section == "" {
		section = transcript
	}

	const maxInputChars = 12000
	if len(section) > maxInputChars {
		section = section[len(section)-maxInputChars:]
	}

	prompt := fmt.Sprintf("%sThere are %d issues to verify.\n\n%s", llmRecoveryVerifyPrompt, issueCount, section)

	model := engine.ResolveModelForSession(eng.Name(), effortLevel, engine.SessionCompaction)
	recovered, _, err := eng.Run(ctx, prompt, engine.RunOpts{
		Model:       model,
		SessionType: engine.SessionCompaction,
		WorkDir:     projectDir,
	})
	if err != nil {
		return "", fmt.Errorf("LLM recovery call failed: %w", err)
	}

	recovered = strings.TrimSpace(recovered)
	if recovered == "" {
		return "", fmt.Errorf("LLM recovery returned empty output")
	}

	if !strings.Contains(recovered, "**Issue:**") && !strings.Contains(recovered, "**Status:**") {
		return "", fmt.Errorf("LLM recovery output does not contain expected verification format markers")
	}

	frylog.Log("  AUDIT: recovered verification report via LLM fallback")
	return ensureTrailingNewline(recovered), nil
}
