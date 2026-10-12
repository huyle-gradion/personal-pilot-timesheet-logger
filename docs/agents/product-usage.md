# Product usage: local and connected (remote) daily workflow

This is the practical operator guide for the `timesheet` CLI and the `/timesheet`
skill. It reflects the current binary at HEAD `6d28f7a` (`go run ./cmd/timesheet
help`). Reference detail lives in the linked notes; this guide links rather than
repeating the full flag list.

Two workflow modes, defined by the owner:

- **Local** — collect evidence from **both** local and configured **remote**
  sources, then review and export. **Do not submit to Gradion.**
- **Remote** (connected) — the same evidence and review, then an **exact preview**,
  an **explicit approval**, and a **Gradion submission**.

Two traps to keep straight:

- The workflow words **local/remote** are not the git source's `config.mode`
  (`local` | `remote` | `both`). A "local workflow" may still collect from a
  remote git source and a calendar feed.
- **`--offline` is not a synonym for the local workflow.** `--offline` only skips
  the **Gradion** remote seam (its reads and writes). Configured **source**
  fetches — calendar ICS, git (including remote GitHub), and the journal note —
  still run, and a remote git source still uses the network.

> Current session authority: **production submission is blocked.** The connected
> (remote) commands below are documented so the gated steps are known, but they
> must **not** be executed. Approval and submission are always separate, explicit
> steps and are never performed by `run`.

## Build and setup

Go `1.27.1` (the `go` directive in `go.mod`). The terminal UI uses the pinned
Charm v2 modules already in `go.mod` (`charm.land/huh/v2 v2.0.3`, plus
bubbletea/bubbles/lipgloss and their indirect pins). You do **not** need to
install the latest Charm modules; the pinned versions build as-is.

Build a local binary once and use it as `$TS`:

```bash
cd /Users/you/src/assignment-1
go build -o ./bin/timesheet ./cmd/timesheet      # build once
TS="./bin/timesheet"
```

Use the compiled binary for interactive sessions (`run --interactive` and
`run --plain`). `go run ./cmd/timesheet` still works — keep it for a one-off
headless command — but it **recompiles and links the program on every
invocation**, so a guided, multi-step session pays that build cost at each call.
Build once and reuse `./bin/timesheet` instead.

Use a **private** storage directory under your home for records and selection
state — never a shared `/tmp` path. `--storage-dir` overrides the config's
`storage_dir`; the default is `~/.timesheet/records`. Non-secret selection state
(catalog cache, recent choices) lives under `~/.timesheet` (or `TIMESHEET_HOME`).

Never commit, paste, or print real credential values or a configured secret feed
URL. Configs hold `keychain:<service>` references only. Do not reuse the owner's
private scratch config; start from a shipped template and use your own
placeholders.

## Configs and templates

Start from a shipped, sanitized template and replace its placeholders with your
own paths:

| Config shape | Template | Purpose |
| --- | --- | --- |
| Local and remote evidence, with Gradion reads | `cmd/timesheet/timesheet.local-git.example.json` | Calendar + git `mode:"both"` (local checkouts + remote GitHub repos), plus a Gradion endpoint. Supports local review without submission or the separately gated full lifecycle. `--offline` skips its Gradion access only. |
| Connected Gradion (optional) | `cmd/timesheet/timesheet.example.json` | Calendar + local git plus a `gradion` endpoint + credential ref → remote workflow (future gated submit) |
| Personal (no remote, no credential) | `cmd/timesheet/timesheet.personal.example.json` | Journal + local-only git; no Gradion endpoint, no credential |
| Journal + local git | `cmd/timesheet/timesheet.journal.example.json` | Journal source + local-only git |

