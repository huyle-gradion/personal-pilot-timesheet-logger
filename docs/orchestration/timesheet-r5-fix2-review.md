# R5-FIX2 dev.15 independent review

Verdict: **F1/F2/F7/F9 repairs pass; overall R5 acceptance withheld for owner/technical adjudication.** The required pending-reload path failed new runtime verification. Two repair rounds are exhausted; no automatic FIX3 or R6. Decision record: [R5 adjudication](timesheet-r5-adjudication.md).

## Why R1–R4 issues remain open

Source acceptance and integration are different gates. The issues already say accepted/awaiting merge; they are not waiting for R5 implementation.

- Live [PR #138](https://github.com/huyle-gradion/personal-pilot-timesheet-logger/pull/138) is OPEN against `main`, with closing references to R1–R3 #132/#133/#134.
- Live [PR #139](https://github.com/huyle-gradion/personal-pilot-timesheet-logger/pull/139) is OPEN against `496a6ab8a81e9418a25046995ae40e2b8713e018`. Its body contains `Closes #135` and the explicit order: merge #138, retarget #139 to main, then merge #139. Closing references are currently empty on that non-default base.
- All four issues are OPEN. Their bodies explicitly distinguish source acceptance from merge; #135 says it stays open until its implementation reaches main.
- Working on the accepted R4 branch base permits R5 development but is not merge authority. The user's question did not authorize merging either PR. Closing the issues now would misstate the agreed integration gate.

This is direct live tracker/PR and governing-policy evidence, not an inference from implementation code. No chat/observability/analytics search was needed to interpret this explicit project rule; no matching historical systems were used.

## Reviewed identity and scope

Owner-mediated, inline review; no child dispatch. Application code was not changed. Only review/governance records and authorized GitHub tracker status were updated.

- Branch/HEAD observed: `99a7f23336845ac6858706319e695a750f9116cd` / `4dbe08183d6a410a04589885a67541a0c51614e9`.
- Candidate `/tmp/timesheet-r5fix2-evidence-20261024T090805`: all 84 entries verified; outer manifest SHA-256 `5a0c2b6efe90ca6be77848ad92d1452c765513268ee0baab174b91b9ab5a3d9f`.
- All eleven selected live files byte-match final sources; all eleven before copies byte-match frozen dev.14. Final-source manifest SHA-256 `c7586e845271813c7609f222661fbe16a7ffbe24c103a6a20be9e0e32cca90cc`.
- Reviewed executable SHA-256 `d56d07b0e6ed54e35c1d8262312bf315163ee95e4c916869eb30eb69bd073a5c`; observed dev.15/protocol 1. State schema remains 3.
- Entire public lock metadata matches dev.14 after excluding only root version stamps. Pi/TUI/typebox remain 1.4.0/1.4.0/1.3.27. The tracked Go delta is AppVersion only.
- Independent evidence `/tmp/timesheet-r5fix2-review-h0gjbq_e/`: frozen sources/binary, controlled protocol-child facts, actual Pi 1.4.0 tmux grids with the real Go child, generated private fixtures and read-only loopback providers.
- No existing static/build suite or legacy host-Git failure was rerun. No real config/credentials/records/transcript, owner Pi profile, ai-database, live timesheet/provider/model or macOS access. GitHub PR/issue tracking reads/writes are separate authorized bookkeeping.

## Standards

**Pass within reviewed scope.** Relevant wire fields are validated before use, frame bytes are checked before decoding, the unkeyed approval cache is removed, and pinned display-cell wrapping replaces UTF-16 slicing. No Go-style, dependency-pin or authorized-path blocker was found. No speculative refactor is requested.

## Spec — named repairs closed

| Finding | Independent changed-path proof |
| --- | --- |
| F1 | Concurrent null-status startup rejects safely, failed ensureReady does not create another child, explicit reopen works. Nonboolean ok, malformed error and invalid UTF-8 fail the transport. A valid refusal remains a refusal with a healthy transport. |
| F2 | The old limit/pause/remainder+newline oversized frame is rejected. An exactly bounded frame succeeds; split CJK/emoji/combining text is preserved. Source applies the same raw-byte check to completed frames and unterminated remainder. Other boundary rows remain qualified verified worker evidence. |
| F7 | In one actual Pi session, A approval is created, B's existing active approval is selected/displayed/sent, B zero-op submission is all_confirmed, A remains active and its dialog still names A. Cancelling A dispatches nothing. B is no longer active afterward. Provider mutation attempts remain zero. |
| F9 | CJK + ZWJ emoji + combining text is reachable in the actual edit picker at 40×24/60×26/100×36. The same full suffix is reachable in a real one-create plan at those sizes with approval controls visible. Paging/resize leaves the durable store byte hash unchanged. |

Previously closed F3/F4/F5/F6/F8 and C1 cryptographic provenance remain closed. This is not a claim that their whole matrices were independently rerun.

## C1-L1 — pending-reload recovery remains a P2 acceptance blocker

This was an explicitly unverified original acceptance criterion, not new product scope: original R5 handoff runtime criterion 6 requires quit/reload/replacement and cancellation while pending; the second repair packet retains that qualification and requires adjudication if it cannot converge.

Observed sequence with the actual pinned launcher/TUI and compiled binary:

1. Start `/timesheet` with calendar enabled and hold the real child's collect at a synthetic loopback HTTP response.
2. Enter the real `/reload` command. Pi reports reload completed, then displays child startup failure/EOF and instructs the operator to use Reopen child in `/timesheet`.
3. Release the provider. A visibly typed `/timesheet <date>` is submitted from the displayed root editor, but no child-reopen dialog, review or Go process appears. No additional collect reaches the provider.
4. Repeat the causal boundary in a new generated runtime with no previous custom form or Session Tree interaction. The same behavior persists for more than nine seconds after the visible recovery command. This rules out the earlier exploratory selector detour as the sole cause.
5. A full Pi process restart with the same profile/config/binary restores the review normally. No provider mutation or automatic collection replay was observed, and all processes were subsequently stopped.

The failure is recovery/lifecycle usability, not an observed unsafe write. **[INFERENCE]** old command/UI continuations or cancellation ownership are implicated: `pi/timesheet.ts:511-516` resets module state and fires shutdown without awaiting it; the pending collect's catch at `745-757` can subsequently call `offerChildReopen`/custom UI. The exact suspended promise and pinned Pi's internal command scheduling were not introspected. Do not present this inferred cause as a verified patch design.

No code repair was made or dispatched. An owner/technical decision is required before further implementation. Accepted Go R1–R4 work is not reopened by this frontend lifecycle finding.

## Evidence map and qualifications

- `closure-status.json`: observed PR/issue state and closure sequence.
- `transport-facts.json`: eight labelled controlled-child outcomes; no business/UI claim from a fault child.
- `01-review-40x24.txt` through `04-wide-picker-*`: actual wide picker content and reachable suffix.
- `18-explicit-recovery-command-visible.txt`, `19-explicit-reopen-dialog.txt`, `20-pending-reload-command-did-not-open.txt`: visible command then absent recovery; despite historical filename 19, no dialog appeared.
- `21-pending-reload-full-restart-control.txt`, `pending-reload-facts.json`: same-provisioning process-restart control and no-replay facts.
- `28-no-selector-reload-command-visible.txt` through `32-no-selector-recovery-after-deadline.txt`: fresh-runtime causal control, no selector interaction, bounded recovery failure.
- `22-A-approval-recorded.txt` through `25-A-own-approval-after-B-submit.txt`, `approval-facts.json`: correct cross-day identities, B consumption, A preservation/cancel.
- `26-wide-plan-100x36-initial.txt`, `27-plan-*`, `paging-facts.json`: real plan rows, reachable suffix, controls and unchanged store hash.
- `cleanup.json`: owned Go/Pi/fault children, both loopback servers and tmux stopped; generated provisioning/state/profile/drivers removed. Earlier capsules untouched. Intermediate selector/focus diagnostic captures were excluded rather than promoted to proof.

Durability_uncertain UI is still source-reviewed plus accepted R4 backend prior evidence, not a new real-UI run. Owner adjudication must explicitly address any remaining acceptance qualification; do not silently relabel it passed. macOS, real accounts/model subscription and R6 remain unverified.

No staging, commit, push, PR mutation, merge, tag, production write or automatic third repair. R5 #136 waits for adjudication; epic #131 distinguishes accepted R1–R4/pending merge from R5's acceptance blocker.
