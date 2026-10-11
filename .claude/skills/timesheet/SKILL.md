---
name: timesheet
description: Review-first daily timesheet workflow over the shared deterministic Go core. Collects evidence, proposes intervals, applies review decisions, previews and approves an exact submission plan, and exports a managed daily note. Trigger with /timesheet, log timesheet, or daily timesheet.
---

# Timesheet Skill (`timesheet`)

This skill is a **harness-agnostic command sequence over the `timesheet` Go
binary**. It orchestrates; it implements **no accounting and no submission logic
of its own**. Every deterministic decision lives in the shared core
(`internal/run`), so the CLI and the skill exercise one implementation.

## Triggers

Invoke this skill when the user issues any of the following triggers:
- `/timesheet`
- `log timesheet`
- `daily timesheet`
- `review timesheet`

## Contract (binding)

- **AI is optional and TEXT ONLY.** It may propose titles and descriptions. It can
  never change intervals, attendance, projects, evidence links, or approvals. With
  `--no-editorial` (or no editor configured) the workflow still completes using
  deterministic text. The editorial agent is invoked through a configured command
  adapter; the concrete live command is wired at S9.
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

## Execution workflow

Refer to [workflow.md](workflow.md) for the exact command sequence. Every step is
either deterministic Go code (`[Script]`) or the optional, bounded editorial agent
(`[AI]`) invoked through the core — the skill itself never synthesizes or accounts:

1. **Step 1: `[Script]` Preflight** (`timesheet preflight`)
2. **Step 2: `[Script]` Collect and propose** (`timesheet review`)
3. **Step 3: `[AI]` Editorial synthesis (optional)** (delegated by `timesheet review`)
4. **Step 4: `[Script]` Apply review decisions** (`timesheet review --decisions`)
5. **Step 5: `[Script]` Submission preview** (`timesheet preview`)
6. **Step 6: `[Script]` Approval** (`timesheet approve --plan`)
7. **Step 7: `[Script]` Submit and export** (`timesheet submit`)

Optionally, and **non-blocking**: `timesheet metrics import --transcript "$PI_SESSION" && timesheet metrics report --date "$DATE"` records tokens/day (3.6); a metrics failure only warns and never changes a workflow exit code or triggers submission. See [workflow.md](workflow.md).

Preflight validates source **configuration** (not live availability) and runs no
fixtures; a `mode:"local"` git source passes offline with no GitHub credential.
See [workflow.md](workflow.md) for the exact per-step contract.

## Configuration

Pass a JSON config document with `--config` (see
`cmd/timesheet/timesheet.example.json`). It carries the evidence config (timezone
and sources), the workflow record directory, the optional vault export settings,
the Gradion MCP endpoint plus a `keychain:<service>` credential reference, the
optional editorial config, and the project→classification map. Raw secrets are
never placed in it.
