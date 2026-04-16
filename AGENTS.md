# AGENTS.md — Instructions for LLMs Working on the Fry Codebase

> **Audience:** AI coding agents (Claude, Codex, Copilot, etc.) tasked with modifying, extending, or fixing code in this repository. Read this file before making any changes.

---

## 1. Project Overview

Fry is a Go 1.22 CLI tool that orchestrates AI agents through multi-sprint build loops. It lives in a single module (`github.com/yevgetman/fry`) with two direct dependencies: cobra (CLI) and testify (testing). The binary compiles to `bin/fry`. See `README.LLM.md` for the full architectural map.

---

## 2. Mandatory Checks Before Submitting Work

### Always run the test suite

Every change — no matter how small — must pass the full test suite before being considered complete:

```bash
make test
```

This runs `go test -race ./...` which includes race condition detection. Do not skip `-race`. If tests fail, fix them before moving on. If your change requires new behavior, write or update tests first (see Section 7).

### Always run the build

Confirm the binary compiles cleanly:

```bash
make build
```

### Lint when available

```bash
make lint
```

If `golangci-lint` is not installed, at minimum ensure `go vet ./...` passes.

### Run orchestrator tests after orchestrator changes

If you modify any file in `.self-improve/` (orchestrator, config, prompts), run the orchestrator test suite:

```bash
bash .self-improve/test-orchestrator.sh
```

This tests config loading, category classification, label mapping, and syntax validation. All tests must pass before committing orchestrator changes.

### Always run a second pass on your own work

Before declaring a task complete, review the full change set again. Treat this as a mandatory quality pass, especially for high-complexity work: look for missed bugs, edge cases, regressions, unclear logic, incomplete tests, and documentation drift, then fix anything you find.

### Always complete the post-task workflow

After the code and documentation are done and all required checks pass:

1. `git add` the relevant files
2. Create a commit with a focused message
3. If you used a worktree for the task, merge that branch back into `master`; do not merge, modify, or remove unrelated branches or worktrees. `cd` out of the worktree directory before removing it so you do not get stuck in a deleted path
4. Run `make install`
5. Push the resulting branch or `master` to the remote
6. Clean up only the worktree you created for the task

---

## 3. Documentation Requirements

### Update documentation when features change

This is not optional. When you add, modify, or remove a feature:

1. **`README.md`** — Update the relevant section (commands table, "Key mechanisms" list, Quick Start examples, Requirements). If you add a new docs file, add it to the Documentation table.

2. **`docs/` files** — Update the specific documentation file for the feature you changed. Every feature has a corresponding doc:
   - CLI flags/commands → `docs/commands.md`
   - Effort levels → `docs/effort-levels.md`
   - Epic directives → `docs/epic-format.md`
   - Engine changes → `docs/engines.md`
   - Sanity checks → `docs/sanity-checks.md`
   - Sprint execution → `docs/sprint-execution.md`
   - Alignment → `docs/alignment.md`
   - Audits → `docs/sprint-audit.md`, `docs/build-audit.md`
   - Review/replan → `docs/sprint-review.md`
   - Docker → `docs/docker.md`
   - Preflight → `docs/preflight.md`
   - Planning mode → `docs/planning-mode.md`
   - Media assets → `docs/media-assets.md`
   - Text assets → `docs/supplementary-assets.md`
   - User prompts → `docs/user-prompt.md`
   - Project layout → `docs/project-structure.md`
   - Terminal output → `docs/terminal-output.md`
   - Architecture → `docs/architecture.md`
   - Codebase awareness → `docs/codebase-awareness.md`

3. **`README.LLM.md`** — Update if the change affects the directory layout, core types, execution flow, CLI flags, key constants, or any section documented there. This file is the AI agent reference and must stay accurate.

4. **Templates** — If you change epic directives, sanity check syntax, or agent instructions, update the corresponding files in `templates/` (`epic-example.md`, `verification-example.md`, `GENERATE_EPIC.md`).

### Documentation style

- Technical, concise, active voice, imperative mood for instructions
- Use **bold** for directives (`@epic`), file paths, and command names
- Use backtick code spans for flags (`--effort`), tokens, file patterns
- Use tables for reference material (directives, flags, comparisons)
- Use fenced code blocks with language identifiers (`bash`, `go`, `markdown`)
- Cross-link related docs: `[Topic](other-doc.md)` (relative paths within `docs/`)
- No fluff — every sentence should carry information

---

## 4. Go Code Conventions

### Package organization

