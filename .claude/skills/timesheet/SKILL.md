---
name: timesheet
description: Review-first daily timesheet workflow over the shared deterministic Go core. Collects evidence (including the optional local journal), proposes intervals, applies review decisions through a guided or scripted run, previews and approves an exact submission plan, and exports a managed daily note. Trigger with /timesheet, log timesheet, or daily timesheet.
---

# Timesheet Skill (`timesheet`)

This skill is a **harness-agnostic command sequence over the `timesheet` Go
binary**. It orchestrates; it implements **no accounting and no submission logic
of its own**. Every deterministic decision lives in the shared core
(`internal/run`), so the CLI and the skill exercise one implementation. The skill
never parses the vault, never accounts, and never confirms, approves, or submits
on its own.

## Boundary: run the binary, never read the implementation

**The binary is the contract.** The repository's design is one shared
implementation exercised by both the CLI and this skill, so a decision derived from
reading the source describes what the code happens to do today, while the tool the
agent is supposed to run is the binary. Those two drift apart, and the drift is
invisible: an agent that completes the interface from the implementation silently
stops following the documented workflow. This skill therefore documents the
interface **completely**, and the agent that uses it reads nothing else.

**Allowed reads.** The skill's own files (`SKILL.md`, `workflow.md`), the config
document, the workflow record, the daily note, and the binary's own `--help` output
and standard output.

**Forbidden.** `internal/`, `cmd/`, `docs/`, any Go file, any `grep` of the
repository source, and any `git log` archaeology of the implementation. Grep your own
records, the config, or the daily note freely; the prohibition is on searching the
implementation.

**Every cross-reference to `docs/` in this file or in [workflow.md](workflow.md) is
maintainer provenance, not an agent read**, wherever it appears — including the ones
below that say a detail "lives" there. An agent must not follow one. This skill
documents every command, every flag, and every JSON document an agent needs,
completely, in these two files: no Go file and no `docs/` file ever has to be opened
to run the workflow. If something an agent needs appears to be missing, that is a
defect in this skill; report it rather than reading the code (see the stop rule
below).

## When the workflow is silent (stop rule)

If a command fails in a way this workflow does not cover, or the workflow is silent
on something a step needs, **STOP and report it as a skill defect**. Do not open a
Go file, do not grep the implementation, and do not infer the missing behaviour from
the source. Report what you were doing, the exact command and its output, and what
you needed. Every gap then becomes a skill fix rather than a detour, and the next
agent reads the fact here instead of rediscovering it from the code.

## Triggers

Invoke this skill when the user issues any of the following triggers:
- `/timesheet`
- `log timesheet`
- `daily timesheet`
- `review timesheet`

## Contract (binding)

- **The guided run never approves or submits automatically.** `run --interactive`
  (or `run --plain`) stages the day's review, prints the exact submission preview
  and the total, and stops with a single `approve --plan <PLANID>` follow-up.
  Approval and submission stay separate, explicit commands. **Production
  submission is not performed or authorized here.** See
  [guided workflow](../../../docs/agents/guided-workflow.md).
- **AI is optional and TEXT ONLY.** It may propose titles and descriptions. It can
  never change intervals, attendance, projects, evidence links, or approvals. With
  `--no-editorial` (or no editor configured) the workflow still completes using
  deterministic text. The editorial agent is invoked through a configured command
  adapter.
- **Approval is bound to an exact submission plan content hash.** There is no bare
  approval flag. `approve --plan <PLANID>` records a durable approval, and `submit`
  executes only the record's **active** approval. If there is no active approval,
  or a requested `--approval` does not match it, `submit` refuses with **zero
  remote writes**.
- **Uncertain remote writes are never retried.** An uncertain, unidentified, or
  content-unverified write STOPS the operational session: it is recorded as
  unresolved and blocks replay until an explicit `resolve` decision. A write that
  merely acknowledges an entry's identity is NOT a confirmed write; its content is
  verified by a bounded read-back first, and a mismatch or an unverifiable
  read-back leaves it unresolved (never a false confirm). Remote entries are
  editable but never deleted.
- **A confirmed interval can be retracted with the `unconfirm` decision, and a whole
  day reset with `reset`.** An interval that has been submitted cannot be removed:
  the remote entry is editable but not deletable, so correct it with an edit. Both
  actions invalidate the day's active approval, and `reset` retains submission
  history, remote mappings, evidence and plans.
