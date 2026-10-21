# Timesheet Pipeline Workflow

This document defines the exact command sequence the `timesheet` skill runs. It is
a harness-agnostic orchestration over the `timesheet` Go binary: **every step is a
documented invocation**, and no accounting or submission logic is reimplemented
here.

**Every `docs/` or `docs/adr/` reference in this file is maintainer provenance, not
an agent read** (see the boundary in [SKILL.md](SKILL.md)): an agent must not follow
one, even where a sentence says a detail "lives" there. Everything needed to run
each step is stated in this file.

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

### Guided review defaults (stated, not asked)

The guided review **states its defaults and asks only genuine unknowns**. An
accepted invitation, a project the mapping already supplies, and an empty exclusion
set are all defaults, not questions:

- **Attendance (meetings).** When the invitation was **ACCEPTED**, attendance is
  treated as **attended** and staged as attended with **no prompt**; the run prints
  `attendance: attended (invitation accepted; stated default, not prompted)`. The
  attendance question (`Did you attend this meeting?`) is asked **only** when the
  invitation was **not accepted** — the one case where attendance is a genuine
  unknown. An accepted invitation is stated as attended by default; a meeting the
  operator accepted but did **not** attend is handled by choosing `Exclude from work`.
- **Project.** When a block's or interval's project is **already set** (from the
  configured mapping), it is kept as-is and **not** prompted; when the edit option is
  chosen the run prints `project: <P> (already set; kept as-is, not prompted)`. The
  project prompt is asked **only** when the project is genuinely **unset** (empty).
  A project already set from the mapping is overridden either by the guided bulk
  `Bulk-assign a project` action or by the scripted `project` decision key.
- **Exclusions.** An **empty exclusion set is the default**: the guided review never
  asks an "exclusions?" question. Excluding is always an explicit per-block choice or
  an explicit bulk action, never a question.

The per-block option labels are exactly:

- A **proposed block**: `Confirm as reviewed work`, `Exclude from work`,
  `Edit times / description` (the project is already set) or
  `Edit times / project / description` (the project is unset), `Leave unconfirmed`;
  a meeting that overlaps an already-confirmed interval additionally offers
  `Attended, joined with the overlapping interval (counts once)`.
- An **existing confirmed interval**: `Keep as is`,
  `Edit times / description` (the project is already set) or
  `Edit times / project / description` (the project is unset), plus exactly one of
  `Submitted - edit the remote entry instead; it cannot be removed` (a mapped
  interval), `Removed with the rest of this split group` (a sibling fragment is
  already staged for removal), or `Remove from this day (unconfirm)`. When a
  sibling fragment is **already staged for removal**, the interval offers **only**
  `Keep as is` and `Removed with the rest of this split group`, with **no edit**:
  the whole group is removed as one, so this fragment is not editable.

The edit label names the project **only** while the project is still unset, so the
option never promises an edit it will not offer; times and description stay editable
in both cases.

**No safety gate is a default.** The final `Apply this review and mark the day
reviewed?` confirmation, the incomplete-source acknowledgment, the carry
confirmation, and every bulk staging confirmation remain questions and are
unchanged.

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
that blocks the preview until acknowledged with `--ack <source-id>`. If the day's
note does not exist yet, skip the read: the run's export creates it, and `journal`
collection then reports `JOURNAL_NOTE_MISSING` (a handled, non-fatal source). Only
recorded work is captured. See
[journal source](../../../docs/agents/journal-source.md).

```bash
$TS review --config "$CFG" --date "$DATE"
```

**Output Seam**: the proposed work/meeting/manual blocks with their stable block
ids (used by later decisions), plus the untimed journal narratives. For a day that
is already confirmed, it also lists the day's existing confirmed intervals with
their **full ids**, which is how the `block_id` for an `unconfirm` decision (Step 4)
is named.

