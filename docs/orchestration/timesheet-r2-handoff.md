WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: private source credentials, provider coverage, calendar/timezone semantics, durable review state
GOVERNING ARTIFACT: docs/timesheet-spec.md
SCOPE: TIMESHEET-R2, GitHub and calendar evidence only
VERIFICATION: real compiled process against hermetic HTTP services and synthetic dotenv/state; no live account access

# R2 evidence collection packet

Historical owner-mediated implementation packet, already executed. After the first repair, [the current re-review](timesheet-r2-rereview.md) requires only the named continuations. Use [the second repair packet](timesheet-r2-continuation-handoff.md), not this original implementation or the completed broad repair. R3 remains blocked.

## Base, snapshot and authority

Stay on `496a6ab8a81e9418a25046995ae40e2b8713e018`, base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve kickoff and accepted R1 changes. Reconcile ownership before editing; take a sanitized rollback snapshot excluding real dotenv/config/records, private transcripts and ai-database.

Accepted snapshot: `/tmp/opencode/r1-tzfix-final-20261022T150356`. Binary SHA-256: `d7318cf3d38631e657688c3a5808aa6efc117dbf765e4d78524b608b81b7dab7`; `hashes.txt` SHA-256: `2bf7dc7c6b4b9c9d75135f8ed5959d25f4eedc9ab23ac7f1679f5a6aac4a2b09`. The acceptance record pins unchanged session/store/config/entry-point files to the preceding reviewed snapshot as well.

Authority when the owner runs this packet: R2 implementation, public first-party documentation research, and hermetic local execution. No child dispatch, commit, push, issue/PR mutation, tag, release, live GitHub/calendar/Gradion calls, credential retrieval, private config/record reads, or vault access. Never open `.env.local`; use a synthetic environment file in a private temporary directory.

## Targeted reads and allowed writes

Read the accepted spec's evidence/config/token/durable-state sections, R1 acceptance, `docs/agents/timesheet-interface.md`, `go.mod`, the placeholder `.env.example`, and the exact affected R1 files. Do not load the private transcript or historical orchestration ledger. Consult current official GitHub API and calendar/library documentation before implementing provider behavior; record the APIs and coverage limitations actually verified. Public documentation access is allowed, live account access is not.

Allowed writes:

- `internal/timesheet/github.go` and `internal/timesheet/calendar.go`.
- `internal/timesheet/config.go`, `model.go`, `store.go`, `session.go` for necessary R2 integration only.
- Optional deterministic behavior tests under `internal/timesheet/*_test.go`.
- `.env.example`, placeholders only.
- `docs/agents/timesheet-interface.md` and `CHANGELOG.md`.

Do not change the production CLI, development CLI entry point, legacy packages/stores/skills, governance documents, `go.mod`, or `go.sum`. Existing pinned calendar dependencies are available; prefer standard-library HTTP/JSON. A new dependency or extra write path requires an explicit scope decision. Inspect references before changing exported symbols when language-server references are available.

## Required behavior

### One binary-owned collection flow

Add a documented strict `collect` request for an opened day that performs configuration checks, fetches the enabled sources, groups evidence and persists collection results without confirming work. The launcher still supplies fixed paths; no request accepts arbitrary URLs, paths, credentials or executable names. Existing R1 manual-only configuration and operations must continue to work with sources disabled.

Implement exactly two source integrations, not a generic plugin framework. The compiled binary owns all provider work. One collection call must not require the model to fetch a catalog, parse exports, open state files or orchestrate pages. Return a compact collection status/count summary; full local review may expose structured topics/proposed meetings, never raw provider documents or credential values.

### GitHub

Use explicit configured repositories and the configured user's attributable activity, covering all owner-selected scopes:

1. Commits and authored PRs.
2. Submitted PR reviews.
3. Issue activity/comments, discussions and other attributable repository interactions.

Document the concrete activity-family inventory and verified API coverage. Do not silently reduce this to PRs or a single event feed. Combine provider endpoints as necessary; exhaust supported pagination and surface retention, search bounds, missing historical detail, rate limits, permissions and unavailable activity families as incomplete coverage. A nonempty response is not proof of completeness.

Preserve stable provider identities, actor, original event time, repository/resource links and provenance. Filter on attributable activity within the recorded day's calendrical timezone window; do not claim another actor's update as the user's work. Group related commits/PR/review/conversation evidence into Topic Candidates and de-duplicate the same provider event without deleting distinct supporting provenance. Topics have unknown worked duration; timestamps and gaps never become working intervals.

### Calendar

