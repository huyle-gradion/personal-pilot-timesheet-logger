# Rewrite interface (R3: explicit migration and read-only remote plans)

Application version: `0.1.0-dev.8` (unreleased development build, not a release tag).
Protocol schema: `1`. State schema: `2`.

This document is the public interface for the R3 slice of the timesheet rewrite.
R3 builds on the accepted local review-first workflow and R2 verification-only
evidence collection. It adds an explicit, repeat-safe migration of confirmed
legacy workflow records and a read-only Gradion boundary for reconciliation,
explicit identity/content resolution and exact submission-plan previews.

R3 does **not** implement approval, submission, or any remote write. A plan can be
`ready` or `blocked`; it can never be `approved`, and no request can authorize a
remote mutation. Historical legacy approvals are inert audit data. Commands,
responses, or notes that mention later slices are limits, not features.

## Command line

```text
timesheet-rewrite version
timesheet-rewrite serve --config <path> --env-file <path>
```

`version` prints the application version and protocol schema and exits 0. It
loads no configuration, environment file, credentials, or evidence.

`serve` starts one persistent process. It reads one JSON request per line from
stdin and writes one JSON response per line to stdout. Diagnostics go to stderr
and are sanitized. The process never launches a shell.

`--config` and `--env-file` are fixed launcher inputs. There are no request
fields for paths, executable names, or arbitrary operations. Both flags are
required.

Exit codes: `0` normal close/EOF, `1` startup or stream failure (for example an
invalid config, locked state, or unreadable stream), `2` usage error.

Only one process may open a given state file. A second `serve` against the same
state fails with `store_locked`; it does not race or overwrite the first.

## Protocol envelope

Request fields are strict. Unknown fields, missing `id`/`op`, trailing JSON, and
fields not allowed for the selected operation are rejected.

```json
{"id":"1","op":"open_day","day":"2026-10-22"}
```

Responses always echo the request ID and carry either `result` or `error`:

```json
{"id":"1","ok":true,"result":{...}}
{"id":"1","ok":false,"error":{"code":"overlap","message":"..."}}
```

If the request cannot be decoded, the response `id` is empty. IDs must be
non-empty, at most 128 bytes, and contain no control characters.

### Operations

| `op` | Fields | Effect |
| --- | --- | --- |
| `open_day` | `day` | Loads or creates the local day record and starts a staged draft. |
| `add_interval` | `day`, `revision`, `text`, optional `evidence_refs` | Parses one typed interval and stages it. |
| `edit_interval` | `day`, `revision`, `interval_id`, `text` | Replaces a staged interval. Evidence references are preserved. |
| `remove_interval` | `day`, `revision`, `interval_id` | Removes a staged interval. |
| `review` | `day` | Returns confirmed and staged segments, totals, cross-midnight allocation, and the `evidence` review object (topics, proposed meetings with decisions, per-source coverage). |
| `confirm` | `day`, `revision` | Promotes the staged draft to a new confirmed revision in memory. Fails `source_unacknowledged` while an incomplete collection attempt on this day is unacknowledged. |
| `save` | `day`, `revision` | Persists confirmed work locally (same gate as `confirm`). Staged changes are not saved. |
| `collect` | `day` | Runs all enabled source collectors for that day's local window (verify-only, no minutes). Receipts carry per-source status/counts. |
| `acknowledge_source` | `day`, `source`, `collection_id` | Explicit human acknowledgment, verified against the exact reviewed collection attempt ID. Missing or stale IDs fail (`bad_request` / `stale_collection`). |
| `decide_meeting` | `day`, `meeting_id`, `decision`, `collection_id` | Records `attended`/`skipped`/`pending` for one proposed meeting. The meeting's provider identity must be present in this day's evidence (`unknown_meeting` otherwise). `collection_id` is required and must match the day's calendar attempt (`bad_request` when missing, `stale_collection` on mismatch); the action is rejected without mutation otherwise. Decisions carry across recollections matched by `UID|Start`. |
| `migration_preview` | `dates` | Validates one explicit legacy date selection without activating anything. Returns bounded counts, conflicts, source hashes and a `migration_id` bound to the source bytes, dates/timezone and current durable target state. |
| `migration_apply` | `migration_id` | Re-reads and re-verifies the exact preview inputs, then atomically activates the whole candidate. Requires that no day is open. The fresh candidate is validated (source identity, durable target, conflicts) before any successful `no_change` return; a repeated identical source is `no_change`, and stale or conflicting input is rejected without mutation. |
| `classifications` | `query` (optional) | One bounded authoritative classification read. Returns a stored `catalog_id`, options and an explicit `complete` flag. A filtered or truncated catalog cannot prove an option absent. |
| `select_classification` | `day`, `revision`, `interval_id`, `catalog_id`, `classification_id` | Records an explicit reviewed remote classification choice for an option the catalog returned, in staged work. Follows confirm/save semantics. |
| `reconcile_remote` | `day` | One fresh read-only observation of the day. Persists the observation and reports mapped, changed, missing and orphaned identities, unbound remotes, overlap/text candidates, unresolved-write history and blockers. A read never binds an identity. |
| `resolve_remote` | `day`, `revision`, `observation_id`, `remote_id`, `local_id` (omitted for `foreign`), `decision` | Records one explicit identity/content decision (`adopt`, `accept_observed`, `foreign`) against the current observation. Rejects stale revision/observation IDs and identity conflicts without mutation. |
| `preview_plan` | `day` | Renders exact create/edit operations and blockers from saved confirmed durable work, explicit classification choices and a fresh read-only observation. Never plans from an unsaved day, never saves a draft, and never exposes an approval. |
| `cancel` | `day`, `revision` | Discards staged edits and restores the last confirmed revision. |
| `status` | none | Reports loaded days, protocol identity, `store_schema`/`durable_schema`, `durability_uncertain`, and compact migration/binding/unresolved-write/plan counts. |
| `check_environment` | none | Reports required variable **names** and which are missing. |
| `close` | none | Replies, then ends the process. Unsaved state is not written. |

