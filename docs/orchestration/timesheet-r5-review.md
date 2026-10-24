# R5 dev.13 independent review

Historical dev.13 verdict: **not accepted**. Its first repair has now failed [dev.14 re-review](timesheet-r5-fix1-review.md); active packet is [R5-FIX2](timesheet-r5-fix2-handoff.md), target dev.15. R5 commit/publication, R6 and live access remain blocked.

## Scope and identity

Owner-mediated, inline review; child dispatch was not authorized. Application files were not edited. Review/governance records and issue tracking are the only integration writes.

- Branch: `99a7f23336845ac6858706319e695a750f9116cd`; published baseline `4dbe08183d6a410a04589885a67541a0c51614e9`.
- Reviewed Go executable: `/tmp/opencode/r5/bin/timesheet`, copied before probing; version `0.1.0-dev.13`, protocol 1. SHA-256: `498f4f4b2e2c034cc9021b161211d0348d2e7113b6dde0fd49d9f221828e5055`.
- Independent evidence: `/tmp/timesheet-r5-review-snipl79o/`. Ten selected source files are frozen under `source/`; `source.sha256` SHA-256 is `58474e845fe36e9de49ebd2c488a43b875281cf1c92146ebe9ba1625bcf43cad`.
- Actual runtime: pinned Pi 1.4.0, Node 26.12.0, real PTYs, generated isolated profiles/config/env/state, synthetic loopback providers and the compiled Go child. No real credentials, owner records/config/transcript, global Pi profile, host Git test or ai-database access.
- Scoped tracked Go diff is only AppVersion dev.12 → dev.13. Dependency pins remain coding-agent 1.4.0, pi-tui 1.4.0 and typebox 1.3.27.

Standards: no blocker in the permitted Go constant change or exact dependency pins. The worker's type/build/vet claims were not rerun or promoted to independent evidence. **Spec/runtime: fail.** Passing static checks do not establish the consumer's protocol shapes or reachable controls.

## Acceptance blockers

All findings below are P2 acceptance blockers. Go's authority checks remained intact in the exercised failures; this is not a claim that those guards were bypassed.

| ID | Defect | Evidence and consequence |
| --- | --- | --- |
| F1 | Transport readiness and failure do not obey their lifecycle contract | `pi/transport.ts:277-288,346-370,389-415,453-457`. `failChild` drops the active request without settling it; silent shutdown drops queued callers too. With a real Go collect blocked at a loopback provider, killing the child left its promise pending after 1.8 seconds although state was `failed`. In a controlled protocol-child probe, a second `ensureReady` returned before negotiation and `open_day` reached a child whose incompatible identity was then rejected. `write(false)` is also wrongly treated as “not accepted”; Node backpressure is not proof that bytes were not accepted. |
| F2 | UTF-8 decoding depends on pipe chunk boundaries | `pi/transport.ts:210-225`. Each Buffer is independently converted with `String(chunk)`, and the purported byte bound uses string length. A controlled child split a valid response inside the first character: expected `合成😀`, received `���成😀`. This is a framing/decoder probe, not a real-Go business scenario. |
| F3 | Manual-only review crashes on valid null evidence lists | `pi/timesheet.ts:828-837,858,877`. The UI assumes non-null topics/meetings. Actual `/timesheet 2026-10-22` with sources disabled raised `Cannot read properties of null (reading 'length')` instead of showing the saved interval. R1-only use is required, not an exceptional configuration. |
| F4 | Origin operations use allocation display data as interval identity/content | `pi/timesheet.ts:1006-1103`. A saved 23:00–01:00 interval was prefilled as 23:00–24:00; accepting that unchanged editor was rejected by Go. No truncation/data loss was observed. Removing its locked spill on the following day switched the view to the origin, then sent the spill date with the origin revision: actual UI `stale_revision`, with work unchanged. Use the canonical/origin representation and the correct day/revision together. |
| F5 | Final removal hides required controls and misstates durability | `pi/timesheet.ts:750-751,894-911`. After removing the final interval, Cancel staged changes disappears. After confirming that empty draft, both lists are empty, Save local disappears, and revision 2 / saved 1 is labelled “no unsaved changes.” Actual TUI captures prove both states. The old revision remains durable; the operator cannot finish the deletion through this UI. |
| F6 | Required identity/recovery actions cannot address their actual targets | `pi/timesheet.ts:1528-1546,1623-1629,1991-1995`. Actual `accept_observed` selected a mapped entry but Go rejected it: `bad_request: remote_id is required`. The picker also excludes `changed`. Separately, an exact edit applied before an HTTP 503 acknowledgment failure left a native unresolved write. Its current observation listed the already-bound target under CHANGED, with no UNBOUND entries; UI adoption said “No unbound remote entries” and returned unresolved. The same observation, write reference and remote ID succeeded through Go `resolve_write`, with no additional provider mutation. |
| F7 | Submission presentation diverges from the receipt and overclaims failure certainty | `pi/timesheet.ts:242-248,1805-1813,1852-1857`; Go `WriteReceiptRow` at `internal/timesheet/submission.go:328-336` uses `status`, not `outcome`. An actual locally approved synthetic edit completed `all_confirmed`, but the per-reference UI rendered `wr-… ?`. Source-level safety defect, not a reproduced interrupted-submit screen: every thrown submit-path error is titled “zero remote mutations on refusal.” A lost dispatched response cannot prove zero effects; the lifecycle repair must not expose that false claim. Preserve any known receipt and distinguish unknown outcome from a proven pre-dispatch refusal. |
| F8 | Production launcher includes an out-of-contract extension escape hatch and dead fixture plumbing | `pi/launch.mjs:29-33,115-130,189-192`; `pi/README.md:65-71`. `--extra-extension` loads another extension despite the packet's trusted-adapter-only requirement. `--fixture-provider` forwards a test-only extension flag that the adapter does not register. These are trusted-operator flags, not a demonstrated model-controlled exploit. Remove shipped test provisioning; loopback model proof can use an externally generated dedicated profile. |

