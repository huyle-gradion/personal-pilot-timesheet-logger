WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: unauthorized/duplicate remote writes, stale approval, crash-window replay, false confirmation
SCOPE: TIMESHEET-REWRITE-R4, exact-plan approval and verified submission
VERIFICATION: compiled persistent binary against stateful loopback MCP; explicit operator approvals and deterministic fault scenarios

# R4 owner-mediated packet

Status: **implemented through dev.12 and independently accepted within Linux/hermetic scope**. See `timesheet-rewrite-r4-acceptance.md`. This original requirements packet and the two repair packets are historical, not instructions to rerun implementation. R4 remains uncommitted pending its separate publication gate/PR; acceptance does not grant R5, child dispatch, merge, release or live access. Track issue #135; preserve PR #138's R1-R3 baseline.

## Authority and baseline

Continue on `feat/timesheet-rewrite-v0.1.0`. Original kickoff base: `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`; the accepted R1-R3 publication commit is the new execution baseline. Record the actual current HEAD before editing; do not require HEAD to remain at the historical kickoff base. Preserve all owner changes, accepted artifacts and prior fixes. One source writer; no child dispatch under current owner-mediated mode. State exposed capabilities, delegation permission and inline rationale once, without reading private configuration to discover tools.

Accepted R3 `/tmp/timesheet-r3fix2-20261023/`: binary SHA-256 `f865a46941da1e716feb6ebc06e4ab4015e8a8e69a0d42684904fc1e31febadc`; `evidence-manifest.sha256` SHA-256 `d09455e24603a38118eb76805e2bf43d2319daa269458640d308ad914b98f034`. Read the named manifest and source pins, not directory sweeps. Additional frozen planning/reader/store pins are recorded in the R3 acceptance and dev.8 reference. Capture selected-file sanitized rollback before editing; never archive real config/env/records or the whole tree.

Target `0.1.0-dev.10`, unreleased/untagged, protocol 1 with additive operations. Use state schema 3 for native approval/execution/intent/outcome data. Validate and preserve accepted rewrite schemas 1 and 2 through explicit in-memory conversion; write schema 3 only on a successful durable transaction. Never promote historical legacy approvals or old always-false plan flags into authorization. Document upgrade/old-loader failure behavior; no silent downgrade or destructive startup rewrite.

No commits, push, publication, tags, main-branch changes, actual credentials/config/store reads, Keychain, ai-database or live provider calls. Remote mutations are allowed **only against the private stateful loopback fixture after explicit synthetic approval**. A live submission needs separate named authority and exact daily approval, neither granted here.

## Exact files and capsule

New implementation: `internal/rewrite/approval.go`, `internal/rewrite/submission.go`.

Integration writes: `internal/rewrite/{gradion,plan,model,store,session}.go`; `docs/agents/rewrite-interface.md`; `CHANGELOG.md`. `cmd/timesheet-rewrite/main.go` may change help/usage only. Optional focused tests `internal/rewrite/{approval,submission}_test.go`; existing R3 `plan_test.go` may change only where the genuine public plan contract changes (for example the now-previewed edit audit reason). Configuration/credential precedence, collectors, migrations, old packages, generic MCP transport, dependencies and frozen R1/R2 tests are read-only. A concrete missing write dependency must be reported, not silently added to the scope.

Read ranges/functions first, only in these named files:

- This packet; `docs/orchestration/README.md:5-24`; `docs/orchestration/timesheet-rewrite-r3-acceptance.md`; `docs/timesheet-rewrite-spec.md:96-106,108-126`.
- Existing rewrite integration files above: plan construction/hash/readiness, source revisions, saved/session views, allocation bindings, remote observation/catalog data, state validation/cloning/commit and dispatch. Do not load whole historical reviews/transcripts.
- Existing read-only boundary `internal/rewrite/gradion.go`; reusable `internal/mcp/session.go` and `internal/mcp/jsonrpc.go` (scoped navigation, unchanged transport).
- Legacy precedents, read-only only: `internal/submit/approval.go:31-137`; `internal/submit/execute.go:68-91,109-220,296-406`; `internal/submit/resolve.go:43-86,89-130`. Their revision/hash and approval-ID rules are NOT the rewrite contract: exact revisions must bind, and new approvals must not reuse a consumed ID just because plan content repeats.
- Wire contract: `internal/gradion/client.go:441-573`; `internal/gradion/parse.go:102-196`; `internal/gradion/tools.go:17-40`; `docs/gradion-mcp-verification.md:53-82` for declared/verified limits only. Older Keychain instructions elsewhere are superseded; do not follow them.
- Public rewrite interface sections needed for request/receipt/error integration. No new external SDK or dependency required.

