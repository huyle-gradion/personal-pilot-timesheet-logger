WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: stale attendance, wrong calendar dates, omitted historical evidence, unverified attribution
GOVERNING ARTIFACT: docs/timesheet-spec.md; docs/orchestration/timesheet-r2-rereview.md
SCOPE: TIMESHEET-R2-CONTINUATION, remaining F3/F4/F5/F6 cases only
VERIFICATION: production CLI against query-aware synthetic providers, date-only/long-span matrices and stale-action rejection

# R2 second repair packet

Status: executed for dev.5; historical packet, not current implementation authority. See [second-repair review and owner decision](timesheet-r2-final-review.md): the named continuations are addressed, but new F7 requires the two-round owner/technical gate before any further repair.

Owner-mediated. F1/F2 are closed and successful F3-F6 subcases must be preserved. R2 is not accepted; R3 is blocked. Do not repeat the original broad implementation or repair everything afresh.

## Capabilities, snapshot and authority

State exposed tool capabilities, delegation permission and inline/specialist rationale once at start and in the report. Do not search private configuration to discover capabilities. Current authority does not permit child dispatch; availability is not permission. One source writer.

Branch `496a6ab8a81e9418a25046995ae40e2b8713e018`; base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve all existing changes and record a sanitized rollback snapshot.

Reviewed artifacts `/tmp/opencode/r2-fix-verify`:

- `hashes.txt` SHA-256 `91309682b2cb247ca94e8e9a87756634e14ac7f2727d0d1f5a263051b48817c6`.
- Binary SHA-256 `cf1c8a9e4635a46d773325d1d13042de32739271235932d06c72e23385587423`.

Authority when the owner runs the packet: named source repairs, public primary-source documentation and hermetic local verification. No commit, push, tag/release, tracker edits, live accounts, real credential/config/record reads, private transcript, ai-database or R3. Never open `.env.local`.

## Exact reads and writes

Read the current re-review, relevant accepted-spec/interface sections and the affected source functions. Do not load the historical ledger or redo completed investigations.

Allowed writes:

- `internal/timesheet/session.go`, `calendar.go`, `github.go`.
- `internal/timesheet/model.go` only for necessary reviewed-target fields/public types/build identity.
- Optional deterministic behavior tests under `internal/timesheet/*_test.go`.
- `docs/agents/timesheet-interface.md`, `CHANGELOG.md`.

No store/config/dependency/CLI/credential changes, legacy packages, Pi, migration or Gradion work. Existing store rollback is closed; do not redesign it. Inspect affected references before exported-symbol changes when language-server references are available. Report a genuinely needed extra caller before expanding scope.

## Required corrections

1. **F3:** every attendance action must verify the exact reviewed attempt or equally strong target fingerprint. An omitted identity must not bypass verification. An override ref containing RECURRENCE-ID alone does not fingerprint the current rescheduled target. Keep deliberate decision persistence for unchanged instances separate from new action authorization. No compatibility shim that silently targets latest state.
2. **F4:** preserve original date-only DTSTART/DTEND values and compute actual day bounds consistently with the accepted first-real-instant resolver. Correct masters, recurring instances and overrides. Resolve the exclusive end from its date, not by carrying a shifted start clock into another day. Never display a declared September 6 event as September 5 due to midnight normalization.
3. **F5:** replace fixed one-day/seven-day recurrence look-backs with span/duration-aware conservative candidate selection and exact overlap filtering. Preserve RDATE/EXDATE/override semantics, DST-aware dates and truthful expansion bounds. Do not special-case fixture dates, assume all-day spans are one day, or silently drop long occurrences.
4. **F6 discovery:** a candidate PR's last-updated day is not a review submission day. Historical review collection must include PRs updated later and then filter each review by submitted_at. Use verified API semantics, a superset candidate query and honest caps. The test provider must actually apply query qualifiers; do not use a fixture that returns the same candidates regardless of the query.
5. **F6 ownership:** remove the unconditional unlinked-email fallback. An arbitrary email in a response does not establish configured-user ownership. Use already available verified ownership if any, otherwise omit unprovable attribution with a visible coverage limitation. Do not invent an email registry, extra credential probes or a new identity subsystem to make a test pass.

Preserve strict errors, failed-write isolation, attempt-bound acknowledgments, canonical deduplication, explicit topic truncation, resource grouping and the already working standard recurrence cases. Do not change unrelated behavior. Tests remain optional; actual output proof is required. Document stricter request requirements and version/schema implications, identify the new unreleased binary consistently, and create no release tag.

## Runtime proof after completing the edit

Build once after editing. Use private synthetic dotenv/config/state and loopback providers. Keep sanitized outputs/hashes and remove drivers afterward. Do not use real `.env.local` or rerun the known host-Git failure.

- Collect/review override A at 13:00. Recollect the same UID/RECURRENCE-ID at 15:00 with the same ref. The old attendance action without an expected identity must be rejected; A's explicit identity must also be rejected. Only a current reviewed target may accept the new action. Prove zero mutation on rejection and persistence of a valid decision.
- A Santiago all-day DATE event declaring September 6-7, 2026 appears only on September 6, keeps those DATE labels, and is absent both September 5 and the exclusive-end September 7. Include a recurring or overridden date-only occurrence and confirm no 01:00 clock is carried into its exclusive end.
- Weekly three-day all-day occurrence October 19 through exclusive October 22 is present on interior October 21 and absent October 22. Monthly timed occurrence October 1 09:00Z through October 11 09:00Z is present October 10. Assert actual instance identities/spans and source completeness, not only collection success.
- A PR with a review submitted on the selected day and a later resource update still yields that review. The fixture must filter `updated` correctly, as a real provider would. Keep out-of-window review records excluded, and report candidate/resource caps honestly.
- Another user's unlinked email is not retained as the configured user's activity. Legitimately verified ownership remains supported if already available; unresolved attribution is visible, not called complete.
- Run one final regression pass for closed F1/F2 and the working F3-F6 scenarios, manual-only R1, persistence, secrecy and source independence. Separate ordinary CLI proof from in-process fault tests and source-only reasoning.

## Report and gate

Return capabilities/delegation decision, exact source/binary snapshot, build identifier, commands/output, remaining-finding dispositions, public-contract changes, actual coverage bounds and limits. macOS/live support stays unverified unless authorized and exercised.

Stop review-ready for independent re-review. This is the second R2 repair round. If it does not converge, request owner/technical review under the control policy rather than initiating another automatic repair loop. No self-acceptance, R3, commit or publication.
