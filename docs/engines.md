# AI Engines

Fry supports three interchangeable AI engines: **OpenAI Codex**, **Claude Code**, and **Ollama**. The engine determines which CLI tool is invoked to execute prompts.

## Supported Engines

### Codex (OpenAI)

A supported build engine for software (coding) mode. Requires the Codex CLI:

```bash
npm i -g @openai/codex
```

Invocation:
```
echo PROMPT | codex exec --dangerously-bypass-approvals-and-sandbox [--model MODEL] [FLAGS]
```

The prompt is passed via stdin.
For resumed non-interactive audit sessions, Fry uses:

```bash
echo PROMPT | codex exec resume --dangerously-bypass-approvals-and-sandbox --json SESSION_ID
```

### Claude (Anthropic)

Requires the Claude Code CLI:

```bash
npm i -g @anthropic-ai/claude-code
```

Invocation:
```
echo PROMPT | claude -p --dangerously-skip-permissions [--model MODEL] [FLAGS]
```

The prompt is passed via stdin.
For resumed non-interactive audit sessions, Fry uses:

```bash
echo PROMPT | claude -p --dangerously-skip-permissions --resume SESSION_ID --output-format json
```

### Ollama (Local Models)

Fry supports Ollama for running builds with locally hosted open-source models (Llama 3, Mistral, CodeLlama, Phi-3, Gemma, etc.) without any API keys or cloud dependencies.

