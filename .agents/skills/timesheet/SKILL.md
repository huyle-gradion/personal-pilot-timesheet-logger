---
name: timesheet
description: Log today's work into the personal timesheet from real git, pull-request and calendar evidence. Run when the user asks to "log today's work" or "log the timesheet", or a past working day.
---

Run exactly what is written - no flag discovery; errors name their fix. Once per day, never twice per block (idempotent). Steps are **[Script]** or **[AI]** (judgment). Never: the TTY-gated `approve`/`submit`/`resolve-write` (human-only); editing evidence, block times, or PR lists during review.

Inputs (resolve once; never commit): `TS_BINARY=<repo>/bin/timesheet`, `TS_CONFIG=$HOME/.timesheet/config.json`, `TS_ENV=$HOME/.timesheet/.env.local` - never read/print/copy the env file; only the binary reads it (`--env-file`). If `bin/timesheet version` fails: `go build -trimpath -o bin/timesheet ./cmd/timesheet`; else never build mid-run.

1. **[Script] Run** (re-runnable; a failed source never blocks the day): `"$TS_BINARY" run --config "$TS_CONFIG" --env-file "$TS_ENV" --date "$DAY" > "$TMP/draft.json"` - stdout is the entire judgment input.

2. **[AI] Review**: judge ONLY `$TMP/draft.json` (never raw git log/calendar/prior days); write ONLY `$TMP/descriptions.json`: `{"notes": "<string>", "descriptions": {"<block_id>": "<prose>"}}` - one grouped-topic entry per block; no draft fields or `PRs:` list (`record` appends `prs_inline`); `notes` = one JSON string or omit; `--file reviewed.json` = recovery only.

3. **[Script] Record** (idempotent per block): `"$TS_BINARY" record --config "$TS_CONFIG" --date "$DAY" --draft "$TMP/draft.json" --descriptions "$TMP/descriptions.json"` - rejection (missing/empty description, unknown `block_id`) => fix, re-run. Re-collected day => re-record with `--replace` (file = day's complete content; absent blocks removed). **[Script] Verify**: `"$TS_BINARY" status --config "$TS_CONFIG" --date "$DAY"`.

4. **[Script] Tokens**: `python3 scripts/tokens.py` (add `--pi-dir "$HOME/.pi/agent/sessions"` on Pi); both if both ran; appends to `benchmarks/tokens.csv` (idempotent).

Step 2 bound: **<= 1,000 tokens/day (input + output)**, cache separate; [Script] steps: no model tokens.

**Submission tail** - commands: `"$TS_BINARY" <cmd> --config "$TS_CONFIG" [--env-file "$TS_ENV"] --date "$DAY"` (except classifications/resolve-write), one process, exclusive store lock (never beside serve); ids verbatim from receipts. Flow: run -> record -> stage -> classifications/classify -> confirm -> save -> plan -> [reconcile/resolve if blockers] -> plan (new plan_id) -> approve -> submit -> submit-status.

5. **[Script]** `stage`: staged intervals re-derive from the entries ledger (entries/ = human log AND staging source); no start+end => skipped, times never invented.
6. **[Script]** `classifications --query <text>` (live: network+credential); **[AI]** picks; **[Script]** `classify --file choices.json`; edits later => stage+classify.
7. **[Script]** `confirm` = source gate (incomplete collect => `acknowledge --source <github|calendar> --collection-id <id from status or the run stderr>`; meetings => `decide --meeting-id <id> --decision <attended|skipped> --collection-id <id>`); `save`.
8. **[Script]** `plan` (live, read-only; plan_id/ops/blockers). Blockers => `reconcile`; **[AI/human]** `resolve --file decision.json` (adopt|accept_observed|foreign; receipt ids); any resolve => plan again.
9. **Human gates - agent stops.** `approve`, `submit --approval-id <id>`, `resolve-write --file <f>` are human-only [Script] prompts needing tty stdin+stdout and the exact plan_id/approval_id/write_ref typed - a deliberate-act barrier, not human-presence proof. The agent MUST NOT attempt them (attempting = contract violation). `submit-status` is read-only, not gated.
10. **Failure discipline**: never auto-retry a submit; uncertain/unidentified/in_flight stay unresolved until human resolution (submit-status shows next_action; resolve-write needs a fresh reconcile/plan observation); persistence_failed is not consumption (the approval stays active); post-save entries divergence is caught by the plan content hash.
