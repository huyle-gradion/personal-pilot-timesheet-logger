# TIMESHEET-R2 review

Verdict: **changes required; R3 blocked**. Six finding groups below, with 13 independent boundary probes against the reported executable.

Historical first-review findings below. The first repair closed F1/F2 and several F3-F6 subcases. Use [the current re-review](timesheet-r2-rereview.md) and its narrow continuation packet; do not repeat the original broad repair.

## Snapshot, capabilities and method

- Branch `496a6ab8a81e9418a25046995ae40e2b8713e018`, reported base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Snapshot `/tmp/opencode/r2-final-20261022T094503Z`.
- Binary SHA-256 `bd735f78677102065a31e502aa48d4abc124a1b857a0370868d3dffee9b117ce`.
- `hashes.txt` SHA-256 `d035152fd36b015933d3c3b5f4bf99b680ad7dc4b387f61b1f0e6a8db335b380`.
- All packaged checksum entries passed. Current six application-source files plus public interface and changelog matched the snapshot.
- Observed review capabilities: scoped source/artifact reads, command execution, programmatic evaluation and public documentation search. Child-agent tools exist but current owner-mediated/no-child-dispatch authority does not permit using them. Review was performed inline, independently of implementation, without source changes.
- A private Python HTTP fixture exercised the actual binary. No supplied acceptance script or server was reused. Data/config/dotenv were synthetic; child environment was minimal and HTTP bound to loopback.
- Sanitized responses: `/tmp/timesheet-r2-review-3pb2ydmd/review-probes.json`. All child processes and the synthetic HTTP service were stopped.

The implementer's 52 passing checks remain evidence for their fixture. They do not cover the conditions below. The reported original-environment `internal/git` failure and clean-HOME pass remain qualified, owner-reported results; this reviewer did not rerun or diagnose that out-of-scope failure.

## Standards

No separate style-only blockers or mandatory-test findings. Effective Go remains the minimum. No-op cap handling and presentation-string-derived identities are addressed under the concrete GitHub correctness finding, not duplicated as style demands. Tests are optional; behavioral evidence is required.

## Spec

### R2-F1: P1, confirmation before first collection crashes the process

Locations: `internal/timesheet/session.go:1487-1494,1514-1548`.

With an enabled source, open a new day, stage a manual interval, then call `confirm` before `collect`. The process exits 2 with `panic: runtime error: invalid memory address or nil pointer dereference`.

`checkSourceGate` passes a nil evidence record to `latestAttempt`, which dereferences it instead of returning the intended unattempted-source error. This loses the running review session rather than keeping the gate closed with an actionable response. Fix the nil case and preserve state/process continuity for both confirm and save.

### R2-F2: P1, failed evidence writes mutate review state and can poison the store

Locations: `internal/timesheet/session.go:1349-1368,1415-1430`; `internal/timesheet/store.go:337-341,349-375`.

Two related rollback defects were reproduced by temporarily moving the synthetic parent directory during a write, then restoring it:

- Failed `acknowledge_source` returns `write_failed`. Disk acknowledgment remains false, but review reports true and `confirm` succeeds. The handler mutates the live `Store.Evidence` pointer before the store captures its previous value.
- Failed `decide_meeting` returns `write_failed`, but review shows `attended` while disk has no decision. Same shared-pointer mutation.
- Failed first `collect` for a new date rolls back by assigning the absent previous value, leaving an explicit null evidence entry. Collecting another date then succeeds and persists that null. Restart exits 1 with `state_invalid: local state contains a null evidence record`.

Required result: copy-on-write review decisions and exact rollback of absence versus presence. No failed pre-activation operation may change observable review state, unblock a gate, or create a record that a later successful write makes unreadable. Preserve the already accepted distinction between pre-activation failure and post-rename uncertainty.

### R2-F3: P1, acknowledgment cannot identify the reviewed attempt

Locations: `internal/timesheet/session.go:19-30,353-361,1334-1376`.

The request accepts only day/source; the collection ID is returned afterward rather than checked against the reviewed attempt.

Reproduction: collect A, review A, collect B, then deliver the acknowledgment action constructed from review A. It succeeds and acknowledges B. The second attempt is not the one reviewed. Resetting acknowledgment on recollection does not prevent this stale-action case.

Require the caller's reviewed collection ID and reject mismatches before mutation. Apply equivalent stale-decision protection where recollection changes a meeting decision target. Do not silently default a missing expected identity to the latest attempt.

### R2-F4: P2, calendar day-window and all-day end semantics are wrong

