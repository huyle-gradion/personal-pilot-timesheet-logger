# Pilot Timesheet

Application version: **`0.2.0-dev.6`** (unreleased development stamp; the
published release line is `0.1.1`). Protocol schema `1`, state schema `3`.
This is a development build: no version bump, release tag or adapter/package
stamp change is part of the daily-workflow work.

This repository is one Pilot Timesheet product: a single compiled Go binary owns all
accounting, validation and persistence. Two callers drive it: the **regular Pi
skill** (`.pi/prompts/timesheet.md` → `.agents/skills/timesheet/SKILL.md`),
which needs no launcher and runs the `next` loop directly, and the optional
**pinned Pi launcher/TUI** (`pi/launch.mjs`), which renders the local operator
workflow and exposes one bounded model tool. Both call the same binary and the
same planner; neither is required by the other. The legacy CLI subcommands, the
old product skill and the vault sync/generator source remain retired and must
not be reintroduced.

## Build

From the repository root, with any Go toolchain matching `go.mod`
(`go 1.27.1`; tested here with go1.27.3 on Linux):

```bash
go build -trimpath -o bin/timesheet ./cmd/timesheet
bin/timesheet version   # prints the application version and protocol schema, exits 0
```

A mise-managed Go (`mise exec -- go build ...`) is environment troubleshooting
on hosts where `go` is not on `PATH`; it is not a project dependency.

`bin/timesheet` is the production-named executable. Its server commands are
`version` and `serve --config <path> --env-file <path>`, plus `setup` and the
headless surface described below. `next` is the planner the skill uses; the
other headless verbs (`run`, `record`, `stage`, `classifications`, `classify`,
`save`, `plan`, `reconcile`, `resolve`, `submit-status`) are also directly
invocable for recovery. `approve`, `submit` and `resolve-write` are TTY-gated
and interactive-only. A normal `version` run, a normal `serve` close/EOF and
`serve` help exit `0`. No top-level command, an unknown top-level command, or
extra `version` arguments exit `2`. `serve` argument validation (an unknown
flag, an extra positional, a missing required flag), startup failure and stream
failure exit `1`. Usage errors make no remote call.

## Daily log: regular Pi skill (no launcher)

Daily driver, in a Pi session: type `/timesheet log today work`. Pi expands
`.pi/prompts/timesheet.md` into the skill below, and the binary resolves
"today" in the configured timezone, so no Node launcher is involved. The
launcher/TUI remains an optional target, not a requirement.

The assignment deliverable is a runner-agnostic skill folder:
`.agents/skills/timesheet/SKILL.md`, picked up by Claude Code through the
`.claude/skills/timesheet` link and executable by any agent runtime with shell
access. The skill locates the binary through its own
`.agents/skills/timesheet/scripts/ts-env` helper, checks `next --help`, and
builds once before the loop if the found binary predates the planner.

Start the day with:

```bash
bin/timesheet next [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>] [--project <id>]
```

With no flags, config resolution is explicit `--config` → `./timesheet.json` in
the current directory → `~/.timesheet/config.json`, and `--env-file` defaults
to `~/.timesheet/.env.local` when it exists. Omitted `--date` means today in
the configured timezone; a supplied date always wins. A setup-generated
configuration stores an **absolute** `state_path`, so commands using that
config do not resolve their store from cwd. A legacy configuration with a
relative `state_path` keeps its existing cwd-relative meaning; do not claim
cwd independence for it.

`next` is read-only and stateless: no progress journal, no `--answer` flag and
no `--do` dispatcher. It prints one typed JSON state per invocation, derived
from the current ledger, stage map, answers and durable store:

- `action` — the exact `action.cmd` argv. Run it verbatim, never through a
  shell. It carries an absolute executable, config and day, plus the env-file
  when one was resolved.
- `judgment` — a model-owned answer file at `judgment.answer_file`, bound to
  the exact content it judged:
  - descriptions: `{"draft_digest":"<from the report>","notes":"…","descriptions":{"<block_id>":"<prose>"}}`
  - classifications: `{"ledger_digest":"<from the report>","selections":{"<interval_id>":{"catalog_id":"…","classification_id":"…"}}}`
  The classification judgment is self-contained in `judgment.blocks[]`
  (`interval_id`, `block_id`, `start`, `end`, `description`); it needs no
  draft or TMP artifact. Copy digest fields verbatim; a stale binding is
  re-judged, never handed to `record`/`classify`.
