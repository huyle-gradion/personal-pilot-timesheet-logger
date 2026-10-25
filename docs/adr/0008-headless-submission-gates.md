---
status: accepted
date: 2026-10-25
deciders:
  - Huy Le Minh
related:
  - 0005
---

# Headless submission gates

## Context

The local workflow (`collect`/`draft`/`run`/`record`/`status`) has been headless
since #155/#156, and the entire submission pipeline exists as pure
Session/store operations (`approvePlan`, `submitPlan`, `GradionWriter`,
reconciliation, unresolved-write semantics) — but every approval and submission
capability lived only in the interactive Pi adapter. The skill's `entries/`
files were a parallel surface nothing in the pipeline read, so a recorded day
could not reach a submitted day without a human driving the TUI end to end.

Campaign issue #160 asked for the missing headless surface (day finalization,
classification, plan/identity, approval/submission) without weakening any
approval, identity, or unresolved-write contract. The design question was how a
headless CLI can expose submission while the product's own approval ledger says
"a scripted request is not proof a human is physically present"
(`approval.go:17-21`).

## Decision

The headless CLI gains approval-gated submission
(#160 slices SUBMIT-1..4, merged as `stage`/`acknowledge`/`decide`/`confirm`/
`save`, `classifications`/`classify`, `plan`/`reconcile`/`resolve`,
`approve`/`submit`/`submit-status`/`resolve-write`), under three structural
choices:

- **Human gates are structural, not flags.** `approve`, `submit` and
  `resolve-write` refuse to run unless BOTH stdin and stdout are terminals
  (`isatty` on each, checked in the CLI wrapper only; the Session ops stay
  transport-agnostic) and each requires typing the exact `plan_id`,
  `approval_id` or `write_ref` at an interactive prompt. Agent shells are not
  TTYs, so they physically cannot execute these commands. This is a
  **deliberate-act barrier**, not proof of human presence: the residual is
  disclosed rather than hidden — a pty-allocating wrapper satisfies the
  terminal check and reaches the prompt, exactly as the ledger's own admission
  anticipates. The gate raises the cost and explicitness of bypassing; it does
  not certify a human.
- **One ledger.** `entries/` stays the human log AND becomes the staging
  source: `stage` converts recorded entries into staged intervals through the
  same session ops serve uses, and every later command re-derives the staged
  set from the entries ledger in its own process. Divergence after a durable
  save is caught by the plan's content hash — a plan rendered against one
  entries state fails closed when the entries no longer match.
- **Unknown outcomes stay unresolved.** No command ever auto-retries a
  submission. `uncertain`/`unidentified`/`in_flight` outcomes remain durable
  unresolved write references until a human resolves them with
  `resolve-write` (itself TTY-gated) against a fresh complete observation from
  `reconcile` or `plan`. `persistence_failed` is not consumption: nothing was
  dispatched and the approval stays active, so a re-submit consumes it.

Every data gate threads receipts verbatim (`collection_id`, `catalog_id`,
`observation_id`, `plan_id`, `approval_id`, `write_ref`); the protocol fails
closed on fuzzy references. Identity decisions (`resolve`) and unresolved-write
decisions (`resolve-write`) are explicit human choices, never auto-cleared;
any resolve changes the plan's identity, so the re-preview yields a NEW
`plan_id` and the stale one is never reused or re-approved. The workflow is
CLI-only under the store's exclusive flock: a headless submission cannot
coexist with a running `serve`.

The skill's own rule is updated accordingly: submission now exists headless but
is human-gated; the skill (the agent) still never submits itself — attempting
the gated commands is a contract violation, and the gate refuses anyway.
`submit-status` is read-only and not gated, so the agent may report recovery
state to the human.

Status: accepted (owner triage of campaign issue #160, 2026-10-25; all six
design decisions approved as filed).

## Alternatives rejected

- **A `--human` / `--yes-human` style flag** on `approve`/`submit`: rejected.
  It was explicitly refused by the ledger's own philosophy — a flag is an
  assertion a script can set, which is precisely the "scripted request" the
  ledger says is not proof a human is present. A boolean cannot be a gate.
- **Agent-executable approval** (let the skill type the exact ids itself):
  rejected. It violates the gate's whole point — the exact-id confirmation
  exists to make the act deliberate for a person at a terminal; an agent that
  can pass the prompt is an unaudited auto-approval path.
- **Config-level auto-approval** (an opt-out setting that disables the gates):
  rejected for the same reason as the flag — configuration is even further
  from the moment of the act than a command-line flag, and it would silently
  convert every future headless run into standing remote-write authority.

## Consequences

The skill can carry a day from evidence to a printed `plan_id` unattended, and
the final approve/submit always crosses a physical terminal with a typed exact
id. The pty residual means the gate is a deliberate-act barrier only; true
human-presence proof remains out of scope, and live Gradion submission
behavior stays owner-verified (per-slice acceptance, #160). `record` stays
lock-free; `stage`/`classify`/`confirm`/`save`/`plan`/`reconcile`/`resolve`
hold the exclusive store lock and cannot run beside `serve`. The serve
protocol, pi adapter and existing subcommand behavior are unchanged.
