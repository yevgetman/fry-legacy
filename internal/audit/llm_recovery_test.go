package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const llmRecoveredAudit = "## Summary\nTwo high-severity issues found.\n\n## Findings\n- **Description:** Soft-delete paths do not return HTTP 410\n- **Severity:** HIGH\n\n- **Description:** BrandHeader SSRF via unvalidated logoUrl\n- **Severity:** HIGH\n\n## Verdict\nFAIL\n"

const llmRecoveredVerify = "- **Issue:** 1\n- **Status:** RESOLVED\n\n- **Issue:** 2\n- **Status:** STILL PRESENT\n"

func TestRecoverAuditReportWithLLM(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name:    "claude",
		outputs: []string{llmRecoveredAudit},
	}

	// Use a transcript that no regex can parse — free-form prose.
	transcript := "I found two problems. First, the gone pages don't send a real 410. Second, brand header fetches arbitrary URLs."

	result, err := recoverAuditReportWithLLM(context.Background(), eng, "high", t.TempDir(), transcript, "")
	require.NoError(t, err)
	assert.Contains(t, result, "## Verdict")
	assert.Contains(t, result, "FAIL")
	assert.Contains(t, result, "HIGH")

	// Verify the prompt sent to the engine contains the transcript.
	require.Len(t, eng.prompts, 1)
	assert.Contains(t, eng.prompts[0], "two problems")
}

func TestRecoverAuditReportWithLLMRejectsInvalidOutput(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name:    "claude",
		outputs: []string{"I'm not sure what you want me to do."},
	}

	_, err := recoverAuditReportWithLLM(context.Background(), eng, "standard", t.TempDir(), "some transcript", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not contain expected audit format markers")
}

func TestRecoverAuditReportWithLLMEmptyTranscript(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "claude",
	}

	_, err := recoverAuditReportWithLLM(context.Background(), eng, "standard", t.TempDir(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no transcript")
}

func TestRecoverAuditReportWithLLMEngineError(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name: "claude",
		errs: []error{assert.AnError},
	}

	_, err := recoverAuditReportWithLLM(context.Background(), eng, "standard", t.TempDir(), "some transcript", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "LLM recovery call failed")
}

func TestRecoverVerificationWithLLM(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name:    "codex",
		outputs: []string{llmRecoveredVerify},
	}

	transcript := "Issue 1 is fixed. Issue 2 is still broken."

	result, err := recoverVerificationWithLLM(context.Background(), eng, "high", t.TempDir(), transcript, "", 2)
	require.NoError(t, err)
	assert.Contains(t, result, "**Issue:** 1")
	assert.Contains(t, result, "RESOLVED")
	assert.Contains(t, result, "**Issue:** 2")
	assert.Contains(t, result, "STILL PRESENT")
}

func TestRecoverVerificationWithLLMRejectsInvalid(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name:    "codex",
		outputs: []string{"Everything looks fine I guess."},
	}

	_, err := recoverVerificationWithLLM(context.Background(), eng, "standard", t.TempDir(), "some transcript", "", 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not contain expected verification format markers")
}

func TestRecoverAuditReportWithLLMTruncatesLongTranscript(t *testing.T) {
	t.Parallel()

	eng := &stubEngine{
		name:    "claude",
		outputs: []string{llmRecoveredAudit},
	}

	// Build a transcript that exceeds maxInputChars (12000).
	long := "x]" + strings.Repeat("A", 15000) + "\n[HIGH] Finding at the end"

	result, err := recoverAuditReportWithLLM(context.Background(), eng, "standard", t.TempDir(), long, "")
	require.NoError(t, err)
	require.NotEmpty(t, result)

	// The prompt should contain the tail of the transcript, not the head.
	require.Len(t, eng.prompts, 1)
	assert.Contains(t, eng.prompts[0], "Finding at the end")
}
