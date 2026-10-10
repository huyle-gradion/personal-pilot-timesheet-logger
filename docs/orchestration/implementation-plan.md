# Staged redesign implementation plan

Status: proposed execution sequence, 2026-10-10. The product design is accepted; later implementation slices still need authorization. No deadlines or production-write permission are assigned here.

## Baseline and priority

Credential integration and temporary mutation gates are committed at `356c680`. Documentation baseline commits are `80bb233` and `faec6a4`. These hashes were observed in the current Git history, not inferred from worker reports.

S1 (durable store) is committed at `d471b46`; S2 (crash-safe lifecycle) at `d4d6c00`. S3 (evidence contracts) is committed at `28d7891` (layer 1) and `5ff9894` (layer 2) on `feat/s3-evidence-contracts`, independently reviewed and re-reviewed. Preserve unrelated untracked files.

Orchestrator first-hand baseline (2026-10-10, snapshot `faec6a4`): `go build ./...` and `go vet ./...` clean; `go test -count=1 ./...` passes for `internal/workflow` and `cmd/sync-timesheet`. One pre-existing, environment-dependent failure in `internal/git` (`TestResolveAuthToken_KeychainMissFallsThrough`) is on untouched code at `356c680` and is not an S1 regression.

Start each slice by inspecting existing ownership and reusable code. Avoid a wholesale rewrite. Keep production writes outside ordinary verification. For each row, run an implement/review/fix/accept cycle using the control policy.

## S0. Credential and temporary safety baseline

State: committed. Dependency: none.

Evidence: source at `356c680`; owner-reported read-only source checks and successful independent reviews. Limits: deprecated flag/env overrides remain; redaction is literal-only; a missing security binary is treated as NotFound; `-approve` is not revision-specific. Live create/edit behavior is unverified. Do not equate this baseline with production readiness.

## S1. Durable workflow store and outcome integration

State: committed `d471b46` (parent `faec6a4`), locally only, not pushed. Depends on S0. Independent @oracle review of snapshot `faec6a4` returned "changes required" (F1-F8); accepted fix set F1/F2/F3/F4/F6 applied and a focused independent re-review returned "accepted" (F1/F4/F6 fully closed; F3 closed with non-blocking residual N1; F2 re-scoped to S6). Carried: F5, F8 (rationale in `state.md`); F7 pre-existing S5/S6. Snapshot md5s re-verified and orchestrator build/vet/test/race green before commit.

Review `internal/workflow/` and the sync-timesheet changes. Independently inspect schema validation, revision updates, process-level locking, legacy preservation, outcome identity, and failure handling. Verify that successful and failed later operations remain separately observable.

S1 owns the persistent Remote Mapping and outcome contract, including stable remote IDs and last-observed-state representation. Later slices consume that contract rather than introduce a competing mapping store. Scope note (from S1 review F2): S1 provides the representation and populates mappings only from confirmed write responses; population from remote reads, skips, or adoption decisions is S6-owned reconciliation behavior. The current REST reconcile path populates nothing on skip and is slated for retirement in S5.

Explicit review questions:

- Does whole-record SaveDay lose intervening updates? Are actual callers safe?
- Does the lock protect the whole submission session, or only individual updates?
- Can a crash after a remote write but before local persistence cause replay?
- Is last-observed remote state taken from an actual response/read?
- Are skipped entries and missing local IDs preventing stable mapping creation?
- Are evidence/confirmation refresh claims package-only or integrated?
- Do fsync failures honestly report uncertain durability?

Exit: independent findings disposition, tests for actual guarantees, accepted bounded slice, and explicit remaining crash/approval gaps. Follow the accepted review scope when fixing. Do not hide blockers as “accepted limitations.”

## S2. Crash-safe submission lifecycle

State: committed `d4d6c00` (parent `9144849`, S1 `d471b46` ancestor), locally only, not pushed. Design accepted from an @oracle pass; owner confirmed three product decisions (2026-10-10): unresolved in-flight ops block and are resolved by S6 (no manual override); S2 locks submission only, deferring review/submit lock unification to S8; stale-lock recovery is manual and token-gated. Independent review and focused re-review both returned ACCEPTED (findings R1-R8 closed or carried as Info). Accepted design: one operation ledger with an `in_flight` intent state; deterministic `DeriveOperationKey`; explicit session lock; host+token lock with pid-liveness and manual `BreakStaleLock`; revision-checked `SaveDayCAS`. Carried Info: lock file not fsynced, `SaveDayCAS` production-dead.