**Pre-existing remote entries for the date.** `review` performs the day's
authoritative collection read and enumerates the **observed** remote entries for
the date with their full id, span and description under `Remote entries (<date>):`.
When that read did **not** complete (a bound hit, duplicates, or a failed stability
re-check) the header is marked
`Remote entries (<date>) [PARTIAL - the read did not complete; this list may be
incomplete]:`, so a partial set is never shown as a complete enumeration.
`review` also prints `remote observation failed: <error>` when that read failed (a
non-fatal warning: evidence collection still succeeds), and a non-blocking
`remote collision: <id>[, <id>...]` warning for an observed entry whose span
overlaps a confirmed interval — a duplicate-looking signal that never blocks.
Nothing is printed for a signal that is absent; offline (no remote) prints no
entries section.

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
unconfirm, reset, acknowledge, mark-empty) through the same `internal/review` actions
an interactive review uses. The decisions file may contain a `{date}` placeholder for
a range.

```bash
$TS review --config "$CFG" --date "$DATE" --decisions decisions.json
```

Example `decisions.json`. This is the **complete** decision document: the keys and
fields below are every one the file accepts, so nothing about it needs to be looked
up elsewhere. Every key is optional, and an absent key means "no decision of that
kind".

```json
{
  "confirm": [{"block_id": "<BLOCK_ID>", "project": "Proj", "attendance": true}],
  "exclude": [{"evidence_id": "<EVIDENCE_ID>", "reason": "user_excluded"}],
  "join": [{"work_block_id": "<BLOCK_ID>", "meeting_ev_id": "<EVIDENCE_ID>"}],
  "trim": [{"block_id": "<BLOCK_ID>", "new_start": "09:15", "new_end": "09:45"}],
  "project": [{"block_id": "<BLOCK_ID>", "project": "Proj"}],
  "description": [{"block_id": "<BLOCK_ID>", "text": "Paired on the parser rewrite"}],
  "classification": [{"block_id": "<BLOCK_ID>", "classification_id": "<ID>", "activity_type": "<derived>", "classification_code": "<CODE>", "classification_name": "<NAME>"}],
  "manual": [{"uuid": "<UUID>", "start": "2026-10-06T09:00:00Z", "end": "2026-10-06T11:00:00Z", "project": "Proj", "description": "Pairing", "evidence_ids": ["<EVIDENCE_ID>"], "confirm": true, "classification_id": "<ID>", "activity_type": "<derived>", "classification_code": "<CODE>", "classification_name": "<NAME>"}],
  "unconfirm": [{"block_id": "<CONFIRMED_INTERVAL_ID>", "reason": "user_retracted"}],
  "reset": false,
  "mark_empty": false,
  "acknowledge": ["cal"]
}
```

Every key and field, and what is required:

| Key | Fields |
| --- | --- |
| `confirm` | `block_id` (required), `project`, `attendance` (bool) |
| `exclude` | `evidence_id` (required), `reason` |
| `join` | `work_block_id` (required), `meeting_ev_id` (required) |
| `trim` | `block_id`, `new_start`, `new_end` (all required; `HH:MM` local clock) |
| `project` | `block_id`, `project` (both required) |
| `description` | `block_id`, `text` (both required) |
| `classification` | `block_id` and `classification_id` (both required), plus `activity_type`, `classification_code`, `classification_name` |
| `manual` | `start` and `end` (required, RFC3339), plus `uuid`, `project`, `description`, `evidence_ids`, `confirm` (bool), and the same four classification fields |
| `unconfirm` | `block_id` (required; a confirmed interval id), `reason` (optional string) |
| `reset` | bool; defaults to `false` |
| `mark_empty` | bool; defaults to `false` |
| `acknowledge` | array of source ids |

Rules that are easy to get wrong, all stated here so they need not be looked up:

- **`description` is ADDITIVE and applied after `confirm`.** It sets a
  user-authored description, so it may reference EITHER a proposal block id or a
  confirmed interval id — as may `trim`, `project`, `join`, and `classification`.
  `description` is the user's own text; an AI-proposed description never substitutes
  for it.
