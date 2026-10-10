# Orchestration state

Updated: 2026-10-10. This is a resumable status ledger, not a grant of authority. Update it at lifecycle transitions without duplicating the product specification.

## Authority now

Owner authorization for the current orchestrator session (2026-10-10):

- Authorized: implement and fix the agreed S1 through S9 plan through bounded worker tasks; dispatch workers directly (direct-dispatch mode); create isolated branches/worktrees while preserving existing work; commit independently accepted slices locally; update repository progress documentation and ai-database; run hermetic checks and tests.
- Not authorized: push, publish GitHub changes, merge, or force-push; retrieve live credentials or access live services without separate permission; modify Keychain items; create, edit, or delete production timesheet entries; change agreed product requirements silently.
- Production timesheet writes remain prohibited during this development workflow.
- Vault scope: this session may update the assignment-1 project note and today's daily note per `commit-and-vault.md`; no other vault notes.
- Prior documentation-only session (recorded above): vault scope covered the operations index, project progress, and daily summary already written.

## Observed Git baseline

Observed locally on 2026-10-10 (orchestrator session):

| Item | Value |
| --- | --- |
| Branch | `feat/keychain-submission-safety` |
| HEAD | `faec6a4` |
| Credential/safety slice | `356c680`, committed |
| Redesign specification/glossary/ADR | `80bb233`, committed |
| Agent pointers/credential docs | `faec6a4`, committed |
| S1 durable store/outcome | `d471b46`, committed (parent `faec6a4`), independently re-reviewed accepted |
| S2 crash-safe lifecycle | `d4d6c00`, committed on `feat/s2-crash-safe-lifecycle` (parent `9144849`), independently re-reviewed accepted |
| S3 evidence contracts | layer 1 `28d7891`, layer 2 `5ff9894`, committed on `feat/s3-evidence-contracts`, independently reviewed/re-reviewed accepted |
| S4 proposal model | layer 1 `5f373f4`, layer 2 `2a06b5b`, committed on `feat/s4-proposal-model`, independently reviewed/re-reviewed accepted |
| S5 MCP transport | layers 1 `42fba53`, 2 `b59661a`, 3 `c943e97`, committed on `feat/s5-mcp-transport`, independently reviewed/re-reviewed accepted |
| S6 plans/approval/reconciliation | layers 1 `3dfe814`, 2 `40d266d`, committed on `feat/s6-plans-approval-reconciliation`, independently reviewed/re-reviewed accepted |
| Orchestration docs | `d3b9378`, `2507498`, `9144849`, `406fce3`, `7433729`, `67b6ab5`, `6535516`, `657e8bf`, committed |
| S1 tracked modifications | `cmd/sync-timesheet/main.go`, `cmd/sync-timesheet/main_test.go` |
| S1 pending new package | `internal/workflow/` (`models.go`, `store.go`, `merge.go`, `file_store.go`, `file_store_test.go`, `merge_test.go`) |
| Unrecorded tracked modification | `AGENTS.md` adds the Orchestration section; documentation only, not S1 code |
| Snapshot artifact | `/private/var/folders/r4/j1g3f1_57sq15v3sfz922wpw0000gq/T/opencode/s1-snapshot-faec6a4/` (`HEAD.txt`, `status.txt`, `tracked.diff`, `manifest.txt` with md5s) |

Unrelated untracked work observed and preserved: `.github/`, `.tickets/`, `benchmarks/`, `references/`, `scripts/`, repo ADRs 0001 through 0004, and `docs/orchestration/`. Ownership must be established before staging them. Orchestration files are new documentation, distinct from S1 code.

## Active slice

| Field | Status |
| --- | --- |
| Slice | S1 durable store/outcome integration |
| Lifecycle | committed (local), independently accepted |
| Commit | `d471b46`, parent `faec6a4`, branch `feat/keychain-submission-safety`; 8 files, 2131 insertions |
| Current writer | none; fixer session `ses_edc11522cffeuDwVD6A1flPhpk` finished and its result is now committed |
| Independent review | Initial @oracle review verdict "changes required" (F1-F8); focused re-review verdict "accepted" with non-blocking residuals N1/N2 |
| Accepted snapshot | branch `feat/keychain-submission-safety`, HEAD `faec6a4`, six changed files; manifest `/private/var/folders/r4/j1g3f1_57sq15v3sfz922wpw0000gq/T/opencode/s1-snapshot-fix1-manifest.txt`; md5s re-verified before commit |
| Orchestrator-baseline checks | first-hand `go build`, `go vet`, `go test -count=1`, `go test -race` all green on the committed snapshot |
| Pre-existing unrelated failure | `internal/git` `TestResolveAuthToken_KeychainMissFallsThrough` fails on this host; `internal/git` is clean/untouched at `356c680` |
| Worker claims | Build/vet/tests/race green; regression tests demonstrated to fail pre-fix (worker-reported; independently re-verified) |
| Next gate | select the next dependency-ready slice; S2 crash-safe submission lifecycle or S3 evidence contracts |
| Commit authority | granted this session for independently accepted slices (local commit only) |

Carried follow-ups (not blocking this commit): N1 unresolved-branch message says "uncertain" though it also covers `unidentified` (`main.go:433-434`); N2 `operationOutcome.Action` comment omits `unidentified` (`main.go:581`); F2 skip-path mapping population → S6; F5 derived-key collision; F7 slot-equality identity (S5/S6); F8 `ListDays` silent 365 truncation.

### S1 review findings and orchestrator disposition

Independent @oracle review (session `ses_edc16670fffe3xSQjc6J3sSybD`, read-only, snapshot md5s verified before/after). Orchestrator assessed each finding independently of the reviewer's severity label.

