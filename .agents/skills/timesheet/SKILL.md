---
name: timesheet
description: Log today's work into the personal timesheet from real git, pull-request and calendar evidence. Run when the user asks to "log today's work" or "log the timesheet", or a past working day.
---

Run exactly what is written - no flag discovery; errors name their fix. **Every invocation runs the whole pipeline, steps 1-9, in order - never stop after `record`/`status`/`stage`:** the day ends recorded, saved, planned and reported, with only the human gates of step 9 left. Once per day, never twice per block (idempotent). Steps are **[Script]** or **[AI]** (judgment). Never: the TTY-gated `approve`/`submit`/`resolve-write` (human-only; attempting them is a contract violation, and the binary refuses non-TTY shells before any remote call); editing evidence, block times, or PR lists during review.

Inputs (resolve once; never commit): `TS_BINARY=<repo>/bin/timesheet` - the CLI defaults to `$HOME/.timesheet/config.json` and, when it exists, `$HOME/.timesheet/.env.local`; pass `--config <path>`/`--env-file <path>` only to override - never read/print/copy the env file; only the binary reads it. If `bin/timesheet version` fails: `go build -trimpath -o bin/timesheet ./cmd/timesheet`; else never build mid-run.

The day: omit `--date` and the binary resolves today in the configured timezone - never compute the date yourself from the host clock. Pass `--date YYYY-MM-DD` only for a past day. After `run`, the draft's `date` field is the day every later step uses.

Steps 5-8 command shape: `"$TS_BINARY" <cmd> --date "$DAY"` (except classifications), one process, exclusive store lock (never beside serve); ids verbatim from receipts. Pipeline: run -> record -> stage -> classifications/classify -> confirm -> save -> plan -> [reconcile/resolve if blockers] -> plan (new plan_id) -> submit-status; the human then approves and submits.

1. **[Script] Run** (re-runnable; a failed source never blocks the day): `"$TS_BINARY" run > "$TMP/draft.json"` - stdout is the entire judgment input; `DAY` comes from the draft's `date` field (never from the host clock). For a past day add `--date YYYY-MM-DD`.

2. **[AI] Review**: judge ONLY `$TMP/draft.json` (never raw git log/calendar/prior days); write ONLY `$TMP/descriptions.json`: `{"notes": "<string>", "descriptions": {"<block_id>": "<prose>"}}` - one grouped-topic entry per block; no draft fields or `PRs:` list (`record` appends `prs_inline`); `notes` = one JSON string or omit; `--file reviewed.json` = recovery only. A block may carry `outside_work_hours` + `work_hours_reason` (`day_not_in_policy`, `before_window`, `between_windows`, `after_window`, `partial`): informational only - outside main hours is allowed, so never reject, skip, or shift a block for it.

3. **[Script] Record** (idempotent per block): `"$TS_BINARY" record --date "$DAY" --draft "$TMP/draft.json" --descriptions "$TMP/descriptions.json"` - rejection (missing/empty description, unknown `block_id`) => fix, re-run. Re-collected day => re-record with `--replace` (file = day's complete content; absent blocks removed). **[Script] Verify**: `"$TS_BINARY" status --date "$DAY"` - also where the `collection_id`s used in step 7 come from.

4. **[Script] Tokens**: `python3 scripts/tokens.py` (add `--pi-dir "$HOME/.pi/agent/sessions"` on Pi); both if both ran; appends to `benchmarks/tokens.csv` (idempotent).

5. **[Script] Stage**: staged intervals re-derive from the entries ledger (entries/ = human log AND staging source); no start+end => skipped, times never invented. Refused as already finalized => the step is done, unless the entries ledger changed since the save (e.g. a `--replace` re-record); then `stage --revise`, which re-derives and re-finalizes at a new revision.

6. **[Script]+[AI] Classify**: `"$TS_BINARY" classifications --query <text>` (live: network+credential; the default env file supplies it when it exists); **[AI]** picks; **[Script]** `classify --date "$DAY" --file choices.json`; edits later => stage+classify.

7. **[Script] Confirm + Save**: `confirm` = source gate - incomplete collect => `acknowledge --source <github|calendar> --collection-id <id from status or the run stderr>`, proposed meetings => `decide --meeting-id <id> --decision <attended|skipped> --collection-id <id>`, then confirm again; then `save`. After save the day is final; further stage/confirm/save re-runs are refused (`stage --revise` is the only way to change a finalized day).

8. **[Script] Plan** (live, read-only; prints plan_id/ops/blockers). Blockers => `reconcile` (fresh read-only observation); **[AI/human]** `resolve --file decision.json` (adopt|accept_observed|foreign; ids verbatim from the reconcile receipt); any resolve => plan again for a NEW plan_id (never reuse the old one). The agent's pipeline ends at the first blocker-free plan.

9. **[Script] Report, then stop at the human gates.** Run `submit-status` (read-only, not gated) and surface any unresolved writes/next_action for the day. `approve`, `submit --approval-id <id>`, `resolve-write --file <f>` are human-only [Script] prompts needing tty stdin+stdout and the exact plan_id/approval_id/write_ref typed - a deliberate-act barrier, not human-presence proof. The agent MUST NOT attempt them (attempting = contract violation). End EVERY run by printing the exact two commands for the human's own terminal, with `$DAY` filled in:
   - `"$TS_BINARY" approve --date "$DAY"` - shows the plan; type the displayed plan_id to confirm.
   - `"$TS_BINARY" submit --date "$DAY" --approval-id <approval_id from the approve receipt>` - type that approval_id to confirm.
   Failure discipline: never auto-retry a submit; uncertain/unidentified/in_flight stay unresolved until human resolution (submit-status shows next_action; resolve-write needs a fresh reconcile/plan observation); persistence_failed is not consumption (the approval stays active); post-save entries divergence is caught by the plan content hash.

Step 2 bound: **<= 1,000 tokens/day (input + output)**, cache separate; [Script] steps: no model tokens.
