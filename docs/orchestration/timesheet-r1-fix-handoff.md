WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: durable overlap corruption, timezone accounting, post-rename durability
GOVERNING ARTIFACT: docs/timesheet-spec.md; docs/orchestration/timesheet-r1-review.md
SCOPE: TIMESHEET-R1-FIX, findings R1-F1 through R1-F4 only
VERIFICATION: real executable with synthetic state; restart and failure boundaries; tests optional

# R1 repair packet

Owner-mediated execution only. This prompt does not dispatch a worker. Do not advance to R2.

Historical broad repair packet, already executed. R1 is now accepted at `0.1.0-dev.3`, with F1-F4 closed. Do not rerun this packet. See [R1 acceptance](timesheet-r1-acceptance.md) and the next [R2 evidence packet](timesheet-r2-handoff.md).

## Snapshot and authority

Work on `496a6ab8a81e9418a25046995ae40e2b8713e018`, base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`, preserving kickoff changes and the implementation.

Reviewed snapshot: `/tmp/opencode/r1-final-20261022T135057`; manifest SHA-256 `52dd0ea012592018f0177479ebe23047335481e0ab469a810d99facdfb9be028`. Source and binary matched at review. Reconcile any intervening changes before mutation; capture a new sanitized snapshot for the repair.

Authority when the owner runs this packet: repair the named findings and run hermetic local checks. No commit, publication, release tag, direct dispatch, live service, real credential/config/record access, ai-database access, or collector work.

## Reads and allowed writes

Read the four findings in `timesheet-r1-review.md`, original R1 requirements in `timesheet-handoff.md`, the accepted specification, and the exact affected implementation/interface files. Do not load the private transcript or the large historical ledger. Never open `.env.local`, real config/stores, or ai-database.

Allowed writes:

- `internal/timesheet/model.go`
- `internal/timesheet/session.go`
- `internal/timesheet/store.go`
- `docs/agents/timesheet-interface.md`
- `CHANGELOG.md`
- Optional deterministic behavior tests in `internal/timesheet/*_test.go`.

No changes to `go.mod`, `go.sum`, the legacy CLI, collectors, Pi, credentials, or governance records. If an exported symbol changes and language-server references are available, inspect references before editing and migrate every affected caller within the named scope. Report any required scope expansion rather than silently changing other files.

## Required repairs

1. **R1-F1:** validate saves against the actual candidate durable store, not unsaved confirmations from other opened days. Receipts must describe what restart will load. Do not implicitly save other days. A dependent save may be explicitly blocked rather than introducing a multi-day transaction abstraction.
2. **R1-F2:** support explicit confirmation and durable save of removal of the final interval. Preserve cancellation and history/revision semantics. Avoid a guard that makes correcting a previously nonempty day impossible.
3. **R1-F3:** use each recorded day's timezone for real endpoint validity, elapsed duration, overlap accounting, and midnight allocation. Reject nonexistent times and make repeated-hour ambiguity explicit. Document the supported resolution/error behavior; never silently guess an occurrence. Do not reinterpret saved work when configuration timezone changes.
4. **R1-F4:** check directory activation/durability errors. Pre-rename failure can preserve the old file; after rename, durability may be uncertain and the disk may already contain the new revision. Keep memory/disk and error guarantees coherent, block unsafe continuation, and do not permit false durable receipts or blind retries. Use one narrowly scoped fault-injection seam if necessary, not a generic filesystem framework.

Maintain a real functional R1 implementation. No placeholders, no suppression of errors, no special cases for fixture dates, and no production mock fallback. Tests remain optional; runtime demonstrations are required. Use Effective Go; do not impose TigerStyle or the superseded Go rules.

This build is unreleased. Document any public-interface or state-schema changes and their upgrade behavior. Do not create a SemVer tag or claim a release; handle version identifiers consistently with the existing release policy.

## Verification after completing edits

Run build/format/static checks once after editing, plus focused real-executable probes in a private synthetic directory. Do not use or copy the real `.env.local`. Keep throwaway drivers outside tracked source and remove them after proof; retain sanitized outputs and source hashes.

- Reproduce the R1-F1 sequence from the review. The invalid save must be rejected or committed consistently with explicit authorized saves. Restart must never expose the reproduced overlap or a different total than the durable receipt. Also check the opposite save order and an unsaved new origin interval, not only editing a saved one.
- Save a day with one interval, remove it, confirm the empty result, save, restart, and show zero work plus preserved revision/history. Show that cancelling the staged removal instead retains the old interval.
- In Europe/Amsterdam on 2026-03-29, reject nonexistent 02:15-02:45 and count 01:30-03:30 as 60 elapsed minutes. Check an unambiguous fall-back-spanning interval, repeated-hour diagnostics, and cross-midnight allocation across a transition. Demonstrate stability after restarting with a different configured timezone.
- Exercise failures before rename and during directory durability synchronization after rename. Show the response, on-disk state, safe subsequent-operation behavior, and restart/recovery. Do not assert old-file preservation after activation without proving it. Fault injection is verification infrastructure only, not a production environment flag or fallback.
- Retain R1's demonstrated 105-minute ordinary review/save/restart path, overlap/stale-revision rejection, cancellation, single-writer exclusion, and value-free dotenv diagnostics. Run these as one final acceptance pass rather than per-edit loops.

## Report and next gate

Return branch/base and the new sanitized source/binary manifest, changed behavior, exact commands and outputs, disposition/evidence for each finding, public-contract changes, and remaining limits. Distinguish source reasoning from exercised fault paths. macOS support remains unverified until actually run.

Stop at review-ready for independent re-review. No self-acceptance, R2 work, commit, or push.