- `stopped` — an explicit blocker with its remedy (source gate, multiple
  projects, a stale plan, a changed finalized ledger, or a missing remote
  boundary). The skill reports the reason and stops; it never silently
  acknowledges a source, guesses an attendance verdict, binds an identity or
  reuses a stale plan. An explicitly user-authorized decision is performed
  once where safe.
- `human_gates` — the terminal state. Run the read-only `submit-status`
  command, print the exact `approve`/`submit` commands, and stop.

A failed action is not retried as-is. The skill repairs only an answer-file
validation error (unknown id, missing description, missing/stale digest) and
runs once more; any other repeat stops with the binary error plus one line.

Underlying verbs remain invocable for recovery and are what `next` derives:

```bash
bin/timesheet run             [--config <path>] [--env-file <path>] [--date <date>]
bin/timesheet record          [--config <path>] [--env-file <path>] [--date <date>] (--draft <draft.json> --descriptions <desc.json> | --file <reviewed.json>)
bin/timesheet stage           [--config <path>] [--env-file <path>] --date <date> [--project <id>]
bin/timesheet classifications [--config <path>] [--env-file <path>] --query <text>
bin/timesheet classify        [--config <path>] [--env-file <path>] --date <date> (--file <choices.json> | --configured-default) [--project <id>]
bin/timesheet save            [--config <path>] [--env-file <path>] --date <date> [--project <id>]
bin/timesheet plan            [--config <path>] [--env-file <path>] --date <date>
bin/timesheet reconcile       [--config <path>] [--env-file <path>] --date <date>
bin/timesheet resolve         [--config <path>] [--env-file <path>] --date <date> --file <decision.json>
bin/timesheet submit-status   [--config <path>] [--env-file <path>] --date <date>
```

`acknowledge`, `decide`, `confirm` and `stage --revise` remain available too;
`approve`, `submit` and `resolve-write` are TTY-gated and human-only. Entries
land in `<state dir>/entries/<date>.json`, merged idempotently per time block
with full source references; `entries/` stays the human ledger and staging
source. `save` is the existing confirm+save pair. `scripts/tokens.py`
optionally records model token usage into `benchmarks/tokens.csv`; metric
collection never blocks the day. `approve`/`submit` require a terminal on
stdin and stdout plus the exact typed `plan_id`/`approval_id` (not proof of
human presence — a pty wrapper defeats it), so the agent never attempts them;
a submit is never auto-retried, and unknown outcomes stay unresolved until a
human resolves them.

To configure an authoritative default, enable Gradion, explicitly query
`classifications --query <text>`, and review one returned option. Set
`gradion.default_classification` to its `catalog_id`, `classification_id` from
the option's `id`, `code`, `name` and `activity_type`. All five fields are required.
The example config uses synthetic placeholders; replace them with the reviewed
receipt or remove the optional object. This differs from suggestion-only
`projects[].default_classification`.

`next` then derives `classify --configured-default` for unselected unsaved
intervals, including meetings and split chunks. It reads only that cached
catalog and preserves existing explicit choices. To override an interval, apply
a reviewed `classify --file` choice before filling the rest with the default.
Missing or mismatched cache bindings stop without fetching; explicitly refresh
and review the binding, or use `--file`. Stale sidecars require reviewed `--file`
repair. Changing the default never rewrites existing choices or saved days.
Approval and submission retain their live semantic verification.

## Optional launcher/TUI target

The regular Pi skill above is the no-launcher path. The pinned launcher is the
optional TUI/operator target and calls the same binary and planner:

```bash
node pi/launch.mjs \
  --binary "$PWD/bin/timesheet" \
  --config "$HOME/.timesheet/config.json" \
  --env-file "$HOME/.timesheet/.env.local" \
  --profile-dir "$HOME/.timesheet/pi-profile"
```

The launcher supplies fixed paths; the regular skill resolves them from flags
or its home/cwd precedence. Keep existing configured state and credential paths
when updating. These example paths are not a migration instruction; do not
start with an empty replacement store just to change directory names.

The launcher spawns the pinned Pi CLI (`@earendil-works/pi-coding-agent`
`1.4.0`, `pi-tui` `1.4.0`, `typebox` `1.3.27`, Node `>=22.19.0`, installed with
`cd pi && npm ci`) with a fixed isolation set and loads only the adapter next to
it. There is no second server, no wrapper CLI, no alias and no test
provisioning flag.

