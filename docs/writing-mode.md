# Writing Mode

Writing mode re-orients Fry's sprint-based pipeline to produce human-language content -- books, guides, reports, documentation -- using the same orchestration, sanity check, and alignment mechanisms as software and planning modes.

Pass `--mode writing` to activate writing mode. The `--mode` flag replaces the boolean `--planning` flag (which is kept as a backwards-compatible alias for `--mode planning`).

## Usage

```bash
# Start from just a prompt
fry run --mode writing --user-prompt "Write a guide to Go concurrency"

# Generate artifacts only (review before running)
fry prepare --mode writing --user-prompt "Write a 10-chapter book on distributed systems"

# With existing plan files
fry run --mode writing

# With effort sizing
fry run --mode writing --effort high --user-prompt "Write a technical report on LLM inference optimization"
```

Claude is the default engine for both prepare and build stages in writing mode, so `--engine claude` is not required.

## Output Conventions

Writing mode outputs all deliverables to `output/` at the project root. Filenames use an ordered naming convention:

```
{sequence}--{name}.md
```

- **sequence** -- a zero-padded number (01, 02, 03...) indicating production order across all sprints
- **name** -- a descriptive kebab-case name

The final sprint instructs the AI agent to consolidate all individual files into a single `output/manuscript.md`.

Example directory layout:

```
your-project/
  plans/
    executive.md                        # INPUT: your executive context (optional)
    plan.md                             # INPUT: generated or authored plan
  output/                               # OUTPUT: all deliverables
    01--introduction.md
    02--fundamentals.md
    03--goroutines-and-channels.md
    04--synchronization-primitives.md
    05--patterns-and-best-practices.md
    06--conclusion.md
    manuscript.md                       # Combined final document
```

## Mode Comparison

| Aspect | `software` (default) | `planning` | `writing` |
|---|---|---|---|
| Default engine (prepare) | Claude | Claude | Claude |
| Default engine (build) | Claude | Claude | Claude |
| Sprint phasing | Scaffolding, Schema, Logic, Integration, E2E | Research, Analysis, Strategy, Planning, Synthesis | Outline, Draft, Revise, Polish, Assemble |
| Sprint deliverables | Source files, configs, tests | Analyses, strategies, plans | Chapters, sections, manuscript |
| Output directory | Project root | `output/` | `output/` |
| Naming convention | Standard source paths | `{seq}--{category}--{name}.md` | `{seq}--{name}.md` |
| Final artifact | Working software | Individual documents | `manuscript.md` |
| Audit criteria | Correctness, Security, Performance, Code Quality, Usability, Edge Cases | Domain-specific | Coherence, Accuracy, Completeness, Tone & Voice, Structure, Depth |
| Alignment instructions | Fix code, resolve build errors | Fix document gaps | Fix content gaps, improve prose |

## Copilot in Writing Mode

The [copilot](copilot.md) is mode-agnostic. When enabled (via `--copilot` or auto-enabled at `--effort=max`), it monitors writing-mode builds the same way it monitors software builds: tick on a cron, re-read the state snapshot, intervene only when something is genuinely stuck. Writing builds rarely need fry-source bug fixes (the build agent is doing prose work, not invoking compilers), so most copilot interventions in writing mode are artifact remediations — for example, cleaning up a partially-written `manuscript.md` that left a section in an inconsistent state, or answering a `decision-needed.md` request mid-build.

```bash
fry --mode writing --effort max --copilot --user-prompt "Write a 5,000-word essay on…"
```

## Sanity Checks

The same four [sanity check primitives](sanity-checks.md) work for writing-mode deliverables. Typical checks focus on file existence, heading structure, and word count minimums rather than builds or test suites.

### Example sanity checks

```
@sprint 1
@check_file output/01--introduction.md
@check_file_contains output/01--introduction.md "^# "
@check_cmd test $(wc -w < output/01--introduction.md) -ge 800

@sprint 2
@check_file output/02--fundamentals.md
@check_file_contains output/02--fundamentals.md "## "
@check_cmd test $(wc -w < output/02--fundamentals.md) -ge 1500

@sprint 5
@check_file output/manuscript.md
@check_cmd test $(wc -w < output/manuscript.md) -ge 5000
```

These checks ensure chapters exist, contain headings, meet minimum word counts, and that the final manuscript is assembled.

## Audit Criteria

In writing mode, the audit agent evaluates each sprint's output against six content-oriented criteria instead of the code-oriented defaults:

| Criterion | Description | Typical Severity |
|---|---|---|
| **Coherence** | Logical flow between sections; consistent narrative thread | HIGH if broken |
| **Accuracy** | Factual correctness of claims, examples, and references | CRITICAL if wrong |
| **Completeness** | All topics from the sprint prompt are covered at appropriate depth | HIGH if gaps |
| **Tone & Voice** | Consistent register, audience-appropriate language, no jarring shifts | MODERATE |
| **Structure** | Clear headings, logical section ordering, effective use of lists and examples | MODERATE |
| **Depth** | Sufficient detail and analysis; not superficial or padded | HIGH if shallow |

The severity levels follow the same blocking rules as software mode: CRITICAL and HIGH block the sprint; MODERATE is advisory. See [Sprint Code Review](sprint-audit.md) for the full review mechanics.

## Alignment

The [alignment](alignment.md) loop works identically in writing mode. When sanity checks fail, the alignment agent receives content-oriented fix instructions that reference the writing context (e.g., "the chapter is missing required headings" or "word count is below the minimum") rather than code-oriented messages.

## Effort Levels

[Effort levels](effort-levels.md) work the same in writing mode. A `fast` effort run produces 1-2 sprints for a short document; `max` produces extended sprints with thorough review and higher word-count expectations.

## Resuming a Writing Build

When a writing build is interrupted or fails (e.g., due to a critical audit), `fry run --continue` automatically restores the `writing` mode from the previous run. There is no need to pass `--mode writing` again:

```bash
fry run --continue                    # auto-detects writing mode
fry run --continue --mode software    # explicit override if needed
```

The mode is persisted to `.fry/build-mode.txt` at the start of every build and read back by `--continue`.

## Backwards Compatibility

The `--planning` flag is kept as an alias for `--mode planning`. Existing scripts and workflows that use `--planning` continue to work without changes.

```bash
# These are equivalent:
fry --planning
fry --mode planning
```

## See Also

- [Planning Mode](planning-mode.md) -- non-code document generation (analyses, strategies, plans)
- [Sanity Checks](sanity-checks.md) -- check primitives and outcome matrix
- [Sprint Code Review](sprint-audit.md) -- post-sprint semantic review
- [Effort Levels](effort-levels.md) -- sprint count and rigor control
- [Commands](commands.md) -- full CLI reference for `--mode` flag