- **`confirm` is the exception to that id rule.** Its `block_id` must be a
  **proposal** block id from Step 2, never a confirmed interval id: confirmation is
  what creates the interval, so a confirmed id there is an error. The five decisions
  listed above (description, trim, project, join, classification) are the only ones
  that may reference a confirmed interval id, and `unconfirm` is the only one that
  must (below).
- **`attendance` is tri-state.** It is optional and it is a pointer, so omitting it
  leaves attendance **unset** rather than asserting `false`; `true` and `false` are
  the two asserted values.
- **A `manual` entry needs a stable `uuid` to be confirmable.** Omitting `uuid`
  generates a fresh identity per call, so pass a stable `uuid` to edit the same
  interval; a `manual` entry with `confirm: true` and no `uuid` is an error, because
  the materialized interval could not then be identified.
- **`manual`'s `evidence_ids` links one user-timed interval to several supporting
  source records** (for example journal items); the links add **no minutes** and the
  interval's own span is authoritative.
- **`unconfirm` retracts an already-confirmed interval.** Its `block_id` must be a
  **confirmed interval id**, not an evidence id and not a proposal block id; the
  `review` command lists the day's existing confirmed intervals with their **full
  ids**, so that listing is how the id is named. The interval is removed and its
  evidence returns to the proposal as an unconfirmed block, so it can be re-confirmed
  or excluded; a split interval's fragments are removed together.
- **`unconfirm` never removes a submitted interval, nor a split group that contains
  one.** A split group is removed as a whole, so if the requested interval **or any
  member of its non-empty split group** carries a remote mapping, the whole review is
  refused with the specific reason `unconfirm_mapped_remote:<localID>`. The id named
  is the **mapped member's** id, which may differ from the one requested. This is
  because a Gradion entry can be edited but never deleted. The correct action for a
  submitted interval is an `edit` via `trim`, `project`, `description`, or
  `classification`.
- **`unconfirm` refuses an id that names no confirmed interval on the day.** If its
  `block_id` matches no confirmed interval, the whole review is refused with the
  specific reason `unconfirm_unknown_interval:<id>`, so a mistyped or already-removed
  id is never silently ignored. Re-read the `review` command's existing-confirmed-work
  listing to get the current full id.
- **`reset` returns the day to a clean review slate.** It clears the reviewed flag and
  the reviewed-empty marker, removes every UNSUBMITTED confirmed interval, and clears
  the other review decisions for the day (exclusions, attendance choices,
  acknowledgements, and the descriptions and classifications of the removed
  intervals). It RETAINS all submission records: evidence, remote mappings, plans,
  submission outcomes, unresolved submission history, and approvals (invalidated,
  never deleted). It never deletes the record file. After a `reset` the day is
  unreviewed and empty; combine it with new `confirm`/`manual` decisions, or set
  `mark_empty: true`, in the same or a later pass.
- **`reset` is refused when the day is not clean.** It is refused if any interval on
  the day is submitted (reason `reset_submitted_interval:<localID>`), and also if any
  submission outcome is unresolved (in_flight, uncertain, or unidentified) (reason
  `reset_unresolved_submission`).
- **Both `unconfirm` and `reset` invalidate the day's active approval.** An approval
  must never survive either action. If the operation is refused, nothing is written
  for that day.
- **`mark_empty` marks the day reviewed with no work**, and is refused when the day
  already carries ANY confirmed interval, or when the same decision set stages
  `confirm` or `manual` work.
- **`acknowledge`** lists the incomplete source ids to accept; it is the document
  form of `--ack`.

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

**The classification fields, unambiguously.** Five names are in play and only one of
them is ever sent:

