WORKER CLASS: complex-critical
EXECUTION MODE: owner-mediated; inline only; no child dispatch
TASK: TIMESHEET-REWRITE-R5-FIX1
TARGET: 0.1.0-dev.14; protocol 1; state 3
STOP: independently review-ready; no commit/publication/R6

# R5 first repair packet

Historical first repair packet: dev.14 implementation completed but failed [independent re-review](timesheet-rewrite-r5-fix1-review.md). Continue only with [R5-FIX2](timesheet-rewrite-r5-fix2-handoff.md), target dev.15. This packet's original safety/product boundaries remain applicable; do not rerun its closed repairs. The directing session has not edited application code or dispatched a worker.

## Authority and pins

- Work only on `feat/timesheet-rewrite-r5-v0.1.0`, baseline HEAD `4dbe08183d6a410a04589885a67541a0c51614e9`. Preserve owner governance/untracked changes. PRs #138/#139 and accepted R4 are frozen.
- Reviewed source capsule: `/tmp/timesheet-r5-review-snipl79o/source/`; ten-file `source.sha256` SHA-256 `58474e845fe36e9de49ebd2c488a43b875281cf1c92146ebe9ba1625bcf43cad`. Reviewed binary is in that directory's parent, SHA-256 `498f4f4b2e2c034cc9021b161211d0348d2e7113b6dde0fd49d9f221828e5055`.
- Check selected current-file identity against the frozen copies before editing; if ownership changed, stop and identify it. Do not reconstruct rollback copies from HEAD: most R5 files are new/untracked.
- Issue #136 under epic #131. No child dispatch, commit/staging/push/PR edits, merge/tag, R6, live accounts, real credentials/config/stores/transcript, global Pi profile, host Git test or ai-database access. Synthetic loopback mutations only after explicit local exact-plan approval.
- Report capabilities and inline rationale once. Tools being available is not authorization. No arbitrary cat/ls/find or equivalent out-of-scope discovery.

## Closed read/write capsule

Read this packet first. The review's findings table and named proof files explain discrepancies; do not load its entire artifact tree, old transcripts or historical repair reports. Read only the affected functions/types in the files below. No repository-wide source scan or node_modules traversal.

Writable:

1. `pi/transport.ts`: lifecycle/queue/handshake/UTF-8 boundaries; optional `pi/transport.test.ts` for real consumer-visible boundaries.
2. `pi/timesheet.ts`: protocol types, preparation/command ownership, review/action eligibility, origin editing/removal, identity/recovery forms and receipt/error presentation.
3. `pi/view.ts`: only if required to meet the existing inspectable details/resize/focus contract; optional `pi/view.test.ts`.
4. `pi/launch.mjs`, `pi/README.md`: remove production test escape hatches and document corrected behavior.
5. `pi/package.json` / `pi/package-lock.json`: dev.14 root package version consistency only. Dependency versions, resolved packages and integrity pins must not change.
6. New `pi/.gitignore`, if absent: `/node_modules/` only, to keep installed dependencies out of publication. Do not edit the root ignore file.
7. `internal/rewrite/model.go`: AppVersion constant only. `docs/agents/rewrite-interface.md` and `CHANGELOG.md`: accurate dev.14 consumer contract/repair notes.

Read-only references: the original `timesheet-rewrite-r5-handoff.md` named Pi 1.4.0 APIs/requirements; relevant interface-doc ops/grammar/review/reconciliation/submission sections; Go `internal/rewrite/submission.go:328-336` for WriteReceiptRow; only the canonical review/request/result definitions in `session.go`/`model.go` if necessary. Pinned public package declarations may be read by exact API path. Do not open any owner profile/config/env/store/export. No Go semantic/schema/dependency change, new RPC, second accounting engine, production fixture fallback or new runtime framework.

## Required repairs

| Finding | Bounded change |
| --- | --- |
| F1 — transport lifecycle | Readiness must mean identity negotiation finished successfully. Share one in-progress startup; no ordinary operation may dispatch before it passes. Every active/queued caller must settle once on failure, cancellation, shutdown or replacement. Preserve no replay and explicit reopen. Treat writable backpressure correctly: `write(false)` is not evidence of no dispatch. Failures after dispatch are not proof of zero effects. Old-child callbacks/frames cannot affect a reopened child. |
| F2 — bytes and UTF-8 | Incrementally decode UTF-8 across chunk boundaries, preserve valid content and enforce actual byte/frame/queue bounds. Malformed/EOF/extra-ID responses fail closed and settle callers. Use standard Node facilities, not another protocol stack or replacement text that silently corrupts valid input. |
| F3 — valid empty evidence | Match valid Go null/empty evidence shapes in the boundary types and rendering. Manual-only and empty-result source days must open, review and edit normally. Do not change Go output to accommodate incorrect TypeScript assumptions. |
| F4 — origin interval actions | Prefill from the canonical origin interval, not one allocation's display endpoints. A 23:00–01:00 interval must not become 23:00–24:00. After spill-to-origin navigation, send the origin day, interval ID and its current revision together. Keep authoritative grammar/revision/overlap checks; do not teach Go to accept the adapter's bad text. Handle legitimate midnight-ending intervals using the accepted grammar, not the `24:00` display label. |
| F5 — empty transitions/durability | Action availability follows lifecycle/revisions, not nonempty segment lists. Removing the final interval must retain cancellation; confirming the empty draft must retain durable Save local. Distinguish staged dirtiness from confirmed-but-unsaved revision changes in the header and receipts. Complete deletion/save/restart without changing Go semantics. |
| F6 — address-bound identity/recovery | `accept_observed` must offer the appropriate changed/mapped target and include its actual remote_id plus local_id/current observation/revision. For native unresolved edits, adoption must allow the intended already-bound identity, not only unbound entries. Use the actual observed target and Go validation; no guessed identities, foreign shortcuts or blanket clearing. |
| F7 — truthful receipts/failures | Consume `WriteReceiptRow.status` and the actual protocol shapes, not an invented `outcome`. Show each confirmed/unresolved/unattempted reference correctly. A transport/unknown exception is not a proven pre-dispatch refusal; never claim zero remote mutations without proof. Preserve any known receipt and direct unknown outcomes to fresh read-only status/reconciliation with no retry. Show persistence_failed as non-consumption, and durability uncertainty distinctly. |
| F8 — shipped launcher scope | Remove `--extra-extension`, `--fixture-provider` and their dead forwarding/docs branches from production. Keep only the trusted adapter and fixed isolation/tool flags. Provision loopback model metadata externally in a newly generated dedicated profile; do not add test hooks to shipped code. |

