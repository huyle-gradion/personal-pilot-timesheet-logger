# Timesheet Pipeline Workflow

This document defines the exact command sequence the `timesheet` skill runs. It is
a harness-agnostic orchestration over the `timesheet` Go binary: **every step is a
documented invocation**, and no accounting or submission logic is reimplemented
here.

- **`[Script]`**: deterministic Go code invoked through `cmd/timesheet`.
- **`[AI]`**: the optional, bounded editorial agent invoked *by the core* through a
  configured command adapter. The skill never synthesizes text itself.

Set `TS="go run ./cmd/timesheet"` (or the built binary) and `CFG=timesheet.json`.
Default to today in the configured timezone; pass `--date` or `--from/--to` to
target specific days. `--offline` never contacts the remote.

---

## Step 1: `[Script]` Preflight

Validate the timezone, resolve credential references (Keychain in memory), and
validate the enabled sources' **configuration** (required config keys, secret
references, and each plugin's declared contract). Fixture-based verification is a
**test input**, not runtime configuration: `preflight` runs **no** fixtures and
does **not** prove live source availability (the fetch happens in Step 2). A
`mode:"local"` git source passes offline with no GitHub credential. A non-NotFound
Keychain failure, an invalid configuration, or a missing remote git credential is
**terminal**: the run refuses and makes zero remote requests. There is no REST
fallback.

`--deep` additionally validates ALL configured sources (including disabled ones,
reported skipped).

```bash
$TS preflight --config "$CFG"
```

**Output Seam**: a per-check `ok`/`FAIL` list with stable codes (the source check
is named `configuration`). Exit code `3` when refused.

---

## Step 2: `[Script]` Collect and propose

Collect evidence from the enabled sources, merge it into the day's workflow
record, and print the deterministic proposal. Collection proceeds independently of
review or submission. Nothing is confirmed.

```bash
$TS review --config "$CFG" --date "$DATE"
```

**Output Seam**: the proposed work/meeting/manual blocks with their stable block
ids (used by later decisions).

---

## Step 3: `[AI]` Editorial synthesis (optional)

When an editorial agent is configured and enabled, `review` asks the core to
synthesize a title and description per block. The core:

- builds a bounded, non-secret request (no times, attendance, or classification),
- validates the agent's response **fail-closed** (a whole-response rejection on any
  unexpected field),
- reuses cached text for an unchanged request,
- enforces the soft daily token target (1000 tokens, counting retries and reruns),
- falls back to deterministic text on any failure, so review is **never blocked**.

The editorial text is TEXT ONLY and is applied to block descriptions by the core;
it can never alter intervals, attendance, projects, evidence, or approvals. Disable
it with `--no-editorial`.

---

## Step 4: `[Script]` Apply review decisions

Apply a serializable decision set (confirm, exclude, join, trim, project, manual,
acknowledge, mark-empty) through the same `internal/review` actions an interactive
review uses. The decisions file may contain a `{date}` placeholder for a range.

```bash
$TS review --config "$CFG" --date "$DATE" --decisions decisions.json
```

Example `decisions.json`:

```json
{
  "confirm": [{"block_id": "<BLOCK_ID>", "project": "Proj"}],
  "exclude": [{"evidence_id": "<EVIDENCE_ID>", "reason": "user_excluded"}],
  "trim": [{"block_id": "<BLOCK_ID>", "new_start": "09:15", "new_end": "09:45"}],
  "acknowledge": ["cal"]
}
```

**Output Seam**: the day is reviewed (or reviewed-empty) and persisted.

---

## Step 5: `[Script]` Submission preview

Build the exact create/edit plan from the reviewed record and the freshly observed
remote state, and persist it. This performs **zero remote writes** (a read-only
re-observe is allowed).

```bash
$TS preview --config "$CFG" --date "$DATE"
```

**Output Seam**: `plan_id: <PLANID>` and the operation count. The plan id is the
plan's content hash.

---

## Step 6: `[Script]` Approval

Bind an approval to the exact plan content hash. The approval is single-use: any
local, plan, or remote change invalidates it.

```bash
$TS approve --config "$CFG" --date "$DATE" --plan "$PLANID"
```

**Output Seam**: `approval_id: <APPROVALID>` (equal to the plan id).

---

## Step 7: `[Script]` Submit and export

Execute the record's **active** approval and then update the managed daily-note
section (read-only with respect to the remote).

```bash
$TS submit --config "$CFG" --date "$DATE"
```

- No active approval, or a requested `--approval` that does not match it, or an
  offline run: the submission is **blocked with zero writes** (exit code `3`).
- Confirmed operations continue; a definitive failure is recorded and the run
  continues; an **uncertain** write is recorded and **stops** the run (never
  retried). Partial success re-plans only the remaining operations, so confirmed
  work is never replayed.
- After execution the core rewrites only the marker block in
  `<vault>/<daily-dir>/<date>.md`, preserving everything else byte-for-byte.

Reconcile an unresolved operation with an explicit decision (never a bare absence):

```bash
$TS resolve --config "$CFG" --date "$DATE" --decisions resolve.json
```

Re-render the managed note without submitting (read-only with respect to the
remote):

```bash
$TS export --config "$CFG" --date "$DATE"
```

where `resolve.json` is an array of `{"operation_key": "...", "kind": "adopt"|"not_applied", "remote_id": "...", "local_id": "..."}`.

**Output Seam**: durable per-operation outcomes and the exported daily note.

---

## Optional Step: `[Script]` Metrics (non-blocking)

This step is **optional and non-blocking**. It records token usage for the day so
the 3.5 optimization can be measured (3.6). A failure here prints a warning and
**does not** change any workflow exit code and **never** triggers a submission or a
replay. Skip it entirely if `$PI_SESSION` is unset.

```bash
$TS metrics import --transcript "$PI_SESSION" && $TS metrics report --date "$DATE"
```

- `import` reads the harness transcript and writes one run file; a reimport of an
  unchanged transcript is a no-op (content hash match).
- `aggregate` is required before `report` if the day's runs changed:
  `$TS metrics aggregate --date "$DATE"`.
- `report --format json` prints the `DailyAggregate` verbatim; the default table
  prints fixed ASCII columns. Over `--from/--to`, `--format json` emits ONE JSON
  array of aggregates.
- `compare --base D --target D` shows the base→target delta/percentage per metric
  (a percentage only when the base is Known and non-zero; otherwise `n/a`).
- `run -- <command...>` records a live monotonic run for a command and derives its
  state from the exit code (0→completed, 3→blocked, else→failed).
- `--commit`, `--workflow-version`, `--config-mode`, and `--optimization-change`
  annotate a run so a later `compare` can cite what changed between two days.
- The transcript adapter extracts only counters, ids, timestamps, the `timesheet`
  subcommand token, and provider-native numeric cost; message text, tool arguments,
  and tool results are never persisted. A claude-schema run is marked
  `[declared schema]`.

Attribution to a workflow step is a **labelled heuristic** over the `timesheet`
subcommand token in the assistant turn that issued the tool call (see
`docs/adr/0006-metrics-contract.md`).
