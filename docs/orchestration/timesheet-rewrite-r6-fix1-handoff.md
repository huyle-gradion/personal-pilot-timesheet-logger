WORKER CLASS: routine-verified
PRESET: deepseek
MODEL: deepseek/deepseek-v4-flash
VARIANT: high
RISK BASIS: documentation-only correction against frozen runtime facts; no executable or data mutation
GOVERNING ARTIFACT: issue #137; timesheet-rewrite-r6-review.md findings R6-F1/R6-F2
SCOPE: README.md, docs/agents/rewrite-interface.md, CHANGELOG.md only
VERIFICATION: prose matches independently recorded CLI/restart behavior; unchanged binary/source ownership; compact retained handback

# TIMESHEET-REWRITE-R6-FIX1

Owner-mediated, inline; no child dispatch. Use the existing implementation session if available; record its actual model/tools once. Cached routine route above was read from the local registry (verified_at 2026-10-02), not live model authentication. No settings change is authorized.

## Baseline and authority

Branch `feat/timesheet-rewrite-r6-v0.1.0`, HEAD `99a7f23336845ac6858706319e695a750f9116cd`; preserve all uncommitted R6 retirement/integration work and the owner's separate governance/private files. This is the first R6 repair round, not a reset of an exhausted loop.

Candidate source manifest `/tmp/r6-snapshot-manifest.sha256`, SHA-256 `63af50ad912627d4b517ca6428f285a879c6a68ae48184087cb677a02a99c460`, was independently verified 41/41. Executable SHA-256 `dc3dbf5054a17d71108aee9f7285e1ca832f285a5f63f2d277da743b840a738a`. Reviewer frozen sources/binary and genuine observations: `/tmp/timesheet-r6-review-g99in8nl/`. Preserve that capsule and the original manifests unchanged.

Only write the three product-document paths in the routing header. **Keep dev.17, the binary, all Go/TS/launcher/package/module/schema behavior and all 225 retirements fixed.** No staging/commit/push/PR change, merge/tag/release, children, live access, real credential/config/store/profile/transcript/Keychain/vault reads or production writes. No untracked owner file/directory deletion. No orchestration/packet/inventory changes.

## Exact repairs

1. **R6-F1, README.md:97-98:** remove the claim that staging survives restart. State that unsaved staged interval edits and confirmed-but-unsaved interval changes are session-only; successfully committed state/durable ledgers reload, but startup never automatically submits or replays. Do not claim persisted approvals disappear, and do not change persistence semantics to fit prose.
2. **R6-F2, README.md:23-25 and docs/agents/rewrite-interface.md:45-46:** distinguish exit codes accurately. Normal `version`, normal serve close/EOF and serve help exit 0. No/unknown top-level command or extra version arguments exit 2. Serve argument validation (unknown flag/extra positional/missing required flags), startup and stream failures exit 1. Do **not** change the preserved Go entrypoint to make the old wording true.
3. Add one concise documentation clarification under the existing Unreleased/dev.17 changelog entry. No version bump, new release entry or production-readiness claim.

## Minimal input and proof

Read this packet, only the review's F1/F2 and evidence qualification, `README.md:23-25,88-100`, interface `:45-46`, and the first changelog section. Use the current displayed locations if line numbers shift. Do not reread whole source trees, inventory JSON, provider documents or runtime logs.

Known actual results, already independently exercised:
- `cli-observations.json`: version=0; version extra/unknown top-level=2; extra serve positional/unknown serve flag=1. The worker's reported missing-flag exit=1 remains attributed, not newly rerun.
- `migration-and-staging-facts.json` and `03-saved-work-without-unsaved-staging.txt`: save 60 minutes, stage 90, restart → saved/staged 60 and clean. Rich migrated origin/spill/mapping/inert audit/unresolved reference and exact durable bytes survive restart/repeat.
- `production-reload-facts.json`/`cleanup.json`: actual dev.17 Pi pending reload/new day/delayed-response isolation and pending quit pass with no replay/orphan.

**Do not rerun the full build/vet/tsc/test/runtime matrix for prose changes.** No permanent wording/source/default tests. Reuse these named observations with provenance, not invented new captures. If a new concrete runtime problem is found, report it; this packet grants no executable fix.

Reconcile the three live docs against the reviewer-frozen copies before editing, then retain their small before/after diff. Programmatically verify unchanged source manifest rows and the executable; only the three doc checksums may differ. Verify 225 retired paths remain absent without following symlinks or touching owner files. Do not dump the machine inventory/lockfile into context.

## Handback

Return a new compact sanitized capsule with the three final docs/diff, updated source checksums plus binary hash, a standard two-column SHA-256 manifest excluding itself (verify it), and a short results/provenance file. Label reused reviewer proof, original worker report-only rows, and any actually new checks separately. Original raw worker recordings were removed: do not manufacture them or mark all remote/model rows freshly rerun. Preserve the accepted prior durability_uncertain UI qualification.

Stop independently review-ready, with no commit/publication or self-acceptance. **Actual macOS Pi/migration proof is still mandatory before full R6 acceptance**; Linux and Darwin cross-builds do not waive it. Live provider/model readiness and production writes remain separately unauthorized/unverified. Two unsuccessful R6 repair rounds return to owner adjudication; no automatic loop.