No repository-wide search, raw provider exports, real dotenv/config/records, host Git settings, account discovery, journal or vault. No shell `cat`, `ls`, `find`/equivalent out-of-scope discovery. Use scoped symbol navigation/LSP if actually available. Do not rerun broad suites or the known host-sensitive Git failure.

## 1. Native approval, not a historical flag

Implement local operator protocol operations:

- `approve_plan{day,plan_id}` — explicitly approve the exact rendered, current ready plan; return an opaque unique `approval_id` plus the bound plan/revision summary.
- `revoke_approval{day,approval_id}` — cancel a not-yet-dispatched native approval locally; never undo remote effects.
- `submit_plan{day,approval_id}` — explicit request to execute only that approval's exact plan, never a bare date/current-active-plan guess.
- `submission_status{day}` — bounded approval/execution/outcome/blocker receipts, including opaque write references for unresolved native or migrated outcomes.
- `resolve_write{write_ref,observation_id,decision,remote_id?}` — explicit local recovery described below; never a remote write or implicit resubmission.

The development private stdio channel is trusted operator transport; a scripted request is not proof of physical human presence. Do not add a fake `human:true` security flag. R5 will remove these operations from model capabilities and provide local human UI. No AI inference, source acknowledgment, work confirmation or plan approval is performed automatically here.

Approval requirements:

- Require a canonical immutable ready plan, saved confirmed work, complete fresh relevant remote observations, no unresolved writes and current explicit classifications. Revalidate at approval and again immediately before consumption/dispatch.
- Bind exact contributing/source revisions, allocation identities, canonical operation content, relevant remote-content/coverage preconditions and configured remote authority context. Reject changed source work, newly staged spill work touching affected dates, dirty/unsaved affected sessions, changed plan/body/reason, stale/incomplete remote data or changed classification proof with zero mutation calls.
- Bind the effective endpoint/credential context without storing or printing its raw values (an opaque approval-scoped context digest is acceptable). Effective endpoint/credential changes must invalidate approval even if two providers happen to return identical entry data. Do not introduce another credential lookup or claim a digest proves a user's real-world identity.
- Approval IDs are unique occurrences, not PlanID aliases. Historical consumed/revoked approvals are immutable audit. Reapproving identical plan content after a resolved failure creates a new approval ID; it cannot overwrite or reactivate the old approval.
- No automatic submission on approval, startup or restart. A persisted native unconsumed approval can be used only by a new explicit submit request that passes every current precondition. Imported legacy approvals remain inert forever.
- Keep immutable plan content separate from the native approval ledger. Remove obsolete always-false persisted R3 authorization flags through the schema-2 conversion; any local UI approval status is derived from real ledger state, never a second authority flag.

## 2. Exact write payloads and fresh classification proof

Preserve the R3 reader's two-read-tool boundary. Add a separate narrowly scoped writer path; only an eligible submit can reach `timesheet__log_time` or `timesheet__edit_time`. No generic tool-call operation, delete, REST fallback, write retry, automatic splitting or hidden payload expansion. Read-only commands still perform zero remote mutations.

Use the known tool argument contract: create sends approved date/startTime/endTime/description/classificationId; edit sends the bound entryId, required bounded audit reason and only approved changes. Do not send derived activity type. Preserve remote-only fields by omission; unknown billable must not become false. If any optional field is intentionally sent, render and hash it in the preview before approval.

R3 plans lack edit reason. Add a bounded deterministic audit reason (date/local identity) to edit previews and canonical operation content before approval, using the existing repository convention where applicable. It is part of the exact body; do not invent/change it after approval or add an unnecessary model/configuration lane.

