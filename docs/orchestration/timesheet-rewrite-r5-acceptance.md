# R5 — qualified source acceptance

Status: **accepted at 0.1.0-dev.16 within Linux/hermetic scope** after independent lifecycle review. F1–F9 remain closed; C1-L1 is closed. No known blocking finding remains in that scope. Acceptance is not publication or issue closure.

## Accepted identity

- Acceptance-time branch `feat/timesheet-rewrite-r5-v0.1.0`; base HEAD `4dbe08183d6a410a04589885a67541a0c51614e9`. Work was uncommitted at acceptance; subsequent scoped commit/publication authority is recorded in [publication gate](timesheet-rewrite-r5-publication.md), with actual identities tracked in #136/#131.
- Version `0.1.0-dev.16`, protocol 1, store 3; Pi/TUI/typebox pins `1.4.0`/`1.4.0`/`1.3.27` unchanged.
- Worker capsule `/tmp/timesheet-r5-lifecycle-20261024T1000`; manifest SHA-256 `6ac1b63c08f4f8af5afc56fc0032139075e12b834d184595f9cd482ce97ff263`; all 94 record hashes/sizes independently verified.
- Executable SHA-256 `5dceac1de905e77f3ecac7fdba6f07584807fe9dfb15467f8f9226cbc3fc9641`.
- Independent review `/tmp/timesheet-r5-lifecycle-review-fi2v_vn_/` and [technical review](timesheet-rewrite-r5-lifecycle-review.md). The report distinguishes fresh actual-TUI observations, source review, attributed worker runtime and retained prior evidence.

## Owner evidence decision

After the lifecycle review passed, the owner selected **“Accept qualified evidence”** rather than requiring another real-TUI durability-fault run.

Specifically accepted: `durability_uncertain` UI rendering is source-reviewed against accepted R4 backend coverage. **It was not newly exercised in the real TUI.** Remote uncertain-write recovery is separate evidence, not a substitute claimed as a local durability-fault run. No functionality is removed, no restart-only workaround is adopted and no runtime proof is fabricated.

This decision supplies the remaining evidence-basis gate in the adjudication. It grants no commit, publication, merge, live access or R6 authority.

## Preserved contract and remaining gates

Pending-reload support remains supported: replacement waits for old shutdown; disposed commands/dialogs settle and cannot take ownership of the new generation; no automatic collect/write replay. Independent actual-Pi runs proved held collect/reload/new-day recovery, delayed old-response isolation, a second pending classification/reload path, normal editor/confirmation controls and pending quit. Worker runtime evidence additionally covers interrupted approved-write recovery and pending preparation/session replacement.

R1–R4 semantics, schemas, dependency pins and accepted closed findings remain intact. macOS, live providers/credentials, authenticated subscription/model readiness and production submission remain unverified.

Issue #136 stays open until its accepted implementation reaches main. R1–R3 await authorized integration of PR #138; R4 awaits the separately authorized stacked PR #139 sequence. The owner's subsequent “next gate” authorizes only scoped R5 commit/publication, as recorded in the publication gate, not merging any PR. R6 requires a new bounded authorized packet; source acceptance/publication alone does not dispatch it. No release tag or claim of a shipped v0.1.0 release is made.
