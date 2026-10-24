# R5 owner/technical adjudication

Status: **resolved — R5 dev.16 has qualified source acceptance** after independent lifecycle review closed C1-L1. The owner subsequently selected “Accept qualified evidence” for the remaining durability-uncertainty UI evidence basis. See `timesheet-r5-lifecycle-review.md` and `timesheet-r5-acceptance.md`. No contract reduction, automatic repair-loop reset, commit/publication or R6 authority.

## Two different gates

| Work | Current state | What is actually waiting |
| --- | --- | --- |
| R1–R3, #132–#134 | Source-accepted; PR #138 OPEN against main | Authorized merge of #138; its closing references already name all three issues |
| R4, #135 | Source-accepted dev.12; stacked PR #139 OPEN | Merge #138, retarget #139 to main, then authorize/merge #139; body already contains Closes #135 |
| R5, #136 | Source-accepted dev.16 within qualified Linux/hermetic scope | Separately authorized commit/publication and eventual main integration |
| R6, #137 | Not authorized | R5 acceptance and a separate bounded cutover release |

Issue closure follows implementation reaching main. Source acceptance permits development on the accepted stack; it does not merge PRs or close issues. The owner's question about open issues is not merge authorization. Accepted R1–R4 can be integrated independently of the R5 lifecycle decision. No existing accepted PR or issue was merged/closed by this review.

## C1-L1: verified remaining boundary

On pinned Pi 1.4.0 + real dev.15 Go, a collect was held at the generated loopback calendar. Actual `/reload` completed, the replacement child reported startup failure/EOF, and the displayed advice was to use Reopen child in `/timesheet`. A visibly entered `/timesheet` command did not open recovery or start Go.

A fresh-runtime control, with no prior custom form or Session Tree interaction, reproduced the same behavior for more than nine seconds after submitting the recovery command. There was no automatic collect replay or provider mutation. Restarting the entire Pi process with the same profile/config/binary restored review.

At dev.15 review, the exact blocked promise/upstream scheduling was unproven and stale continuations were an **[INFERENCE]**. Dev.16 investigation and independent pinned-SDK/source review established both the discarded shutdown promise/store-lock race and an unsettled custom UI/old-command continuation. The repaired boundary now passes independent actual-Pi recovery; see the lifecycle review for evidence attribution.

Independent safe proof: `/tmp/timesheet-r5fix2-review-h0gjbq_e/`, especially `pending-reload-facts.json`, `18`–`21` terminal grids, and fresh no-selector control `28`–`32`. The selected dev.15 source/binary is frozen there. Candidate manifest `5a0c2b6efe90ca6be77848ad92d1452c765513268ee0baab174b91b9ab5a3d9f` verified; executable `d56d07b0e6ed54e35c1d8262312bf315163ee95e4c916869eb30eb69bd073a5c`.

## Owner decision — preserve pending-reload support

Exact authorization: “authorize a narrowly scoped lifecycle investigation/fix preserving pending-reload support”.

Selected: establish the pinned Pi cancellation/command/UI lifetime, repair the demonstrated lifecycle boundary, and prove pending collect/reload/reopen plus pending-write uncertainty without replay. Preserve closed findings and all Go schema/accounting/approval semantics. Execution remains owner-mediated and inline under the bounded lifecycle packet; child dispatch is not authorized.

Not selected: restart-only recovery or a waiver of pending-reload support. The authorization does not grant merges, publication, R6, live access, broad redesign, or acceptance of the remaining evidence qualifications.

**Subsequent evidence decision:** after independent dev.16 lifecycle review passed, the owner selected “Accept qualified evidence”. Accepted basis: R4 backend coverage plus reviewed `durability_uncertain` renderer source, **not a newly driven UI case**. This supplies the remaining R5 acceptance gate without relabelling it runtime-proven. macOS, live accounts, real model/subscription authentication and production writes remain separate unverified gates.

## Authorized next action and retained boundaries

- The bounded lifecycle investigation/fix and independent review are complete; C1-L1 is closed, F1-F9 remain closed and qualified R5 source acceptance is recorded.
- Application work remains on `99a7f23336845ac6858706319e695a750f9116cd`, base HEAD `4dbe081`; owner changes and prior capsules were preserved. No staging, commit, push, PR change, merge/tag/release, child dispatch, R6 or real account access was authorized or performed by this review.
- The directing session reviewed application code read-only, ran isolated compiled-binary/actual-Pi scenarios, and records acceptance/governance/tracking. Generated reviewer fixtures and processes were removed.
- No additional automatic repair is opened. R5 commit/publication and R6 require separate authority; R1-R4 merge decisions remain separate. Issue closure still follows main integration.