Freshly validate each selected classification against authoritative current catalog results, reusing stored selected option/code/name metadata only as retrieval hints. A fresh partial result can prove a returned ID exists but cannot prove a missing ID absent. Never silently swap IDs by name, apply a project default, trust a pruned/missing old catalog as proof or send a cached catalog as live truth. A relevant classification/derived activity-type change requires explicit reselection/new preview, not hidden mutation of an approved body. Deduplicate identical read queries within a request; no model calls.

Make the exact preconditions/choice proof relevant to write intent part of the plan/approval identity while excluding audit-only timestamps. An identical fresh observation must remain stable; meaningful content/choice changes must invalidate. Keep all per-allocation midnight/date identities and R3 F1-F9 guards. Do not assume unverified live timezone, midnight syntax or duration policy; an unsupported projection must block before dispatch, never shorten time or create unapproved fragments.

## 3. Durable intent before any mutation

Use the existing exclusive store writer and atomic temp/rename/directory-sync path. No parallel side-effect path or separate unprotected intent file.

Before dispatching each operation, durably record its intent: unique write reference, native approval/execution ID, exact operation/body digest and expected remote preconditions, local/allocation/source identities and attempt state. Consume/mark the approval executing in the same protected transition so a crash cannot leave reusable authorization for an already dispatched operation. Do not dispatch until durability is confirmed; pre-rename failure yields zero mutation calls, post-rename uncertainty blocks dispatch and further persistence claims under the existing sticky semantics.

Execute the complete approved operation list in deterministic order. Do not silently narrow multi-operation plans. Account only for verified self-induced remote changes when checking the next operation; do not invalidate a running batch merely because its own confirmed create added the expected remote ID. Conversely, do not replace expected remote state with an unexplained fresh snapshot and continue through outside changes. Stop on any failure/uncertainty or unexplained drift; record completed versus unattempted work honestly.

Each eligible operation is dispatched at most once per explicit approval attempt; never automatically retry a write. A partial run cannot reuse its original approval to replay either completed or remaining operations. After resolution, derive a fresh remaining plan and require a new approval. Startup/restart never resumes a batch automatically. In-flight intent or interrupted execution must block replay until reconciled, even if a prior operation succeeded.

## 4. Verify outcomes, do not echo plans as facts

After a write, use acknowledged stable identity plus a bounded fresh read-back to verify actual content before confirming or creating/updating a binding. A full structured response is useful observed evidence but not permission to skip the required read-back in this slice. Verify intended date/span/classification/description and any intentionally sent fields; preserve remote-only fields without inventing unknown values. Record the scope of what could actually be observed.

- Identity-only acknowledgments are not confirmed writes. Missing/ambiguous IDs are unidentified; never infer identity automatically from a unique-looking time slot or request echo.
- Timeout/reset/5xx, malformed response, tool-level error without conclusive no-effect proof, absent/incomplete read-back or content mismatch remains uncertain/unidentified. Preserve any acknowledged remote ID, the exact attempted intent and stable reason codes. Stop; do not retry, repair or delete remotely.
- Treat an operation as definitively failed/not dispatched only when the evidence establishes that classification. Do not turn a generic error into permission to replay.
- Confirmed outcomes and mappings use verified observed identity/content, not the planned state copied into observation fields. Persist before proceeding. Failure to persist a post-write outcome leaves a durable unresolved intent and stops further dispatch; never return overall success because the HTTP write looked successful.
- Receipts distinguish all-confirmed, partial, stopped/unresolved, blocked-before-dispatch and local durability uncertainty; include compact counts/IDs and next safe action, not credentials, authenticated URLs or raw provider documents.

Remote constraints matter: the known service has no declared idempotency key or conditional-edit CAS. Fresh checks cannot eliminate the observation-to-write race or prove exactly-once remote effects. Document that residual; never claim the local lock makes remote check/write atomic.

## 5. Explicit recovery, not a replay switch

`submission_status` must expose stable opaque write references for unresolved native intents and migrated outcomes, preserving original audit. `resolve_write` binds a specific unresolved reference to a current complete read-only observation and explicit operator decision:

- `adopt`: require the selected observed stable ID and matching intended content, one-to-one allocation identity, and no theft/rebinding of another active allocation. For an edit, retain its intended remote identity. Record human resolution and verified mapping/outcome without dispatching anything. Insufficient legacy intent/identity information remains a visible blocker, not guessed proof.
- `not_applied`: explicit owner/operator attestation against reviewed complete observations; a bare absence is never automatically proof a create did not apply. Reject contradictory matching effects/ambiguity. Preserve the original uncertainty/reason and record the new decision as audit; do not silently erase history.

These actions clear only the addressed, validated blocker. They do not approve a new plan, reactivate spent approval, skip other unresolved rows or trigger a write. Existing R3 adoption/foreign/content acknowledgment must not bypass this gate. Support valid migrated unresolved histories as well as native interrupted intents; never treat ordinary confirmed/failed/skipped history as pending. Do not add a blanket clear-all flag.

## Runtime acceptance — real binary and stateful loopback MCP

Build once after edits, scoped formatting/vet/tests once. Tests are optional and must target plausible consumer-visible boundaries, not wiring/copy echoes/source text/incidental defaults. No `go test ./...`, no host-sensitive legacy Git rerun or host-profile repair. Do not rerun prior broken binaries simply to confirm already reported failures.

Use generated private config/env/state/legacy fixtures, minimal environment and a loopback MCP provider implementing real mutable synthetic entry state. It may declare/read the two read tools and the necessary create/edit tools; count every mutation and capture the exact bodies. No real accounts or endpoints. Keep one persistent Go process for phases until deliberate restart/crash probes.

Prove at least:

1. No approval, wrong/revoked/spent approval, blocked plan, dirty/new spill work or changed source revision/body/remote/classification/context produces zero write calls. A ready preview and historical migrated approval alone never authorize a write.
2. An explicit exact approval survives only unchanged preconditions. Successful create and edit verify actual read-back, persist per-allocation mappings and history, and restart does not duplicate them. Sent edit reason/body exactly match the preview; remote-only fields are preserved.
3. Consumed approval cannot be reused, including restart. Identical content may be reapproved only with a distinct fresh approval after a new eligible preview; old audit stays intact. Revoke-before-submit causes zero writes.
4. Pre-dispatch intent-write failure causes zero mutations. Demonstrate the real CLI path (for example private state-parent unavailability), not only an in-process hook. Existing directory-sync uncertainty semantics remain; do not add a production fault flag.
5. Identity-only acknowledgment with matching read-back confirms; identity-only/malformed acknowledgment with missing or mismatched/incomplete content never falsely confirms. Applied-then-reset/timeout is attempted once, remains blocked after restart and is never retried automatically.
6. Kill/interruption after durable intent and remote effect but before outcome persistence leaves a restart blocker and spent execution; no automatic or old-approval replay. A multi-operation run records confirmed/uncertain/unattempted parts accurately and stops at the first unresolved point.
7. A normal multi-operation batch completes its already-approved operations without treating its own verified effects as external drift; unexpected outside changes stop later operations. Remaining work needs a new preview/approval, not an invisible plan rewrite.
8. Explicit recovery of native and migrated unresolved writes performs zero mutation calls, preserves audit, clears only the addressed blocker, and still requires new approval for remaining work. Foreign/adoption cannot evade unresolved-intent gates.
9. Schema-1/2 work/history/evidence/migration/remote data survives schema-3 conversion and restart. Old approvals stay inert; source archives and accepted artifacts are unchanged. Protocol/version/status and actual supported schemas agree.
10. Mutation inventory is confined to the approved exact Gradion operations. No credentials, host identities, authenticated URLs or provider raw documents in retained output/state. Read-only/manual/no-Gradion workflows remain read-only; no model is needed for any approval/submission decision.

Retain selected source rollback/final copies/diff/hashes, actual compact protocol receipts, state-transition assertion summary, provider mutation/read inventory, binary/version/schema identity and qualified check provenance in a NEW sanitized `/tmp/` review directory. Remove throwaway fixtures/drivers and stop all processes after capture. Do not copy the old unsanitized dev.8 report or claim prior suites as re-executed.

Stop review-ready for independent review, not self-accepted. No R5, commits, publication, direct child dispatch or live submission. Any missing production proof remains explicitly unverified; a hermetic pass is not a production-write grant.
