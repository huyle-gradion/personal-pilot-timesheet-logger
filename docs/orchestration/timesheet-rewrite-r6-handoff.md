WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: high
RISK BASIS: destructive source retirement, migration/approval preservation, cross-runtime cutover and unavailable platform proof
GOVERNING ARTIFACT: issue #137; docs/timesheet-rewrite-spec.md; .omo/plans/timesheet-rewrite.md
SCOPE: TIMESHEET-REWRITE-R6 source cutover and hermetic integrated proof; no release or live access
VERIFICATION: actual production-named Go binary plus pinned Pi TUI; tracked-only retirement proof; separate mandatory macOS readiness gate

# TIMESHEET-REWRITE-R6

Status: historical R6 implementation contract. Dev.17 is independently reviewed: Standards pass; Spec requires two P2 documentation corrections, with no observed executable cutover defect. Continue only under [R6-FIX1](timesheet-rewrite-r6-fix1-handoff.md); [review](timesheet-rewrite-r6-review.md). The original broad implementation scope below is not permission for more source edits. Owner-mediated, no children/commit/publication/live access; full R6 acceptance still requires actual macOS proof. Cached complex-critical routing here describes the original implementation, not the docs-only repair.

## 1. Baseline, permissions and ownership

- Repository `/home/thearesia/Projects/personal-pilot-timesheet-logger`; prepared branch `feat/timesheet-rewrite-r6-v0.1.0`, HEAD `99a7f23336845ac6858706319e695a750f9116cd`. R5 is PR #140, stacked on R4 #139 and R1-R3 #138. Do not switch onto or push any earlier accepted branch or main.
- Target **`0.1.0-dev.17`**, protocol 1/state 3, exact existing Pi/TUI/typebox pins 1.4.0/1.4.0/1.3.27. Development identity only; no tag/release, RC/stable label or new schema.
- Authorized: the exact source cutover/retirement below, public operator docs, hermetic local runtime and Darwin cross-builds. Unit/integration tests are optional and allowed, not a substitute for runtime proof. Follow Effective Go; TigerStyle/go-rules are not mandatory.
- Forbidden: staging/commit/push/PR creation or edits, merge/retarget/tag/release, R7/other features, live accounts/model/provider calls, credentials/config/stores/transcript/owner Pi profile/Keychain/vault reads, ai-database writes or real timesheet mutations. Only explicit synthetic-loopback approvals may dispatch synthetic writes.
- Preserve existing owner-governance diffs in `.omo/plans/timesheet-rewrite.md`, `docs/orchestration/README.md`, R4 acceptance, and all untracked owner work. Packet/inventory/other orchestration files are read-only to the worker. Do not open `transcript_full.jsonl`, `.env.local` or real config/records.
- State observed tools/capabilities and the no-child inline decision once. Tool availability is not dispatch authority. One source writer; no automatic restart of an exhausted repair loop.

## 2. Exact cutover scope

Read the compact [inventory](timesheet-rewrite-r6-inventory.md). Its JSON is **machine input only**, not an export to open in model context: SHA-256 `29248a6901c47fab2fa526e0c0477d6e2cc72c673b6a8d23a6710601de200310`. Reconcile each selected path's baseline object/type against current ownership before changing it. Stop on unexplained changes; never discard them. Use the tracked-only rollback named in the inventory and preserve its bytes; take a selected-file pre-edit snapshot if any authorized dirty source exists.

