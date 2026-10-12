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

### Available runtime agents (oh-my-opencode-slim)

Select an agent by task and capability, not by a fixed role pairing. The agents
actually available in this runtime (preset `openai-zen`) are:

| Agent | Lane | Use for |
| --- | --- | --- |
| `oracle` | Architecture, risk, debugging strategy, independent review | High-stakes design; independent review; simplification |
| `explorer` | Fast codebase recon | Locating flows, contracts, and symbols before planning |
| `librarian` | External docs and library research | Authoritative external API/library facts |
| `designer` | UI/UX design and terminal interaction | User-facing and terminal-interaction design where available |
| `fixer` | Bounded implementation and fixes | Implementing or repairing an authorized, fully specified change |
| `observer` | Observation/measurement lane | Present in the preset; dispatch only if the runtime exposes it |
| `ai-database` | Knowledge management | Vault project-note and daily-log updates |

Map a role to the agent whose capability fits: exploration to `explorer`,
external facts to `librarian`, bounded implementation/fixing to `fixer`,
independent review and design-risk to `oracle`, terminal/UX design to
`designer`, vault updates to `ai-database`. Do not require every agent type or
invent an unavailable one. Record the selected agent and rationale in the task
packet, and preserve independent review and ownership gates regardless of which
agent performs a lane.

## Next handoff

S1 (durable workflow store) `d471b46`. S2 (crash-safe submission) `d4d6c00`. S3 (evidence contracts) `28d7891`/`5ff9894`. S4 (proposal model) `5f373f4`/`2a06b5b`. S5 (MCP transport) `42fba53`/`b59661a`/`c943e97`. S6 (plans/approval/reconciliation) `3dfe814`/`40d266d`. S7 (editorial synthesis) `3d4bcc8`. S8 (entry points/export) `06ce95b`/`d65bdf0`. S9 (integrated acceptance) `ca96a20`. All nine slices were independently reviewed and re-reviewed; integrated acceptance was accepted. Release is a separate owner decision; live MCP/agent/Keychain verification remains outstanding. See [state](state.md) and [implementation plan](implementation-plan.md).

PERSONAL-WORKFLOW-1 (personal daily workflow): A (optional read-only journal evidence source) `195fb4e`; B (guided daily review through explicit local gates) `543966d`. Both independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). See [state](state.md) and [the personal workflow handoff](personal-workflow-handoff.md).

CONFIG-GUIDED-1 (guided configuration): `59991a8`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Adds a supported `timesheet config init|edit` flow plus equivalent skill questions. See [state](state.md) and [the implementation plan](implementation-plan.md).

INTERACTIVE-PERFORMANCE-1 (interactive performance): `2d67de3`. Independently reviewed, closed source-accepted, local only (not pushed). Measured provenance/startup/catalog latency, then added compiled-binary guidance and in-run catalog fetch reuse. See [state](state.md) and [the implementation plan](implementation-plan.md).

MIDNIGHT-CARRY-1 (midnight linked intervals): `aee253c`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Composed midnight+four-hour split with a durable next-day carry requiring explicit confirmation. See [state](state.md) and [the implementation plan](implementation-plan.md).
