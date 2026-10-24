WORKER CLASS: complex-critical
EXECUTION MODE: owner-mediated; inline only; no child dispatch
TASK: TIMESHEET-R5-FIX2
TARGET: 0.1.0-dev.15; protocol 1; state 3
STOP: independently review-ready; no commit/publication/R6

# R5 second repair packet

Historical second repair packet: dev.15 implementation completed and [independent review](timesheet-r5-fix2-review.md) closed F1/F2/F7/F9, but overall acceptance is withheld on pending-reload recovery. Two rounds are exhausted. The active gate is [owner/technical adjudication](timesheet-r5-adjudication.md), not another implementation packet. No automatic FIX3, commit/publication, child dispatch or R6. The directing session has not edited application code.

## Authority and exact baseline

- Existing branch `99a7f23336845ac6858706319e695a750f9116cd`; HEAD `4dbe08183d6a410a04589885a67541a0c51614e9`. Preserve all owner governance/untracked changes. No staging/commit/push/PR change, merge/tag/release, child dispatch, R6, live access, real credentials/config/stores/transcript, owner Pi profile, host Git test or ai-database.
- Frozen candidate: `/tmp/timesheet-r5fix1-evidence-20261024T021046`, outer manifest SHA-256 `c7fa9be9d5ec449cc5c50cd3bf5f9698c541946b58632c32e6c8bef13fdcd9c1`. Its final-source manifest is `source/final.sha256`, SHA-256 `6f2c1ff3287b786085f7923d608fab302de5a68fe8cd21baec36827fbe976f59`; eleven live files independently byte-matched it.
- Reviewed dev.14 executable SHA-256 `fcde8e3dd3f0b97dcd524970d8e716f8e7bcb45524f4918840c6d615fdf089c5`. Independent new evidence: `/tmp/timesheet-r5fix1-review-w28g40c6/`, including the selected frozen sources and executable. Neither capsule may be changed.
- Before editing, reconcile only the named current source pins with frozen dev.14. Do not reconstruct untracked R5 files from HEAD. Unexpected ownership changes require reconciliation, not overwrite.
- Track #136 under epic #131. PRs #138/#139 and accepted R4 remain frozen. Report tools/delegation permission and inline rationale once. The owner launches this packet; no automatic dispatch.

## Read/write capsule

Read this packet, then only the relevant findings/proof JSON and affected functions. No full transcripts, whole evidence trees, node_modules traversal, arbitrary cat/ls/find or equivalent out-of-scope search.

Writable:

- `pi/transport.ts`: response/identity validation, startup failure path and complete-frame size check. Relevant current ranges: `192-258,264-383,500-576,599-622`. Keep the existing transport API and repaired serialization/settlement/generation behavior.
- `pi/timesheet.ts`: exact active approval selection and removal of its unkeyed cache. Relevant current ranges: `354-375,416-436,1980-2102`; inspect `op` at `463-478` only to preserve the real-envelope/transport-failure distinction.
- `pi/view.ts`: existing detail wrapping/render/paging at `162-320`, using pinned Pi width/render primitives.
- Existing identity/version fields in `pi/package.json`, `pi/package-lock.json`, `pi/transport.ts`, plus `internal/timesheet/model.go` AppVersion constant only: align dev.15. No dependency/version/integrity changes except package root dev version fields.
- `pi/README.md`, `docs/agents/timesheet-interface.md`, `CHANGELOG.md`: accurate repaired behavior/version and explicit proof qualifications.
- Optional focused `pi/transport.test.ts` / `pi/view.test.ts` for actual boundary invariants. Tests remain optional; mocked UI/source-text tests are not substitutes for runtime proof.

Everything else is read-only, including `pi/launch.mjs`, `pi/.gitignore`, all other Go code, schemas/dependencies, old CLIs/skills and governance files. Read the original R5 handoff/interface only for the precise existing contract. Pinned public Pi declarations may be read by exact API path for display-cell/grapheme/row handling. Do not read real profile/config/env/store/export content. No new production fixture, retry system, RPC operation, cache/authority ledger, accounting engine or renderer.

## Required fixes

### F1 continuation — validate before claiming identity or refusal

Observed controlled status `result:null` rejects ensureReady with TypeError but leaves `starting`, `failure:null` and a live child. Another ensureReady starts a second child while the first remains alive. A malformed envelope with matching id and `ok:"not-a-boolean"` becomes a fabricated `ok:false/internal` refusal while state stays ready.

Validate envelope discriminator/error structure and the minimal status identity before using them. Malformed negotiation must enter failed state, stop the child, settle callers and require explicit reopen; no implicit second spawn. A malformed envelope is a transport failure, not a real Go refusal and not evidence of zero effects. Do not manufacture child errors from invalid wire data. Preserve valid Go refusals, shared startup, active/queued settlement, backpressure and stale-generation isolation. Use existing stable local error categories; no Go protocol change or schema dependency.

