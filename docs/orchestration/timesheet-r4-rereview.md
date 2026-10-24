# R4 first repair re-review — dev.11

Historical dev.11 verdict. Superseded by [qualified dev.12 acceptance](timesheet-r4-acceptance.md); F2/F7/F8 and V1 below are closed for the accepted snapshot. Do not repeat the historical continuation gate.

Verdict: **Standards pass in reviewed source; Spec fail. R4 remains unaccepted.** F1/F3/F4/F5/F6 are closed for this snapshot. F2 remains open on an unsaved final-removal path; two additional zero-operation durability/restart defects are independently reproduced. Evidence qualifications also remain.

## Authority, identity and provenance

Owner-mediated independent re-review of issue #135 and the first repair packet; no children dispatched, application source edits, commits, push, merge, live access, private credentials/config/store/transcript reads or production mutations. One directing reviewer worked inline within the existing authority. PR #138 remains the separate published R1-R3 baseline.

- Reviewed worker evidence: `/tmp/timesheet-r4-fix1-20261023/evidence/`.
- Outer `evidence-manifest.sha256`: all **165 entries** verify; SHA-256 `8794b55f3ce5f769f07e94ed65f2f3901eeaf9a8b9b563798d11f2747bbab421`.
- Binary SHA-256 `560e59388066d7cb4b34453deb003eab6d7a11f7b420a716fb56e390c51bffbb`; executed version `timesheet 0.1.0-dev.11 (protocol schema 1)`.
- Nine tracked application/interface/changelog files match the frozen diff's target Git blobs; four new source/test files match their complete snapshot copies. Diff rendering differs because current Git prefixes/algorithm differ, not because target source blobs changed. Full reviewer source copies and SHA-256 pins retained independently.
- All 121 retained assertion records say passed. These are worker execution records, not a claim that the reviewer reran that harness. Source inspection, selected actual transcripts and provider inventories were checked independently. Build/vet/package-test claims remain attributed. The reported earlier host-Git failure was not rerun or reclassified.
- Source changes remain inside the R4/repair allowlist. No hard Effective Go violation identified in the reviewed changes; the functional and evidence findings below determine acceptance.

## Original findings

| Finding | Disposition | Basis |
| --- | --- | --- |
| F1: classification semantics | Closed | Selected relevant ID/name/code/activity type enters canonical operations and approval proof. Fresh proof is compared. Independent type-change probe blocks with zero mutations. |
| F2: dirty affected-date footprint | **Open, P1** | New spill staging now blocks, but an unsaved confirmation removing the final interval leaves no current footprint to inspect; the saved footprint is omitted. |
| F3: existing/self remote content drift | Closed | Expected content advances only by verified self-effects. Independent three-operation run observes outside modification to the first verified entry after operation two and stops operation three. |
| F4: edit acknowledgment identity | Closed | Wrong edit acknowledgment ID is unresolved; entry resource kind is checked. Independent wrong-ID probe confirms zero operations and preserves the original binding/LastObserved identity. |
| F5: migrated spill unresolved gate | Closed | Pending outcomes use affected dates and actual allocation relevance. Independent real migration yields a blocked spill preview and refused approval with zero writes. |
| F6: migrated spill adoption | Closed | Recovery resolves origin interval plus allocation date and validates migration linkage. Independent real spill adoption succeeds without writes and survives restart; source bytes unchanged. |

## Remaining behavioral blockers

### R4-F2 continuation — P1: unsaved confirmation can erase the only dirty footprint

Location: `internal/timesheet/plan.go:809-815,844-879`, particularly 876.

The plan still renders from saved `st.Days`, but the dirty guard inspects only `open.Confirmed` and `open.Staged`. After an unsaved confirmation removes the final interval, both are empty. It never inspects `st.Days[openDate].Confirmed`, despite the comment claiming to cover saved footprints.

Compiled-binary reproduction: save/classify Oct 20 09:00-10:00 at revision 2; preview and approve; remove its final interval; confirm revision 3 without saving. Local confirmed interval count is zero. Submit the existing approval: one create is dispatched for the removed interval and receipt is `all_confirmed`.

Required: use the saved durable footprint as well as the session's confirmed/staged footprints for every dirty origin. Check both approval and consumption; keep unrelated dirty dates usable. A removal that has been confirmed but not saved must never authorize the saved stale view. Original packet line 55 and repair F2 explicitly require unsaved confirmations and saved/current footprints.

### R4-F7 — P2: failed zero-operation consumption reports durable success

Location: `internal/timesheet/submission.go:1209-1228`.

`consumeWithoutWrites` distinguishes only post-rename `uncertain`; a pre-activation failure returns an empty persistence result, which falls through to `all_confirmed`.