Locations: `internal/timesheet/calendar.go:446-465,765-779`.

- A one-day all-day event with `DTSTART;VALUE=DATE:20261022` and `DTEND;VALUE=DATE:20261023` appears when collecting October 23, with calendar status `complete`. DTEND is exclusive, but the implementation adds another day to it.
- Collecting October 22 also returns a cancelled override for October 10. `overrideMeeting` includes every cancelled timed override regardless of the selected window.

Use exclusive all-day ends consistently and restrict cancelled/overridden occurrences to relevant original/override windows. Do not remove all cancellations or all-day evidence to suppress these cases.

### R2-F5: P2, recurrence sets silently omit or retain the wrong instances

Locations: `internal/timesheet/calendar.go:162-165,469-489,501-568,637-659`.

- A series with an exhausted RRULE plus `RDATE:20261022T090000Z` yields no meeting on October 22 and reports `complete`. `buildRRuleSet` adds only RRULE/DTSTART; the RDATE handling runs only without an RRULE.
- An Amsterdam series at 09:00 on October 23 remains proposed despite `EXDATE;TZID=UTC:20261023T070000`, again with status `complete`. EXDATE parameter metadata is discarded and its value is interpreted in DTSTART's timezone instead.
- Source review also shows the all-day recurring branch never applies EXDATE or RDATE; this subcase was not independently runtime-probed.

Implement the recurrence-set union/difference with property-specific value form/timezone and de-duplication for timed and all-day events. Unsupported forms must produce honest partial coverage, not silently complete evidence.

### R2-F6: P2, GitHub coverage, attribution and provider identity are inconsistent

Locations: `internal/timesheet/github.go:315-360,421-430,449-484,535-627,725-833`.

Independent probes:

- A known PR has an available approval-only submitted review, but the collector retrieves no review and marks the `submitted_pr_reviews` family `empty`. It queries review comments, not the submitted-review endpoint. The event feed is explicitly bounded and cannot substitute for this scope while the review family claims exhaustion. The snapshot REVIEW inventory describes per-PR review calls that the source does not make.
- The same issue comment ID 88 returned by REST and events becomes two retained items: `gh:o/r:issue_comment:88` and `gh:o/r:issue-comment:88`. Provenance is not merged because identities differ.
- A commit response naming `another-user` is retained for configured actor `reviewer`. The collector trusts the query filter instead of verifying the provider-named actor, contrary to the strict-attribution contract. This is an injected provider-boundary case, not a claim that live GitHub commonly ignores its filter.
- With 51 commits in one topic, only 50 items remain, yet the family reports `complete` and `51 item(s) retained`. The overflow branch `t.Ref = t.Ref + ""` records no loss or bound.

Source review additionally shows PR review/comment grouping falls back to unrelated repository buckets or parses rendered titles instead of retaining resource identity. Preserve real provider resource/event identifiers through extraction, use one canonical event identity across families, filter attributable actors, implement the selected review scope, and make every collector truncation explicit. Do not narrow the accepted GitHub scope or pin fixture-specific strings.

## Documentation/scope corrections

- The protocol doc says missing credentials/empty/disabled states do not block, while the gate actually accepts only `complete` or acknowledged attempts for enabled sources. Update it to the intended behavior; missing credentials must remain an explicit source failure requiring acknowledgment.
- The original packet prohibited editing the development entry point, so stale CLI usage saying collection is unimplemented is an orchestrator scope omission, not an unauthorized implementer deviation. The repair packet explicitly permits only its usage text to be updated.
- Maintain source API coverage citations matching the actual code. Do not claim every check as regular CLI evidence if a path was covered only by in-process tests or source review.

## Primary references

- RFC 5545: [VEVENT non-inclusive DTEND, recurrence RDATE/EXDATE semantics](https://www.rfc-editor.org/rfc/inline-errata/rfc5545.html), sections 3.6.1 and 3.8.5.
- GitHub: [List reviews for a pull request](https://docs.github.com/en/rest/pulls/reviews#list-reviews-for-a-pull-request), including submitted review state and `submitted_at`. Review comments are a different resource.

## Verdict and next action

Standards: 0 separate style-only blockers. Spec: 6 finding groups, including process loss, evidence-store corruption, and stale acknowledgment risks. R2 is not accepted; R1's prior acceptance remains scoped to its reviewed snapshot, not automatically inherited by changed R2 code.

Next: [R2 repair packet](timesheet-r2-fix-handoff.md), then independent re-review. No application edits, commits, live services, real credentials/config/records, private transcript, ai-database or host-test reruns occurred during review.
