# R3 independent review — dev.7

Historical initial dev.7 review. Current disposition is the [dev.8 re-review](timesheet-rewrite-r3-rereview.md): most findings are closed; do not reuse this record's original next-action instruction as fresh execution authority.

Verdict: **Standards pass; Spec changes required. R3 is not accepted; R4 remains blocked.** Eight boundary scenarios reproduced eight findings below. None performed a remote mutation. This is R3's initial review, not a repeat of the completed R2 repair loop.

## Pinned artifacts and method

- Branch/base from the packet/report: `feat/timesheet-rewrite-v0.1.0`, `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Reviewed snapshot `/tmp/opencode/r3-20261022T172725Z/`.
- Manifest SHA-256 `cf82d978a19e1938dded2a5dfe222807af872453cd33176cbb39d47e47f11625`; independently verified all 34 packaged entries.
- Binary SHA-256 `c0c8342d96fd3b5df2d6f1231c58bb5a440eb6e5f9e537bb0981e9526e418d3c`; actual version command returned `timesheet-rewrite 0.1.0-dev.7 (protocol schema 1)`.
- All 13 current-source entries in `after-hashes.txt` independently matched. GitHub/calendar, the three frozen repair tests and go.mod/go.sum independently retain the accepted dev.6 hashes. The inspected 13-file inventory is within the R3 allowlist.
- Reviewed migration decoding/identity/audit/activation, read completeness, reconciliation/resolution, plan generation/hash/persistence and schema/store validation. Inline integration review; no child dispatch and no application source edits.
- Worker-supplied build/vet/package tests and 85 assertions remain separately attributed. They were not independently re-executed. No broad suite or reported host-sensitive Git failure rerun.
- New compiled-binary evidence: `/tmp/timesheet-r3-review-k84n7g00/boundaries.json`, SHA-256 `88760d223345a7eead318589672d4ed400cde7f0e1675d32647e683a33225ebd`. Eight private synthetic scenarios, loopback MCP declaring only the two read tools. Every child/service stopped and synthetic config/env/state/legacy directories removed; only sanitized evidence remains.

## Standards

No blocking Effective Go/style issue identified in the inspected changes. These findings are observable contract/logic failures, not missing mandatory tests or former TigerStyle/Go-rules violations. The existing regression evidence is useful but does not cover these combinations.

## Spec findings

| ID | Severity | Location | Observed failure |
| --- | --- | --- | --- |
| R3-F1 | P1 | `plan.go:153-175,540-550,610-640,718-814`; `store.go:300-310`; interface:552-554 | Cross-midnight preview uses the whole origin interval and one binding instead of the selected day's allocation. |
| R3-F2 | P1 | `migration.go:591-617,669-676,875-888` | Duplicate source local IDs are silently aliased; their remote mapping is redirected to the last interval. |
| R3-F3 | P2 | `migration.go:489-507,738-753,799-801`; `model.go:1047-1129` | Migration silently truncates provenance and reduces/overwrites historical audit data. |
| R3-F4 | P2 | `plan.go:392-432,564-572,785-803` | A successful foreign decision does not resolve a non-overlapping equal-text candidate. |
| R3-F5 | P1 | `gradion.go:144-163,202-269` | A target-day object without an `entries` property is accepted as complete emptiness and produces a ready create. |
| R3-F6 | P2 | `plan.go:833-853,922-943` | After accepting changed remote content, preview returns ready under an existing ID whose immutable durable record is still blocked. |
| R3-F7 | P1 | `plan.go:394-396,434-450,787-789` | A remote bound to a removed local interval is skipped during collision checks; replacement work gets a ready overlapping create. |
| R3-F8 | P2 | `migration.go:669-687,898-904`; legacy `submit/resolve.go:72-86` | Confirmed/definitively failed terminal history is incorrectly classified as unresolved solely because `resolved` is false. |

P1 impact is incorrect readiness or durable identity corruption, not an observed unauthorized write. R3 remains read-only. Original legacy source files were not changed; F3 concerns loss from the migrated target/audit, not deletion of the originals.

### F1 — daily allocation and remote identity

`cross-day-plan`: saved UTC work October 22, 23:00-01:00. Both October 22 and October 23 previews report ready with the identical October 22 create, 23:00-01:00, 120 minutes. Each local day contains only 60 minutes. The spill preview does not observe October 23's remote entries; it observes the origin date. Binding lookup is by interval ID alone, adoption rejects a remote dated on the allocation's later day, and validation prohibits a second binding for that interval.

This contradicts the packet's per-allocation identities and exact daily operations. The interface was changed to say one origin interval is one remote identity; that is an unauthorized semantic substitution, not a solution. Use allocation date/span/minutes and `(origin identity, allocation date)` binding semantics. Preserve all source revisions. If a remote endpoint projection is genuinely unsupported, block it explicitly; never mark an incorrectly dated/full-duration operation ready.

### F2 — ambiguous legacy identities

`duplicate-legacy-local-id`: two non-overlapping confirmed intervals in one source day both have ID `duplicate`, at 09:00-10:00 and 11:00-12:00. Its mapping names `duplicate`, remote `remote-first`, with 09:00-10:00 binding content.

Preview says ready and apply succeeds. The first interval retains ID `duplicate`; the second receives `iv-20261022-1`. `bd.remapped["duplicate"]` is overwritten, so the remote mapping now binds `remote-first` to the second 11:00-12:00 interval. No user identity decision occurred.

Reject ambiguous duplicate IDs within a source record before aliasing. Legitimate ID collisions across separate dates/target state need a date-scoped deterministic crosswalk; mappings, outcome references and audit must use the correct crosswalk. Do not repair ambiguity by guessing from time equality.

### F3 — migration preservation is not just counts

`migration-history-preservation`: two valid source dates contain 202 provenance references, separate review decisions, historical plan operations and approval timestamps. Migration reports ready and applied, but:

- Only 200 provenance references survive. A confirmed interval still cites `e-1-100`, whose provenance was dropped.
- A single `LegacyReview` pointer contains only the second day's review; per-item exclusion reasons/timestamps and attendance decisions are reduced to counts.
- Historical operation descriptions/content and approval timestamp disappear; only IDs/counts/inert flags remain. Inert authorization is correct, but does not excuse losing history.

The interval-audit slice is also statically truncated at 200. Bounds must reject an oversized candidate with no activation, or preserve the bounded requested data completely; never silently truncate a successful migration. Store known non-secret historical content and date association while keeping approvals unusable and provider raw documents excluded. Do not fabricate missing history or silently rewrite author text.

### F4 — accepted foreign decision has no effect

`foreign-equal-text`: local 09:00-10:00 and remote 14:00-15:00 share a generic description. A reviewed `foreign` decision succeeds against the exact current observation, but the next identical fresh preview remains blocked by the same equal-text `unresolved_identity` candidate.

Apply the current observation-bound foreign decision to identity hints. It must survive an identical fresh observation/restart and become stale when relevant observed content changes. A genuine physical remote overlap must remain a collision; foreign is not permission to double-log overlapping time or erase a binding.

### F5 — missing data is not an empty list

`missing-entries-property`: the read returns a structured week containing `{date:"2026-10-22"}` for the target day but no `entries` property. Go's zero-value slice hides the omission. The resulting observation has `complete:true`; preview reports ready and creates the hour.

The verified shape has an entries array. Validate required-container presence/type before claiming absence; explicit `entries:[]` remains a valid empty observation. Treat unknown/missing content as incomplete/unreadable and replace prior complete proof. Do not infer nullable behavior from a synthetic fixture alone or turn a read error into an empty success.

### F6 — returned readiness contradicts durable plan

`plan-readiness-persistence`: a mapped remote changed to exactly the current desired local content; a second local interval needs a create. First preview is blocked (`remote_changed`) but contains that second create. After explicit `accept_observed`, preview returns ready with the same operation content and same plan ID. Since the ID already exists, it returns `persisted:false` and leaves the stored plan blocked with the old blocker.

Readiness/resolution preconditions are missing from canonical identity or are being stored in the wrong immutable object. Make the returned ready preview and the durable plan it references agree, including after restart, without overwriting immutable history or making audit timestamps churn IDs.

### F7 — orphaned mappings cannot hide an occupied slot

`orphaned-mapping-collision`: a local 09:00-10:00 interval is explicitly adopted to `bound-remote`; the unchanged plan is ready with no operations. Removing that local interval, saving a new 09:00-10:00 interval under a new ID, then reconciling surfaces the old mapping as orphaned but still says ready. Preview emits a ready create overlapping `bound-remote`, with no blockers. The collision loop skips every bound remote even when its local interval no longer exists.

Check relevant observed collisions regardless of whether an entry is mapped to another/removed local identity. Surface relevant orphan conditions as blockers rather than treating a bound ID as proof the slot is free. Do not erase/reassign a mapping, hide the observed entry, or add a delete/reset/remap workaround to unblock the create.

### F8 — terminal outcomes are not pending writes

`terminal-history-not-uncertain`: a migrated mapped interval has normal historical `confirmed` and definitive `failed` outcomes, both with the default `resolved:false`. A complete current observation matches the mapped interval. Migration counts two unresolved outcomes, and preview blocks with “a preserved confirmed write outcome is unresolved” and the equivalent failed message.

The canonical legacy predicate (`internal/submit/resolve.go:78-86`) blocks unresolved `in_flight`, `uncertain` and `unidentified`, not confirmed/failed/skipped rows. Preserve their real status and resolution semantics. Do not clear actual uncertain history to fix this false blocker; unknown/malformed statuses must not silently become safe.

## Preserved positive evidence and limits

Artifact integrity, allowed scope and version checks passed. The new probes successfully exercised explicit classification, ordinary local confirm/save, successful migration, read-only observations/adoption and an unchanged mapped plan; the fixture saw only `timesheet__get_my_week` and `timesheet__get_classifications`. No source repair, child dispatch, commit, publication, live access or vault work occurred.

No macOS/live-account, Pi, approval or submission behavior verified. The worker's 85 assertions retain worker provenance. R1/R2 acceptance is not reopened; their frozen collectors/tests/dependencies retain their pins.

Next owner-mediated action: [first R3 repair packet](timesheet-rewrite-r3-fix-handoff.md). Fix these named findings only, preserve passed subcases and return focused runtime evidence. Do not advance to R4 or start a broad rewrite afresh.