Compiled-binary reproduction: first create and verify the mapping, preview a ready zero-operation plan, explicitly approve it. During submission's fresh classification call, replace the private state parent directory with a regular file **after** preview persistence has succeeded. The consumption transaction fails. Receipt still reports `all_confirmed` with an execution ID, while the actual durable approval remains `active` and no corresponding execution is durable. No mutation occurs during this no-op submit; its setup create is separately counted in the provider inventory.

Required: handle pre-activation failure explicitly, return a truthful stable failure/blocked receipt, leave approval reusable only because no consumption activated, and never invent a durable execution. Preserve sticky post-rename uncertainty. The original packet requires durable ledger transitions and honest persistence receipts; zero operations are not an exception.

### R4-F8 — P2: new zero-operation approval loses its proof across restart

Location: `internal/timesheet/approval.go:83-91,308-315`; `submission.go:698-704`.

A current-format zero-operation approval correctly has an established empty choice map, but `json:"choices,omitempty"` drops it on persistence. Reload turns it into nil, indistinguishable from a pre-proof dev.10 approval. Submission rejects it as stale even when every precondition is unchanged.

Compiled-binary reproduction without any failure injection: create/save/verify mapped work; preview zero operations; approve; clean restart; explicitly submit the approval. It is refused with `stale_approval` and the message that no classification choice proof was bound. The newly persisted summary omitted `choices`. No new remote mutation occurs.

Required: durably distinguish an established empty proof from a missing legacy proof. Current zero-operation approvals must survive restart; old/missing-proof approvals must remain unauthorized. Do not weaken the nil/missing-proof safety gate or reactivate legacy approvals to work around this.

## Evidence qualifications — V1

1. **G4 does not reach the claimed intent boundary.** `harness-source/scenarios3.go:378-401` makes the state parent unwritable before `submit_plan`. `transcript-g4.jsonl:20` reports `plan revalidation failed before any dispatch: local state cannot be written`; the preview observation transaction failed before intent construction. This proves a safe earlier failure, not intent-commit failure. The independent reviewer supplied a genuine compiled CLI intent-boundary probe: obstruct parent at submit's classification read after preview persistence; receipt explicitly identifies failed durable intent, zero mutation calls; restore parent and the same unconsumed approval executes once. Attribute that proof correctly rather than claiming the original G4 did it.
2. **G9 is not preservation-through-activation proof.** `scenarios3.go:449-511` checks schema/version status, restarts an untouched schema-1 file, and loads a sparse schema-2 fixture. It never activates these fixtures as schema 3 or checks their confirmed work/history/evidence/migration/remote data after activation and restart. The assertion labelled "with work preserved" checks only schema/version. Complete the original acceptance group 9 with actual rich schema-1/2 data, a successful durable transaction and restart comparisons; retain inert-approval checks.
3. **Nested source manifest is defective, despite the valid outer manifest.** `source-snapshot/source.sha256` includes its own stale hash and fails that entry; it pins only seven full source files plus diff, not all thirteen. The outer 165-entry manifest is valid, and tracked target blobs plus four new files establish reviewed source identity, so this is not a fabricated binary/source finding. Next snapshot must omit manifest self-inclusion, name its coverage accurately and preferably pin every owned source directly. Never edit frozen artifacts to hide the mismatch.

Do not repeat unrelated broad suites or the known host-Git failure. Correct the evidence claims and provide only the still-missing proofs plus changed-path regressions; the old 121 rows need not be rerun merely to change a count.

## Independent runtime evidence

New directory: `/tmp/timesheet-r4fix-review-vu0ku8l3/`.

- `boundaries.json`: `b862cce2c29ce60bbc293ee9f286df04376fc0c7dd329e2fdf3ce10ac20f81b4`.
- `protocol.json`: `76adb1814da2792670bb63583b0fc4980fcab524a86ce7215eb5bf45f4fcf7e0`.
- `provider-inventory.json`: `d81d85bcf6f72c8ae3c437731c8179c98461706223ea463023b214e39849def5`.
- Full thirteen-file source capsule `source.sha256`: `2ef5d2c84cf19685091fca33bb3427b044e40c5acb946b9f5d1891d1e2f5302b`.

Ten named compiled-binary scenarios across nine private provider/child fixtures: three failing boundaries F2/F7/F8 and seven passing control scenarios, including the genuine intent-write failure and safe approval reuse, classification change rejection, new spill rejection, wrong edit-ID rejection, verified-prefix content drift stop, and migrated spill gate/adoption/restart. All mutations are synthetic and follow explicit approvals. All child/provider processes stopped; generated config/env/store/legacy fixtures removed. No private owner data or live access; macOS/Pi/live behavior remains unverified.

## Next gate

R4 #135 remains open and review-blocked; no accepted R4 PR or R5 authority. First repair did not converge. Use `timesheet-r4-continuation-handoff.md` for the second bounded repair (F2 continuation, F7/F8, V1), then independent re-review. If the second repair also does not converge, stop for owner/technical adjudication under the two-round policy, not an automatic third implementation loop. Do not commit or append unaccepted R4 to PR #138.
