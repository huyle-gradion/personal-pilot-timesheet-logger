# R3 first-repair re-review — dev.8

Historical first-repair dev.8 record. The second repair has converged: [dev.9 R3 acceptance](timesheet-rewrite-r3-acceptance.md) closes F3/F9/C1. Do not repeat this record's continuation instruction as fresh authority.

Verdict: **source Standards pass; Spec changes required. R3 is not accepted; R4 remains blocked.** F1, F2 and F4-F8 are addressed for the reviewed repair. F3 has a two-field audit continuation. New F9 concerns repeat-migration identity/revalidation. The first R3 repair round is complete; the next packet is the second, not a third automatic loop.

## Identity and provenance

- Snapshot `/tmp/opencode/r3fix-20261023T030006Z/`.
- Manifest SHA-256 `a40e7ceb88c4d0aff544b2e08238591380b17a2304ccbca31754f0f823707f0d`; all 31 packaged entries independently verified.
- Binary SHA-256 `6bc400f98a61d85f0bc08e16b7686b7f56698e4d0da78feec37183792f269d70`; actual version `timesheet-rewrite 0.1.0-dev.8 (protocol schema 1)`.
- All nine entries in the repair `after-hashes.txt` match the current source. Session/config retain their dev.7 hashes; the new F9 session shortcut was frozen by the first repair packet, not newly introduced by the repair.
- Inspected allocation-key lookup/projection, collision/foreign handling, canonical readiness/persistence, audit/crosswalk/capacity code, required-container validation and terminal-outcome predicate. No application source edit or child dispatch.
- The worker's 10 scenarios/111 assertions and scoped rewrite-test result remain worker-supplied evidence. Coverage mapping is a supplied index, not proof that its original-failure summary fields are current outcomes.
- Independent new evidence `/tmp/timesheet-r3fix-review-sr9bjp9e/boundaries.json`, SHA-256 `bbc636a307189ab3de7f483f6b07fae7cf834c8fed54f8bcb9401b12cfeb3b23`: four scenarios, one positive and three remaining counterexamples. Actual packaged binary, private synthetic config/state/legacy data, minimal child environment and loopback MCP. Only the two read tools observed. All processes/services stopped; generated sensitive fixture directories removed.

## Standards and execution compliance

No new blocking Effective Go issue in the inspected source. Tests remain optional; no TigerStyle or former Go-rules requirement imposed.

**C1 — execution/evidence correction:** the worker report records a broad `go test ./...` run despite the packet's explicit prohibition on rerunning the known host-sensitive Git failure. It correctly reports that failure rather than claiming an all-green suite, but its retained `REPORT.md` also includes a host Git account identifier despite claiming no account identifiers were retained. That identifier is not repeated here. No credential exposure was observed; no rotation request is justified by this fact alone.

Do not rerun the failure, inspect/change host Git configuration, modify the old package, or reclassify the failure as green. Future checks must be scoped to the rewrite. Keep the frozen dev.8 artifact intact for provenance, but do not publish/copy its unnecessary host identity into the next sanitized report/evidence. Code acceptance and compliance with execution/read bounds are separate findings.

## Original repair dispositions

| Finding | Disposition |
| --- | --- |
| F1 | Allocation spans/dates/minutes and binding keys now use the selected allocation. Source plus supplied two-binding/restart evidence addresses the named defect. Independently, Santiago September 5 23:00-01:30 previews September 6 as 01:00-01:30, 30 minutes, with a stable plan ID after restart. Live provider interpretation remains unverified. |
| F2 | Duplicate within-record identities are rejected; date-scoped crosswalk and outcome remap address the corruption. Source and supplied cases support closure. |
| F3 | Capacity refusal, per-date review rows and historical plan/approval fields address the main cases; two decoded approval-binding fields still disappear, below. |
| F4 | Current-observation foreign decisions are applied to equal-text hints while physical overlap still blocks. Source and supplied transition/restart cases support closure. |
| F5 | Required day/entries containers are explicitly validated; missing/null/non-list data cannot become empty success. Source and supplied negative/empty controls support closure. |
| F6 | Readiness blockers now participate in identity; stored-record matching prevents contradictory reuse. Source and supplied exact-change-plus-second-create transition support closure. |
| F7 | Physical collisions against bound/orphan entries are checked, and allocation existence is date-aware. Source and supplied replacement/restart cases support closure. |
| F8 | The predicate now distinguishes terminal and unresolved statuses; unknown statuses are rejected conservatively. Source and supplied cases support closure. |

## Remaining findings

### R3-F3 continuation — P2: approval binding audit is incomplete

Locations: `migration.go:268-277,869-879`; `model.go:1214-1224`.

The private legacy approval decoder reads `record_revision` and `remote_observed_at`, but `LegacyApprovalAudit` has neither field and its constructor discards them. In the independent fixture, the historical plan revision was 16 and the historical approval revision was 17, with distinct observation timestamps. After successful migration the approval audit retained neither original approval value. They cannot be safely inferred from the linked plan.

Preserve those exact historical fields alongside existing timestamps/revocation metadata. Keep `inert:true`, `usable:false`; retaining audit is not enabling authorization. This is a narrow continuation of the original history-preservation requirement, not a request to revive old approvals or archive provider documents.

### R3-F9 — P2: no-change migration bypasses exact identity/revalidation

Locations: `session.go:1822-1836`; `migration.go:453-481`; source identity definition at `migration.go:1067-1077`.

Two independent observations:

1. Import a source, obtain a repeat `no_change` preview, then change the source bytes before applying that preview. `migration_apply` returns success/no_change with the old receipt without rereading. A fresh preview immediately reports `source_changed`. Target bytes remain intact: this is a false revalidation/no-change receipt, not an observed overwrite.
2. Import under UTC, restart with the same source bytes but explicit legacy timezone Europe/Amsterdam, then preview/apply. The new preview's `source_identity` differs and names Europe/Amsterdam, yet it reports no_change/no conflicts and returns the original receipt. Durable work is still UTC. Comparing only source hashes silently ignores the timezone component of the declared migration identity.

Both no-change return paths must follow appropriate fresh source/configuration/target checks. A changed source or governing interpretation must produce an explicit stale/conflict result, never claim the exact new identity already imported. Compare timezone/effective per-date evidence correctly; do not reject a valid repeated subset merely because it was originally imported in a larger batch. Preserve ordinary identical repeat no-change behavior and never reinterpret/overwrite previously migrated work.

## Decision and boundaries

Next owner-mediated action: [second bounded R3 repair packet](timesheet-rewrite-r3-continuation-handoff.md). Preserve the closed fixes. Limit source work to the F3 field preservation and F9 identity/revalidation paths, plus necessary dev.9 documentation/version bookkeeping. Correct C1 in the new evidence package without modifying the frozen reference or rerunning the forbidden suite.

No macOS, live accounts, real credentials/config/store, Pi, approval or submission verified. No remote writes, commits, publication, source repair, child dispatch or ai-database work occurred. If the second R3 repair does not converge, stop for owner/technical review under the two-round policy rather than opening an automatic third repair.