A day must be opened before its other operations. `revision` is required for
every mutation and for `save`/`cancel`; a mismatch fails with `stale_revision`.

## Collection and the incomplete-source gate

`collect{day}` runs each enabled collector against the day's local time window:

- **Window** is `[day 00:00 local, next day 00:00 local)`, resolved in the day's
  recorded timezone. A 25-hour fall-back day collects a 25-hour window; a day
  whose midnight is skipped starts at its first real instant.
- **GitHub** `status` is `complete` (all enabled families fetched to exhaustion),
  `incomplete` (a family hit an honest bound — page cap, search 1000-result cap
  or `incomplete_results`, the submitted-review candidate ceiling, the events
  window, an HTTP failure such as a 403 on one repository, an omitted
  unverifiable commit ownership, or topic item-cap truncation), or
  `missing_credentials`. Attribution is strictly by provider-named actor,
  verified in the provider's own response bodies (`author` filter pulses
  verified against the commit's GitHub user document, `login` qualifier plus
  `user.login`, `reviewer user.login` on the reviews endpoint, `actor.login`);
  a response that names another actor is dropped, and a commit with no user
  document is omitted (a commit-author email is a filter value, not verified
  ownership) with the omission reported in the family detail.
  Families: commits (`/repos/{o}/{r}/commits`), authored pull requests
  (search `author:`), submitted PR reviews (search `reviewed-by:` candidates
  across every PR the user reviewed, independent of when the resource was last
  updated; each candidate verified at
  `/repos/{o}/{r}/pulls/{pull_number}/reviews` and filtered by the exact
  `submitted_at` instant — approval-only reviews appear regardless of the
  events feed, and a review submitted on the day is retained after later PR
  activity), PR review comments
  (`/repos/{o}/{r}/pulls/comments`), issue comments
  (`/repos/{o}/{r}/issues/comments`), and the bounded events feed
  (`/repos/{o}/{r}/events`, always reported incomplete). One provider action
  seen through two families carries one canonical event identity and its
  provenance is merged (issue comment `gh:{repo}:issue-comment:{id}`,
  PR review comment `gh:{repo}:pr-review-comment:{id}`, submitted review
  `gh:{repo}:review:{id}`); distinct actions keep distinct identities. Topics
  group by retained provider resource identity (`resource` field), and a topic
  with more items than the 50-item cap records `truncated` with the dropped
  count. Item `kind`s: `commit`, `authored_pull_request`, `submitted_pr_review`,
  `pr_review_comment`, `issue_comment`, `discussion`, `discussion_comment`,
  `event`.
- **Calendar** `status` is `complete`/`incomplete`/`empty`/`missing_credentials`.
  Series expand over the full recurrence set RRULE ∪ RDATE − EXDATE with each
  property's own TZID/value form and de-duplication, for timed and all-day
  series (pinned rrule-go/golang-ical; unsupported value forms report
  `incomplete`, never silent drops). All-day DTEND is exclusive: a one-day all-
  day event with DTEND the next date covers exactly its DTSTART date, with no
  DTEND it covers its DTSTART date; the displayed all-day `end` is that
  exclusive end date. Date-only events keep their declared DATE labels:
  occurrence and boundary instants are resolved from the dates with the same
  first-real-instant rule as the day window, date-only recurrence expands on
  civil dates, and the exclusive end is resolved from its own date rather than
  by carrying a normalized start clock into another date — so a declared
  September 6 event never appears on September 5 or on its exclusive-end
  September 7 because local midnight was skipped or repeated.
  RECURRENCE-ID overrides suppress their replaced master
  occurrence; a scheduled override appears where its own span overlaps the day,
  and a cancelled override appears only when the replaced original instance or
  the override's own span touches the queried day — unrelated days do not
  emit unrelated cancellations. Recurrence candidate selection is
  span/duration-aware: the expansion look-back is derived from the series'
  occurrence duration or calendrical span and exact interval overlap decides
  membership, so a long timed or all-day occurrence that began before the day
  is retained (interior days of a three-day all-day instance, the tenth day of
  an eleven-day timed instance). A cancelled master event or override is still
  a proposal with `status: "cancelled"`; attendance is the attendee's own
  PARTSTAT. An event / override / recurrence value that cannot be parsed,
  resolved, or expanded marks the source `incomplete` and never silently
  drops it.
- Details in receipts are status-only (HTTP status, bound counts); feed URLs,
  tokens, and raw provider documents are never echoed.

`confirm` and `save` fail `source_unacknowledged` while any enabled source's
attempt for the day is neither `complete` nor explicitly acknowledged — this
includes the never-collected state (run `collect` first), as well as
`incomplete`, `failed`, and `missing_credentials` attempts. Missing
credentials are an explicit source failure requiring acknowledgment exactly
like any other incomplete coverage; a `disabled` source imposes no gate. Each
`collect` creates a new attempt ID and resets earlier acknowledgments — a
fresh attempt always requires a fresh human look.