- `cmd/fry/main.go` — Entry point only. Calls `cli.Execute()` and nothing else.
- `internal/cli/` — Cobra command definitions. All user-facing CLI logic goes here.
- `internal/<feature>/` — One package per feature domain (e.g., `epic`, `sprint`, `verify`, `heal`, `audit`, `review`, `engine`, `prepare`, `git`, `docker`, `preflight`, `lock`, `log`, `media`, `assets`, `summary`, `shellhook`, `textutil`, `continuerun`, `archive`, `copilot`).
- `internal/config/` — Constants only. No logic, no functions.
- `templates/` — Embedded markdown templates (compiled into binary via `//go:embed`).

### Naming

- **Packages:** lowercase, single word (`epic`, `sprint`, `verify`)
- **Exported functions:** PascalCase, descriptive (`RunSprint`, `ParseEpic`, `AssemblePrompt`)
- **Unexported helpers:** camelCase (`stripPromptBleed`, `gitDiffStat`)
- **Constants:** PascalCase in grouped blocks (`DefaultMaxHealAttempts`, `StatusPass`)
- **Interfaces:** noun or role name, not `-er` suffix when it reads better (`Engine`, not `Runner`)
- **Type aliases:** named types for semantic clarity (`type EffortLevel string`, `type CheckType int`)

### Import grouping

Three groups separated by blank lines:

```go
import (
    "context"
    "fmt"
    "os"

    "github.com/spf13/cobra"

    "github.com/yevgetman/fry/internal/config"
    frylog "github.com/yevgetman/fry/internal/log"
)
```

Use aliases only when there is a namespace collision (e.g., `frylog` for `internal/log` vs standard `log`).

### Error handling

- **Wrap with context:** `return fmt.Errorf("parse epic line %d: %w", lineNo, err)`
- **Non-fatal warnings:** `fmt.Fprintf(os.Stderr, "fry: warning: %v\n", err)` — then continue
- **Distinguish not-found:** check `os.IsNotExist(err)` separately from other errors
- **Exit codes from subprocesses:** use `errors.As(err, &exitErr)` pattern (see `engine/claude.go`)
- **Never panic.** Return errors up the call chain.

### File operations

- Build paths with `filepath.Join(projectDir, config.SomeConstant)` — never hardcode separators
- Create directories with `0o755`, files with `0o644`
- Use `os.MkdirAll()` for nested directories
- Always check errors on file writes, even to temporary files

### Concurrency

- Use `context.Context` for cancellation — pass it through every function that may block
- Check `ctx.Done()` in loops: `select { case <-ctx.Done(): return ctx.Err() default: }`
- Use `sync.Once` for one-time cleanup (e.g., lock release)
- Use `sync.Mutex` for shared mutable state
- Use `exec.CommandContext()` for subprocess timeouts

### Process execution

- Always use `exec.CommandContext(ctx, ...)` — never bare `exec.Command()`
- Capture stdout and stderr to `bytes.Buffer`
- Distinguish between process errors and non-zero exit codes
- Set `cmd.Dir` to the working directory

---

## 5. Adding a New Feature

Follow this checklist:

1. **Create the package** in `internal/<feature>/` with a main file (e.g., `feature.go`)
2. **Define public types** at the top of the file
3. **Add constants** to `internal/config/config.go` if the feature has configurable values (file paths, defaults, invocation prompts)
4. **Implement the logic** — keep the package focused on one responsibility
5. **Write tests** in `<feature>_test.go` in the same package (see Section 7)
6. **Wire into CLI** in `internal/cli/` — add flags, subcommands, or call the new package from the run/prepare flow
7. **Update documentation** — the relevant `docs/` file, `README.md`, `README.LLM.md`, and `AGENTS.md` if new conventions are introduced
8. **Run `make test && make build`** to confirm everything works

### Adding a new engine

1. Create `internal/engine/<name>.go`
2. Implement the `Engine` interface: `Run(ctx, prompt, opts) (output, exitCode, error)` and `Name() string`
3. Add the engine name to the `switch` in `NewEngine()` and `ResolveEngine()` in `engine.go`
4. Update `docs/engines.md` and the validation error message

### Adding a new epic directive

1. Add parsing logic to `internal/epic/parser.go` (handle the `@directive` in the appropriate state)
2. Add the field to `Epic` or `Sprint` struct in `internal/epic/types.go`
3. Set default value in the parser's finalization block
4. If the directive has a config default, add it to `internal/config/config.go`
5. Update `docs/epic-format.md` (directives table)
6. Update `templates/epic-example.md` and `templates/GENERATE_EPIC.md`
7. Add test cases in `internal/epic/parser_test.go`

### Adding a new CLI flag

1. Add the flag in `internal/cli/root.go` (persistent) or the specific command file
2. Thread the value through to the feature code
3. Update `docs/commands.md` (flags table and examples)
4. Update `README.md` if the flag is significant
5. Add integration test cases in `internal/cli/integration_test.go`

---

## 6. Modifying Existing Code

