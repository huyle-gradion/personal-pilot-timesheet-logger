---
name: timesheet
description: Log today's work into the personal timesheet from real git, pull-request and calendar evidence. Run once per day when the user asks to "log today's work" or "log the timesheet", or on request for a past working day.
---

Run exactly what is written — no `--help`, flag discovery, or other repo docs; a command error names its fix. Once per day, never twice per time block (recording is idempotent per block). Runner-agnostic: Pi (demonstrated), Claude Code (`.claude/skills/timesheet` link), any shell runtime. Steps are **[Script]** (deterministic binary subcommands) or **[AI]** (judgment): only real decisions go to the model. Never: remote submission — approval-gated, stays with the interactive human flow (`pi/launch.mjs`); editing evidence, block times, or PR lists during review.

Inputs (resolve once; never commit): `TS_BINARY=<repo>/bin/timesheet`, `TS_CONFIG=$HOME/.timesheet/config.json`, `TS_ENV=$HOME/.timesheet/.env.local` (may be empty for local-only days). Never read, print, or copy the env file's contents; only the binary reads it (`--env-file`). If `bin/timesheet version` exits 0, never `go build` during a run; if it fails, rebuild: `go build -trimpath -o bin/timesheet ./cmd/timesheet`.

1. **[Script] Run**: one bounded collect attempt plus the draft (meetings; confirmed intervals outside them; unallocated topics; whole-day if none); re-runnable; a failed source is recorded honestly, never blocks the day:
   `"$TS_BINARY" run --config "$TS_CONFIG" --env-file "$TS_ENV" --date "$DAY" > "$TMP/draft.json"`
   stdout is the **entire** judgment input; redirect it to your run file; artifacts dir: `$TMPDIR/timesheet/$DAY/` (same bytes in `draft.json`; path on stderr).

2. **[AI] Review**: only `$TMP/draft.json` enters the judgment step (never raw `git log`, calendar payloads, earlier days); write ONLY `$TMP/descriptions.json`: `{"notes": "<one plain string>", "descriptions": {"<block_id>": "<prose>"}}`
   - One entry per block keyed by `block_id`: short grouped-topic summary of `topic_candidates` (combine small items); every block needs one (`record` names missing ids).
   - Do NOT reproduce draft fields (times, titles, candidates, flags, `prs_inline`) or a `PRs:` list; `record` appends `prs_inline` to the description itself.
   - `notes`: one plain JSON string (never an array): day-level remark (event that is not work, incomplete day per `flags`: `source_incomplete:*`, `no_confirmed_intervals`); omit if nothing to say; run-context only, not persisted; durable content goes in a block description.
   - Do not re-read the draft or write a reviewed file; `record` composes it (`--file reviewed.json` = recovery only).

3. **[Script] Record**: idempotent per block (re-run updates in place, never a second entry); entries: `<state dir>/entries/<date>.json` with source references:
   `"$TS_BINARY" record --config "$TS_CONFIG" --date "$DAY" --draft "$TMP/draft.json" --descriptions "$TMP/descriptions.json"`
   Composition rejection (missing/empty description, unknown `block_id`): fix the named block in `$TMP/descriptions.json`, re-run. `--file reviewed.json` recovery keeps the original contract: empty description, missing `PRs:` list, duplicate block.

4. **[Script] Verify**: confirm entries and descriptions:
   `"$TS_BINARY" status --config "$TS_CONFIG" --date "$DAY"`

5. **[Script] Token usage**: appends runtime-prefixed session totals to `benchmarks/tokens.csv` (idempotent per session); run the form matching the runtime (both if both ran); manual runs add no row:
   ```sh
   python3 scripts/tokens.py --pi-dir "$HOME/.pi/agent/sessions"   # Pi (default)
   python3 scripts/tokens.py                                        # Claude Code
   ```

Step 2: **≤ 1,000 tokens/day (input + output)**, cache separate, any runtime; input is exactly the day's draft (300–800 tokens); [Script] steps use no model tokens. Run fuses collect and draft. Measured: 113,187 → 50,751 (runs 1–2; labeled drops run-3/run-4); run-4 judgment artifact ~340 B/~100 tokens vs ~2.7 KB pre-#152.