| Name | What it is | Where it goes |
| --- | --- | --- |
| `classification_id` | The catalog option's remote id. **The one payload field.** | A `classification` or `manual` decision; the only value sent in a `log_time`/`edit_time` payload. |
| `activity_type` | Derived **by the server** from the classification. Display and validation only. | A `classification` or `manual` decision, for validation; **never** a payload field. |
| `classification_code` | The catalog option's code, for example `INTGRADI2610`. A retrieval hint, preferred over the name. | Recorded on the confirmed interval; **never** proof and **never** a payload field. |
| `classification_name` | The catalog option's name. A retrieval hint. | As above. |
| `--classification` (the `classify select` flag) | An explicit user token, resolved as a **code, an id, or an unambiguous id-prefix**. | Only on the command line, and resolved to a `classification_id` before anything is recorded. |

So `--classification` takes a code or an id; the decisions file takes
`classification_id` plus optional hints; and only `classification_id` is ever
transmitted. An unset `classification_id` blocks the plan, and a legacy interval
with no recorded code/name fails closed with `reselect_classification`.

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

**Pre-existing entries for the date.** `review` (Step 2) performs the day's
authoritative collection read and enumerates every **observed** remote entry for
the date with its full id, span and description, so "what is already on this
date?" is answerable from a supported command.

`preview` separately re-observes the remote **read-only** and reports
`remote observed: <timestamp>`, `remote complete: <bool>`, and, for every operation
it would edit, a `remote=<id>`. Require that observation before any submission; a
`remote complete: false` means the observation was partial and the plan may be
incomplete. Offline (`--offline`, or no configured endpoint) is reported honestly
as `remote observed: unobserved (offline; no remote was contacted)` with
`remote complete: false`: nothing about the remote is known.

A remote id is obtained from the supported enumeration alone: `review` (Step 2)
prints the day's observed remote entries with their full id, and `preview` prints
`remote=<id>` on each operation it would edit.

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

where `resolve.json` is an array of resolution objects, and these four fields are
every one it accepts:

| Field | Meaning |
| --- | --- |
| `operation_key` | **Required.** The unresolved operation's stable key, as the run reports it. |
| `kind` | **Required.** Exactly `"adopt"` or `"not_applied"`; any other value is an error. |
| `remote_id` | The remote entry's id. **Required for `kind: "adopt"`**; it may be omitted only when the operation itself already carries one. |
| `local_id` | The local interval id to map. Optional; defaults to the operation's own local id. |

```json
[
  {"operation_key": "<OPERATION_KEY>", "kind": "adopt", "remote_id": "<REMOTE_ID>", "local_id": "<LOCAL_ID>"},
  {"operation_key": "<OPERATION_KEY>", "kind": "not_applied"}
]
```

Rules, stated here so they need not be looked up:

- **An unresolved CREATE is never settled by a bare absence.** Leaving its decision
  out keeps it unresolved on purpose; only `not_applied` (the write never applied)
  or `adopt` (it did, and here is the id) resolves it.
- **An unresolved EDIT may settle itself from a safe read-only observation**:
  post-edit remote content proves the edit applied, pre-edit content proves it did
  not, and absent or ambiguous content leaves it unresolved. That needs no decision
  from the agent, and nothing is ever resent.
- **A retried operation's key carries an attempt suffix.** An unresolved second or
  later attempt's key ends in `#N` after the unit-separator control character (the
  part after `uncertain:` in the gate reason); copy it EXACTLY as the gate shows it
  — never strip the suffix.
- **A decision whose `operation_key` matches no unresolved operation is an error**,
  as is an unknown `kind` and an `adopt` with no remote id. The command reports how
  many decisions it applied.

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
replay. Skip it entirely if no harness transcript is available. `$PI_SESSION` is the
pi location; use the same command with another harness's transcript path in its
place.

```bash
$TS metrics import --transcript "$PI_SESSION" --date "$DATE" && $TS metrics report --date "$DATE"
```

Locate the transcript by harness: pi uses `$PI_SESSION`; Claude Code keeps sessions
at `~/.claude/projects/<project-slug>/<session-id>.jsonl`; Antigravity keeps a
conversation's log at
`~/.gemini/antigravity/brain/<conversation-id>/.system_generated/logs/transcript.jsonl`
(the fuller `transcript_full.jsonl` sits beside it). `metrics import` reads any of
these through `--provider auto`, or explicitly with `--provider pi`,
`--provider claude`, or `--provider antigravity`.

