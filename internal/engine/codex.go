package engine

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
)

type CodexEngine struct{}

func (e *CodexEngine) Run(ctx context.Context, prompt string, opts RunOpts) (string, int, error) {
	args := codexArgs(opts)
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = opts.WorkDir
	cmd.Stdin = strings.NewReader(prompt)

	var buffer bytes.Buffer
	cmd.Stdout = combinedWriter(&buffer, opts.Stdout)
	cmd.Stderr = combinedWriter(&buffer, opts.Stderr)

	err := cmd.Run()
	exitCode := exitCodeFromError(err)
	output := buffer.String()

	// Detect codex's "not authenticated" output. Same rationale as
	// the claude engine: without this check fry would happily write
	// the auth error string into build artifacts and fail downstream
	// with inscrutable validation errors.
	if looksLikeNotLoggedIn(output, codexNotLoggedInPatterns) {
		return output, exitCode, wrapNotAuthenticated("codex", output, "codex login")
	}

	return output, exitCode, err
}

func (e *CodexEngine) Name() string {
	return "codex"
}

func codexArgs(opts RunOpts) []string {
	args := []string{"exec"}
	if opts.SessionID != "" {
		args = append(args, "resume")
	}
	args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	if opts.StructuredOutput {
		args = append(args, "--json")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	args = append(args, opts.ExtraFlags...)
	if opts.SessionID != "" {
		args = append(args, opts.SessionID)
	}
	return args
}

func combinedWriter(buffer *bytes.Buffer, extra io.Writer) io.Writer {
	if extra == nil {
		return buffer
	}
	return io.MultiWriter(buffer, extra)
}
