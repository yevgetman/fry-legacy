package monitor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yevgetman/fry/internal/team"
)

func TestNewTeamSource_PollNoActiveTeam(t *testing.T) {
	t.Parallel()

	dir := t.TempDir() // no .fry/team/active-team.txt inside
	src := NewTeamSource(dir)

	changed, err := src.Poll()
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, src.Snapshot())
}

func TestNewTeamSource_PollTransitionToNil(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := NewTeamSource(dir)

	// Simulate prior state: a non-nil snapshot was previously captured.
	// Fields are accessible because the test is in the same package.
	src.snap = &team.Snapshot{}
	src.lastHash = "old-hash"

	// Poll with no active team directory — should detect the transition.
	changed, err := src.Poll()
	require.NoError(t, err)
	assert.True(t, changed, "transition from non-nil snap to nil should report changed=true")
	assert.Nil(t, src.Snapshot())
}