Depends on S1. Authorize as a separate safety slice before any production writes.

Persist a stable operation identity and pre-write intent before mutation. Keep the operation unresolved across crashes until reconciliation establishes its outcome. Protect same-day submission across processes, not just individual record saves. Define safe manual recovery for stale locks with active-owner checks. Add revision-checked save semantics or restrict unsafe whole-record callers based on S1 evidence.

Tests: crash/fault injection before and after intent persistence, remote response, local outcome persistence, and process exit; multi-process contention; restart blocks unresolved replay. Local persistence never makes the remote write transactional.

Exit: a restart cannot blindly resend an in-flight operation; persistence failures halt; recovery preserves every confirmed outcome.

## S3. Evidence contracts and collector adapters

State: committed at layer 1 `28d7891` and layer 2 `5ff9894` on `feat/s3-evidence-contracts` (locally only, not pushed). Layers 1 and 2 each independently reviewed and re-reviewed (accepted). Owner confirmed four decisions (configured non-secret source id; retain cancelled/declined evidence with status and filter at proposal; agent contract/parser/fixture only; keep-all raw retention). S3 is complete at the code level; owner decision 3 is met. Carry: wire `VerifyContracts`/`PreflightContracts` into the S8 entry points; live-source verification (S8/S9); literal-redaction limit.

Depends on S1. Can proceed independently of S2 only in an isolated authorized worktree with non-overlapping files and a stable store contract.

Define a minimal new Evidence contract with stable source identity, original timestamps, provenance, invitation status, project hints, and unknown-duration representation. Adapt calendar, local git and PR sources without assuming legacy SimpleEntry is sufficient. Deduplicate PR/commit evidence and retain separate source references.

Add explicit registry configuration, contract checks, per-source results, and a fixture plugin. Failed sources remain visibly incomplete. Unknown executables are never auto-enabled; contract checks are not a security sandbox. Optional agent events record start/pause/resume/finish but never establish user working duration.

Tests: duplicate collection, cancelled/declined and unknown invitation status, PR/branch/repository identity, malformed output, timeout, and missing-source acknowledgment. Preserve source URL and token secrecy.

Exit: evidence survives store reload; refresh leaves confirmed fields intact; source completeness is explicit.

## S4. Proposal model and terminal review

State: committed at layer 1 `5f373f4` and layer 2 `2a06b5b` on `feat/s4-proposal-model` (locally only, not pushed). Layers 1 and 2 each independently reviewed and re-reviewed (accepted). Owner confirmed three decisions (cross-midnight carryover; manual intervals need explicit confirm; not-attended meetings excluded from work but retained as evidence). S4 is complete at the code level. Carry: S4L2-6/S4L2-7 and N1/N2/N3 (non-blocking); no production entry point invokes proposal/review yet; local 4-hour/grid/timezone policy unverified (S9).

Depends on S3.

Build Topic Candidates and separate Activity Groups using the recommended 45-minute adjacency heuristic. Suggest spans without confirming duration. Meetings interrupt inferred work. Support user-provided intervals, project resolution, attendance bulk confirmation, collision resolution, and empty days.

Preserve originals while offering optional grid adjustments. Split confirmed intervals at midnight and the configured four-hour maximum without changing total minutes. Keep both meeting and commit content in shared interval descriptions. Validate no approved overlap; maintain lunch/off-hours work.

The four-hour maximum is an owner-stated local policy, not verified Gradion behavior. The configured local timezone is likewise not proof of the server's governing timezone. Implement and test local rules provisionally; production use must wait for trusted remote semantics or an explicit owner decision at S9. Do not present those local tests as remote validation.

Tests: unknown duration/attendance/project blocks progression, repeated review preserves user edits, isolated commits have no fabricated duration, same topic across gaps remains separated, meeting/git counts once, midnight/splits/DST cases, and confirmed empty days create no remote work.

Exit: review state and confirmations persist end-to-end, not only through unit merge helpers.

## S5. Direct Gradion MCP transport