`acknowledge_source{day,source,collection_id}` carries and verifies the
exact reviewed collection identity. The collection ID of the reviewed attempt
is returned by `collect` and shown as `collection_id` in the review's
`sources` entries. A request without `collection_id` fails `bad_request`; an
ID that does not match this day's recorded attempt for the source fails
`stale_collection` and mutates nothing — collection may have run since the
call's review, and silently acknowledging whichever attempt is currently
latest is refused. Acknowledgment and meeting decisions are copy-on-write: a
failed write leaves review state, disk, gate state, and the absence of
evidence keys exactly as they were; no failed pre-activation write can create
a record that a later successful write persists as an unreadable null
evidence entry, and preserved post-rename `write_uncertain` behavior is
unchanged.

`decide_meeting` decisions are recorded separately from confirmation and from
calendar attendance flags; `pending`/`attended`/`skipped` never create
minutes. Every attendance decision must carry the `collection_id` of the
reviewed calendar attempt: a request without it fails `bad_request`, and an ID
that is not the day's current calendar attempt fails `stale_collection`, both
without mutation. The required identity is deliberate — an omitted identity is
not treated as permission to target the latest state. The meeting `meeting_id`
(an opaque reference) must also name a proposed meeting in the day's current
evidence; the reference fingerprints the current target, including the
rescheduled start of an overridden occurrence, so a decision built against a
recollected, moved instance fails `unknown_meeting` even when its original
RECURRENCE-ID is unchanged. Recollections re-match persisted decisions by
`UID|Start` identity of the proposed meeting; that carry-over of an unchanged
instance's existing decision is separate from authorizing a new action, and a
replacement that changes the instance identity does not inherit the old
decision.

### Review `evidence` object

`review{day}` returns `result.evidence` for the day's most recent attempt:

- `sources`: one entry per configured source — `source`, `status`
  (`complete`/`incomplete`/`empty`/`missing_credentials`/`disabled`), and a
  status-only `detail` when bounded.
- `topics`: provider-suggested topic candidates grouped by exercise (per-repo
  commits, per-PR items, per-issue items, per-discussion items), each `item`
  carrying an opaque `ref`, a `kind`, and time text — never durations.
