# Agent Foundation

Fry includes an agent foundation layer (`internal/agent/`) that provides the building blocks for programmatic interfaces to the Fry build engine.

## CLI Commands

### `fry status --json`

Returns structured JSON build state.

```bash
$ fry status --json --project-dir ~/code/myproject
{
  "active": true,
  "project_dir": "/Users/yev/code/myproject",
  "epic": "REST API Build",
  "effort": "standard",
  "engine": "claude",
  "total_sprints": 4,
  "current_sprint": 2,
  "current_sprint_name": "API Endpoints",
  "status": "running",
  "last_event": {
    "type": "sprint_complete",
    "ts": "2026-03-27T10:31:45Z",
    "sprint": 1,
    "data": {"status": "PASS", "heal_attempts": "0"}
  },
  "git_branch": "fry/rest-api",
  "started_at": "2026-03-27T10:00:00Z",
  "pid": 12345
}
```

### `fry events`

List or stream build events from the observer event log.

```bash
# List all events
$ fry events --project-dir ~/code/myproject

# Stream events in real-time (follows events.jsonl)
$ fry events --follow --json --project-dir ~/code/myproject
{"ts":"2026-03-27T10:31:45Z","type":"sprint_complete","sprint":1,"data":{"status":"PASS"}}
{"ts":"2026-03-27T10:35:00Z","type":"sprint_start","sprint":2,"data":{"name":"API Endpoints"}}
```

### `fry agent prompt`

Print the agent system prompt. This is the canonical prompt that makes any LLM "speak Fry" — it includes the artifact schema, build lifecycle, event types, identity, and conversation patterns.

```bash
$ fry agent prompt
# Identity
...
# Role
...
# Build Lifecycle
...
```

---

## Go Packages

### `internal/agent/` — Agent Foundation

Types:
- **`BuildState`** — Structured representation of a running or completed build
- **`BuildEvent`** — A single event from the observer event stream
- **`ArtifactInfo`** — Description of a Fry build artifact (for prompt generation)

Functions:
- **`ReadBuildState(projectDir)`** — Assembles state from `.fry/` artifacts
- **`ReadProgress(projectDir, scope)`** — Reads sprint or epic progress
- **`ReadLatestLog(projectDir, logType, lines)`** — Reads recent build logs
- **`TailEvents(ctx, projectDir)`** — Follows events.jsonl, returns Go channel
- **`ReadAllEvents(projectDir)`** — Reads all events at once
- **`ArtifactSchema()`** — Returns complete artifact manifest
- **`BuildAgentSystemPrompt()`** — Generates the Fry agent system prompt

### `internal/steering/` — Build Steering (Layer 1)

File-based IPC for mid-build intervention. The runtime reads steering signals and settles deterministic resume points. Atomic writes prevent partial directives or checkpoints.

Functions:
- **`ConsumeDirective(projectDir)`** — Atomically read and delete the directive file (rename + read + remove)
- **`ReadDirective(projectDir)`** — Read directive without consuming (for inspection)
- **`ClearDirective(projectDir)`** — Delete the directive file
- **`IsHoldRequested(projectDir)`** — Check if the hold sentinel exists
- **`ClearHold(projectDir)`** — Remove the hold sentinel
- **`WriteDecisionNeeded(projectDir, prompt)`** — Create the decision-needed file (atomic write)
- **`ClearDecisionNeeded(projectDir)`** — Remove the decision-needed file
- **`WaitForDecision(ctx, projectDir)`** — Block until a directive appears (polls every 2s via ticker)
- **`IsPaused(projectDir)`** — Check if the pause sentinel exists
- **`ClearPause(projectDir)`** — Remove the pause sentinel
- **`RequestExit(projectDir)`** — Create `.fry/exit-request.json` for `fry exit`
- **`ReadStopRequest(projectDir)`** — Resolve the effective graceful-stop signal (`fry exit` request or legacy pause sentinel)
- **`WriteResumePoint(projectDir, point)`** — Persist `.fry/resume-point.json` for deterministic pickup
- **`ReadResumePoint(projectDir)`** — Read the settled resume checkpoint
- **`ClearStopRequest(projectDir)`** — Remove the graceful-stop request artifacts
- **`CleanupAll(projectDir)`** — Remove all steering files (called at build completion)

---

## Build Steering Artifacts

These files are created by the steering system:

| File | Purpose | Written By | Read By |
|------|---------|-----------|---------|
| `.fry/agent-directive.md` | User directive for next iteration | External agent or human | Sprint loop |
| `.fry/agent-hold-after-sprint` | Hold flag (sentinel) | External agent or human | Inter-sprint loop |
| `.fry/agent-pause` | Legacy pause flag (sentinel) | External agent or human | Sprint/alignment/review control flow |
| `.fry/exit-request.json` | Structured graceful-exit request | `fry exit` | Runtime graceful-exit checkpoints |
| `.fry/resume-point.json` | Settled resume checkpoint | Fry runtime | `--continue`, `--simple-continue`, humans, agents |
| `.fry/decision-needed.md` | Build waiting for human input | Sprint loop | External agent or human |

## Build Steering Events

| Event | When | Key Data |
|-------|------|----------|
| `directive_received` | Sprint loop read a directive | `preview` |
| `decision_needed` | Build holding for user decision | `reason`, `completed_sprint`, `remaining_sprints` |
| `decision_received` | User responded to hold | `preview` |
| `build_paused` | Build stopped at a settled checkpoint | `sprint`, `phase`, `detail` |
