WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: migration identity/history preservation, duplicate remote creates, inert historical authorization
SCOPE: TIMESHEET-R3, explicit migration plus read-only Gradion reconciliation/plans
VERIFICATION: real persistent binary, synthetic legacy records and loopback MCP; zero remote mutations

# R3 owner-mediated packet

Status: implemented through dev.9 and **accepted for the qualified Linux/hermetic R3 scope**; see [R3 acceptance](timesheet-r3-acceptance.md). The two repair rounds converged after independent re-review. This packet is historical implementation authority, not a new drive. The [R4 approval/submission packet](timesheet-r4-handoff.md) is prepared but awaits owner release; no child dispatch, commit, publication or live-write authority follows from R3 acceptance.

## Authority, baseline and capabilities

Use branch `496a6ab8a81e9418a25046995ae40e2b8713e018`, packet base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`; preserve all existing owner changes. No commit, push, tag, publication, main-branch work, live provider/credential/store access, vault work or child dispatch. One source writer. State exposed tools, delegation permission and inline/specialist rationale once; do not discover capabilities through private config. Default remains inline/no children, not a claim that specialists are unavailable.

Accepted baseline: `/tmp/opencode/r2-f7-20261022T164214Z/`, binary SHA-256 `c0d3e11aa90faa50a5aa0caa88509218961b11b049194e5817f4c6b7e4a96afc`, version dev.6. `after-hashes.txt` SHA-256 `b7ef0c9dd4eb2be2120011ecb8fbcd29d7a419560e88f7688fd15a62dbf26d12` pins 14 current files. Read the named manifest, not a directory sweep. Preserve that baseline and the earlier frozen dev.5 artifacts. Capture a selected-file sanitized rollback before edits; never archive the whole working tree.

Target application `0.1.0-dev.7`, unreleased and untagged. Protocol remains `1` with additive operations. Introduce state schema `2` for the durable migration/remote/plan contract: load and validate existing implementation schema-1 state, preserve its complete work/history/evidence, and activate schema 2 only through a successful durable save/transaction, not a hidden startup write. Document the upgrade and old-loader fail-closed behavior. No shim CLI or permanent alternate product entry point.

## Exact surfaces

New implementation: `internal/timesheet/migration.go`, `internal/timesheet/gradion.go`, `internal/timesheet/plan.go`.

Integration writes: `internal/timesheet/{config,model,store,session}.go`, `.env.example`, `docs/agents/timesheet-interface.md`, `CHANGELOG.md`. `cmd/timesheet/main.go` may change **help/usage text only** if necessary to describe R3 honestly. Optional new focused behavior tests: `internal/timesheet/{migration,gradion,plan}_test.go`. All other code, old packages, existing tests and dependencies are frozen. Do not refactor collectors or fix unrelated observations.

Read capsule (ranges first; expand only within these named files when needed):

- This packet; `docs/orchestration/README.md:5-24`; `docs/timesheet-spec.md:96-106`; `docs/orchestration/timesheet-r2-acceptance.md`.
- Existing implementation integration files above, scoped by the functions/types being integrated; public interface sections for revisions, durable writes, typed intervals and evidence.
- Legacy schema: `internal/workflow/models.go:11-108,126-159,177-259,278-320,390-434`; file layout `internal/workflow/file_store.go:29-81`. These are read-only provenance, not runtime dependencies to import wholesale.
- Gradion tool names/bounds: `internal/gradion/tools.go:17-40,119-135`.
- Existing domain reads: `internal/gradion/client.go:54-74,120-249`. Old `VerifyCapabilities` requires write tools; do not reuse that behavior for R3.
- Verified week/reference shapes: `internal/gradion/parse.go:199-308`.
- Classification reference/text join and truncation: `internal/gradion/catalog.go:13-47,66-192`.
- Reconciliation precedent only if needed: `internal/submit/reconcile.go:23-83,120-200`. Do not import its approval/execution pipeline.
- Reusable transport: `internal/mcp/session.go`, `internal/mcp/jsonrpc.go`, with scoped navigation. Reuse the existing generic MCP transport unchanged; it takes endpoint/token as data and does not look up credentials.
- `.env.example` contains placeholders only. Do not read any real dotenv/config/records, host Git settings, transcript, journal or ai-database. Do not load the old daily skill, historical orchestration ledger or raw provider exports.

No repository-wide discovery or shell `cat`, `ls`, `find`/equivalent searching outside the capsule. Use exposed symbol navigation/scoped search within named files. Go LSP was unavailable in the preparing session; use it if actually available in yours, otherwise bounded source navigation. A truly missing prerequisite must be named, not compensated for by browsing private files.

## 1. Explicit, repeat-safe migration

The authoritative legacy daily format is workflow `Record` schema 1 at `<configured legacy records root>/workflow/YYYY-MM-DD.json`. Superseded files directly at the root are not current confirmed state: leave them intact and do not promote them into work. Never call the old store constructor: it creates directories/locks. Binary migration reads only configured, date-named regular files; no recursive discovery, arbitrary request paths, symlink escape or writes into the legacy root.

Add disabled-by-default provisioned migration settings for the legacy root and explicit legacy timezone. Old workflow records do not carry their governing timezone; do not silently reinterpret them in a changed new-config timezone. Dates are selected explicitly through the local protocol, bounded without silent truncation. Legacy writers must be quiescent; detect changed source bytes before activation and document that consistency precondition rather than pretending an unlocked multi-file read is atomic with an independent writer.

Implement `migration_preview{dates}` and `migration_apply{migration_id}`. Preview validates the complete candidate without activation and returns bounded counts/conflicts plus an identity bound to source hashes, dates/timezone and current durable target state. Apply rechecks those inputs and atomically activates exactly that candidate, or rejects stale/conflicting input without mutation. Require no opened/staged daily session during activation; do not discard a draft or leave session pointers stale after replacing durable state.

Preserve:

- Confirmed work, IDs (or explicit deterministic legacy-to-new ID mappings), projects, descriptions, explicit classifications, evidence provenance and source revisions. Never infer time from evidence or confirm carry proposals. Preserve separately confirmed carry fragments without duplicating them.
- Stable local/remote mappings and their binding versus last-observed content distinction, submitted outcomes, history, old plans and approvals. Preserve source material needed for audit in private binary-owned migration data; do not fabricate historical snapshots that were never stored.
- `uncertain`, `unidentified` and unresolved `in_flight` outcomes as durable blockers. Never replay or automatically clear them merely because a read found a candidate.
- Old approvals strictly as inert history. They cannot become active authorization, approve a new plan or enable a write. R3 has no approval/submission operation.

A matching already-imported source identity is a no-change result: no duplicate intervals, mappings, history or migration receipts, including after restart. Changed source or incompatible occupied destination dates are conflicts, never silent overwrite, time-slot-based merging or automatic rebinding. Reject invalid dates, unknown source schema, ambiguous/nonexistent endpoints, overlapping confirmed work, malformed/dangling identities and conflicting target data without partially importing the batch. Preserve original files and prior target bytes. Keep R1's pre-rename failure rollback and sticky post-rename uncertainty semantics.

Any legacy project/classification value not directly representable needs a provisioned explicit mapping or a visible conflict; do not invent a replacement. Keep original values in audit. Ensure future interval allocation cannot reuse a migrated ID. Historical provenance from retired collectors is archival only; it must not enable those collectors.

## 2. Read-only Gradion boundary

Add disabled-by-default non-secret Gradion configuration with a launcher-provisioned endpoint and token variable name; default token name `GRADION_MCP_TOKEN`, configurable by name only. Add only placeholder documentation to `.env.example`. Only the binary resolves the dotenv/inherited environment using existing precedence, including inherited-empty. Missing/invalid credentials report names/codes, never values, authenticated URLs or provider bodies. No Keychain dependency, real settings reads or implicit live endpoint calls in verification. Version still works without config/credentials.

Wrap the existing MCP transport with an implementation-domain reader. Initialize and verify **read capabilities only**. Allow exactly `timesheet__get_my_week` and `timesheet__get_classifications` through this boundary. Do not require or call `log_time`, `edit_time`, a delete tool, REST fallback or arbitrary tool names. Do not expose a generic MCP call operation. No new retries or external dependencies.

Use the existing verified structured week shape (`days[].date`, entry IDs/times/classification/description/task; week bounds) and classification reference/text join. Resource-reference entry envelopes, malformed/unknown shapes and failed reads are not an empty timesheet. Preserve unknown fields such as unobserved billable state as unknown, not false. Coverage must be explicit for missing ranges, duplicates/conflicting IDs, caps or partial reads. A failed fresh observation must not leave an old complete observation usable as current proof of absence.

Implement `classifications{query}` with bounded authoritative options and a catalog identity. The 20-row cap/filtered results do not prove absence. `select_classification{day,revision,interval_id,catalog_id,classification_id}` records an explicit reviewed choice for a returned option in staged work, following existing confirm/save semantics. Defaults and migrated unverified hints are suggestions, not silent remote selections. Preserve proven legacy explicit choices while reporting what must be revalidated; never derive a classification from a title or an AI response.

## 3. Reconciliation and exact plans, not execution

Implement `reconcile_remote{day}` and explicit local `resolve_remote{day,revision,observation_id,remote_id,local_id,decision}` with decisions `adopt`, `accept_observed` or `foreign` (local_id omitted for foreign). Reject stale revision/observation IDs. A fresh read alone never binds an unmapped entry: overlap/equal text is only a candidate. An adoption is a human identity decision against a specific observed entry, recorded durably with audit; accepting a changed mapped baseline is likewise explicit. These decisions do not clear migrated unresolved-write blockers or authorize remote mutation.

Surface mapped matches, changed/missing/orphaned mappings, unbound remotes/candidates and unresolved outcomes. Before declaring a safe create, require complete fresh remote coverage and resolution of relevant identity/overlap ambiguity; an empty local store proves nothing. Incomplete/failed reads and unresolved outcomes must block readiness while still allowing honest display of known evidence.

Implement `preview_plan{day}` from saved confirmed durable work, explicit classification choices and fresh read-only reconciliation. Never plan from an unsaved origin/day or silently save a draft. For cross-midnight allocations, retain origin interval identities and bind all contributing origin revisions; per-allocation remote identities must not collapse two dates onto one mapping.

Render exact create/edit operations, stable local/remote identities, intended fields, relevant observed preconditions and blockers. Unchanged mapped work yields no write operation; remote-only/orphaned entries never imply deletion. Edits preserve unselected remote-only fields. Do not silently shorten intervals, lose midnight minutes, invent timezone/server policy support or guess unknown attributes. Any projection the verified contract cannot represent must be an explicit blocker, not altered work.

Canonical plan identity binds the daily/source revisions, exact operation content and relevant remote-content/coverage fingerprint. Audit timestamps alone must not make identical fresh observations different plan contents; actual local/remote changes must invalidate the plan. Persist enough immutable plan/audit data for R4, but distinguish `ready` from `approved`: there is no usable approval or remote write in R3. Document every new request/receipt/error and the state upgrade; preserve existing R1/R2 operation semantics and no-source behavior.

Maintain one-to-one stable bindings per remote allocation. Adoption cannot steal another local allocation's remote ID or silently replace an existing conflicting binding; `foreign` cannot erase an active binding. `accept_observed` accepts content for the same mapped remote identity, not a time-slot-based identity substitution.

## Acceptance: actual compiled process

Use a private synthetic legacy root/new store and loopback MCP provider that declares only read tools; make any write call a hard fixture failure. No real credentials/config/store or live service. Build once after edits, run scoped static checks once, then exercise the real persistent binary. Optional deterministic regression tests should target consumer-visible boundaries; they do not replace runtime proof. Do not rerun broad legacy suites or the reported host-sensitive Git failure.

Prove:

1. Existing implementation schema-1 work/history/evidence survives schema-2 upgrade and restart. Manual/no-Gradion workflow remains usable.
2. Migration of confirmed/classified work, remote IDs, revision/audit/history, inert approvals and unresolved outcomes; source bytes unchanged. Unconfirmed carry stays unconfirmed. Repeated preview/apply/restart does not duplicate data or grant authorization.
3. Invalid/mixed/conflicting/stale-source migration rejects the whole candidate without losing target state; ID allocation after import remains unique. Keep write-failure durability semantics rather than implementing a separate unsafe persistence path.
4. A migrated stable mapping reconciles to the observed entry without a duplicate create. Empty/unmapped local identity with an existing matching remote blocks create until explicit adoption. Missing/changed mapped remote, stale human resolution and unresolved-write history cannot become ready silently.
5. Exact valid create and edit previews; unchanged mapping yields no operation. Classification is explicit; a partial catalog cannot prove an ID absent. Partial/failed/malformed/ID-only remote observations block readiness. An identical fresh observation keeps canonical content stable; changed work/remote content changes the plan.
6. Restart preserves migration receipts, mappings, resolution/audit, blockers and plan content; prior approvals remain unusable. Approval/submission requests are rejected, and the fixture records zero remote mutations throughout.
7. Version/status identify dev.7, protocol 1/state 2; state/protocol/output contain no token, authenticated endpoint or raw provider document. Keep compact status/receipts model-safe; full work/migration/remote views belong only on the private local UI transport, not exported files for an agent to read.

Retain selected source copies/diff/hashes, actual compact protocol evidence, provider tool-call inventory, assertion summary, binary identity and explicit provenance of checks in a new sanitized `/tmp/` review directory. Remove throwaway drivers/config/env/state and stop services after evidence capture; preserve no secrets or owner data. Report exact files, schema/compatibility changes, behavior proven and macOS/live/PI limits. Stop review-ready for independent review. No self-acceptance, R4, commits, publication, child dispatch or repair of unrelated findings.