- `import` reads the harness transcript and writes one run file; a reimport of an
  unchanged transcript is a no-op (content hash match). It also writes ONE session
  record (the harness session's tokens) and prints a one-line session summary.
- `import` takes `--model-catalog FILE` (the runtime `models-store.json`; default
  `$PI_MODEL_CATALOG`, else `~/.pi/agent/models-store.json`) to price the session,
  and `--timezone TZ` (an IANA zone; default the process local zone) for its
  local-date association. A missing or unparseable catalog only **warns** and
  continues with an unknown price — it never changes an exit code.
- `import --date D` keeps its meaning (it overrides the run's date) AND records
  **which day** the session logged: the session's logged date (the association key)
  is set to `D` with association `import_flag`. Only this operator-stated value is
  used; no transcript tool-argument value is decoded.
- Without `--date`, the association falls back to the **local date of the session's
  own workflow-step calls** (association `local_step_date`). That is the date the
  session RAN, which equals the logged day only when the run happens on the day it
  logs: **when you log a past day, pass `--date`, or the session is associated with
  the run date instead of the day you logged.** Zero such dates, or more than one,
  is `unknown`, and an `unknown` session is never folded into a day's totals.
- `sessions` lists the stored session records: per session its id, provider, model,
  logged date, association, calls, input, output, cache read, cache write,
  cache-hit %, total tokens, price source, and cost. `--session ID` shows one
  session, `--date D` filters by logged date, and `--format json` emits the records.
- `aggregate` is required before `report` if the day's runs changed:
  `$TS metrics aggregate --date "$DATE"`. Its one-line summary and its
  `--format json` output carry the session lane (session count, session cache-hit %,
  session cost, unassociated sessions).
- `report --format json` prints the `DailyAggregate` verbatim; the default table
  prints fixed ASCII columns (including `SESSIONS`, `SESS-CACHEHIT`, `SESS-COST`,
  and `UNASSOC`). Over `--from/--to`, `--format json` emits ONE JSON array of
  aggregates.
- **The honesty rule.** An unknown price or model prints `unknown` and is never
  shown as `0` (a nil cache-hit or cost is `unknown`, never `0`). A session whose
  day cannot be established is `unknown` (association `unknown`) and is NOT counted
  into any day's totals: its day is never guessed, and it is never folded into a
  day. It is surfaced **per-day and touch-scoped**: a day's `unassociated_sessions`
  counts it ONLY when the queried date is one of its workflow-step `LocalDates`, so
  a session straddling local midnight is reported on each day it actually touched
  while its tokens are folded into none of them. An unassociated session with NO
  dated workflow-step calls is counted in no day's aggregate at all; it stays
  visible only through `metrics sessions`.
- `compare --base D --target D` shows the base→target delta/percentage per metric
  (a percentage only when the base is Known and non-zero; otherwise `n/a`).
- `run -- <command...>` records a live monotonic run for a command and derives its
  state from the exit code (0→completed, 3→blocked, else→failed).
- `--commit`, `--workflow-version`, `--config-mode`, and `--optimization-change`
  annotate a run so a later `compare` can cite what changed between two days.
- `--provider` selects the transcript adapter explicitly when auto-detection is not
  wanted, and `--metrics-dir` overrides the metrics store location. `import`,
  `report` and `compare` all accept `--metrics-dir`.
- The transcript adapter extracts only counters, ids, timestamps, the `timesheet`
  subcommand token, and provider-native numeric cost; message text, tool arguments,
  and tool results are never persisted. A claude-schema run is marked
  `[declared schema]`.

Attribution to a workflow step is a **labelled heuristic** over the `timesheet`
subcommand token in the assistant turn that issued the tool call (see
`docs/adr/0006-metrics-contract.md`).
