package copilot

// This file previously contained the fry-main-owned TickScheduler that
// managed the copilot's wake loop as a goroutine inside the fry process.
// That design was removed because the scheduler died when fry crashed,
// leaving the copilot blind to build failures.
//
// The copilot now manages its own schedule via CronCreate during the
// bootstrap prompt. It is a fully independent process that survives fry
// crashes and can detect and recover from them.
//
// BuildTickArgs is retained as a utility for diagnostic and restart CLIs.

// BuildTickArgs builds the argv for resuming a copilot session. The wake
// message (if any) should be appended by the caller as the final argument.
//
// claude:  claude --dangerously-skip-permissions --resume <id> [--model X] -p
// codex:   codex exec resume --dangerously-bypass-approvals-and-sandbox [--model X] <id>
func BuildTickArgs(engine, sessionID, model string) []string {
	switch engine {
	case "claude":
		args := []string{"claude", "--dangerously-skip-permissions"}
		if sessionID != "" {
			args = append(args, "--resume", sessionID)
		}
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, "-p")
		return args
	case "codex":
		args := []string{"codex", "exec"}
		if sessionID != "" {
			args = append(args, "resume")
		}
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
		if model != "" {
			args = append(args, "--model", model)
		}
		if sessionID != "" {
			args = append(args, sessionID)
		}
		return args
	default:
		return []string{engine}
	}
}