**Prerequisites:** Install [Ollama](https://ollama.com) and pull at least one model:

```bash
brew install ollama
ollama pull llama3
```

**Invocation:**
```
echo PROMPT | ollama run <model>
```

The prompt is passed via stdin. The model name is the first positional argument. Use the `@model` directive or `--model` flag to specify the Ollama model name.

**Usage:**
```bash
fry --engine ollama
fry --engine ollama --model codellama
```

**Model validation:** Fry accepts any Ollama model name without static validation. If the model is not locally available, Ollama will error at runtime.

**Tier mapping:** All tiers (frontier, standard, mini, labor) default to `llama3`. Override per-sprint with `@model <model-name>` or globally with `--model`.

## Engine Resolution

The AI engine is resolved with this precedence (highest wins):

1. `--engine` CLI flag
2. `@engine` directive in the epic file
3. `FRY_ENGINE` environment variable
4. Stage-specific default (see below)

### Default Engines by Mode and Stage

| Mode | Prepare Stage | Build Stage |
|---|---|---|
| **Software** (default) | Claude | Claude |
| **Planning** (`--mode planning`) | Claude | Claude |
| **Writing** (`--mode writing`) | Claude | Claude |

Claude is the default engine for all modes and stages. Use `--engine codex` or `--prepare-engine codex` to explicitly select Codex for any stage.

These defaults apply only when no explicit engine is specified via CLI flag, epic directive, or environment variable.

## Cross-Engine Failover

After Fry exhausts the normal same-engine retry loop, Claude and Codex builds can fail over to each other on transient failures.

### What triggers failover

Failover is considered only after the active engine's exponential-backoff retries are exhausted, and only for transient failure classes:

- rate limits (`429`, `rate limit`, `overloaded`, `too many requests`)
- timeouts / `deadline exceeded`
- `500` / `502` / `503` / `504`
- service unavailable / temporary unavailability
- connection reset / refused / transport / upstream network failures

Fry does **not** fail over on deterministic configuration problems such as invalid model names, bad engine flags, missing authentication, or other hard setup errors.

### Sticky promotion

When the fallback engine succeeds once, Fry promotes it for the rest of the build. It does not switch back and forth between engines.

This sticky promotion applies across the rest of the pipeline: sprint execution, audit, review, replan, build summary, codebase scans, and other later engine sessions all stay on the promoted engine.

When this happens, Fry prints a failover message on the main CLI output, updates the build status engine field, and emits an `engine_failover` event into the observer stream so `fry monitor` can show the switch.

### Model remapping on failover

When Fry switches engines, it keeps using the same session-selection matrix described below. If the requested model is not valid for the fallback engine, Fry re-resolves the model for the fallback engine using the same `session × effort` rules.

Example:

- sprint build starts on Claude with `sonnet`
- Claude rate-limits after same-engine retries
- Fry fails over to Codex
- the sprint session is remapped to Codex's sprint tier for that effort level (for example `gpt-5.3-codex` or `gpt-5.4`)

### Flags

Use these persistent CLI flags on `fry run`, `fry prepare`, `fry audit`, `fry replan`, or `fry init`:

| Flag | Description |
|------|-------------|
| `--fallback-engine <claude\|codex\|ollama>` | Override the fallback engine. If omitted, Fry uses `claude -> codex` and `codex -> claude`. |
| `--no-engine-failover` | Disable cross-engine failover entirely and stay on the selected engine. |

Notes:

- Automatic implicit failover is only configured between Claude and Codex.
- Ollama has no implicit fallback target.
- If the fallback engine CLI is not installed or cannot be created, Fry logs a warning and continues on the primary engine.

## Mixing Engines

CLI flags take absolute precedence over all other engine settings. For example, use Codex for sprint execution while keeping Claude for preparation:

```bash
fry --engine codex
```

Or use Codex for both stages:

```bash
fry --prepare-engine codex --engine codex
```

The `--prepare-engine` flag controls which engine is used during `fry prepare` (artifact generation), while `--engine` controls which engine executes the sprints. These flags override `@engine` directives in the epic, the `FRY_ENGINE` environment variable, and the default.

### Review Engine

When [dynamic sprint review](sprint-review.md) is enabled, the reviewer session can use a separate engine:

```
@review_engine claude
@review_model claude-sonnet-4-6
```

## Automatic Model Selection (Tier System)

Fry automatically selects the best model for each agent session based on the **engine**, **effort level**, and **session type**. Models are organized into four tiers:

| Tier | Purpose | Claude | Codex | Ollama |
|------|---------|--------|-------|--------|
| **Frontier** | Most capable, highest cost | `opus[1m]` | `gpt-5.4` | `llama3` |
| **Standard** | Strong all-rounder | `sonnet` | `gpt-5.3-codex` | `llama3` |
| **Mini** | Fast, cost-efficient | `haiku` | `gpt-5.4-mini` | `llama3` |
| **Labor** | Cheapest, no reasoning needed | `haiku` | `gpt-5-codex-mini` | `llama3` |

To update models when new ones release, change only the tier mapping table in `internal/engine/models.go`.

### Session × Effort Rules

| Session | fast | standard | high | max |
|---------|-----|--------|------|-----|
| Sprint execution, Alignment, Audit fix, Review, Replan | Standard | Standard | Frontier | Frontier |
| Audit, Audit verify, Build audit (Claude) | Standard | Standard | Standard | Frontier |
| Audit, Audit verify, Build audit (Codex) | Mini | Standard | Frontier | Frontier |
| Build summary | Mini | Mini | Standard | Standard |
| Compaction | Labor | Labor | Labor | Standard |
| Continue analysis | Mini | Mini | Standard | Standard |
| Project overview | Labor | Labor | Labor | Labor |
| Prepare | Standard | Standard | Standard | **Frontier** |

Empty effort level defaults to "standard" behavior.

## Model Override

Epic directives override the automatic tier selection for their session group:

```
@model opus[1m]                # Overrides sprint execution + alignment + compaction + summary
@review_model sonnet            # Overrides sprint code review + build audit + continue analysis
@review_model claude-sonnet-4-6  # Overrides review + replan
```

The continue analysis session (`--continue`) uses `@review_model` if set, otherwise falls back to `@model`.

Or via `--model` flag for `fry replan`.

Aliases `@codex_model` and `@codex_flags` are supported for backward compatibility.

## Engine Flags

Pass extra CLI flags to the underlying engine:

```
@engine_flags --full-auto
```

These flags are appended to the engine invocation command.

## Copilot Engine Selection

The [copilot](copilot.md) is an independent engine session from the build. It can use a different engine than the one running the build:

```bash
fry run --engine=codex --copilot=claude     # build runs on codex; copilot runs on claude
fry run --engine=claude --copilot=codex     # build runs on claude; copilot runs on codex
fry run --copilot                           # both default to claude
fry run --copilot=auto                      # copilot inherits the build engine
```

Engine compatibility for the copilot:

| Engine | Persistent session mechanism | Notes |
|---|---|---|
| `claude` | Independent cron — bootstrap subprocess installs its own cron via CronCreate, writes cron ID to `.fry/copilot/cron.id`. Cron wakes `claude --resume <session-id>` every interval. | Recommended. The copilot is independent of fry main — it survives fry crashes and can restart the build via `fry run --continue`. |
| `codex` | Independent cron — bootstrap is `codex exec`, cron wakes `codex exec resume <session-id>` | Same independence model as claude. |
| `ollama` | Not supported | The copilot bootstrap will fall back to claude with a warning. |

The copilot engine and the build engine are resolved independently — they have separate `engine.RunOpts` and separate session IDs. See [Copilot](copilot.md) for the full feature documentation.

## Engine Interface

Internally, all three engines implement the same interface:

```go
type Engine interface {
    Run(ctx context.Context, prompt string, opts RunOpts) (output string, exitCode int, err error)
    Name() string
}
```

This means all Fry features (sprints, sanity checks, alignment, review) work identically regardless of which engine is selected.

## Audit Session Continuity

Fry supports explicit same-role session continuity for build audits on engines that expose resumable non-interactive sessions. Sprint code reviews use a single session by design and do not need session continuity.

| Engine | Build audit continuity | Mechanism |
|---|---|---|
| Claude | Supported | Fry captures `session_id` from JSON output and resumes later audit/fix calls with `--resume <session-id>` |
| Codex | Supported | Fry captures `thread_id` from JSONL output and resumes later audit/fix calls with `codex exec resume <thread-id>` |
| Ollama | Not supported | `ollama run` has no compatible persisted conversation/session ID for Fry's non-interactive audit loop |

Rules:

- Fry only resumes **same-role** audit sessions: audit-to-audit and fix-to-fix.
- Fry never resumes fix into verify. Verify stays stateless by design.
- Session IDs are explicit and file-backed under `.fry/sessions/`; Fry does not rely on "most recent session" heuristics.
- Same-role continuity is budgeted. Fry refreshes audit and fix sessions when they exceed per-role call, prompt-size, token, or carry-forward thresholds.
- When Fry refreshes a session, the next same-role call starts from a fresh session and receives a compact carry-forward summary of unresolved findings plus recent failed fix attempts.
- Build audit metrics record session refresh counts and refresh reasons in `.fry/build-logs/` and surface the refresh count in `.fry/build-status.json`.
- If session capture or resume fails, Fry silently falls back to the existing stateless behavior.

## Rate-Limit Resilience

All engine calls are automatically wrapped with retry logic that detects rate-limit errors and retries with exponential backoff. This prevents long-running builds from failing due to transient API throttling.

### How It Works

When an engine call fails, Fry inspects the output and error for rate-limit indicators:

- `rate_limit` / `rate limit`
- HTTP `429` status code
- `overloaded` errors
- `too many requests`
- `throttled`, `request limit`, `usage limit`, and `resource exhausted`
- `retry-after: N` (parsed; used as the backoff delay)
- `try again in N <unit>` (parsed for `ms`, `seconds`, `minutes`, and `hours`)

If a rate limit is detected, Fry waits and retries. If the output contains a `retry-after` value, that delay is used. Otherwise, exponential backoff kicks in.

### Backoff Sequence

| Attempt | Base Delay | With Jitter (25%) |
|---------|-----------|-------------------|
| 1       | 10s       | 7.5s -- 12.5s     |
| 2       | 20s       | 15s -- 25s        |
| 3       | 40s       | 30s -- 50s        |
| 4       | 80s       | 60s -- 100s       |
| 5       | 120s      | 90s -- 120s (cap) |

Total maximum wait before giving up: ~4.5 minutes.

### Defaults

| Parameter | Value |
|-----------|-------|
| Max retries | 5 |
| Base delay | 10 seconds |
| Max delay | 120 seconds |
| Jitter | 25% |

These are defined in `internal/config/config.go` as `RateLimitMaxRetries`, `RateLimitBaseDelaySec`, `RateLimitMaxDelaySec`, and `RateLimitJitter`.

### Ollama

Ollama runs models locally and does not hit HTTP rate limits. Rate-limit detection is skipped for the Ollama engine.

### Implementation

Rate-limit resilience is implemented as a `ResilientEngine` decorator that wraps any `Engine`. Sticky cross-engine promotion is implemented by a `FailoverEngine` decorator layered above the resilient engines. Both are transparent to callers (sprint execution, alignment, audit, review, etc.). See `internal/engine/resilient.go`, `internal/engine/failover.go`, `internal/engine/failure.go`, and `internal/engine/ratelimit.go`.

## MCP Server Configuration

Claude Code supports [MCP (Model Context Protocol)](https://modelcontextprotocol.io) servers that provide additional tools to the agent during execution (e.g., LSP diagnostics, AST search/replace, Python REPL).

### Configuration

Pass an MCP server configuration file via the `--mcp-config` flag or `@mcp_config` epic directive:

```bash
fry --mcp-config ./mcp-servers.json
```

Or in an epic file:

```
@mcp_config ./mcp-servers.json
```

### Resolution Order

1. `--mcp-config` CLI flag (highest priority)
2. `@mcp_config` epic directive
3. Claude Code's auto-discovery of `.mcp.json` in the project directory (no Fry configuration needed)

The MCP config path is resolved to an absolute path before being passed to Claude Code.

### Engine Support

Only the Claude engine supports MCP. The `--mcp-config` flag and `@mcp_config` directive are silently ignored when using Codex or Ollama engines.