| ID | Reviewer severity | Orchestrator disposition |
| --- | --- | --- |
| F1 directory fsync error swallowed (`file_store.go:415-418`) | Blocking | Accepted. Fix now: propagate dir open/sync errors. Root of false durability. |
| F2 skip discards known RemoteID + LocalBlockID, no mapping (`main.go:397-400`) | Major | Accepted as claim correction. No behavior change: S1 provides the mapping representation; read/skip-derived population is S6-owned (`implementation-plan.md` S6). Align comments. |
| F3 "recovery required" / "crash never loses" overstated (`main.go:284-286,306-316`) | Major | Accepted. Fix now: correct comments/messages; durable pre-write intent is S2. |
| F4 whole-record `SaveDay` has no revision CAS (`file_store.go:82-116`) | Medium | Accepted. Fix now: drop `SaveDay` from exported `Store` interface (no production caller) and document the hazard loudly; S2 owns caller restriction/CAS. |
| F6 confirmed write with empty remote ID recorded as plain confirmed (`main.go:483-527`) | Medium | Accepted. Fix now: distinct `unidentified` outcome, no fabrication, CLI treats as unresolved and stops. |
| F5 derived OperationKey collision for empty-LocalID same slot+action (`merge.go:80-86`) | Minor | Carried with rationale: not producible by the current non-overlapping plan. Recorded, not fixed. |
| F8 `ListDays` silent 365 truncation (`file_store.go:266-268`) | Minor | Carried with rationale: bounded, no current caller. Recorded, not fixed. |
| F7 slot-equality identity inside `internal/api` | Note (pre-existing) | Not S1. S5/S6-owned reconciliation identity gap; no S1 action. |

Findings F1, F2, F3, F4, F6 are the accepted set handed to `S1-FIX-1`. F5, F7, F8 are recorded follow-ups. This disposition is the orchestrator's independent assessment, not a copy of reviewer labels.

## S2 active slice: crash-safe submission lifecycle

| Field | Status |
| --- | --- |
| Slice | S2 crash-safe submission lifecycle |
| Lifecycle | committed (local), independently accepted |
| Commit | `d4d6c00`, parent `9144849`, branch `feat/s2-crash-safe-lifecycle`; 12 files, 2604 insertions |
| Branch / base | `feat/s2-crash-safe-lifecycle` (S1 `d471b46` ancestor) |
| Design | @oracle design pass accepted by the orchestrator; recorded below |
| Current writer | none; S2-FIX-1 result committed |
| Independent review | initial @oracle review ACCEPTED (R1-R8 Low/Info); focused re-review ACCEPTED (all R1-R5/R8 closed, R6/R7 Info carried, no new defects) |
| Accepted snapshot | 12 files; manifest `/private/var/folders/r4/j1g3f1_57sq15v3sfz922wpw0000gq/T/opencode/s2-snapshot-fix1-manifest.txt`; md5s re-verified before commit; gofmt/build/vet/test/race green |
| Next gate | select the next dependency-ready slice (S3 evidence contracts, or S5 MCP transport) |

Carried S2 follow-ups (non-blocking): S2-R6 lock file not fsynced (harmless, O_EXCL serializes live processes); S2-R7 `SaveDayCAS` production-dead (intended S6 primitive); test doubles retain unused `UpdateDay` methods; multi-op crash where an earlier op is `uncertain` is untested (the loop breaks before a later intent).

## S3 active slice: evidence contracts and collector adapters

| Field | Status |
| --- | --- |
| Slice | S3 evidence contracts and collector adapters |
| Lifecycle | committed (layers 1 + 2), independently accepted |
| Branch / base | `feat/s3-evidence-contracts` (S2 `d4d6c00` and S1 `d471b46` ancestors) |
| Commits | layer 1 `28d7891` (13 files); layer 2 `5ff9894` (12 files) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_edbe7dae8ffe7t2q6wrEbgh6ct`) |
| Independent review | layer 1 + layer 2 each reviewed and re-reviewed; all accepted (S3L1-1..9, S3L2-1..8 closed or carried Info) |
| Next gate | select the next dependency-ready slice (S4 proposal model, or S5 MCP transport) |

S3 is complete at the code level: evidence contract + identity + compile-time registry + config + fixture plugin + additive persistence (layer 1); calendar/git/agent adapters + agent JSONL parser + library preflight helper (layer 2). Owner decision 3 (contract + JSONL parser + fixture) is met.

Carried S3 follow-ups (non-blocking): S3L1-7/S3L2-7 literal-redaction limit (resolved calendar `source` outside the backstop); S3L2-5 `PreflightContracts` production-dead (wire at S8 entry points); S3L2-8 commit-provenance branch nuance; N2 unexercised mode markers. No production entry point invokes collection/preflight yet, and live sources are unverified (S8/S9).

## S4 active slice: proposal model and terminal review

| Field | Status |
| --- | --- |
| Slice | S4 proposal model and terminal review |
| Lifecycle | committed (layers 1 + 2), independently accepted |
| Branch / base | `feat/s4-proposal-model` (S1 `d471b46`, S2 `d4d6c00`, S3 `28d7891`+`5ff9894` ancestors) |
| Commits | layer 1 `5f373f4` (14 files); layer 2 `2a06b5b` (11 files) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_edb9eaf21ffeQ2PeGagGke0fFb`) |
| Independent review | layers 1 and 2 each reviewed and re-reviewed; accepted (A1/P2/P3/P5, S4L2-1..5/8/9 closed; rest carried) |
| Next gate | select the next dependency-ready slice (S5 MCP transport) |

S4 is complete at the code level: pure proposal model + additive `SourceRef.At` per-commit timing (layer 1); additive `workflow` review persistence + `internal/review` actions and readiness gate (layer 2). Owner decisions met (cross-midnight carryover, manual explicit confirm, not-attended excluded-but-retained).

Carried S4 follow-ups (non-blocking): S4L2-6 (a joined meeting is also presented standalone; double-count prevented by the overlap gate — S6/S8 review UX); S4L2-7 (joint block `Duration` vs union span — cosmetic); N1 (`MergeConfirmed` does not clear `Review.Empty`, so a refresh can leave `Empty=true` beside confirmed work — not a readiness bypass; clear it or document; S8 export/hardening); N2 (joint interval keeps meeting text after exclusion — cosmetic); N3 (omitting `ManualInterval.UUID` generates a random ID per call — S8 must persist a stable UUID). No production entry point invokes proposal/review yet; local 4-hour/grid/timezone policy remains unverified (S9).

## S5 active slice: direct Gradion MCP transport

