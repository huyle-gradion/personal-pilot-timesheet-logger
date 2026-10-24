WORKER CLASS: complex-critical
PRESET: owner-selected existing Pi session
MODEL: owner-selected supported chat model; record actual identity
VARIANT: owner-selected; do not change settings
RISK BASIS: first actual macOS persistence/process/UI proof after CLI cutover
GOVERNING ARTIFACT: issue #137; timesheet-rewrite-r6-review.md; owner request to publish a pullable macOS verification branch
SCOPE: actual macOS hermetic verification of the published R6 candidate; no production code or owner-data changes
VERIFICATION: Darwin-built timesheet plus bundled pinned Pi, synthetic migration/local workflow/restart and bounded retained evidence

# R6 macOS verification

This is an **unaccepted verification candidate**, not a release or merge authorization. The owner explicitly requested a pullable branch so a macOS Pi agent can run it. Local root README exit-code/restart wording still has R6-F1/F2; use the corrected behavioral facts below. Publishing does not close those findings or issue #137.

## Checkout and permissions

- Branch: `feat/timesheet-rewrite-r6-v0.1.0`; ancestor R5 `99a7f23336845ac6858706319e695a750f9116cd`. Record the pulled HEAD and compare with the publication hash supplied by the owner/issue #137, not the old R5 baseline.
- Work from the owner's explicitly supplied checkout path. Do not guess another user's `/Users/...` location or search the filesystem. Linux `/tmp/...` evidence paths in older records are provenance, **not files to search for on macOS**.
- Owner-mediated, inline; no child dispatch, commit/push/PR/merge/tag/release, live provider/model calls, production timesheet writes or product code/docs fixes in this verification pass. R6-FIX1 remains a separate docs-only repair task.
- Never open `.env.local`, owner config/records/stores/Pi profiles/auth, Keychain, vault or `transcript_full.jsonl`. Use generated isolated fixtures only. Do not follow retired skill links or restore old runtime paths. No ai-database integration.
- The outer Pi coding session is the owner's chosen harness. The **application under test** must use its own pinned Pi 1.4.0 and empty dedicated profile; never reuse the outer session's profile or change global Pi settings.

## Minimal setup

From the checkout root, inspect only tool versions and the named build files. Go >=1.27.1 and Node >=22.19.0 are prerequisites. If unavailable, report the exact prerequisite; do not silently install a new global toolchain.

```sh
GOTOOLCHAIN=local go version
node --version
GOTOOLCHAIN=local go build -trimpath -o bin/timesheet ./cmd/timesheet
bin/timesheet version
npm --prefix pi ci
```

`npm ci` may fetch the **locked public dependencies**; no provider account access is permitted. Pins remain Pi/TUI/typebox 1.4.0/1.4.0/1.3.27. The Linux executable is intentionally not committed: build a native macOS binary. Its hash will differ from Linux; record its own hash, architecture and toolchain rather than claiming a mismatch against Linux ELF.

Create a fresh private temporary root containing synthetic `config.json`, empty/generated `fixture.env`, `state.json`, legacy `workflow/` directory, isolated HOME, dedicated Pi profile and empty workdir. Never copy an owner config/env/profile. Configure project P1, UTC local timezone, sources omitted/disabled and Gradion disabled for the initial local proof. Enable migration only for that generated legacy root, timezone Europe/Amsterdam, project map OLD-1→P1. The config and env are binary-owned; don't dump them into model context.

Launch the real TUI with absolute **generated** paths:

```sh
node pi/launch.mjs \
  --binary "$PWD/bin/timesheet" \
  --config "$FIXTURE/config.json" \
  --env-file "$FIXTURE/fixture.env" \
  --profile-dir "$FIXTURE/pi-profile" \
  --workdir "$FIXTURE/workdir"
```

`FIXTURE` means the newly created private temporary root, not a real user's timesheet directory. Do not execute the checked-in example as-is against `/Users/example/...`; it is a schema example, not an installed configuration.

## Bounded source input

Read this file and `docs/orchestration/timesheet-rewrite-r6-review.md` for findings/qualification. For request shapes use only needed sections of `docs/agents/rewrite-interface.md`; public example `cmd/timesheet/timesheet.example.json` is illustrative. For synthetic legacy data, reuse the **legacyDayOne constant and migrationFixture setup only**, `internal/rewrite/migration_test.go:12-59`. Other accepted core/adapter implementations need no whole-file read. Do not load full inventory JSON/lockfile, old transcripts, raw provider documents or growing logs into context; parse artifacts locally and return compact facts.

## Actual macOS acceptance cases

1. Record Darwin/macOS version, architecture, Go/Node/pinned Pi versions, source HEAD, native binary identity/hash, protocol 1/state 3. `version` requires no config/env. Correct exit facts: unknown top-level/extra version args → 2; serve argument/startup/stream failures → 1; normal close/EOF/help → 0. Do not modify code to match the known wrong README sentence.
2. Drive the native compiled `serve` JSONL process with generated fixtures. Migrate the rich legacy day, repeat preview/apply, terminate and restart a **different process**, and re-read the migrated day. Assert source bytes unchanged, revision 3, origin 180 minutes and locked next-day 75-minute spill, binding legacy-1→r-100, inert unusable approval audit, unresolved mw-k-1 and durable audit preserved. Old approvals never authorize a write.
3. Use the actual bundled Pi TUI to inspect that migrated origin/spill and a separate local day. Type a synthetic interval, confirm versus save, edit/cancel, restart and verify saved minutes/revisions. A saved 60-minute day with an additional unsaved 30-minute staged edit must reopen at 60, not 90. Unsaved staged and confirmed-but-unsaved interval changes are session-only; committed ledgers reload but startup never dispatches.
4. Verify one child PID across normal multi-phase local work, store_locked for a second writer against the **same generated state**, usable `/reload`/new-day navigation and clean shutdown. On macOS inspect only known child PIDs; do not use Linux `/proc` or scan unrelated processes. Report any lock, directory-sync, transport or terminal failure honestly; no fallback/retry/replay or core patch to force a pass.
5. Direct local `/timesheet` operation needs no application model call. No live model is needed for this macOS platform gate. If additional source/submission/model-boundary proof is attempted, use synthetic loopback fixtures only under explicit local fixture approval and label it separately; no real account/auth/profile discovery.

No new unit tests/full matrix rerun is required merely for the platform handoff. A compile-only Mach-O result is not this actual runtime proof. Preserve the accepted durability_uncertain UI evidence qualification (reviewed source + R4 backend), not falsely a newly run fsync fault case. Real provider/model subscription readiness remains unverified and separately gated.

## Retained handback

Keep a compact sanitized capsule: source HEAD, native binary hash, process IDs, pass/fail/not-run facts, source/durable-state hashes, actual review/receipt terminal captures and a standard two-column SHA-256 manifest excluding itself (verify it). Label Linux/prior/worker-reported facts separately from fresh macOS observations. Do not retain credentials, authenticated URLs, raw provider documents or raw state dumps. Stop all owned children/fixtures and remove generated HOME/profile/config/env/state after proof; keep the bounded evidence capsule.

Return the capsule/report for independent review. No self-acceptance, commit, push or production readiness claim. R6-F1/F2 and main integration remain separate gates even if macOS proof passes.
