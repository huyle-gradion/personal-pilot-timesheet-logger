WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: preserving verified partial evidence without weakening incomplete-source gates
SCOPE: TIMESHEET-R2-F7, one error-path correction plus dev-build bookkeeping
VERIFICATION: focused real-CLI partial-failure, persistence and acknowledgment proof

# Authorized F7 correction

Status: executed for dev.6 and accepted after focused independent re-review; see [R2 acceptance](timesheet-r2-acceptance.md). Historical packet, not current repair authority. The proposed next R3 packet requires separate owner release.

The owner explicitly authorized this bounded correction after independent technical adjudication upheld F7. This is not automatic permission for a third broad repair round. R2 remains unaccepted and R3 blocked until focused independent re-review. Owner-mediated execution; no child dispatch, commits, push, tags, publication, live access or remote writes.

## Authority and starting state

Branch `496a6ab8a81e9418a25046995ae40e2b8713e018`; packet base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve all owner changes and prior repairs. Do not switch branches or restore files from the base commit. If the named source differs from the frozen inputs beyond recorded owner changes, stop with a precise drift report rather than overwriting it.

Frozen reference `/tmp/opencode/r2c-snapshot-20261022T161203Z/` stays untouched. Binary SHA-256 `c226acad53397f5b4cf90111a395645fcab7b456636e23da9fcb55142ca2b56a`; `MANIFEST.sha256` SHA-256 `115b8e382a6e5dbce5ecde7ac566af25174f4d579360e73c62271a49fca232b1`. Consult the manifest for exact packaged paths; do not enumerate the directory.

State exposed tools, delegation permission and inline rationale once. Current permission excludes child dispatch; this single-site fix does not need specialists. Do not inspect private configuration to discover capabilities.

## Bounded reads

- This packet and `docs/orchestration/README.md:5-24` for current authority.
- `internal/timesheet/github.go:510-630`, especially 603-606. Additional ranges in this same file only if needed to understand existing helpers or aggregation.
- `internal/timesheet/model.go:23-27,775-790`; `FamilyReport.Count` means retained attributable items.
- `CHANGELOG.md:1-16`; `docs/agents/timesheet-interface.md:1-6` for version bookkeeping.
- For runtime request/receipt questions, scoped search/read only in `docs/agents/timesheet-interface.md` and `internal/timesheet/session.go`. Relevant gate/acknowledgment source ranges are 1385-1429 and 1575-1617; do not reopen calendar work.
- Observed evidence `/tmp/timesheet-r2c-review-2jle0_6u/boundary-evidence.json`.
- Technical adjudication `/tmp/timesheet-r2-technical-review.CmxguP/REVIEW.md:72-107`, only if more explanation of the approved correction is needed.

No transcript, real configuration/store, `.env.local`/other real dotenv, credentials, ai-database, host Git settings, raw provider exports or unrelated files. No repository-wide discovery or shell `cat`, `ls`, `find`/equivalent search. Name any genuinely missing prerequisite rather than exploring outside these bounds. The binary, not the agent, loads generated synthetic configuration.

## Exact write allowlist

1. `internal/timesheet/github.go`: only the review-fetch error branch currently at 603-606.
2. `internal/timesheet/model.go`: only `AppVersion`, `0.1.0-dev.5` to `0.1.0-dev.6`.
3. `docs/agents/timesheet-interface.md`: only application-version line 3, currently stale at dev.4, to dev.6. No public semantics change.
4. `CHANGELOG.md`: one small new Unreleased dev.6 entry describing retained partial reviews and unchanged failed-source gate; preserve historical entries.
5. Optional new `internal/timesheet/f7_test.go`: focused consumer-visible partial-failure regression coverage only. Tests are permitted, not required.