| Field | Status |
| --- | --- |
| Slice | S5 direct Gradion MCP transport |
| Lifecycle | committed (layers 1 + 2 + 3), independently accepted |
| Branch / base | `feat/s5-mcp-transport` (S1 `d471b46`, S2 `d4d6c00`, S3 `28d7891`+`5ff9894`, S4 `5f373f4`+`2a06b5b` ancestors) |
| Commits | layer 1 `42fba53` (6 files); layer 2 `b59661a` (8 files); layer 3 `c943e97` (7 files, −4101 net) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_edb501788ffeQD2aF5HG3ypYyu`) |
| Independent review | layers 1, 2, and 3 each reviewed and re-reviewed; all accepted (S5L1-1..9, F1..F5/T1..T4, F1..F3 closed; rest carried) |
| Next gate | select the next dependency-ready slice (S6 revision-bound plans/approval/reconciliation) |

S5 is complete at the code level: generic MCP transport (layer 1), Gradion domain client + additive `RemoteState` fields (layer 2), and legacy REST submission containment (layer 3). Owner decisions met (hermetic; no live reads/writes; contain the REST write path now).

Carried S5 follow-ups (non-blocking): N1 `tools.go` doc-precision nit; S5L1-9 network errors reduced to a static string; two dead constants in the deprecated `internal/api` (`MaxLocalBlocksCount`, `AssertAPISyncWritersNil`); the duplicated uncertain-write helpers marked deprecated. **F4 (S6/S8 carry):** the S2 integrated restart gate is now dormant — `workflow.UnresolvedOperations` and the session/intent primitives remain unit-tested, but no production consumer enforces the restart gate because the CLI write loop was retired; S6/S8 must re-create the restart gate, uncertain/unidentified handling, and multi-op crash tests against the MCP/approval-bound path. Residual remote race (no conditional update / idempotency key) documented, not closed. No production entry point wires `internal/mcp`/`internal/gradion`; live MCP semantics unverified.

## S6 active slice: revision-bound plans, approval, and reconciliation

| Field | Status |
| --- | --- |
| Slice | S6 revision-bound plans, approval, and reconciliation |
| Lifecycle | committed (layers 1 + 2), independently accepted |
| Branch / base | `feat/s6-plans-approval-reconciliation` (S1-S5 ancestors verified) |
| Commits | layer 1 `3dfe814` (11 files); layer 2 `40d266d` (8 files) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_edadd7e1dffeoojCEagYiCl8LA`) |
| Independent review | layers 1 and 2 each reviewed and re-reviewed; all accepted (F1-F6, F1-F7 closed; rest carried) |
| Next gate | select the next dependency-ready slice (S7 editorial synthesis) |

S6 is complete at the code level: additive `SubmissionPlan`/`Approval` representation + pure plan/reconcile/approval (layer 1); execution + the re-created restart gate (layer 2). S6's exit holds: no production mutation path bypasses exact approval, durable intent, validation, reconciliation, or outcome persistence, and `-approve` alone is insufficient.

Carried S6 follow-ups (non-blocking): **F1 (S8 obligation)** — the S8 entry point must pass `workflow.ActiveApproval(rec,date)` and compare `ApprovalID` before `Execute` (a caller-supplied stale approval can re-validate only if the plans are operationally identical); F4 classification-only edit inference is conservative (unresolved); F5 advisory rebuild re-observes after releasing the lock (TOCTOU, no hazard); `RebuildFailed` is defensive/unreachable; NEW-1 (empty observed `Date` can force a spurious edit — S9 live verification or a defensive fix); residual remote write race (no conditional update / idempotency key) documented, not closed. No production entry point wires `Execute`; live MCP semantics unverified.

S6 layer-2 dispositions (orchestrator, independent of reviewer labels):
- F2 advisory rebuild failure returned as the run error (Low): accepted, fix (surface distinctly, not top-level error).
- F3 rebuild block reasons dropped (Info): accepted, fix.
- F6 no end-to-end restart-cycle test (Info): accepted, add.
- F7 initial gate reasons unsorted (Info): accepted, fix.
- F1 caller-supplied approval trust (Low): carried to S8 (pass `workflow.ActiveApproval(rec,date)` and compare `ApprovalID`); bounded because a stale approval can only re-validate if the plans are operationally identical.
- F4 classification-only edit inference (Info): carried (conservative).
- F5 advisory rebuild TOCTOU (Info): carried (advisory only, no hazard).
The layer-2 safety addition (Execute gates on RestartGate at the start and on the rebuild) was independently confirmed correct and load-bearing (an uncertain outcome would otherwise yield a replayable plan / duplicate create).

S6 layer-2 scope: `RemoteObserver`/`RemoteWriter` seams; `Execute` (single Session, re-fetch under lock, ValidateApproval, per-op durable intent → dispatch once → outcome + mapping, partial success, new plan for remaining); re-created `RestartGate`; `ResolveUnresolved` (explicit adopt/not-applied decisions only — never auto-resolve a create from absence). Carry NEW-1 (empty observed `Date` can force a spurious edit — defensive fix or live verification at S9).

S6 layer-1 dispositions (orchestrator, independent of reviewer labels):
- F1 `BuildPlan` adoptions arg does not suppress a create → blind duplicate (Major, gating): accepted, fix now (treat adopted pairs as effective mappings).
- F2 `editReason` embeds `Record.Revision` → hash not revision-independent (Medium): accepted, fix now.
- F4 `RemoteContentEqual` ignores `Date` (Low): accepted, fix/document.
- F5 `PlanHash` uses unstable sort (Low): accepted, fix.
- T2/T3/T4/T6/T7 test gaps: accepted, add tests.
- F3 resolver output not in LocalFingerprint (Low): carried with rationale (the approval binds the exact approved op content; a resolver change produces a different plan on the next build).
- F6 `PlanOperation.Task`/`Billable` unpopulated (Info): carried/documented (layer 2 sources remote task/billable from `ExpectedRemote` for edits).

Owner-confirmed S6 decisions (2026-10-10): (1) Project→classification uses a configured default resolver plus a review override (additive optional `ConfirmedInterval.ClassificationID`), blocked when neither resolves; (2) an uncertain create is resolved only by explicit adoption (a complete listing showing the slot absent is not proof of failure); (3) the edit audit reason is a fixed, honest machine-readable string.

Engineering decisions (orchestrator): new `internal/submit` package owns identity/plan/approval/execution; `internal/workflow` owns representation; `internal/gradion` owns observation; additive `Record.Plans`/`Approvals` (omitempty, SchemaVersion 1); `LocalFingerprint` content hash replaces raw `Record.Revision` as the approval key (revision bumps on every write); `RemoteObserver`/`RemoteWriter` interfaces for hermetic injection; slot overlap only blocks, never matches; plan immutability via content hash; partial success builds a new plan for remaining ops (no replay); no delete path. Non-goals: S7 editorial, S8 entry points, S9 acceptance.

