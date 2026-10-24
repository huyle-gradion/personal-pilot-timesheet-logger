# R3 acceptance — reviewed dev.9

Verdict: **Standards pass; Spec pass for the accumulated qualified Linux/hermetic R3 scope.** F1-F9 are closed for this snapshot. The second repair converged after independent re-review; no third repair or owner/technical escalation is needed. R4 may be prepared, but implementation, live access, remote writes and publication require their separate authority.

## Accepted identity and source scope

- Branch/base from the authorized packet/report: `feat/timesheet-rewrite-v0.1.0`, `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. No commit/publication performed.
- Accepted artifacts `/tmp/timesheet-r3fix2-20261023/`.
- `evidence-manifest.sha256` SHA-256 `d09455e24603a38118eb76805e2bf43d2319daa269458640d308ad914b98f034`; independently verified all 34 entries.
- Binary SHA-256 `f865a46941da1e716feb6ebc06e4ab4015e8a8e69a0d42684904fc1e31febadc`; actual version command returned `timesheet-rewrite 0.1.0-dev.9 (protocol schema 1)`.
- All six `source/after-hashes.txt` entries independently match the current working tree. Planning, Gradion reader, store and plan tests independently retain the reviewed dev.8 hashes.
- Inspected the bounded field-copy/type/version/identity changes. Independently compared session.go to its complete frozen dev.7 copy: only the intended apply revalidation/no-change region changed. The worker supplied hash/region evidence rather than a complete before-copy for that file; the independent full comparison closes that provenance gap.
- Inline independent integration review, no child dispatch or application source repairs.

## Standards and C1

No blocking Effective Go issue. Tests are optional; no former TigerStyle/Go-rules gate imposed. The worker reports clean formatting, scoped vet/build and rewrite tests; these commands were not independently rerun.

C1 is corrected in the new package: scoped execution is explicitly reported, the known broad legacy Git failure was not rerun/reclassified, and the reviewed new report/summary do not repeat the prior host Git identity. The old frozen package remains historical private evidence, not a sanitized publication source. No host Git settings were inspected or changed in this review. Known workspace paths in source-integrity metadata are not a claim that all strings in every artifact are anonymous.

## Finding closure

F1/F2/F4-F8 retain the first-repair dispositions in the [dev.8 re-review](timesheet-rewrite-r3-rereview.md), with those code paths preserved. This acceptance does not re-label the worker's original 85/111 assertions as independently rerun.

F3 continuation: `LegacyApprovalAudit` now includes the approval's own record revision and remote-observation timestamp, copied verbatim from the source approval. They remain distinct from the linked plan's values; inert/unusable status is unchanged.

F9: every successful no-change apply now follows fresh source-identity, target-identity and conflict checks. Per-date repeat identity checks timezone as well as bytes, preserving valid same-interpretation subsets without confusing a changed timezone with already-imported work. No old approval is activated, no migration overwrite or retry added, and no remote authority granted.

Worker-supplied compiled evidence: four persistent serve sessions, 62 passing assertions covering ordinary repeat/restart, source changes after no-change preview, timezone reinterpretation, changed bound target, batch subset, exact historical approval fields and non-mutation. These remain separately attributed supplied observations.

## Independent compiled-binary smoke

New scenario: import three dates in two separate receipts, deliberately reusing a legacy local ID across dates. Repeat a selection spanning both receipts, restart, reject the prior-process preview, then obtain a fresh repeat preview/apply.

Observed/asserted:

- Two receipts and three durable days remain; both repeat applies return `no_change` with byte-identical store contents.
- The prior-process pending preview is rejected with `migration_stale`.
- Three interval IDs remain distinct; active remote bindings and terminal outcomes refer to the correct date-scoped aliases.
- All three approval audits preserve revision 17 and their own observation timestamps after restart, with `inert:true` and `usable:false`; linked plan revision 16 is not substituted.
- All synthetic legacy source bytes are unchanged. Durable state schema is 2.

No provider, credential or network service was needed: the child used a generated empty env-file, private config/legacy/state and a minimal environment without provider credentials. The process stopped and private fixture tree was removed after capture.

Evidence `/tmp/timesheet-r3-final-review-xbafiteh/`:

- `proof.json` SHA-256 `cb90bc3a3e66ae6e716226ee88c550fbd16cc57e3f22dce35999e3bd53e3df31`.
- `protocol.json` SHA-256 `567d99b8f21fb1e281df8f7e446acce41691bcf7d0e39e14fa1dabc34763f813`.

## Scope and next gate

Accepted R3 capability: explicit read-only legacy migration with identity/history preservation, inert historical approvals and unresolved-write blockers; read-only Gradion observation/reconciliation, explicit local identity decisions and exact immutable create/edit previews. R1/R2 acceptance remains qualified and unchanged.

Not verified: macOS runtime, live Gradion/GitHub/calendar behavior, actual credentials/config/store, Pi adapter/chat-provider path, approval or submission. R3's read boundary remains read-only. No remote mutation, commit, tag, publication, child dispatch or ai-database work occurred.

Next deliverable: an owner-release R4 packet for exact-plan human approval, durable pre-dispatch intent, verified outcomes and unresolved-write recovery. Preparing it is not execution authority or permission for any production submission.
