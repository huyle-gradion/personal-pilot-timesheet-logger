# Orchestration state

Updated: 2026-10-11. This is a resumable status ledger, not a grant of authority. Update it at lifecycle transitions without duplicating the product specification.

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
| S7 editorial synthesis | `3d4bcc8`, committed on `feat/s7-editorial-synthesis`, independently reviewed/re-reviewed accepted |
| S8 entry points/export | layers 1 `06ce95b`, 2 `d65bdf0`, committed on `feat/s8-entry-points-export`, independently reviewed/re-reviewed accepted |
| S9 integrated acceptance | `ca96a20`, committed on `feat/s9-integrated-acceptance`, integrated review accepted (source acceptance; release is a separate owner decision) |
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

## S7 active slice: optional editorial synthesis and usage

| Field | Status |
| --- | --- |
| Slice | S7 optional editorial synthesis and usage |
| Lifecycle | committed, independently accepted |
| Branch / base | `feat/s7-editorial-synthesis` (S1-S6 ancestors verified) |
| Commit | `3d4bcc8` (22 files) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_eda93832fffeUT2287dqL1w6Cp`) |
| Independent review | reviewed and re-reviewed accepted (S7-F1/F2/F3/F6 closed; S7-F4/F5 carried to S8) |
| Next gate | select the next dependency-ready slice (S8 shared entry points and managed vault export) |

S7 is complete at the code level: text-only optional editorial layer (bounded request, fail-closed validation, deterministic fallback, command adapter, usage/budget, apply) + additive `workflow` fields. S7's exit holds at the mechanism level: AI is optional, constrained (cannot set intervals/attendance/projects/evidence/approvals), cannot authorize or confirm work, and text changes participate in S6 invalidation.

Carried S7 follow-ups (non-blocking): S7-F4 (usage does not accumulate across reruns; S8 must pass accumulated Known `Spent` and add runs); S7-F5 (one editor call per target; S8 may batch); `Report.Orchestration` defaults to Unknown when omitted (honest; S8 presentation). No production entry point wires the editor; live agent semantics unverified (S8/S9). Owner override recorded: soft daily target 1000 tokens incl. cache (spec said 600).

## S8 active slice: shared entry points and managed vault export

| Field | Status |
| --- | --- |
| Slice | S8 shared entry points and managed vault export |
| Lifecycle | committed (layers 1 + 2), independently accepted |
| Branch / base | `feat/s8-entry-points-export` (S1-S7 ancestors verified) |
| Commits | layer 1 `06ce95b` (15 files); layer 2 `d65bdf0` (17 files, −2097 net) |
| Design | @oracle design pass accepted by the orchestrator (session `ses_eda70735effenHla0rxFzsO0X4`) |
| Independent review | layers 1 and 2 each reviewed and re-reviewed; accepted (S8L1-1..7, F1..F5 closed) |
| Next gate | select the next dependency-ready slice (S9 integrated acceptance) |

S8 is complete at the code level: the shared core `internal/run` + managed export `internal/export` (layer 1); the thin CLI `cmd/timesheet`, the skill rewrite, and the deletion of the dead `cmd/sync-timesheet` + `internal/api` (layer 2). S8's exit holds: both frontends share validation and approval semantics (the documented `resolve.json` shape parses; approval is plan-content-hash-bound; `submit` uses the record's active approval), and exports are safe and truthful (marker-scoped, idempotent, journal/YAML-preserving, statuses reviewed/submitted/partial/failed/unresolved, never replay a submission).

Carried S8 follow-ups (non-blocking): `resolve` is fail-loud on an already-applied decision (Info); duplicate decision keys count cosmetically (Info); orchestration usage measurement wiring (Info). The concrete live editorial command and live MCP/agent verification remain S9. The legacy `cmd/review-timesheet`/`internal/timesheet`/`internal/storage` remain physically but are not used by the new flow (documented legacy). No live endpoint contacted; production readiness is not claimed.

## S9 active slice: integrated acceptance and release decision

| Field | Status |
| --- | --- |
| Slice | S9 integrated acceptance and release decision |
| Lifecycle | closed - source-accepted; owner deferred the production pilot (2026-10-10) |
| Branch / base | `feat/s9-integrated-acceptance` (S1-S8 ancestors verified) |
| Commit | `ca96a20` (5 files; the harness + the four-hour split implementation) |
| Independent review | integrated review + focused re-review; accepted (F1/F3/F4/F5/F6 closed, F2 partial with residual documented, F7 corrected) |
| Next gate | owner release decision and any live-verification authorization |

S9 is complete at the code/acceptance level: a hermetic integrated acceptance harness over the shared core covers the redesign's 10 required scenarios + 5 fault cases (all genuine), and the four-hour split is now implemented end-to-end (linked fragments, exact totals) after the integrated review surfaced it as missing.

Carried S9 follow-ups (non-blocking): F-NEW-1 (a separate Project/Trim/Join decision on a split block's ID resolves only the last fragment); F-NEW-2 (>4h joint "counts once" Duration hint untested); F-NEW-3 (defensive split hardcodes 240); F-NEW-4 (split→plan→submit integration not asserted end-to-end); F2 residual (the durable-record/export secret clause is by-construction). Live-authority verification (real Keychain reference, live MCP list/create/edit + stable IDs/pagination/validation/write semantics, conditional update/uncertain-write reconciliation, Gradion classification fields and the four-hour REMOTE rule, calendar invitation metadata) remains unverified and requires separate authority.

S9 integrated-review dispositions (orchestrator, independent of reviewer labels):
- F1 four-hour split not wired into the production path; durable fragments unlinked (HIGH, real requirement gap): accepted, fix now (propagate linkage + wire SplitAtMaxDuration into the confirm path; end-to-end test).
- F2 scenario 9 secret assertions vacuous (Medium): accepted, fix (route the sentinel through a carrying stage).
- F3 scenario 5b bypasses the core (Medium): accepted, fix (exercise the real review path).
- F4 no CLI/skill parity in the harness (Low/Medium): accepted, add/reference.
- F5 scenario 8 malformed-output coverage (Low): accepted, add.
- F6 misleading reload comment (Low): accepted, fix.
- F7 header over-claim (Info): accepted, correct.
The reviewer confirmed 9 of 10 scenarios + all 5 fault cases are genuine, hermetic, and assert durable state / call counts; and that the harness is honest about hermeticity and the literal-secret bound. It does NOT establish production readiness.

S9 builds a hermetic integrated acceptance harness over the shared core (`internal/run`) covering the redesign's 10 required scenarios + fault injection (intent-persist failure, uncertain write, remote change invalidating approval, export failure without replay, concurrent writer). Live MCP/agent/Keychain verification and the concrete editorial command require separate owner authority. Exit: independent integrated review, source acceptance, and owner release decision; a production pilot requires separate approval of real work in an exact daily plan.

S8 layer-2 dispositions (orchestrator, independent of reviewer labels):
- F1 skill's documented `resolve.json` keys do not decode into `submit.Resolution` (no json tags) → the adopt decision is silently dropped (Major): accepted, fix now (add tags; CLI test with the documented shape).
- F2 `resolve` prints an unconditional success count (Medium): accepted, fix (return/print the applied count; unmatched = error/no-op).
- F3 git credential resolved for every subcommand incl. read-only `export` (Low): accepted, fix (lazy).
- F4 `--ack` ignored without `--decisions` (Low): accepted, fix.
- F5 test gaps (documented resolve keys, Date() default, range iteration, skill resolve subcommand, CLI secret-ref guard) (Info): accepted, add.
The reviewer confirmed the rest of layer 2 holds: thin CLI adapter, no bare `-approve`, record-resolved active approval, no-AI operation, complete deletions with no dangling references, secret refs keychain-only/in-memory, and the export properties.

S8 layer-2 scope: new thin CLI `cmd/timesheet` delegating to `internal/run` (revision-bound `--plan` approval; no bare `-approve`; no-AI operation; per-day ranges); rewrite the skill to delegate to the CLI (harness-agnostic, no accounting); delete `cmd/sync-timesheet` + `internal/api` (owner decision); update `tests/skill_test.go` + `tests/e2e_test.go`. The concrete live editorial command is deferred to S9.

S8 layer-1 dispositions (orchestrator, independent of reviewer labels):
- S8L1-1 editorial text never applied through the core (Major, gating): accepted, fix now (fold persisted suggestions via `editorial.Apply` before confirm; run-level test).
- S8L1-2 no all-failed status (Medium/Low): accepted, fix (add `StatusFailed`; spec requires the section to reflect failed outcomes).
- S8L1-3 absent git credential not flagged in preflight (Low): accepted, fix.
- S8L1-4 export atomic write lacks a directory fsync (Low): accepted, fix.
- S8L1-5 orchestration usage never populated / accumulate would replace (Info): accepted, fix accumulate additively; measurement wiring to layer 2/S9.
- S8L1-6 `keychainCode` mislabels git errors (Info): accepted, fix.
- S8L1-7 test gaps (collect, remote-init failure, offline submit, resolve, usage_unknown, parity, precedence): accepted, add.
The reviewer accepted the rest of layer 1: preflight contract/terminal-credential/remote refusal (a), record-resolved `ActiveApproval` + zero-write refusal (b), accumulated editorial usage (c), and the export properties.

Owner-confirmed S8 decisions (2026-10-10): (1) the managed export uses HTML-comment markers and leaves any legacy `## Clustered timesheet` block untouched (no silent migration); (2) delete the dead `cmd/sync-timesheet` stub and the deprecated `internal/api` read surface; (3) defer the concrete live editorial command to S9 (S8 stays hermetic).