- **Read the code first.** Understand what exists before changing it. Read the file, read its tests, read the doc.
- **Minimal changes.** Fix or add what's needed — don't refactor surrounding code, add comments to code you didn't write, or "improve" things that aren't part of your task.
- **Preserve existing patterns.** If a file uses table-driven tests, add table entries. If it uses `require.NoError`, don't switch to `assert.NoError`. Match the surrounding style.
- **Don't break the public API.** Exported function signatures and type definitions are the contract. If you must change them, update all callers.
- **Update the corresponding `_test.go` file** for any behavioral changes. If a function's behavior changes, its tests must reflect the new behavior.

---

## 7. Testing Standards

### Every package must have tests

If you create a new package or add a new exported function, write tests. No exceptions.

### Test file conventions

- Test files live alongside source: `internal/verify/runner.go` → `internal/verify/runner_test.go`
- Same package name (not `_test` external package)
- Import `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require`

### Test function structure

```go
func TestFunctionName(t *testing.T) {
    t.Parallel()  // REQUIRED on every test function

    // Arrange
    dir := t.TempDir()
    // ... setup

    // Act
    result, err := SomeFunction(args)

    // Assert
    require.NoError(t, err)
    assert.Equal(t, expected, result)
}
```

### Rules

- **Always call `t.Parallel()`** as the first line of every test function
- **Use `t.TempDir()`** for file I/O — never write to the working directory
- **Use `t.Helper()`** in test helper functions so failure messages point to the caller
- **Use `t.Setenv()`** for environment variable overrides (auto-cleaned up)
- **Use `require` for preconditions** (fatal on failure) and `assert` for the actual assertions under test (non-fatal)
- **Test failure paths** — not just happy paths. Verify error messages, edge cases, and boundary conditions.
- **No test should depend on another test's state.** Each test must be independently runnable.
- **Run with race detection:** `go test -race ./...` — this is what `make test` does

---

## 8. Git Practices

### Commit messages

- Imperative present tense: "Add feature", "Fix bug", "Remove unused code"
- Describe **what and why**, not how: "Add --user-prompt-file flag to load user prompt from a file"
- One logical change per commit
- **Copilot-authored commits**: any commit produced by `fry run --copilot` while the copilot is fixing a canonical fry bug must use the `[copilot]` prefix and include the full provenance template documented in [docs/copilot.md](docs/copilot.md). This is what makes copilot interventions findable via `git log --grep='\[copilot\]'` and lets reviewers distinguish them from human-authored commits.

### What not to commit

- `.env` files (secrets)
- `.fry/` directory (generated artifacts)
- `plans/` directory (user-specific)
- `build-docs/` directory
- Build output (`bin/`, the `fry` binary at root)
- `build-audit.md` (generated)
- `build-summary.md` (generated)
- `.fry-archive/` directory (archived builds)
- These are all in `.gitignore` — respect it

### Branching

- `master` is the main branch
- Create feature branches for non-trivial changes
- Keep commits clean and focused

### Use git worktrees for large feature changes

When a task involves adding a new feature, introducing a new package, implementing a multi-file change, or otherwise touching several parts of the codebase, **work in a git worktree** rather than directly on the current checkout. This keeps the user's working tree clean and makes it easy to review or discard the work.

Use your judgement — a worktree is appropriate when:
- Adding a new CLI command, engine, or epic directive (multiple files + tests + docs)
- Refactoring that spans more than 2–3 files
- Any change that could leave the tree in a broken state mid-implementation

A worktree is **not** needed for:
- Small bug fixes (one or two files)
- Documentation-only changes
- Adding test cases to an existing test file
- Tweaking constants or config values

When using a worktree, create a descriptive branch name (e.g., `feature/add-ollama-engine`) and ensure all mandatory checks (`make test && make build`) pass inside the worktree before presenting the work.

When finishing work in a worktree, merge only the task's branch back into `master`, push the updated branch state to the remote, and then remove only that task worktree. Do not disturb unrelated branches or worktrees. `cd` out of the worktree directory before cleaning it up.

---

## 9. Architecture Invariants

These are design decisions that must be preserved:

1. **Single binary, zero runtime dependencies.** Fry compiles to one static binary. Do not add dependencies that require external runtimes, shared libraries, or runtime downloads. The only external tools are git, bash, and the AI engine CLIs — and those are the user's responsibility.

2. **Minimal Go dependencies.** Currently only cobra and testify. Do not add new dependencies without strong justification. Prefer stdlib solutions.

3. **Config is constants-only.** `internal/config/config.go` contains `const` blocks. No functions, no init(), no logic. If you need computed defaults, put them in the consuming package.

4. **Templates are embedded.** All `templates/*.md` files are compiled into the binary via `//go:embed`. Do not read templates from the filesystem at runtime.

