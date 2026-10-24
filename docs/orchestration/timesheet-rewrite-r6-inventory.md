# R6 bounded reference inventory

Baseline: R5 commit `99a7f23336845ac6858706319e695a750f9116cd`, PR #140. Branch `feat/timesheet-rewrite-r6-v0.1.0` is created at that exact commit. No cutover source edit or deletion has been performed by this inventory.

Machine input: `timesheet-rewrite-r6-inventory.json`, SHA-256 `29248a6901c47fab2fa526e0c0477d6e2cc72c673b6a8d23a6710601de200310`. **Do not read the full JSON into model context.** It lists baseline Git blob IDs, modes and exact tracked paths for bounded checks/removal. Parse it locally and emit counts or mismatching paths only. Blob IDs are Git object identities, not SHA-256 claims.

| Ownership class | Baseline tracked files | Meaning |
| --- | ---: | --- |
| retire | 225 | Superseded source/tests/product skill/vault generator only; no directory-wide deletion |
| replace_legacy | 2 | `cmd/timesheet/main.go` and its public `timesheet.example.json` |
| bounded_edit | 10 | Exact version/module/docs integration paths, further restricted by handoff |
| read_only | 28 | Accepted core, shared MCP and restricted harness files; preserve byte content |

New permitted files: root `README.md`; optional behavior-only `cmd/timesheet/cutover_test.go`. Unit tests are allowed, not required.

## Compiler-backed retirement boundary

Observed `go list -deps` for `internal/rewrite` plus `cmd/timesheet-rewrite` returns exactly three module-local packages: `internal/mcp`, `internal/rewrite`, `cmd/timesheet-rewrite`. The accepted Gradion implementation is `internal/rewrite/gradion.go`, not `internal/gradion`.

The sixteen unused legacy package trees are `internal/{benchmark,calendar,editorial,evidence,export,git,gradion,keychain,metrics,proposal,review,run,selection,storage,submit,workflow}`. Their source and tests are exact manifest rows, not permission to erase untracked files under those directories.

Retire old `cmd/fetch-git`, `cmd/fetch-calendar`, `cmd/benchmark`, the temporary `cmd/timesheet-rewrite`, superseded files under `cmd/timesheet`, and the four old `tests/*.go` files. The latter describe obsolete commands/skills, including wording/source pins; do not re-pin those assertions against the new product. Replace the two named legacy production/example files with the accepted server entrypoint and a strict-schema public example.

Retire `.agents/skills/timesheet/{SKILL.md,workflow.md}`, the tracked `.claude/skills/timesheet` symlink (mode `120000`, unlink only), `scripts/sync-vault.sh` and tracked `scripts/vaultgen` source/test fixtures. Never follow the symlink or touch an owner vault.

Preserve `internal/mcp`, accepted `internal/rewrite` behavior/tests, Pi isolation/lifecycle/UI code, `archive/`, `benchmarks/metrics.csv`, historical docs/ADRs/tickets and unrelated maintenance scripts such as `scripts/sync-github-issues.sh`. The latter is not a production daily entrypoint; do not run it.

## Current documentation conflicts to resolve

`docs/agents/credentials.md` still prescribes the superseded Keychain-only pipeline. Replace that active guidance with the owner's later `.env.local` policy and the accepted binary-owned dotenv/inherited-environment precedence; do not add a resolver or read a real credential. The interface document's top-level identity still says R4/dev.11 despite later R5 sections: make current identity/commands truthful for R6, retaining historical limits as historical rather than expanding runtime claims.

Observed SemVer inventory: no matching local `v[0-9]*` tags and GitHub release list returned `[]`. Target remains unreleased `0.1.0-dev.17`; this is not tag/release authority.

Sanitized tracked-source rollback: `/tmp/timesheet-r6-prep-3cjxhh1v/baseline-source.tar`, SHA-256 `edfe40c970c33ffdcb4d52b2088afe4fc48cac5d2fcd8b318044d246c2e934d5`. No transcript, `.env.local`, private config/store/profile/vault or untracked owner data is included. The worker must reconcile live selected paths before editing; the immutable baseline alone does not prove current working-tree ownership.
