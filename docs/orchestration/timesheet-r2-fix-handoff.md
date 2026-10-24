WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: process loss, durable evidence corruption, stale human decisions, provider semantics
GOVERNING ARTIFACT: docs/timesheet-spec.md; docs/orchestration/timesheet-r2-review.md
SCOPE: TIMESHEET-R2-FIX, R2-F1 through R2-F6 plus named documentation corrections
VERIFICATION: actual executable against synthetic local HTTP services, write-failure/restart probes, standards-backed provider fixtures

# R2 repair packet

Historical first R2 repair packet, already executed. F1/F2 are closed; narrow F3/F4/F5/F6 continuations remain in [the re-review](timesheet-r2-rereview.md). Do not rerun this broad packet; use [the second repair packet](timesheet-r2-continuation-handoff.md). R3 remains blocked.

## Capability/delegation checkpoint

At task start, state the harness-exposed tools actually available, delegation permission, and the decision to work inline or use named specialists. Use tool inventory supplied by the harness; do not search private config or outside scope to rediscover capabilities. Report the decision once, not on every step.

Current authority grants no child dispatch. Inline execution is therefore compliant, not evidence of forgotten tools. If the owner separately enables bounded read-only specialists, RFC/calendar interpretation and GitHub API coverage are independent useful questions; keep one source writer and record agent IDs/results. Do not infer that permission from tool availability or this suggestion.

## Snapshot and authority

Remain on `496a6ab8a81e9418a25046995ae40e2b8713e018`, base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`, preserving kickoff and implementation changes. Reconcile ownership and record a sanitized rollback snapshot before editing.

Reviewed snapshot: `/tmp/opencode/r2-final-20261022T094503Z`; `hashes.txt` SHA-256 `d035152fd36b015933d3c3b5f4bf99b680ad7dc4b387f61b1f0e6a8db335b380`; binary SHA-256 `bd735f78677102065a31e502aa48d4abc124a1b857a0370868d3dffee9b117ce`.

Authority when the owner runs this packet: named repairs, public primary-source documentation research and hermetic local execution. No commit, push, release/tag, tracker mutation, child dispatch, live account access, real credential/config/record reads, private transcript, ai-database access or R3. Never open `.env.local`.

## Targeted reads and exact writes

Read the R2 review, relevant accepted-spec/interface sections, and these affected implementation files. Read public API/RFC/library docs for contract questions; do not load the historical orchestration ledger.

Allowed writes:

- `internal/timesheet/model.go`, `session.go`, `store.go`, `github.go`, `calendar.go`.
- Optional deterministic consumer-behavior tests under `internal/timesheet/*_test.go`.
- `docs/agents/timesheet-interface.md`, `CHANGELOG.md`.
- `cmd/timesheet/main.go` **usage text only**, to correct the packet's earlier scope omission about implemented collection. No command/control-flow changes there.

No config/credential-file changes, legacy packages, dependency changes, Pi, migration or Gradion work. Inspect references before exported-symbol changes when language-server references are available. Ask for scope expansion only when an actual necessary caller lies outside this list.

## Required repairs

Use all six finding groups in `timesheet-r2-review.md` as the acceptance source:

1. **F1:** nil/unattempted evidence returns a structured source gate error; confirm/save must not crash or lose the running draft.
2. **F2:** review decisions are copy-on-write. Pre-activation failure preserves memory, disk, gate state and absence of new evidence keys. Failed first collection must not leave null entries that another successful operation persists. Keep the existing post-rename uncertain-state behavior intact.
3. **F3:** acknowledgment carries and verifies the exact reviewed collection identity. Reject stale/missing expected identity rather than silently acknowledging the latest attempt. Protect stale attendance decisions where refresh changes their target. Document protocol changes, required fields, version/schema implications and errors.
4. **F4:** all-day DTEND is exclusive. Cancelled/overridden instances are selected by relevant date-window semantics, not globally emitted on every day. Retain valid cancellation/all-day evidence and provenance.
5. **F5:** support the actual DTSTART/RRULE/RDATE/EXDATE recurrence set, including each property's timezone/value form, de-duplication, timed and all-day forms, and override interactions. Do not report unsupported or dropped recurrence data as complete. Use the pinned libraries appropriately rather than another hand-written incomplete rule engine.
6. **F6:** implement submitted reviews beyond review comments/bounded events; verify provider-named actors; canonicalize event identity across endpoints without merging distinct actions; preserve resource identity for grouping; report all evidence caps accurately. Remove placeholder/no-op cap branches. Keep all owner-selected GitHub scopes and report real API limits without making a partial family appear exhausted.

Update the incorrect gate documentation and stale CLI usage text. Ensure API coverage inventory describes the production calls actually implemented, with primary-source citations. Do not add suppressions, fixture-date special cases, mock production fallbacks, unrequested retries, generic frameworks or new test mandates.

## Runtime acceptance after the completed repair

Run checks after editing, not per-edit loops. Use the real compiled executable, private synthetic config/dotenv/state and loopback HTTP services. The existing 52 checks can remain regressions, but they are not a substitute for these cases. Permanent tests remain optional.

- With an enabled source but no attempt, stage work then confirm/save: return the documented gate error; process remains usable, draft unchanged; collect and deliberate review can subsequently complete the flow.
- Force a pre-activation write failure during first collection, acknowledgment and meeting decision. Show unchanged disk and review state; acknowledgment must not unblock confirmation, attendance must not appear applied, and a later successful write plus restart must load valid state without null evidence records. Also preserve sticky post-rename uncertainty through optional in-process fault evidence.
- Review attempt A, collect changed attempt B, submit the A-targeted acknowledgment: reject it. A B-targeted acknowledgment succeeds. Check corresponding stale meeting-decision behavior.
- A date-only event October 22 with DTEND October 23 must not appear on October 23. An October 10 cancelled override must not appear in an unrelated October 22 review. Test relevant cancellations and multi-day/all-day overlap without inventing hours.
- A finite RRULE plus an RDATE inside the queried day must include the extra occurrence. An EXDATE expressed in UTC equivalent to an Amsterdam occurrence must exclude it. Exercise all-day exclusions/additions and override interactions too; assert actual instance identity/time, not merely a nonempty result.
- An approval-only submitted review must appear even when the event feed is empty/bounded. Assert the review record and metadata, not just an HTTP-call count. Match actual documented provider response shapes.
- REST and event records for the same issue-comment ID produce one item with merged provenance; distinct interactions stay distinct. Other actors are not retained. More than 50 items in a topic must be retained or visibly capped with truthful source/family counts and reasons. Verify per-resource grouping using identifiers rather than display strings.
- Preserve manual-only R1 behavior, staged/confirmed work across collection, source independence, decision persistence, source-attempt reset, date windows/DST, and credential secrecy. No provider body, token, private feed URL or dotenv line in outputs/state/reports.

Use the known environment-sensitive `internal/git` result as qualified prior evidence; do not rerun it merely to reconfirm the reported failure. If running the suite with a synthetic HOME, state that environment explicitly rather than reporting an unconditional host-suite pass.

Remove throwaway drivers after retaining sanitized outputs and hashes. Explicitly label CLI execution versus in-process fault tests/source reasoning. Keep macOS and live integration marked unverified unless actually authorized and exercised.

## Report and gate

Return capability/delegation decision, exact branch/base/source/binary snapshot, finding-by-finding disposition, public-contract/version changes, commands/results, accurate API inventory and remaining limits. No self-acceptance or next-slice work. Stop review-ready for independent re-review.
