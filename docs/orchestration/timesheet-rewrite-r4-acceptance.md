# R4 qualified acceptance — dev.12

Verdict: **Standards pass; Spec pass within the reviewed Linux/hermetic scope. R4 accepted.** The second repair converged. R4-F1 through R4-F8 and evidence qualification V1 are closed for the pinned snapshot. No owner/technical escalation is needed for a third repair because no reviewed acceptance blocker remains.

This is source/runtime acceptance of exact native approval, durable intent, verified synthetic submission and explicit recovery. It is not product completion, macOS/Pi/live-provider readiness, a release, a GitHub merge or production-write authority.

## Authority and reviewed identity

Owner-mediated independent review of issue #135. The directing reviewer worked inline: no child-dispatch authority was granted. No application source changes, commits, push, merge, release/tag, live calls, private dotenv/config/store/transcript reads or vault writes were performed by this review. PR #138 remains the separate accepted R1-R3 publication; its branch was not pushed or its contents updated.

- Branch: `feat/timesheet-rewrite-v0.1.0`; published baseline `496a6ab8a81e9418a25046995ae40e2b8713e018`. Accepted R4 changes remain uncommitted and unpublished.
- Worker snapshot: `/tmp/timesheet-r4-fix2-20261023/`.
- Binary SHA-256: `652882d47a763818eb0455135229b18035963b4c5c5fab5dd319e4f257a1392e`.
- Executed version: `timesheet-rewrite 0.1.0-dev.12 (protocol schema 1)`; state schema 3, legacy rewrite schemas 1/2 read through explicit in-memory conversion.
- Outer `evidence-manifest.sha256`: **142 entries independently verified**, SHA-256 `2dd4731a59c4d7387b649dd85d59dd713de5cb7d9702fdcac170718498638394`.
- Inner `source-snapshot/source.sha256`: **13 entries verified**, SHA-256 `82a39aeaaa6c12acf7f28b90e7c71b9b6d6401c50050e27b509c81e51267e467`. Every complete final source copy matches the current repository file.
- `rollback.sha256`: **9 entries verified**, SHA-256 `1dcfb0b34076ab226885986f5b196086cf3b51cc1e154a26f42deafc4b18fa3a`.
- Against the independently frozen dev.11 capsule, only the permitted plan/approval/submission fixes, model version constant, relevant tests, interface and changelog changed. Other captured integration files remain byte-identical. No new authority marker, schema field or dependency was added for empty proof.

## Standards

No hard Effective Go violation identified in the reviewed changes. The repair uses the durable store transaction path, an explicit persistence-result branch and the existing JSON map representation rather than a second authorization flag. TigerStyle/historical Go rules are not mandatory. Optional behavior tests and runtime evidence supplement review; a passing suite alone does not establish acceptance.

## Findings disposition

| Finding | Final disposition |
| --- | --- |
| F1: classification semantics | Closed in dev.11: selected relevant choice enters plan/approval identity and is compared with current authoritative proof. Relevant change blocks; missing proof is not inferred. Relevant repair code unchanged in dev.12. |
| F2: dirty saved/current allocation footprint | Closed in dev.12: guard resolves durable saved work with its own timezone in addition to current confirmed/staged work. Final removal plus unsaved confirmation blocks approval and consumption, including origin-owned spill allocations. |
| F3: existing/self remote content drift | Closed in dev.11: expected content advances only by verified effects; observed outside changes stop later operations. Repair code unchanged in dev.12. |
| F4: edit acknowledgment identity | Closed in dev.11: edit confirmation retains the approved bound ID; a different acknowledgment stays unresolved without cross-ID binding updates. Repair code unchanged in dev.12. |
| F5: migrated spill unresolved gate | Closed in dev.11: pending history is scoped to affected allocation/origin relevance, not origin dates alone. Repair code unchanged in dev.12. |
| F6: origin-owned spill recovery | Closed in dev.11: actual allocation, migration linkage and one-to-one identity are checked; valid recovery performs no write. Repair code unchanged in dev.12. |
| F7: false zero-operation durability success | Closed in dev.12: pre-activation consumption failure returns `persistence_failed` / `write_failed`, no execution ID or durable-consumption claim, approval active. A later explicit submit is required; sticky post-rename uncertainty remains distinct. |
| F8: empty proof lost on restart | Closed in dev.12: `choices: {}` persists as an established empty map; absent/null proof remains nil and unauthorized. No parallel authority marker or historical backfill. |
| V1: proof provenance and manifests | Closed: earlier G4 correctly attributed to preview persistence; independent genuine CLI intent-failure evidence retained; rich schema-1/2 activation/restart comparisons completed; new manifests have no self-entry and directly pin all thirteen sources. |

The public receipt addition is `persistence_failed` for failed-before-activation zero-operation consumption. Protocol 1 and state 3 remain unchanged. R5 must render this as non-consumption, not success; it must not retry automatically. Explicit empty proof is a serialization distinction, not a new authority field. Older rows missing proof remain unusable and require new explicit approval. No downgrade execution was tested or promised; the change is in encoding, not a new decoder capability.

## Worker evidence — inspected, attributed

