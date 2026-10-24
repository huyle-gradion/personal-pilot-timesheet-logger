# R4 independent review — dev.10

Historical dev.10 verdict. Superseded by [qualified dev.12 acceptance](timesheet-r4-acceptance.md); the findings below remain evidence of the reviewed older snapshot, not current repair instructions.

Verdict: **Standards pass in the reviewed scope; Spec fail. R4 not accepted.** Six behavioral blockers are independently reproduced on the compiled binary. The implementation's optional test suites do not satisfy the packet's standalone runtime matrix. R5 remains blocked.

## Authority and identity

Owner-mediated independent review of issue #135 and `timesheet-r4-handoff.md`; no children dispatched. Work stayed inline because direct dispatch is not authorized. No application source edits, commits, push, merge, live account access, private credentials/config/store/transcript reads or production mutations.

- Published baseline: `496a6ab8a81e9418a25046995ae40e2b8713e018` on `496a6ab8a81e9418a25046995ae40e2b8713e018`. PR #138 contains accepted R1-R3 only and remains separate from R4.
- All 13 entries in `/tmp/timesheet-r4-20261023/post-edit.sha256` match current source. The observed tracked deltas plus four new approval/submission source/test files are within the packet's write scope. Main entry-point changes are usage text.
- Binary SHA-256: `024a225c8b97cb347d7797c7cd5c3dd15186cfb4d544b7e1a86a053a057c7e9a`; actual version: `timesheet 0.1.0-dev.10 (protocol schema 1)`.
- Worker-reported formatting/build/vet/tests are attributed, not independently rerun. No broad legacy suite or known host-Git failure was rerun.

## Standards

No independent hard Effective Go violation identified in the reviewed changes. The explicit reader/writer boundary, cloned durable candidates and one-dispatch path are consistent with the accepted architecture. TigerStyle and historical Go rules are not mandatory. Comments and passing tests do not override the behavioral findings below.

## Spec

### R4-F1 — P1: changed selected classification semantics do not invalidate approval

Location: `approval.go:389-477`; `submission.go:659-695`; `plan.go:1058-1127`.

The proof only establishes that the selected ID appears. Approval records catalog identities, but submission never compares current proof with the approved selected option. The plan hash omits classification choice semantics. A catalog/type change before approval also need not invalidate the previously rendered selection.

Observed: select `cat-1` as Development, approve, then return the same ID/code/name with activity type Meeting. The parser reports `activity_type: Meeting` and a changed catalog identity. Submission still performs one create and reports `all_confirmed`.

Required: bind relevant selected-option semantics to explicit selection/preview/approval and compare fresh proof before dispatch. Reject a relevant name/code/activity-type change pending explicit reselection/new preview. Do not use an entire query/catalog hash as the sole semantic identity: unrelated options, query hints and audit timestamps must not spuriously invalidate unchanged choices. Packet §§1-2, especially lines 55 and 69-71.

### R4-F2 — P1: new dirty spill origins are outside the approval guard

Location: `plan.go:706-725`; approval/submit re-render callers.

The dirty-session check only visits origins already contributing saved work. It misses open staged/unsaved origins that newly allocate into an affected date.

Observed: approve saved Oct 20 09:00-10:00; open Oct 19 and stage unsaved 23:30-00:30. Submit Oct 20 still performs the approved create and returns `all_confirmed`. The spill is non-overlapping; this tests the required dirty-affected-date guard, not overlap validation. An overlapping staged addition was correctly rejected by the existing overlap guard and is not the failing case.

Required: inspect affected saved, staged and unsaved-confirmed allocation/date footprints, including removal/move of existing contributions. Reject dirty origins touching the plan dates at both approval and consumption. Unrelated dirty dates may remain usable. Packet line 55 and runtime case 1.

### R4-F3 — P1: content changes of existing remote entries are absorbed mid-batch

Location: `submission.go:761-787,1282-1297`.

Drift checks compare appearance/disappearance of IDs, but not all existing entry content. Only the current edit target gets a content comparison. A changed existing entry can overlap a later create without stopping it.

Observed: baseline `outside-1` is 16:00-17:00; approve creates at 09:00-10:00 and 11:00-12:00. During the first effect, change `outside-1` to 11:00-12:00. The first required readback exposes that change before operation two, but both creates run and the receipt is `all_confirmed` with two confirmations. This is not the unavoidable observation-to-write race: the conflicting content was already observed before the later dispatch.

Required: compare the complete relevant fresh content against consumption baseline plus exactly verified self-effects, including prior self-effects, before continuing. Stop on unexplained changes; never silently narrow or reapprove the batch. The ordinary two-create control succeeds. Packet line 79 and runtime case 7.

