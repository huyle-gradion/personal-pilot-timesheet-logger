# Timesheet daily workflow

Every step below is tagged **[Script]** (deterministic, performed by the
compiled binary or a fixed script — the same input always yields the same
output) or **[AI]** (needs judgment). Principle: deterministic work is a
script; only real decisions go to the model.

**Run exactly what is written.** This file is the complete procedure: the
commands below show every flag they need. Do not run `--help`, discover
flags, or read other repository documentation mid-run — the first agent
run spent tokens on exactly that (see the improvement log, 2026-10-25).
If a command fails, read its error message; it names the fix.

Fixed inputs (resolve once per machine; never commit their values). On a
normal setup these are already:

```text
TS_BINARY=<repo>/bin/timesheet
TS_CONFIG=$HOME/.timesheet/config.json
TS_ENV=$HOME/.timesheet/.env.local   # may be an empty file for local-only days
```

Binary currency: if `bin/timesheet version` exits 0, the binary is current —
never run `go build` during a run; only rebuild if `version` fails.

## Steps

1. **[Script] Collect the day's evidence.**

   ```sh
   "$TS_BINARY" collect --config "$TS_CONFIG" --env-file "$TS_ENV" --date "$DAY"
   ```

   One bounded collection attempt over GitHub and calendar sources. The
   output reports per-source status (complete/incomplete/failed). A failed
   source is recorded honestly; it never blocks the day's entry.

2. **[Script] Build the compact draft.**

   ```sh
   "$TS_BINARY" draft --config "$TS_CONFIG" --env-file "$TS_ENV" --date "$DAY" > "$TMP/draft.json"
   ```

   Deterministic partitioning into time blocks: calendar meetings, confirmed
   work intervals outside meetings, the span of unallocated evidence
   topics, or one whole-day block when the day has no time anchors. This
   draft JSON is the **entire** input of the judgment step.

3. **[AI] Review the draft** (`$TMP/draft.json` only — never raw `git log`,
   calendar payloads, or earlier days) by writing ONLY the descriptions file
   `$TMP/descriptions.json`. It is the judgment step's entire output:

   ```json
   {"notes": "<one plain string for the whole day>",
    "descriptions": {"<block_id>": "<description prose>"}}
   ```

   - For each block in the draft, write one `descriptions` entry keyed by
     that block's `block_id`: one short grouped-topic summary of
     `topic_candidates` (combine small items; do not list every action).
     Every block needs an entry; if one is missing, `record` names the
     offending block ids.
   - Do NOT reproduce draft fields (times, titles, candidates, flags,
     `prs_inline`) and do NOT write the `PRs:` list anywhere: when a block
     has `prs_inline`, `record` appends it to the description itself.
   - `notes` is one plain JSON string (never an array): a day-level remark —
     e.g. a calendar event that should not count as work, or an incomplete
     day per `flags` (`source_incomplete:*`, `no_confirmed_intervals`).
     Omit the field when there is nothing to say. The day-level `notes`
     line is run-context only: it is NOT persisted as durable entry data —
     anything that must persist goes into a block's `descriptions` entry.
   - Do not re-read the draft afterwards and do not write a full reviewed
     file: `record` composes it. (`--file reviewed.json` remains a recovery
     path only.)

4. **[Script] Record the reviewed entries.**

   ```sh
   "$TS_BINARY" record --config "$TS_CONFIG" --date "$DAY" --draft "$TMP/draft.json" --descriptions "$TMP/descriptions.json"
   ```

   Idempotent per time block: a re-run updates the same block in place and
   never creates a second entry for the same block, so a failed run retried
   still counts as one day. Entries land in `<state dir>/entries/<date>.json`
   with full source references for later checking.

5. **[Script] Verify the day.**

   ```sh
   "$TS_BINARY" status --config "$TS_CONFIG" --date "$DAY"
   ```

   Confirm the recorded entries and their descriptions before finishing.

6. **[Script] Record the day's token usage.**

   ```sh
   python3 scripts/tokens.py --pi-dir "$HOME/.pi/agent/sessions"   # Pi runs (default runner)
   python3 scripts/tokens.py                                        # Claude Code runs, if any (~/.claude)
   ```

   Appends the session's token totals (session id included, prefixed by
   runtime) to `benchmarks/tokens.csv`. Idempotent per session. Run the
   form matching the runtime that executed this workflow (both when both
   ran). Manual terminal runs consume no model tokens and add no row.

## Token budget

Target: **≤ 1,000 tokens per day for step 3 (input + output)**, cache
reported separately — regardless of which agent runtime executes it. The
input is exactly one day's draft JSON — typically 300–800 tokens — because
steps 1, 2, 4, 5 and 6 are scripts that consume no model tokens. Nothing
else (raw provider payloads, prior days, repository history) ever enters the
judgment step.

## Failure handling

- A failed `collect` or `draft` may be re-run; each run is one new
  collection attempt and recording stays idempotent.
- If `record` rejects the composition (missing/empty description or an
  unknown `block_id`), fix the named block in `$TMP/descriptions.json` and
  re-run record. On the `--file reviewed.json` recovery path the original
  contract holds: empty description, missing `PRs:` list, duplicate block.
- Remote timesheet submission is out of scope for this skill: it stays on
  the interactive, human-approved workflow.
