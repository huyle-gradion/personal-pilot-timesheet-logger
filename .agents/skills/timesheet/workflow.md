# Timesheet daily workflow

Every step below is tagged **[Script]** (deterministic, performed by the
compiled binary or a fixed script — the same input always yields the same
output) or **[AI]** (needs judgment). Principle: deterministic work is a
script; only real decisions go to the model.

Fixed inputs (substitute once per machine; never commit their values):

```text
TS_BINARY=<repo>/bin/timesheet
TS_CONFIG=<owner timesheet config.json path>
TS_ENV=<owner .env.local path (may be an empty file for local-only days)>
```

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
   calendar payloads, or earlier days):

   - For each block, write `description`: one short grouped-topic summary
     of `topic_candidates` (combine small items; do not list every action).
     When `prs` is non-empty, end the description with the inline list:
     `PRs: #12 acme/api, #8 acme/web`.
   - Judge ambiguous calendar events: a block whose meeting title is not
     clearly work gets described as what it actually was, or flagged in
     `notes` if it should not count as work.
   - Judge completeness from `flags` (`source_incomplete:*`,
     `no_confirmed_intervals`): add a day-level `notes` line when the day
     looks incomplete rather than inventing content.
   - Never change `block_id`, `start`, `end`, `prs`, `sources`, or
     `completeness`; `record` rejects a dropped `PRs:` list.

   Write the edited draft (same schema, plus `description` per block) to
   `$TMP/reviewed.json`.

4. **[Script] Record the reviewed entries.**

   ```sh
   "$TS_BINARY" record --config "$TS_CONFIG" --date "$DAY" --file "$TMP/reviewed.json"
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
   python3 scripts/tokens.py                       # Claude Code transcripts (~/.claude)
   python3 scripts/tokens.py --pi-dir "$HOME/.timesheet/pi-profile"   # add Pi transcripts
   ```

   Appends the session's token totals (session id included, prefixed by
   runtime) to `benchmarks/tokens.csv`. Idempotent per session. Run whichever
   form matches the runtime that executed this workflow (both when both ran).

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
- If `record` rejects the reviewed draft (empty description, missing
  `PRs:` list, duplicate block), fix the named block and re-run record.
- Remote timesheet submission is out of scope for this skill: it stays on
  the interactive, human-approved workflow.