S6 owns exact create/edit plan construction, immutable plan content hashing, approval bound to plan revision + remote assumptions with invalidation, reconciliation identity by stable IDs with explicit adoption, conservative handling of uncertain operations, and partial-success handling with per-operation outcomes and stable IDs (no blind replay, no deletion, no silent replacement). It also re-creates the dormant restart gate (S5 F4 carry). Depends on S2, S4, S5. Non-goals: S7 editorial, S8 entry points, S9 acceptance.

S5 layer-3 dispositions (orchestrator, independent of reviewer labels):
- F1 `tests/e2e_test.go` no longer compiles (references removed symbols + retired flags) — accepted, fix now (retire or rewrite to the contained surface; S5 is not complete until the e2e tag builds clean).
- F2 dead `BuildSummary`/reconcile types + inaccurate `internal/api` doc: accepted, clean up.
- F3 orphaned test comment: accepted, remove.
- F4 (carry, S6/S8): the S2 *integrated* restart gate is now dormant — `workflow.UnresolvedOperations` and the session/intent primitives remain unit-tested, but no production consumer enforces the restart gate because the CLI write loop was retired. S6/S8 must re-create the restart gate, uncertain/unidentified handling, and multi-op crash tests against the MCP/approval-bound path. Recorded as an explicit carry, not a preserved guarantee.

S5 layer-3 scope (owner-approved containment): remove the REST mutation surface (CreateEntry/PatchEntry/DeleteEntry, ActionDelete, slot-equality Reconcile) and retire the cmd/sync-timesheet write path and `-approve` so no production write path can bypass later approval gates; update tests and document the coverage change. Carry N1 (tools.go doc-precision nit).

S5 layer-2 dispositions (orchestrator, independent of reviewer labels):
- F1 offset-shift can mislabel `Complete` → false "not found": accepted, fix now (boundary stability re-check + soften the claim + regression test).
- F2 dead `validateLimit`/vacuous limit claim: accepted, fix.
- F3 capability verification not enforced: accepted, fix (verified flag + fail closed).
- F4 `BuildOutcome` fabricates confirmed from empty id: accepted, fix (downgrade to unidentified).
- F5 `Initialize` nil-session code: accepted, fix.
- T1/T2/T3/T4 test gaps (exactly-once on retryable write path, read retry budget, arg-bound/limit, cross-page dedup, more classifications): accepted, add tests.

S5 layer-2 scope: new `internal/gradion` (MCP client, tool constants, capability verification, offset pagination with the Complete rule, exactly-one-attempt writes, uncertainty classification incl. S5L1-5 ErrSchema-on-write → uncertain, S1 outcome builders) plus additive `RemoteState` `Task`/`Billable` for S6 invalidation fidelity. Layer 3 (next) contains the legacy REST write path.

S5 layer-1 dispositions (orchestrator, independent of reviewer labels):
- S5L1-1 JSON-RPC error path truncates before redacting → token-prefix leak (Major, secret safety): accepted, fix now (redact then bound; boundary test).
- S5L1-2 unbounded server-supplied protocol version in an error message: accepted, fix.
- S5L1-3/4 comment accuracy (exactly-one-attempt wording; Accept intent): accepted, fix comments.
- S5L1-6 test gaps (no-session-id path, zero requests on uninitialized, 404→session expired): accepted, add tests.
- S5L1-7 dead `Error.cause`/misleading `CallResult` tags: accepted, fix.
- S5L1-8 response id/jsonrpc not validated: accepted, fix.
- S5L1-5 `isError` dropped when no content → assign to S5-IMPL-2 (ErrSchema-on-write must map to uncertain).
- S5L1-9 network errors swallowed to a static string: carried (Info).

Owner-confirmed S5 decisions (2026-10-10): implement hermetically (no live MCP reads/writes); production writes remain prohibited; contain the legacy REST submission path now (remove REST mutation methods, `ActionDelete`, slot-equality `Reconcile`, and the cmd mutation loop and `-approve` flag) so no production write path can bypass later approval gates.

Engineering decisions (orchestrator): package split `internal/mcp` (generic JSON-RPC transport) + `internal/gradion` (domain client + S1 outcome builders); offset pagination with a duplicate/truncation-driven `Complete` flag (absence only from a complete listing); exactly-one-attempt writes and bounded-retry reads; JSON framing primary, SSE fail-closed; token resolved at the boundary and held in memory only; tool-name constants re-validated against `tools/list`; conservative default that a write `isError` → uncertain (no whitelist); no REST fallback; capture `task`/`billable` additively in `RemoteState` for S6 invalidation fidelity. No delete path. Residual race (no conditional update / idempotency key) documented, not closed.

S5 implements the verified MCP tool contract behind the shared core (initialize, paginated listing, stable-ID selection, classifications, creates/edits, required audit reason, bounded parsing, uncertain outcomes) and returns observed remote IDs/states/outcomes in the S1 contract. Hermetic first; live reads/writes require named authority (not granted this session). Non-goals: S6 reconciliation/approval, S7 editorial, S8 entry points, S9 integrated acceptance.

S4 layer-2 dispositions (orchestrator, independent of reviewer labels):
- S4L2-2 readiness bypass via sticky `Empty`: accepted, fix now.
- S4L2-1 `MarkReviewedEmpty` drops prior review decisions: accepted, fix now.
- S4L2-3 readiness does not require `Reviewed`: accepted, fix (require review for a non-empty day).
- S4L2-4 manual evidence ID diverges from `evidence.ManualID`: accepted, fix.
- S4L2-5 not-attended does not remove a prior confirmed meeting: accepted, fix.
- S4L2-8 excluded evidence re-proposed and not flagged changed: accepted, fix.
- S4L2-9 merge slice aliasing / ordering nondeterminism: accepted, fix.
- S4L2-6 joined meeting also presented standalone (double-count only prevented by the overlap gate): carried to S6/S8 review UX.
- S4L2-7 joint block Duration vs union span: carried (cosmetic, no consumer).
No blocking secret/duplicate-write/lost-work issue; the persistence and merge guarantees otherwise hold with passing tests.

