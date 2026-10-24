WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: timezone day-boundary accounting
GOVERNING ARTIFACT: docs/timesheet-rewrite-spec.md; docs/orchestration/timesheet-rewrite-r1-rereview.md
SCOPE: R1-F3 continuation only, local-day boundaries and allocation labels
VERIFICATION: compiled CLI probes on synthetic state, allocation conservation and restart; tests optional

# R1 narrow timezone repair

Historical owner-mediated packet, already executed and accepted at `0.1.0-dev.3`. Do not rerun it. See [R1 acceptance](timesheet-rewrite-r1-acceptance.md); the next separate owner-mediated action is the [R2 evidence packet](timesheet-rewrite-r2-handoff.md). Instructions below describe the completed narrow repair.

## Snapshot and authority

Remain on `feat/timesheet-rewrite-v0.1.0`, base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve all kickoff and implementation changes.

Reviewed snapshot: `/tmp/opencode/r1-fix-final-20261022T144049`; manifest SHA-256 `a52fd9552a2cfc9e5102b009786cf83692c2985f8803eb65add8ac9fcc15bae9`; binary SHA-256 `7df54fe7c37962e49a0e546aaaef043f07f82e2d522ca504ba2f5111effb9e2b`.

Authority when the owner runs this packet: the narrow repair and hermetic verification only. No commit, push, tag, release, child dispatch, live service, real credentials/config/records, transcript or ai-database access. Never open `.env.local`.

## Exact scope

Read the re-review finding and these affected files. Allowed writes:

- `internal/rewrite/model.go`
- `internal/rewrite/repair_test.go`, if adding behavior coverage
- `docs/agents/rewrite-interface.md`
- `CHANGELOG.md`

No config, store, session, CLI entry-point, dependency, collector, migration, or Pi changes. If another file genuinely needs modification, report the reason before expanding scope.

## Required change

Fix `localDayStart` and any dependent allocation rendering so an existing date whose midnight is skipped starts at its first real instant. `time.Date` normalization is not proof that the civil date is absent. Distinguish a wholly absent date from a missing midnight; do not suppress endpoint errors or add timezone/date special cases.

Use the origin day's recorded timezone. Preserve correct elapsed durations, repeated-hour rejection, absolute overlap handling, and recorded timezone stability. Continuation labels must show actual local segment starts, not a hard-coded nonexistent `00:00`.

Keep the change small. Do not introduce a timezone framework or a new dependency. Identify any new unreleased build consistently; no SemVer tag or release is authorized. Tests are optional; output proof is required.

## Runtime proof after the completed edit

Build the real CLI once and use a throwaway driver in a private synthetic directory. Keep sanitized output and hashes; remove the driver afterward. No real `.env.local` or network access.

1. Accept 09:00-10:00 as 60 minutes in America/Santiago on both 2026-09-05 and 2026-09-06, and America/Havana on 2026-03-08. Confirm/save/restart representative results, not only parser acceptance.
2. Santiago 2026-09-05 23:00-01:30 must total 90 minutes: September 5 contributes 60, September 6 contributes 30, with the continuation labeled 01:00-01:30. Verify the sum and the saved/reopened spill view.
3. Reject a September 6 endpoint within Santiago's missing midnight hour. Keep ordinary, Amsterdam spring/fall, ambiguous-endpoint, and Lord Howe half-hour-transition behavior intact.
4. Make allocation totals equal endpoint elapsed duration. Check that missing-midnight handling does not turn a wholly skipped civil date into fabricated work.
5. Run one final regression pass for already closed F1/F2/F4; do not rerun checks after every edit or expand their implementation without evidence of regression. Clearly distinguish ordinary CLI output from in-process fault-test evidence.

Report the exact changed snapshot, binary hash/version, commands, observed output, F3 disposition, public-contract changes and remaining limits. Stop review-ready for independent re-review. macOS remains unverified unless actually exercised.
