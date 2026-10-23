# R2 acceptance — reviewed dev.6 snapshot

Verdict: **Standards pass; Spec pass for the reviewed Linux/hermetic R2 scope.** F7 is closed. R1/R2 source prerequisites for preparing R3 are satisfied. This is not release readiness, live-account/macOS support, R3 implementation authority, or permission to commit, dispatch children or publish.

## Accepted identity and review

- Branch/base from the packet: `feat/timesheet-rewrite-v0.1.0`, `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Accepted artifacts: `/tmp/opencode/r2-f7-20261022T164214Z/`.
- Independently hashed executable `bin/timesheet-rewrite`: SHA-256 `c0d3e11aa90faa50a5aa0caa88509218961b11b049194e5817f4c6b7e4a96afc`.
- Actual version command: `timesheet-rewrite 0.1.0-dev.6 (protocol schema 1)`.
- Source identity manifest `after-hashes.txt`: SHA-256 `b7ef0c9dd4eb2be2120011ecb8fbcd29d7a419560e88f7688fd15a62dbf26d12`. All 14 entries independently verified against the current working tree.
- Reviewed bounded diff and current GitHub error branch. Only the authorized error branch, version bookkeeping, changelog and new focused behavior test changed. The other listed source/dependency hashes match the frozen baseline; calendar, session, config, store and prior repair tests remain unchanged.
- Inline independent integration review under owner-mediated/no-child authority. No application source repair performed in this review session.

## Standards

No blocking Effective Go issue in the correction. The focused test exercises real collector behavior and a plausible evidence-loss regression. Tests remain optional; no former TigerStyle/Go-rules requirement imposed.

Worker-supplied evidence: clean formatting/vet, focused regression passing, one dev build and 48/48 runtime assertions. Those commands/assertions were not independently rerun. The reported legacy host-sensitive Git failure and prior broad 105-assertion matrix were not rerun.

## Spec and finding closure

F1-F6 retain their prior reviewed dispositions; the protected code is unchanged apart from F7's narrow branch and application-version bookkeeping. The owner/technical gate after two repair rounds was honored: fresh technical adjudication upheld F7, then the owner explicitly authorized its bounded correction.

F7 now returns accumulated verified reviews with the existing failed family report and truthful retained count. The bounded detail preserves the HTTP error and states that retained reviews do not establish complete remaining coverage. The legitimate pre-enumeration search failure remains nil/zero count. No retry, new provider request, schema/status change, gate weakening or attribution fallback was added.

The worker's actual retained protocol evidence covers later-candidate failure, count/status/detail, current-attempt acknowledgment, stale-ID rejection without mutation, unchanged manual totals and restart. These remain worker-supplied observations, distinct from the additional independent probe below.

## Independent adjacent runtime proof

Executed the accepted binary against a loopback synthetic provider with private generated config/state, empty env-file, minimal environment and synthetic token. One persistent session, followed by a deliberate restart; no owner data or live service.

A successful first review page contained 100 provider rows: one attributable review (`707`) and 99 reviews by another actor. Page 2 then returned HTTP 503. Observed and asserted:

- Requested pages were exactly 1 and 2; retained topics contained exactly `gh:o/r:review:707`.
- Submitted-review family count was 1, status `failed`; detail named HTTP 503, one retained review and unverified remaining coverage.
- Both confirm and save rejected `source_unacknowledged` while previously confirmed manual work otherwise satisfied their prerequisites.
- Acknowledging the exact current attempt allowed confirm/save. Revision and saved revision became 2; manual total stayed exactly 90 minutes.
- Restart retained the same review ref, count, failed status and detail, with exactly 90 confirmed minutes and revision/saved revision 2.

Sanitized evidence: `/tmp/timesheet-r2-f7-page-review-_38ssbiv/page-failure-evidence.json`, SHA-256 `9ab273c53c55990e0e53eafeb41aaf1d743211433f25e808f05af2c663cc1759`. Synthetic config/env/state removed; child and loopback service stopped. This new case checks a later page within one candidate, rather than merely repeating the reported later-PR failure.

## Boundaries and next gate

Accepted: compiled GitHub/calendar evidence collection and the repaired local workflow under the accumulated qualified R1/R2 review evidence. Evidence never establishes worked minutes or authorizes submission.

Unverified: macOS runtime, live GitHub/calendar/Gradion accounts, real credentials/store, the Pi adapter and selected chat-provider path. No migration, remote planning, approval or submission behavior is claimed. No commit, tag, publication, child dispatch, live access or ai-database work occurred.

Next deliverable: a bounded owner-mediated R3 packet for explicit legacy migration and read-only Gradion reconciliation/plans. Preparing that packet does not authorize its execution or R4 writes.
