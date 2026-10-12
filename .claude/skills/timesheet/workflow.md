# Timesheet Pipeline Workflow

This document defines the exact command sequence the `timesheet` skill runs. It is
a harness-agnostic orchestration over the `timesheet` Go binary: **every step is a
documented invocation**, and no accounting or submission logic is reimplemented
here.

- **`[Script]`**: deterministic Go code invoked through `cmd/timesheet`.
- **`[AI]`**: the optional, bounded editorial agent invoked *by the core* through a
  configured command adapter. The skill never synthesizes text itself.

Set `TS` to a **prebuilt binary** — build once with
`go build -o ./bin/timesheet ./cmd/timesheet` and use `TS="./bin/timesheet"`
(rebuild only if it is missing). A prebuilt binary avoids recompiling on every
invocation; `go run ./cmd/timesheet` still works but recompiles each call, which
is wasteful for a multi-step guided session. Set `CFG=timesheet.json`.
Default to today in the configured timezone; pass `--date` or `--from/--to` to
target specific days. `--offline` skips **ONLY** the Gradion remote; configured
source fetches (calendar ICS, git, journal) still run.

## Run modes

`run` has one scripted path and a guided one. `--decisions FILE` /
`--non-interactive` force the scripted path and take precedence; `--plain` is the
safe line-based guided path (works on a pipe, EOF aborts); `--interactive` is the
terminal UI and runs only when stdin and stdout are real terminals and the
environment is not CI. The default (no flag) is scripted, so headless behavior
and exit codes are unchanged. The guided run reviews one day at a time, applies
every choice in one atomic step only after a deliberate final confirmation, and
never approves or submits. Full modes, precedence, and cancellation:
[guided workflow](../../../docs/agents/guided-workflow.md).

---

## Step 0: `[Script]` Configuration (when no config exists, or to change one)

Every later step reads a config document (`$CFG`). Author it with the **supported
configuration flow** rather than hand-writing JSON:

```bash
$TS config init                                    # guided TUI (needs a terminal on stdin AND stdout)
$TS config init --interactive                      # the explicit alias of the SAME guided mode (identical gate)
$TS config init --plain --out "$CFG"               # line-based prompts (safe on a pipe; EOF aborts)
$TS config init --answers answers.json --out "$CFG"   # non-interactive: validate + write a config document
$TS config edit --config "$CFG"                    # load an existing config, prefill, edit in place
```

The TUI runs only at a real terminal (stdin **and** stdout, not CI); otherwise
`config` refuses and points at `--plain` / `--answers` — it never silently falls
back. `--interactive` is only the explicit alias of that default guided mode and
does **not** change the terminal/CI gate.

The non-interactive `--answers FILE` is a **complete config document in the same
schema** the flow writes; `--non-interactive` requires it. The flow collects ONLY
the existing schema: `evidence` (`id`, `timezone`, `sources`), `storage_dir`,
`vault` + `daily_dir`, optional `gradion` (`endpoint` + `keychain:<service>`
`credential_ref`), optional `editorial` (`enabled`/`adapter`/`command`/
`credential_ref`), and the optional project→classification map.

A guided **edit** preserves values it does not prompt for (the editorial tuning
fields `soft_target`/`max_attempts`/`endpoint`/`model`, and existing
classification entries), but REBUILDS each source's `config` map from the collected
keys only — so uncollected source keys (e.g. a `fixture`) are dropped. Author such
keys via `--answers`.

Validation runs BEFORE any write and is **offline** (IANA timezone via
`time.LoadLocation`, the source configuration contract, `editorial.Validate()`, and
`keychain:` reference-format checks). It makes **no** live network, Keychain, or
remote call and **never** resolves a secret. It prints the **exact JSON preview**
(references only) and requires explicit confirmation; an existing target file is
**refused** unless the user consents or `--force` is passed. The write is atomic.

