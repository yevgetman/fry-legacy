package engine

import (
	"errors"
	"fmt"
	"strings"
)

// ErrEngineNotAuthenticated indicates the engine binary is not currently
// authenticated with its provider. The user must run the engine's
// login flow before fry can continue.
//
// fry's engine.Run implementations detect well-known auth-failure
// strings in the engine's stdout/stderr and wrap them in this sentinel
// so callers can present a clear, actionable top-level error rather
// than the inscrutable downstream symptoms ("validate step 1: AGENTS.md
// has no numbered rules", "no @sprint blocks", etc.) that occur when
// the auth-failure message gets written to disk and treated as
// legitimate engine output.
var ErrEngineNotAuthenticated = errors.New("engine not authenticated")

// claudeNotLoggedInPatterns lists the substrings that indicate Claude
// Code is in an unauthenticated state. The presence of any of these in
// stdout/stderr means the user must run `claude /login` (or the host
// terminal's interactive login flow) before fry can make progress.
var claudeNotLoggedInPatterns = []string{
	"Not logged in",
	"Please run /login",
	"Please log in",
}

// codexNotLoggedInPatterns lists analogous patterns for codex. The
// codex CLI's exact wording may differ from claude; this list is
// best-effort and may need to grow as new failure modes are observed.
var codexNotLoggedInPatterns = []string{
	"Not logged in",
	"Please run codex login",
	"authentication required",
	"Authentication required",
}

// looksLikeNotLoggedIn reports whether the given output appears to be
// an "engine is not authenticated" message rather than a real engine
// response. The check is intentionally cheap and substring-based —
// false positives are tolerable because the patterns are unusual
// content for a real LLM response.
func looksLikeNotLoggedIn(output string, patterns []string) bool {
	if output == "" {
		return false
	}
	for _, p := range patterns {
		if strings.Contains(output, p) {
			return true
		}
	}
	return false
}

// wrapNotAuthenticated builds an ErrEngineNotAuthenticated error with
// a remediation hint that names the specific engine and the command
// the user should run to resolve it.
func wrapNotAuthenticated(engineName, output, loginHint string) error {
	trimmed := strings.TrimSpace(output)
	// Cap the included output so we don't dump kilobytes into the
	// error message in the rare case the engine emitted a long
	// preamble before its login warning.
	if len(trimmed) > 200 {
		trimmed = trimmed[:200] + "..."
	}
	return fmt.Errorf("%w: %s reported %q. Run `%s` to authenticate, then retry",
		ErrEngineNotAuthenticated, engineName, trimmed, loginHint)
}
