# Codebase Awareness

Fry can scan, understand, and learn from existing codebases. When `fry init` is run in a directory with an existing project, Fry detects it, indexes the files, and builds a semantic understanding document. This context feeds into every subsequent build, and Fry accumulates project-specific knowledge with each run.

## How It Works

### 1. Detection

`fry init` auto-detects existing projects using heuristics:

- Git history has more than 1 commit
- A known project marker exists (`go.mod`, `package.json`, `Cargo.toml`, `requirements.txt`, `pyproject.toml`, `Gemfile`, `pom.xml`, `build.gradle`, `CMakeLists.txt`, `composer.json`, `mix.exs`, `Package.swift`, `pubspec.yaml`, `*.sln`)
- The directory contains more than 10 non-hidden files

No LLM call is needed for detection — it's purely deterministic.

### 2. Structural Scan

A fast, no-LLM scan that produces `.fry-config/file-index.txt`:

- **File tree** — walks the directory respecting `.gitignore` (via `git ls-files`)
- **Languages** — detected from project markers (high confidence) and file extension counts (medium confidence)
- **Frameworks** — detected from dependency manifests (React, Express, Cobra, Flask, etc.)
- **Entry points** — common main/index/app/server files
- **Test directories** — directories containing test files or named `tests/`, `__tests__/`, etc.
- **Dependencies** — parsed from `go.mod`, `package.json`, `requirements.txt`
- **Git history** — recent commits, frequently changed files, top contributors

### 3. Semantic Scan

A Sonnet-class LLM analyzes the structural snapshot plus key file contents to produce `.fry-config/codebase.md`:

- **Summary** — what the project is, what it does
- **Architecture** — how it's organized, key modules, data flow
- **Tech stack** — languages, frameworks, dependencies, build tools
- **Key files** — the files that matter most and why
- **Conventions** — naming, error handling, test patterns, import grouping
- **Entry points** — where execution starts
- **Dependencies** — external services, APIs, libraries
- **Test structure** — how tests are organized, how to run them
- **Git activity** — recent development focus, frequently modified areas
- **Gotchas** — things that aren't obvious from reading individual files

Use `--heuristic-only` to skip the semantic scan and only run structural heuristics.

### 4. Pipeline Integration

When `.fry-config/codebase.md` exists, it is automatically used throughout the build:

| Integration Point | How |
|-------------------|-----|
| **Sprint prompts** | Layer 0.5 (CODEBASE CONTEXT) — injected before executive context |
| **Sprint code review / build-audit prompts** | Injected as architecture and convention context for review and remediation |
| **Prepare pipeline** | Included in plan, epic, and sanity check generation |
| **Triage** | Included in complexity classification |
| **File index** | Auto-refreshed on each `fry run` if stale |

### 5. Codebase Memories

After each build, Fry extracts project-specific learnings into `.fry-config/codebase-memories/`:

- **Extraction** — A Haiku-class (cheap) LLM analyzes the build's observer scratchpad, events, sprint results, git diff, and audit findings to extract codebase-specific learnings
- **Storage** — Each learning is a small `.md` file with frontmatter (confidence, source build, date, reinforced count)
- **Deduplication** — Before writing, new memories are compared against existing ones using word overlap (>=80% similarity). Duplicates increment the `reinforced` counter instead of creating new files
- **Prompt injection** — Layer 0.75 (CODEBASE MEMORIES) in sprint prompts, sorted by confidence then reinforcement, capped at 10KB
- **Compaction** — When memories exceed 50, an LLM merges/prunes to ~20. Reinforced memories survive preferentially

### 6. Incremental Updates

After builds complete:

- If `.fry-config/codebase.md` exists and significant changes were made (>=5 files), it's incrementally updated
- If `.fry-config/codebase.md` doesn't exist (from-scratch project after first build), it's generated for the first time

### 7. Persistence

`.fry-config/codebase.md`, `.fry-config/file-index.txt`, and `.fry-config/codebase-memories/` live in `.fry-config/` which is not affected by archive/clean. Running `fry init` again rescans the codebase.

## Artifacts

| Path | Description | Lifecycle |
|------|-------------|-----------|
| `.fry-config/codebase.md` | Semantic codebase understanding | Persistent — lives in `.fry-config/`, updated incrementally |
| `.fry-config/file-index.txt` | Structural file index with stats | Persistent — auto-refreshed on `fry run` if stale |
| `.fry-config/codebase-memories/` | Accumulated learnings from builds | Persistent — grows over time, compacted at 50+ |

## Commands

```bash
# Full scan (structural + semantic) on existing project
fry init

# Structural scan only, no LLM call
fry init --heuristic-only

# Override engine for semantic scan
fry init --engine codex

# Rescan an already-initialized project
fry init  # Just run it again
```

## Prompt Layers

The codebase awareness features add two new layers to the sprint prompt:

| Layer | Name | Source |
|-------|------|--------|
| 0.5 | CODEBASE CONTEXT | `.fry-config/codebase.md` |
| 0.75 | CODEBASE MEMORIES | `.fry-config/codebase-memories/*.md` |

These appear before all other layers, giving the agent ground truth about the existing codebase before it reads the plan or sprint instructions.
