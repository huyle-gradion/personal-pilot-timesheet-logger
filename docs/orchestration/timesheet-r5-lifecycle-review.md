# R5 dev.16 lifecycle — independent review

Disposition: **Standards pass; lifecycle contract pass. C1-L1 closed.** No new blocking source finding. Review performed inline and read-only on application code under the owner-mediated packet; no child dispatch. The owner subsequently selected **“Accept qualified evidence”** for the remaining `durability_uncertain` UI evidence basis. Overall qualified R5 acceptance is recorded in [R5 acceptance](timesheet-r5-acceptance.md).

## Identity and scope

- Branch `99a7f23336845ac6858706319e695a750f9116cd`; base HEAD `4dbe08183d6a410a04589885a67541a0c51614e9`.
- Candidate `/tmp/timesheet-r5-lifecycle-20261024T1000`; manifest SHA-256 `6ac1b63c08f4f8af5afc56fc0032139075e12b834d184595f9cd482ce97ff263`.
- Executable SHA-256 `5dceac1de905e77f3ecac7fdba6f07584807fe9dfb15467f8f9226cbc3fc9641`; observed version `timesheet 0.1.0-dev.16 (protocol schema 1)`; store schema 3.
- All 94 candidate records independently checked for hash and byte size with a strict parser. Its manifest has an additional size column, so the initial GNU `sha256sum -c` attempt treated sizes as part of filenames. That attempt did not verify the manifest; the subsequent hash-and-size parser did. The original capsule was not rewritten.
- All ten captured final files matched the live selected files; all ten before files matched frozen dev.15. `pi/.gitignore` was independently unchanged from dev.15. Transport's only change is the version identity; dependency pins/integrities and launcher are unchanged. Go semantics are unchanged: only AppVersion advances. Root package/lock stamps advance without other lock metadata changes.
- Independent review capsule: `/tmp/timesheet-r5-lifecycle-review-fi2v_vn_/`, including frozen source, executable, integrity facts, terminal captures and cleanup facts. `review-manifest.sha256` seals this review's artifacts in standard checksum format.

## Root cause and source review

The pinned Pi 1.4.0 implementation independently corroborates the worker's two-part diagnosis:

1. `AgentSession.reload` awaits `session_shutdown`, invalidates the previous extension runtime, rebuilds it and emits `session_start` (`dist/core/agent-session.js:2998-3031`; `extensions/runner.js:80-86,813-840`). Dev.15 discarded the transport shutdown promise. Replacement startup could therefore race the old Go process's exclusive store lock.
2. Pi's `ui.custom` promise settles through its completion callback; UI reset can remove the component without invoking that callback (`interactive-mode.js:1898-1925,2288-2352,5328-5403`). A disposed generation opening recovery UI could leave its old command pending. Pi awaits `session.prompt`; later submissions can consequently remain queued (`:914-924,2686-2695,3405-3416`).

Dev.16 returns bounded transport shutdown to Pi, keeps state per extension factory, guards disposed generations, races pending interactions against closure, aborts supported dialogs and explicitly settles owned custom screens through a one-close lease. Editor cancellation has no pinned SDK contract; the adapter races its promise against disposal. Reviewed raw UI call sites are contained in these lifecycle wrappers. Existing paging/rendering and Go authority boundaries are not replaced.

Worker diagnostic before/after timings remain attributed worker evidence, not independently measured timings. The reviewer independently inspected the pinned SDK and adapter source and exercised the repaired boundary below.

## Independent actual-Pi runtime

Pinned Pi 1.4.0 launched through the shipped launcher with the compiled dev.16 Go child, generated isolated HOME/profile/config/env/state, and synthetic loopback calendar/MCP services. No model was configured; this is not a live model/authentication test. No real credentials or owner files were read.

| Scenario | Independently observed result |
| --- | --- |
| Held calendar collect → `/reload` → new-day `/timesheet` | Same Pi PID `1346575`; old Go `1346598` exited, replacement `1346694`; saved 60-minute day opened before the old HTTP response was released. Exactly two calendar requests: interrupted request and explicit new command; zero automatic replay. |
| Old calendar response released after replacement review | New day/review remained usable; no stale overwrite or recovery dialog. |
| Held classification read → reload → different date | New empty day opened; releasing the old catalog afterward did not take focus or replace the view. Exactly one classification request. |
| Normal editor and confirmation controls after replacements | A 30-minute draft was staged, then explicitly cancelled through confirmation; review returned to zero minutes and durable state hash was unchanged. A reviewer input-prefill mistake was corrected in the throwaway driver, not application source. |
| Quit while another collect was held | Pi/session and own Go child exited; no orphan remained. |

Evidence: `pending-collect-facts.json`, `catalog-and-dialog-facts.json`, captures `01`–`12`, and `cleanup.json` in the independent capsule. Across this review: four calendar requests, one classification request, zero provider mutation attempts. Synthetic server/thread, tmux and child processes stopped; generated fixture/profile/config/env/state tree removed.

## Attributed and retained evidence

- The hash-verified worker capsule reports actual-TUI applied-but-unacknowledged write/reload recovery: one `log_time`, honest unresolved outcome, matching-effect `not_applied` refusal and explicit adoption without replay. It also reports `/new` during pending model preparation, pending quit, idle reload and explicit reopen. Source/SDK review supports the shared lifecycle path. Those scenarios were **not independently rerun** in this review.
- Closed F1–F9 remain supported by the qualified dev.15 review and earlier accepted evidence. Cross-day approvals, byte/UTF-8 bounds and narrow display-cell controls were not broadly rerun; relevant source changes are version-only or lifecycle ownership, not reimplementations of those paths.
- Strict TypeScript, build and package tests in the worker report are retained worker evidence, not freshly rerun checks by this reviewer. The reviewer independently ran the compiled program and real TUI.
- `durability_uncertain` UI rendering remains **source-reviewed plus accepted R4 backend coverage**, not newly runtime-proven. The owner explicitly accepted this qualification. This is distinct from the worker's remote uncertain-write/recovery scenario.
- macOS, real accounts/credentials, authenticated model/subscription readiness and production writes remain unverified. No acceptance claim covers them.

## Authority

Qualified source acceptance does not stage, commit, publish, merge, release, authorize R6 or close an implementation issue before main integration. No application source was changed by this review. Earlier capsules and owner work were preserved. No automatic additional repair loop is opened.
