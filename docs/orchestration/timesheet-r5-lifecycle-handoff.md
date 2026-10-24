WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: high
RISK BASIS: repeated lifecycle failure; cross-runtime cancellation/UI ownership; interrupted-write safety
GOVERNING ARTIFACT: issue #136; timesheet-r5-adjudication.md; original R5 lifecycle contract
SCOPE: C1-L1 investigation and root-cause fix only, preserving pending-reload support
VERIFICATION: actual pinned Pi TUI + compiled Go, pending read/reload/reopen and pending-write uncertainty; independent re-review

# TIMESHEET-R5-LIFECYCLE

Status: **completed and independently accepted at dev.16 within qualified Linux/hermetic scope**. See `timesheet-r5-lifecycle-review.md` and `timesheet-r5-acceptance.md`; the owner explicitly accepted the remaining durability-uncertainty UI evidence qualification. This packet is a historical execution record, not a new dispatch. Protocol 1/state 3 and model routing are unchanged; routing is not proof of live model/subscription authentication. No child dispatch, commit/publication or R6 authority follows.

Exact owner decision: “authorize a narrowly scoped lifecycle investigation/fix preserving pending-reload support”. [Adjudication](timesheet-r5-adjudication.md) records this explicit follow-on after two exhausted repair rounds. It is not an automatic FIX3, a contract waiver, or a reset of the repair loop.

## Baseline, authority and one writer

- Branch `99a7f23336845ac6858706319e695a750f9116cd`, HEAD `4dbe08183d6a410a04589885a67541a0c51614e9`; preserve owner governance/untracked changes. No new branch/worktree unless separately authorized.
- Frozen dev.15 candidate `/tmp/timesheet-r5fix2-evidence-20261024T090805`: outer manifest SHA-256 `5a0c2b6efe90ca6be77848ad92d1452c765513268ee0baab174b91b9ab5a3d9f`; eleven-file `source/final.sha256` SHA-256 `c7586e845271813c7609f222661fbe16a7ffbe24c103a6a20be9e0e32cca90cc`; executable SHA-256 `d56d07b0e6ed54e35c1d8262312bf315163ee95e4c916869eb30eb69bd073a5c`.
- Independent failed-boundary proof: `/tmp/timesheet-r5fix2-review-h0gjbq_e/`, `pending-reload-facts.json`, visible command captures `18`–`21`, and fresh no-selector control `28`–`32`. Read these compact facts, not whole evidence trees. Earlier capsules are immutable.
- Reconcile the selected live files against frozen dev.15 before editing; do not reconstruct untracked Pi sources from HEAD. Unexpected ownership changes require reconciliation, not overwrite. One active writer.
- No staging, commit, push, PR change/merge, tag/release, R6, real credentials/config/stores/transcript, owner Pi profile, host Git test, ai-database or live timesheet/model accounts. Synthetic loopback mutations only after explicit local approval of the exact fixture plan.
- Report capabilities, no-child permission and inline rationale once. Do not force agents or treat tool availability as authority. No arbitrary cat/ls/find or equivalent out-of-scope discovery.

## Problem and diagnosis gate

Verified dev.15 behavior: hold a real Go collect at a synthetic calendar response; enter actual Pi `/reload`; Pi reports reload then replacement-child startup failure/EOF. A visibly entered `/timesheet` command opens neither recovery nor a Go child, including a fresh runtime without earlier dialogs/selectors, for more than nine seconds. Full Pi restart with unchanged profile/config/binary restores review. No automatic collect replay or provider mutation was observed.

The failing-before capsule is ground truth; do not spend a run merely reconfirming it. A narrow instrumented hermetic run is allowed to identify the blocked lifetime/promise. Before application edits, record the causal path with pinned-source references and distinguish observed facts from hypotheses. The review's stale command/custom-UI continuation hypothesis is **not an established diagnosis**.

