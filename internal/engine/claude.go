package engine

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

type ClaudeEngine struct {
	mcpConfig string
}

func (e *ClaudeEngine) Run(ctx context.Context, prompt string, opts RunOpts) (string, int, error) {
	args := claudeArgs(opts)
	if e.mcpConfig != "" {
		args = append(args, "--mcp-config", e.mcpConfig)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdin = strings.NewReader(prompt)

	var buffer bytes.Buffer
	cmd.Stdout = combinedWriter(&buffer, opts.Stdout)
	cmd.Stderr = combinedWriter(&buffer, opts.Stderr)

	err := cmd.Run()
	exitCode := exitCodeFromError(err)
	output := buffer.String()

	// Detect the well-known "Not logged in · Please run /login" output
	// that claude emits when its session is missing or expired. Without
	// this check, fry happily writes the auth error string into the
	// triage decision file and AGENTS.md, then fails downstream with
	// inscrutable validation errors. The wrapped error gives the user
	// a clear, actionable top-level message instead.
	if looksLikeNotLoggedIn(output, claudeNotLoggedInPatterns) {
		return output, exitCode, wrapNotAuthenticated("claude", output, "claude /login")
	}

	return output, exitCode, err
}

func (e *ClaudeEngine) Name() string {
	return "claude"
}

func claudeArgs(opts RunOpts) []string {
	args := []string{"-p", "--dangerously-skip-permissions"}
	if opts.SessionID != "" {
		args = append(args, "--resume", opts.SessionID)
	}
	if opts.StructuredOutput {
		args = append(args, "--output-format", "json")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, opts.ExtraFlags...)
	return args
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if ok := errors.As(err, &exitErr); ok {
		return exitErr.ExitCode()
	}
	return -1
}