When delegating from chat, the skill asks the **equivalent questions** in
chat (evidence id, timezone, sources and their keys, storage dir, vault +
daily dir, Gradion, editorial, classification map), shows the preview, obtains
confirmation and overwrite consent, and lets `timesheet config` do the write. The
skill never writes the target config (`$CFG`) itself — materializing the
`--answers` INPUT document is expected and allowed — never auto-confirms an
overwrite, and never places a raw secret in the answers.

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

Collect evidence from the enabled sources (calendar, git, and the optional
read-only journal note), merge it into the day's workflow record, and print the
deterministic proposal. Collection proceeds independently of review or
submission. Nothing is confirmed.

Journal items appear as **UNTIMED narrative candidates** with their provenance
(relative note path, heading, line range, stated date) and an explicit unknown
duration; a note never establishes working time. A missing or unreadable note is
a **visibly incomplete** source (`JOURNAL_NOTE_MISSING` / `JOURNAL_VAULT_MISSING`)
that blocks the preview until acknowledged with `--ack <source-id>`. Only
recorded work is captured. See
[journal source](../../../docs/agents/journal-source.md).

```bash
$TS review --config "$CFG" --date "$DATE"
```

**Output Seam**: the proposed work/meeting/manual blocks with their stable block
ids (used by later decisions), plus the untimed journal narratives.

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
it can never alter intervals, attendance, projects, evidence, or approvals. Private
journal note text is **never** forwarded to the editorial service, including a
manually linked journal description. Disable it with `--no-editorial`.

---

## Step 4: `[Script]` Apply review decisions

Apply a serializable decision set (confirm, exclude, join, trim, project, manual,
acknowledge, mark-empty) through the same `internal/review` actions an interactive
review uses. The decisions file may contain a `{date}` placeholder for a range.

```bash
$TS review --config "$CFG" --date "$DATE" --decisions decisions.json
```

Example `decisions.json` (a subset of the `run.ReviewDecisions` fields):

```json
{
  "confirm": [{"block_id": "<BLOCK_ID>", "project": "Proj", "attendance": true}],
  "exclude": [{"evidence_id": "<EVIDENCE_ID>", "reason": "user_excluded"}],
  "join": [{"work_block_id": "<BLOCK_ID>", "meeting_ev_id": "<EVIDENCE_ID>"}],
  "trim": [{"block_id": "<BLOCK_ID>", "new_start": "09:15", "new_end": "09:45"}],
  "project": [{"block_id": "<BLOCK_ID>", "project": "Proj"}],
  "classification": [{"block_id": "<BLOCK_ID>", "classification_id": "<ID>", "activity_type": "<derived>", "classification_code": "<CODE>", "classification_name": "<NAME>"}],
  "manual": [{"uuid": "<UUID>", "start": "2026-10-06T09:00:00Z", "end": "2026-10-06T11:00:00Z", "project": "Proj", "description": "Pairing", "evidence_ids": ["<EVIDENCE_ID>"]}],
  "mark_empty": false,
  "acknowledge": ["cal"]
}
```

A `manual` entry's `evidence_ids` links one user-timed interval to several
supporting source records (for example journal items); the links add **no
minutes** and the interval's own span is authoritative. Omitting `uuid` generates
a fresh identity per call, so pass a stable `uuid` to edit the same interval.

**Output Seam**: the day is reviewed (or reviewed-empty) and persisted.

---

## Step 5: `[Script]` Classification selection (explicit only)

Classification is an **explicit user choice**. Repository mappings, calendar text,
AI, and config defaults may **suggest** only — they are never applied silently. An
unset classification **blocks** the plan (`unresolved_classification`).

First present the fetched catalog, then ASK the user to select, then apply the
explicit choice:

```bash
$TS classify list --config "$CFG"              # live by default; --cache reads the cache
$TS classify list --config "$CFG" --json       # machine-readable Catalog
$TS classify select --config "$CFG" --date "$DATE" --classification "<CODE|ID>" --block-id "<BLOCK_ID>"
$TS classify select --config "$CFG" --date "$DATE" --classification "<CODE|ID>" --all
```