State: committed at layer 1 `42fba53`, layer 2 `b59661a`, layer 3 `c943e97` on `feat/s5-mcp-transport` (locally only, not pushed). All three layers independently reviewed and re-reviewed (accepted). Owner decisions met: hermetic implementation (no live reads/writes); contain the legacy REST write path now. S5 is complete at the code level. Carry: the S2 integrated restart gate is dormant until S6/S8 re-creates it against the approval-bound MCP path; live MCP semantics unverified; residual remote race (no conditional update / idempotency key).

Depends on S0 and a stable S1 mapping/outcome contract. Hermetic implementation first; live reads require named authority.

Implement the verified MCP tool contract behind the shared core: initialize, paginated listing, stable-ID selection, classifications, creates and edits, required audit reason, bounded parsing, and uncertain outcomes. Verify current schemas instead of relying on stale names. Separate declared tools from observed behavior. No delete path.

S5 returns observed remote IDs, states, and outcomes in the S1 contract. It does not decide which unmapped local and remote entries are the same work.

Avoid adding a parallel REST client; retire or contain the legacy submission path so it cannot bypass later approval gates. Revisit the owner if MCP lacks a necessary capability. Pagination must not falsely establish a complete remote snapshot while pages shift.

Tests: protocol/session behavior, pagination and duplicates, tool-level errors inside successful HTTP responses, stable identities, response limits, timeout, and exactly one attempt for uncertain writes. No production write probes.

Exit: mocked create/edit behavior and read-only contract evidence support the next reconciliation slice; live write semantics remain explicitly unverified.

## S6. Revision-bound plans, approval, and reconciliation

State: committed at layer 1 `3dfe814` and layer 2 `40d266d` on `feat/s6-plans-approval-reconciliation` (locally only, not pushed). Layers 1 and 2 each independently reviewed and re-reviewed (accepted). Owner confirmed three decisions (configured default + review override for Project→classification; explicit adoption required to resolve an uncertain create; fixed machine-readable edit audit reason). S6 is complete at the code level; its exit holds (no production mutation path bypasses exact approval, durable intent, validation, reconciliation, or outcome persistence). Carry: F1 (S8 must pass `ActiveApproval` and compare `ApprovalID`); NEW-1, F4, F5, `RebuildFailed` (Info); residual remote write race; no production entry point wires `Execute`.

Depends on S2, S4, and S5.

Build exact create/edit plans from confirmed local records, stable Remote Mappings, and observed remote state. Persist approval binding to the immutable plan content, record revision, and remote assumptions. Any included interval, text, classification, or remote change invalidates relevant approval.

S6 owns reconciliation and explicit adoption decisions that populate or update S1 mappings, using S5 observations. Storage owns representation; transport owns observations; reconciliation owns identity decisions.

Re-fetch immediately before affected writes. Map by stable IDs, not slot equality. Unmapped remote entries require explicit adoption or conflict resolution; never fabricate identities. Reconcile uncertain operations conservatively, leaving ambiguous matches unresolved. Handle partial success with a new plan for remaining operations rather than replaying the day. No deletion or silent replacement correction.

Tests: edited text/revisions invalidate approval, remote changes stop writes, uncertainty blocks creates, duplicate-looking entries remain unresolved, partial outcomes preserve IDs, correction constraints, auth failures, and concurrent sessions. Document the remaining server-side race and absence of idempotency keys.

Exit: no production mutation path bypasses exact approval, durable intent, validation, reconciliation, or outcome persistence. `-approve` alone is insufficient and must not remain a bypass.

## S7. Optional editorial synthesis and usage