1. Replace legacy `cmd/timesheet/main.go` with the accepted 99-line server entrypoint from `cmd/timesheet-rewrite/main.go`. Brand usage/version/diagnostics as `timesheet`; retain only `version` and `serve --config --env-file` and their existing exit/credential-loading behavior. No alias, legacy flags, wrapper CLI, second server or copied business logic. Delete the temporary entrypoint after the move.
2. Replace `cmd/timesheet/timesheet.example.json` with a minimal valid public rewrite-schema example using synthetic placeholders, not owner settings. Retire every other `retire` row (225 tracked files): obsolete commands/packages/tests, old product skill and symlink, vault sync/generator source. Remove **only manifest-listed tracked files**, not directories wholesale. Never use `rm -rf`, follow symlinks, or delete untracked records/fixtures. Remove obsolete tests with their product; never re-pin wording/source/default assertions.
3. `internal/rewrite/model.go`: AppVersion only → dev.17. `pi/transport.ts`: APP_VERSION only → dev.17. `pi/package{,-lock}.json`: root version stamps only. Do not alter dependency versions/integrities, transport validation, disposal/UI ownership, approval selection or display-cell logic.
4. `go.mod`/`go.sum`: prune only dependencies made unused by retirement with the installed Go toolchain; no upgrades, toolchain/go-directive change or replacement dependencies. `internal/rewrite` and `internal/mcp` implementation/tests remain unchanged except AppVersion. A missing capability requires a report and scope decision, not a core patch.
5. Add root `README.md` with one production build/launch path, fixed config/env paths, local `/timesheet` flow, explicit migration/reconciliation before writes, recovery/no-replay rules, and honest readiness. Update `pi/README.md`, current interface identity/commands, `docs/agents/credentials.md` and `CHANGELOG.md`. Correct the stale R4/dev.11 interface heading. Mark superseded stage descriptions historical; do not advertise old commands/skills/Keychain-only credentials as active.
6. Keep `.env.local` binary-owned. Preserve accepted inherited-env-over-file precedence including inherited-empty, explicit configured paths and source independence. No config/credential values enter model context or error text. MacOS-first means platform verification, not permission to revive legacy Keychain fallback. Do not edit `.env.example`, launcher, Pi workflow/view, accepted core/tests, historical docs/ADRs/archives/CSV, unrelated maintenance scripts or owner data.

Optional new permanent test path: `cmd/timesheet/cutover_test.go`, only for uncertain consumer-visible behavior. No other new runtime files/dependencies/features are authorized. Generated harnesses/fixtures stay private and are removed after sanitized proof; no shipped test hooks.

## 3. Bounded read capsule

Start with this packet and compact inventory; do not dump its full JSON, old packages, state, provider documents or evidence logs into context. Ordinary shell `cat`/`ls`/`find` or equivalents for out-of-scope discovery are forbidden.

- `cmd/timesheet-rewrite/main.go:1-99`; old `cmd/timesheet/main.go:1-38` only if needed for replacement ownership. Other retiring file contents need no LLM read.
- `internal/rewrite/model.go:21-35,126-143`; the `Project` type only if needed for the example. Use available symbol navigation for that type; no full 71 KiB model dump.
- `pi/transport.ts` APP_VERSION declaration only; `pi/package.json`; `pi/package-lock.json` root stamps only. Compare dependency metadata programmatically, not by reading the lockfile.
- `go.mod`; machine-managed Go sum. `.env.example` public names only; `docs/agents/credentials.md` active guidance; current `pi/README.md` build/launch paragraphs.
- `docs/agents/rewrite-interface.md:21-96,883-904` plus only needed migration/reconciliation/approval/recovery and R5 consumer sections by heading. Use these request/result contracts, not guessed shapes.
- Acceptance/qualification references: R5 acceptance and lifecycle review. `internal/rewrite/migration_test.go` fixture-construction helpers only if legacy synthetic shapes cannot be obtained from the interface; do not load whole suites or change them.

Additional investigation stays inside the explicit manifest/allowed docs. Report the exact missing fact before widening scope. Codegraph has no established index here: do not create one. Use supported navigation or the supplied compiler-backed closure; do not repeat repository-wide exploration.

## 4. Final checks and actual acceptance matrix

Run static gates once after coherent changes: gofmt on changed Go files, `go build ./...`, `go vet ./...`, strict tsc on the three existing adapter files, shipped launcher syntax/help, and scoped whitespace check. Use existing installed toolchains and record versions; no silent latest-tool installation. Tests may be run for the retained packages; do not rerun the known legacy `internal/git` environment failure before deleting its obsolete package. No tests are mandatory merely for coverage counts.

