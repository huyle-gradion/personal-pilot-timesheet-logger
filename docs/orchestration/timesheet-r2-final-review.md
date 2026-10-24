# R2 second-repair review and owner decision

Historical verdict for frozen dev.5: named F3-F6 continuations were addressed, but new P2 F7 blocked R2 acceptance. Independent technical adjudication confirmed F7; the owner authorized its bounded correction. **F7 is now closed and dev.6 accepted** under the qualified [R2 acceptance](timesheet-r2-acceptance.md); the original evidence below remains historical.

The owner-authorized [F7 packet](timesheet-r2-f7-handoff.md) has been executed and independently reviewed. The frozen dev.5 reference stays untouched. Do not reuse this historical finding record as new repair authority; R3 is a separate owner-release gate.

## Snapshot and method

- Reviewed artifacts: `/tmp/opencode/r2c-snapshot-20261022T161203Z`.
- Binary SHA-256: `c226acad53397f5b4cf90111a395645fcab7b456636e23da9fcb55142ca2b56a`.
- `MANIFEST.sha256` SHA-256: `115b8e382a6e5dbce5ecde7ac566af25174f4d579360e73c62271a49fca232b1`; all packaged entries verified. Current reviewed source hashes matched the corresponding snapshot copies.
- Real version command: `timesheet 0.1.0-dev.5 (protocol schema 1)`.
- Branch/base carried from the packet: `496a6ab8a81e9418a25046995ae40e2b8713e018`, `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. No commit or publication performed.
- Reviewed calendar civil-date/recurrence windows, attendance identity checks and GitHub candidate/actor handling against the continuation packet and supplied evidence. The implementer's 105 assertions remain supplied evidence, not a suite independently re-executed here.
- Independently exercised five adjacent scenarios with the packaged binary, isolated synthetic state and loopback-only providers. Four behaved correctly; the fifth reproduced F7 below. Evidence: `/tmp/timesheet-r2c-review-2jle0_6u/boundary-evidence.json`.
- Inline independent integration review using scoped reads, shell and evaluation tools. No child dispatch under owner-mediated authority; no source repair. Synthetic config/dotenv/state directories removed, child processes and loopback service stopped. No real credentials/configuration, live account, transcript or vault read.

## Standards

No new blocking Effective Go issue found in the inspected changes. Tests remain optional; no TigerStyle, former Go-rules or mandatory-test gate was imposed. The known host-sensitive legacy Git test was not rerun.

## Spec and repair dispositions

| Finding | Disposition for this snapshot |
| --- | --- |
| F1/F2 | Previously closed; the frozen store/configuration and repair-test files remain unchanged. Prior qualified fault-path evidence stands; those failures were not rerun. |
| F3 | Mandatory reviewed collection identity and rescheduled-start override refs address the continuation. A new unchanged-UID recollection probe, including `@` in the UID, preserved the skipped decision across restart; an old collection action returned `stale_collection` without changing state bytes. |
| F4 | Civil DATE labels and independently resolved exclusive ends address the continuation. A new Santiago skipped-midnight RDATE/EXDATE combination appeared on September 6 with labels September 6-7, and not on exclusive-end September 7. |
| F5 | Duration/span-derived candidate windows address the continuation. A ten-day timed recurring occurrence remained visible on its final partially overlapping day, October 11, and absent October 12. |
| F6 | Update-independent reviewed-by candidates and removal of unverified email attribution address the named continuation; source and supplied query-aware evidence support those cases. The new partial-failure case below is a separate outstanding collector defect. |

Acceptance requires collecting attributable submitted reviews and preserving identity/provenance while honestly declaring partial enumeration: [specification, GitHub and source failures](../timesheet-spec.md#github). F7 loses already collected evidence; it is not a request to infer work, widen scope or make unavailable coverage appear complete.

## R2-F7 — P2: a later review fetch failure discards earlier successful evidence

Location: `internal/timesheet/github.go:603-605`, in the per-candidate submitted-review loop.

`decode` appends verified, in-window reviews to `items`. If a subsequent candidate or page fails, the error branch returns `nil` instead of the accumulated items, but constructs the family report with `Count: len(items)`.

`internal/timesheet/model.go:775-777` defines the family report count as attributable items retained, not merely fetched. The technical assessment must account for this contract when evaluating the count mismatch.

The real CLI produced:

| Synthetic provider outcome | Retained review refs | Family count | Family status |
| --- | --- | --- | --- |
| PR #7 succeeds with review 707 | `gh:o/r:review:707` | 1 | `complete` |
| Same PR #7 succeeds, then PR #9 returns HTTP 503 | none | 1 | `failed` |

Thus an unrelated later failure removes known available evidence from review and durable topics while its family count remains nonzero. Failure visibility is preserved; this is evidence loss, not a false complete-coverage claim. The incomplete-source gate must remain intact when correcting it.

## Technical adjudication and bounded reopening authority

Independent assessment: `/tmp/timesheet-r2-technical-review.CmxguP/REVIEW.md`, SHA-256 `68858ec9bd1d131258fdb6a46d266bdb3b338925fbef29964e8a0000d607ea4a`. Standards pass; Spec fail; F7 stands as a valid P2 blocker. It confirms retained-count semantics and the other families' partial-retention precedent, with no new runtime scenario claimed. The owner then explicitly selected “Authorize bounded correction.”

Authorized behavior change: return accumulated review items with the existing `failed` family report and truthful retained count, without weakening the acknowledgment gate. The packet permits bounded failure-detail clarification, dev.6 version/changelog bookkeeping and optional focused regression coverage. No retries, provider expansion, schema/status changes or broad refactor.

This bounded authority has been exercised. The correction and focused independent later-page/restart/gate proof are recorded in the R2 acceptance above. No R3 implementation, child dispatch, commit or publication authority follows from R2 acceptance.

## Verification limits

Linux, hermetic providers only. No macOS runtime, live-account behavior, production store or release readiness claimed. The known host-dependent Git-test qualification remains unchanged. No commit, tag, publication, live access, R3 or ai-database work occurred.