`--config` and `--env-file` are **fixed launcher inputs**. They are never
protocol request fields and never model arguments. The launcher and adapter
handle paths only; the compiled child reads the files.

## Local `/timesheet` flow

Inside the optional pinned launcher/TUI session (the regular skill path is
above):

```text
/timesheet 2026-10-23
```

Every operator capability is a local form, never a model tool: collect
evidence (GitHub/calendar, optional synthetic or real sources), acknowledge an
incomplete collection bound to the exact collection attempt, record attendance,
author typed intervals (including the cross-midnight locked spill and
origin-day navigation), confirm versus durable save, revise or cancel, search
and select a remote classification, preview the exact plan, approve it, submit
it once, and inspect status/recovery. A durable save is the only thing that
makes work eligible for a plan; an unsaved draft is never planned.

## Migration and reconciliation before any write

Switching from legacy records is explicit and read-only until it is applied:

1. `migration_preview` over an explicit date list validates legacy records,
   conflicts, source hashes and the durable target; `migration_apply` re-reads
   and re-verifies exactly those inputs before one atomic activation, and a
   repeat is `no_change` with the legacy bytes unchanged. Migrated legacy
   approvals are inert audit data and can never authorize a write.
2. `reconcile_remote` makes one fresh read-only observation of the day and
   reports mapped, changed, missing and orphaned identities. A read never binds
   an identity: `adopt`, `accept_observed` and `foreign` are explicit human
   decisions against the current observation.
3. `preview_plan` renders the exact create/edit operations from saved confirmed
   work plus that fresh observation. `approve_plan` binds one approval to that
   exact rendered plan and never dispatches; `submit_plan` revalidates
   everything fresh, writes one durable pre-write intent per operation, then
   dispatches once and confirms only an identified acknowledgment plus a
   matching complete read-back.

Never write before the day is reconciled; never plan from an unsaved draft.

## Recovery and no replay

- A dispatched write that cannot be verified stays a durable **unresolved**
  reference. It is never retried, never replayed across a restart, and never
  silently blocks.
- Unresolved references block further writing until a human resolves each one
  with `resolve_write` against the *current complete* observation: `adopt`
  binds an observed remote identity, `not_applied` is a human attestation and
  is refused when a matching effect is visible.
- Restart never resumes an approval, a submission or a draft. Unsaved staged
  interval edits and confirmed-but-unsaved interval changes are session-only;
  only successfully committed state and durable ledgers reload. Startup never
  submits or replays anything on its own.
- A transport failure after dispatch is an unknown remote outcome, never proof
  of zero writes.

## Readiness

- Verified: Linux hermetic workflows and actual macOS native migration,
  local Pi logging, confirm/save/cancel, saved-versus-staged restart, edit/cancel,
  reload, normal shutdown and lock release. The Darwin runner result was supplied
  by the owner; its remote capsule was not independently downloaded here.
- For the `next`/daily-workflow integration, the parent baseline passed all Go
  tests on Linux; macOS was **not run** for this change.
- **Known dependency risk, owner-accepted for this release:** pinned Pi `1.4.0`
  embeds Undici `8.10.2`, affected by a high-severity fetch process-crash advisory
  and two moderate EventSource/integrity advisories. No patched Pi bundle has
  been selected. [R6 / issue #137](https://github.com/huyle-gradion/pilot-timesheet/issues/137)
  remains open for remediation and further improvements. This release is not
  claimed to be vulnerability-free.
- **Not verified:** live accounts/credentials, authenticated model/subscription
  readiness or real Gradion submission. Local logging does not require a model
  request; remote submission still requires an exact human-approved plan.
- `durability_uncertain` UI behavior remains source-reviewed plus accepted
  backend coverage, not a newly TUI-fault-proven case.

## Documentation

- `docs/agents/timesheet-interface.md` — authoritative `serve` protocol, config,
  environment precedence, error codes and the accepted stage history.
- `docs/architecture/timesheet-daily-workflow.md` — the stateless `next`
  planner, digest-bound judgments, responsibility map and accepted deviations.
- `docs/setup-linux.md` — first-run setup and configuration discovery on Linux.
- `pi/README.md` — launcher/adapter isolation, consumer contract and limits.
- `cmd/timesheet/timesheet.example.json` — minimal valid configuration with
  synthetic placeholders.
- `docs/agents/credentials.md` — the active `.env.local` credential policy.

## License

Pilot Timesheet is licensed under the MIT License — see
[LICENSE](LICENSE). Contributions are licensed under the same license.