Build the real `bin/timesheet` and drive actual pinned Pi through the unchanged trusted launcher with its `--binary` pointing to that executable. Use generated isolated HOME/profile/config/env/state and loopback GitHub/ICS/MCP/model fixtures only. No fixture extension/launcher escape hatch. Capture concise assertions/terminal proof, not raw document/model/state dumps.

| Criterion | Required observed evidence |
| --- | --- |
| Single product cutover | `go list ./...` contains only `cmd/timesheet`, `internal/rewrite`, `internal/mcp`; all retirement rows gone, read-only rows unchanged, no remaining temporary/legacy build target. `timesheet version` (no config/env flags) exits 0 without any resource files; extra version arguments and old commands exit 2 without remote calls. |
| Persistent daily workflow | Same child PID: GitHub/calendar collect, incomplete-source acknowledgment bound to the actual collection, attendance, typed manual work/classification, confirm versus durable save, revise/cancel, and restart recovery with exact minutes/revisions. Retain cross-midnight origin/spill control. |
| Migration after legacy-code removal | Synthetic durable work + remote mappings/history + inert old approval + unresolved outcome: explicit preview/apply/repeat and restart preserve identity/audit, source hashes unchanged. Existing matching remote reconciles without duplicate create; unresolved write blocks dispatch. |
| Exact human approval | Actual TUI renders exact plan; cancel/revoke/stale approval make zero writes; explicitly approved synthetic create/edit verifies body/identity/read-back and survives restart without reuse. No model or forged text grants approval. |
| Uncertain/lifecycle recovery | Applied-but-unacknowledged synthetic write stays unresolved and is never retried/replayed. Explicit fresh-observation recovery only. Pending collect → `/reload` → usable new-day `/timesheet`, delayed old response cannot take focus; pending quit leaves no orphan. Preserve the accepted dev.16 lifecycle repair. |
| Model/token boundary | Direct local workflow = zero model requests. Separate natural-language initiation = one synthetic model request, tool list exactly `timesheet_prepare`, bounded result ≤512 bytes, no follow-up/config/env/state/provider data. Shell/read/search/forged approval remains refused. Report available input/cache/output counters; unavailable is not zero. |
| Platform proof | Darwin arm64/amd64 cross-builds, labelled compile-only. Actual macOS pinned-Pi migration/local flow remains mandatory for full R6 acceptance; it cannot be inferred from cross-build or Linux proof. |

The public research summary records 10,939 initial-context tokens (`docs/research-timesheet-token-efficiency.md:389-402`); the owner's ~2-million-token run is a different total. Compare like counters only. Do not read/import the original transcript, infer prices, or invent a reduction percentage from unlike/synthetic counters.

## 5. Required gates, report and stop

This host is Linux. No macOS runtime or real provider/model authentication has been supplied/authorized. Complete reachable source cutover and hermetic proof; record unavailable macOS proof as an explicit blocker to **full R6 acceptance**, not a passed criterion or silently reduced contract. Real model/subscription readiness remains a separate named live-read authorization gate; never open an owner profile to satisfy it. No RC/stable/production claim follows source review.

The owner already accepted `durability_uncertain` UI evidence as source-reviewed plus accepted R4 backend coverage, not newly TUI-fault-proven. Preserve that exact attribution; remote uncertain-write proof is not the local durability-fault case. No new code cycle is requested for that accepted qualification.

Return a sanitized new snapshot: actual branch/base/version, exact changed/deleted list, baseline/live mismatch results, source/binary hashes, standard two-column SHA-256 manifest excluding itself (verify it), real-PID runtime matrix with pass/fail/not-run, model request/tool inventory and available counters, owner-data/source non-mutation, cleanup facts, remaining platform/live prerequisites. Do not retain tokens, authenticated URLs or raw provider documents. Remove generated fixtures/drivers and stop all child/provider processes after proof.

Stop **independently review-ready**, not self-accepted/complete R6. No commit/publication or further worker dispatch. Two unsuccessful repair rounds return to owner/technical adjudication; no automatic repeated loop. Issue #137 closes only after separately authorized implementation integration into main.
