{{.Identity}}

# Final Summary Pass

You are running this prompt because the copilot session was either auto-
compacted or restarted. The continuous session memory you usually rely on
may be missing. Reconstruct your view of the build from disk and write
the final summary.

## State on disk

Build directory:  {{.BuildDir}}
Fry source dir:   {{.FrySourceDir}}
Started:          {{.StartedAt}}
Outcome (rough):  {{.Outcome}}

## Procedure

1. Read these files in order, then answer the questions below:
   - .fry/copilot/events.txt
   - .fry/copilot/scratchpad.md
   - .fry/copilot/interventions/ (every file)
   - .fry/build-status.json
   - .fry/build-summary.md (if present)
   - .fry/copilot/state-snapshot.json (final state)

2. Write .fry/copilot/final-summary.md with these exact headers:

   ## Outcome
   One sentence: did the build succeed, fail, or abort, and why?

   ## Anomalies Observed
   Bulleted list of every notable thing across all wakes, even ones you
   did not intervene on.

   ## Interventions Made
   Numbered list. For each:
     - what you did
     - why you did it
     - what changed (commit SHA if fry-source, file paths if artifact)
     - whether it appears to have worked

   ## Fry Bugs Found
   For each canonical fry bug fixed:
     - one-line description
     - commit SHA
     - whether pushed
     - whether build was restarted after the fix

   ## Manual Remediations
   For each build-artifact remediation:
     - one-line description
     - what file or process you touched

   ## Recommendations For Next Time
   What should fry do automatically so the copilot wouldn't need to
   intervene? These become candidates for fry features.

   ## Cost
   Token usage if available, else omit.

3. Run: fry copilot emit-event --type=copilot_final_summary \
     --data='{"outcome":"{{.Outcome}}"}'

4. Read .fry/copilot/cron.id and call CronDelete with that ID.

5. Run: fry copilot emit-event --type=copilot_cron_removed \
     --data='{"cron_id":"<id>"}'

6. Append to .fry/copilot/events.txt:
     {{.NowISO}}  Copilot exiting cleanly. See final-summary.md.

7. Exit the session.