- `meetings`: proposed meetings with `uid`, opaque `ref`, start/end (original
  offsets preserved, all-day date-only), `recurrence`
  (`single`/`recurring`/`override`), `status` (`scheduled`/`cancelled`),
  attendance (the attendee's own PARTSTAT), and the explicit human `decision`
  (`pending`/`attended`/`skipped`) when recorded.

## Typed interval grammar

```text
DATE HH:MM-HH:MM PROJECT[/CLASSIFICATION] DESCRIPTION
```

- `DATE` is `YYYY-MM-DD` and must equal the open day.
- Times use 24-hour `HH:MM`. `END` equal to `START` is rejected as zero duration.
- Times are resolved in the open day's recorded IANA timezone. A newly opened day
  takes the configured timezone; a saved day always keeps the timezone stored in
  its record, so changing configuration never reinterprets saved work.
- `PROJECT` must match a configured project ID.
- `CLASSIFICATION` is optional. If omitted, the project's configured
  `default_classification` is used. The selected value is stored explicitly.
- `DESCRIPTION` is the remaining text. One pair of surrounding single or double
  quotes is stripped. Empty descriptions are rejected.

Examples:

```text
2026-10-22 09:00-10:00 SYN-1 Synthetic review
2026-10-22 10:30-11:15 SYN-1/DEV "Synthetic follow-up"
2026-10-22 22:30-01:15 SYN-2 Late synthetic session
```

`evidence_refs` is an optional array of opaque strings (maximum 20). Multiple
references never add time; R1 does not resolve or fetch them. GitHub duration
inference is absent.

### Midnight and day boundaries

Intervals belong to the day they start. A cross-midnight interval is split for
display into per-day allocations:

```text
2026-10-22 22:30-01:15  =>  2026-10-22 22:30-24:00 (90 min)
                            2026-10-23 00:00-01:15 (75 min)
```

The start day's total counts only 90 minutes; the 75 minutes after midnight are
reported on 2026-10-23 as a `locked` segment and are included in that day's
total. Nothing wraps, disappears, or is counted twice. A locked segment is
edited on its origin day, not on the day it spills into. An `END` of `00:00`
with a later start means the interval ends exactly at midnight and is not a
cross-midnight split.

### Timezone transitions

Elapsed minutes use absolute time, not the wall-clock difference. With the
recorded timezone `Europe/Amsterdam`:

```text
2026-03-29 01:30-03:30  =>  60 min   (02:00-02:59 does not exist)
2026-10-25 01:30-03:30  =>  180 min  (the 02:00-02:59 hour repeats)
```

A start or end wall time that does not exist fails with `nonexistent_time`. A
wall time that occurs twice fails with `ambiguous_time`; this build never chooses
an occurrence. Choose a time outside the transition, or split the work so both
endpoints are unambiguous. An unambiguous interval may still span a transition,
and its allocation to each local date is computed from absolute time. Midnight
allocation and overlap also use each origin day's recorded timezone.

A transition may also skip local midnight. That does not remove the date: the
local day begins at its first real instant, for example local `01:00` on
2026-09-06 in `America/Santiago`. Ordinary work on such a date is accepted and
allocated there, cross-midnight work spills with the actual local continuation
start, and a wholly skipped civil date is not reported as work. A wall time in
the missing midnight hour still fails with `nonexistent_time`.

These rules are deterministic but intentionally incomplete for repeated-hour
work. A later slice may add an explicit offset or occurrence field; R1 does not
store one.

## Review, confirmation, save, revision, cancellation

`review` renders the last confirmed state and the current staged draft:

```json
{
  "date": "2026-10-22",
  "timezone": "Europe/Amsterdam",
  "revision": 1,
  "saved_revision": 1,
  "dirty": false,
  "confirmed": {"segments": [...], "total_minutes": 105},
  "staged": {"segments": [...], "total_minutes": 105},
  "notes": ["..."]
}
```

Each segment shows its interval ID, date, start/end, minutes, project,
classification, description, evidence references, origin date, `locked`, and
cross-midnight allocations when relevant. Interval IDs are unique across the
store (`iv-YYYYMMDD-n` in R1) and remain stable through staged edits. The
review also lists confirmed revision `history` (revision, confirmation time,
interval count, total minutes, and the confirmed interval snapshot) for later
slices; it grants no remote authority.

- Opening a day copies confirmed work into a staged draft. Mutations affect the
  draft only.
- `confirm` requires a valid draft unless the day already has confirmed work. It
  replaces confirmed work, increments `revision`, and appends local history. An
  explicitly reviewed removal of the final interval is therefore confirmable and
  records a `0`-interval, `0`-minute revision. The response has `"durable":
  false`; confirmation is not yet on disk and is never a remote submission.
- `save` writes confirmed work durably. It requires the current revision and the
  same durable revision the session loaded. It writes atomically to a private
  state file with mode `0600`.
- Save validation uses the candidate durable store: persisted state for every
  other date plus this day's confirmed intervals. Unsaved confirmations on other
  open days are not saved implicitly and do not make a save appear valid. If the
  candidate would overlap, the save fails with `overlap` and the response says
  which recorded days conflict; save or correct the origin day first.
- A successful save receipt describes exactly what restart will load for that
  day. Review and status are session views and may include unsaved confirmations
  on other open days, but those are absent from save receipts and from disk until
  their own save succeeds.
- `cancel` resets the draft to confirmed work. It leaves the confirmed revision,
  saved state, and revision number unchanged.
- `close` never implies save.
- Restart loads only saved confirmed work. Staged edits, and confirmations not
  followed by a successful save, do not survive a restart.

Zero/negative/invalid intervals and unresolved overlaps are rejected before the
draft changes. Staging and confirmation evaluate overlaps in the session view,
so dependent edits can be planned before either day is saved. Saving re-evaluates
the candidate durable state and rejects a dependency that is still unresolved.
Overlaps are absolute-time ranges across stored days, including cross-midnight
allocations and timezone differences. Back-to-back intervals (`10:00` end,
`10:00` start) are allowed.

### Save failure and durability

A save reports success only after the new file is activated and its directory is
synchronized. Failures are split at the rename boundary:

- `write_failed` before activation: the previous durable file is unchanged, the
  in-memory snapshot is rolled back, and retrying after the cause is fixed is
  safe.
- `write_uncertain` after activation, when the directory cannot be opened or its
  durability cannot be confirmed: the visible file may contain the new revision,
  but no durable receipt is returned. Further saves in that session fail with
  `write_uncertain`; blind retries are blocked. Close and restart to reconcile
  with whatever the state file contains. `status` reports
  `"durability_uncertain": true` until restart.

## Durable state, migration, and remote reads (R3)

### State schema 2

State schema `2` is the durable contract for migration, remote identity and plan
records. It adds three optional sections to schema `1`:

- `migration`: append-only receipts (source hashes, dates/timezone, counts, a
  date-scoped ID crosswalk plus the legacy per-date revisions/audit, inert plans,
  approvals, reviews and revalidation list)
  and a repeat-safe source-identity map, plus unconfirmed legacy carry fragments.
- `remote`: stable bindings keyed by (origin interval, allocation date) — the
  binding snapshot plus `last_observed` content, the latest observation per date,
  explicit resolutions, preserved write outcomes and bounded classification
  catalogs.
- `plans`: immutable content-addressed previews. `approved` and
  `approval_available` are always `false`; R3 has no approval.

A schema-1 file is validated in its own shape, upgraded **in memory**, and left
byte-identical on disk. Schema `2` is activated only by a successful durable save
or transaction (the same atomic rename + directory-sync path as a work save), never
by startup. `status` reports `store_schema: 2` (the running schema) and
`durable_schema` (what is actually on disk: `1` until that first write).

A schema-2 file fails closed (`state_invalid`) in a schema-1-only loader. That
forward incompatibility is intentional: a build without migration/remote/plan
semantics must not load receipts, identity decisions, unresolved-write history or
plans it cannot honour. A schema-1 declaration that carries schema-2 sections is
also rejected. A state file claiming `approved: true` or
`approval_available: true` is rejected.

### Explicit legacy migration

Configuration:

```json
{
  "migration": {
    "enabled": true,
    "legacy_root": "/Users/you/records",
    "legacy_timezone": "Europe/Amsterdam",
    "project_map": {"LEGACY-PROJECT": "SYN-1"},
    "classification_map": {"legacy-classification-value": "DEV"}
  }
}
```

Migration is disabled by default. When enabled, the authoritative source is
exactly `<legacy_root>/workflow/YYYY-MM-DD.json` declaring legacy
`schema_version: 1`. Superseded files directly at the root are not current
confirmed state; they are left intact and never promoted. The binary reads only
configured, date-named regular files: no recursive discovery, no request-supplied
path, no symlinked `workflow` directory or source file, no escape from the
resolved root, and no write into the legacy root. The old store constructor is
never called.

Dates are selected explicitly through `migration_preview{dates}` and are bounded
(`62` dates). An over-bound or invalid selection is rejected; it is never silently
truncated. Legacy records do not carry their governing timezone, so
`legacy_timezone` is required and recorded on the migrated days; a changed new
configuration timezone never reinterprets them.

The documented consistency precondition is **quiescent legacy writers**. The
binary re-hashes every selected source file immediately before activation and
fails the whole candidate if any byte changed. It does not pretend an unlocked
multi-file read is atomic with an independent writer.

Preview validates the complete candidate without activation and returns bounded
counts/conflicts plus a `migration_id` bound to `{source hashes, dates, timezone,
current durable target fingerprint}`. `migration_apply{migration_id}` requires a
preview from the same process (a preview grants no standing authority and does not
survive a restart), re-reads and re-hashes the sources, recomputes the candidate,
and rejects a stale or conflicting input without mutation. Activation requires
that **no day is open**, so a draft can never be discarded and no session pointer
can be left stale. The whole candidate is activated in one atomic state write.

Preserved:

- Confirmed work, stable interval IDs (or a deterministic legacy-to-new mapping,
  recorded as a DATE-SCOPED crosswalk in the receipt), projects, descriptions,
  explicit classification values, evidence references plus their non-secret
  provenance pointers, and legacy source revisions/`updated_at` as audit.
  Preserved audit rows are date-associated. A legacy local ID repeated inside one
  source record is `ambiguous_local_id`: it is rejected before any alias or
  binding is guessed, instead of being resolved by row position. A legitimate
  collision across separate dates (or with an existing target ID) uses a
  deterministic date-scoped alias, and mapping/outcome references translate
  through that per-date crosswalk rather than one collapsed map. Time is never
  inferred from evidence and carry proposals are never confirmed.
- Stable local/remote mappings with the binding-versus-last-observed distinction,
  submitted outcomes, legacy plans and approvals. Raw collected provider documents
  are **not** imported; only their count is audited, and archival provenance never
  enables a retired collector.
- Legacy review history PER DATE (not one last-day pointer): the review outcome,
  authored exclusion reasons/timestamps, attendance decisions
  (`evidence_id`, decision, `decided_at`) and acknowledged sources as inert audit.
- Legacy plan history strictly inert: its per-date record, operation contents
  (`remote_id`, date/times, classification, description) and the preconditions it
  was bound to (`local_fingerprint`, record revision, remote observed-at and
  completeness), plus approval/revocation timestamps and status.
- Submitted outcomes with the canonical legacy meaning (see
  `internal/submit/resolve.go`): a resolved row never blocks, and only unresolved
  `in_flight`/`uncertain`/`unidentified` rows block. Confirmed/failed/skipped rows
  are TERMINAL history — their manual-resolution flag being false is not a pending
  write. An unrecognised status is a `malformed_outcome` conflict; it is never
  imported as safe history. Legitimate unresolved rows remain durable blockers,
  survive restart, and are never replayed or automatically cleared.
- Legacy approvals strictly as inert history: `inert: true`, `usable: false`,
  including the approval binding's own `record_revision` and
  `remote_observed_at` (preserved verbatim, never inferred from the linked
  plan, whose values may differ), plus approval/revocation timestamps and
  status. They approve nothing, and
  R3 has no approval or submission operation.
- Unconfirmed legacy carry fragments as unconfirmed durable carries. A separately
  confirmed carry fragment is an ordinary confirmed interval and is not duplicated.

Repeat safety: identical source bytes and the same declared legacy timezone
for already-imported dates are a
`no_change` result with no duplicate intervals, mappings, receipts or history,
including after restart. Receipt evidence is compared per date against the
receipt covering that date, so a repeated subset of a larger imported batch is
a valid no-change selection when its bytes and timezone agree. Changed source
bytes, the same bytes under a different declared legacy timezone
(`interpretation_conflict`: the recorded day/receipt interpretation is
preserved, and no no-change receipt is issued for the different identity), a
selection mixing imported and new
dates, or an occupied destination date are conflicts; migration never
overwrites,
merges by time slot, or rebinds. `migration_apply` validates the freshly
re-read candidate (source identity, durable target, conflicts) before any
successful `no_change` return, so a source or target changed since the preview
is a structured failure, never a false revalidation receipt. Invalid dates, an
unknown source schema, an
ambiguous/nonexistent endpoint, overlapping confirmed work, dangling or
non-one-to-one identities, an unrepresentable project/classification, and
malformed carry/outcome records reject the whole candidate without partially
importing the batch. Preserved audit rows are bounded per category
(2000 provenance pointers, 2000 interval audits, 500 plan audits, 500 approval
audits); a selection above a bound is a `migration_capacity` conflict — rows are
never silently truncated and the whole candidate refuses activation. Original
files and the prior target bytes are preserved; the existing pre-rename rollback
and post-rename `write_uncertain` semantics are unchanged.

Future interval allocation cannot reuse a migrated ID: the day's counter is
advanced past preserved `iv-<date>-<n>` shapes and allocation additionally rejects
any staged/confirmed collision.

### Read-only Gradion boundary

```json
{
  "gradion": {
    "enabled": true,
    "endpoint": "https://mcp.example.invalid/mcp",
    "token_env": "GRADION_MCP_TOKEN"
  }
}
```

Disabled by default. `endpoint` is launcher-provisioned. `token_env` is a variable
**name** (default `GRADION_MCP_TOKEN`); only the compiled binary resolves its value
through the documented dotenv/inherited precedence, including inherited-empty. A
missing credential reports the name only. No Keychain dependency, real settings
read, or implicit live call exists; `version` works without any of it.

The boundary wraps the existing MCP transport and verifies **read capabilities
only**. It requires exactly `timesheet__get_my_week` and
`timesheet__get_classifications`, does not require or call `log_time`, `edit_time`,
a delete tool, a REST fallback or an arbitrary tool name, and exposes no generic
MCP call operation. There are no new retries or dependencies.

The verified week shape is the structured
`{days:[{date,entries:[{id,startTime,endTime,classificationId,description,
durationMinutes,status,task}]}], weekStart,minDate,maxDate,today}` document.
Required containers are validated before any absence is claimed. `days` must be a
list of day objects, and every day object must carry a `date` string and an
`entries` LIST: a day object without `entries` — or with `entries: null` — is
missing/unreadable data, never an empty day. Only an explicit `entries: []` is a
valid empty result for that date. Unknown fields (for example billable state the
read does not expose) stay unknown, never false. A resource-reference envelope,
an empty payload, an unknown shape or a failed read is a read error, never an
empty timesheet. Coverage is explicit: a missing requested date, a provider date
range that excludes it, a malformed entry, duplicate/conflicting IDs or the entry
bound make the observation `complete: false`. A failed fresh observation replaces
the stored complete observation, so an old read can never stand in as current
proof of absence.

`classifications{query}` returns bounded options (the tool's 20-row cap) and a
content-addressed `catalog_id`. Reaching the cap or a truncation header, or a
structured envelope with no text rendering, is `complete: false`; such a catalog
can only show that an option was not returned, never that it is absent.
`select_classification` accepts only an option that this catalog actually
returned, records both the option and catalog identity on the staged interval, and
becomes durable only through `confirm` and `save`. A project default and a
migrated legacy classification are suggestions, never silent remote selections;
`classification_id` is never derived from a title or model text.

### Reconciliation and explicit resolution

`reconcile_remote{day}` performs one fresh read-only observation, persists it, and
surfaces mapped matches, changed/missing mapped remotes, unbound remotes,
overlap/equal-text candidates, orphaned mappings and unresolved-write history.
Identity is the origin interval AND the date an allocation renders on: a
cross-midnight interval has one binding per allocation date. Orphaned means the
bound local allocation no longer exists — the interval was removed, or an edit
moved the work off the binding's date. A bound remote is never a free slot: when
an unmapped allocation would physically overlap an entry bound to another (or
removed) identity, the reconciliation is blocked with `identity_conflict` (or
`orphaned_mapping`) instead of skipping the entry, and the mapping is never
erased or reassigned. Unrelated dates and unrelated mappings elsewhere stay
scoped: a historical orphan only matters where it physically occupies planned
time. A fresh read alone never binds an unmapped entry: overlap or equal text is
only a candidate. An empty local store proves nothing, and remote-only/orphaned
entries never imply deletion.

`resolve_remote` records one human decision bound to the current observation:
`adopt` binds an observed entry to an unmapped local allocation — the allocation's
OWN date, so the later day of a cross-midnight interval can adopt its actual
remote entry even when the origin date is already bound; `accept_observed`
accepts observed content for the SAME already-mapped remote identity and
allocation date; `foreign` acknowledges a remote entry with no local counterpart
(`local_id` omitted) and cannot erase an active binding. A foreign decision is
bound to the exact current observation: an identical fresh observation keeps it,
and a changed relevant observation makes it stale. It rescinds equal-text hints
only — it never grants permission for physical overlap. Stale revisions or
observation IDs, an unobserved remote ID, a time-slot identity substitution, an
adoption that would steal another allocation's remote ID or replace an existing
binding, and malformed local/remote pairs are all rejected without mutation.
Resolutions never clear a migrated unresolved-write blocker and never authorize a
remote mutation.

### Exact plan previews

`preview_plan{day}` uses only saved confirmed durable work, explicit
`select_classification` choices, and a fresh read-only observation of the planned
date. Every plan operation is ONE ALLOCATION: for a cross-midnight interval it
renders the date's own span and minutes (a 23:00-01:00 interval is one
60-minute 23:00-00:00 operation on the origin date and one 60-minute 00:00-01:00
operation on the later date, never two 120-minute origin operations), and each
date's preview observes that remote date. It never plans from an unsaved origin/day
(a dirty open origin fails `dirty_session`), never saves a draft, and never
silently shortens an interval or drops midnight minutes. An allocation whose
verified shape has no representable same-day remote span (a whole-day allocation)
is `unrepresentable_projection` — the work is never altered or declared ready.

- Unchanged mapped work yields no write operation. An interval alone is not a
  remote identity: binding identity is (origin interval, allocation date), so
  both dates may be bound independently.
- A mapped edit carries the stable remote ID, the allocation's intended fields,
  the exact observed precondition, and the remote-only fields the local model
  does not own (`task`, `billable`).
- A create requires complete fresh coverage and resolution of relevant
  identity/overlap ambiguity. Incomplete/unreadable reads, unbound overlap/text
  candidates, entries occupied by another or orphaned identity (`identity_conflict`
  / `orphaned_mapping`), missing/changed mapped remotes (without the matching
  `accept_observed`), unselected classification choices, unresolved-write history
  and any projection the verified contract cannot represent are explicit
  blockers, not altered work.
- Cross-midnight work keeps the origin interval identity and binds the origin
  revision on every per-allocation operation; an equal-text hint is evaluated
  against the allocation's own remote date only.
- Remote-only and orphaned entries are surfaced, never deleted.

The plan identity is a content hash of the previewed day, contributing source
revisions, exact operation content, a remote-content/coverage fingerprint AND the
exact readiness blockers. It excludes audit timestamps, so identical fresh
observations with unchanged decisions keep canonical content and the same
`plan_id`; an actual local or remote change, or a readiness decision, changes it.
A blocked preview and the ready preview that follows an explicit decision are
therefore distinct immutable records — persisting the ready one never reuses or
overwrites the blocked record under the same ID. Persisted plans are immutable and
distinguish `ready` from `blocked`. `approved` and `approval_available` are always
`false`; there is no usable approval and no remote write in R3.

## Configuration


Non-secret JSON only. Unknown fields and invalid values are rejected without
echoing file contents.

```json
{
  "timezone": "Europe/Amsterdam",
  "state_path": "/Users/you/.timesheet/rewrite/state.json",
  "projects": [
    {"id": "SYN-1", "name": "Synthetic Project", "default_classification": "DEV"}
  ],
  "required_env": ["GITHUB_TOKEN", "GCAL_ICS_URL"],
  "sources": {
    "enabled": true,
    "github": {
      "enabled": true,
      "user": "apilot",
      "repositories": ["your/repo", "org/other-repo"],
      "api_base": "https://api.github.com"
    },
    "calendar": {
      "enabled": true,
      "user": "apilot@example.com"
    }
  },
  "migration": {
    "enabled": true,
    "legacy_root": "/Users/you/records",
    "legacy_timezone": "Europe/Amsterdam",
    "project_map": {"LEGACY-PROJECT": "SYN-1"},
    "classification_map": {"legacy-classification-value": "DEV"}
  },
  "gradion": {
    "enabled": true,
    "endpoint": "https://mcp.example.invalid/mcp",
    "token_env": "GRADION_MCP_TOKEN"
  }
}
```

| Field | Required | Meaning |
| --- | --- | --- |
| `timezone` | yes | IANA timezone for newly opened day records. Saved days keep their recorded timezone. |
| `state_path` | yes | Private local state file. Parent directories are created `0700`. |
| `projects` | yes | At least one project. IDs match `[A-Za-z0-9][A-Za-z0-9._-]*` and cannot contain `/`. |
| `required_env` | no | Credential variable **names** for collection. R1-era local operations still do not require them. |
| `sources` | no | Optional collection source config. Omitting `sources` entirely reproduces exact R1 behavior (`no_sources` collection, no gate). |
| `sources.enabled` | yes if present | Master switch; when false, collection reports per-source `disabled` and no gate applies. |
| `sources.github.user` | if enabled | GitHub login used for attribution and filters. `[a-zA-Z0-9-]{1,39}`. |
| `sources.github.repositories` | no | `owner/name` allow list; empty means all repos the search regardless of repository. |
| `sources.github.api_base` | no | Defaults to `https://api.github.com`; normalized service base (kept verbatim after scheme/host). |
| `sources.calendar.user` | no | Attendee address used for attendance PARTSTAT interpretation. The private ICS feed URL itself is supplied only as a credential variable value (e.g. `GCAL_ICS_URL` via `required_env`) — never stored in configuration or state. |
| `sources.calendar.user` | no | Attendee address used for attendance PARTSTAT interpretation. |
| `migration` | no | Disabled-by-default explicit legacy migration provision. Omitted or `enabled: false` means no legacy file is read. |
| `migration.legacy_root` | if enabled | Records root containing the authoritative `workflow/YYYY-MM-DD.json` files. Read-only; never created or written. |
| `migration.legacy_timezone` | if enabled | IANA timezone that governed the legacy records. Recorded on migrated days; never replaced by the current `timezone`. |
| `migration.project_map` / `migration.classification_map` | no | Explicit legacy-value translations. A value not directly representable and not mapped is a visible conflict, never an invented replacement. |
| `gradion` | no | Disabled-by-default read-only remote boundary. |
| `gradion.endpoint` | if enabled | Launcher-provisioned MCP endpoint. Never echoed into a response, receipt, error or state document. |
| `gradion.token_env` | no | Credential variable **name**, default `GRADION_MCP_TOKEN`. Only the binary resolves its value. |

`check_environment` returns `required`, `missing`, and `present` **names** only.
It performs no network call. An inherited empty value overrides a file value, so
that name is reported missing; a file-only value is reported present. A `save`
never requires a remote credential.

R1 has no required or optional credential variable names of its own. The
`required_env` list is an owner-declared, name-only reservation for later named
slices; it never causes R1 local operations to fail or reach the network.

## Environment file and precedence

`--env-file` points at a local file (normally `.env.local`). It is read by the
compiled binary as data. The model/Pi adapter must not read it.

Supported syntax:

- blank lines and lines whose first non-space character is `#` are ignored;
- `KEY=VALUE`; keys match `[A-Za-z_][A-Za-z0-9_]*`;
- unquoted values are trimmed and end at a whitespace-prefixed `#` comment;
- single-quoted values are literal;
- double-quoted values support `\\`, `\"`, `\n`, and `\t`; other escapes fail.

Not supported: `export` prefixes, command substitution, variable expansion,
sourcing, or undocumented dotenv features. Invalid syntax reports only a line
number and a reason; it never echoes the raw line or value.

Inherited process environment values override file values for the same name,
including explicitly empty values. Validation is lazy: missing required values
are reported by `check_environment` (or a future operation that needs them), not
by local review. Diagnostics and errors never contain credential values, raw
dotenv lines, authenticated URLs, or a full config dump.

## Error codes

| Code | Meaning |
| --- | --- |
| `bad_request` | Malformed envelope, missing/extra field, or invalid request ID. |
| `unknown_op` | Operation is not part of this build. |
| `invalid_date` | Date is not a real `YYYY-MM-DD` calendar date. |
| `invalid_grammar` | Typed interval text does not match the documented grammar. |
| `invalid_interval` | Zero/negative duration, invalid time, empty description, or bad evidence reference. |
| `nonexistent_time` | The local start/end wall time is skipped by a timezone transition. |
| `ambiguous_time` | The local start/end wall time occurs twice; this build does not choose an occurrence. |
| `unknown_project` | Project is not configured. |
| `overlap` | Intervals collide in absolute time and require human resolution. |
| `unknown_day` | Day was not opened in this session. |
| `unknown_interval` | Interval ID is not in the staged draft. |
| `stale_revision` | The request's expected revision is not current. |
| `nothing_to_confirm` | Confirmation requested with no draft and no previously confirmed work. |
| `source_unacknowledged` | An incomplete collection attempt on this day is not explicitly acknowledged; `confirm`/`save` fail closed. A never-collected enabled source fails the same gate with the unattempted-source message. |
| `unknown_meeting` | `meeting_id` does not match any proposed meeting for the day. |
| `stale_collection` | The request's `collection_id` does not match this day's recorded attempt; collection changed since the reviewed attempt and no mutation was made (`acknowledge_source` and `decide_meeting`). |
| `store_locked` | Another process owns the single-writer state lock. |
| `state_invalid` | Local state is unreadable, has an unsupported schema, or contains work invalid in its recorded timezone. |
| `write_failed` | Local save failed before activation; previous durable state remains. |
| `write_uncertain` | Local save may have activated a new file but directory durability is unconfirmed; restart before saving again. |
| `config_invalid` | Configuration is missing, unreadable, or invalid. |
| `dotenv_invalid` | Environment file syntax or parsing failed. |
| `migration_disabled` | Explicit legacy migration is not enabled in configuration. |
| `migration_invalid` | The date selection is missing, invalid, or above the documented bound. |
| `migration_stale` | The preview identity, source bytes, or target state changed; nothing was activated. |
| `migration_conflict` | The candidate has conflicts (overlap, occupied destination, unknown project, malformed identity, changed source); nothing was activated. |
| `session_open` | Migration activation was requested while a day is open; activation requires a fresh process with no open day. |
| `remote_disabled` | The read-only remote boundary is not enabled in configuration. |
| `remote_unavailable` | A required read tool or the named credential is missing. Carries names/codes only. |
| `remote_incomplete` | The observation does not provide complete coverage for the requested date. |
| `remote_unreadable` | The read failed or returned an unrecognized shape; this is not an empty timesheet. |
| `stale_observation` | The observation identity is not the current successful observation for that date. |
| `unknown_remote` | The remote ID is not present in the current observation. |
| `unknown_local` | The local interval is not confirmed durable work contributing to that date. |
| `identity_conflict` | The resolution would break one-to-one identity or substitute by time slot. |
| `unknown_classification` | The option was not returned by the named catalog; a partial catalog cannot prove it absent. |
| `stale_catalog` | The catalog identity is not stored; fetch the catalog again. |
| `no_confirmed_work` | There is no saved confirmed work that renders on the requested date. |
| `dirty_session` | An origin day has unsaved changes; save or cancel before previewing a plan. |
| `internal` | Unexpected failure with no safe detail. |

## Security boundaries and R3 limits

- stdout is protocol output only in `serve` mode; diagnostics stay on stderr.
- Requests cannot supply paths, executable names, shell commands, or arbitrary
  dispatch.
- No shell is launched. Network access is collection-only and goes exclusively
  to the configured GitHub API base, the credential-held ICS feed URL, and the
  launcher-provisioned read-only MCP endpoint, over HTTPS unless an explicitly
  configured local endpoint targets a local service. Credentials are read only
  through the documented dotenv/process precedence and are never logged, echoed,
  or included in receipts, state or errors. Collection/remote receipts and error
  details carry stable codes, HTTP status and bound counts only; raw provider
  documents and authenticated URLs are never echoed into the protocol.
- Migration reads only configured legacy files. It never writes into the legacy
  root, never discovers dates recursively, and never falls back to the old store
  constructor. Raw legacy collected-provider documents are not imported.
- An incomplete-source gate binds `confirm` and `save` to explicit human
  acknowledgment of every incomplete collection attempt; a new attempt always
  requires a fresh acknowledgment.
- Evidence is verify-only: collection never creates, edits, or extends worked
  minutes. Meeting decisions and attendance flags are not minutes.
- The remote boundary is read-only. It verifies exactly the two read tools and
  has no approval, submission, delete or arbitrary-call operation. Preserved
  legacy approvals are inert (`usable: false`) and cannot authorize anything; a
  state file claiming approval authority fails closed.
- State schema is `2`. A schema-1 file loads unchanged and is upgraded in memory;
  schema `2` is written only by a successful durable save/transaction. A
  schema-1-only loader rejects a schema-2 file with `state_invalid` and leaves it
  intact. A pre-R2 build rejects an R2 evidence file the same way.
- The production CLI (`cmd/timesheet`) and legacy packages are unchanged; the
  development entry point `cmd/timesheet-rewrite` carries only usage text, and
  its text now describes the implemented R3 scope. The `serve` protocol in this
  document is the authoritative interface.
- Later slices must add approval/submission execution and Pi integration. Do not
  present those as implemented: R3 plans are previews, never approvals.
