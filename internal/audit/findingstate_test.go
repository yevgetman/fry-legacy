package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFindingsCapturesNewEvidence(t *testing.T) {
	t.Parallel()

	content := "## Findings\n- **Location:** tracked.go:12\n- **Description:** Handler still lacks input validation\n- **Severity:** HIGH\n- **Recommended Fix:** Validate the payload before use\n- **New Evidence:** The unchanged handler path still trusts user input after the prior fix.\n"

	findings := parseFindings(content)
	require.Len(t, findings, 1)
	assert.Equal(t, "The unchanged handler path still trusts user input after the prior fix.", findings[0].NewEvidence)
}