Trace only: extension command completion/queueing, custom-UI completion/cancellation, session_shutdown, reload/new-runtime startup, transport disposal, and late continuations. Establish who owns each pending request/form and when that ownership ends. Do not guess that awaiting shutdown alone fixes it, suppress the error, add a timer/delay/retry, or patch only the collect catch while sibling paths remain vulnerable.

## Closed read/write capsule

Read this packet first, then the relevant portions of [dev.15 review](timesheet-r5-fix2-review.md) and the named compact proof. Original R5 handoff supplies the pinned API contract, not a broad discovery mandate.

Writable production surfaces:

1. `pi/timesheet.ts`: lifecycle/state/command/operation ownership, disposal, and stale UI/continuation guards. Start at current `session_start/session_shutdown`, `op`, command wrapper, `runHumanCommand`, `openDayAndCollect`, `offerChildReopen`; follow only relevant caller paths. Preserve existing accounting and human authority behavior.
2. `pi/view.ts`: only custom-UI lifetime/completion/cancellation changes justified by the diagnosis. Preserve the repaired display-cell wrapping, paging, resize and local controls.
3. `pi/transport.ts`: only shutdown/startup handoff and generation/cancellation changes justified by the diagnosis; plus dev.16 identity. Preserve validated envelopes, frame bounds, UTF-8, queue settlement, backpressure and no replay.
4. `internal/timesheet/model.go`: AppVersion constant only. `pi/package.json` / `pi/package-lock.json`: root dev-version stamps only; resolved dependencies/integrities unchanged.
5. `pi/README.md`, `docs/agents/timesheet-interface.md`, `CHANGELOG.md`: truthful lifecycle contract, root cause, dev.16 notes and verification qualifications.
6. Optional `pi/transport.test.ts` / `pi/view.test.ts` for consumer-visible lifecycle invariants. Tests remain optional; no source-text/wiring/mock-echo tests.

Read-only: `pi/launch.mjs`, `pi/.gitignore`, all other Go/runtime/schema/approval/migration/store/collector code and frozen tests, dependency pins, installed packages, original spec and relevant interface lifecycle/submission/recovery sections. No governance/AGENTS/CLAUDE edits by the worker. No new production framework, daemon, RPC, authority marker, accounting engine, retry system or fixture hook.

For the root-cause investigation, scoped navigation within the pinned public `@earendil-works/pi-coding-agent` 1.4.0 package is allowed for reload, extension-runner command dispatch, custom UI and cancellation/shutdown implementation/declarations. Start with its named `docs/extensions.md` / `docs/tui.md` and linked definitions; read only implementing symbols/ranges. Pi-TUI declarations may be read for the relevant UI primitive. No node_modules traversal/bulk scan or owner profile/auth/catalog caches. Pinned reference commit is `58c0b02ce552adab76014e7ed712ab7981359aa8`, already verified against the published 1.4.0 docs; do not substitute newer durable-API snippets or upgrade/patch the installed package.

If the proven root cause requires another writable surface/dependency/API, finish the reachable diagnosis and return the concrete missing prerequisite for owner approval before expanding scope. This does not authorize a workaround that drops pending-reload support.

## Required behavior

- Reload/replacement disposes the old runtime and pending UI/command ownership, not just child handles. Every old active/queued caller settles; old continuations cannot open a modal, overwrite replacement day/review/collection, release a new workflow's guard or consume its input.
- The replacement runtime is usable without restarting the Pi process. An explicit child-reopen prompt is acceptable after interruption if it is visible and works; a transient startup failure is not permission for an automatic spawn/retry loop. Dispose/join the prior writer or expose honest explicit recovery, never leave the next local command blocked.
- No automatic replay/recollection/reapproval/submission. A new human `/timesheet <date>` may perform its documented fresh collection; a stale request may not masquerade as that decision. At most one active writer and one owned child per active generation; old callbacks/frames cannot affect the new generation.
- Cancellation or transport loss after write dispatch means outcome unknown until authoritative read-only status/reconciliation. Preserve durable approval consumption/intents/unresolved writes; no claim that shutdown cancelled a provider mutation. No backend/schema/remote identity changes.
- Preserve single-child daily phases, busy-human/model-preparation isolation, date-only bounded tool, terminate behavior, fixed capability restrictions, source attempt identities, exact plan/approval binding and all previously closed F1–F9 behavior.

