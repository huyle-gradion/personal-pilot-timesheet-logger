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
- **Uncertain remote writes are never retried.** An uncertain write stops the run,
  is recorded as unresolved, and blocks replay until an explicit `resolve`
  decision. Remote entries are editable but never deleted.
- **The export is marker-delimited and non-destructive.** It rewrites only the
  block between `<!-- timesheet:begin v1 -->` and `<!-- timesheet:end -->` and
  leaves every other byte (frontmatter, journals, legacy sections) untouched.
- **Secrets live in the macOS Keychain.** Configuration holds `keychain:<service>`
  references only and never a raw secret.
- **Classification is an explicit choice.** `log_time`/`edit_time` receive only a
  `classificationId`; the activity type is derived server-side and never sent. The
  project→classification map and the recent-selection preference may **suggest**
  only — an unset classification blocks the plan, and `--all` bulk selection
  requires the explicit flag.
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
ICS, git, journal) still run. Source keys and boundaries live in
[collection configuration](../../../docs/agents/collection-config.md) and
[journal source](../../../docs/agents/journal-source.md).