The dev.12 snapshot retains **114 passing assertion records / zero recorded failures**, across nine changed-path/missing-proof scenarios. The reviewer verified artifact identity, inspected the scenario implementations, actual compact receipts and rich state-preservation assertions; this does not mean the reviewer reran the worker's harness or static gates.

- Dirty final removal before approval/after approval; origin-owned spill removal; save-empty and restart; unchanged/unrelated-dirty/staged-spill controls.
- Real compiled-binary no-op consumption failure at the fresh catalog barrier after preview persistence; honest failed receipt, explicit retry, restart and replay refusal.
- Current empty-proof restart/consume-once; absent/null legacy proof refusal with audit preserved.
- Rich schema-1/2 startup hash non-mutation, explicit save activation to schema 3, actual work/history/evidence/migration/remote/plan comparisons and restart; historical approval inertness.
- Worker-reported scoped formatting/build/vet/package tests are retained as attributed command evidence. Four before/after regression tests and sticky post-rename in-process evidence remain labelled as worker/in-process, not reviewer CLI output.

The earlier dev.11 121-row matrix remains qualified prior evidence for unchanged paths, supplemented by independent dev.10/dev.11 reviews. It was not rerun or added to the dev.12 count. No broad legacy test run or host-Git failure was rerun here. An earlier G9 label/status-only check is superseded by actual rich activation proof; the earlier G4 label is not reused as intent-boundary proof.

## New independent compiled-binary proof

Reviewer artifact directory: `/tmp/timesheet-r4-final-review-7_zm70pg/`.

- `boundaries.json`: SHA-256 `07986a6a66518a0ba03302b3b5e08ff7c10d682e2063fd42c90dcbbbb3f3edd4`.
- `protocol.json`: SHA-256 `3871d6b6fcd7ad903c90f5c09552cd49e7fce28aad9e84b5989d0e2ac45205bf`.
- `provider-inventory.json`: SHA-256 `e2096c36c15881ce45d7398198cc88a7f57b7563558d4ab1cbea654047de4541`.
- Full source capsule `source.sha256`: SHA-256 `84ea9d29c98578b723c7c406e7ad790224754afdb6d99c146ecdbdee41984102`.

Seven named scenarios across four private persistent child/provider fixtures:

1. Combined plan with ordinary work plus a prior-day saved spill. Remove the spill's final interval and confirm without saving: both old approval consumption and approval of the saved preview fail `dirty_session`; zero writes, saved interval intact.
2. Save that empty origin: old combined approval becomes `stale_plan`. A new exact one-operation approval executes only remaining ordinary work. Restart preserves origin revision 2 with zero minutes.
3. Create three unique zero-operation approval occurrences, clean restart, then obstruct the generated state parent at the fresh catalog barrier. Consumption returns `persistence_failed`, `write_failed`, no execution ID; durable approval still active and durable execution count unchanged. Setup create counted separately; failure adds no mutation.
4. Restore the fixture directory, clean restart and explicitly consume the same approval: durable success with zero operations. Another restart refuses replay; exactly one zero-operation execution was added.
5. Alongside that valid consumed approval, inject absent and null proofs into the two other synthetic approval rows. Both fail `stale_approval` with zero mutation delta. Missing proof is never promoted to empty proof.
6. Reuse the worker's rich schema-1 synthetic input but execute a **new unrelated-day 45-minute save**, rather than its harness scenario. Startup bytes unchanged; old days/history/evidence retained; schema 3 activated; restart state identical and new day renders 45 minutes. Zero provider mutations.
7. Do the same independent unrelated-day activation with the rich schema-2 input. Preserve old days/history/evidence/migration/remote/plans, apart from intentionally stripped obsolete false plan-authorization fields; restart state identical. Zero provider mutations.

All assertions came from the actual compiled private JSONL child and generated mutable loopback provider. Fixture filesystem changes were private test setup, not production fault switches. The reviewer source did not change application code. All children/providers stopped; generated config/env/store/legacy fixtures removed. Retained outputs are synthetic protocol evidence, compact inventory, proof summary and selected source/binary copies, not owner data or credentials.

## Boundaries and next gate

- Linux/hermetic proof only. macOS runtime, live Gradion/GitHub/calendar, Pi UI/tool restrictions and model-provider integration remain unverified.
- The private development stdio channel is trusted operator transport, not proof of physical human presence. R5 must keep approval/submission/recovery capabilities out of model tools and provide local human controls.
- No declared provider idempotency key or conditional-edit CAS: bounded fresh checks and local intent do not establish remote exactly-once effects or atomic check/write. Uncertainty still requires explicit reconciliation; no automatic retry/delete.
- Existing remote time syntax, midnight/timezone and provider policy are not live-qualified. No live production readiness claim follows from synthetic acceptance.
- Issue #135 becomes **source-accepted, pending a separate implementation PR/merge**, not closed. PR #138 must not acquire R4 changes; no merge authorized by this record.
- R5 prerequisites are now source-accepted. R5 implementation is **not released** by this acceptance. Next gates are R4 publication through its separate PR under publication authority, then a bounded R5 packet with verified pinned upstream Pi APIs and explicit owner release. No automatic child dispatch, implementation, credential access, live writes, release or vault synchronization.
