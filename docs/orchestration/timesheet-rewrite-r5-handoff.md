WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: model capability escape, credential/context leakage, unauthorized human-gate forwarding, uncertain-write replay
SCOPE: TIMESHEET-REWRITE-R5, restricted Pi launcher/adapter and local human workflow
VERIFICATION: actual pinned Pi TUI plus compiled persistent Go child; synthetic sources/provider; captured model requests/tool inventory and process identity

# R5 owner-mediated implementation packet

Status: dev.14 first repair was independently re-reviewed and **not accepted**. Continue only with [R5-FIX2](timesheet-rewrite-r5-fix2-handoff.md), target dev.15; [re-review](timesheet-rewrite-r5-fix1-review.md). This original packet remains the product/API contract, not the active target. No child dispatch, commit/push/PR change, R6/cutover, live access, merge or release. Stop independently review-ready; another nonconvergent repair requires owner/technical adjudication.

## Authority, baseline and stack

- Accepted R4 is commit `4dbe08183d6a410a04589885a67541a0c51614e9` on `feat/timesheet-rewrite-r4-v0.1.0`, PR #139. It is stacked on R1-R3 PR #138. Merge #138 first, then retarget #139 to main before its merge; do not merge R4 into #138's branch or push more work to either accepted PR branch.
- R5 implementation branch: `feat/timesheet-rewrite-r5-v0.1.0`, based on that R4 commit. Record actual HEAD and preserve owner governance/untracked changes, especially the private transcript without opening it. One source writer. No automatic worker branch/commit/push activity or child dispatch.
- Track issue #136, part of epic #131. R4 issue #135 is source-accepted/pending PR merge, not an implementation blocker on this published stack. GitHub dependency/checkbox completion still follows merges.
- Acceptance baseline: `timesheet-rewrite-r4-acceptance.md`. R4 worker binary SHA-256 `652882d47a763818eb0455135229b18035963b4c5c5fab5dd319e4f257a1392e`; outer `/tmp/timesheet-r4-fix2-20261023/evidence-manifest.sha256` SHA-256 `2dd4731a59c4d7387b649dd85d59dd713de5cb7d9702fdcac170718498638394`; full source manifest `source-snapshot/source.sha256` SHA-256 `82a39aeaaa6c12acf7f28b90e7c71b9b6d6401c50050e27b509c81e51267e467` (13 files). Read selected pins, not historical transcripts or whole evidence trees.
- Target bundled application/adapter `0.1.0-dev.13`, protocol 1, state 3. Keep the temporary Go CLI until R6; do not call R5 a production release. No Go accounting/submission/schema changes are authorized here.
- Tests optional. Actual Pi interaction and real compiled-child behavior mandatory. No real `.env.local`, owner config/stores/transcript, global Pi settings/auth, Keychain, host Git profile, ai-database or live service access. Public pinned npm download and isolated synthetic runtime provisioning are allowed. All remote mutations only synthetic loopback after explicit local approval.

## Exact files and read capsule

New production files: `pi/package.json`, `pi/package-lock.json`, `pi/launch.mjs`, `pi/timesheet.ts`, `pi/transport.ts`, `pi/view.ts`, `pi/README.md`. Keep the adapter small; no plugin framework, daemon, generic RPC bridge or second accounting engine. Optional focused tests `pi/transport.test.ts` / `pi/view.test.ts` only for consumer-visible boundaries; do not replace actual UI evidence with mocked ExtensionAPI calls.

Integration writes: `internal/rewrite/model.go` AppVersion constant only; `docs/agents/rewrite-interface.md`; `CHANGELOG.md`. All other Go files, `go.mod`/`go.sum`, old CLIs/skills/packages, collector/migration/store/approval logic and frozen tests are read-only. Report a concrete missing protocol capability instead of silently implementing it or substituting fake success. No root JS dependencies or AGENTS/CLAUDE edits.

Read only:

1. This packet; R4 acceptance; `docs/timesheet-rewrite-spec.md:19-29,55-94,96-106,119-128`; `.omo/plans/timesheet-rewrite.md` R5 row and current gate.
2. `docs/agents/rewrite-interface.md` protocol framing/ops, grammar, review/source identity/attendance, classification, migration/reconciliation, approval/submission/recovery and statuses needed for the UI. Do not load whole historical repair reports.
3. `cmd/timesheet-rewrite/main.go` launch contract; `internal/rewrite/session.go` Request/Response, dispatch validation and only relevant result structs/helpers. `config.go` and model types only to identify fixed launch/environment names and public data shapes, not real config. `approval.go`/`submission.go` receipt structs and exact error/status semantics if needed. No repository-wide exploration.
4. Pinned public Pi references below; installed public package declarations for the named extension/UI APIs. Do not read owner Pi auth/settings/catalog caches to discover capabilities.