All other repository files are frozen, including existing tests, collector logic outside the one branch, schemas, config, store, session, CLI, dependencies and orchestration documents. Hash-only before/after checks are permitted for `go.mod`, `go.sum`, `internal/timesheet/{calendar,config,session,store,repair_test,repair2_test}.go`, and `cmd/timesheet/main.go`; do not text-read their unrelated contents. No dependency installation or edits.

## Correction

Verified review items accumulate in `items`; the later fetch-error branch incorrectly returns `nil` while reporting `Count: len(items)`. Return accumulated `items` with that existing report. Preserve `StatusFailed` and `Count: len(items)`; do not downgrade failure to a different status.

Within that same branch, a bounded detail sentence may state how many reviews survived and that remaining review coverage is unverified, while retaining the existing sanitized provider error. Do not include URLs, tokens, provider bodies or invented remainder counts. Use existing formatting/truncation conventions.

Do not alter the legitimate search-phase `nil`/zero-count failure at 548-549. No retries, new provider requests, fallback attribution, aggregation workaround, status vocabulary, schema, gate or broad helper refactor. F1-F6 behavior stays intact. Effective Go applies; TigerStyle/former Go rules and mandatory tests do not.

## Runtime proof

Use the existing pinned Go toolchain. After edits, format the touched Go files, build the actual development executable into a private temporary artifact directory and run scoped static checks once. A focused new test is optional; do not rerun broad suites or the reported host-sensitive legacy Git failure. Do not rerun the old broken binary merely to confirm the already established failure.

Drive the repaired executable against loopback-only synthetic GitHub responses. Use fresh synthetic config/state, an explicit generated empty env-file and a minimal child environment with a synthetic `GITHUB_TOKEN`; never fall through to real settings. Config needs only UTC, private `state_path`, project `P1`, `sources.enabled:true` and GitHub enabled with user `reviewer`, repositories `["o/r"]`, and `api_base` set to the fixture's loopback URL. Calendar is disabled. Provider families outside the target return honest empty/bounded results.

Keep phases within one persistent process until a deliberate restart. Seed confirmed manual work using the successful control and acknowledge any legitimately incomplete source first, so subsequent confirm/save probes have otherwise valid prerequisites. Then collect a new attempt where PR #7 returns actor-matched, in-window review 707 and PR #9 returns HTTP 503. Assert actual receipts/state transitions, not assumed revisions:

- Review topics contain `gh:o/r:review:707`; exactly one submitted review is retained, family count is 1 and family status remains `failed`. Detail identifies the HTTP failure and makes remaining coverage unverified.
- New incomplete collection requires acknowledgment again. With otherwise valid manual state, both confirm and save reject `source_unacknowledged` before acknowledgment.
- Acknowledge the exact current collection ID; valid confirm/save then succeed without deriving minutes from evidence.
- Close/restart using the same synthetic store: the review ref, truthful count, failed family status and manual work total survive.
- Successful single-review control remains count 1/status `complete` for that family. Do not require the entire source to be complete if another family's documented historical bounds make it incomplete.
- `version` and protocol `status.app_version` identify dev.6; protocol/state schema stay 1.

Optional permanent coverage should catch a real retained-evidence/count regression, including a later candidate or page failing; do not add source-text, wiring or mock-echo tests. Do not expand into the already closed calendar matrix or fault suite.

## Evidence and stop

Keep rollback and final snapshots restricted to allowed files and selected hash metadata; never archive the whole working tree or include real env/config/records/transcripts. Retain the actual compact protocol evidence, assertion summary, binary/version/hash, bounded source diff against the frozen copies and frozen-file hash comparison in a new private `/tmp/` artifact directory. Do not dump provider bodies. Remove throwaway drivers/fixtures and stop services after preserving sanitized evidence.

Report exact artifact paths, changed files, observed checks, source count/status/ref after restart, gate-before/after-ack result, manual total, version and limits. Do not label prior 105 assertions as re-executed. No macOS/live support claim. Stop review-ready for focused independent re-review; do not self-accept R2, advance to R3, dispatch children, commit or initiate another repair on a new finding.
