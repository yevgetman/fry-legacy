package templates_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/templates"
)

func TestLoadText(t *testing.T) {
	t.Parallel()

	text, err := templates.LoadText("invocations/agent.txt")
	require.NoError(t, err)
	assert.NotEmpty(t, text)
}

func TestLoadTextNotFound(t *testing.T) {
	t.Parallel()

	_, err := templates.LoadText("invocations/nonexistent.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load embedded template")
}
