WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: stale authorization, duplicate remote effects, false edit confirmation, blocked valid recovery
SCOPE: TIMESHEET-REWRITE-R4-FIX, first repair round F1-F6 and missing standalone binary evidence
VERIFICATION: persistent compiled JSONL process against stateful loopback MCP; full original R4 runtime matrix plus reviewer boundaries

# R4 first repair packet

Status: **executed as dev.11; superseded by accepted dev.12 second repair**. Historical packet, not a fresh-start instruction. See `timesheet-rewrite-r4-acceptance.md`: F1-F8 and evidence V1 are now closed within the qualified scope. Do not rerun this repair or treat it as R5, child-dispatch, commit/publication or live-access authority.

## Authority and fixed baseline

- Stay on `feat/timesheet-rewrite-v0.1.0`, published HEAD `496a6ab8a81e9418a25046995ae40e2b8713e018`, with owner R4 changes uncommitted. Do not revert the working tree to HEAD or start from the old kickoff commit. One source writer; no child dispatch. State observed capabilities and inline decision once.
- PR #138 remains the frozen accepted R1-R3 baseline. Track this repair and its later separate PR in issue #135; do not append R4 commits to #138. No worker commits, push, tags, release, merge or GitHub publication.
- Reviewed dev.10 binary SHA-256: `024a225c8b97cb347d7797c7cd5c3dd15186cfb4d544b7e1a86a053a057c7e9a`.
- Frozen reviewer capsule: `/tmp/timesheet-r4-review-5ue9kmwy/`; `source.sha256` SHA-256 `e943676930298eafc6e07498e1f1ea7d107260c2b79cdd3dcfc76d147a8d0be8` pins 13 source files under `source/`. `boundaries.json` SHA-256 `93b3ba820056a3481cc567e46c6d6a3408e4be90b95bd4bcb9cd7c0e61f6d50e`; actual receipts `protocol.json` and compact `provider-inventory.json`. Read selected cases, not whole historical transcripts. Never modify frozen artifacts.
- Original worker `/tmp/timesheet-r4-20261023/post-edit.sha256` has the same 13 current source hashes. Capture a new selected-file rollback before edits, excluding real dotenv/config/records, transcript, whole-tree archives and old private reports.
- Target `0.1.0-dev.11`, protocol 1, state 3. Keep old rewrite work/migration/evidence/audit data and inert historical approvals. If adding choice-proof fields, old/missing proof must require explicit reselection/new approval, never fabricated authorization or mutation of historical approval occurrences. Document compatibility for unreleased dev.10 state; no silent data loss.
- No real `.env.local`, config, credentials, stores, private transcript, host Git configuration, vault or live endpoints. Writes only to private loopback MCP after explicit synthetic approvals. No production authority, retries, delete or fallback APIs.

## Exact scope and read capsule

Write only `internal/rewrite/{approval,submission,plan,model,store,session}.go`, their existing R4 tests `approval_test.go`, `submission_test.go`, and `plan_test.go` where genuine changed behavior requires it; `docs/agents/rewrite-interface.md`, `CHANGELOG.md`; `cmd/timesheet-rewrite/main.go` usage text only if needed. `gradion.go` may change only if selected-option proof needs a minimal correction to its existing catalog interpretation; do not change transport or unrelated remote reader semantics.

Read-only: configuration/credential precedence, collectors, `migration.go`/`migration_test.go`, all old packages, `go.mod`/`go.sum`, frozen R1/R2 tests and accepted dev.9 evidence. F5/F6 should reuse allocation/contribution helpers rather than change the accepted migration format. Report a concrete missing dependency rather than silently expanding scope.

Read first:

1. This packet; `timesheet-rewrite-r4-review.md`; original R4 packet sections 1-5 and runtime cases 1-10. The original requirements remain binding; this repair does not narrow them.
2. `approval.go:142-181,226-321,389-481`; `submission.go:248-311,538-561,659-695,761-804,1185-1366,1458-1595,1600-1724`.
3. `plan.go:105-199,222-325,706-839,1058-1155`; relevant plan/classification/interval models, state cloning/validation and selection protocol. Re-read latest ranges if line numbers shift.
4. `gradion.go:349-501` for current selected-option parsing; existing R4 tests/loopback fixture and relevant interface sections only.

No repository-wide discovery or shell `cat`, `ls`, `find`/equivalent out-of-scope reads. Use exact files and scoped symbol navigation when available. A tool's presence is not permission to use children or private data.

## Repairs and observable acceptance

### F1 — bind relevant classification choice, not ID existence alone

Bind the explicitly selected option's relevant ID/code/name/activity-type semantics into current selection, preview and approval preconditions. Re-observe current choices at approval/submit and compare relevant content with what was selected/rendered/approved. Reject meaningful changes before any mutation; require reselection and a new preview/approval. Current submission calculates a new proof but does not compare it with approval proof. Do not fix that merely by comparing whole query-dependent catalog hashes: unrelated option/query/timestamp changes must not invalidate the same choice. Missing/pruned old metadata is a retrieval hint, not proof; fail closed and request reselection when the accepted choice cannot be established.

Binary proof: same ID/name/code, Development -> Meeting before approval and separately after approval both block with zero writes. Explicit reselection/new preview/approval then permits the new semantics. An identical relevant option with different audit timestamp/unrelated result rows remains stable and executable. Partial returned-ID proof remains valid only for the matching unchanged option; no default or name-based substitution.

### F2 — dirty affected-date footprint includes new origins and moves

