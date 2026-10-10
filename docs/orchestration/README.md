# Orchestrator entry point

Coordinate workers, evaluate their evidence, commit accepted slices, and keep ai-database current. The orchestrator owns integration and records; workers own bounded code changes. This documentation creates no running agent service and grants no production-write permission.

## Start here

1. Read [control policy](control-policy.md) for authority and lifecycle gates.
2. Reconcile [current state](state.md) with Git and the latest worker reports.
3. Select a dependency-ready slice from [implementation plan](implementation-plan.md).
4. Fill a task packet from [handoff contracts](handoffs.md), then use [worker prompts](prompts.md).
5. After independent acceptance, follow [commit and vault protocol](commit-and-vault.md).

Read `AGENTS.md`, `GLOSSARY.md`, `docs/timesheet-redesign.md`, and repo ADR 0005 when first taking over or when their contents change. Load credentials, issue-tracker, and engineering references only when the task needs them. Product requirements remain in the redesign specification, not this operating guide.

## Default mode

Owner-mediated execution is the default. Produce a complete worker prompt and stop. The owner runs the worker and returns its report. If the owner explicitly enables direct dispatch, use available child-agent tools with the same packets and gates. Tool availability never implies permission.

Separate permissions for implementation, local commits, GitHub publication, live reads, and production writes. Ask for missing authority before acting. This documentation session permits documentation and vault updates only.

## Roles

| Role | Responsibility | Boundary |
| --- | --- | --- |
| Owner | Authorizes scope and exceptions; decides product trade-offs | Approves each remote timesheet plan separately |
| Orchestrator | Chooses slices, dispatches, reconciles findings, commits accepted work, updates records | Does not silently implement worker code or waive safety blockers |
| Implementer | Implements the assigned slice and tests | Stops for independent review; no commit or vault writes |
| Reviewer | Independently checks the exact snapshot and evidence | Read-only; does not retrieve credentials or alter files |
| Fixer | Repairs accepted findings with regression tests | Does not expand scope or commit |
| Researcher | Resolves named facts with bounded, authorized access | No changes or live mutations unless separately authorized |

An implementer's self-review is useful evidence, not independent acceptance. Reviewers and fixers can use fresh agents or separate sessions; record which happened.

## Next handoff

S1 (durable workflow store) is committed at `d471b46`. S2 (crash-safe submission lifecycle) is committed at `d4d6c00`. S3 (evidence contracts) is committed at `28d7891` (layer 1) and `5ff9894` (layer 2). S4 (proposal model and terminal review) is committed at `5f373f4` (layer 1) and `2a06b5b` (layer 2). All four slices were independently reviewed and re-reviewed. See [state](state.md) and [implementation plan](implementation-plan.md) for the next dependency-ready slice.
