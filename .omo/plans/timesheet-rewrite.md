# Timesheet rewrite execution plan

Governing specification: `docs/timesheet-rewrite-spec.md`, product design accepted on 2026-10-22.

Execution mode: owner-mediated. R1/R2 retain qualified acceptance; R3 dev.9 is accepted for reviewed Linux/hermetic migration/reconciliation/planning behavior after its second repair converged. F1-F9 and current C1 hygiene are closed within that evidence scope. The owner authorized GitHub issue/PR tracking and release of R4 after publishing the accepted baseline. R4 implementation is owner-mediated, not direct child dispatch; no application source repair by the directing session. Publication of accepted R1-R3 and tracking docs is authorized; merge, release, credential-read, live-read and production-write authority remain separate.

## Baseline

- Dedicated rewrite branch: `feat/timesheet-rewrite-v0.1.0`, created from `main` at base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. All rewrite implementation stays on this branch; do not commit or submit to `main`.
- Initially untracked owner files: `.env.local`, `transcript_full.jsonl`. The research note was created during this design conversation.
- Kickoff changes: credential ignore rules, research constraints, governing specification, this plan, first worker packet, and changelog.
- `.env.local` was not read. Do not include it or the private transcript in a source snapshot, commit, worker context, or model request.
- Go module currently declares Go 1.27.1. Do not change toolchain/dependencies merely to begin the rewrite.
- Only snapshot tags were observed locally. No SemVer tag or application release declaration was found in the inspected release surfaces. Verify publication history before actually publishing a version.

## Shared implementation contract

Build fresh workflow implementation rather than wrapping the old agent choreography. Reuse an existing dependency only when appropriate; do not create a second permanent production CLI. During development use `cmd/timesheet-rewrite` and `internal/rewrite`; the cutover replaces `cmd/timesheet` and removes the temporary entry point and superseded runtime implementation in the same accepted cutover. Existing user stores and credentials are never deleted.

The development entry point is temporary build isolation, not a compatibility alias or a shipped second product. No development milestone is a full-product completion claim.

Use private line-delimited JSON over stdin/stdout. Protocol schema starts at integer 1. Every response echoes the request ID and carries either a result or stable error code. One child remains alive through review/revision/approval; logs are separate from protocol output. Establish exact request grammar and document it with R1 rather than delegating accounting rules to Pi.

Prefer standard-library JSON/filesystem/process support. No generic plugin framework: implement exactly GitHub and calendar. No daemon. No new test requirement. Every accepted slice needs an actual runtime demonstration and exact command/output evidence.

## Dependency order and allowed surfaces

| Slice | Prerequisites | Owned surfaces | Runnable outcome |
| --- | --- | --- | --- |
| R1: local daily workflow | kickoff | `cmd/timesheet-rewrite/main.go`; `internal/rewrite/{config,model,store,session}.go`; `.env.example`; `docs/agents/rewrite-interface.md`; `CHANGELOG.md` | Real persistent compiled process supports manual intervals, review confirmation, save-local, revision, cancellation, status, safe credential/config loading, version and protocol negotiation. Durable output survives restart. No collectors or remote submission claimed. |
| R2: evidence | R1 accepted | `internal/rewrite/{github,calendar}.go`; config/model/store/session integration; optional behavior tests; `.env.example`; interface/changelog | Actual GitHub and calendar collectors produce provenance-preserving topics/meetings with accurate completeness, pagination, date-window behavior, and human-only failure acknowledgment. Ordinary verification uses local HTTP services, not real credentials. |
| R3: migration and remote plans | R1, R2 | `internal/rewrite/{migration,gradion,plan}.go`; model/store/session integration; interface/changelog | Binary migrates legacy records read-only, keeps identities/history/unresolved states, disables old approvals, reconciles remote observations, and renders exact creates/edits. Migration is repeat-safe and performs no remote write. |
| R4: approved submission | R3 | `internal/rewrite/{approval,submission}.go`; plan/store/session/Gradion integration; interface/changelog | Exact revision-bound human approval, durable pre-write intent, verified outcomes, and unresolved-write blocking exercised against a local service. No live production authority. |
| R5: restricted Pi integration | R1 interface stable; R2-R4 for integrated acceptance | explicit bundled Pi adapter and launcher under `pi/`; interface docs; changelog | Real Pi local forms drive one child through review and approval; supported chat model is optional. No shell/read/search or unrelated tools reach the model, no config/records enter its context, and direct UI needs no model calls. Exact adapter files are named in its packet after pinned upstream interface verification. |
| R6: cutover and integrated acceptance | R1-R5 independently accepted | `cmd/timesheet/`; temporary development entry point; superseded runtime packages and skill files identified by reference inventory; user guide/changelog/release metadata | One production entry point, no obsolete product paths/shims, migration and restricted Pi workflow demonstrated end to end. Retired ai-database integrations removed without touching owner data. |