Proof: controlled null/missing identity and invalid envelope (including nonboolean ok/malformed error); all concurrent startup callers reject safely, failed ensureReady does not spawn, explicit reopen starts exactly one clean child, pending/queued callers settle. No operation may dispatch before accepted identity. Inspect the consumer failure classification so only valid child refusals receive the existing from_child tag. Retain split-byte/EOF/extra-ID/backpressure controls without calling old evidence a rerun.

### F2 continuation — bound complete raw frames

The buffer limit currently runs after complete newline frames have been removed. A valid JSON response with a 16 MiB padding string plus its envelope was accepted when the child wrote exactly the limit, paused, then wrote the remainder and newline.

Check each complete raw frame before decoding/dispatch and also bound an unterminated remainder. Define the byte/LF boundary consistently; newline, trailing whitespace and chunk alignment cannot bypass it. Preserve valid UTF-8 across chunks; no silent text truncation or permissive replacement used as validation.

Proof: both a completed oversized frame and an unterminated oversized frame reject/stop/settle; valid frames near the boundary and split multi-byte content still work. Keep retained evidence compact (byte counts/status), not 16 MiB payload copies.

### F7 continuation — select the exact active approval

Actual UI: A approval cached on Oct 22, then Oct 23 with an existing active B approval. B's submit dialog rendered B's revisions but named A's approval; Go refused unknown_approval with zero calls. Restarting selected B correctly and the same zero-op plan succeeded.

Remove the unkeyed `lastApprovalId` bypass. Prefer the existing bounded submission_status read scoped to the current day and exact plan with active state; do not introduce a parallel cache/ledger. Remove dead state/type/initialization/writes if the cache is unnecessary. The selected returned ID must be displayed before explicit submit and remain the ID sent. Do not silently reapprove, substitute after confirmation, revoke or consume the other day's approval.

Proof in actual Pi + compiled Go: seed two independent current plans/active approvals; approve/view A then switch to already-approved B without restarting. B shows/sends B's ID and succeeds; cancel dispatches nothing; A's approval remains untouched. Include a same-day changed-plan/consumed-or-revoked approval boundary so stale metadata cannot masquerade as active. Synthetic provider effects only after explicit local approval; zero-op plans are sufficient for the cross-day identity case.

### F9 — page actual display rows, not UTF-16 chunks

A saved description `START-CJK` + 240 `界` + `END-CJK-ROW` is readable at 60×26 but its end is unreachable in the actual edit picker at 40×24 after G and 30 j presses. Controls remain visible; resizing to 100×36 reveals the hidden suffix. This is not just a clipped paging label.

Use the already-pinned Pi display-cell/grapheme-aware wrapping/rendering helpers and actual available physical rows, including editor/chrome/footer overhead. Do not hard-wrap by JS string length and then let Text wrap those chunks again. Do not force a minimum detail window that exceeds available space. Keep j/k/g/G, local controls and resize/focus behavior; no second terminal renderer, new dependency or modification/truncation of stored work.

Proof in actual Pi at 40×24, 60×26 and restored normal width: complete long ASCII/CJK/emoji/combining/multiline rows, including a sentinel at the very end, remain reachable without changing records. Resize and end/home/paging keep controls usable. Use a real plan/detail surface as well as the edit picker; full approval content cannot remain hidden. Do not fix this by removing the completeness label or declaring the tested narrow size unsupported.

## Closed work and remaining evidence

F3, F4, F5, F6 and F8 are closed at reviewed source/pinned evidence. F1 settlement/shared-startup and F2 valid UTF-8 chunks, plus F7 status rendering/unknown wording, are repaired subcases; do not implementation or regress them. C1 cryptographic provenance is now verified. No broad collector/migration/Go repair is authorized.

The reviewer independently exercised idle `/reload`: new child, saved revision/work restored, no orphan. This is not proof of pending reload/cancellation. Durability_uncertain UI remains source-reviewed/accepted-backend prior evidence, not a newly driven UI run. Complete reachable original acceptance evidence; preserve explicit source/prior/unavailable labels. Do not claim all runtime criteria passed from a manifest or backend tests. If a concrete required proof remains unreachable, state the prerequisite for owner adjudication rather than waiving it or adding production test hooks.

## Verify, hand back, stop

After coherent changes, run one final strict TS check, scoped Go build/version and affected checks. Tests optional. No broad legacy suite, known host-Git rerun, live subscription check or owner files. Exercise the exact final dev.15 binary through the supported pinned Pi launcher. Protocol/state remain 1/3; Pi/TUI/typebox remain 1.4.0/1.4.0/1.3.27.

NEW sanitized snapshot: exact before/final selected sources, bounded diff, package integrity/runtime versions, binary/source hashes and non-self-referential manifest, actual TUI captures, compact fault-child facts, exact-approval receipts and process/provider/model inventories, cleanup record and F1/F2/F7/F9/C1 acceptance map. Keep original verified capsules intact. Remove generated fixture/config/env/state/profile/driver files and stop every process after retaining safe proof; no secrets or raw provider documents.

Stop independently review-ready, not self-accepted. No staging/commit/push/PR change or R6. If this second repair does not converge, the next action is owner/technical adjudication, not an automatic third implementation packet.