## Independent proof map

Evidence files are rendered terminal grids or compact protocol facts, not screenshots of a mocked ExtensionAPI.

- `transport-pending-result.json`, `transport-wire-result.json`, `transport-facts.json`: real-Go pending EOF/exit failure; separately labelled controlled-child negotiation and split-byte failures.
- `01-origin-review.txt`: manual-only null-list error. Despite the historical filename, this capture is an error screen, not a successful origin review.
- `04-origin-review-with-evidence.txt`, `05-edit-prefill.txt`, `06-unchanged-overnight-edit-rejected.txt`: canonical overnight interval, incorrect editor prefill, authoritative rejection.
- `07-final-removal-no-cancel.txt`, `08-confirmed-empty-no-save.txt`: empty staged/confirmed transitions and missing controls.
- `09-spill-day-review.txt`, `10-remove-spill-origin-routing-error.txt`: locked allocation and wrong-date/revision rejection.
- `11-mapped-reconciliation.txt` through `13-accept-observed-missing-remote-id.txt`: explicit identity choice and actual Go diagnostic.
- `20-ready-synthetic-edit-plan.txt` through `24-submission-per-reference-status-missing.txt`: exact edit displayed, explicit local approval and submit, then successful aggregate receipt with the missing per-reference status.
- `25-bound-edit-unresolved-status.txt` through `27-bound-edit-adopt-inaccessible.txt`, `bound-edit-facts.json`, `selected-protocol-receipts.json`: uncertain bound edit, inaccessible UI recovery, and successful explicit address-bound Go recovery with provider write count unchanged.
- `cleanup-and-model-counts.json`: review launchers/seed processes/providers stopped; direct workflows made zero requests to the instrumented loopback model routes. Profiles had no authenticated model. This is not subscription/live-model proof.

One synthetic edit was dispatched through the actual Pi approval/submit controls. A separate exact edit was explicitly approved/submitted through the real Go protocol to establish the lost-ack recovery fixture; it applied once and returned HTTP 503. Recovery was local/read-only toward the provider and did not replay either write. No production remote was touched.

## C1 — handback and remaining verification

The supplied `/tmp/opencode/r5/evidence-snapshot/MANIFEST.md` is prose, not a cryptographic source/binary manifest. That directory has no selected source rollback/final capsule, source diff or binary hash. Its retained screenshots/logs cannot independently bind all earlier worker claims to the reviewed source. The independent freeze above does not retroactively supply that missing provenance.

The repair must provide the original handback, not only new happy-path screenshots: hash-pinned source/binary/runtime identity, safe receipts, process/resource/tool/model/provider inventories and a criterion-by-criterion acceptance map. Keep qualified prior evidence labelled; do not claim it was rerun. Demonstrate busy-human versus preparation-tool isolation, pending cancellation/quit/reload cleanup, changed-entry acceptance, bound-edit recovery, migration no_change, zero-op restart, persistence_failed and inspectable long/narrow/resize surfaces. Missing evidence may be completed or a concrete blocker reported; it is not a silent scope waiver. Installed `pi/node_modules/` is currently unignored; exclude it from publication without changing dependency pins or staging unrelated owner files.

No existing broad suite or legacy host-Git failure was rerun. macOS, live accounts, authenticated GPT/subscription readiness and R6 remain unverified. No commit, push, merge, tag or PR mutation occurred in this review. R5 stays open in #136 under epic #131; PRs #138/#139 and accepted R4 remain unchanged.
