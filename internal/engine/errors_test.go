package engine

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLooksLikeNotLoggedIn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		output   string
		patterns []string
		want     bool
	}{
		{
			name:     "claude exact match",
			output:   "Not logged in · Please run /login",
			patterns: claudeNotLoggedInPatterns,
			want:     true,
		},
		{
			name:     "claude case-sensitive partial",
			output:   "Error: Not logged in",
			patterns: claudeNotLoggedInPatterns,
			want:     true,
		},
		{
			name:     "claude embedded",
			output:   "some preamble\nNot logged in\nmore lines",
			patterns: claudeNotLoggedInPatterns,
			want:     true,
		},
		{
			name:     "codex exact match",
			output:   "Please run codex login",
			patterns: codexNotLoggedInPatterns,
			want:     true,
		},
		{
			name:     "codex authentication required",
			output:   "Authentication required",
			patterns: codexNotLoggedInPatterns,
			want:     true,
		},
		{
			name:     "real claude response",
			output:   `{"type":"result","subtype":"success","result":"Bootstrap complete."}`,
			patterns: claudeNotLoggedInPatterns,
			want:     false,
		},
		{
			name:     "empty output",
			output:   "",
			patterns: claudeNotLoggedInPatterns,
			want:     false,
		},
		{
			name:     "unrelated error",
			output:   "Error: rate limit exceeded",
			patterns: claudeNotLoggedInPatterns,
			want:     false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := looksLikeNotLoggedIn(tc.output, tc.patterns)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWrapNotAuthenticated(t *testing.T) {
	t.Parallel()

	err := wrapNotAuthenticated("claude", "Not logged in · Please run /login", "claude /login")
	require_error_is_sentinel(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "engine not authenticated")
	assert.Contains(t, msg, "claude")
	assert.Contains(t, msg, "Not logged in")
	assert.Contains(t, msg, "claude /login",
		"wrapped error should include the remediation command")
}

func TestWrapNotAuthenticated_TruncatesLongOutput(t *testing.T) {
	t.Parallel()

	long := ""
	for i := 0; i < 500; i++ {
		long += "x"
	}
	err := wrapNotAuthenticated("claude", long, "claude /login")
	msg := err.Error()
	// Sanity: the wrapped message should not contain all 500 x's.
	assert.Less(t, len(msg), 400, "long output must be truncated to keep error messages readable")
	assert.Contains(t, msg, "...", "truncation should be marked with an ellipsis")
}

// require_error_is_sentinel is a small helper used by the wrap test to
// assert errors.Is matches the sentinel.
func require_error_is_sentinel(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrEngineNotAuthenticated) {
		t.Fatalf("expected error to wrap ErrEngineNotAuthenticated, got: %v", err)
	}
}
