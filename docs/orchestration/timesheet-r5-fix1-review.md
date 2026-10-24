# R5-FIX1 dev.14 independent re-review

Verdict: **not accepted**. One R5 repair round has failed to converge. Next: [R5-FIX2](timesheet-r5-fix2-handoff.md), target dev.15; no commit/publication or R6. A second nonconvergent repair requires owner/technical adjudication, not automatic FIX3.

## Identity, scope and method

Owner-mediated, inline review; child dispatch was not authorized. Application sources were not changed. Review/governance records and issue tracking are the only integration writes.

- Branch/HEAD observed: `99a7f23336845ac6858706319e695a750f9116cd` / `4dbe08183d6a410a04589885a67541a0c51614e9`.
- Candidate `/tmp/timesheet-r5fix1-evidence-20261024T021046`: manifest SHA-256 `c7fa9be9d5ec449cc5c50cd3bf5f9698c541946b58632c32e6c8bef13fdcd9c1`; every manifest entry verified. All eleven selected live files byte-match `source/final/`.
- Candidate final-source manifest SHA-256 `6f2c1ff3287b786085f7923d608fab302de5a68fe8cd21baec36827fbe976f59`. Reviewed executable SHA-256 `fcde8e3dd3f0b97dcd524970d8e716f8e7bcb45524f4918840c6d615fdf089c5`; observed version dev.14 / protocol 1.
- Scoped tracked Go diff is AppVersion dev.12 → dev.14 only. Pi/TUI/typebox pins remain 1.4.0/1.4.0/1.3.27. No pin or permitted Go-delta blocker was found.
- New independent evidence: `/tmp/timesheet-r5fix1-review-w28g40c6/`. Selected sources and executable are frozen there. Business/UI probes used actual Pi 1.4.0, tmux, the real compiled child, generated private provisioning and read-only loopback MCP. Separate controlled children tested only protocol/framing faults.
- No existing build/type/vet suite or host-sensitive Git test was rerun. No real credentials/config/stores/transcript/global Pi profile, ai-database, macOS or live service access.

## Standards

Boundary-validation defect remains at `pi/transport.ts:314-348`: parsed payloads are asserted into envelope/status types before their relevant structure is validated. The TypeScript boundary-validation rule requires a check, not a type assertion. F1 below demonstrates the runtime consequence. This is not a request for an extra schema library or style-only refactor. No separate Go-style, dependency-pin or authorized-path blocker was found.

## Spec — remaining acceptance blockers

All four findings are P2. Go prevented the wrong-date submission; no unsafe remote write was observed.

| ID | Evidence | Required correction |
| --- | --- | --- |
| **F1 continuation — malformed protocol does not fail closed** | `pi/transport.ts:301-359,599-622`. Controlled status `{ok:true,result:null}` makes `checkIdentity` throw TypeError, clears the timer, leaves lifecycle `starting`, `failure:null`, and leaves the child alive. Another `ensureReady` starts a second child while the first is still alive. Separately, `{id:<matching>,ok:"not-a-boolean"}` resolves as a fabricated `ok:false/internal` error and leaves lifecycle `ready`, instead of failing the transport. `op` would label that fabricated error `from_child`, undermining the distinction between a genuine pre-dispatch refusal and a protocol failure. The last consequence is source-traced, not a reproduced unsafe write. | Validate envelope/identity structure before using it; malformed negotiation must fail/stop/settle, and subsequent ensureReady must require explicit reopen. Reject malformed envelopes as transport failures; do not manufacture a child refusal. Preserve the repaired settlement, single-flight and generation rules. |
| **F2 continuation — complete frames bypass the byte bound** | `pi/transport.ts:270-285`. The bound is checked only on the remainder after newline processing. A controlled child wrote exactly 16 MiB first, then the remaining bytes of a larger valid JSON response plus newline. The response, including a 16 MiB string and its envelope overhead, was accepted and state stayed ready. | Check each complete raw frame before decoding/dispatch, as well as an unterminated remainder. A newline or trailing whitespace must not exempt an oversized frame. Preserve split-byte UTF-8 correctness. |
| **F7 continuation — cached approval is not bound to day/plan** | `pi/timesheet.ts:2005,2022,2086-2095`. `currentActiveApprovalId(planId)` returns any cached lastApprovalId when `state.plan.plan_id === planId`, which is true for its current caller. Actual UI: approve A on Oct 22; open Oct 23 with a different existing active B approval; B's Submit dialog names A's approval while rendering B's source revision. Submit returns `blocked_before_dispatch/unknown_approval`, 0 reads/writes. After clean restart, the same B plan selects B's approval and zero-op submission succeeds. | Remove the unkeyed shortcut; derive the current active approval for the exact day/plan from the existing ledger read before displaying the submit confirmation. Do not silently replace an ID after asking the human or create a replacement approval. |
| **F9 — display-cell wrapping/paging remains incomplete** | `pi/view.ts:190-209,261-271`. Wrapping counts UTF-16 string length; Pi Text subsequently wraps wide glyphs into additional physical rows, but paging still counts the earlier chunks. Actual edit picker with `START-CJK`, 240 `界` characters and `END-CJK-ROW`: end visible at 60×26; at 40×24 it remains hidden after G and 30 j presses while controls remain visible. Resizing to 100×36 reveals the end. This is missing work text, not merely the admitted clipped paging label. | Use pinned Pi display-cell/grapheme-aware wrapping and actual rendered row budgets. Account for chrome/editor space; every row must remain reachable at the supported narrow size. Do not truncate records or fix this by hiding the completeness claim. |

