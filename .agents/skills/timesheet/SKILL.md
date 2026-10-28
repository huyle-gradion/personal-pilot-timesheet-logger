---
name: timesheet
description: Log today's work into the personal timesheet from real git, pull-request and calendar evidence. Run when the user asks to "log today's work" or "log the timesheet", or a past working day.
---

# Timesheet

The binary owns the pipeline; the model owns only the judgment it asks for and the human-gate report. Drive the day with one loop over `next` — never hold the step order in prose.

## Setup (once per invocation)

`SKILL_DIR` is this skill's absolute directory, supplied by the loader. Pass `--config <path>` / `--env-file <path>` to `next` only to override the defaults. Never read, print or copy the env file: only the binary reads it.

```sh
SKILL_DIR="<absolute directory of this loaded skill>"
TS_BINARY="$("$SKILL_DIR/scripts/ts-env")"       # must succeed; its error names the fix
TS_ROOT="$("$SKILL_DIR/scripts/ts-env" --root)"  # repository root, for the build and token script
```

`ts-env` resolves the repository from its own real path (symlinks included), then `<repo>/bin/timesheet`, `$TIMESHEET_ROOT/bin/timesheet`, or a `timesheet` on `PATH`. Prove the binary supports `next` before the loop; a found `bin/timesheet` may predate it:

```sh
"$TS_BINARY" next --help >/dev/null 2>&1 || {
  ( cd "$TS_ROOT" && go build -trimpath -o bin/timesheet ./cmd/timesheet )
  TS_BINARY="$TS_ROOT/bin/timesheet"
  "$TS_BINARY" next --help >/dev/null 2>&1
}
```

Use the Go on `PATH`, or `( cd "$TS_ROOT" && mise exec -- go build -trimpath -o bin/timesheet ./cmd/timesheet )` when the shell has none. If the check or build fails, stop and report it. Build only here, never mid-loop. A mise-managed Go is environment troubleshooting, not a project dependency.

## The loop

Invoke `"$TS_BINARY" next` (add `--date YYYY-MM-DD` initially for a past day; add `--project <id>` when the configuration defines several projects and one is chosen). It prints ONE JSON document. Capture `DAY` from the first report's `day`, then always resume with `next --date "$DAY"` and the same config/env/project overrides. This keeps a run on its original day across midnight. Obey exactly one `state` per report. Never compute the date from the host clock.

- `action` — run `action.cmd` as an argv vector: element 0 is the executable, the rest are its arguments, each passed as its own argument. Never join the vector into a shell string, never `eval` it, and never substitute a field into one. Use the absolute artifact paths the binary prints (`run` prints `run: artifacts: <path>`); invent no `$TMP` directory.
- `judgment` — do the model work below, writing only `judgment.answer_file`, then `next` again. Judgments are files: there is no `--answer` flag, and `--do` is refused. Copy every digest field from the report verbatim; never recompute it.
- `stopped` — print `reason` verbatim: it names the precise blocker and remedy. Never silently acknowledge a source, guess an attendance verdict, choose an identity binding, or reuse a stale plan. When the user has explicitly authorized the specific decision (the acknowledgement command shown, a meeting verdict, or a resolve decision file they authored), perform exactly that one step and invoke `next` again; otherwise stop.
- `human_gates` — run `human.status_command` exactly as the binary printed it (read-only submit-status; the binary shell-quotes it) and surface its unresolved writes and `next_safe_action`; then print `human.approve_command` and `human.submit_command` exactly and stop. Never run them: `approve`/`submit`/`resolve-write` are TTY-gated human steps, and attempting one is a contract violation.

**Failure discipline.** A failed action is never retried as-is. If `next` still reports the same `step` and `action.cmd`, repair only the judgment it consumes when the error is a validation error on `judgment.answer_file` (unknown block/interval id, missing description, missing or stale digest) — rewrite the answer with the current report's digest — and run it once more. Any other repeat is a blocker: print the binary's error and one line naming the action, then stop; never spin.

## Judgment

### `block_descriptions`

Read the draft path named in `judgment.protocol` (also `draft.json` beside `judgment.answer_file`). Judge only that draft's current blocks — never raw git/calendar/prior-day evidence or a remembered draft. Write to `judgment.answer_file`:

```json
{"draft_digest":"<judgment.draft_digest copied verbatim>","notes":"<string>","descriptions":{"<block_id>":"<prose>"}}
```

One grouped-topic entry per draft block; no draft fields, block times or `PRs:` list (`record` appends `prs_inline`); `notes` is one JSON string or omitted. A block may carry `outside_work_hours` + `work_hours_reason` (`day_not_in_policy`, `before_window`, `between_windows`, `after_window`, `partial`): informational only — out-of-policy time is allowed, so never reject, skip, shift or edit a block for it. Never alter evidence. A stale or missing `draft_digest` sends `next` back to this judgment; rewrite the whole answer with the current digest.

### `classifications`

Use only `judgment.blocks[]`: each entry's `interval_id`, `block_id`, `start`, `end` and `description`. The report is self-contained; do not read a draft, descriptions file or `$TMP` artifact for this judgment. Query the catalog per distinct `description`/topic with `"$TS_BINARY" classifications --query "<topic text>"`, carrying the same `--config`/`--env-file` flags given to `next`; the read is live (network + credential) and fails closed. Choose exactly one returned option per interval, then write to `judgment.answer_file`:

```json
{"ledger_digest":"<judgment.ledger_digest copied verbatim>","selections":{"<interval_id>":{"catalog_id":"<catalog_id>","classification_id":"<option id>"}}}
```

Copy `catalog_id` from the classifications receipt and `classification_id` from the chosen option's `id` verbatim; never invent an id or topic, silently default, or add fields the receipt does not carry. `{"selections":{}}` is the honest no-op when nothing fits, and still carries the exact `ledger_digest`. `next` emits the `classify` action only after the file's `ledger_digest` matches the current staged ledger; never run `classify` on an unbound or stale file.

## Tokens (optional, non-blocking)

When the runner keeps transcripts, record the day's usage with the repository script; a failure never affects the day or the gates:

```sh
[ -n "$TS_ROOT" ] && python3 "$TS_ROOT/scripts/tokens.py" --pi-dir "$HOME/.pi/agent/sessions" || true
```

Keep judgment within **1,000 tokens/day (input + output)**, cache separate; every [Script] step spends none.