Engineering decisions: new `internal/run` (shared deterministic core composing proposal/review/submit/editorial/evidence) + `internal/export` (marker-delimited, idempotent, journal/YAML-preserving, atomic); `Submit` uses the record's `ActiveApproval` and compares `ApprovalID` (S6-F1); export is read-only over durable state and never replays a submission; editorial `Spent` accumulates via persisted `EditorialUsage` with unknown surfaced honestly; default date is the configured timezone with per-day approvals; the CLI/skill both call `internal/run` (structural parity). Non-goals: S9 acceptance, live MCP/agent verification, multi-day combined approval.

S8 wires the shared deterministic core into the CLI and the skill (preflight -> collection -> proposal -> review -> save -> submission preview -> approval -> execute -> outcome -> export), replaces the retired REST stub and the bare `-approve`, and adds a managed daily-note export (reviewed-not-submitted / partial / unresolved / submitted) that preserves journal text and YAML and never replays a submission on export failure. It discharges the S8 obligations: wire `VerifyContracts`/`PreflightContracts`, pass `workflow.ActiveApproval` and compare `ApprovalID` before `Execute`, and supply accumulated editorial `Spent`/runs. Depends on S4 and S6; editorial integration may follow S7. Non-goals: S9 integrated acceptance.

S7 dispositions (orchestrator, independent of reviewer labels):
- S7-F1 omitted `Spent` disables the soft budget on the fresh-day path (Medium): accepted, fix now (default a zero/unknown Spent to Known zero).
- S7-F2 CommandEditor captures unbounded output before the parse-time cap (Low): accepted, fix now (bound at capture).
- S7-F3 dead `boundedExcerpt` (Low): accepted, remove.
- S7-F6 changed-`EvidenceIDs` reuse not explicitly tested (Info): accepted, add a test/document.
- S7-F4 no accumulation across reruns (Info): carried to S8 (pass accumulated Known `Spent` and add runs).
- S7-F5 one editor call per target (Info): carried to S8 (batching consideration).
The reviewer judged the `internal/workflow/file_store.go` capacity-line deviation ACCEPTABLE (required to make `MaxEditorialCount` non-dead; additive, minimal).

