# R2 first-repair re-review

Historical first-repair record. The continuation packet has since been executed; current disposition is in the [second-repair review and owner decision](timesheet-r2-final-review.md). Do not use this record's next-action instruction as new execution authority.

Verdict: **changes required; R3 remains blocked**. F1 and F2 are closed for the reviewed repair. F3-F6 retain the narrow continuations below. Several original subcases of F4-F6 are fixed; do not redo them.

## Snapshot and method

- Branch: `496a6ab8a81e9418a25046995ae40e2b8713e018`; reported base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Artifacts: `/tmp/opencode/r2-fix-verify`.
- Binary SHA-256: `cf1c8a9e4635a46d773325d1d13042de32739271235932d06c72e23385587423`.
- `hashes.txt` SHA-256: `91309682b2cb247ca94e8e9a87756634e14ac7f2727d0d1f5a263051b48817c6`.
- All twelve manifest entries matched current source/docs, binary and retained evidence.
- Reviewed the changed decision/rollback paths, calendar recurrence/window code and submitted-review/actor handling against the packet and supplied evidence.
- Inline independent integration review: scoped file/command/evaluation/public-documentation tools used; child tools not used under current owner-mediated/no-child-dispatch authority. No source repair performed.
- Six additional logical CLI probe cases, including a three-date calendar matrix, used synthetic loopback providers/config/dotenv/state. Evidence: `/tmp/timesheet-r2-rereview-f7gdogdp/repair-boundaries.json`. All processes and fixture services stopped.

The supplied 69-check run and in-process tests remain qualified supporting evidence, not independently rerun checks. No host-sensitive Git test, real credential/config/record, private transcript, ai-database or live account was accessed.

## Standards

No separate style-only blocker. Tests remain optional. Effective Go is the governing baseline. Current findings concern consumer-visible semantics, not test count or TigerStyle rules.

## Spec disposition

| Finding | Disposition |
| --- | --- |
| F1 nil/unattempted-source crash | Closed: nil-safe attempt lookup and structured gate path; source and supplied CLI continuity evidence agree. |
| F2 failed evidence writes | Closed: copy-on-write decisions and deletion of previously absent keys restore pre-activation state; supplied CLI/fault evidence covers rollback and sticky uncertainty. |
| F3 stale human decisions | Source acknowledgment fixed; optional meeting collection identity still permits the stale override action below. |
| F4 calendar date windows | Ordinary exclusive DTEND and unrelated cancellation filtering fixed; skipped-midnight date-only events still move to the wrong date. |
| F5 recurrence sets | RDATE/EXDATE union/difference and property timezones fixed; fixed look-back windows still omit overlapping long occurrences. |
| F6 GitHub integrity | Direct review fetching, canonical deduplication, resource grouping and explicit topic caps improved; historical candidate discovery and unlinked-email attribution remain incorrect. |

## Remaining findings

### F3 continuation: P1, omitted collection identity bypasses stale attendance protection

Locations: `internal/timesheet/session.go:1422-1479`; `internal/timesheet/calendar.go:842-887`.

`decide_meeting` checks `collection_id` only when supplied. An override's reference contains its original RECURRENCE-ID, not its current rescheduled start, so reference equality does not prove that the user reviewed the current target.

Reproduced:

1. Collect/review an overridden occurrence at 13:00; retain its ref.
2. Provider reschedules that override to 15:00 with the same UID/RECURRENCE-ID. Recollect; the ref remains identical and decision is pending.
3. Submit the old attendance action with that ref and no collection ID.
4. It succeeds, records `attended` on the 15:00 proposal, and returns the newer collection ID.

Require an exact reviewed collection identity or equally strong reviewed-target fingerprint for every attendance decision. Do not retain an optional-identity compatibility bypass. Carrying already recorded decisions across unchanged instances is separate from accepting a new stale action.

### F4 continuation: P2, an all-day date is shifted backward across skipped midnight