### R4-F4 — P1: an edit can confirm using another entry's acknowledgment/readback

Location: `submission.go:538-560,1327-1365`; acknowledgment parser at 248-287.

Verification accepts any acknowledged ID whose readback content matches the requested fields. It does not require an edit's acknowledgment to identify its intended bound remote ID. The binding update retains the old ID while storing the other entry as `LastObserved`.

Observed: the exact edit targets `created-1`; the provider acknowledges `unrelated-ack-id`, with matching content present in the readback. Receipt and intent say confirmed against `unrelated-ack-id`; durable binding still says `remote_id: created-1` but `last_observed.remote_id: unrelated-ack-id`.

Required: reject mismatched edit identity as unresolved; do not update a binding or report confirmation. Preserve attempted and acknowledged IDs as distinct audit. Adoption must retain the original edit identity. Keep acknowledgment parsing fail-closed, including the documented entry resource kind, not any nonempty kind. Packet lines 85-90 and 99.

### R4-F5 — P1: migrated spill uncertainty does not gate approval/submission

Location: `plan.go:291-300,836-839`.

Migration outcomes are filtered by contributing origin dates, while an outcome may carry its allocation date. A spill-day unresolved row is visible in `submission_status` yet absent from readiness checks for that same spill plan.

Observed: migrate Oct 19 23:00-01:00 plus an unresolved create for Oct 20 00:00-01:00. With an empty complete Oct 20 observation and explicit classification, the spill preview is ready; approve/submit creates it and reports `all_confirmed`. `submission_status` still lists `mw-spill-uncertain`. Unknown prior effects must block even when the current read is empty.

Required: scope migrated and native pending histories by affected allocation dates and verified origin/local identities, not origins alone. Prevent both approval and submission through an unresolved row; preserve ordinary terminal history behavior. Packet lines 54, 81 and 102; runtime cases 1 and 8.

### R4-F6 — P2: valid migrated spill recovery cannot find its allocation

Location: `submission.go:1674-1685`.

Adoption looks for an interval physically stored in `Days[outcome.Date].Confirmed`. Spill allocations belong to an interval stored on an earlier origin day.

Observed: the same real migration preserves local ID `legacy-spill` on Oct 19 and an uncertain Oct 20 allocation. A complete Oct 20 observation contains exactly matching stable ID `actual-spill`. `resolve_write(adopt)` returns `unknown_local` because no origin record is stored under Oct 20; zero writes occur and the legitimate blocker remains unresolved.

Required: resolve the actual `(origin interval ID, allocation date)` contribution through existing allocation helpers, respecting timezone and crosswalk identities. Validate one-to-one identity without requiring an independent spill-day record or guessing insufficient legacy intent. Packet lines 99 and 102; runtime case 8.

## Independent compiled-binary evidence

Artifact directory: `/tmp/timesheet-r4-review-5ue9kmwy/`.

- `boundaries.json`: SHA-256 `93b3ba820056a3481cc567e46c6d6a3408e4be90b95bd4bcb9cd7c0e61f6d50e`.
- `protocol.json`: SHA-256 `7d09eefe08bca2bda251ac6eba9df056005336374133cd5c5673a79cd2f09733`.
- `provider-inventory.json`: SHA-256 `acd5842e597dc9bafd1a01a92900e02f4850834995df08eb9b55dde11d7b7294`.

Ten named scenarios across ten private child/provider fixtures: six failing boundaries above, plus four passing controls (identity-only acknowledgment with matching create readback and restart/spent rejection; normal two-create batch; applied-then-reset with restart/no replay; actual Go-child kill after remote effect before acknowledgment with durable in-flight restart blocker/no replay). Only the two declared read tools and create/edit mutations were used. Each mutation was synthetic and followed an explicit protocol approval. Legacy fixture source bytes remained unchanged. All child/server processes were stopped; generated private config/env/state/legacy fixtures removed. Actual sanitized protocol receipts, compact provider inventory and reviewed binary remain.

This is independent boundary evidence, **not completion of all ten packet runtime groups**. Still required from the repair: full standalone compiled-binary matrix, especially real CLI pre-intent persistence failure, positive edit/exact reason and remote-only preservation, revocation/context/revision/choice invalidation, native and migrated recovery, and schema-1/2 preservation through schema-3 activation/restart. In-process hooks/tests can supplement but not replace those cases. macOS/live/Pi remain unverified.

## Disposition

R4 remains blocked; issue #135 stays open. First bounded repair packet: `timesheet-r4-fix-handoff.md`. No R4 commit/PR, merge of #138, R5 work or production authorization is granted by this review. Repair findings, then return a fresh sanitized snapshot and standalone evidence for independent re-review; do not self-accept.
