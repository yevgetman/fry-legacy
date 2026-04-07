package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yevgetman/fry/internal/archive"
	"github.com/yevgetman/fry/internal/config"
	"github.com/yevgetman/fry/internal/copilot"
	"github.com/yevgetman/fry/internal/lock"
)

var cleanForce bool
var cleanYes bool

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Archive build artifacts from .fry/ and remove .fry-worktrees/",
	Long: `Move .fry/ and root-level build outputs (build-audit.md, build-summary.md) into a timestamped folder under .fry-archive/.

Also removes leftover .fry-worktrees/<branch>/ directories so the next build starts from a clean slate. Branches created by fry on the fry/<slug> pattern survive this removal — they live in the main repository's git history, not in the worktree itself. Use ` + "`git branch -D fry/<slug>`" + ` if you also want to delete the branch.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pDir, _ := cmd.Flags().GetString("project-dir")
		projectPath, err := resolveProjectDir(pDir)
		if err != nil {
			return err
		}

		if lock.IsLocked(projectPath) {
			fmt.Fprintln(cmd.ErrOrStderr(), "fry: warning: a build appears to be running (lock file active)")
		}

		// Warn the user that fry clean cannot cancel copilot crons.
		// The cron lives in Claude Code's session storage, not in .fry/.
		// The orphan agent should self-prune on its next wake (Tick
		// Checklist step 0), but the user should know what to expect.
		if copilot.CopilotConfigured(projectPath) {
			cronID := copilot.ReadCronIDFile(projectPath)
			if cronID != "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"fry: warning: copilot cron %q will be archived to .fry-archive/ along with .fry/, "+
						"but the cron itself remains scheduled in Claude Code's runtime. fry cannot cancel "+
						"external crons. The orphan should self-prune on its next wake (the bootstrap "+
						"prompt detects a missing manifest and calls CronDelete). If it persists, resume "+
						"the orphan with `claude --resume <session-id>` and ask it to delete its cron.\n",
					cronID)
			}
		}

		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")
		if force || yes {
			fmt.Fprintln(cmd.OutOrStdout(), "Archive .fry/ and build outputs? [y/N] y (auto-accepted)")
		} else {
			fmt.Fprint(cmd.OutOrStdout(), "Archive .fry/ and build outputs? [y/N] ")
			reader := bufio.NewReader(cmd.InOrStdin())
			answer, _ := reader.ReadString('\n')
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "y") {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
		}

		archivePath, err := archive.Archive(projectPath)
		if err != nil {
			return fmt.Errorf("clean: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Archived to %s\n", archivePath)

		// Also remove leftover .fry-worktrees/ entries. Without this,
		// `fry clean` archives .fry/ but leaves the worktree dirs on
		// disk; the user's mental model ("clean = blank slate") is
		// violated, and the next build can also be confused by the
		// stale state inside the leftover worktree.
		//
		// We use `git worktree remove --force` first (so git's tracking
		// is updated cleanly), then fall back to os.RemoveAll for any
		// dirs git doesn't recognise (e.g., orphaned dirs from a
		// previous bug). Branches created by fry on the fry/<slug>
		// pattern survive this removal — they live in the main
		// repository's git history, not in the worktree itself.
		removed := cleanWorktrees(cmd, projectPath)
		if removed > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d worktree dir(s) under %s\n", removed, config.GitWorktreeDir)
		}
		return nil
	},
}

// cleanWorktrees walks <projectPath>/.fry-worktrees/ and removes each
// subdir via `git worktree remove --force`, falling back to a plain
// directory removal for entries git no longer tracks. Returns the
// number of dirs removed. Errors are reported as warnings to stderr
// but never cause clean to fail.
func cleanWorktrees(cmd *cobra.Command, projectPath string) int {
	root := filepath.Join(projectPath, config.GitWorktreeDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	removed := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		wtPath := filepath.Join(root, ent.Name())
		gitCmd := exec.Command("git", "worktree", "remove", "--force", wtPath)
		gitCmd.Dir = projectPath
		if err := gitCmd.Run(); err != nil {
			// git didn't know about this worktree (or couldn't remove
			// it). Fall back to a plain rm -rf.
			if rmErr := os.RemoveAll(wtPath); rmErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "fry: warning: could not remove worktree %s: %v\n", wtPath, rmErr)
				continue
			}
		}
		removed++
	}
	if removed > 0 {
		// Prune git's view in case any of the removes left dangling state.
		pruneCmd := exec.Command("git", "worktree", "prune")
		pruneCmd.Dir = projectPath
		_ = pruneCmd.Run()
	}
	return removed
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "Skip confirmation prompt")
	cleanCmd.Flags().BoolVarP(&cleanYes, "yes", "y", false, "Auto-accept confirmation prompts")
}
