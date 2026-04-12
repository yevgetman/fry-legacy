# Git Strategy

Fry supports multiple **git strategies** for isolating build work. By default, Fry works directly on the current branch (`current` strategy). Use `--worktree` to isolate the build in a git worktree, or `--git-strategy` for fine-grained control.

## Strategies

| Strategy | Description |
|---|---|
| `auto` | Resolves to `current`. Equivalent to not passing `--git-strategy`. |
| `current` | Work directly on the current branch. No branch or worktree created. This is the default. |
| `branch` | Create a new git branch for the build. Switches to it before the first sprint. |
| `worktree` | Create an isolated git worktree under `.fry-worktrees/`. Build runs entirely inside the worktree. |

## CLI Flags

| Flag | Description |
|---|---|
| `--worktree` | Run build in a git worktree (shorthand for `--git-strategy worktree`) |
| `--git-strategy <auto\|current\|branch\|worktree>` | Git isolation strategy (default: `auto`, which resolves to `current`) |
| `--branch-name <name>` | Explicit branch name. Overrides auto-generated name. Cannot be used with `--git-strategy current`. |

`--worktree` and `--git-strategy` are mutually exclusive.

## Branch Names

Branch names follow the pattern `fry/<slug>` where `<slug>` is a lowercased, hyphenated version of the epic name (max 50 characters).

- Auto-generated from the epic `@epic` name (e.g., `@epic My REST API` -> `fry/my-rest-api`)
- Override with `--branch-name my-feature` to use an explicit name
- If no epic name is available, falls back to `fry/build`

## Continue / Resume Behavior

The resolved strategy is persisted to `.fry/git-strategy.txt` after setup. When `--continue` or `--resume` is used:

1. Fry reads `.fry/git-strategy.txt` to recover the strategy, branch name, and working directory
2. If both the original project and the persisted worktree contain build artifacts, Fry compares their state and prefers the more advanced/newer one
3. For `branch` strategy: checks out the existing branch
4. For `worktree` strategy: reattaches to the existing worktree directory only when its state is still canonical
5. For `current` strategy: no action needed

If the persisted strategy file does not exist (builds started before this feature), `--continue`/`--resume` defaults to `current`.

## Worktree Lifecycle

When strategy is `worktree`:

1. **Creation** -- a git worktree is created at `.fry-worktrees/<slug>/` with a new branch `fry/<slug>`
2. **Artifact copy** -- `.fry/` and `plans/` are copied from the original project directory into the worktree so the sprint runner finds all build artifacts
3. **Build execution** -- all sprint operations (agent runs, sanity checks, alignment, audit) happen inside the worktree directory
4. **Auto-merge on success** -- when the build completes successfully, Fry automatically merges the worktree branch into the original branch, removes the worktree, deletes the branch, and cleans up the strategy file. The log shows:
   ```
     GIT: merging worktree branch fry/my-rest-api into main...
     GIT: worktree merged and cleaned up
   ```
5. **Preservation on failure** -- if the build fails, the worktree is preserved for inspection. Fry prints the path and a removal command:
   ```
     GIT: worktree preserved at .fry-worktrees/my-rest-api
          To remove: git worktree remove .fry-worktrees/my-rest-api
   ```

The `.fry-worktrees/` directory is listed in `.gitignore`.

## Branch Strategy Lifecycle

When strategy is `branch`:

1. **Creation** -- a new branch `fry/<slug>` is created and checked out
2. **Build execution** -- all operations run in the same project directory, on the new branch
3. **Post-build** -- you remain on the feature branch. Merge or delete as needed.

If the branch already exists (and `--continue`/`--resume` is not set), Fry exits with an error suggesting `--branch-name` or manual deletion.

## Artifacts

| File | Purpose |
|---|---|
| `.fry/git-strategy.txt` | Persisted strategy for `--continue`/`--resume` reattachment |
| `.fry-worktrees/` | Parent directory for worktree checkouts (gitignored) |

## Examples

```bash
# Default — work directly on main/master
fry --user-prompt "add a REST endpoint"

# Isolate in a worktree
fry --worktree --user-prompt "build microservice architecture"

# Same thing, explicit flag
fry --git-strategy worktree --user-prompt "build microservice architecture"

# Use a branch instead
fry --git-strategy branch --user-prompt "add auth system"

# Use a specific branch name
fry --git-strategy branch --branch-name feature/auth-system

# Continue a build that used worktree strategy
fry --continue

# Resume on a specific sprint (reattaches to persisted strategy)
fry --resume --sprint 4
```

## Interaction with Other Flags

- `--continue` / `--resume`: reads persisted strategy from `.fry/git-strategy.txt`. Ignores `--git-strategy` if set (uses persisted value).
- `--dry-run`: strategy is resolved and displayed but no branch or worktree is created.
- `--no-project-overview`: skips triage confirmation (including git strategy display), but strategy resolution still applies.
- `--full-prepare`: bypasses triage but respects `--git-strategy` / `--worktree`.