5. **The epic parser is a state machine.** It processes one line at a time through three states (`stateGlobal`, `stateSprintMeta`, `stateSprintPrompt`). Additions must fit this model — don't restructure the parser.

6. **Engine interface is minimal.** Two methods: `Run()` and `Name()`. Keep it that way. Engine-specific configuration goes in the engine implementation, not the interface.

7. **Progress tracking uses two files.** `sprint-progress.txt` (per-sprint, unbounded append) and `epic-progress.txt` (cross-sprint, compacted summaries). This separation is intentional for bounded context management — don't merge them.

8. **Prompt assembly is layered.** The 8-layer prompt structure in `sprint/prompt.go` has a specific order (layers 1, 1.25, 1.5, 1.75, 2, 3, 4, 5). New prompt content must fit into one of the existing layers or have a clear justification for a new layer.

9. **Sanity checks are independent of the AI agent.** Checks run in a separate process, not inside the agent. This separation is by design.

10. **Git checkpoints are automatic.** Every completed sprint gets a git commit. Do not add flags to skip this — it's a safety mechanism.

---

## 10. Common Patterns to Follow

### Adding a scanner/walker (like `assets/` or `media/`)

```go
func Scan(dir string) ([]Item, error) {
    var items []Item
    var totalSize int64

    err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
            return filepath.SkipDir
        }
        if d.IsDir() || !d.Type().IsRegular() {
            return nil
        }
        info, err := d.Info()
        if err != nil {
            return nil // skip unreadable
        }
        // Check limits
        if totalSize+info.Size() > maxTotalSize {
            return filepath.SkipAll
        }
        totalSize += info.Size()
        items = append(items, Item{Path: path, Size: info.Size()})
        return nil
    })
    if err != nil && !errors.Is(err, filepath.SkipAll) {
        return nil, fmt.Errorf("scan %s: %w", dir, err)
    }
    return items, nil
}
```

### Adding a CLI subcommand

```go
// internal/cli/mycommand.go
var myCmd = &cobra.Command{
    Use:   "mycommand",
    Short: "Brief description",
    RunE: func(cmd *cobra.Command, args []string) error {
        projectDir, _ := cmd.Flags().GetString("project-dir")
        // implementation
        return nil
    },
}

func init() {
    // Flags specific to this command
    myCmd.Flags().String("my-flag", "default", "Description")
}
```

Register in `root.go`:
```go
rootCmd.AddCommand(myCmd)
```

### Parsing a new directive in the epic parser

Add to the `switch` in the `stateGlobal` or `stateSprintMeta` block of `parser.go`:

```go
case "my_directive":
    ep.MyDirective = value
```

Set the default in the finalization section:
```go
if ep.MyDirective == "" {
    ep.MyDirective = config.DefaultMyDirective
}
```

---

## 11. Things to Avoid

- **Don't add global mutable state.** No package-level `var` that gets mutated at runtime (except `sync.Once` patterns for cleanup).
- **Don't use `init()` functions** except for cobra command registration in `internal/cli/`.
- **Don't read from stdin** unless implementing an interactive feature that the user explicitly requested.
- **Don't add `//nolint` directives** without a comment explaining why.
- **Don't use `interface{}` / `any`** when a concrete type will do.
- **Don't add logging to library packages** (`epic`, `verify`, `engine`). Logging belongs in `cli/` or the orchestration layer. Library packages return errors and let callers decide how to report.
- **Don't create new top-level directories** without updating `.gitignore` (if generated) and `docs/project-structure.md`.
- **Don't modify the Makefile** unless adding a genuinely new build target. The existing targets (`build`, `test`, `lint`, `clean`, `install`) cover standard workflows.
- **Don't leave dead code.** If you remove a feature, remove all of its code, tests, config constants, docs references, and CLI flags. Partial removal is worse than no removal.

---

## 12. Quick Reference

| Task | Command |
|------|---------|
| Build | `make build` |
| Test (with race detection) | `make test` |
| Lint | `make lint` |
| Install locally | `make install` |
| Clean build artifacts | `make clean` |
| Run without building | `go run ./cmd/fry` |
| Run specific test | `go test -race -run TestName ./internal/package/` |
| Check compilation | `go build ./...` |
| Vet | `go vet ./...` |
| Test orchestrator | `bash .self-improve/test-orchestrator.sh` |

| File | Purpose |
|------|---------|
| `README.md` | User-facing project documentation |
| `README.LLM.md` | AI agent codebase map (architecture, types, flow) |
| `AGENTS.md` | This file — LLM coding instructions |
| `docs/*.md` | Feature-specific documentation (21 files) |
| `internal/config/config.go` | All constants and defaults |
| `templates/*.md` | Embedded prompt/example templates |
| `.gitignore` | Tracks what should not be committed |