Locations: `internal/timesheet/calendar.go:485-514,600-603,708-714`.

The calendar still constructs date-only boundaries with `time.Date(..., 00:00, loc)` instead of the accepted first-real-instant day resolver. `addAllDayInstance` derives the displayed date from the normalized instant and its end by adding days to that time.

A source event declares DATE DTSTART September 6 and exclusive DATE DTEND September 7, 2026, in a Santiago review:

- September 5 collection incorrectly includes it, labeled September 5-6.
- September 6 collection also labels it September 5-6.
- All attempts report `complete`.

The civil DATE values must remain September 6-7. Resolve both start and exclusive end from their dates using correct first-real-instant boundaries; adding one day to a 01:00 boundary would incorrectly extend the event into the next date's 01:00. Verify the day before, actual day and exclusive-end date, not only elapsed totals. Recurrence/override date-only paths must use the same rule.

### F5 continuation: P2, look-back constants miss still-active recurring instances

Locations: `internal/timesheet/calendar.go:523-539,568-591`.

Observed with `status: complete` and no meeting:

- A weekly all-day occurrence starts October 19 and ends exclusively October 22. Collecting its interior day October 21 yields nothing because the query looks back only one day.
- A monthly timed occurrence spans October 1 09:00Z through October 11 09:00Z. Collecting October 10 yields nothing because the query looks back only seven days.

Use occurrence duration/calendrical span to select a conservative recurrence candidate window, then test actual interval overlap. Account for inclusive/exclusive lower-bound semantics. If expansion hits a real resource bound, report incomplete coverage; never label omitted in-window occurrences complete. This applies to RRULE and explicit additional dates.

### F6 continuation: P2, historical review discovery uses resource-update date as review date

Location: `internal/timesheet/github.go:503-617`, especially line 507.

The candidate query is `reviewed-by:<user> ... updated:<selected-day>..<selected-day>`. GitHub's `updated` qualifier refers to when the PR was **last updated**, not when each review was submitted.

An independent synthetic provider honors the query: a PR reviewed October 22 but last updated October 24 is excluded from the October 22 candidate search. The collector returns no review and marks `submitted_pr_reviews` empty/exhausted, although the selected-day review exists at the reviews endpoint. This fixture models a historical query after later PR activity.

Candidate discovery must be a superset of PRs that can contain selected-window reviews, independent of later resource updates. Filter individual review records by submitted_at. Preserve honest pagination/candidate bounds rather than claiming an exhaustive empty result from the narrower query.

Primary semantics: [GitHub search creation/last-updated qualifiers](https://docs.github.com/en/search-github/searching-on-github/searching-issues-and-pull-requests#search-by-when-an-issue-or-pull-request-was-created-or-last-updated).

### F6 continuation: P2, the email fallback is still unverified ownership

Location: `internal/timesheet/github.go:399-417`.

When `author.login` is absent, any nonempty commit-author email is retained. There is no configured or verified email match at this point.

Probe: configured identity `reviewer`, nil author document, email `unlinked-other@example.invalid` results in that actor being retained. The repaired branch checks a nonempty login but the fallback again trusts the server-side author parameter rather than verifying ownership. This is a provider-boundary fixture, not a claim that live GitHub routinely violates its filter.

Retain only identities provably attributable to the configured user. Do not call an arbitrary email "matched". If ownership cannot be established, omit that attribution with a visible coverage limitation rather than inventing it. No new email-discovery subsystem is required to fix the unsafe assumption.

## Next gate

Standards: 0 separate style-only blockers. Spec: four finding groups remain, supported by six new logical probe cases. No build/test reruns or application edits are claimed by this review; source/binary/evidence checksum verification and the described CLI probes were exercised.

Next owner-mediated packet: [R2 continuation](timesheet-r2-continuation-handoff.md). Preserve the closed repairs and successful subcases. This is the second R2 repair round; if it fails to converge, stop for owner/technical review under the existing control policy rather than starting an automatic third repair loop.