Use scoped symbol navigation when available; otherwise exact named files/ranges. No arbitrary shell cat/ls/find or equivalent out-of-scope searching. State available tools, no-child permission and inline rationale once.

## Verified upstream pin, not latest assumptions

Use exact npm dependencies `@earendil-works/pi-coding-agent: 1.4.0`, `@earendil-works/pi-tui: 1.4.0`, and `typebox: 1.3.27` where imported. Commit an actual lockfile with registry integrities; no ^/latest/git-main dependency. Node requirement at least upstream's `>=22.19.0`. No global installation or owner package update.

Package registry metadata: <https://registry.npmjs.org/@earendil-works%2Fpi-coding-agent/1.4.0>. Coding-agent tarball integrity:

`sha512-Z41LpZ3yYHSC/UUps6Mr39UMFPwH8elhJBG9DY1P7kuzsE5qSK/6eMj2EHzO5evJRQWLbKSqUMw+4nCNtIyiQg==`

References at commit `58c0b02ce552adab76014e7ed712ab7981359aa8`; their CLI/extensions/TUI/environment docs were byte-compared with the published tarball and match:

- [CLI flags/isolation](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md)
- [Extension lifecycle/tools/commands](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md)
- [TUI components/custom screens](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/tui.md)
- [Extension types](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts)
- [Environment isolation](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/environment-variables.md)
- [Provider authentication](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/providers.md) and [model capabilities](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/models.md).

Research proof `/tmp/timesheet-r5-api-proof-ny6gs79_/`: verified registry integrity, extracted public package, actual isolated Node CLI `--version` -> `1.4.0` / exit 0 and `--help` exit 0 with the required switches. Offline `--list-models gpt-6` reports **no authenticated models available** in the fresh profile; no owner credentials were consulted. This proves runtime availability/CLI, not an implemented adapter, UI or real subscription. Context7 was consulted but returned some newer durable-API snippets: they are NOT this contract. Use pinned `content`/`details`/`terminate`, not unrelated `output`/`control` result shapes.

Verified API decisions:

- `registerCommand` handlers receive `ExtensionCommandContext`; local commands/dialogs do not start a model turn. Guard custom UI with `ctx.mode === 'tui'`, not merely `hasUI` (RPC can have UI).
- `ctx.ui.select/input/editor/confirm/custom` and Pi TUI components implement local views. Prefer built-in scrolling/input/width helpers over another renderer.
- `registerTool` uses TypeBox parameters and async execute(callID, params, signal, onUpdate, ctx), `executionMode: 'sequential'`. Only result `content` is model-facing; `details` is persisted/rendered, not a safe place for full records. `terminate: true` suppresses automatic follow-up when every completed tool in a batch agrees.
- Factory only registers capabilities. Spawn lazily from the command/tool or session_start; idempotent shutdown on session_shutdown. Reload/new/resume/fork replace the runtime; no process persistence across those boundaries required.

## 1. Restricted launcher and model capability surface

`pi/launch.mjs` is the only supported daily entry point for this slice. Launch the pinned local Pi CLI with Node `spawn`, absolute trusted paths and `shell: false`; no shell wrapper, dotenv sourcing or free-form Pi-argument pass-through. Provisioning arguments name the fixed compiled binary, non-secret config path, dotenv path, dedicated profile and optional model choice. These are operator/launcher inputs, never model arguments. Neither launcher nor extension reads the config/dotenv contents; only Go loads them.

Use an isolated working directory/profile, not the repository or normal personal Pi profile. Ignore ambient project context and discovered packages/extensions/skills/templates/MCP. Enforce explicit CLI switches including `--no-extensions` plus only the trusted adapter via `--extension`, `--no-context-files`, `--no-skills`, `--no-prompt-templates`, `--no-mcp`, `--no-builtin-tools`, `--tools timesheet_prepare`, and `--no-session`. Disable automatic network/telemetry/version refresh; do not confuse offline automatic-refresh control with an OS network sandbox. Do not load user settings by copying a normal profile. A dedicated profile may be provisioned/authenticated separately by the owner; never open its private auth files with agent tools.

Only `timesheet_prepare` may be model-declared/callable. No shell/read/write/edit/search/list, codemode/tool_search, MCP/resource tools, generic binary op, source acknowledgment, attendance, confirmation, classification mutation, plan approval/submission or recovery tool. Operator functions must not be registered even as hidden/deferred tools. Check effective tools and fail closed on unexpected capability exposure; defend tool_call against unlisted names. Disable user `!`/`!!` shell dispatch in this dedicated profile with the supported user_bash gate; ordinary pasted/model/evidence text cannot turn into local commands.