One source writer at a time. Independent work may be prepared concurrently only with explicit direct-dispatch authority and isolated ownership. Each slice's packet must name the exact integration files, frozen snapshot, and next review gate before execution.

Accepted records: `docs/orchestration/timesheet-rewrite-r1-acceptance.md`, `docs/orchestration/timesheet-rewrite-r2-acceptance.md`, `docs/orchestration/timesheet-rewrite-r3-acceptance.md`. Next authorized slice: `docs/orchestration/timesheet-rewrite-r4-handoff.md`, exact native approval, durable write intent, verified submission and explicit recovery against hermetic providers. Owner-mediated execution starts from the published accepted baseline, not the historical kickoff HEAD. No child dispatch, worker commits/publication or live submission. Do not rerun historical R3 repair packets.

GitHub tracking: [rewrite epic #131](https://github.com/huyle-gradion/personal-pilot-timesheet-logger/issues/131); R1 #132, R2 #133 and R3 #134 source-accepted pending implementation PR merge; R4 #135 owner-released; R5 #136 and R6 #137 dependent and not authorized. Close implementation issues through merged PRs, not worker completion reports. The initial publication combines the already-integrated accepted R1-R3 snapshot; do not fabricate historical per-slice commits.

## Release milestones

- `0.1.0-dev.<n>`: unreleased implementation builds. Development versions are never evidence of a complete product.
- `0.1.0-alpha.<n>`: R1, R2, and local Pi review portion of R5 accepted; real local daily collection/review/save workflow, documented as not submission-ready.
- `0.1.0-beta.<n>`: R3 and R4 integrated with Pi; migration/reconciliation and approved submission demonstrated against hermetic services. Still not proof of live Gradion readiness.
- `0.1.0-rc.<n>`: R6 accepted and macOS/Pi/model-provider integration exercised under explicit live-read authority. Live submission verification remains separately gated.
- `0.1.0`: complete initial development release, with any remaining environment-specific readiness limits explicit. No release is created by this plan.
- `1.0.0`: accepted public interface stable, migration/cutover complete, macOS runtime demonstrated, and separately authorized live Gradion read/create/edit/reconciliation evidence accepted. This release requires an owner decision.

Within the 0.x line, public breaking changes increment MINOR and compatible fixes PATCH. After 1.0 follow ordinary MAJOR/MINOR/PATCH rules. Tags and release contents are immutable. Update the changelog and migration notes for each public-contract change; do not infer a version bump from code volume.

## Verification and review

Run checks once after each completed slice, not repeatedly mid-flight. Tests may be added for uncertain behavior but are not required. Runtime evidence is required: build and launch the actual binary, exchange multiple requests with the same PID, exercise the changed state transitions, and inspect local output without exposing private data. Pi work requires actual UI/tool-surface evidence.

The first packet includes concrete R1 smoke scenarios. Later packets must include migration repetition, remote-state conflicts, approval invalidation, partial/uncertain writes, calendar recurrence/day windows, GitHub partial coverage, cancellation, and model-context inspection appropriate to their slice. A hermetic remote simulator is verification infrastructure, not a production fallback.

Before implementation, use the existing dedicated `feat/timesheet-rewrite-v0.1.0` branch, preserve the uncommitted kickoff changes, and take a sanitized source rollback snapshot excluding credentials and private evidence. Do not create a separate R1 branch or copy user data into a worktree. A synthetic data directory is sufficient for ordinary execution.

Independent review must inspect the exact changed snapshot and reported runtime evidence. Source acceptance does not authorize commit or release. Failed checks and unavailable macOS/live environments remain explicit, not silently waived.