Before approval and consumption, guard all open staged and unsaved-confirmed work whose saved or current allocation footprints touch the plan's affected dates, not only durable contributors returned by `collectContributions`. Include newly added prior-day spill, changed timezone-resolved allocation, removal/move of an old contribution and unsaved confirmation. Reject pending affected changes; do not persist them implicitly. Do not block an unrelated dirty date.

Binary proof: approved Oct 20 09:00-10:00, then stage Oct 19 23:30-00:30; submit must block with zero writes even though the spill does not overlap. Also reject approval while such draft exists. Cancellation restores eligibility if every bound precondition is unchanged. Cover an unsaved confirmation and an unrelated dirty-day control.

### F3 — compare all observed batch content, accounting only for exact self-effects

Keep consumption-time expectations and advance them only by verified operations from this run. Compare content of existing IDs, not just ID membership or the next edit target. Previously verified self entries must not become an exemption for outside edits. Stop on unexplained changes observed before later dispatch. Never absorb a new snapshot as the baseline or silently narrow the original batch.

Binary proof: baseline unrelated entry 16:00-17:00; approved creates 09:00-10:00 and 11:00-12:00. First readback exposes that existing ID moved to 11:00-12:00. Only the first write may happen; prefix persists, later op is unattempted, approval cannot replay. Ordinary two-operation self-effects still succeed. Include a changed previously confirmed self entry. Preserve residual no-CAS race qualification; do not invent remote locking or retries.

### F4 — edit identity must equal approved bound identity

Verify an edit against exactly its approved remote ID. A different acknowledged ID is not proof about that target even if another entry has identical content. Keep acknowledgment and intended identity as distinct audit; mark unresolved and do not update binding/LastObserved or report confirmed. Readback, confirmation and explicit adoption must all preserve the edit identity. Tighten acknowledgment resource parsing to the actual supported entry kind; arbitrary nonempty kinds are not stable entry proof.

Binary proof: edit `created-1`, acknowledge `unrelated-ack-id`, return matching content at the latter ID. No confirmation or cross-ID LastObserved binding; durable intent remains unresolved across restart, old approval cannot replay. Correct-ID edit with exact previewed reason and remote-only preservation succeeds. Ambiguous/non-entry acknowledgment remains unidentified, not a slot-based identity guess.

### F5 — unresolved migrated allocation gates both approval and submission

Use affected allocation dates and verified local/origin relevance for pending migrated history, not only contributing origin dates. A visible pending spill outcome must prevent ready/approved execution for its spill plan even when the latest remote observation is empty. Preserve status-aware handling of confirmed/failed/skipped history and native unresolved-intent guards. R3 identity/adopt/foreign decisions must not evade unresolved-write gates.

Binary proof: actual migration of Oct 19 23:00-01:00 with unresolved Oct 20 00:00-01:00 create. Classification selected explicitly. Empty complete Oct 20 observation must still yield unresolved-write blocker and zero mutations through approval/submit; `submission_status` addresses the same reference. A terminal-history control remains usable. Do not auto-clear based on empty observation.

### F6 — recovery resolves actual allocation on its origin

Use actual contribution/allocation identity `(origin interval ID, allocation date)` rather than requiring `Days[outcome.Date]` to own the interval. Validate crosswalk identity, timezone-resolved allocation existence and one-to-one binding. Preserve original outcome/uncertainty beside explicit resolution. No invented independent spill record; insufficient legacy information still blocks. Apply actual allocation checks to shared native/migrated recovery helpers where appropriate without scope creep.

Binary proof: real migration from F5, matching complete observed spill entry, explicit `adopt` succeeds with zero write calls and maps `legacy-spill` on Oct 20 while its confirmed origin remains Oct 19. Restart preserves mapping/resolution/audit. Reject wrong allocation date, missing local contribution and theft of another binding. Other unresolved rows remain blocked; resolution never approves or submits anything.

## Complete the missing standalone runtime evidence

Implementer explicitly reported only in-process Session/Store loopback tests. The independent review added ten compiled-binary boundary/control scenarios, not the full original matrix. Deliver the original R4 packet's ten runtime groups against the actual persistent compiled child, plus all six repaired boundaries above. Do not present in-process tests as stdio proof or narrow the packet to the six regressions.

In particular retain genuine CLI proofs of pre-intent persistence failure with zero mutations; identity-only and mismatched/incomplete readback; actual interruption after effect and before outcome persistence; complete/partial multi-op histories; native and migrated recovery; wrong/revoked/spent/context/revision/choice invalidation; schema-1/2 preservation and schema-3 activation/restart; read-only/manual/no-Gradion and secrecy. Use real provider state/read/mutation inventory and exact payloads/reason, not mock echoes as the sole assertions. No production fault flag. In-process post-rename hooks may supplement, labelled accurately.

Build/format/vet/scoped tests once after coherent edits. No `go test ./...`, no rerun or host-config repair of the known legacy Git failure. Tests remain optional; regression tests must catch the behavior, not source text/wiring/copies.

## Required handback

A new sanitized `/tmp/` directory: final/rollback selected files, diff, source/binary hashes, version/schema identities, actual compact JSONL receipts, named assertions mapped to original groups and F1-F6, exact read/mutation inventory, and attributed check commands/results. Preserve original reviewed dev.10 capsule. Remove throwaway drivers/config/env/state/legacy fixtures and stop providers/children after retaining safe outputs. No credentials, authenticated URLs, host identities or raw provider documents.

Report each finding's disposition and any unrun proof honestly. Stop review-ready for independent re-review. This is the first R4 repair round; do not self-accept, start R5 or alter PR #138.
