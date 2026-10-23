WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: stale removed-work authorization and false durable consumption receipts
SCOPE: TIMESHEET-REWRITE-R4-FIX2, F2 continuation, F7/F8 and evidence V1
VERIFICATION: compiled persistent binary; actual filesystem failure at consumption boundary; restart and rich schema preservation

# R4 second bounded repair packet

Status: **executed as dev.12 and independently accepted; second repair converged**. See `timesheet-rewrite-r4-acceptance.md` for source pins, actual proof and qualifications. F2 continuation, F7/F8 and evidence V1 are closed; no third repair or technical adjudication is needed. Historical packet, not a fresh-start instruction. No R5, direct child dispatch, commit/publication, merge, release or live-access authority is created by this acceptance.

## Authority and baseline

- Branch `feat/timesheet-rewrite-v0.1.0`; published HEAD `496a6ab8a81e9418a25046995ae40e2b8713e018`. Preserve all uncommitted dev.11 R4 work and owner governance changes. Do not revert to HEAD or the historical kickoff. One source writer, inline; no child dispatch. State capability/delegation decision once.
- PR #138 is the frozen accepted R1-R3 publication. Issue #135 tracks R4, which still requires independent acceptance and a separate future PR. No commits, push, publication, tags, merge or release.
- Reviewed dev.11 binary: `/tmp/timesheet-r4-fix1-20261023/binary/timesheet-rewrite`, SHA-256 `560e59388066d7cb4b34453deb003eab6d7a11f7b420a716fb56e390c51bffbb`.
- Worker outer manifest `/tmp/timesheet-r4-fix1-20261023/evidence/evidence-manifest.sha256`: 165 verified entries, SHA-256 `8794b55f3ce5f769f07e94ed65f2f3901eeaf9a8b9b563798d11f2747bbab421`. Its nested source manifest has a stale self-entry: do not edit it or claim it verifies. Source identity was separately checked.
- Complete frozen reviewer source capsule `/tmp/timesheet-r4fix-review-vu0ku8l3/source/` with `source.sha256` SHA-256 `2ef5d2c84cf19685091fca33bb3427b044e40c5acb946b9f5d1891d1e2f5302b`. Reviewer boundaries SHA-256 `b862cce2c29ce60bbc293ee9f286df04376fc0c7dd329e2fdf3ce10ac20f81b4`; actual receipts and provider inventory are alongside it. Read selected cases only; preserve every frozen artifact.
- Target `0.1.0-dev.12`, protocol 1, state 3. No schema downgrade or silent rewrite. Preserve work/evidence/migration/remote and native/legacy audit. Missing historical choice proof must never become usable approval. Current empty proof must be represented explicitly; old buggy approvals lacking it can require new explicit approval, not fabricated backfill.
- No real credentials, `.env.local`, config, stores, transcript, host Git config, Keychain, ai-database or live services. All test mutations confined to private loopback provider after explicit synthetic approval. No retries, deletes, remote fallback, model calls or production authority.

## Exact files

Implementation write scope: `internal/rewrite/plan.go` (saved/current dirty-footprint guard), `approval.go` (durable empty-proof distinction), `submission.go` (zero-operation consumption result handling), `model.go` version constant only. Optional regression coverage in existing `approval_test.go`, `submission_test.go`, `plan_test.go` for these genuine behaviors. Docs: `docs/agents/rewrite-interface.md`, `CHANGELOG.md`; CLI help only if a changed public contract actually needs it.

All other application files read-only, including store/loader/migrations, collectors, config, Gradion reader, generic MCP, dependencies, frozen tests and old packages. A concrete missing dependency requires reporting before expansion. Do not change the original worker/reviewer evidence.

Read capsule, ranges before full files:

1. This packet and `timesheet-rewrite-r4-rereview.md`; original R4 packet requirements 1-5 and group 9 remain binding.
2. `plan.go:805-879` and existing `collectContributions`/interval allocation helpers; saved state and open session semantics. `session.go` only the existing confirm/remove/save/cancel handlers and dispatch shapes needed to drive the binary.
3. `approval.go:76-108,278-330,422-539`; `submission.go:593-749,1077-1095,1209-1233`; relevant existing tests.
4. `store.go:82-153,162-215,340-475` read-only for schema/ledger semantics. Interface state-upgrade and submission sections only.
5. Named worker evidence `harness-source/scenarios3.go:378-401,449-511`, `transcript-g4.jsonl:19-20`, `source-snapshot/source.sha256`. They demonstrate why the previous labels overstate coverage; do not load all transcripts.

No repository-wide discovery, private file reads, arbitrary shell `cat`/`ls`/`find` or equivalent out-of-scope searches. Scoped navigation/LSP if actually available; do not assume tool availability grants authority.

## F2 continuation — saved footprint cannot disappear with an unsaved confirmation

The existing guard iterates `open.Confirmed` and `open.Staged`. After final removal plus unsaved confirmation, both are empty while the planner still reads saved work. Include the durable saved origin's confirmed allocation footprint, along with the current session lists, for dirty origins. Use each view's correct timezone/resolved allocations; do not persist drafts implicitly. Both approval and consumption re-render through this guard. Preserve unrelated dirty-date usability.

