# Epic File Format

An epic file defines global configuration and a sequence of sprint blocks. It can be auto-generated from your plan via `fry prepare` or hand-authored.

## Global Directives

Placed before any `@sprint` block:

```
@epic My Project Phase 1
@engine codex
@effort standard
@docker_from_sprint 2
@docker_ready_cmd docker compose exec -T postgres pg_isready -U myapp
@docker_ready_timeout 30
@require_tool node
@require_tool docker
@preflight_cmd npm --version
@pre_sprint npm install
@pre_iteration npm run lint:fix
@model gpt-4.1
@engine_flags --full-auto
@verification .fry/verification.md
@review_between_sprints
@max_deviation_scope 3
```

### Directive Reference

| Directive | Description |
|---|---|
| `@epic <name>` | Display name for logs and summaries |
| `@engine <codex\|claude\|ollama>` | AI engine (default: claude). See [AI Engines](engines.md). |
| `@effort <fast\|standard\|high\|max>` | Effort level — controls sprint count, density, and review rigor (default: auto-detect). See [Effort Levels](effort-levels.md). |
| `@docker_from_sprint <N>` | Start docker-compose from sprint N |
| `@docker_ready_cmd <cmd>` | Custom health check after docker-compose up |
| `@docker_ready_timeout <s>` | Health check timeout in seconds (default: 30) |
| `@require_tool <name>` | CLI tool that must be on PATH (repeatable) |
| `@preflight_cmd <cmd>` | Custom check before build starts (repeatable) |
| `@pre_sprint <cmd>` | Run before each sprint starts |
| `@pre_iteration <cmd>` | Run before each agent invocation |
| `@model <model>` | Override the agent model (alias: `@codex_model`) |
| `@engine_flags <flags>` | Extra CLI flags for the agent (alias: `@codex_flags`) |
| `@mcp_config <path>` | Path to MCP server configuration file (Claude engine only). See [Engines: MCP](engines.md#mcp-server-configuration). |
| `@verification <file>` | Sanity checks file (default: `.fry/verification.md`) |
| `@max_heal_attempts <N>` | Auto-alignment attempts after sanity check failure (default: effort-level default or 3). When explicitly set, overrides effort-level behavior and disables progress detection. Set to 0 to disable alignment. **Ignored at `max` effort** — max effort always uses unlimited progress-based alignment regardless of this directive (a warning is logged if you try to set it). |
| `@max_fail_percent <N>` | Maximum percentage of checks that can fail while still passing the sprint (default: 20; 0 = strict, 100 = always pass). See [Sanity Checks](sanity-checks.md). |
| `@compact_with_agent` | Use AI agent to summarize sprint progress (default: mechanical extraction) |
| `@review_between_sprints` | Enable mid-build sprint review (default: disabled) |
| `@review_engine <codex\|claude\|ollama>` | AI engine for reviewer session (default: same as `@engine`) |
| `@review_model <model>` | Model override for the reviewer session |
| `@max_deviation_scope <N>` | Maximum sprints a single deviation can touch (default: 3; auto-expanded to `totalSprints` for all non-fast effort levels, capped at 10) |
| `@audit_after_sprint` | Enable post-sprint semantic audit (default: enabled). See [Sprint Audit](sprint-audit.md). |
| `@no_audit` | Disable post-sprint semantic audit. See [Sprint Audit](sprint-audit.md). |
| `@max_audit_iterations <N>` | Maximum audit→fix cycles per sprint (default: 3) |
| `@audit_engine <codex\|claude\|ollama>` | AI engine for audit/fix sessions (default: same as `@engine`) |
| `@audit_model <model>` | Model override for audit/fix sessions |

## Sprint Blocks

Each sprint is defined as a block with metadata and a prompt:

```
@sprint 1
@name Scaffolding & Infrastructure
@max_iterations 20
@promise SPRINT1_DONE
@max_heal_attempts 5
@prompt
Sprint 1: Scaffolding for My Project.

Read .fry/AGENTS.md first, then plans/plan.md.

Build:
1. package.json with TypeScript, Express, pg dependencies
2. tsconfig.json with strict mode
3. src/index.ts — minimal Express server
4. docker-compose.yml — PostgreSQL 16

Verify:
Sanity checks are defined in .fry/verification.md (sprint 1).
- npm run build succeeds
- npm test passes
- Docker services start and become healthy

CRITICAL: This is a TypeScript project. No JavaScript files.

If stuck after 10 iterations: check import paths and tsconfig paths.

===PROMISE: SPRINT1_DONE=== when all checks pass.
@end
```

### Sprint Directives

| Directive | Required | Description |
|---|---|---|
| `@sprint <N>` | Yes | Sprint number (must be sequential starting from 1) |
| `@name <name>` | Yes | Sprint display name |
| `@max_iterations <N>` | Yes | Maximum agent iterations (must be > 0) |
| `@promise <TOKEN>` | Yes | Completion signal token |
| `@max_heal_attempts <N>` | No | Override global alignment attempts for this sprint |
| `@prompt` | Yes | Begins the multi-line prompt block |
| `@end` | Yes | Ends the prompt block |

### Sprint Prompt Structure (7-Part Convention)

1. **Opener** — "Sprint N: [What] for [Project]."
2. **References** — "Read .fry/AGENTS.md, then plans/plan.md [relevant sections]."
3. **Build list** — Numbered, specific: exact filenames, function signatures, SQL DDL
4. **Constraints** — "CRITICAL: [thing that will go wrong if ignored]."
5. **Sanity checks** — Reference to `.fry/verification.md` checks, plus prose summary of key outcomes
6. **Stuck hint** — "If stuck after N iterations: [most likely cause + fix]."
7. **Promise** — "Output `===PROMISE: TOKEN===` when [exit criteria]."

## Validation Rules

The epic parser enforces:
- At least one sprint must be present
- Sprint numbers must be sequential (1, 2, 3, ...)
- Each sprint must have `@name`, `@max_iterations` > 0, `@promise`, and prompt content
- When `@effort` is set, sprint count must not exceed the level's maximum (fast: 2, standard: 4, high/max: 10)

## Manual Epic Authoring

If you prefer to generate the epic with a different LLM (ChatGPT, Claude web, etc.):

1. Run `fry prepare --validate-only` to check prerequisites
2. Use the embedded `epic-example.md` template as a format reference
3. Save your epic as `.fry/epic.md`
4. Validate: `fry --dry-run`

## Sprint Sizing Guidelines

| Layer | Typical Sprints | Max Iterations | Files |
|---|---|---|---|
| Scaffolding/config | 1 (always first) | 15-20 | 5-12 |
| Schema/migrations | 1-2 | 15-20 | 5-15 |
| Domain models/types | 1-2 | 20-25 | 5-20 |
| Business logic | 1-2 | 25-30 | 10-20 |
| API handlers/routes | 1-2 | 20-25 | 5-15 |
| Wiring/integration/E2E | 1 (always last) | 30-35 | 5-15 |

Aim for 4-10 total sprints. If a sprint would produce 30+ files, split it. If it would produce 1-3 files, merge it with an adjacent sprint.
