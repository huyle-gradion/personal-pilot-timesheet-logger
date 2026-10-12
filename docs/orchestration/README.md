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

TUI-USABILITY-1 (guided terminal usability): `ec6d47e`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Presentation-only hierarchy, labels, provenance, unknown-duration indicators, keyboard help, and incomplete-source warnings. See [state](state.md) and [the implementation plan](implementation-plan.md).

BULK-REVIEW-1 (scoped bulk review): `cb813e7`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Staged bulk confirm/exclude/assign-project with a safety-gated preview; closes the carried nonempty-day mark-empty guard. See [state](state.md) and [the implementation plan](implementation-plan.md).

CLASSIFICATION-INTERACTION-1 (classification interaction): `4b5b242`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Unapplied recent/first-run suggestions, searchable authoritative choices, and a staged bulk-classification action. See [state](state.md) and [the implementation plan](implementation-plan.md).

LOCAL-GIT-TIMEZONE-TEST-1 (local-git day-window test determinism): `9cebb13`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Test-only; a confirmed production git date-window timezone defect is recorded as a tracked follow-up (not fixed). See [state](state.md) and [the implementation plan](implementation-plan.md).

GIT-DATE-WINDOW-1 (git day-window timezone anchoring): `4e50ae6`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). Fixes the confirmed production defect: the git collection window is anchored to the configured timezone with a calendrical half-open `[localMidnight, nextLocalMidnight)` interval. See [state](state.md) and [the implementation plan](implementation-plan.md).

INCIDENT-REPAIR-1 (source-only incident repair R1-R3): `a9c1f06` with the acceptance record `6c3da32`. Independently reviewed and re-reviewed, closed source-accepted, local only (not pushed). See [state](state.md).

PUBLICATION-1 (repository publication): the accepted development branch was published to `origin` at https://github.com/huyle-gradion/personal-pilot-timesheet-logger. Published work tip `6c3da32` on `feat/s9-integrated-acceptance` (parent `a9c1f06`), over the 107-commit linear range from `origin/main` `bc6023f`. This is a source publication only: the push was a normal fast-forward (destination ref created, no existing remote ref), with no merge, no pull request, no `main` push, no force-push, and no history rewrite. Production readiness is not established and production submission remains blocked. See [state](state.md).

TRACKER-1 (tracker workflow restoration): publication left the accepted series with no issue and no pull request, so the repository's documented loop had been unused since 2026-10-08. Nine outstanding items were filed as issues #26 to #34 (the three owed incident decisions, EVIDENCE-WINDOW-CORRECTION-1, the adjacent collector timezone items, the carried F6 projection, the pre-existing gofmt deviations, the untimed journal collector, and the read-only metrics import decision), integration issue #35 was opened, and pull request #36 (`feat/s9-integrated-acceptance` into `main`) was created against it. The one previously unpushed change in that pull request is the reviewed documentation commit `523e8d9`. Merge is not authorized and production readiness is not established. See [state](state.md).

MERGE-1 (owner-authorized approval and merge of PR #36): PR #36 (`feat/s9-integrated-acceptance` into `main`) was approved by the owner, merged by fast-forward, and `main` was pushed for the first time, making `main` `a9249a0` with all 110 commits preserved exactly as-is and no merge commit. The GitHub approving review was rejected because the authenticated account also authors the pull request (`Review Can not approve your own pull request`), so the approval is recorded in [state](state.md) rather than as a GitHub review. PR #36 is MERGED and integration issue #35 closed; the pre-merge gate battery passed on the frozen snapshot. An unavoidable side effect is recorded: PR #25's head `e8b6496` was the first commit of the merged range, so GitHub marked PR #25 MERGED and closed issue #11. The `main` push is authorized for this merge only, not in general, and the records commit is a direct commit to `main` rather than a pull request, which deviates from the TRACKER-1 loop and is flagged in the ledger. Production readiness is unchanged: production timesheet writes remain blocked, and live MCP, agent, and Keychain write verification is still outstanding. See [state](state.md).