Required binary cases:

- Save/classify Oct 20 09:00-10:00, preview/approve, remove the final interval and confirm revision 3 without saving. Submit must reject with zero mutations, not create the old saved interval. Durable state and approval remain intact.
- The same unsaved final removal before approval must prevent approving an old saved preview. Include a prior-day final spill removal so the saved allocation date remains guarded even when current origin lists are empty.
- After explicitly saving the empty day, the old approval must remain unusable; restart must show the actual empty work/history. Do not weaken empty-day removal from R1.
- Controls: unchanged saved work executes; unrelated dirty date does not block; first repair's new staged spill still blocks. No reapproval or replay as an implicit recovery action.

## F7 — honest zero-operation consumption persistence result

`consumeWithoutWrites` currently treats the empty pre-activation persistence result as success. Handle clean success, failed-before-activation and sticky uncertainty distinctly. A failed transaction cannot claim durable consumption or a completed execution. Leave the approval active only when no transaction activated; restore filesystem availability and require a new explicit submit to consume it. No remote mutation is needed for zero operations.

Required genuine CLI proof, no production fault flag:

- Generate mapped unchanged work, zero-operation preview and explicit approval. After submit's fresh preview has durably completed, obstruct the private state parent at the fresh catalog/proof barrier (the reviewer used a generated directory renamed aside and a regular file in its place). This reaches consumption persistence rather than failing preview first.
- Failure response must truthfully describe non-consumption; actual durable approval remains active and no execution row for that attempt is claimed durable. Provider mutation delta is zero; record setup writes separately.
- Restore only generated filesystem fixtures. Explicitly submit that same unconsumed approval: clean durable consumption succeeds exactly once, survives restart and cannot be consumed again.
- Preserve the existing sticky post-rename uncertainty behavior. In-process fault-hook evidence may supplement it and must be labelled in-process, not CLI proof. Do not introduce a protocol/config fault switch.

## F8 — persist established empty proof, not legacy missing proof

A new zero-operation approval has a legitimate empty `Choices` map. Current `omitempty` encoding drops it, so reload produces nil and triggers the missing-proof safety gate. Durably distinguish these states without weakening legacy authorization checks. Preserve exact approved plan identity and occurrence ID; do not regenerate approval on startup or backfill old rows as authorized.

Required binary cases:

- Create/verify work, preview zero operations, approve, clean restart with no injected failure, then explicitly submit the persisted approval. It must consume durably with zero new writes and refuse a second consumption.
- Pre-proof/missing `choices` dev.10/dev.11 rows still cannot authorize; their audit remains intact. An explicitly encoded empty current proof is distinct from absent/null legacy proof. Nonempty classification change guards remain unchanged.
- Document unreleased dev.11 compatibility: previously omitted proof is not reconstructed as approval; new explicit approval is required where proof was not durably established.

## V1 — repair evidence claims and complete schema proof

- Preserve old evidence. Correct attribution: old G4 failed at preview revalidation, not intent commit. Independent reviewer supplied actual intent-commit CLI failure plus unchanged-approval reuse; refer to the frozen reviewer evidence accurately rather than rerunning an old binary to reproduce that known observation. New F7 must exercise the zero-op consumption boundary on the repaired binary.
- Complete original group 9 with rich schema-1 and schema-2 synthetic state. Prove startup is non-mutating by bytes/hash and durable schema, then activate schema 3 with a successful explicit durable transaction and restart. Compare actual confirmed intervals/history/revisions, evidence, and for schema 2 migration receipts/audit, identities/bindings, observations/resolutions, plans and unresolved outcomes. Historical approvals remain inert. Status/version alone is not a preservation assertion. Use the existing accepted models and synthetic fixture construction; do not alter loader semantics merely to make a fixture pass.
- New manifest must not include its own stale hash. Pin every owned source directly with full hashes, plus binary, selected rollback/final diff and actual runtime outputs. An outer manifest can pin an inner one; label coverage accurately. Never edit frozen files to make old checks green.
- Keep the prior 121-row execution as qualified prior evidence. Run changed paths and missing proofs; do not rerun everything just to produce a larger count. Scope checks once after edits; no `go test ./...`, no host-Git failure rerun or environment repair.

## Handback and stop rule

A NEW sanitized `/tmp/` snapshot with selected rollback/final sources, correct source/binary manifests, diff, actual compact protocol receipts, state assertions, read/mutation inventory, version/schema identities and command provenance. Map F2/F7/F8 and schema activation cases explicitly to observed facts. Remove generated fixture/config/env/state/legacy trees and drivers after retaining safe evidence; stop all child/provider processes. No secrets, authenticated endpoints, host identities or raw provider exports in retained output.

Stop review-ready for independent re-review; no commit/PR or R5. This is repair round two. If it does not converge, escalate remaining evidence for owner/technical adjudication under the two-round policy, not an automatic third repair loop.