Owner-confirmed S7 decisions (2026-10-10): (1) the optional editorial agent is invoked via a harness-agnostic command (subprocess) adapter; the concrete live command is wired at S8, and the CLI works with no agent (deterministic fallback); (2) the soft daily editorial target is 1000 tokens (an explicit owner override of the spec's 600) and counts editorial input+output+cache tokens across retries/reruns.

Engineering decisions: text-only boundary (output decodes only `Text{Title,Description}`; `Apply` touches only `ProposedTimeBlock.Description`); fail-closed whole-response rejection with a strict key-set check; display-only title, description is the persisted text; applied text flows through the existing review path so an S6 approval is invalidated by `LocalFingerprint`/`PlanHash` (no new S6 code); unknown measurement is not zero; editorial and orchestration usage are separate fields. Non-goals: S8 entry points/export/presentation, S9 acceptance, live agent adapter wiring.

S7 is a text-only optional editorial layer: AI may propose titles/descriptions only (never intervals/attendance/projects/evidence/approvals), with summary reuse for unchanged evidence, a soft 600-token daily editorial target including retries, orchestration usage kept separate (unknown is not zero), budget-exhaustion fallback to deterministic/manual text, and no-agent operation. Text changes must participate in S6 invalidation. Depends on S4. Non-goals: S8 entry points/export, S9 acceptance.

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

**Live read verification (2026-10-10) found a code-level blocker (B5), now FIXED, live-confirmed, and committed (`6facb5b`):** the live Gradion `list_my_entries` / `get_classifications` responses are `{resources:[{id,kind,label}]}` reference envelopes; structured entries come from `timesheet__get_my_week`. `internal/gradion` was adapted to read structured entries via `get_my_week` (bounded, fail-closed, hard entry bound, cursor that cannot skip weeks). A live read-only confirmation returned 5 entries (`complete=true`, all fields present), `EntryByID` found, and 20 classifications (19 named). See `docs/gradion-mcp-verification.md` section 9. Carried residuals: F4 (Billable nil-vs-non-nil asymmetry), F6 (week path lacks a first-page stability re-check), N1 (stale comment). **B6 resolved by owner/admin confirmation (2026-10-10):** the Gradion per-entry maximum is 4 hours (240 minutes) and the governing timezone for entry times is Asia/Ho_Chi_Minh (owner/admin evidence, not server-observed; the server documents neither).

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

```text
2026-10-10 / S7 / planned -> designing -> implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed: a command-adapter editorial agent (live wiring at S8); soft daily target 1000 tokens incl. cache (explicit owner override of the spec's 600).
Branch / HEAD / exact snapshot: feat/s7-editorial-synthesis / 3d4bcc8 (S1-S6 ancestors) / 22 files; md5s matched accepted manifests before commit.
Worker/reviewer report references and verification level: @oracle design (ses_eda93832fffeUT2287dqL1w6Cp); @fixer S7-IMPL-1/S7-FIX-1 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle review + re-review (ses_eda7cd3d8ffeHSaJ7LS4R2DCVp) accepted. Orchestrator gofmt/build/vet/test/race green.
Open findings and disposition: S7-F1/F2/F3/F6 closed; S7-F4/F5 carried to S8 (accumulated usage, per-target batching). No secret/duplicate-write/lost-work issue.
Commit and publication status: committed locally 3d4bcc8; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S7 commit.
Next gate and blockers: next dependency-ready slice S8 (shared entry points and managed vault export). Production blockers unchanged; no production entry point wires the editor.
```

```text
2026-10-10 / S8 / planned -> designing -> implementing (L1,L2) -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. Owner confirmed three S8 decisions (comment-marked export, legacy block untouched; delete the dead cmd/sync-timesheet + internal/api; defer the concrete editorial command to S9).
Branch / HEAD / exact snapshot: feat/s8-entry-points-export / d65bdf0 (layer 1 06ce95b; S1-S7 ancestors) / 32 files across two commits (layer 2 net -2097 lines); md5s matched accepted manifests before each commit.
Worker/reviewer report references and verification level: @oracle design (ses_eda70735effenHla0rxFzsO0X4); @fixer S8-IMPL-1/S8-FIX-1/S8-IMPL-2/S8-FIX-2 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle reviews/re-reviews (ses_eda5e6395ffex0M34vLGCR7hYR, ses_eda3f07c4ffe36JboGzdTQpIVv) accepted. Orchestrator gofmt/build/vet/test/race and e2e-tag green.
Open findings and disposition: S8L1-1..7 and F1..F5 closed; N1/N2 Info carried; S9 owns live MCP/agent verification and the concrete editorial command. No secret/duplicate-write/lost-work issue.
Commit and publication status: committed locally 06ce95b and d65bdf0; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S8 commits.
Next gate and blockers: S9 integrated acceptance (hermetic scenario matrix + fault injection, authorized live read-only checks where needed). Production blockers unchanged; live MCP/agent semantics unverified.
```

```text
2026-10-10 / S9 / planned -> implementing (harness) -> reviewing -> fixing -> re-reviewing -> integrated acceptance ACCEPTED -> committed
Owner authority reference: direct-dispatch session grant; local commits of independently accepted slices. No live authority granted.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / ca96a20 (S1-S8 ancestors) / 5 files; md5s matched the accepted manifest before commit.
Worker/reviewer report references and verification level: @fixer S9-IMPL-1/S9-FIX-1 (ses_edbe22f3fffegJeHjAvwnOt9JA); @oracle integrated review + re-review (ses_eda1d532affeiYVFk5o5iho9NN) ACCEPTED. Orchestrator gofmt/build/vet/test/race and e2e-tag green.
Open findings and disposition: F1 four-hour split now implemented end-to-end; F3/F4/F5/F6/F7 closed; F2 partial (residual documented); F-NEW-1..4 carried as non-blocking follow-ups. Live-authority verification items explicitly not run.
Commit and publication status: committed locally ca96a20; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the S9 commit.
Next gate and blockers: owner release decision and any live-verification authorization. Production blockers: live MCP/agent/Keychain and remote-policy semantics unverified; residual remote write race; no production entry point contacted a live service.
```

```text
2026-10-10 / live-verification / owner granted read-only authority -> checks run -> BLOCKER FOUND
Owner authority reference: owner granted live read-only authority (Keychain resolution, read-only MCP checks, calendar-metadata inspection; no writes).
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 3ff1301 / tracked tree clean; a temporary in-module read-only probe was created, run, and removed (not committed).
Evidence: docs/gradion-mcp-verification.md section 9 (sanitized; no secrets). Keychain: all three items resolve via internal/keychain. MCP: initialize/tools-list/VerifyCapabilities OK (71 tools, protocol 2025-06-18). Calendar: PARTSTAT metadata confirmed (ACCEPTED 39 / DECLINED 5 / NEEDS-ACTION 19).
Blocker: B5 - live list_my_entries/get_classifications return {resources:[{id,kind,label}]}; the internal/gradion parser expects a bare array/known list key, so live reads fail with GRADION_ERR_READ_SCHEMA; resources/read with the raw id returns -32602 unsupported uri scheme; no outputSchema declared. This blocks S6 reconciliation against live as built and is a pilot blocker. B6: four-hour remote rule and governing timezone still unverified (need a write).
No writes; no secrets displayed; no Keychain item modified.
Next gate and blockers: owner decision - authorize further read-only contract discovery + a bounded parser fix, or accept the gap. Release remains blocked on B5 (and B6 for full live parity).
```

```text
2026-10-10 / B5 / diagnosed -> bounded fix implemented -> live read-only confirmed -> independent review in progress
Owner authority reference: owner authorized read-only discovery + a bounded B5 fix (no writes, no Keychain modification, no push/merge/publication).
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 62cbc95 / internal/gradion/{tools,parse,client,client_test,week_test}.go (uncommitted); manifest .../opencode/b5-fix1-manifest.txt.
Evidence: docs/gradion-mcp-verification.md section 9 (verified contract). Live read-only confirmation of the fixed reader: 5 entries, complete=true, id/start/end/date/classification present on all; EntryByID found; Classifications 20 (19 named). Orchestrator-run; sanitized; no values printed.
Open findings and disposition: fixer flagged a capability-based selection (get_my_week structured path when declared, else bounded legacy path); independent review in progress.
Commit and publication status: uncommitted pending review; no push.
Vault sync status: pending acceptance.
Next gate and blockers: independent review verdict; then commit the accepted B5 fix locally and sync the vault. B6 remains open.
```

```text
2026-10-10 / B5 / independent review ACCEPTED -> hardening round B5-FIX-2 -> focused re-review ACCEPTED -> committed
Owner authority reference: owner authorized read-only discovery + a bounded B5 fix (no writes, no Keychain modification, no push/merge/publication).
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 6facb5b (parent 69ec337) / internal/gradion/{tools,parse,client,client_test,week_test}.go; md5s matched the accepted b5-fix2 manifest before commit.
Evidence: @oracle reviews (ses_ed9d1b9dbffebq8mOeGCKKtr8a) ACCEPTED both rounds; B5 resolved. Orchestrator live read-only confirmation: 5 entries, complete=true, all fields present; EntryByID found; Classifications 20 (19 named).
Open findings and disposition: F1/F2/F3/F5/F7 closed; F4 (Billable asymmetry) and F6 (no week-path stability re-check) carried (need B6/a write); N1 (stale comment) carried.
Commit and publication status: committed locally 6facb5b; NOT pushed, no PR.
Vault sync status: complete - project and daily note updated with the B5 fix.
Next gate and blockers: B6 (four-hour remote rule, governing timezone) via trusted documentation/admin evidence; then the owner release decision. Production submission remains blocked; no live write performed.
```

```text
2026-10-10 / B6 / read-only documentation exhausted -> owner/admin confirmation -> resolved
Owner authority reference: owner granted read-only verification; B6 resolved by owner/admin evidence (no write).
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 6facb5b / docs only.
Evidence: docs/gradion-mcp-verification.md section 9. Read-only tool-schema inspection found no documented duration limit or timezone; the owner (admin) confirmed the per-entry maximum is 4 hours (240 minutes) and the governing timezone is Asia/Ho_Chi_Minh. Recorded as owner/admin evidence, not server-observed.
Open findings and disposition: B6 resolved; F4/F6/N1 residuals remain (F4 needs a write to confirm billable echo).
Commit and publication status: docs committed locally; NOT pushed.
Vault sync status: complete.
Next gate and blockers: owner release decision. Production submission remains blocked; no live write performed.
```

```text
2026-10-10 / closure / source-accepted; production pilot deferred by owner
Owner authority reference: owner decision (2026-10-10): accept the redesign as source-complete with B5 fixed and B6 confirmed; keep production submission blocked; defer any pilot to a separately approved exact daily plan.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 8948056 / tracked tree clean.
Evidence: S1-S9 each independently reviewed (and re-reviewed where findings existed); integrated acceptance harness (10 scenarios + 5 fault cases) accepted; B5 fixed (6facb5b) and live-confirmed read-only; B6 confirmed by owner/admin. See docs/gradion-mcp-verification.md and this ledger.
Open findings and disposition: F4 (Billable asymmetry - needs a write), F6 (week-path absence-safety), N1 (stale comment), residual remote write race - all documented, non-blocking for source acceptance.
Commit and publication status: all local; NOT pushed, no PR, no merge.
Vault sync status: complete - project and daily notes updated with the release decision.
Next gate and blockers: none pending; a production pilot requires a separately approved exact daily plan and live write authority. Production submission remains blocked.
```

```text
2026-10-11 / S8-FOLLOWUP-1 / empty-day review draft -> independent review ACCEPTED -> hardening (F2 + tests) ACCEPTED -> committed
Owner authority reference: direct-dispatch session grant; local commits of accepted slices only.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 0e3010d (parent fd58948) / internal/run/{propose,collect}.go(+tests), cmd/timesheet/main.go(+test); md5s matched the accepted manifest before commit.
Evidence: @oracle independent review (ses_ed733d357ffeCnUwSOm2Epf0cc) ACCEPTED (no introduced defects); focused re-review ACCEPTED (F2/F4/F5/F6 closed, no new defects). Orchestrator CLI confirmation (rebuilt from the working tree, fresh store): two SEPARATE single-date invocations - `review --date 2026-10-10` and `review --date 2026-10-08` - each printed a 0-block proposal (exit 0) with no sources; `--date 2026-10-7` was still rejected (invalid format); mark_empty + export wrote a reviewed-empty note. (Correction: the two dates were separate invocations, not a slash range; and after the F2 hardening a no-source day leaves Collection == nil - it no longer fabricates an empty Complete summary.)
Open findings and disposition: F2 closed (Collect writes the summary only when sources are configured); F4/F5/F6 test gaps closed; F1 (bare review persists a record per date) intended; F3 (mark_empty does not validate Confirmed empty) pre-existing, carried.
Commit and publication status: committed locally 0e3010d; NOT pushed, no PR.
Vault sync status: complete.
Next gate and blockers: none pending; a production pilot requires a separately approved exact daily plan and live write authority. Production submission remains blocked.
```

```text
2026-10-11 / S8-FOLLOWUP-2 / submission-preview detail rendering -> independent review ACCEPTED -> hardening (S8F2-1/S8F2-2/doc/tests) ACCEPTED -> committed
Owner authority reference: direct-dispatch session grant; local commits of accepted slices only.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 8820895 (parent 06c25e6) / cmd/timesheet/main.go(+test); md5s matched the accepted manifest before commit (main.go 5e503b9ad9733f6476b5ee8424e2936a, main_test.go a868a45ccf87fda05395b17a3adc4311).
Evidence: @oracle independent review (ses_eda5e6395ffex0M34vLGCR7hYR) ACCEPTED (no introduced defects; S8F2-1..4 Low/Info); focused re-review ACCEPTED (S8F2-1, S8F2-2, doc nit, S8F2-4 closed; no new defects). Orchestrator CLI confirmation (rebuilt from the working tree): a 2-interval day (09:00-10:30 + 13:00-17:30) with `classifications {Proj: cls-1}` printed both `create … Proj <description>` lines and `total: 6h 0m across 2 operation(s)`; an unresolved-project day printed `preview blocked: unresolved_classification:c9` (exit 3). `plan_id:`/`operations:` remain byte-identical (e2e line-scan unaffected).
Open findings and disposition: S8F2-1 (label could mislabel when the classification map is non-injective) closed by labelling from the confirmed interval's own project via the plan op LocalID; S8F2-2 (unsanitized description) closed via oneLine; doc nit closed; S8F2-4 test gaps (edit suffix, (unclassified), midnight total) closed. S8F2-3 (`remote complete: false` unreachable in a non-blocked preview) carried as informational. Note: the preview `total` is the total of the plan operations (creates/edits); an interval already identical remotely is a no-op and excluded.
Commit and publication status: committed locally 8820895; NOT pushed, no PR, no merge.
Vault sync status: complete.
Next gate and blockers: none pending; a production pilot requires a separately approved exact daily plan and live write authority. Production submission remains blocked.
```

```text
2026-10-11 / CONFIG-COLLECT-1 / authorized -> implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: owner-authorized bounded configuration/collection fix for findings A (no configured sources) and B (local git unnecessarily depends on GitHub); local commit of the accepted slice; vault update. Not authorized: push/publish/merge, live collection, Keychain changes, production writes.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 68e0a37 (parent 612a5ea) / 12 files: internal/evidence/plugins/{git,git_test}.go; internal/run/{collect,collect_test,preflight,preflight_test,run_test}.go; cmd/timesheet/{main,main_test}.go; cmd/timesheet/timesheet.example.json; cmd/timesheet/timesheet.local-git.example.json (new); docs/agents/collection-config.md (new). md5s matched the accepted fix1 manifest before commit (.../opencode/config-collect-1-fix1-manifest.txt).
Worker/reviewer report references and verification level: @fixer CONFIG-COLLECT-1 (ses_ed6ee4f72ffehA4jJS3C6dm3so) implement + fix; @oracle independent review (ses_ed6e788f3ffezknTa2Ys2vVcpj) verdict "commit yes / production no" (F2 High); focused re-review verdict "all six findings closed; commit yes". Orchestrator gofmt/build/vet/tests/race green on the committed snapshot.
Behavior: Finding A - a run with no enabled evidence sources now prints exactly "No evidence sources configured." and is distinguished from configured sources returning no evidence; the collection-summary write guard keys off enabled-source count, so an all-disabled list cannot mark a prior incomplete collection complete. Finding B - the git source gains `mode` (local|remote|both, default both) and `local_repos` (explicit checkout paths, default "."); mode=local never contacts GitHub and needs no GitHub credential at preflight/run; when both are configured and GitHub fails, collected local evidence is retained and reported as partial (visible, gated by the existing missing-source acknowledgment), never silently complete. Local repo identity is `CanonicalRepoKey(RepoName, source id)` (no filesystem path in SourceRef).
Open findings and disposition: F1/F2/F3/F4/F5/F6 closed. Carried non-blocking: N1 exported `run.GitSourceEnabled` now has no caller (dead; misuse risk); N2 no direct unit test for the absent/invalid-mode credential fail-safe (verified by inspection); pre-existing CONTRACT-mode `fixture` requirement makes end-to-end `preflight`/`run` unusable from the shipped examples (documented, not fixed); remote-less local identity falls back to the source id (multiple remote-less checkouts under one source id would collide).
Commit and publication status: committed locally 68e0a37; NOT pushed, no PR, no merge.
Vault sync status: complete - project note and daily note updated.
Next gate and blockers: none pending; production submission remains blocked (a pilot requires a separately approved exact daily plan). Live collection remains unverified.
```

```text
2026-10-11 / PREFLIGHT-RUNTIME-1 / authorized -> implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: owner-authorized bounded runtime-contract fix (separate runtime source validation from fixture-based behavioral testing); local commit of the accepted slice; vault update. Not authorized: push/publish/merge, live service access, Keychain changes, production writes.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 4589598 (parent 4a16e9b) / 14 files: internal/evidence/{registry,registry_test}.go; internal/evidence/plugins/{git_test,calendar_test}.go; internal/run/{preflight,preflight_test,collect_test,run}.go; cmd/timesheet/{main,main_test}.go; tests/e2e_test.go; docs/agents/collection-config.md; .claude/skills/timesheet/{workflow,SKILL}.md. md5s matched the accepted fix1 manifest before commit (.../opencode/preflight-runtime-1-fix1-manifest.txt).
Worker/reviewer report references and verification level: @fixer PREFLIGHT-RUNTIME-1 (ses_ed6ee4f72ffehA4jJS3C6dm3so) implement + fix; @oracle independent review (ses_ed6aec615ffeyLR6B0Bak72yBV) verdict "commit yes / production no" (F1 Low introduced; F2 Low pre-existing); focused re-review "F1/F2 closed; commit yes". Orchestrator gofmt/build/vet/tests/race/e2e green on the committed snapshot.
Behavior: runtime source validation (new Registry.ValidateSources: config requirements + plugin CheckContract) is separated from fixture-based behavioral/determinism verification (VerifyContracts/PreflightContracts, now test-only). preflight no longer requires a fixture for git/calendar/agent sources; git mode:local passes preflight offline without a GitHub credential or network. The source check is renamed contracts -> configuration (RUN_PREFLIGHT_CONFIGURATION_INVALID); --deep validates ALL configured sources (disabled reported skipped) without fixtures; disabled sources never block (the credential loop now skips them) while enabled sources stay fail-closed. Skill workflow, CLI help, and docs are aligned to configuration semantics; preflight does not claim live source availability.
Open findings and disposition: F1/F2 closed. Carried Info: F3 (--deep reports a disabled unknown-type source as skipped, by design); F4 (skill/CLI parity is structural; wording hand-maintained); F5 (this record resolves the earlier stale ledger note - the dead run.GitSourceEnabled was removed in this slice). No fixtures added to production examples.
Commit and publication status: committed locally 4589598; NOT pushed, no PR, no merge.
Vault sync status: complete - project note and daily note updated.
Next gate and blockers: none pending; production submission remains blocked (a pilot requires a separately approved exact daily plan). Live source availability remains unverified and is exercised by review, not preflight.
```

```text
2026-10-11 / METRICS-1 / authorized -> design -> implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: owner-authorized bounded benchmark+metrics slice; local commit; vault update. Not authorized: push/publish/merge, live service access, Keychain changes, production writes.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 936910a (parent 340f7c2) / 33 files, +4727/-8: new internal/metrics/{models,adapter,adapter_pi,adapter_claude,attribute,duration,aggregate,store,report,compare,live}.go(+tests, testdata/*.jsonl); new cmd/timesheet/{metrics,metrics_test}.go; new docs/engineering/complexity.md; new docs/adr/0006-metrics-contract.md; edits cmd/timesheet/main.go, .gitignore, docs/timesheet-redesign.md, .claude/skills/timesheet/{SKILL,workflow}.md, internal/run/{synthesize,synthesize_test}.go, internal/workflow/models.go. md5s matched the accepted fix2 manifest (.../opencode/metrics-1-fix2-manifest.txt).
Worker/reviewer report references and verification level: @oracle design (ses_ed688c266ffeeo6ANIo5TAugIT); @fixer METRICS-1 (ses_ed684250dffer9X2ACZmo1QeVS) implement + 2 fix rounds; @oracle independent review (ses_ed66adef9ffeuHbAlwWsAV9jfv) verdict "commit yes / production no" (F2-F12), focused re-review "F2-F12 closed; commit yes", N1 re-review "N1 closed; commit yes". Orchestrator gofmt/build/vet/tests/race/e2e green on the committed snapshot.
Behavior: new additive internal/metrics (stdlib + internal/workflow only): per-run/per-turn/per-step token accounting (input/output/cache-read/cache-write/reasoning; unknown != zero; no Total field; provider context-length NEVER aggregated; reasoning excluded from consumed tokens); pi OBSERVED JSONL + claude DECLARED adapters; step + retry/rerun attribution (unassigned never guessed); duration model (monotonic live + transcript estimate; review-stopped = partial, never completed); idempotent per-run/per-day storage (content-hash no-op; byte-identical aggregate); table + JSON. CLI: metrics import|aggregate|report|compare|run. Editorial usage (record) and orchestration usage (transcripts) stay separate; editorial-input-to-step is the 3.5 optimization metric, distinct from the 1000/day budget; compare emits a percentage only with a valid nonzero known baseline. One additive cross-package change: workflow.EditorialUsage gains editorial_input/output/cache (SchemaVersion stays 1), populated in run.accumulateUsage. Sanitized reproduction of the handoff partial offline run reproduces the handoff TOKEN totals (input 24,924 / output 2,250 / cacheRead 118,633 / reasoning 855 / 8 calls / state partial-not-completed) because the fixture preserves the real per-turn usage objects; the fixture TIMESTAMPS are synthetic, so the E2E/ACTIVE/WAIT durations derived from it are fixture-derived and are NOT the measured handoff durations (do not claim timing reproduction). The `internal/git TestResolveAuthToken_KeychainMissFallsThrough` failure is a known pre-existing host failure (no `gh`), not introduced by this slice.
Open findings and disposition: F2-F12 closed; N1 closed. Carried: claude schema DECLARED-not-verified (no host sample); the store-open-failure branch of `metrics run` verified by inspection only; legacy benchmark.DefaultTokenCeiling=600 and the legacy extractTokens double-count left untouched (out of scope); RunBlocked/RunFailed reachable only via `metrics run`. No raw transcripts/HTML committed (.gitignore now ignores pi-session-*.html).
Commit and publication status: committed locally 936910a; NOT pushed, no PR, no merge.
Vault sync status: complete - project note and daily note updated.
Next gate and blockers: none pending; production submission remains blocked (a pilot requires a separately approved exact daily plan).
```

```text
2026-10-11 / ACTIVITY-CLASSIFICATION-1 / authorized -> discovery -> design (derived-property, owner-confirmed) -> implementing -> reviewing -> fixing -> re-reviewing -> accepted -> committed
Owner authority reference: owner-authorized bounded slice; targeted Keychain retrieval + live read-only catalog discovery; local commit; vault update. Not authorized: remote mutations, Keychain changes, push/publish/merge, production writes.
Branch / HEAD / exact snapshot: feat/s9-integrated-acceptance / 765fdc8 (parent dcc734a) / 27 files, +2305/-107: new internal/gradion/{catalog,catalog_test}.go, internal/selection/*, internal/submit/{catalog,catalog_test}.go, cmd/timesheet/{classify,classify_test}.go, docs/agents/classification-selection.md; edits internal/mcp/{jsonrpc,session_test}.go, internal/gradion/{client,parse,tools}.go, internal/run/{run,run_test,submit,submit_test}.go, internal/submit/{plan,plan_test,approval_test}.go, internal/workflow/{models,merge}.go, cmd/timesheet/main.go, tests/acceptance_test.go, .claude/skills/timesheet/{SKILL,workflow}.md. md5s matched the accepted fix1 manifest (.../opencode/activity-classification-1-fix1-manifest.txt).
Worker/reviewer report references and verification level: @explorer flow recon (ses_ed641e467ffeLSHOLIS0iDHeV3); orchestrator live read-only catalog discovery; @fixer (ses_ed6376a7cffeLAqGFqJeY5fgfq) implement + fix; @oracle independent review (ses_ed620d4f3ffe2j3FjKcARYpbnv) verdict "commit yes / production near-ready (F1)", focused re-review "F1-F8 closed; commit yes; production ready". Orchestrator gofmt/build/vet/tests/race/e2e green on the committed snapshot.
Verified contract: server gradion-workspace 1.0.0, protocol 2025-06-18, 71 tools; there is NO standalone activity-type catalog tool. `get_classifications` (props search, activityTypeId) returns structuredContent `{resources:[{kind,id,label}]}` (id + name only) AND a text block `- <Name> (<Code>) [<Activity Type>] · <uuid>` (the only source of code + activity type). `log_time`/`edit_time` accept only classification/classificationId — no activity-type field; the server derives activity type from the classification. Owner confirmed the derived-property model. First-run suggestion resolved live: `INTGRADI2610` = "Gradion Intern Academy 2026" with activity type "Internal Project".
Behavior: new gradion.ClassificationCatalog parses text+structured (join by id), marks Complete=false on the truncation header, the 20-cap, or a missing text block, and fails closed on unparseable text. Selection is explicit-only: the plan uses only ConfirmedInterval.ClassificationID; the config resolver becomes a suggestion (PlanResult.Suggestions) never applied; an unset id blocks with unresolved_classification and zero writes. Additive ConfirmedInterval.ActivityType feeds LocalFingerprint (changing the classification or its derived activity type invalidates approval); it is never sent to the server. CLI `classify list|select` (search, --json, --cache with provenance/freshness and a stale-cache refusal unless --force, explicit --all bulk, unambiguous id-prefix); non-secret recent preferences + catalog cache under ~/.timesheet; pre-submission ValidateCatalog blocks unverifiable/unresolved classification with zero writes.
Open findings and disposition: F1-F8 closed. Carried: F3 defense-in-depth (catalog validation is API-level opt-in; the CLI always supplies it; documented, no hard block added to avoid breaking unrelated callers); the CLI live-fetch path is not unit-covered (tests use --cache); the catalog cache is a single global file overwritten by a filtered list; `--force` affects selection only, never submission.
Commit and publication status: committed locally 765fdc8; NOT pushed, no PR, no merge.
Vault sync status: complete - project note and daily note updated.
Next gate and blockers: none pending; production submission remains blocked (a pilot requires a separately approved exact daily plan).
```

## Documentation review

On 2026-10-10 an independent documentation reviewer identified seven gaps. Corrected actor labels for orchestrator-only commits, explicit session vault authority, shared-workflow coverage, provisional remote limit/timezone assumptions, mapping ownership, review snapshot/independence fields, and significant-outcome daily logging. Corrections affect documentation only; S1 source review remains pending. Independent re-review of these documentation corrections has not been run.
