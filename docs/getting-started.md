# Getting Started

## Prerequisites

- **git** — for automatic checkpointing
- **bash** — required by AI engine CLIs and sanity check commands
- **Go 1.22+** — to build from source
- One of the following AI engine CLIs:
  - **OpenAI Codex CLI**: `npm i -g @openai/codex`
  - **Claude Code CLI**: `npm i -g @anthropic-ai/claude-code`
- **Docker** (optional) — only needed if your epic uses `@docker_from_sprint`

## Installation

```bash
# From source
git clone https://github.com/yevgetman/fry.git
cd fry
make install    # builds and copies to ~/.local/bin/fry by default
# macOS: ad-hoc signs ~/.local/bin/fry after copying

# Or build without installing
make build      # outputs to bin/fry

# Or using go install
go install github.com/yevgetman/fry/cmd/fry@latest
```

## Your First Build

You can start a Fry build in three ways: with just a prompt, with a plan file, or with an executive document. Pick whichever fits your workflow.

### Option A: Start from a prompt (fastest)

No files needed. Describe what you want and Fry generates everything:

```bash
fry --user-prompt "build a REST API for a todo app with PostgreSQL and JWT auth" --engine claude

# Or load a longer prompt from a file
fry --user-prompt-file ./requirements.txt --engine claude
```

Fry will generate an executive context document from your prompt and present it for review. Type `y` to approve and Fry proceeds to generate `plans/executive.md`, `plans/plan.md`, and all build artifacts automatically.

### Option B: Write a plan file

Create a `plans/` directory with a detailed build plan:

```bash
mkdir -p plans
cat > plans/plan.md << 'EOF'
# My Project — Build Plan

**Stack:** Node 20, Express, PostgreSQL 16, TypeScript strict mode.

**Database tables:**
- users — id (UUID PK), email (UNIQUE), name, created_at
- posts — id (UUID PK), user_id (FK), title, body, published (bool), created_at

**API endpoints:**
- POST /users — create user
- GET /posts — list published posts, cursor pagination
- POST /posts — create post (authenticated)

**Tests:** Jest for unit tests, supertest for integration tests.
EOF

fry --engine claude
```

### Option C: Write an executive document

Write a higher-level document describing the project's purpose, business goals, target users, and scope. Fry generates the detailed plan from it:

```bash
mkdir -p plans
# Write plans/executive.md with your vision and goals
fry --engine claude
```

When both `executive.md` and `plan.md` are present, Fry feeds `executive.md` into every generation step so the AI understands *why* the project exists.

### What happens when you run

```bash
# Uses Claude for prepare and build (defaults) — auto-detects effort level
fry

# Or use Claude Code for both prepare and build
fry --engine claude

# For a simple task, use fast effort (1-2 sprints)
fry --effort fast

# For a critical build, use max effort (extended prompts, thorough reviews)
# This also auto-enables the build copilot — see step 5 below
fry --effort max --engine claude

# Force the copilot at any effort level (a parallel agent that monitors
# the build and intervenes for canonical fry bugs)
fry --copilot
```

Fry will automatically:
1. Detect that `.fry/epic.md` doesn't exist and run `fry prepare`
2. Generate `.fry/AGENTS.md` (operational rules for the AI)
3. Generate `.fry/epic.md` (sprint definitions)
4. Generate `.fry/verification.md` (independent sanity checks per sprint)
5. Parse the epic file and validate its structure
6. Run preflight checks
7. Execute each sprint until completion or max iterations
8. Run independent sanity checks after each sprint

### 3. Validate before running (recommended)

```bash
fry --dry-run
```

This parses the epic, checks prerequisites, and shows the sprint plan without executing anything.

### 4. If a build fails

When a sprint fails, Fry prints recovery commands:

```bash
Resume:   fry run --resume --sprint 4
Restart:  fry run --sprint 4
Continue: fry run --continue
```

- **`--continue`** (recommended) — uses an LLM agent to analyze build state and automatically determine where and how to resume. Restores the build mode from the previous run. See [Sprint Execution — Resuming Failed Builds](sprint-execution.md#resuming-failed-builds).
- **`--resume`** — skip iterations and go straight to sanity checks + alignment with more attempts. Use when the code is already written but checks are failing.
- **Resume** (no flags) — re-run the sprint from scratch if the approach was fundamentally wrong.

See [Alignment](alignment.md) for details.

### 5. Optional: enable the build copilot

For long, complex, or unattended builds you can enable the [copilot](copilot.md) — a parallel agent session that wakes on a cron and intervenes when something goes wrong:

```bash
# Force on at any effort level
fry --copilot

# Auto-enabled when --effort=max
fry --effort=max

# Combine with --yes for fully unattended runs
fry --copilot --effort=max -y
```

While the build runs, in a separate terminal:

```bash
fry copilot status              # one-shot snapshot of the copilot session
fry copilot tail --follow       # live narrative log
fry copilot attach              # exec into the live Claude Code session
```

The copilot auto-enables at `--effort=max`. Pass `--no-copilot` to opt out at any effort level. See [Copilot](copilot.md) for the full feature documentation.

## Adding Fry to an Existing Project

```bash
cd my-existing-project
mkdir -p plans
# Write plans/plan.md and/or plans/executive.md
fry --engine claude
```

Fry automatically creates the `.fry/` directory, initializes git (if needed), and sets up `.gitignore` entries on first run.

## Input File Options

| Setup | Behavior |
|---|---|
| Only `--user-prompt` or `--user-prompt-file` | Fry generates `executive.md` (with interactive review), then `plan.md`, then all build artifacts |
| Only `plans/plan.md` | Fry uses your plan directly for all generation |
| Only `plans/executive.md` | Fry auto-generates `plan.md` from your executive context (Step 0), then proceeds normally |
| Both files | Fry uses `executive.md` as alignment context alongside your detailed `plan.md` for better-aligned artifacts |
| `--user-prompt` + existing files | The user prompt is injected as a directive; existing files are used as-is |

When `plan.md` is auto-generated (from `executive.md` or from a user prompt), the LLM makes all design, architecture, and implementation decisions. The generated files are written to `plans/` so you can review them before building.

## Media Assets (Optional)

Place images, PDFs, fonts, data files, or other assets in a `media/` directory at the project root. Reference them in your plan (e.g., "use `media/logo.png` for the header") and Fry will include a categorized manifest in every prompt so the AI agent knows what assets are available and where to find them.

```bash
mkdir -p media
cp ~/designs/logo.png media/
cp ~/docs/wireframe.pdf media/
```

The agent can then copy or reference these files as instructed in the plan. See [Media Assets](media-assets.md) for details.

## Supplementary Assets (Optional)

Place text-based reference documents -- API specs, requirements, schemas, design notes -- in an `assets/` directory at the project root. Unlike `media/` files (which the AI sees as a path manifest), `assets/` files are **read in full** and their contents are injected into the prompts that generate `plans/plan.md` and `.fry/epic.md`.

```bash
mkdir -p assets
cp ~/docs/api-spec.yaml assets/
cp ~/docs/requirements.md assets/
```

Reference them in your plan (e.g., "follow the OpenAPI spec in `assets/api-spec.yaml`") and the AI will incorporate their content when generating the build plan and sprint decomposition. Once `epic.md` is generated, the asset contents are baked in and no longer used during sprint execution.

See [Supplementary Assets](supplementary-assets.md) for details on supported file types and size limits.