Use the configured private ICS feed, resolved inside Go. Handle UID/recurrence identity, recurrence rules, exclusions and overridden instances, original event timezone/UTC timestamps, all-day events, cancellation and invitation/attendance metadata. Select by overlap with the recorded day's actual local window; preserve original spans and provenance for events crossing midnight.

Produce proposed meeting intervals, never confirmed work. Cancellation/decline must not silently become attendance. Provide an explicit local review decision for attendance and keep invitation status separate from the user's attendance declaration. All-day or otherwise untimed records must not invent worked hours. Use the accepted timezone/day-boundary implementation rather than another incompatible date-window convention.

### Credentials and safe failure

Use the existing public template names `GITHUB_TOKEN` and `GCAL_ICS_URL`. Only Go resolves them from the supplied dotenv file or inherited environment, with inherited values taking precedence. Missing inherited-only/file-only/empty-override cases must behave consistently with R1. Do not add Keychain access or shell/CLI credential probes.

Non-secret source configuration is read only by Go. Document its schema, enabled-source behavior and identity/repository/feed settings without returning a config dump. Trusted configured endpoints may target local HTTP services for verification; no model-supplied endpoint or path field is allowed.

Sanitize network and parser errors; HTTP errors often include the requested URL. Never persist or echo the private feed URL, Authorization header, credential value, raw dotenv line, or raw ICS/HTTP document. Keep full provider payloads out of model-facing responses.

Each source has a visible result/completeness state. If one fails or a required activity family is incomplete, retain successful-source evidence and show the failed/missing coverage. Permit explicit acknowledgment of the named source attempt and manual completion, never automatic acknowledgment. Bind acknowledgment to the reviewed source attempt, not standing permission. Document and enforce the incomplete-source review gate while preserving manual-only R1 operation when no sources are enabled.

The acknowledgment/attendance protocol is for trusted local user commands; it must not become a model preparation-tool flag. Acknowledgment, attendance, confirmation and submission approval remain distinct.

### Persistence and compatibility

Persist source identities, provenance, Topic Candidates/proposed meetings, completeness and explicit review decisions through restart. Collection/refresh never confirms work, applies project/classification suggestions, changes entered times/descriptions, or loses staged or confirmed user work. Recollection must be repeat-safe and must not duplicate candidates or minutes.

Keep confirmed local state/history and the F1-F4 guarantees intact. Adding evidence fields must not make dev.3 records unreadable, reset revisions, or discard intervals. Document and prove any state upgrade; fail closed on unsupported newer schemas. Do not implement legacy-store migration or remote approval structures in R2.

Update application identity and interface/changelog consistently for the unreleased build. Do not create a release tag or claim Pi/macOS/live-service support.

## Runtime evidence after editing

Build and run the actual executable against private local HTTP services with synthetic GitHub JSON, ICS, dotenv and state. The services exercise production collectors; they are not runtime fallbacks. Permanent tests are optional. Run checks after the complete slice, not after each edit.

Demonstrate:

- Multi-page GitHub activity from every selected scope, unrelated actors/repositories outside the result, boundary timestamps, grouping/provenance and repeat collection without duplicates. An unavailable/bounded activity family must produce honest partial coverage, not fake completeness.
- Calendar recurrence plus exception/override, timezone/day-boundary selection including skipped midnight/DST, cancellation/decline, an all-day item and a cross-midnight event. No automatic attendance, inferred work duration, or double-counted confirmed work.
- One source succeeding while the other fails; no implicit acknowledgment, a deliberate named-attempt acknowledgment permits manual completion, and a new source failure is not pre-authorized by an old acknowledgment.
- Explicit attendance decisions and source/evidence status survive restart. Refresh preserves entered/staged/confirmed intervals and descriptions.
- File-only and inherited-only synthetic credentials plus an empty inherited override. Synthetic secret/feed-URL markers must not appear in errors, stdout, durable state or reports. Report checks without printing markers or secret values.
- An existing dev.3 synthetic store loads and retains its intervals, history and revisions; enabled-source collection does not break the ordinary 105-minute R1 save/restart path or F1-F4 boundaries.
- Final collection status, actual local review output, source decisions and save/restart receipt from the same persistent binary process where applicable. No LLM, real account or repository search by the daily agent is needed.

Keep sanitized output and exact source/binary hashes; remove throwaway drivers after proof. Clearly distinguish CLI evidence, optional in-process tests, source-only reasoning and unavailable live/macOS checks.

## Report and stop

Return the exact branch/base/snapshot, written files, verified API/coverage inventory with primary-source references, public protocol/config/env/state changes, commands/output, acceptance results and remaining limits. Do not call R2 complete if only collection wiring, fixtures, one source, or a narrowed activity scope works.

Stop review-ready for independent review. No self-acceptance, R3, commit, push, publication or live access.