## Runtime acceptance

Use real pinned Pi 1.4.0 through `pi/launch.mjs`, generated isolated profiles/config/env/state and the exact compiled dev.16 child. No mocked ExtensionAPI as UI evidence. Fault children are for transport fault injection, not replacement business implementations.

1. **Original C1-L1:** actual collect held at loopback provider → visibly entered `/reload` → release/cancel old request → visibly entered `/timesheet` and, if needed, visible explicit Reopen. The next review opens in the same Pi process with fresh status; no process restart or hidden/modal keystroke is required. Include the clean first-command/no-selector case. Record old/new PIDs and provider requests; no automatic collection replay.
2. **Shared lifetime boundary:** exercise one other pending human read/form path (for example classification read or plan preview) across reload and then a different-date local command. Old dialogs/results cannot reappear or overwrite the new workflow. Prove new controls/focus are usable, not merely that a reload banner appeared.
3. **Pending approved write:** through actual local forms, display an exact synthetic plan and explicitly approve/submit it. Hold the fixture after applying the intended write but before acknowledgment; reload. The replacement can obtain read-only submission status and reconcile that reference; no second write/reapproval/replay occurs. Explicit recovery resolves only the addressed effect, while contradictory not_applied remains rejected. Approval/intent/outcome and saved work survive as authoritative Go state.
4. **Preparation and replacement ownership:** interrupted preparation must not mutate a replacement human workflow or clear its busy guard. Use actual Pi model transport with an externally provisioned loopback model profile if that path changes; no shipped fixture flags or real model credentials. Exercise quit plus one supported session-replacement path with pending work; no orphan child, unresolved caller or stale UI reuse. Inspect pinned API semantics before selecting the replacement action; no owner sessions.
5. **Preservation controls:** idle reload still works; cross-day active approval selection still names the correct ID; UTF-8/byte-bound failure handling and narrow wide-text inspection retain their verified behavior. Keep this focused; do not rerun whole historical collector/migration matrices or the host-sensitive Git failure.

Record the diagnosed before/after lifetime sequence and map each required behavior to actual observations. Do not claim pending-write uncertainty proves local-filesystem `durability_uncertain` UI: that existing source/prior-backend qualification remains distinct and was not waived by this authorization. macOS, live provider/model/subscription readiness and R6 remain separately unverified.

## Final gates, handback and stop

After coherent changes: one final strict TypeScript check, scoped Go build/version and affected checks; no mid-flight blanket build/lint/test loops. Tests optional. Run the changed paths with the exact final binary; keep failed-before reviewer evidence separately attributed rather than fabricating a rerun. No Go semantic changes to make a UI test pass.

Produce a NEW sanitized `/tmp/` capsule: selected before/final sources, bounded diff, diagnosed root cause with pinned API references, package/runtime identity, binary/source hashes and verified non-self-referential manifest, compact request/receipt/process/provider/model facts, genuine terminal captures and acceptance map. Preserve original capsules. Remove generated drivers/config/env/state/profile/legacy/provider fixtures and stop all owned children/servers after retaining safe proof. No secrets, authenticated endpoints, owner records or raw provider documents.

Report C1-L1 disposition, exact affected paths, commands/results, before/after runtime evidence, unresolved prerequisites and honest qualifications. Stop independently review-ready; no self-acceptance, staging/commit/publication or R6. This is one explicitly owner-adjudicated investigation/fix handback. If it does not converge, return to owner/technical review; no automatic further repair.
