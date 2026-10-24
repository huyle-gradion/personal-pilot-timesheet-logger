WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: incorrect ready plans, corrupted migrated identities, lost audit, false remote absence
SCOPE: TIMESHEET-R3-FIX, R3-F1 through R3-F8 only
VERIFICATION: repaired real binary with focused synthetic migration and loopback MCP scenarios

# First R3 repair packet

Status: executed for dev.8; historical first repair packet. [Focused re-review](timesheet-r3-rereview.md) closes F1/F2/F4-F8, retains a two-field F3 continuation and adds F9. Next action is the [second bounded R3 repair packet](timesheet-r3-continuation-handoff.md), not another broad F1-F8 implementation.

Owner-mediated continuation of the authorized R3 slice. R3's initial review found eight blockers; R3 is not accepted and R4 remains blocked. Fix these findings only, not the whole implementation afresh. No child dispatch, real credential/config/store access, live provider, remote mutation, commit, push, publication, vault work or unrelated cleanup.

## Baseline, ownership and read bounds

Branch `496a6ab8a81e9418a25046995ae40e2b8713e018`, packet base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`. Preserve owner changes. One source writer; use the implementation session holding R3 context if available. State exposed tools, delegation permission and inline rationale once. Do not inspect private configuration to discover capabilities.

Frozen reviewed reference `/tmp/opencode/r3-20261022T172725Z/`: manifest SHA-256 `cf82d978a19e1938dded2a5dfe222807af872453cd33176cbb39d47e47f11625`, binary SHA-256 `c0c8342d96fd3b5df2d6f1231c58bb5a440eb6e5f9e537bb0981e9526e418d3c`. All 34 entries were independently verified, as were the current 13 source entries. Preserve this snapshot, accepted R1/R2 references and selected-file rollback; do not reconstruct or overwrite the frozen artifacts.

Read:

- This packet; `docs/orchestration/README.md:5-24`; `docs/orchestration/timesheet-r3-review.md` for exact scenarios/locations.
- Compact observed evidence `/tmp/timesheet-r3-review-k84n7g00/boundaries.json`, SHA-256 `88760d223345a7eead318589672d4ed400cde7f0e1675d32647e683a33225ebd`.
- `internal/timesheet/plan.go`: contributing allocations, binding lookup, reconciliation/resolution and preview/hash/persistence functions (locations in the review).
- `internal/timesheet/migration.go`: source decoding, candidate/audit/ID construction and outcome predicate; `internal/timesheet/model.go`: affected durable types/version; `internal/timesheet/store.go`: relevant validators/commit; `internal/timesheet/session.go`: affected dispatch/revision/persistence boundaries only.
- `internal/timesheet/gradion.go:115-272` for structured week validation; additional ranges in this same file only if needed.
- Original historical contracts `internal/workflow/models.go:11-108,126-159,177-259,278-320,390-434`; canonical unresolved predicate `internal/submit/resolve.go:43-86`. Do not import the old runtime or execute legacy code.
- Existing R3 behavior tests, public interface sections for R3, and changelog insertion point. The original R3 packet is provenance if a scope question remains, not an instruction to redo all discovery.

No repository-wide searches, directory sweeps, raw provider exports, transcripts, actual dotenv/config/records, host Git settings, journal or ai-database. No shell `cat`, `ls`, `find`/equivalent out-of-scope reads. Use scoped navigation and LSP if available; Go LSP was unavailable in the directing session. Treat the observed failures as ground truth; do not rerun the broken binary merely to confirm them.

## Write allowlist

- `internal/timesheet/{migration,gradion,plan,model,store,session}.go`, only changes needed for F1-F8.
- Existing R3 `internal/timesheet/{migration,plan}_test.go`; optional `internal/timesheet/gradion_test.go` for meaningful boundary coverage. Tests are optional, not a mandatory test/style gate.
- `docs/agents/timesheet-interface.md` and `CHANGELOG.md`, matching repaired semantics and dev-build identity.

Everything else is frozen: `config.go`, CLI, `.env.example`, generic MCP transport, GitHub/calendar collectors, R1/R2 tests, old packages, go.mod/go.sum and orchestration documents. If a fix genuinely needs another path, report the exact dependency rather than expanding ownership silently. Target `0.1.0-dev.8`, unreleased/untagged. Keep protocol 1; prefer compatible state-2 extensions. Any necessary persisted/request shape adjustment must be documented and validated against supported inputs, never silently reinterpret existing history or discard accepted schema-1 work. Do not invent a schema downgrade or approval migration.

## Required corrections and focused proof

### F1 — per-allocation plans and bindings (P1)

Current spill-day preview repeats the full origin-day operation. Generate the selected allocation's date/span/minutes, observe that remote date, and bind all contributing source revisions. Use stable `(origin interval identity, allocation date)` binding semantics throughout lookup, adoption, persistence and validation, rather than one remote ID for the entire origin interval. Later-day adoption must be possible against its actual allocation. Correct the interface's substituted “one origin interval is one remote identity” wording.

Prove 23:00-01:00 UTC is 60 minutes on each date, never two 120-minute origin operations; the later preview observes the later day and catches an existing later-day entry. Bind different remote IDs to the two allocations without stealing either mapping. Persist/restart retains both. Preserve R1's midnight/DST accounting; explicitly block a genuinely unsupported remote projection rather than altering work or declaring it ready.

### F2 — unambiguous migration crosswalk (P1)

Reject duplicate local IDs within a source record before assigning aliases or constructing bindings. The observed ambiguous record must fail the whole candidate with unchanged target/source bytes; do not pick the last row or guess from its time slot.

Legitimate collisions across separate dates or with existing target IDs may use deterministic, date-scoped aliases. Preserve the original ID and date in audit, translate active mapping/outcome references consistently, and do not collapse distinct crosswalk entries into an unscoped map. Prove that valid aliased records keep the intended remote identity and unresolved history link, and that post-import allocation remains unique.

### F3 — preserve audit, never silently truncate (P2)

Remove success-path slicing/breaking that silently loses required evidence or interval audit. Either preserve the complete bounded selected batch or reject it with an explicit capacity conflict before activation; deliberately smaller batches must remain supported and repeat-safe.

Preserve date-associated known non-secret historical plan operations/preconditions, approval/revocation timestamps/status as inert audit, review exclusion/attendance decisions and relevant original interval/classification/project attributes. Counts alone are not historical records; a single last-day review pointer is not a multi-day audit. Keep authored text faithful. Do not fabricate snapshots, import raw collected provider documents or make old approvals usable.

Prove the 202-reference case either retains all references (including confirmed work's final reference) or refuses atomically with a truthful bound. Also prove an in-bound two-date batch retains both dates' distinct review decisions, historical operation contents and inert approval timestamps, across restart. Originals remain intact.

### F4 — meaningful foreign resolution (P2)

Honor an exact current-observation foreign decision when evaluating equal-text identity hints. A non-overlapping remote with the same generic description can be explicitly marked foreign and must no longer block that create solely for equal text. Identical fresh observation/restart preserves the decision; changed relevant observation makes it stale.

Do not treat foreign as permission to physically overlap an existing remote interval, erase an active mapping or clear an uncertain write. Preserve real collision blockers and observation/revision checks.

### F5 — required week containers (P1)

Validate required day/entries container presence and type before claiming complete coverage. `{date:D}` without `entries` is not an empty array. Recognized `entries:[]` stays a valid empty result. Do not infer nullable semantics from a fixture's zero value; use the verified contract or fail closed.

Prove that a previously complete observation is replaced by incomplete/unreadable data when a fresh target-day response omits the required entries container. No ready create may be produced. Keep malformed/reference-only/tool-error behavior, bounded partial evidence, safe redaction and two-read-tool-only transport unchanged. Update synthetic fixtures to the real contract if needed, not the contract to accommodate a malformed fixture.

### F6 — coherent immutable plan identity/readiness (P2)

Include relevant resolution/validation preconditions in canonical identity, or separate validation from immutable content correctly. Returned ready preview and the durable plan it references must agree. Do not overwrite immutable historical content under an existing ID just to hide the mismatch, and do not add audit timestamps to make every observation produce a new ID.

Prove the mapped remote changed to exactly the desired local content plus a second pending create case: blocked preview -> explicit accept_observed -> ready preview -> restart. The returned plan references the correct ready record, not the old blocked one. Identical fresh observations with unchanged decisions remain stable; real work/content/decision changes invalidate the applicable identity.

### F7 — bound/orphan remotes are not free slots (P1)

Do not skip an observed collision merely because its remote ID is bound to another or removed local identity. Surface relevant orphan/identity conflicts and block unsafe creates. Keep unrelated dates scoped correctly; do not globally block all work because any historical orphan exists elsewhere.

Prove the unchanged adopted interval first yields no operation, then its removed/replaced local interval cannot receive a ready create over the still-existing mapped remote. Do not erase/reassign mappings, hide observed entries, clear history or introduce delete/reset/remap endpoints as a workaround. If a safe resolution is not represented by existing authorized operations, return the explicit blocker rather than false readiness.

### F8 — preserve terminal versus unresolved outcomes (P2)

Replace the blanket `!Resolved` rule with the canonical legacy status/resolution meaning. Normal confirmed, definitively failed and skipped outcomes are not pending merely because their manual-resolution flag is false. Actual unresolved in_flight/uncertain/unidentified outcomes remain blockers. Unknown/malformed statuses must not silently become safe.

Prove a correctly mapped, freshly observed interval with terminal confirmed/failed history is not counted as unresolved and yields the appropriate unchanged plan. Prove genuine unresolved histories remain blocked, survive migration/restart and cannot be cleared by foreign/adoption/content acknowledgment.

## Verification and evidence

Use existing pinned Go tooling; no dependency installation. After all edits run formatting/build/scoped checks once. Existing tests that encode a contradicted R3 contract must be corrected to the real behavior, not re-pinned to the old defect; frozen R1/R2 tests stay untouched. Optional new tests must catch genuine consumer-visible failures, not source text, wiring, mock echoes or incidental wording. No broad legacy suite or host-sensitive Git-test rerun.

Required smoke: repaired compiled persistent process, private generated config/legacy/state, explicit synthetic env-file, minimal environment and loopback MCP declaring only the two read tools. Fail the fixture on any mutation call. Exercise each corrected transition and its persistence/error boundary; do not substitute test results or copied JSON for actual runtime output. No real settings/credentials, live accounts, approval or submission. Keep compact receipts/evidence; do not export full provider responses for an agent to read.

Retain selected-file rollback/final copies, bounded diff, source/binary hashes, version/schema identity, compact genuine protocol evidence, assertion summary and tool inventory in a new private sanitized `/tmp/` snapshot. Preserve dev.7 and accepted R1/R2 artifacts. Remove throwaway fixtures/drivers and stop processes/services. No prior 85 assertions claimed as re-executed unless actually exercised.

Report F1-F8 dispositions individually, exact files and artifact paths, observed checks versus supplied evidence, compatibility changes and limits. Stop review-ready for independent re-review, not self-accepted. This is the first R3 repair round; do not start another round or R4 yourself. If two repair rounds fail to converge, return to owner/technical review under the control policy.