- **Recovering from an unresolved write requires SEPARATE owner authority.** No
  self-adoption, no new-plan approval, no source hot-patching, and no debugging
  writes without that separate authority. A conversational "go" confirms ONLY the
  immediately presented exact plan, and ONLY when submission authority is already
  established; it cannot authorize a different plan and never implicitly grants
  debugging or source-repair authority. These STOP rules are
  **instruction-enforced** at this boundary (the skill must not act on its own);
  the unresolved-write STOP itself is additionally **code-enforced** (the run
  halts and the restart gate blocks replay).
- **The export is marker-delimited and non-destructive.** It rewrites only the
  block between `<!-- timesheet:begin v1 -->` and `<!-- timesheet:end -->` and
  leaves every other byte (frontmatter, journals, legacy sections) untouched.
- **Secrets live in the macOS Keychain.** Configuration holds `keychain:<service>`
  references only and never a raw secret.
- **Classification is an explicit choice.** `log_time`/`edit_time` receive only a
  `classificationId`; the activity type is derived server-side and never sent. The
  project→classification map and the recent-selection preference may **suggest**
  only — an unset classification blocks the plan, and `--all` bulk selection
  requires the explicit flag. The explicit choice records the catalog option's
  **code** (preferred) and **name** as retrieval metadata only; at `submit` each
  choice is re-proven against a fresh LIVE remote result (a cache is never proof),
  and a legacy selection with no recorded code/name fails closed with a
  `reselect_classification` diagnostic, unless a fresh live full complete catalog
  proves it.
- **The journal source is read-only narrative evidence.** ai-database is an
  optional evidence input and never the primary store. The journal **collector**
  never writes it; the separate managed export boundary still writes only the
  marker-delimited timesheet section (see below). A journal note yields
  **UNTIMED** items (zero start/end, unknown duration, retained provenance); it
  never establishes working time. Only recorded work is captured, and a missing
  note is a visibly incomplete source that requires an explicit acknowledgment
  (`--ack`). Raw note text is never sent to an external service. See
  [journal source](../../../docs/agents/journal-source.md).
- **One manual interval may cite several evidence sources.** A user-timed interval
  can link multiple supporting records; the links add no minutes and the
  interval's own span is authoritative.
- **Pre-existing remote entries for the target date are not enumerated by any
  supported surface.** `preview` re-observes the remote **read-only** and reports
  it — `remote observed:`, `remote complete:`, and `remote=<id>` on each operation
  it would edit — and that is the sanctioned check before any submission. It is
  **not** a duplicate-detection enumeration: a full listing of a date's remote
  entries is unavailable from any supported command, and issue #28 records the
  disposition of the untracked `list-entries` diagnostic, which requires rebuilding
  that capability as a supported subcommand. Never reach for `bin/list-entries`; that
  binary is built from an untracked `cmd/` directory and is not a supported
  surface.

## Execution workflow

Refer to [workflow.md](workflow.md) for the exact command sequence. Every step is
either deterministic Go code (`[Script]`) or the optional, bounded editorial agent
(`[AI]`) invoked through the core — the skill itself never synthesizes or accounts:

1. **Step 1: `[Script]` Preflight** (`timesheet preflight`)
2. **Step 2: `[Script]` Collect and propose** (`timesheet review`)
3. **Step 3: `[AI]` Editorial synthesis (optional)** (delegated by `timesheet review`)
4. **Step 4: `[Script]` Apply review decisions** (`timesheet review --decisions`)
5. **Step 5: `[Script]` Classification selection** (`timesheet classify list`, then the user selects, then `timesheet classify select`)
6. **Step 6: `[Script]` Submission preview** (`timesheet preview`)
7. **Step 7: `[Script]` Approval** (`timesheet approve --plan`)
8. **Step 8: `[Script]` Submit and export** (`timesheet submit`)

The default `run` is scripted and headless, unchanged. `run --interactive` (a real
terminal) or `run --plain` (safe on a pipe) walks the review instead; `--decisions
FILE` or `--non-interactive` forces the scripted path and takes precedence. The
TUI never runs on a pipe or under CI and never silently falls back. Cancelling a
guided run before the final deliberate local-review confirmation applies **no
unfinished confirmation, approval, or submission** change, and prior reviewed
state is preserved; collection and optional editorial synthesis may already have
persisted their own durable state. The guided run stops at the exact preview and
export and prints the `approve --plan` follow-up — it never approves or submits.
Details: [guided workflow](../../../docs/agents/guided-workflow.md).

For a guided/interactive run, prefer a **prebuilt binary**: build once
(`go build -o ./bin/timesheet ./cmd/timesheet`) and invoke `./bin/timesheet`,
rebuilding only if it is missing. `go run ./cmd/timesheet` recompiles and links
on every invocation, which is wasteful for a multi-step session. The choice is
harness-agnostic and changes no command semantics.