The `timesheet.personal.example.json` template ships **placeholder** values — the
`vault` root (`/Users/you/vault`), the git `author` (`you@example.com`), and
`local_repos`/`repos` — and every one **MUST be replaced** with your own local
paths before use. The journal `vault` is a **read-only input**; the managed
timesheet export writes a **separate** path (the same vault/`daily_dir`, but only
the marker-delimited section — see [Export and persistence](#export-and-persistence)).

Top-level config keys (all non-secret; validated by `preflight`):

| Key | Meaning |
| --- | --- |
| `evidence` | `{ id, timezone, sources[] }` — the timezone and the evidence sources |
| `storage_dir` | Workflow record directory (default `~/.timesheet/records`) |
| `vault` + `daily_dir` | Managed daily-note export root and its relative daily directory |
| `gradion` | `{ endpoint, credential_ref }` — optional; `credential_ref` is `keychain:<service>` |
| `editorial` | Optional editorial agent (`enabled`, `adapter`, `command`, ...) |
| `classifications` | Optional project → classification-id map (a **suggestion** only) |

Credential-**reference** fields must be `keychain:<service>` references, never a
raw value: the calendar source's `url_ref`, `gradion.credential_ref`, and
`editorial.credential_ref`. A raw value in one of those declared fields is
rejected (preflight/`loadConfig`). This is a check on declared credential
references, not a general scanner for arbitrary secret-looking text elsewhere in
the config. Secrets live in the macOS Keychain and are referenced as
`keychain:<service>`.

## Source configuration

Each source is one entry in `evidence.sources` with `type` and a `config` string
map. Full key semantics: [collection configuration](collection-config.md) and
[journal source](journal-source.md).

**Journal** (`"type": "journal"`) — the read-only Obsidian daily note:

| Key | Meaning |
| --- | --- |
| `vault` | **Required** vault root (read-only input) |
| `daily_dir` | **Required** relative daily-note directory, e.g. `02 - Daily & Logs` |
| `fixture` / `fixture_date` | Test-only contract inputs; never required at runtime |

**Git** (`"type": "git"`):

| Key | Meaning |
| --- | --- |
| `mode` | `local` \| `remote` \| `both` (absent ⇒ `both`) |
| `local_repos` | Comma-separated local checkout paths (absent ⇒ `["."]`) |
| `repos` | Comma-separated host-less `owner/repo` slugs; required when `mode` includes `remote` |
| `author` | Optional local-commit author filter (email or name) |
| `project_map` | Optional JSON object `owner/repo` → project |
| `fixture` | Test-only fixed input |

A `mode:"local"` git source contacts no GitHub and needs **no** GitHub
credential; `remote`/`both` need one (resolved from `GITHUB_TOKEN`, `GH_TOKEN`,
Keychain, or a `gh` probe).

**Calendar** (`"type": "calendar"`):

| Key | Meaning |
| --- | --- |
| `url_ref` | **Required** `keychain:<service>` reference to the ICS feed URL (never a raw URL) |
| `user_email` | Optional owner email for invitation matching |
| `timezone` | Optional IANA timezone for date-window parsing |
| `cache_path` / `no_cache` | Optional local ICS cache controls |

The calendar `url_ref` is resolved in memory at collection time and never
persisted.

**Editorial** (`editorial`): `enabled`, `adapter` (`none`/`command`), `command`
(the agent argv), optional `credential_ref`. With `enabled:false` or
`adapter:"none"` the run uses deterministic text.

**Gradion** (`gradion`): `endpoint` (MCP URL) and `credential_ref`
(`keychain:<service>`). Present ⇒ connected (remote) mode.

### Evidence count, no sources, and incomplete sources

`review` prints one line per enabled source that is not complete/skipped, and
distinguishes conditions:

- **No sources configured** → `No evidence sources configured.` (distinct from a
  configured day that legitimately returned nothing).
- **Configured but no evidence** → an empty draft; not an error.
- **Incomplete source** (a missing journal note `JOURNAL_NOTE_MISSING`, a missing
  vault `JOURNAL_VAULT_MISSING`, an unreadable note, a calendar/remote failure)
  → the source is shown and the preview **blocks** with `incomplete_collection`
  until acknowledged with `--ack <source-id>` (or a decisions `acknowledge`
  entry). A missing journal note is non-fatal and never aborts the run.

## Workflow modes and the matrix

`run` picks a mode by precedence (highest first): `--decisions FILE` /
`--non-interactive` → scripted; `--plain` → safe line-based; `--interactive` →
terminal UI (only when stdin **and** stdout are real terminals and the environment
is not CI, else it **refuses**, never silently falls back); default → scripted, so
headless behavior and exit codes are unchanged. `--plain` and `--interactive`
apply to `run` only; the guided run reviews one day at a time.

| Workflow | Entry | Command | Who |
| --- | --- | --- | --- |
| Local | CLI guided (real TTY) | `$TS run --interactive --config CFG --date D` | A human at a terminal |
| Local | CLI plain (pipe/CI safe) | `$TS run --plain --config CFG --date D` | Human over a pipe; EOF aborts |
| Local | CLI scripted (explicit) | `$TS run --decisions decisions.json --non-interactive --config CFG --date D` | Automation |
| Local | Skill | `/timesheet` (prompt) | Human via the skill |
| Remote | CLI guided/plain | same `run` as local, then `preview` → `approve --plan` → `submit` | Human (future gated submit) |
| Remote | CLI scripted | `run --decisions …`, then `preview` → `approve --plan` → `submit` | Automation (future gated submit) |
| Remote | Skill | `/timesheet` (prompt) | Human via the skill |

`run` never approves or submits. The guided run stops at the exact preview and
export and prints a single `approve --plan <PLANID>` follow-up. Details:
[guided workflow](guided-workflow.md).

## Running the local workflow

Throughout this section, `CFG` is the path to your config file and `D` is the
target date:

```bash
CFG=./timesheet.local.json
D=2026-10-06
```

### 1. Discover the day

The first pass collects evidence and prints the proposal. Run it once to
**discover the actual block IDs** before writing decisions:

```bash
$TS run --interactive --config "$CFG" --date "$D"      # guided
# or, non-interactive discovery:
$TS review --config "$CFG" --date "$D" --no-editorial
```

`review` prints `work`/`meeting`/`manual` blocks with their stable IDs, plus
untimed **narrative** journal candidates (section, provenance, `duration=unknown`).

### 2. Apply review decisions (scripted)

`--decisions FILE` applies a serializable decision set through the same
`internal/review` actions the guided run uses. `{date}` in the path is substituted
per day. Example (the actual `run.ReviewDecisions` fields):

```json
{
  "confirm":     [{"block_id": "<BLOCK_ID>", "project": "Proj", "attendance": true}],
  "exclude":     [{"evidence_id": "<EVIDENCE_ID>", "reason": "user_excluded"}],
  "join":        [{"work_block_id": "<BLOCK_ID>", "meeting_ev_id": "<EVIDENCE_ID>"}],
  "trim":        [{"block_id": "<BLOCK_ID>", "new_start": "09:15", "new_end": "09:45"}],
  "project":     [{"block_id": "<BLOCK_ID>", "project": "Proj"}],
  "description": [{"block_id": "<BLOCK_ID>", "text": "User-authored description"}],
  "classification": [{"block_id": "<BLOCK_ID>", "classification_id": "<ID>", "activity_type": "<derived>"}],
  "manual":      [{"uuid": "<UUID>", "start": "2026-10-06T09:00:00Z", "end": "2026-10-06T11:00:00Z", "project": "Proj", "description": "Pairing", "evidence_ids": ["<EVIDENCE_ID>"], "confirm": true}],
  "mark_empty":  false,
  "acknowledge": ["journal-work"]
}
```

Apply it in one non-interactive pass (or use the explicit `review` step):

```bash
$TS run --decisions decisions.json --non-interactive --config "$CFG" --date "$D"
# equivalently, the explicit review step:
$TS review --config "$CFG" --date "$D" --decisions decisions.json
```

Notes (exact keys, from the current source):

- **Confirm** requires an explicit `confirm` decision; a proposed block is never
  confirmed automatically. `attendance` applies to meetings.
- **Manual** — `confirm` defaults to **false**: without `"confirm": true` the
  interval is only *proposed* and needs a later confirm. `evidence_ids` links one
  user-timed interval to several supporting records (for example journal items);
  the links add **no minutes** and the interval's own span is authoritative. Pass
  a stable `uuid` to edit the same interval.
- **Description** and **Classification** reach every fragment of a split block.
- **Classification** changes invalidate a prior approval for the date.

### Bulk actions in the guided review

The guided review (`run --interactive` / `run --plain`) offers an optional **Bulk
actions** step, placed between the per-block review and the classification step.
It offers exactly four owner-confirmed actions over exactly one mutually-exclusive
scope:

- **Bulk-confirm** eligible blocks.
- **Bulk-exclude** blocks.
- **Bulk-assign a project** (the label states `assign-project X (confirms the
  block)`, because assigning a project stages the block's confirmation too).
- **Bulk-assign a classification** (one action per run): the user picks a single
  classification through the same searchable catalog UI, and the step previews the
  **exact classifiable intervals and counts** before consent. It stages the
  classification on the same atomic review only; an already-classified interval is
  retained (skipped), and an abort stages nothing. The per-block classification step
  does **not** re-prompt an interval the bulk action staged (it is disclosed as
  bulk-classified and skipped): per-block override holds at the **data level**
  (add-if-absent staging; a per-block decision for the same interval replaces the
  bulk choice), and the UI override path is the printed
  `timesheet classify select --block-id <ID>` follow-up.

**Effective eligible set.** Bulk-confirm/assign eligibility is "a **known
duration**, not a carryover, and **preselected**". In the current proposal only an
**accepted meeting** satisfies all three: work blocks are `duration=unknown`,
manual blocks and joint blocks are `Preselected=false`. So the effective
bulk-confirm/assign set is **accepted meetings with a known duration**; a `work`,
`manual`, or `joint` scope is **inert** for confirm/assign (its blocks land in the
`skip` buckets, not the applied set). Bulk-exclude applies to any evidence-backed
block.

Scopes: `all`, `by kind` (`work`/`meeting`/`joint`/`manual`), `by project`, or an
explicit block-id list. The step prints the **exact affected-block list** with each
block's per-block action and the counts (`confirm N`, `skipped unknown-duration M`,
`requires explicit collision K`) **before** requiring an explicit consent. Nothing
is staged until that consent, and nothing is written until the single atomic review
apply, so an abort applies nothing.

Bulk never resolves a conflict silently:

- an **unknown-duration** block (a git observation window) is never bulk-confirmed;
- a **carryover**, a non-preselected meeting, or a joint block requires an explicit
  per-block confirmation;
- a **meeting that overlaps a work/manual interval that is already confirmed
  (durably, or per-block in this session) or that this bulk action would confirm**
  is excluded and surfaced as requiring an explicit collision decision (joining
  stays per-block). An overlapping in-scope block that is merely **skipped** is not
  a collision, because confirming the meeting alone creates no overlap;
- a **bulk project assignment** never bypasses the normal project resolution;
- a **nonempty day is never marked empty**.

Per-block decisions always win: bulk staging is add-if-absent, so a per-block choice
is never overwritten regardless of order. An empty scope prints
`no blocks match this scope; nothing staged` and stages nothing.

Known boundaries of the bulk step:

- A bare per-block **"Leave unconfirmed"** stages nothing, so a later bulk-confirm
  **is** authoritative over it (the preview shows the block in the applied set).
- The exclude warning scans the **durable** confirmed intervals, not in-session
  staged confirms; an in-session confirm that a later bulk-exclude would drop is
  applied as consented, without a warning.
- The pre-existing "no staged work ⇒ mark empty" rule still applies: if every
  in-scope block is skipped and nothing else is confirmed, the day is marked
  reviewed-empty (bulk never sets `MarkEmpty` itself).
- A degenerate **zero-length** confirmed interval (already flagged
  `unconfirmed_duration` by readiness) is treated by the collision gate as a
  full-day span, so it can over-block a meeting; fix the interval rather than
  relying on the bulk step to resolve it.
- The collision gate compares **same-day clock-minute spans**: a genuinely
  post-midnight confirmed interval is not numerically compared against a
  same-clock-day meeting. This is largely precluded by the day-scoped record
  model (a cross-midnight interval is split at local midnight).

### 3. Classification (explicit, authoritative)

Classification is an explicit choice; config maps and recent selections are
**suggestions only** and are never auto-applied. An unset classification blocks
the preview (`unresolved_classification`).

```bash
$TS classify list --config CFG                 # live; --cache reads the cache; --json for machine output
$TS classify select --config CFG --date D --classification "<CODE|ID>" --block-id "<BLOCK_ID>"
$TS classify select --config CFG --date D --classification "<CODE|ID>" --all   # explicit bulk
```

`classify list` prints `Name (Code) [Activity Type] · <id-prefix>` with a
`complete|partial` marker and provenance/freshness. The Activity Type is **derived
by the server** from the classification and is never sent. `classify select`
requires an **exact unique** match (id, code, or unambiguous id-prefix); a
**stale** `--cache` catalog is refused unless `--force`. Both `classify list` and
the guided classification step surface, labelled `unapplied`, up to the **3 most
recent** selections, or — when there is none — the owner's **first-run** suggestion
`INTGRADI2610 (Gradion Intern Academy 2026)` with its derived `internal project`
type (resolved against the fetched catalog; never fabricated). A recent selection
takes precedence over the first-run suggestion. In the guided step a catalog search
with no match says the catalog is **partial** and offers search again — it never
asserts absence. The guided run prints a
`timesheet classify select --date D --block-id <ID> --classification <CODE|ID>`
follow-up to change an interval that already carries a classification. See
[classification selection](classification-selection.md).

### 4. Preview and export

```bash
$TS preview --config CFG --date D     # zero remote writes; prints plan_id, operations, total
$TS export  --config CFG --date D     # re-render the managed note; never contacts the remote
```

`preview` prints the exact creates/edits, intervals, projects, and total. In the
local workflow you stop here (or after the guided run's export). **Approval is
optional locally** because nothing is submitted; approval exists only to gate a
remote submission.

## Running the connected (remote) workflow

Connected mode adds a `gradion` endpoint. The evidence and review steps are
**identical**; only the final gated steps differ. Run the same `run`/`review`
above, then:

```bash
# FUTURE GATED STEPS — do not execute while production submission is blocked.
$TS preview --config CFG --date D
$TS approve --config CFG --date D --plan "<PLANID>"     # binds approval to the exact plan hash
$TS submit  --config CFG --date D                       # executes only the record's ACTIVE approval
```

- **Approval is bound to the exact plan content hash** and is per-day. Any local,
  plan, or remote change invalidates it; re-run `preview` and `approve` with the
  new plan id.
- **Submit re-proves every selected classification against a fresh LIVE remote
  result** before any write (a fresh cache never proves identity). Each choice is
  looked up by its recorded code (or name); a legacy selection with no recorded
  code/name fails closed with a `reselect_classification` diagnostic and zero
  writes, unless a fresh live full complete catalog proves it. See
  [classification selection](classification-selection.md).
- **Submit is currently blocked**: live Gradion create/edit semantics are
  unverified and this session holds no write authority. The command is documented
  for the future gated step only.
- **Local connected mode still does live reads.** With a Gradion endpoint and a
  resolved credential, `preflight`/`review`/`preview` perform read-only MCP calls
  (capabilities, catalog, remote listing) **when the user authorizes them** — but
  no submission. `--offline` disables exactly those Gradion calls.
- **Uncertain writes stop the run** and are never retried blindly. Reconcile later
  with an explicit `resolve` decision (a future gated step).

## Skill usage

The skill delegates to the same binary; it parses no vault and performs no
accounting. It never auto-confirms, auto-approves, or auto-submits, and a skill
request is **not** standing remote-write authority.

- **Local, interactive (human at a terminal):** the skill runs `run --interactive`;
  you confirm each choice, and the run stops at the preview.
- **Local, headless (agent/automation):** the skill uses the scripted path
  (`run --decisions … --non-interactive`, or `review` then `export`). Agents
  cannot drive the TUI, so a headless run uses `--decisions` (or `--plain` over a
  pipe).
- **Remote:** the same `/timesheet` flow, then the explicit `preview` →
  `approve --plan` → `submit` gate — documented, not executed here.

Example prompts (any of the skill's triggers work; the skill picks the mode from
the environment — a real terminal for `--interactive`, otherwise the headless
path):

| Workflow | Context | Prompt |
| --- | --- | --- |
| Local | Human at a terminal | `/timesheet review today from my journal and git, no submission` |
| Local | Agent / headless | `/timesheet daily timesheet for 2026-10-06, no editorial, no submission` |
| Remote | Human at a terminal | `/timesheet review today and prepare the submission plan` |
| Remote | Agent / headless | `/timesheet review 2026-10-06 and prepare the submission plan for approval` |

In every case the skill delegates to the binary and stops before submission; a
remote prompt prepares the exact plan for a human to `approve`/`submit` later.

## Privacy and editorial

- **Headless:** pass `--no-editorial` (or configure no editor) for
  deterministic-only text.
- **Configured editorial:** the optional agent proposes titles/descriptions only
  (TEXT ONLY); it can never alter intervals, attendance, projects, evidence, or
  approvals. **Private journal note text is never forwarded** to the editorial
  service, including a manually linked journal description. Fallback to
  deterministic text is automatic on any failure.

## Export and persistence

- The **journal collector is read-only**; `run` may separately **write the managed
  export** (the `<!-- timesheet:begin v1 -->` … `<!-- timesheet:end -->` section
  in `<vault>/<daily_dir>/<date>.md`), preserving everything outside the markers
  byte-for-byte.
- **Aborting a guided run** applies **no unfinished confirmation, approval, or
  submission** before the final deliberate local-review confirmation, and prior
  reviewed state is preserved. Collection and optional editorial synthesis may
  already have persisted their own durable state.
- **Existing confirmed work is safe:** a reviewed-empty day stays empty with no
  invented zero-hour block, and a refresh never drops confirmed work.
- **Journal evidence is narrative and untimed:** zero start/end, unknown duration,
  never a working time. Editing an implicit item changes its identity, so the old
  item is **detached** history (retained, never re-proposed); an optional explicit
  `<!-- evidence_id: X -->` keeps identity across edits. Duplicate explicit or
  implicit ids in one section **fail visibly** rather than silently collapsing.
- **Only recorded work is captured** — this never establishes a complete day.

## Troubleshooting

Exit codes: `0` success, `1` error, `2` usage error, `3` blocked.

| Symptom | Cause / fix |
| --- | --- |
| `No evidence sources configured.` | No enabled sources — add sources or accept an empty draft |
| `JOURNAL_NOTE_MISSING` / `JOURNAL_VAULT_MISSING` | Journal note/vault absent — fix the path or `--ack <source-id>` |
| `incomplete_collection` blocks preview | An incomplete source needs `--ack <source-id>` |
| `unresolved_classification` | An interval has no classification — `classify select` |
| `readiness:unconfirmed_duration` | Supply and explicitly confirm a valid working interval; do not infer duration from narrative evidence |
| `readiness:not_attended_as_work` | A meeting marked not attended is still included as work; exclude it or correct the reviewed attendance |
| `readiness:unresolved_project` | Resolve the interval's project explicitly |
| Catalog `partial` / `STALE` | The catalog is truncated or old — `classify list` (live) or `--force` deliberately |
| `uncertain` submission | A write's outcome is unknown — the run stops; **never retry blindly**; `resolve` later (future gated) |
| `--interactive` refused | Not a real terminal or under CI — use `--plain` or `--decisions` |
| Piped/`tee`d output | Not a TTY — the TUI will not start; use `--plain` |

## Reference

- [Guided workflow](guided-workflow.md) — `run --interactive`/`--plain` modes and review contents
- [Journal source](journal-source.md) — journal keys, identity, active/detached, privacy
- [Collection configuration](collection-config.md) — git/calendar keys and preflight semantics
- [Classification selection](classification-selection.md) — catalog and explicit selection
- [ADR 0007](../adr/0007-journal-evidence-source.md) — journal evidence decision

## Hermetic retest

The named offline harness drives the real orchestration with a sanitized note and
no credential, remote, or live service. It is **not** a live or real-terminal
verification:

```bash
go test ./cmd/timesheet -run '^TestGuidedOfflineHarness$' -count=1 -v
```
