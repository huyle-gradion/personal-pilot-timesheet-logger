# Timesheet 0.1.0 release disposition

## Owner authority

The owner explicitly instructed: ignore the upstream Pi problem for this release, replace the current main product with the verified implementation, release it, and retain R6 for further improvements. This authorizes the release commit, branch publication, ordered pull-request integration and release tag/publication. It does not authorize live credential reads, provider/model access, real timesheet writes, vault integration or deletion of private/owner files.

The known upstream risk is accepted for **0.1.0**, not fixed or permanently waived. Pi 1.4.0 remains pinned with pi-tui 1.4.0 and typebox 1.3.27. Its bundled Undici 8.10.2 has [a high-severity fetch process-crash issue](https://github.com/advisories/GHSA-xjj6-f33q-929c), [EventSource memory exhaustion](https://github.com/advisories/GHSA-4qv8-2p9j-2685) and [integrity-token validation bypass](https://github.com/advisories/GHSA-f72v-6w45-q472). No patched Pi bundle was selected. Do not represent this release as vulnerability-free. R6 / issue #137 stays open for remediation and improvements.

## Scope and ancestry

- Pre-integration main: `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Published implementation/verification baseline: `8990e85bfa648ad03638966a64db43c01401fac3`.
- Rollback: a local annotated `snapshot/pre-v0.1.0-main-*` tag preserves the old main; a private sanitized rollback directory preserves known working-tree documents, excluding credentials, transcripts and owner runtime data.
- Integration order: PR #138 (R1–R3), #139 (R4), #140 (R5), then the R6/release PR into main. All merges are through PRs, with no direct-to-main record commit, squash or ancestry implementation.
- The release stamp is `0.1.0`; protocol remains 1 and durable state schema remains 3. No application, accounting, persistence, submission or dependency behavior changes from dev.17. Existing documentation corrections are included. Legacy CLI/vault paths stay retired.
- Existing unrelated owner governance/private changes are preserved outside the release staging set. The orchestrator entry receives only an isolated release pointer; its unrelated working-tree edits are not staged.

## Evidence and limits

Previously accepted R1–R5 source/runtime evidence is retained. Native Darwin migration/restart/writer exclusion and the owner's Darwin one-command UI result establish local operation: confirm/save/cancel, saved-versus-staged restart, edit/cancel, reload/new day, graceful exit and lock release. Owner-reported runner PIDs: 25974, 26146, 26207. The remote Mac capsule was not independently downloaded or checksum-verified here.

New release-candidate checks passed: Go formatting, build/vet, all retained Go tests, strict TypeScript compilation, launcher syntax, native version output, and a real pinned-Pi Linux TUI smoke of confirm/save/cancel/restart/edit/reload/normal quit/lock release. Candidate identity was `timesheet 0.1.0 (protocol schema 1)`. Darwin arm64 and amd64 artifacts cross-compile; those builds are compile-only proof. The actual Darwin runtime proof is the prior dev.17 behavior, promoted with identity-only source changes and unchanged SDK pins.

Live accounts, authenticated model/subscription readiness and real Gradion submission remain unverified. Exact human plan approval is still required for remote writes. The `durability_uncertain` UI case retains source-reviewed plus accepted backend coverage; it is not newly TUI-fault-proven. No release operation performs a real timesheet mutation.

The GitHub release and integration PR retain final merged commit identities, release asset checksums and publication results; this pre-integration record does not invent future merge hashes. R1–R5 tracking issues close only after main integration. R6 and the project epic retain the explicitly unfinished improvements.