`timesheet_prepare` parameters: only an explicitly bounded ISO civil date; no op/path/executable/env/URL/approval/ack fields or permissive additional properties. It may open the day, collect configured evidence and fetch review data into ephemeral local memory. It must never confirm, acknowledge, choose attendance/classification, resolve identities, approve or submit. Calls use one serialized child queue; a busy human workflow is not permission to mutate it from a sibling tool.

Model result: finite status/code, date and fixed next action, at most 512 UTF-8 bytes. No topics, descriptions, counts that require full records, intervals, links, raw errors/provider text, records or exports. Return `terminate: true`; no sendUserMessage/triggerTurn to narrate deterministic output. Put at most local opaque IDs in details/structured fields; full data stays in memory and local UI, never logs/session entries/model context. Do not point the model at an export file as an escape hatch.

No classifier calls or Luna dependency. Optional supported chat is selected from the dedicated Pi provider catalog; subscription/auth verification is a separately authorized live gate, not inferred from API compatibility. Basic local review must run without model calls; optional natural-language initiation can be proven with a temporary loopback model endpoint using the real Pi transport. That endpoint is test infrastructure, never a shipped fallback or claim of live GPT/subscription support. Do not implement editorial model calls in this slice unless the existing spec's text-only restriction can be met without new unauthorized Go protocol work; preparation/conversation alone is sufficient for the optional initiation surface.

## 2. One persistent private Go child and minimal environment

`transport.ts` owns exactly one session-scoped Go process over stdio JSONL, launched without a shell from the fixed operator-provisioned paths. Adapter never reads config/env/records/exports. Pass only required OS variables and explicitly provisioned timesheet credential variable NAMES; preserve inherited values including empty strings. Default names GITHUB_TOKEN/GCAL_ICS_URL/GRADION_MCP_TOKEN; custom required/token-env names may be provisioned by operator, never discovered by parsing config or supplied by model. Never forward chat-provider secrets wholesale to Go. Never log environment values or authenticated endpoints.

Negotiate `status` protocol/schema and application identity before exposing actions; fail closed on mismatch. Correlate exact request IDs, serialize operations, bound frame/buffer/queue sizes, handle malformed/EOF/extra-ID responses and cancellation explicitly. Do not truncate data into an apparently complete review. Surface transport failure locally with bounded model status.

Same PID must survive review, corrections, acknowledgment, confirm/save, exact-plan approval and submission within the uninterrupted daily Pi session. No daemon. Shutdown is idempotent; a runtime replacement disposes transport and ephemeral view state. Never respawn/replay pending operations automatically, especially a write/approval/ack. After interruption, explicit user reopen obtains fresh status/reconciliation; unresolved writes stay unresolved. Cancellation is not proof a dispatched write did not apply.

## 3. Actual local forms, not a model-operated menu

`/timesheet <date>` starts/reuses the child, collects only configured sources, shows the real review locally and lets the operator act without a model. Use `view.ts` for presentation and local controls only; Go remains authoritative for grammar, minutes/allocations, revisions, identity, persistence and readiness. No duplicate accounting calculations, provider fetching, local scanning or direct store writes in TypeScript.

Required local actions, implemented through the existing protocol:

- Review GitHub topics/provenance/incomplete coverage and calendar proposals/overrides; scroll/details without writing full records into Pi transcript or model messages.
- Typed add/edit/remove intervals using the Go grammar, including nonempty and empty-day changes, cancellation, cross-midnight locked spill/origin navigation and overlap/DST errors.
- Explicit attempt-bound source acknowledgment and meeting attendance choices. Show the actual source/collection or meeting identity; stale identities require refresh and a NEW decision, never automatic resubmission.
- Explicit bounded classification search/selection from returned catalogs; defaults are suggestions only. Confirm work and save-local are separate from source acknowledgment and remote approval. Show unsaved/saved revisions and errors honestly.
- Read-only migration preview/apply with explicit date selection, preserving the configured legacy root's path invisibility; reconciliation, surfaced/missing/changed/orphaned remote identities, and explicit existing resolve_remote choices. Never clear an unresolved write through foreign/adopt shortcuts.
- Exact plan preview: render all creates/edits, selected classifications, date/span/description and edit reason, source revisions, counts/readiness/blockers; never turn a blocked/partial view into an approval UI. Display before asking approval.
- A local explicit approval can lead to submission of THAT returned approval ID in the same user flow, never an automatically reapproved/refreshed plan. Cancel means no approval/submission. Changed/stale preconditions display a fresh preview and require new explicit approval; no silent retries.
- Submission outcome, per-reference confirmed/unresolved/unattempted status, durability uncertainty and `persistence_failed`. Latter means no local consumption was activated and no durable execution exists; a new explicit submit is required after restoring availability. Never auto-retry.
- Submission status and explicit address-bound recovery of one native/migrated unresolved write against a current complete observation. Show identity/decision and require human choice; never guess not_applied, blanket-clear, revive old approval or write remotely during recovery.