Preparation-tool/human-workflow ownership is an existing acceptance invariant, not optional extra scope. Demonstrate that an in-flight model preparation cannot replace or mutate an active human form/day/collection. Add the smallest explicit ownership guard if Pi's real scheduling does not establish this. Keep the one allowed date-only tool, bounded ≤512-byte content, safe opaque details and terminate behavior. Do not solve races with automatic collection/ack/approval/submission retries.

Align existing application/adapter/package identity with dev.14. No SemVer tag/release; exact Pi/TUI/typebox pins remain 1.4.0/1.4.0/1.3.27.

## Required runtime proof

Use actual pinned Pi TUI plus the real compiled Go child for business flows; a controlled protocol child is appropriate only for framing/negotiation/IO fault injection. Do not call a mocked ExtensionAPI a real UI. Label each evidence source and unavailable item.

1. **Transport:** real pending collect → child exit settles the caller; queued cancellation and quit/reload settle/dispose everything with no orphan/replay. Concurrent startup has one child and no ordinary dispatch before accepted identity. Wrong identity, malformed/extra-ID/EOF, backpressure and UTF-8 byte splitting preserve fail-closed behavior and exact valid text. Reopen is explicit and ignores old-child responses.
2. **Manual/empty:** no-source saved day and configured-but-empty evidence day are usable. Remove final interval → cancel restores it; remove → confirm empty → save → restart preserves zero work and correct revisions/history. Include an added-then-removed empty draft and truthful unsaved headers.
3. **Origin:** saved 23:00–01:00 survives an unchanged edit with the authoritative 120-minute total. Navigate from its locked spill to edit/remove the origin with correct identities; no stale_revision caused by adapter date mixing. Include a midnight-ending interval. Go, not TypeScript, computes all allocations/minutes.
4. **Identity/recovery:** mapped and changed accept_observed, foreign, stale-observation refusal and native bound-edit lost-ack recovery. After an exact edit applies but its acknowledgment fails, select the existing remote identity and resolve that one write; provider mutation count must not increase. A matching effect must still contradict not_applied, and foreign cannot clear unresolved writes.
5. **Approval/receipts:** actual exact create/edit display → cancel with no approval/write, then explicit approval/submit; same child throughout uninterrupted phases. Correct per-reference statuses, preserved known receipts, no false zero-effects error. Drive zero-op approval across restart and persistence_failed non-consumption/explicit retry through the UI using synthetic fault barriers, not production hooks. Keep previously accepted Go guards unchanged.
6. **Capability/model:** supported launcher has only the trusted adapter/resource inventory and timesheet_prepare declared/callable; shell/unlisted tools/forged approval text cannot execute privileged actions. Direct UI has zero instrumented model requests. Optional natural-language initiation uses the actual Pi model transport, bounded content/details, no record/config/credential disclosure and no automatic follow-up. Demonstrate the busy-human boundary and honest token availability fields.
7. **Migration/presentation:** real nonempty legacy migration and repeat no_change through local forms, legacy bytes unchanged and old approvals inert. Actual normal/narrow/resize/focus and long/multiline/wide/hostile text remain fully inspectable; controls stay reachable. A single long wrapped row must not disappear behind logical-line paging or become hidden approval content.

Map all original R5 acceptance criteria to final evidence. Qualified old evidence may remain labelled prior evidence where source changes do not invalidate it; never relabel it a rerun or use it to replace a changed path. The dev.13 worker's missing cross-midnight/accept_observed/foreign/no_change cases are not accepted exceptions.

## Verification, packaging and stop

Tests are optional, not forbidden. If added, pin real protocol/lifecycle/transition boundaries; no source-text/mock-echo tests. Run one coherent final strict TypeScript check, scoped Go build/version check and affected checks; do not repeat broad legacy suites or the known host-Git environment failure. Build once after final source changes, then exercise that exact binary.

Create a NEW sanitized `/tmp/` snapshot. Include selected before/final source copies, bounded diff, source and binary SHA-256 manifests with no self-hash, package/lock integrity/runtime versions, actual TUI evidence, compact JSONL receipts, child/process trace, provider/model mutation/call inventory, exact model-visible payload capture and acceptance map. Verify the manifests once. `MANIFEST.md` prose alone is insufficient. Never include `.env*`, authenticated endpoints/values, real records/transcripts or raw provider documents. Remove generated drivers/profiles/config/env/state/legacy fixtures and stop every child/provider after retaining safe evidence. Keep dev.13/reviewer/accepted R4 artifacts intact.

Report F1–F8 and C1 disposition individually, affected paths, exact commands/results, source/binary hashes, runtime evidence and any concrete remaining prerequisite. Stop review-ready; no self-acceptance or commit. This is R5 repair round one. Two nonconvergent R5 repair rounds require owner/technical adjudication, not a third automatic repair loop.