## Findings closed or narrowed

- **F3 closed:** nullable/empty evidence rendering is corrected; independent real manual-only review loaded the saved 60-minute day and opened editing without the old crash.
- **F4 closed at reviewed source + pinned worker proof:** canonical overnight/midnight-ending text and shared origin/day/revision target are correct. The worker's cross-midnight captures are qualified evidence, not a claimed independent rerun.
- **F5 closed at reviewed source + pinned worker proof:** transition controls follow dirtiness/saved revision rather than nonempty lists; the final-removal/history evidence is retained in the candidate.
- **F6 closed at reviewed source + pinned worker proof:** mapped/changed accept targets include both IDs, and recovery obtains a fresh observation and offers bound targets. Go validation is unchanged.
- **F7 status/error presentation portion closed:** write rows consume `status`, and transport failures no longer automatically claim zero effects. Exact active-approval selection remains blocked as above.
- **F8 closed:** production test flags/forwarding are removed; the dependency ignore rule is present and pins remain unchanged.
- **F1 settlement/shared-startup and F2 valid split-byte portions are repaired:** do not reopen these successful subcases while fixing malformed identity/envelope and complete-frame boundaries.
- **C1 provenance portion closed:** this candidate supplies real source/binary hashes, before/final copies, bounded diff and labelled evidence. Full acceptance remains blocked by the findings and qualified limitations; a valid manifest is not behavioral acceptance.

## Independent evidence map

- `transport-boundaries.json`: controlled null-status startup/repeated-start observations and oversized complete-frame acceptance. Two child PIDs were observed alive during the malformed-startup probe; the probe then stopped both.
- `malformed-envelope-facts.json`: invalid boolean discriminator resolved as a fabricated child error while transport remained ready.
- `01-wide-review-60x26.txt` through `07-wide-row-restored-100x36.txt`, `wide-row-facts.json`: genuine terminal surfaces, including the unreachable CJK suffix and restoration on resize.
- `08-actual-reload.txt`, `09-post-reload-review.txt`, `reload-facts.json`: actual idle `/reload` succeeded; old child 1031082 was replaced by 1032341 and saved revision 1 / 60 minutes recovered. This closes the idle-reload gap independently, not pending-reload coverage.
- `10-plan-A-before-approval.txt` through `16-restarted-B-zero-op-success.txt`, `approval-cache-facts.json`: local A approval, approved B plan, wrong A submit ID with B revisions, authoritative zero-call rejection, and correct B success after restart. Provider mutation attempts remained zero.
- `cleanup.json`: own Pi sessions, real Go and controlled children stopped; loopback server stopped; generated provisioning/state/profile/driver fixtures removed. Original candidate and earlier review capsules remain intact.

The worker explicitly did not drive durability_uncertain through the UI; accepted R4 backend evidence plus renderer source are qualified, not a new UI run. Pending reload/cancellation is not proved by the independent idle-reload case. Preserve these distinctions in the next acceptance map. MacOS/live-model/subscription/provider readiness and R6 remain unverified.

No application edit, staging, commit, push, PR change, merge, tag or production mutation. PRs #138/#139 remain frozen. Track the remaining gate under #136 and epic #131.