- `classify list` prints `Name (Code) [Activity Type] · <id-prefix>` plus a
  `complete|partial` marker and provenance/freshness (`live`/`cache`, observed-at).
  The Activity Type is **derived by the server** from the classification; it is
  display/validation only and is never a payload field.
- `classify select` requires an **EXACT unique** match — by id, by code, or by an
  unambiguous id-prefix (unknown/ambiguous tokens are refused; no id is ever
  synthesized). It prints the catalog's provenance/freshness, and **refuses a STALE
  `--cache` catalog** unless `--force` is given. `--block-id` sets one interval.
  `--all` is the visible **bulk** option: it applies to **every** confirmed interval
  of the day and requires the explicit `--all` flag; the exact change list is printed
  before applying.
- A selection change **invalidates any existing approval** (via `stale_local`). The
  recent choice is persisted as a **suggestion that still requires explicit
  confirmation** — it is never auto-applied.
- `--classification` must be an explicit user input; no config default fills it.
- The explicit selection records the catalog option's **code** (preferred) and
  **name** as RETRIEVAL metadata on the confirmed interval. This metadata is never
  proof and never a payload field; at `submit` each choice is re-proven against a
  fresh LIVE remote result by its recorded code. A legacy interval with no recorded
  code/name fails closed with a `reselect_classification` diagnostic (zero writes),
  unless a fresh live full complete catalog proves it. See
  [classification selection](../../../docs/agents/classification-selection.md) §3.

**Output Seam**: the chosen `classification_id` (and its derived activity type, plus
the retrieval code/name) on the day's confirmed interval(s), with any prior approval
invalidated.

---

## Step 6: `[Script]` Submission preview

Build the exact create/edit plan from the reviewed record and the freshly observed
remote state, and persist it. This performs **zero remote writes** (a read-only
re-observe is allowed). An empty classification blocks; the resolver's value is
surfaced as a **non-applied suggestion** only.

```bash
$TS preview --config "$CFG" --date "$DATE"
```

**Output Seam**: `plan_id: <PLANID>` and the operation count. The plan id is the
plan's content hash.

---

## Step 7: `[Script]` Approval

Bind an approval to the exact plan content hash. The approval is single-use: any
local, plan, or remote change invalidates it.

```bash
$TS approve --config "$CFG" --date "$DATE" --plan "$PLANID"
```

**Output Seam**: `approval_id: <APPROVALID>` (equal to the plan id).

---

## Step 8: `[Script]` Submit and export

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
- A write response that only **acknowledges an entry's identity** is not a
  confirmed write: the core verifies its content by a bounded stable-ID read-back
  before recording a mapping. A read-back that is absent, unverifiable, or whose
  content does not match the plan leaves the write **content-unverified** and
  unresolved (never a false confirm). A server-normalized value reads as a
  mismatch — conservative, never an optimistic confirm.
- **An uncertain, unidentified, or content-unverified write STOPS the operational
  session.** Recovering from it requires SEPARATE owner authority: no
  self-adoption, no new-plan approval, no source hot-patching, and no debugging
  writes without that authority. A conversational "go" confirms ONLY the
  immediately presented exact plan, and ONLY when submission authority is already
  established; it cannot authorize a different plan and never implicitly grants
  debugging or source-repair authority. (The unresolved-write STOP is
  code-enforced by the run and the restart gate; the recovery rules above are
  instruction-enforced.)
- After execution the core rewrites only the marker block in
  `<vault>/<daily-dir>/<date>.md`, preserving everything else byte-for-byte.

A normal day does not call `resolve`. Reconcile an operation left unresolved by
an **uncertain** write with an explicit decision (never a bare absence); `resolve`
reports the applied count and errors when a decision matches no unresolved
operation:

```bash
$TS resolve --config "$CFG" --date "$DATE" --decisions resolve.json
```

where `resolve.json` is an array of
`{"operation_key": "...", "kind": "adopt"|"not_applied", "remote_id": "...", "local_id": "..."}`.

Re-render the managed note without submitting (read-only with respect to the
remote):

```bash
$TS export --config "$CFG" --date "$DATE"
```

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
