# Changelog

Versioning follows [Semantic Versioning 2.0.0](https://semver.org/). This file starts the documented rewrite release line; it does not retroactively assign versions to historical commits.

## Unreleased

### Second R4 repair round (`0.1.0-dev.12`)

Boundary-scoped repairs to the first R4 repair (R4-F2 continuation, R4-F7,
R4-F8) without changing the closed R4F1/F3/F4/F5/F6 behaviors, schemas or
R1-R3 surfaces:

- A dirty session's DURABLE saved allocation footprint now participates in the
  plan dirty guard (R4F2 continuation). Confirming a final interval removal —
  including removing a prior-day spill whose allocation renders on the planned
  date — without saving emptied both session lists, so the guard previously saw
  nothing while the preview still rendered the saved work. Approval and
  consumption both re-render through the guard, so the stale saved view can no
  longer be approved or submitted; the durable state and the existing approval
  stay intact, and unrelated dirty dates stay usable.
- Zero-operation consumption reports its persistence outcome truthfully (R4F7).
  A commit failure BEFORE activation previously fell through to `all_confirmed`
  with an execution id and no durable execution. It now returns the new
  `persistence_failed` status with a `write_failed` blocker, no execution id, an
  honest `next_action`, and zero remote calls; the approval remains `active`
  because nothing was consumed, and a later explicit submit consumes it exactly
  once. Post-rename `durability_uncertain` remains sticky and unchanged.
- A zero-operation approval's legitimate EMPTY classification-choice proof is
  now durable (R4F8): the approval summary encodes `choices` explicitly as `{}`
  instead of omitting an empty map, so the established empty proof survives
  restart as a non-nil empty map. Pre-proof dev.10/dev.11 rows (absent/null)
  still reload as nil and cannot authorize anything; they are never
  reconstructed or backfilled at load. Non-empty dev.11 proofs remain valid and
  are still revalidated against the current catalog.
- New receipt status: `persistence_failed`. `version` reports `0.1.0-dev.12`;
  protocol schema remains `1` and state schema remains `3`. No state field is
  added or removed: the existing `choices` field is simply written even when
  empty (legacy rows that lack it may materialize it as `null` on a later
  rewrite, which still reloads as missing proof and stays unauthorized). Existing
  state loads unchanged; no migration is created or implied.

### First R4 repair round (`0.1.0-dev.11`)

Boundary-scoped repairs to the R4 slice (review findings R4-F1 through R4-F6)
without changing R1-R3 behavior:

- Selected classification choices carry relevant semantic identity (R4F1):
  every planned operation binds the selected option's id/code/name/activity
  type, the plan identity and approval summary record that binding, and fresh
  authoritative results are compared per-option at BOTH approval and
  consumption. A relevant change (`classification_choice_changed`) or a choice
  that cannot be established (`classification_choice_unverifiable`) forces
  explicit reselection and a new preview/approval; unrelated catalog rows,
  queries and timestamps never invalidate an unchanged choice. Dev.10
  approvals predate the bound-choice field and fail closed for consumption
  instead of being trusted.
- Plan previews guard ALL open sessions by saved AND staged allocation
  footprint (R4F2): prior-day spill drafts, changed cross-midnight
  allocations, moved/removed contributions and unsaved confirmations touching
  the plan's affected dates block preview, approval and consumption with zero
  remote calls; unrelated dirty dates never block, and cancellation restores
  eligibility.
- Batch drift checks compare FULL entry content (R4F3): a re-read date is
  compared against the consumption baseline advanced only by this run's
  verified self-effects, so an outside change to an existing entry — including
  a previously confirmed self entry — stops later operations instead of being
  absorbed; the consumption baseline itself is never replaced.
- Edit verification requires the acknowledged identity to equal the approved
  bound one (R4F4): a different acknowledged id (`edit_identity_mismatch`)
  stays unresolved with both ids as distinct audit, never confirmed, never
  bound, never updating `LastObserved`. Write acknowledgments now parse only
  the documented `entry` resource kind.
- Unresolved-write readiness is scoped by affected allocation dates (R4F5):
  pending migrated and native writes gate a plan when their recorded date OR
  their locally resolved allocation dates touch the plan's dates, so a
  spill-day outcome whose recorded date is its origin still blocks an empty
  spill-day plan; `submission_status` surfaces the same relevance.
- Migrated adoption resolves the actual contribution (R4F6): the row's local
  identity must be confirmed durable work that timezone-resolves onto the
  recorded write date with the intended representable span, backed by the
  claimed migration crosswalk when one is named; wrong dates, missing local
  contributions, unknown crosswalks and cross-bindings all fail closed while
  the original outcome and its uncertainty stay preserved beside the explicit
  resolution.
- New stable codes: `classification_choice_changed`,
  `classification_choice_unverifiable`; new native intent error string
  `edit_identity_mismatch`.

### Native approval and verified submission (`0.1.0-dev.10`)

R4 slice: exact-plan approval, durable pre-write intent, and one verified
submission path. No R1/R2 behavior changes.

- Added the native approval ledger (state schema `3`, new `approvals`,
  `intents` and `executions` sections): `approve_plan` binds a unique
  occurrence approval (`apr-…`, never a plan alias) to the exact current
  rendered plan content after fresh precondition proof (plan identity,
  complete readable coverage, and the current authoritative catalog proving
  every selected classification). It never dispatches; `revoke_approval`
  revokes explicitly, and both records are immutable audit.
- Deriving plan status from the ledger: persisted plans no longer carry
  `approved`/`approval_available` flags; the preview result derives both from
  real approval state, while every plan operation now hashes its exact body
  including the deterministic edit audit reason.
- Added `submit_plan`: full fresh revalidation before any call, then one
  durable `in_flight` intent per operation committed BEFORE a single,
  never-retried dispatch, followed by a mandatory complete read-back whose
  content must match the exact intended body before a write is confirmed,
  bounded per-run re-checks for outside drift, and honest receipt statuses
  (`all_confirmed`, `partial_stopped`, `blocked_before_dispatch`,
  `durability_uncertain`). Zero-op plans consume their approval without
  dispatching anything.
- Added `submission_status` and `resolve_write`: every dispatched-but-unverifiable
  write is a durable unresolved reference (`wr-…`), visibly blocking further
  writing; recovery is an explicit human decision against the current complete
  observation (`adopt` an observed ID, or `not_applied` refused when a matching
  effect is visible), recorded as additive audit that never erases the original
  attempt or dispatches anything. Migrated legacy outcome rows are addressable
  through stable `mw-…` references by the same gate; rows without usable intent
  information stay visible blockers rather than resolved facts.
- Schema-2 files are converted in memory to schema `3` after their own
  validation: obsolete always-false plan authorization flags are stripped and a
  file carrying approval sections or any non-false/non-boolean claim is
  rejected (`state_invalid`). Approval authority is never read from migrated
  data.
- New stable codes: `stale_plan`, `plan_not_ready`, `unknown_approval`,
  `stale_approval`, `classification_unverified`, `unknown_write`.

### R3 continuation (`0.1.0-dev.9`)

Narrow continuation of the first R3 repair packet; no R1/R2 behavior changes:

- The inert approval audit preserves the approval binding's own historical
  `record_revision` and `remote_observed_at` beside the existing approval,
  revocation timestamps and status (F3 continuation). The values are copied
  verbatim from the legacy record and never inferred from the linked plan,
  whose revisions and observation timestamps may differ; the audit stays
  `inert: true` / `usable: false`.
- Repeat identity covers the declared legacy timezone per date (F9): matching
  per-date receipt evidence must agree on bytes AND timezone. The same bytes
  under a different declared legacy timezone are an `interpretation_conflict`,
  never an already-imported `no_change` result, while a repeated subset of a
  larger imported batch stays a valid no-change selection when both agree.
- `migration_apply` revalidates the fresh candidate before every successful
  no-change return (F9): a source or durable-target change since the preview
  is a structured `migration_stale` / `migration_conflict` failure instead of
  a false revalidation receipt, with the target untouched.

### R3 first repair packet (`0.1.0-dev.8`)

Boundary-scoped repairs to the R3 slice (findings R3-F1 through R3-F8) without
changing R1/R2 behavior:

- Plans are per-ALLOCATION (R3-F1): each date of a cross-midnight interval renders
  its own span and minutes, observes its own remote date, and binding identity is
  (origin interval, allocation date), so both allocations can bind to different
  remote IDs without stealing either mapping. An allocation without a
  representable same-day remote span blocks as `unrepresentable_projection`
  instead of being altered.
- Reconciliation surfaces occupied or orphaned identity collisions instead of
  skipping bound entries (R3-F7): an unmapped allocation overlapping an entry
  bound to another (or removed) identity blocks with `identity_conflict` /
  `orphaned_mapping`; unrelated mappings stay date-scoped and never globally
  block work.
- Foreign decisions (R3-F4) are bound to the exact current observation: they
  stop blocking equal text only, never authorize overlap, go stale on a changed
  observation, and survive identical fresh reads.
- The plan identity includes the exact readiness blockers (R3-F6), so a blocked
  preview and the ready preview after an explicit decision are distinct immutable
  records instead of one record whose state silently disagrees.
- The verified week contract validates required containers fail-closed (R3-F5):
  `days` must be a list of day objects and every day object must carry a `date`
  string and an `entries` list; a day without `entries` (or `entries: null`) is
  unreadable, never an empty day. Only `entries: []` is a valid empty result.
- Migration rejects a duplicated legacy local ID inside one source record
  (`ambiguous_local_id`) before guessing aliases or bindings, translates mapping
  and outcome references through a DATE-SCOPED crosswalk, and keeps the original
  ID/date on every audit row (R3-F2).
- Legacy outcome semantics follow the canonical predicate (R3-F8): resolved rows
  never block, only unresolved `in_flight`/`uncertain`/`unidentified` rows block,
  confirmed/failed/skipped are terminal history, and an unrecognised status is a
  `malformed_outcome` conflict rather than silently safe history.
- Migration preserves the complete bounded audit and fails loudly on excess
  (R3-F3): per-date review history with authored exclusions and attendance
  decisions, legacy plan operation contents and preconditions, approval/revocation
  timestamps, and per-category capacity bounds (`migration_capacity`) that reject
  the whole candidate instead of silently truncating.

### R3 explicit migration and read-only plans (`0.1.0-dev.7`)

- Added explicit, repeat-safe migration of confirmed legacy workflow schema-1 records. `migration_preview{dates}` validates a bounded explicit date selection and returns counts, conflicts, source hashes and a `migration_id` bound to the source bytes, dates/timezone and current durable target state. `migration_apply{migration_id}` re-reads and re-hashes every source, recomputes the candidate, rejects stale/conflicting input without mutation, and activates the whole candidate in one atomic state write through the existing durable path. Activation requires no opened day.
- Migration reads only configured, date-named regular files under `<legacy_root>/workflow/`; no recursive discovery, request-supplied paths, symlinked workflow directory/file, or writes into the legacy root, and the old store constructor is never called. The documented precondition is quiescent legacy writers; changed source bytes are detected before activation rather than pretending an unlocked multi-file read is atomic.
- Preserved through migration: confirmed work and IDs (or recorded deterministic mappings), projects/descriptions/classifications, evidence references plus non-secret provenance pointers, legacy revisions/`updated_at`, stable local/remote mappings with the binding-versus-last-observed distinction, submitted outcomes, legacy plans and approvals, and unconfirmed carry fragments. Raw collected provider documents are not imported. `uncertain`, `unidentified` and unresolved `in_flight`/`failed` outcomes remain durable blockers that R3 never replays or clears; historical approvals are inert (`usable: false`). Repeated identical source bytes are a `no_change` result with no duplicate data, while changed sources and occupied destinations are conflicts.
- Added a disabled-by-default read-only Gradion boundary over the existing MCP transport. It verifies exactly `timesheet__get_my_week` and `timesheet__get_classifications`, does not require write tools, exposes no generic call operation, and resolves the credential by NAME only (`GRADION_MCP_TOKEN` by default) with the existing dotenv/inherited precedence. Unrecognized shapes, reference envelopes, failed reads and partial coverage are explicit non-empty failures; a failed observation replaces the stored complete observation.
- Added `classifications{query}` with a bounded catalog identity and `select_classification{day,revision,interval_id,catalog_id,classification_id}` for an option the catalog actually returned. A partial/filtered catalog cannot prove an option absent; defaults and migrated hints are suggestions, never silent selections.
- Added `reconcile_remote{day}`, explicit `resolve_remote{day,revision,observation_id,remote_id,local_id,decision}` (`adopt`/`accept_observed`/`foreign`) and `preview_plan{day}`. A fresh read never binds an identity; adoption is a human one-to-one decision against a specific observed entry; changed mapped content requires explicit acceptance; stale revision/observation IDs are rejected. Plans render exact create/edit operations with stable identities and observed preconditions, omit unchanged mapped work, preserve remote-only fields on edits, never delete, never silently shorten intervals, and are content-addressed so audit timestamps alone do not change the plan. Identical fresh observations keep a stable `plan_id`; actual local/remote changes invalidate it.
- State schema `2` adds durable `migration`, `remote` and `plans` sections. A schema-1 file is validated in its own shape and upgraded in memory; schema `2` is activated only by a successful durable save/transaction. `status` reports `store_schema`/`durable_schema` and compact counts. A schema-1-only loader fails closed on a schema-2 file (`state_invalid`, file intact), and a schema-2 file claiming approval authority is rejected.
- New stable error codes: `migration_disabled`, `migration_invalid`, `migration_stale`, `migration_conflict`, `session_open`, `remote_disabled`, `remote_unavailable`, `remote_incomplete`, `remote_unreadable`, `stale_observation`, `unknown_remote`, `unknown_local`, `identity_conflict`, `unknown_classification`, `stale_catalog`, `no_confirmed_work`, `dirty_session`. `version` reports `0.1.0-dev.7`; protocol schema remains `1` with additive operations.
- R3 limits: no approval, submission, remote write, delete, retry, Pi adapter, macOS/live-account claim, or R4 execution. `preview_plan` distinguishes `ready` from `blocked`; it can never produce an approved plan.

### R2 F7 correction (`0.1.0-dev.6`)

- Submitted-review collection no longer discards reviews that were already verified when a later candidate pull request's reviews request fails. The family still reports `failed` — the failed-source gate is unchanged — but its `count` and retained topics now reflect the verified reviews, and the detail names the HTTP failure, the number retained, and states that remaining review coverage is unverified. A search-phase failure still returns no items because none have been verified at that point. `version` reports `0.1.0-dev.6`; protocol and state schemas remain `1`.

### R2 second repair (`0.1.0-dev.5`)

- Attendance decisions are now attempt-bound for every action: `decide_meeting{day,meeting_id,decision,collection_id}` requires the exact reviewed calendar collection ID. A missing ID fails `bad_request`; an ID that is not the day's current calendar attempt fails `stale_collection`; both reject without mutating evidence, so an action built from an earlier review can no longer land on a rescheduled target. This is an intentional breaking request requirement for the unreleased protocol (protocol schema remains `1`): callers must send the `collection_id` from `collect` or the review's `sources` entry. Meeting references additionally fingerprint the current target, including an overridden occurrence's rescheduled start, so a moved instance no longer shares an unchanged RECURRENCE-ID-only reference. Carrying an already recorded decision across recollection for an unchanged instance (`UID|Start`) remains separate from authorizing a new action.
- Calendar date-only correctness: all-day occurrence and boundary instants are resolved from their declared DATE values with the accepted first-real-instant day resolver, date-only recurrence sets expand on civil dates, and the exclusive end is resolved from its own date. A Santiago event declaring September 6-7, 2026 appears only on September 6 with DATE labels `2026-09-06`/`2026-09-07`, and is absent on both September 5 and the exclusive-end September 7; no `01:00` boundary is carried into the end date. Recurring, RDATE/EXDATE and overridden date-only paths use the same rule; all-day overrides with DTEND no longer fail end resolution.
- Recurrence candidate selection is span/duration-aware: the expansion look-back is derived from each series' occurrence duration (timed) or calendrical span (date-only), with exact interval overlap deciding membership. A weekly three-day all-day occurrence October 19 through exclusive October 22 is present on October 21 and absent on October 22; a monthly timed occurrence October 1 09:00Z through October 11 09:00Z is present on October 10. A real expansion bound (500 instances) still reports incomplete coverage instead of claiming the omitted instances complete.
- Historical review discovery no longer equates a resource's last-updated date with a review submission date: candidate PRs are the repository-wide `reviewed-by:` superset with no `updated:` date qualifier, and each candidate's reviews are filtered by their exact `submitted_at`. A PR reviewed on the selected day and updated later still yields that review; out-of-window reviews stay excluded. Candidate-page, candidate-count, search-total and `incomplete_results` bounds are reported as `incomplete` with honest counts.
- Commit attribution no longer falls back to a commit-author email when the response has no GitHub user document. Unverified ownership is omitted and reported as a coverage limitation in the commits family detail and observable through the source status; legitimately verified identities (user document, `user.login`, reviewer `user.login`, `actor.login`) remain supported, and no email registry or new identity subsystem was added.
- `version` reports `0.1.0-dev.5`. State schema remains `1` with no new durable fields; existing state loads unchanged. No release tag, publication, or migration is created or implied by this unreleased build.

### R2 evidence collection (`0.1.0-dev.4`)

- Added configurable source collection over HTTPS-reachable providers, driven by a new `collect{day}` protocol operation: GitHub (six REST families — commits, `author:`-filtered PR search, submitted PR reviews via `reviewed-by:` search candidates verified at `pulls/{pull_number}/reviews`, repository-wide PR review comments, issue comments, and the bounded events/discussions feed) and calendar feeds (ICS; full recurrence sets, overrides, attendance PARTSTAT). Attribution is strictly by provider-named actors verified inside provider responses (commit user document or author email, `user.login`, reviewer `user.login`, `actor.login`); a response naming another actor is dropped, and `updated_at` only selects search candidates, never attributes activity. Pagination runs to exhaustion with honest bounds — family page cap `maxGithubFamilyPages` (20), the documented search 1,000-result/`incomplete_results` cap, the submitted-review candidate cap (`maxReviewPRCandidates`), the documented events window (30 days, 300-event feed cap), and the 50-item per-topic cap (overflow is recorded on the topic as `truncated` with a dropped-item count and makes the source `incomplete`) — always reported with status-only details.
- Canonical provider identity: the same action seen through two endpoint families (issue comment, PR review comment, submitted review) carries one reference and merges provenance; distinct interactions stay distinct. Topics group by retained provider resource identity (`resource`), not rendered titles.
- Collection is verify-only: evidence proposes topics and meetings but never creates worked minutes. Explicit attendance decisions (`pending`/`attended`/`skipped`) are separate from calendar PARTSTAT and from timesheet confirmation.
- Added the incomplete-source review gate: `confirm` and `save` fail closed with `source_unacknowledged` while an enabled source's attempt for the day is not `complete` and not acknowledged — including the never-collected state, `incomplete`, `failed`, and `missing_credentials`; a never-collected source yields a structured gate error (no crash), and disabled/manual-only R1 behavior is unchanged. `acknowledge_source{day,source,collection_id}` requires and verifies the exact reviewed collection attempt ID; missing IDs fail `bad_request` and stale IDs fail a new `stale_collection` error without mutation, so a second collection cannot be silently acknowledged from a stale review action. Meeting decisions verify the meeting's current evidence identity and optionally the reviewed `collection_id`.
- Review decisions are copy-on-write at the store boundary: failed pre-activation evidence writes (`write_failed`) leave review state, disk, gate state, and absence of evidence keys exactly as they were; a failed first collection can no longer leave a null evidence entry that a later successful write persists into an unreadable state (`state_invalid` after restart). Post-rename uncertainty (`write_uncertain`) is unchanged.
- Calendar day-window correctness: all-day DTEND is exclusive (a one-day event with DTEND the next date no longer appears on that next date), and cancelled/overridden instances are selected by their replaced-or-own windows rather than being emitted on every unrelated day — while cancelled series/overrides and all-day evidence remain proposals with their provenance.
- Recurrence-set correctness with the pinned `teambition/rrule-go v1.8.2` and `arran4/golang-ical v0.3.7` (dependencies unchanged from the R2 base commit; `go.mod`/`go.sum` untouched): instances are RRULE ∪ RDATE − EXDATE with per-property TZID/value-form handling and de-duplication, for timed and all-day series; an unresolvable or form-mismatched RDATE/EXDATE value records an incomplete-coverage issue instead of silently claiming complete evidence. Wall-clock recurrence across DST transitions is unchanged.
- State schema remains `1`; the optional `Evidence` map gains only additive optional fields (verified acknowledgment identity receipts, item `resource`, topic truncation counts, decision `collection_id`). A pre-R2 binary still fails closed (`state_invalid`) on a file this build writes; pre-R2 files load unchanged.
- New protocol receipts and errors: `collect`, `acknowledge_source` (now with verified `collection_id`), `decide_meeting`; new error code `stale_collection`. Error details carry HTTP status only; feed URLs, tokens, and raw provider documents are never echoed.
- Configuration gains an optional `sources` block (GitHub `user`/`repositories`/`api_base`, calendar feed user, `required_env` extension); a config without `sources` behaves exactly like R1 (`no_sources` collection, no gate).
- The development entry point's usage text now describes the implemented collection scope; the `serve` protocol in [docs/agents/rewrite-interface.md](docs/agents/rewrite-interface.md) is the authoritative interface.
- R2 limits unchanged from R1 beyond the above: no migration, Pi adapter, remote reconciliation, submission approval, or remote write; evidence never creates worked minutes.

### R1 timezone continuation (`0.1.0-dev.3`)

- Fixed local day-boundary resolution when a transition skips midnight: `time.Date` normalizing a nonexistent `00:00` into a neighboring date is no longer treated as proof that the civil date is absent. An existing date now starts at its first real instant, so ordinary work on such a date is accepted and allocated correctly (for example 09:00-10:00 on 2026-09-06 in `America/Santiago` and 2026-03-08 in `America/Havana`, 60 minutes each). A wholly skipped civil date is still not reported as work. A cross-midnight interval spills with real local continuation labels: Santiago 2026-09-05 23:00-01:30 totals 90 minutes, split 60 on September 5 and 30 on September 6 labeled 01:00-01:30, and totals agree between save and restart views. Wall times in the missing midnight hour still fail `nonexistent_time`; no zone or date special cases and no new dependencies were added.

### R1 repair (`0.1.0-dev.2`)

- Save validation now uses the candidate durable store: persisted state for other dates plus the requested day's confirmed intervals. Unsaved confirmations on other open days can be planned and reviewed in-session but are never saved implicitly and cannot make a save appear valid. A save receipt describes exactly what restart will load for that day. A dependent save that would still overlap fails with `overlap` and an actionable conflict message.
- Fixed explicit removal of a day's final interval. `confirm` accepts an empty staged draft when the day already had confirmed work, records a zero-interval revision and history entry, and `save` persists it. Cancelling a staged removal still restores the confirmed interval.
- Interval validity, elapsed duration, overlap, and midnight allocation now use each day's recorded IANA timezone. Spring-forward intervals use elapsed time (for example 60 minutes for 01:30-03:30 in `Europe/Amsterdam`); fall-back intervals include the repeated hour when unambiguous. Nonexistent local times fail with `nonexistent_time`; repeated-hour endpoints fail with `ambiguous_time` rather than guessing. Existing saved days keep their recorded timezone when configuration changes.
- Added a test-only, narrowly scoped fault-injection seam and split save failures at the rename boundary. Pre-rename failures return `write_failed`, preserve the previous file, and roll memory back for a safe retry. Post-rename directory-open/sync/close failures return `write_uncertain`, keep the activated in-memory snapshot, block blind retries, and surface `durability_uncertain` in `status` until restart.
- State schema remains `1`; the build adds stricter timezone validation. Unreleased `0.1.0-dev.1` state containing a saved nonexistent or ambiguous wall time now fails `state_invalid`; no automatic migration is provided.
- R1 limits are unchanged: no GitHub/calendar collection, migration, Pi adapter, remote reconciliation, submission approval, or remote write.

### Local daily workflow (R1, `0.1.0-dev.1`)

- Added the development entry point `cmd/timesheet-rewrite` and `internal/rewrite` for the local, review-first daily workflow. This is not a release and not a second production CLI.
- Added a persistent line-delimited JSON `serve` session with strict request schemas, echoed request IDs, expected revisions, stable error codes, and protocol schema `1`. `version` reports the development build identifier and loads no credentials.
- Added manual typed intervals (`DATE HH:MM-HH:MM PROJECT[/CLASSIFICATION] DESCRIPTION`), staged add/edit/remove, review with accurate totals, explicit confirmation, atomic local save, revision, cancellation, status, and restart recovery.
- Rejected zero/negative/invalid intervals and unresolved overlaps without changing confirmed work. Multiple evidence references add no minutes. Cross-midnight work is allocated explicitly to both local dates without wrapping or double-counting.
- Added binary-only JSON configuration and environment-file loading with inherited environment precedence, documented dotenv syntax, lazy required-variable checks, and value-free diagnostics. Local save requires no remote credential.
- Added single-writer state locking, atomic `0600` state replacement, and failed-write rollback so cancellation or a failed write cannot report success or erase the last confirmed state.
- Documented the R1 public interface in [docs/agents/rewrite-interface.md](docs/agents/rewrite-interface.md) and added placeholder-only environment examples.
- R1 limits: no GitHub/calendar collection, migration, Pi adapter, remote reconciliation, submission approval, or remote write. No legacy store is read, rewritten, or deleted.

### Project kickoff

- Accepted the [timesheet rewrite specification](docs/timesheet-rewrite-spec.md) and prepared an owner-mediated implementation plan and first worker packet.
- Targeted a persistent compiled Go workflow with local Pi review, GitHub/calendar evidence, user-supplied working intervals, explicit migration, and exact-plan submission approval.
- Replaced the proposed Keychain-first credential flow with binary-only `.env.local` loading and process-environment overrides.
- Added Git exclusions for `.env.local` and environment-specific local credential files. Credential contents were not inspected.

### Release policy

- Initial rewrite target: `0.1.0`, with development/alpha/beta/rc identifiers as documented in the [execution plan](.omo/plans/timesheet-rewrite.md).
- No rewrite application implementation, release artifact, tag, or production readiness is claimed by this kickoff.
- Public-interface compatibility covers CLI/input grammar, Pi protocol, configuration names/precedence, and state migration. Stable `1.0.0` requires accepted end-to-end behavior and an owner release decision.