S4 layer-2 scope: additive `workflow` review fields (`Record.Review`, `ConfirmedInterval` additions, `MergeReviewState`, capacity bounds, SchemaVersion 1 preserved); new `internal/review` actions + `Readiness`; close layer-1 carried P1 (materialize git groups as proposed blocks, carry ProjectHints, use BlockJoint) and P4 (overlap validation across all confirmed blocks) and the N1 doc nit. Owner decisions: manual intervals need explicit confirm; not-attended meetings excluded from work but retained as evidence; cross-midnight carryover.

S4 layer-1 dispositions (orchestrator, independent of reviewer labels):
- A1 `internal/proposal` imports `internal/evidence/plugins` (breaks the recorded pure-package decision): accepted, fix now (move the predicate to `internal/evidence`).
- P2 covering-meeting behavior unpinned: accepted, add a test.
- P3 `SplitAtMaxDuration` zone handling inconsistent: accepted, fix.
- P5 empty-repository topic collapse reachable via exported `Build`: accepted, add a guard.
- P1 (git groups→ProposedTimeBlock, `BlockJoint`, ProjectHints carry-through) and P4 (full overlap validation) assigned to S4-IMPL-2.
- S3A1 (`time.Time` omitempty no-op) carried as a pre-existing pattern.

Owner-confirmed S4 decisions (2026-10-10):
1. Cross-midnight split keeps same-day fragments and flags the post-midnight part as carryover for the next day's own review; no auto-write into another day's record.
2. Manual intervals require an explicit confirm step; entry alone does not confirm work.
3. A meeting marked not-attended is excluded from confirmed work but retained as visible calendar evidence.
Engineering decisions (orchestrator): apply the additive S3 fix `SourceRef.At` (non-secret, not an identity input) so per-commit timing exists for the 45-minute adjacency heuristic (the spec's own example requires it); build new pure `internal/proposal` + thin `internal/review` rather than reusing `internal/timesheet.ClusterEntries`/`ResolveAll` (they expand/fabricate time); recompute proposals and persist only decisions; meetings are both adjacency barriers and overlap collisions; agent-session evidence is context-only; grid adjustment never expands silently; durations computed on absolute times; four-hour max = 240 min local/unverified with sub-grid remainders allowed. Additive `workflow` fields with SchemaVersion 1 preserved.

S4 builds Topic Candidates and separated Activity Groups using the 45-minute adjacency heuristic, suggests spans without confirming duration, supports attendance bulk-confirmation, project resolution, collisions, manual intervals, and empty days, and performs midnight/four-hour splits preserving exact minutes. Depends on S3. Non-goals: S6 approval/reconciliation, S7 editorial, S8 entry points/export.

S3 layer-2 dispositions (orchestrator, independent of reviewer labels):
- S3L2-1 `GitHubActivitySource` footgun could route PR discovery through branch-name inference: accepted, fix now (type boundary must prevent the mis-wire).
- S3L2-2 real `LocalGitCollectorAdapter` integration untested: accepted, add a test.
- S3L2-3 agent JSONL parser accepts shape-invalid lines as empty events (colliding IDs): accepted, fix now (content-free rejection).
- S3L2-4 warnings dropped on failure/contract paths: accepted, fix now (cheap).
- S3L2-6 stale fixture package doc: accepted, fix.
- S3L2-5 preflight production-dead: carried to S8 wiring.
- S3L2-7 calendar resolved-URL redaction gap: carried (documented literal-redaction limit).
- S3L2-8 commit provenance Branch nuance: carried (informational).
Reviewer judgment: layer 1 + layer 2 satisfy the S3 exit at the mechanism level and owner decision 3 is met; no production entry point invokes collection/preflight yet (S8-owned).

S3 layer-1 commit `28d7891` covers the evidence contract/identity/registry/config, the fixture plugin, and additive persistence. Carried: S3L1-7 literal redaction (documented limit); N1 `VerifyContracts` has no production caller (wire it into preflight in S3-IMPL-2/S8); N2 `ModeLive`/`IsContract` unexercised in production.

S3-IMPL-2 requirements: calendar/git/agent collector adapters emitting the contract; the agent-session JSONL parser (owner decision 3); wrap (not rewrite) `internal/calendar` and `internal/git`; host/repo/branch identity rules; dedup with retained source refs; visibly-incomplete failed sources; wire `VerifyContracts` into preflight so an enabled source is contract-checked before use.

S3 layer-1 dispositions (orchestrator, independent of reviewer labels):
- S3L1-1 every enabled source collected twice (Medium): accepted, fix now — live collection must run once; determinism stays a contract-check concern.
- S3L1-2 unguarded `SourceRef.URL` could persist a secret feed URL (Low/Medium): accepted, fix now — remove the field.
- S3L1-3 repo key drops host (Low): accepted, fix now — include normalized host.
- S3L1-4 empty repo key not rejected (Low): accepted, fix now — reject before identity derivation.
- S3L1-5 NUL-in-part ambiguity (Low): accepted, fix now — make the tuple unambiguous.
- S3L1-6 constant `bounded_timeout` check + canceled-as-timeout (Low): accepted, fix now.
- S3L1-8 test gaps (Complete=false persistence; on-disk secret assertion) (Info): accepted, add tests.
- S3L1-7 literal redaction limit (Info): carried as a documented limit.
- S3L1-9 file-count doc nit (Info): corrected by the orchestrator.
- Deferral: the agent-session JSONL parser is tracked as an explicit S3-IMPL-2 exit item; owner decision 3 is met only when layer 2 lands.

Owner-confirmed S3 decisions (2026-10-10):
1. Source identity uses an owner-configured non-secret id (never the secret feed URL or a filesystem path).
2. Cancelled events and declined/unknown invitations are retained as evidence with status; excluded later at proposal time (S4).
3. Agent-session scope is contract + JSONL parser + fixture only; the live emitter is a later task.
4. Raw evidence retention default is keep-all; prune only on explicit removal.

Accepted design decisions: contract lives in new `internal/evidence` with adapters in `internal/evidence/plugins`; stable identity is `sha256` over a NUL-joined scheme-versioned tuple with a kind prefix; persistence is an additive `workflow.Record` field (no SchemaVersion bump) with a new capacity bound; registry is compile-time (no dynamic executable loading); completeness is per-source `SourceResult` plus a persisted `CollectionSummary` with `RequireAcknowledgment`; calendar/git collectors are wrapped, not rewritten. Config is stdlib JSON (no new dependency). Non-goals: S4 proposal model, S5 transport, S6 reconciliation, agent emitter, legacy CLI migration.

S3 defines a new minimal Evidence contract (stable source identity, original timestamps, provenance, invitation status, project hints, unknown-duration representation) with calendar/git/PR adapters, an explicit plugin registry with contract checks and a fixture plugin, and visibly-incomplete failed sources. Legacy `SimpleEntry` is not assumed sufficient. Non-goals: S4 proposal/review model, S5 transport, S6 reconciliation identity.

S2 review dispositions (orchestrator, independent of reviewer labels):
- S2-R1 misleading "zero remote writes" diagnostic on non-first intent failure: accepted, fix now (diagnostic honesty).
- S2-R2 `-break-lock` silently ignored under `-dry-run`: accepted, fix now (reject or announce).
- S2-R3 stale lock unrecoverable via tool on hostname change / PID reuse (fail-closed): accepted, document the manual-removal limit.
- S2-R5 `UpsertOutcome` empty-key fallback diverges from `DeriveOperationKey`: accepted, fix now (latent duplicate-row footgun).
- S2-R8 test gaps (multi-op partial-progress crash; unidentified restart-gate; weak assertion): accepted, add tests.
- S2-R4 dead `workflowRecorder.UpdateDay`: accepted as optional cleanup (remove if unused).
- S2-R6 lock file not fsynced; S2-R7 `SaveDayCAS` production-dead: carried as Info with rationale (harmless; intended future primitive).
No blocking categories present: no secret exposure, lost confirmed work, duplicate/unauthorized write, stale approval, corrupt identity, or false success reporting. Production readiness remains unclaimed.

Owner-confirmed product decisions (2026-10-10):
1. An unresolved in-flight operation BLOCKS submission and is resolved by S6; S2 adds no manual mark-applied override.
2. S2 locks submission only; unifying the review and submit write paths under one day lock is deferred to S8, gap documented.
3. Stale-lock recovery is manual and token-gated; no auto-break.

Accepted design decisions: one operation ledger (reuse `Record.Outcomes`) with a durable `in_flight` intent state rather than a second collection; deterministic operation identity `DeriveOperationKey` over date/action/remoteID/localID/slot (closes F5); an explicit `Session`/`BeginDaySession` holding the day lock once so per-operation persistence does not deadlock; lock payload with host + token and pid-liveness (`syscall.Kill(pid,0)`) with manual `BreakStaleLock`; revision-checked `SaveDayCAS` and unexported `saveDay`. Explicit non-goals: S4 confirmations, S5 live/MCP semantics, S6 reconciliation identity and approval binding, and remote transactionality.

Reported limitations to investigate, not blanket exceptions: stale locks, package-only evidence/confirmation integration, skipped mappings, whole-record SaveDay races, unmanaged interval revision, and best-effort directory fsync.

## Readiness blockers

Exact revision/plan approval, durable pre-write intent and crash recovery, session-level submission ownership, stable-ID reconciliation, immediate remote recheck, preferred MCP submission transport, and integrated evidence/review/export remain unaccepted. Timezone, remote limits, and live create/edit semantics are not verified. MCP exposes no conditional update/idempotency field according to the read-only verification report; this absence leaves residual remote risk.

The accepted spec still contains dated initial credential failures and `implementation_status: not-started`. Later credential verification and source commits supersede those implementation-status facts, not the product requirements. Consult `docs/agents/credentials.md`, `docs/gradion-mcp-verification.md`, and this ledger for dated progress. Updating those caches must preserve the original design decision history.

## Update record template

Append a concise transition record, and update the active rows above:

```text
Date / task ID / lifecycle transition:
Owner authority reference:
Branch / HEAD / exact snapshot:
Worker/reviewer report references and verification level:
Open findings and disposition:
Commit and publication status:
Vault sync status:
Next gate and blockers:
```

No implementation deadlines are agreed. Keep planned slices unscheduled until the owner supplies dates.

## Transition records

```text
2026-10-10 / S1 / reconciliation -> reviewing
Owner authority reference: direct-dispatch session grant (implement/fix S1-S9, isolated branches/worktrees, local commits of accepted slices, repo docs + ai-database, hermetic checks). Not granted: push/publish/merge/force-push, live credentials/services, Keychain edits, production entries.
Branch / HEAD / exact snapshot: feat/keychain-submission-safety / faec6a4 / dirty tracked (cmd/sync-timesheet/main.go, main_test.go; AGENTS.md doc) + untracked internal/workflow/*.go; manifest .../opencode/s1-snapshot-faec6a4/manifest.txt
Worker/reviewer report references and verification level: orchestrator first-hand baseline (build/vet clean, workflow+sync tests pass); independent @oracle read-only review dispatched (ses_edc16670fffe3xSQjc6J3sSybD) - verdict pending.
Open findings and disposition: pending review; worker-reported limitations listed under Active slice.
Commit and publication status: uncommitted; no push.
Vault sync status: not yet (no accepted commit).
Next gate and blockers: independent review verdict -> bounded fix of accepted findings -> focused re-review. No architectural blockers raised yet.
```

```text
2026-10-10 / S1 / reviewing -> fixing
Owner authority reference: direct-dispatch session grant; local commits of accepted slices only.
Branch / HEAD / exact snapshot: feat/keychain-submission-safety / faec6a4 / unchanged working tree + untracked internal/workflow/*.go; reviewer verified md5s identical before/after review.
Worker/reviewer report references and verification level: independent @oracle read-only review (ses_edc16670fffe3xSQjc6J3sSybD) returned "changes required" (F1 Blocking; F2/F3/F6 Major/Medium; F4/F5/F8 Medium/Minor; F7 pre-existing note). Verified independently with source greps (no production caller of workflow.SaveDay; no production caller of MergeEvidence/MergeConfirmed; api fallback returns empty-ID entry).
Open findings and disposition: accepted fix set F1,F2,F3,F4,F6 (see disposition table); F5,F8 carried with rationale; F7 pre-existing, S5/S6-owned.
Commit and publication status: uncommitted; no push. Fixer S1-FIX-1 (ses_edc11522cffeuDwVD6A1flPhpk) dispatched, uncommitted result expected.
Vault sync status: not yet (no accepted commit).
Next gate and blockers: focused independent re-review of fixed snapshot; then commit if accepted. No architectural blockers.
```

```text
2026-10-10 / S1-FIX-1 / fixing -> re-reviewing
Owner authority reference: direct-dispatch session grant; local commits of accepted slices only.
Branch / HEAD / exact snapshot: feat/keychain-submission-safety / faec6a4 / six changed files only (main.go, main_test.go tracked; models.go, store.go, file_store.go, file_store_test.go untracked); merge.go/merge_test.go unchanged; manifest .../s1-snapshot-fix1-manifest.txt
Worker/reviewer report references and verification level: @fixer S1-FIX-1 (ses_edc11522cffeuDwVD6A1flPhpk) worker-reported: F1/F2/F3/F4/F6 implemented, regression tests demonstrated to fail pre-fix, build/vet/test/race green. Not yet independently verified.
Open findings and disposition: accepted set F1/F2/F3/F4/F6 to be confirmed by focused re-review; F5/F8 carried; F7 pre-existing. Fixer noted a residual wording item at main.go ~410-412 (unidentified vs "uncertain") left outside named F3 scope - re-review to judge blocking vs follow-up.
Commit and publication status: uncommitted; no push.
Vault sync status: not yet (no accepted commit).
Next gate and blockers: focused independent re-review verdict; then commit if accepted and re-verify. No architectural blockers.
```

```text
2026-10-10 / S1 / re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commit of independently accepted slice.
Branch / HEAD / exact snapshot: feat/keychain-submission-safety / d471b46 (parent faec6a4) / 8 files committed: internal/workflow/{models,store,merge,file_store,file_store_test,merge_test}.go + cmd/sync-timesheet/{main,main_test}.go; md5s matched the accepted manifest before commit.
Worker/reviewer report references and verification level: focused re-review (ses_edc16670fffe3xSQjc6J3sSybD) verdict "accepted"; F1/F4/F6 fully closed, F3 closed with non-blocking residual N1, F2 re-scoped S6; no new defects. Orchestrator final build/vet/test/race green on the committed snapshot.
Open findings and disposition: F5/F8 carried; F7 pre-existing S5/S6; N1/N2 non-blocking doc follow-ups; F2 population gap owned by S6. None blocking.
Commit and publication status: committed locally d471b46; NOT pushed, no PR.
Vault sync status: complete - project note `Project - assignment-1.md` and daily note `2026-10-10.md` updated with S1 commit, verification level, and remaining gaps.
Next gate and blockers: next dependency-ready slice (S2 or S3). Production blockers unchanged: no live/remote verification, approval not revision-bound, no cross-process submission lock or durable pre-write intent.
```

```text
2026-10-10 / docs / committed -> vault-synced
Owner authority reference: direct-dispatch session grant; local docs commit; ai-database update authority granted this session.
Branch / HEAD / exact snapshot: feat/keychain-submission-safety / d3b9378 to follow / orchestration docs.
Worker/reviewer report references and verification level: vault writes are orchestrator-owned; no independent review required for process docs.
Open findings and disposition: none new.
Commit and publication status: docs update committed locally; NOT pushed.
Vault sync status: complete for S1.
Next gate and blockers: begin S2 (crash-safe submission lifecycle) as the next safety slice; or S3. No blockers.
```

```text
2026-10-10 / S2 / planned -> authorized -> designing -> implementing
Owner authority reference: direct-dispatch session grant (S1-S9 bounded worker tasks, isolated branches, local commits of accepted slices). Owner confirmed three S2 product decisions (block-only/S6 resolution; submission-only lock deferring unification to S8; manual token-gated break).
Branch / HEAD / exact snapshot: feat/s2-crash-safe-lifecycle created at 2507498 (S1 d471b46 ancestor); tree clean of tracked changes at dispatch.
Worker/reviewer report references and verification level: @oracle S2 design pass completed and accepted (session ses_edc07a847ffeGqOzf8mRUaG8oW). @fixer S2-IMPL-1 dispatched (ses_edc02ce48ffeDu2D5hOUm03WOU); result pending, uncommitted.
Open findings and disposition: none yet; S1 carried items (F2->S6, F5 addressed by design DeriveOperationKey, F7/F8 recorded) remain.
Commit and publication status: uncommitted; no push.
Vault sync status: S1 synced; S2 pending acceptance.
Next gate and blockers: independent read-only review of the S2 implementation snapshot. No architectural blockers.
```

```text
2026-10-10 / S2-IMPL-1 + S2-FIX-1 / implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commit of independently accepted slice.
Branch / HEAD / exact snapshot: feat/s2-crash-safe-lifecycle / d4d6c00 (parent 9144849, S1 d471b46 ancestor) / 12 files committed: internal/workflow/{models,merge,store,file_store,lock,file_store_test,merge_test,lock_test,session_test}.go + cmd/sync-timesheet/{main,main_test,crash_test}.go; md5s matched the accepted fix1 manifest before commit.
Worker/reviewer report references and verification level: @fixer S2-IMPL-1 then S2-FIX-1 (ses_edc02ce48ffeDu2D5hOUm03WOU); @oracle design (ses_edc07a847ffeGqOzf8mRUaG8oW); @oracle review + focused re-review (ses_edbf50f2fffenNp0xTGjta5oci) verdict ACCEPTED both rounds, no blocking findings. Orchestrator final gofmt/build/vet/test/race green on committed snapshot.
Open findings and disposition: S2-R1/R2/R3/R4/R5/R8 closed by S2-FIX-1; S2-R6/R7 carried Info; cosmetic test-double methods and one untested uncertain-then-later-op path recorded.
Commit and publication status: committed locally d4d6c00; NOT pushed, no PR.
Vault sync status: complete - project note and daily note updated with S2 commit, verification level, and remaining gaps.
Next gate and blockers: next dependency-ready slice (S3 evidence contracts or S5 MCP transport). Production blockers unchanged: no live/MCP verification, approval not revision-bound, no reconciliation identity, no idempotency key.
```

```text
2026-10-10 / S3 / planned -> designing -> implementing (layers 1,2) -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed four S3 decisions (configured non-secret source id; retain cancelled/declined evidence with status and filter at proposal; agent contract/parser/fixture only; keep-all raw retention).
Branch / HEAD / exact snapshot: feat/s3-evidence-contracts / 5ff9894 (layer 1 28d7891, S1 d471b46 and S2 d4d6c00 ancestors) / 25 files across two commits; md5s matched accepted manifests before each commit.
Worker/reviewer report references and verification level: @oracle design (ses_edbe7dae8ffe7t2q6wrEbgh6ct); @fixer S3-IMPL-1/S3-FIX-1/S3-IMPL-2/S3-FIX-2 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle reviews + re-reviews (ses_edbd79ceaffewYj2nAFey2Oae5, ses_edbb45235ffeDv2KWirtxUB8T7) all ACCEPTED. Orchestrator gofmt/build/vet/test/race green on committed snapshots.
Open findings and disposition: S3L1-1..6/8 and S3L2-1..4/6 closed; S3L1-7/S3L2-7 literal-redaction limit, S3L2-5 preflight production-dead (S8), S3L2-8 provenance nuance, N2 markers carried as Info.
Commit and publication status: committed locally 28d7891 and 5ff9894; NOT pushed, no PR.
Vault sync status: complete - project note and daily note updated with the S3 commits, verification level, and remaining gaps.
Next gate and blockers: next dependency-ready slice (S4 proposal model; or S5 MCP transport). Production blockers unchanged; no production entry point invokes collection/preflight yet.
```

```text
2026-10-10 / S4 / planned -> designing -> implementing (L1) -> reviewing -> fixing -> re-reviewing -> accepted -> implementing (L2) -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed three S4 decisions (cross-midnight carryover; manual intervals need explicit confirm; not-attended meetings excluded from work but retained as evidence).
Branch / HEAD / exact snapshot: feat/s4-proposal-model / 2a06b5b (layer 1 5f373f4; S1 d471b46, S2 d4d6c00, S3 28d7891+5ff9894 ancestors) / 25 files across two commits; md5s matched accepted manifests before each commit.
Worker/reviewer report references and verification level: @oracle design (ses_edb9eaf21ffeQ2PeGagGke0fFb); @fixer S4-IMPL-1/S4-FIX-1/S4-IMPL-2/S4-FIX-2 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle reviews/re-reviews (ses_edb8853bdffekCYaaBjV5CcS3H, ses_edb66f4ceffefEwiHLGPbXG2dF) accepted. Orchestrator gofmt/build/vet/test/race green on committed snapshots.
Open findings and disposition: A1/P2/P3/P5 and S4L2-1..5/8/9 closed; S4L2-6/S4L2-7 and N1/N2/N3 carried as non-blocking follow-ups. No secret/duplicate-write/lost-work issue.
Commit and publication status: committed locally 5f373f4 and 2a06b5b; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S4 commits.
Next gate and blockers: next dependency-ready slice (S5 MCP transport; then S6). Production blockers unchanged; no production entry point invokes proposal/review yet.
```

```text
2026-10-10 / S5 / planned -> designing -> implementing (L1,L2,L3) -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed: hermetic implementation (no live MCP reads/writes); contain the legacy REST write path now.
Branch / HEAD / exact snapshot: feat/s5-mcp-transport / c943e97 (layer 1 42fba53, layer 2 b59661a; S1-S4 ancestors) / 21 files across three commits (layer 3 net -4101 lines); md5s matched accepted manifests before each commit.
Worker/reviewer report references and verification level: @oracle design (ses_edb501788ffeQD2aF5HG3ypYyu); @fixer S5-IMPL-1/S5-FIX-1/S5-IMPL-2/S5-FIX-2/S5-IMPL-3/S5-FIX-3 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle reviews/re-reviews (ses_edb405f00ffeKRGaBa2WZ6huih, ses_edb26e5c9ffeSk3sDVtaun0N9U, ses_edaf181a0ffeyLJazmia34qvoW) accepted. Orchestrator gofmt/build/vet/test/race and e2e-tag green on committed snapshots.
Open findings and disposition: S5L1-1..4/6..8, F1..F5/T1..T4, F1..F3 closed; N1 doc-precision, S5L1-9, two dead api constants, duplicated api helpers carried. F4 (S6/S8 carry): the S2 integrated restart gate is dormant until the approval-bound MCP path re-creates it. No secret/duplicate-write/lost-work issue.
Commit and publication status: committed locally 42fba53, b59661a, c943e97; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S5 commits.
Next gate and blockers: next dependency-ready slice S6 (revision-bound plans, approval, reconciliation). Production blockers unchanged; live MCP semantics unverified; residual remote race documented.
```

```text
2026-10-10 / S6 / planned -> designing -> implementing (L1,L2) -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed three S6 decisions (configured default + review override for Project→classification; explicit adoption required to resolve an uncertain create; fixed machine-readable edit audit reason).
Branch / HEAD / exact snapshot: feat/s6-plans-approval-reconciliation / 40d266d (layer 1 3dfe814; S1-S5 ancestors) / 19 files across two commits; md5s matched accepted manifests before each commit.
Worker/reviewer report references and verification level: @oracle design (ses_edadd7e1dffeoojCEagYiCl8LA); @fixer S6-IMPL-1/S6-FIX-1/S6-IMPL-2/S6-FIX-2 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle reviews/re-reviews (ses_edac0976fffem8H9n9vQ2ysCMI, ses_edaa37e94ffe9C6CgTkGN5i1CM) accepted. Orchestrator gofmt/build/vet/test/race green on committed snapshots.
Open findings and disposition: F1-F6 (layer 1) and F1-F7 (layer 2) closed or carried; F1 approval-trust is an S8 obligation; NEW-1, F4, F5, RebuildFailed carried as Info; residual remote race documented.
Commit and publication status: committed locally 3dfe814 and 40d266d; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S6 commits.
Next gate and blockers: next dependency-ready slice S7 (optional editorial synthesis and usage). Production blockers unchanged; no production entry point wires Execute yet.
```

## Documentation review

On 2026-10-10 an independent documentation reviewer identified seven gaps. Corrected actor labels for orchestrator-only commits, explicit session vault authority, shared-workflow coverage, provisional remote limit/timezone assumptions, mapping ownership, review snapshot/independence fields, and significant-outcome daily logging. Corrections affect documentation only; S1 source review remains pending. Independent re-review of these documentation corrections has not been run.
