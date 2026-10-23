WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: truthful migration identity/revalidation and complete inert approval audit
SCOPE: TIMESHEET-REWRITE-R3-CONTINUATION, F3 two-field continuation + F9 + C1 evidence hygiene
VERIFICATION: focused real-CLI repeat-migration and historical-audit transitions

# Second R3 repair packet

Status: executed for dev.9 and accepted after independent re-review; see [R3 acceptance](timesheet-rewrite-r3-acceptance.md). Historical packet, not current repair authority. F3/F9/C1 are closed for the qualified snapshot; the proposed R4 packet requires separate owner release.

Owner-mediated continuation of the authorized R3 slice. The dev.8 repair addresses F1/F2/F4-F8. Preserve those fixes; do not redo their implementation or full discovery. F3 needs only two missing historical fields; new F9 is the no-change identity/revalidation path. C1 requires scoped execution and a sanitized NEW report, not application feature work. R3 is not accepted and R4 remains blocked.

No child dispatch, live access, actual credentials/config/store, remote mutation, commits, publication or vault work. One source writer. State exposed capabilities, delegation permission and inline rationale once; do not discover tools by inspecting private configuration. Use the implementation session with R3 context if available.

## Baseline and narrow capsule

Branch `feat/timesheet-rewrite-v0.1.0`, packet base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve owner changes and all accepted/frozen artifacts.

Reviewed reference `/tmp/opencode/r3fix-20261023T030006Z/`: manifest SHA-256 `a40e7ceb88c4d0aff544b2e08238591380b17a2304ccbca31754f0f823707f0d`; binary SHA-256 `6bc400f98a61d85f0bc08e16b7686b7f56698e4d0da78feec37183792f269d70`, dev.8. All 31 entries and nine current-source pins matched independently. `session.go` is unchanged from dev.7 at SHA-256 `d8f4c2cd61707a8e8d1e5632cb691b001d7cc6a9cb600dddbe4ca862278ab7d5`; it is newly granted below because F9 lives in the previously frozen shortcut.

Read only:

- This packet; `docs/orchestration/README.md:5-24`; `docs/orchestration/timesheet-rewrite-r3-rereview.md`.
- `/tmp/timesheet-r3fix-review-sr9bjp9e/boundaries.json`, SHA-256 `bbc636a307189ab3de7f483f6b07fae7cf834c8fed54f8bcb9401b12cfeb3b23`.
- `internal/rewrite/session.go:1764-1858` for preview/apply.
- `internal/rewrite/migration.go:268-277,390-485,869-879,1067-1077` and identity helpers reached from those functions only.
- `internal/rewrite/model.go:1211-1224` and application-version constant; receipt/type definitions reached from these paths only.
- `internal/rewrite/migration_test.go` for existing fixture patterns and focused new behavior tests; relevant public migration/audit interface sections and changelog insertion point.
- The named `MANIFEST.sha256` / `after-hashes.txt` for selected-file integrity, not directory enumeration or whole-report/log imports.

No raw provider exports, transcript, actual dotenv/config/records, host Git settings, account discovery or ai-database. No repository-wide search or shell `cat`, `ls`, `find`/equivalent outside this capsule. Use scoped navigation/LSP when actually available; do not reopen closed collector/plan logic.

## Write allowlist

1. `internal/rewrite/migration.go`: approval-audit copy and per-date repeat-identity checks only.
2. `internal/rewrite/session.go`: `migrationApply` revalidation/no-change ordering only.
3. `internal/rewrite/model.go`: two additive historical approval fields and AppVersion dev.8 -> `0.1.0-dev.9` only.
4. `internal/rewrite/migration_test.go`: optional focused behavior regressions/necessary fixture updates only.
5. `docs/agents/rewrite-interface.md`, `CHANGELOG.md`: truthful corrected audit/no-change semantics and dev.9 entry.

All other files frozen, including plan/Gradion/store/config/CLI, plan tests, R1/R2 tests, dependencies, old packages, template env and orchestration documents. Keep protocol 1/state 2; the two audit fields are inert additive data, not a new authorization surface. No approval, retry, new provider call, remap endpoint or schema redesign.

## F3 — two approval audit fields

