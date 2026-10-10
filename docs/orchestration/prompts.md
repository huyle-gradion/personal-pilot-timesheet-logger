# Reusable prompts

Replace all bracketed fields and attach a completed task packet from `handoffs.md`. The authority and safety rules in `control-policy.md` apply to every role. A worker receiving unresolved placeholders stops for clarification.

The start, resume, and commit blocks are orchestrator operations, not implementer or reviewer dispatches. The orchestrator retains commit ownership. Only the explicitly labeled worker blocks are handed to code workers or researchers.

## Start an orchestrator

Actor: orchestrator. This sets session authority and execution mode.

```text
Act as orchestrator for this repo. Read AGENTS.md and docs/orchestration/README.md,
then follow its control policy, state ledger, handoff contracts, and staged plan.
Reconcile actual Git state with reports before selecting work.

Execution mode: owner-mediated. Give me one complete worker prompt per next gate,
then wait for my returned report. Do not silently dispatch or implement code.
Scope authorized now: [slice and implement/review/fix/planning permission].
Local commit permission: [yes for exact accepted slice / no].
Vault updates: [yes / no]. Publication and live access: off unless separately granted.

Start with the pending gate, not the next feature. Track exact snapshots, independent
review, findings, verification, commit ancestry, vault synchronization, and blockers.
Production timesheet writes remain off. A repository commit is not remote approval.
```

## Implement a slice

Actor: implementer worker.

```text
Implement [slice ID and objective] using the attached task packet.
Read AGENTS.md, docs/orchestration/control-policy.md, GLOSSARY.md, and the specified
redesign sections. Inspect existing code and preserve unrelated work.
Work only in [directory/branch] on [allowed files]. Reuse compatible code.

Deliver [required behavior] and the packet's acceptance tests. Stop for any product
trade-off, incompatible schema, unexpected file ownership, or safety requirement
outside your scope. No commits, vault writes, credentials, or live endpoints.

Run the packet's verification and report exact results using handoffs.md. Identify
known limits, actual versus requested behavior, and any tests not run. Stop with the
implementation uncommitted for independent review.
```

## Independent review

Actor: independent reviewer worker.

```text
Independently review [slice] at [exact snapshot]. Read-only: no edits, commits,
credential retrieval, Keychain changes, vault writes, or live endpoints.
Read AGENTS.md, the task packet, relevant redesign sections, and ADR 0005.
Inspect the complete tracked and untracked implementation diff, not just HEAD.

Assess each acceptance criterion and risk case in the packet. Check failure paths,
identity, persistence, concurrency, unauthorized effects, and truthful diagnostics.
Separate introduced defects from pre-existing issues and scope limitations.
A worker calling a limitation accepted does not establish that it is safe.

Run the specified hermetic checks. Report findings with ID, severity, file:line,
concrete scenario, evidence, and fix. State coverage and verification limits.
Give separate verdicts for committing this bounded slice and production readiness.
Use handoffs.md. If the snapshot changes during review, stop and report it.
```

## Fix reviewed findings

Actor: fixer worker.

```text
Fix only findings [IDs] from [report reference] for [slice/snapshot]. Follow the
attached task packet and preserve unrelated work. Read AGENTS.md and the relevant
requirement references. Confirm each finding against current code before editing.

For each accepted defect, add a regression test demonstrating the failure and fix
the underlying behavior. If a finding is incorrect, report concrete evidence rather
than changing code to appease it. Scope expansion or product changes require approval.

Run the required checks and report each finding's disposition and new snapshot.
No commit, vault edits, credential access, or live writes. Stop for focused review.
```

## Focused re-review

Actor: independent reviewer worker.

```text
Read-only review of fixes for [finding IDs], original report [reference], and new
snapshot [ID]. Verify each failure is closed and regression tests genuinely exercise
it. Inspect affected callers and new error paths for regressions. Run packet checks.
Report unresolved findings, new defects, and separate commit/production verdicts.
No edits, commits, credentials, live endpoints, or vault updates.
```

## Bounded fact-finding

Actor: researcher worker.

```text
Resolve [specific unanswered fact] for [slice]. Read [targeted references].
Authority: [local-only / exact read-only service and scope]. Report primary evidence,
date, assumptions, and unknowns. Distinguish declared capability from observed behavior.
No source edits, publication, Keychain changes, or remote writes. Never output secrets
or private entry content. Stop if resolution requires broader authority.
```

## Accept and commit

Actor: orchestrator only, after the owner grants commit authority. Do not dispatch this block to an implementer, fixer, or reviewer.

```text
Commit permission is granted for [slice and exact independently accepted snapshot].
Follow docs/orchestration/commit-and-vault.md. Verify the accepted report, ownership,
branch ancestry, current content, and final checks. Stop if source has changed.
Stage explicit accepted paths only; preserve unrelated work and inspect for private data.
Create a focused Conventional Commit and report hash, parent, included files, remaining
changes, and production blockers. No push, PR, merge, live access, or vault writes unless
the attached authority explicitly includes them.
```

## Resume after a worker report

Actor: orchestrator.

```text
Reconcile the attached worker report with [slice and snapshot] using handoffs.md.
Record claims with their verification level. If review is pending, generate the next
independent-review prompt. If findings exist, generate a bounded fix prompt. If accepted,
check commit authority before committing or issue a commit prompt. Synchronize the vault
only when authorized. Do not jump directly to another feature or production readiness.
```