State: committed at `3d4bcc8` on `feat/s7-editorial-synthesis` (locally only, not pushed). Independently reviewed and re-reviewed (accepted). Owner confirmed a command-adapter editorial agent (live wiring at S8) and a soft daily target of 1000 tokens including cache (owner override of the spec's 600). S7 is complete at the code level. Carry: S7-F4 (accumulated usage across reruns), S7-F5 (per-target invocation/batching) — S8 obligations; no production entry point wires the editor.

Depends on S4; changes to text must participate in S6 invalidation.

Implement text-only editorial input/output validation, summary reuse for unchanged evidence, and a soft 600-token daily editorial target including retries. Keep orchestration usage separate; unknown measurements are not zero. Budget exhaustion uses deterministic or manual text.

Tests: malicious/extra output fields cannot alter time/projects/evidence/approval, unsupported impact is rejected or reviewed, rerun caching, retry accounting, and no-agent operation.

Exit: AI is optional, constrained, and cannot authorize or confirm work.

## S8. Shared entry points and managed vault export

State: implementing on branch `feat/s8-entry-points-export` (base `f1d95c1`, S1-S7 ancestors). Design accepted from an @oracle pass; owner confirmed comment-marked export (legacy block untouched), deletion of the dead `cmd/sync-timesheet` + `internal/api`, and deferring the concrete editorial command to S9. Layer 1 (shared core `internal/run` + `internal/export`) implementing; layer 2 (CLI `cmd/timesheet`, skill rewrite, deletions) follows.

Depends on S4 and S6; editorial integration may follow S7 without blocking no-AI operation.

CLI and skill call the same deterministic core. Replace old workflow instructions that infer time or treat a bare flag as exact approval. Export a managed daily-note section after local review and update it for actual submission outcomes. Preserve unrelated journal text and valid YAML. ai-database remains optional, not the primary store.

Tests: no-AI CLI path, skill delegation parity, repeat export without duplication, journal preservation, reviewed/partial/unresolved states, export failure without submission replay, and daily-isolated date ranges.

Exit: both frontends share validation and approval semantics; exports are safe and truthful.

## S9. Integrated acceptance and release decision

Depends on S2 through S8, with editorial optional when explicitly disabled.

Run the full requirement matrix below using hermetic scenarios and fault injection, plus authorized live read-only checks where needed. Resolve timezone and server limits from trusted documentation/admin or read evidence before any pilot. Record residual races and the unresolved-result procedure.

Exit: independent integrated review, source acceptance, and owner release decision. A production pilot requires separate approval of real work in an exact daily plan; synthetic billable test entries are not permitted. Commits, passing tests, and a plan do not grant that approval.

## Requirement traceability

### Shared-workflow coverage

| Specification step | Owning slices | Completion evidence |
| --- | --- | --- |
| Preflight | S0 credential resolution; S3 configuration/source checks; S5 integration; S8 entry-point wiring | Timezone, configuration, references, and source/integration availability validated before collection |
| Collection | S3 evidence; S5 remote reads | Complete or explicitly incomplete source results and remote pagination |
| Proposal | S4 | Stable candidates and unconfirmed duration suggestions |
| Editorial synthesis | S7 | Text-only optional synthesis with usage accounting |
| Review | S4 | Persisted user decisions and unresolved-state validation |
| Local save and export | S1 persistence; S8 export | Reviewed state durable and managed note section preserved |
| Submission preview | S6 | Exact immutable daily plan and revision-bound approval |
| Recheck and submit | S2 lifecycle; S5 transport; S6 reconciliation | Durable intent, current remote assumptions, and approved operations |
| Outcome and metrics | S1/S2 outcome durability; S6 per-operation truth; S7 usage; S8 presentation | Actual statuses, editorial/orchestration separation, runtime/memory reporting, and unknown measurement flags |

S7 owns metric aggregation and measurement adapters, including available orchestration, runtime, and memory measurements; S8 owns presentation. No-AI runs still report available metrics without loading an editorial agent. Validate these in S9.

### Acceptance scenarios

Numbers refer to the redesign specification's 10 required scenarios.

| Scenario | Owning slices | Required evidence |
| --- | --- | --- |
| 1. Duplicate collection | S3, S4 | Repeated end-to-end collection yields stable candidates |
| 2. Preserve confirmed edits | S1, S3, S4 | Refresh/reload preserves user decisions |
| 3. Unknown duration/attendance blocks | S4, S6 | Every submission entry point rejects unresolved work |
| 4. Meeting plus commit counts once | S4 | Both descriptions, one non-overlapping interval |
| 5. Exact midnight/four-hour splits | S4 | Exact minute totals and linked fragments |
| 6. Remote changes invalidate approval | S6 | Fresh read mismatch causes zero affected writes |
| 7. No blind duplicates after failure | S1, S2, S5, S6 | Crash/restart/partial/uncertain-write scenarios |
| 8. Visible plugin failures | S3, S4, S6 | Incomplete source state requires acknowledgment |
| 9. Secret protection | S0 and every slice | Credential isolation and bounded diagnostics, documented redaction limits |
| 10. Shared no-AI workflow | S7, S8 | CLI/skill parity and deterministic/manual fallback |

Every claimed pass links exact test names and command artifacts in the slice record. A test existing is not a test passing; a unit pass is not integrated verification.