The legacy approval decoder already reads `RecordRevision` and `RemoteObservedAt`, but the durable audit struct/constructor drops them. Preserve both exact values in the inert approval audit. Do not infer either value from the linked plan: valid legacy plan/approval revisions and observation timestamps can differ. Preserve existing approved/revoked timestamps, statuses, `inert:true` and `usable:false`.

Required proof: migrate a historical plan at revision 16 and approval at revision 17 with different remote-observation timestamps. Inspect only generated synthetic state through the private driver; show that the approval's original revision/timestamp survive apply and restart. Old approvals remain unusable. Do not fabricate values absent from a source, rewrite plan history or import provider raw documents.

## F9 — every successful no-change receipt needs valid current identity

`migrationApply` currently returns early for cached `cand.NoChange`, before rereading. Its second `fresh.NoChange` branch also precedes the source/target comparisons. Move the relevant fresh validation ahead of successful no-change returns, preserving atomicity and ordinary idempotence. A stale or conflicting source/target must produce a structured failure, not a false exact-preview success. Never overwrite/reinterpret previously migrated work to accommodate changed input.

The already-imported date test compares only hashes. Check the governing timezone as part of repeat identity too. Compare matching per-date receipt evidence correctly: a legitimate repeated subset of a larger imported batch must still work when bytes/timezone agree. Do not compare entire batch identities blindly and accidentally reject valid subsets. Same bytes under a different declared legacy timezone are an interpretation conflict, not already imported under the new identity.

Required compiled-binary cases:

- Normal fresh preview/apply, then identical repeat preview/apply and restart: no duplicates, no_change and unchanged store bytes/receipt count.
- After obtaining a no-change preview, change the synthetic source bytes before apply: reject stale/conflicting input; target bytes remain intact. A fresh preview must disclose the source change.
- Change the explicit legacy timezone across restart while keeping source bytes identical: preview reports an interpretation conflict and apply cannot return success/no_change for the different identity; original day/receipt timezone stays intact.
- Exercise changed bound target state through a valid protocol transaction and the old pending preview; honor the documented target precondition before returning success. Do not mutate the store file behind its writer lock as a test shortcut.
- Valid same-timezone repeated subset from an earlier multi-date batch remains a no-change result, including after restart.

Protocol shapes are unchanged: `migration_preview{dates}` returns `migration_id`, `source_identity`, `timezone`, `ready`, `no_change`, `conflicts`; `migration_apply{migration_id}` returns an applied/no_change receipt or structured failure. Apply requires a same-process pending preview and no open day. Do not loosen either guard.

## C1 — execution and artifact hygiene

Do NOT run `go test ./...` or repeat the known legacy Git failure. Do not inspect/change global Git configuration or touch the old package to make it green. The prior broad failure is qualified historical evidence, not a new regression or a passing check.

The frozen dev.8 report contains an unnecessary host Git account identifier. Keep the frozen reference untouched for integrity, but do not copy/repeat it in the next sanitized report, logs or snapshot. If the prior failure must be mentioned, record only the test name, pre-existing qualification and that it was not rerun. Do not alter old manifests to hide history, claim the broad run did not happen, or present unrelated failed checks as green.

## Checks, evidence and stop

Use existing pinned Go tooling, format touched Go files, build once after edits, and run scoped static checks once. Optional tests may exercise real consumer-visible migration boundaries; do not add source-text/wiring/wording-only tests. No dependency install and no broad legacy suite. Accept the review's failure observations as ground truth; do not rerun the old binary merely to confirm them.

Drive the repaired binary over persistent serve JSONL using only generated private config/env/legacy/state and minimal environment; use loopback-only providers if a valid protocol target-state transaction needs one. No owner settings or real source data. Retain compact genuine protocol/assertion evidence, selected source copies/diff/hashes, binary/version and exact check provenance in a NEW sanitized `/tmp/` directory. Preserve dev.8/dev.7/accepted R1-R2 artifacts. Remove throwaway fixtures/drivers and stop all services.

Report F3/F9/C1 separately, exact files, restart/error/no-change evidence, source/target non-mutation, receipt counts and limits. Target dev.9; no standing approval. Stop review-ready for independent re-review. This is the SECOND R3 repair round: if it does not converge, return to owner/technical adjudication rather than initiating an automatic third repair or proceeding to R4.