Classification is an **explicit user choice**. Repository mappings, calendar text,
AI, and config defaults may **suggest** only — they are never applied silently. The
skill fetches the catalog (`timesheet classify list`), ASKS the user to choose, and
applies the explicit choice (`timesheet classify select`). `--all` is the visible
bulk option and requires explicit consent; an unset classification blocks the plan.

Optionally, and **non-blocking**: `timesheet metrics import --transcript "$PI_SESSION" && timesheet metrics report --date "$DATE"` records tokens/day (3.6); a metrics failure only warns and never changes a workflow exit code or triggers submission. See [workflow.md](workflow.md).

Preflight validates source **configuration** (not live availability) and runs no
fixtures; a `mode:"local"` git source passes offline with no GitHub credential.
See [workflow.md](workflow.md) for the exact per-step contract.

## Configuration

Pass a JSON config document with `--config`. Start from the shipped
`cmd/timesheet/timesheet.personal.example.json` (journal + local-only git): its
PLACEHOLDER values — the `vault` root, the git `author`, and `local_repos`/`repos`
— MUST be replaced with your own local paths before use. The document carries the
evidence config (timezone and sources), the workflow record directory, the
optional vault export settings, the Gradion MCP endpoint plus a
`keychain:<service>` credential reference, the optional editorial config, and the
project→classification map. Raw secrets are never placed in it.

`--offline` skips **ONLY** the Gradion remote; configured source fetches (calendar
ICS, git, journal) still run. The per-source keys are listed under guided setup
below — calendar `url_ref`, `user_email`, `timezone`; git `mode`, `local_repos`,
`repos`, `author`, `project_map`; journal `vault`, `daily_dir`; agent `path` — and
those are the whole of the source interface an agent needs. The `docs/` references
in this file are maintainer provenance only (see the boundary above).

### Guided setup (`timesheet config`)

When no config exists yet — or the user wants to change one — the skill offers the
**supported configuration flow** instead of hand-writing JSON:

```bash
$TS config init                                    # guided TUI (needs a terminal on stdin AND stdout)
$TS config init --interactive                      # the explicit alias of the SAME guided mode (identical gate)
$TS config init --plain --out "$CFG"               # line-based prompts (safe on a pipe; EOF aborts)
$TS config init --answers answers.json --out "$CFG"   # non-interactive: validate + write a config document
$TS config edit --config "$CFG"                    # load an existing config, prefill, edit in place
```

`--interactive` is only the explicit form of the default guided mode: it does
**not** change the terminal/CI gate (the TUI still needs a real terminal on stdin
AND stdout). A guided **edit** preserves values it does not prompt for (the
editorial tuning fields and existing classification entries), but rebuilds each
source's `config` from the collected keys only — author keys the flow does not
prompt for (e.g. a `fixture`) via `--answers`.

The agent/harness asks the **SAME questions in chat**, then delegates the write and
validation to `timesheet config`. Ask them in this order, applying the same
answers the flow would (blank keeps the shown default):

1. **Evidence id** — a non-secret label (default `default`).
2. **Timezone** — an IANA name (e.g. `UTC`, `America/Los_Angeles`).
3. **Enabled evidence sources** — any of calendar, git, journal, agent.
4. **Per-source config** — the documented keys only: calendar `url_ref`
   (`keychain:<service>`), `user_email`, `timezone`; git `mode`
   (`local`/`remote`/`both`), `local_repos`, `repos` (host-less `owner/repo`,
   required when `mode` includes remote), `author`, `project_map`; journal `vault`
   + `daily_dir` (relative to the vault); agent `path`.
5. **Storage directory** — workflow records (default `~/.timesheet/records`).
6. **Vault export root** (optional) and the **daily-note directory** relative to it.
7. **Gradion** (optional) — `endpoint` + `credential_ref` (`keychain:<service>`).
8. **Editorial** (optional) — `enabled`, `adapter` (`none`/`command`), `command`,
   `credential_ref` (`keychain:<service>`).
9. **Project → classification map** (optional).

Then **show the exact JSON preview** the flow would write, **ask for explicit
confirmation**, and — if the target file already exists — **ask for explicit
overwrite consent** (or pass `--force`). The skill then lets `timesheet config`
perform the write and validation. The skill **must not** write the target config
file (`$CFG`) itself — that is `timesheet config`'s job; materializing the
`--answers` INPUT document for the harness to hand to the command is expected and
allowed. The skill also **must not** auto-confirm an overwrite, and **must not**
put a raw secret in the answers (credential fields are `keychain:<service>`
references only). Validation is offline — authoring resolves no live service.