Keep receipts compact by default with full local details available on demand. Render evidence/model/error text as untrusted plain text: strip/control terminal escapes including OSC/CSI/clipboard sequences; do not interpolate it into shell, editor commands, resource discovery, URLs that execute actions, or protocol keys. Preserve legitimate human work text in Go; sanitization is at presentation, not silent record editing. Use Pi width/focus/scroll primitives and keyboard paths, including narrow terminals and cancellation; no second terminal renderer.

## 4. Measurement and proof

Count actual model requests and available token/cache/output fields from event results incrementally; do not reread a growing Pi transcript. Distinguish unavailable from zero; avoid double-counting cache components or inventing totals/cost. A local direct path must make exactly zero model calls, proved by a loopback provider's real request inventory as well as local instrumentation. Natural-language initiation can use the one allowed preparation tool; capture the exact model-visible request/result and prove no automatic follow-up after tool completion. Full review/credentials/config paths must not appear in model-visible payloads.

Runtime acceptance (actual pinned Pi TUI, not just a mocked API):

1. Launch through the supported isolated launcher with generated private config/env/state and synthetic GitHub/ICS/Gradion sources. Prove resource/tool inventory: no ambient owner context, skills/extensions/MCP or shell/search/file tools; only timesheet_prepare declared/callable. Inspect real serialized model requests using a local fixture provider. Attempt unlisted tool/op/path and forged model/evidence approval text: zero privileged actions.
2. Exercise actual `/timesheet` UI: collect, scroll/details, manual intervals, attendance, incomplete-source acknowledgment, revise/cancel/confirm/save and restart recovery. Same child PID throughout one uninterrupted session, zero model requests. Invalid/stale protocol identities and overlapping/unsupported spans display the actual Go error, not adapter success.
3. Local migration/reconciliation/identity/classification forms drive real Go behavior. Preserve legacy bytes. Complete current exact create/edit preview, cancel then explicit approve/submit, verify synthetic remote effects and durable outcome. Changed revision/remote/choice/dirty spill invalidates old approval without automatic reapproval or mutation.
4. Show uncertain write interruption/reopen, no automatic replay, read-only status and one explicit recovery. Show `persistence_failed` as non-consumption and zero-operation proof surviving reload/restart. A transport crash never causes blind resubmission.
5. Natural-language initiation against a real Pi model transport with a loopback model fixture: bounded preparation result only, human UI opens locally, no record/config/credential text in model messages/details/session logs, no post-tool model restatement. Call/token counters report observed values and missing fields honestly; no claim of real subscription/billing readiness.
6. Demonstrate cleanup on quit/reload/session replacement and cancellation while pending; no orphan child or stale response reuse. Unsupported non-TUI mode must fail safely rather than auto-answering forms.
7. Capture actual terminal surface: normal and narrow widths, resize/focus, long/multiline/wide text and hostile escape sequences. Full rows remain locally inspectable, not silently truncated into approval. Retain sanitized screenshot/terminal evidence plus model/provider/child inventories and compact receipts, not owner logs.

Build/format/type checks once after coherent work; scoped existing Go check if AppVersion changed. Tests optional. No broad legacy suite, host-Git rerun, real auth file reads or live model/network-account probe. A fixture provider proving integration is not a production fallback. macOS/live-model readiness remains unverified until separately authorized execution; do not claim it from Linux screenshots or a package install.

## Required handback

NEW sanitized `/tmp/` snapshot with selected source rollback/final copies, diff/hashes, lock/package integrity, Go/adapter/Pi/protocol identity, actual TUI evidence, same-PID process trace, JSONL receipts, exact model-visible payload capture, effective tool/resource inventory, provider read/mutation/model-call counts, token fields with availability markers, and acceptance map. Keep old snapshots intact. Remove generated config/env/state/legacy/driver fixtures and stop every child/provider; retain safe proof only.

Report capabilities/delegation once; all claims attributable. Stop review-ready, not self-accepted. No commits/push/PR changes, R6, production writes, real credentials or ai-database. Missing real provider authentication is a named live-read prerequisite, not permission to use owner files or substitute unmeasured model claims.
