# Observer

The observer is Fry's metacognitive layer. It watches the build, writes structured reflections, and feeds those reflections into the consciousness session. Observer failures are non-fatal: they never fail the build.

## Architecture

The observer has four runtime components:

### Event stream

Mechanical JSON-line events written to **`.fry/observer/events.jsonl`**. These are structured runtime signals, not model output.

### Identity

Fry's identity is compiled into the binary from `templates/identity/`. The observer reads it for context only. Builds never mutate identity.

### Scratchpad

Working memory at **`.fry/observer/scratchpad.md`**.

Rules:

- new session: reset scratchpad
- resume session: keep existing scratchpad
- parse failure: leave scratchpad unchanged
- parse success with a delta: append the delta and record it in consciousness scratchpad history

### Wake-ups

Short model sessions at natural boundaries. Each wake-up reads:

- identity
- current scratchpad
- recent observer events
- wake-point-specific build metadata

The observer must return one JSON object with:

- `thoughts`
- `scratchpad`
- optional `directives`

## Wake-Up Schedule

Wake-ups are gated by [effort level](effort-levels.md).

| Effort | After Sprint | After Build Audit | Build End |
|---|---|---|---|
| `fast` | disabled | disabled | disabled |
| `standard` | no | no | yes |
| `high` | yes | yes | yes |
| `max` | yes | yes | yes |

## Structured Parse Rules

Observer output is parsed strictly. Fry never promotes raw engine transcripts into canonical observer thoughts.

Extraction order:

1. raw JSON
2. fenced JSON
3. last valid JSON object in the response

Checkpoint parse status is recorded as:

| Status | Meaning |
|---|---|
| `ok` | Raw output was already valid structured JSON |
| `repaired` | Fry recovered valid structured JSON from noisy output |
| `failed` | No valid structured output could be recovered |

On `failed`:

- canonical `thoughts` stay empty
- scratchpad is not updated
- the output is quarantined via the observer log path

## Consciousness Integration

Each successful wake-up produces a durable checkpoint in `.fry/consciousness/` immediately after the wake returns.

The observer itself owns:

- reading identity
- reading and writing scratchpad
- collecting recent events
- invoking the model
- parsing structured output

The consciousness layer owns:

- checkpoint persistence
- scratchpad history
- checkpoint distillation
- final build summary synthesis
- upload queueing

## Session Semantics

Observer initialization is mode-aware:

| Mode | Behavior |
|---|---|
| `InitNewSession` | Reset scratchpad and event log, then emit `build_start` with `mode=new` |
| `ResumeSession` | Preserve scratchpad and event log, then emit `build_start` with `mode=resume` |

This keeps observer continuity intact across `--resume`, `--continue`, interrupted restarts, and process restarts inside the same logical build.

## Directives

The observer can emit structured directives in the `directives` field:

| Type | Purpose |
|---|---|
| `WARN` | Flag a likely problem |
| `NOTE` | Record a neutral observation |
| `SUGGEST` | Propose an adjustment |

Directives are persisted with the checkpoint. Fry does not automatically execute them.

## CLI

Commands:

```bash
fry identity
fry identity --full
```

Flags:

- `--no-observer` disables observer events, wake-ups, checkpoints, and scratchpad updates
- `--effort fast` also disables the observer
- `--dry-run` also disables the observer

## File Locations

| File | Purpose | Persists |
|---|---|---|
| `.fry/observer/events.jsonl` | Structured event stream | Per logical build session |
| `.fry/observer/scratchpad.md` | Current scratchpad snapshot | Preserved on resume |
| `.fry/observer/wake-prompt.md` | Wake-up prompt | Deleted after use |
| `.fry/consciousness/scratchpad-history.jsonl` | Scratchpad delta history | Per logical build session |
| `~/.fry/experiences/build-<session-id>.json` | Long-term build experience record | Across builds |

## Relationship to the Copilot

The observer and the [copilot](copilot.md) are complementary, not redundant. The observer is a **passive metacognitive layer** that runs synchronously inside the build process at fixed wake-points and writes thoughts to disk. The copilot is an **active out-of-process intervention loop** that runs in a separate persistent agent session, polls a state snapshot every ~10 minutes, and edits real files (fry source for canonical bug fixes; build artifacts for tactical remediation).

Use the observer for memory and pattern recognition. Use the copilot for hands-on remediation when something goes wrong.

## Related Documentation

- [Consciousness](consciousness.md)
- [Effort Levels](effort-levels.md)
- [Sprint Execution](sprint-execution.md)
- [Copilot](copilot.md)
