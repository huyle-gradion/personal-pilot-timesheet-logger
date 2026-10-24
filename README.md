# Timesheet (rewrite line)

Application version: `0.1.0-dev.17` (unreleased development build, **not** a
release tag). Protocol schema `1`, state schema `3`.

This repository is one timesheet product: a single compiled Go binary owns all
accounting, validation and persistence, and one pinned Pi consumer renders the
local human workflow and exposes exactly one bounded model tool. The legacy
CLI subcommands, the old product skill and the vault sync/generator source were
retired in this cutover; they are not part of this product and must not be
reintroduced.

## Build

One path, from the repository root, with the Go toolchain the module pins
(`go 1.27.1`, tested with go1.27.3 on Linux):

```bash
go build -trimpath -o bin/timesheet ./cmd/timesheet
bin/timesheet version   # prints the application version and protocol schema, exits 0
```

`bin/timesheet` is the production-named executable. It has exactly two
commands: `version` and `serve --config <path> --env-file <path>`. Extra
arguments and every other command exit `2` without any remote call.

## Launch

One path, the trusted launcher:

```bash
node pi/launch.mjs \
  --binary "$PWD/bin/timesheet" \
  --config "$HOME/.timesheet/rewrite/config.json" \
  --env-file "$HOME/.timesheet/rewrite/.env.local" \
  --profile-dir "$HOME/.timesheet/rewrite/pi-profile"
```

The launcher spawns the pinned Pi CLI (`@earendil-works/pi-coding-agent`
`1.4.0`, `pi-tui` `1.4.0`, `typebox` `1.3.27`, Node `>=22.19.0`, installed with
`cd pi && npm ci`) with a fixed isolation set and loads only the adapter next to
it. There is no second server, no wrapper CLI, no alias and no test
provisioning flag.

`--config` and `--env-file` are **fixed launcher inputs**. They are never
protocol request fields and never model arguments. The launcher and adapter
handle paths only; the compiled child reads the files.

## Local `/timesheet` flow

Inside the pinned Pi session:

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
- Restart never resumes an approval, a submission or a draft. Only staging and
  durable saves survive; unsaved session drafts do not.
- A transport failure after dispatch is an unknown remote outcome, never proof
  of zero writes.

## Readiness

- Verified in this snapshot: Linux source cutover and hermetic runtime proof
  with the real pinned Pi TUI, synthetic loopback GitHub/ICS/MCP/model fixtures
  and an isolated generated HOME/profile/config/env/state; Darwin arm64/amd64
  cross-builds are compile-only proof.
- **Not** verified: macOS runtime (a mandatory, separate gate for full R6
  acceptance), live accounts/credentials, and authenticated model/subscription
  readiness. These are prerequisites, not passed criteria.
- No RC/stable/production claim follows from source review. `durability_uncertain`
  UI behavior remains source-reviewed plus accepted backend coverage, not a
  newly TUI-fault-proven case.

## Documentation

- `docs/agents/rewrite-interface.md` — authoritative `serve` protocol, config,
  environment precedence, error codes and the accepted stage history.
- `pi/README.md` — launcher/adapter isolation, consumer contract and limits.
- `cmd/timesheet/timesheet.example.json` — minimal valid configuration with
  synthetic placeholders.
- `docs/agents/credentials.md` — the active `.env.local` credential policy.
