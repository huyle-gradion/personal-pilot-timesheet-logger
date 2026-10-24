# R6 dev.17 independent review

**Standards: pass. Spec: changes required — two P2 documentation defects.** No executable cutover defect was observed. Actual macOS proof remains a separate blocker to full R6 acceptance; it is not a Linux repair finding. Owner-mediated, inline review; no children or application/product-document edits. The bounded next action is [R6-FIX1](timesheet-rewrite-r6-fix1-handoff.md), documentation only, not another runtime implementation.

## Identity and verified scope

- Branch `feat/timesheet-rewrite-r6-v0.1.0`; baseline `99a7f23336845ac6858706319e695a750f9116cd`. No commit/publication authority follows this review.
- Worker source manifest `/tmp/r6-snapshot-manifest.sha256`: SHA-256 `63af50ad912627d4b517ca6428f285a879c6a68ae48184087cb677a02a99c460`; all 41 records independently matched and their contents were frozen by the reviewer.
- Executable `timesheet 0.1.0-dev.17 (protocol schema 1)`, SHA-256 `dc3dbf5054a17d71108aee9f7285e1ca832f285a5f63f2d277da743b840a738a`; copied before runtime probes. State schema remains 3.
- All 225 retirement rows are absent, including the skill symlink itself. The deletion receipt exactly matches the authorized baseline paths/blob IDs; SHA-256 `a340dc887c5701bd06e97780bf0a573996534b66f0860cc8ef4940bc78858417`. All 28 read-only files match baseline contents.
- Production main's body is identical to the accepted server after branding replacement. AppVersion/transport differ only by dev.16 → dev.17; normalized lock metadata is identical after root stamps are removed. Module changes only remove requirements/checksums; remaining direct pins are unchanged. Independent `go list ./...` returns exactly `cmd/timesheet`, `internal/mcp`, `internal/rewrite`.
- Prepared packet/inventory and preserved plan/orchestration-entrypoint content match the pre-worker preparation snapshot. The reviewer did not read/probe transcript, real dotenv, config, store, profile, Keychain or vault.

## Standards

No documented Effective Go violation or actionable baseline smell in the permitted cutover. The entrypoint is reused, not a second workflow engine; no aliases, new dependencies, core behavior or schema changes were introduced. Optional tests were not required or added by this review. Worker build/vet/tsc/retained-test/Darwin results remain worker-reported, not fresh reviewer results.

## Spec findings

### R6-F1 — P2: restart guidance falsely says staging survives

Location: `README.md:97-98`. It says “Only staging and durable saves survive” immediately before saying unsaved session drafts do not. Staged interval edits are session-only. The wording can cause loss of unsaved human work and contradicts the required confirm/save/restart contract.

Independent compiled-binary proof: save 60 minutes at revision 1, stage another 30 minutes, observe staged 90/dirty, close and restart. Confirmed/staged views return to the saved 60 minutes, dirty=false, saved_revision=1. The real Pi review displays the same 60/60 result. Evidence: `migration-and-staging-facts.json`, `protocol-facts.json`, `03-saved-work-without-unsaved-staging.txt` in the independent capsule.

Fix only the guide: unsaved staged edits and confirmed-but-unsaved interval changes do not survive restart. Successfully committed state and durable ledgers reload; startup does not dispatch/replay. Do not weaken save semantics or erase persisted active approvals to fit prose.

### R6-F2 — P2: public exit-code guidance overstates exit 2

Locations: `README.md:23-25`; inherited ambiguity in the now-current `docs/agents/rewrite-interface.md:45-46`. The new guide says extra arguments exit 2. The preserved executable exits **1** for `serve unexpected-positional` and `serve --unrecognized-flag`; `version extra` and an unknown top-level command exit 2. Missing required serve flags were also explicitly reported as exit 1 by the implementer; that reported case was not rerun to reconfirm it.

Evidence: `cli-observations.json`, source `cmd/timesheet/main.go:44-79`. This is a documentation/consumer contract repair, not permission to change the preserved CLI behavior. Document the distinction between top-level/version usage errors and serve argument/startup/stream failures.

## Independently exercised runtime

The real pinned Pi 1.4.0 launcher and the frozen production binary used generated isolated HOME/profile/config/env/state, synthetic legacy data and a loopback calendar only. No GitHub/MCP/model/live provider was enabled in these reviewer runs.

| Boundary | Fresh observation |
| --- | --- |
| Production CLI | Version exits 0; version extra/unknown top-level command exit 2; unknown serve flag/extra serve positional exit 1. No resource file required for version. |
| Rich migration and process restart | PIDs 1472714 → 1473388. Two imported intervals, revision 3, origin 180 minutes plus locked 75-minute spill; binding legacy-1 → r-100, inert approval audit with usable=false, plan/evidence/revision audit and unresolved mw-k-1 preserved. Repeat preview/apply is no_change. Entire durable state is byte-identical across restart/repeat and source bytes are unchanged. This supplies the implementer's explicitly missing migrated-day restart case. |
| Saved versus staged | Saved 60 minutes survives; unsaved 90-minute staged view does not. Same result visible in real Pi, child PID 1474338 across migrated origin/spill/manual views. |
| Pending reload and delayed response | Held collect on production PID 1476606; actual `/reload` replaces it with PID 1476694. New-day `/timesheet` opens before old response release and stays usable afterward. Exactly interrupted + explicit fresh collect, no automatic replay. |
| Pending quit | Another held collect followed by Ctrl-D exits Pi and child; no orphan. Three calendar requests across these phases, zero provider mutation attempts. |

Capsule: `/tmp/timesheet-r6-review-g99in8nl/`, frozen 41 sources and executable, CLI/protocol/migration/audit facts, seven terminal captures, lifecycle facts and cleanup. Generated fixtures/config/env/state/profiles were removed; owned tmux, children and server stopped. No application source was changed by the reviewer.

## C1 — evidence provenance qualification, not a third code blocker

The implementer retained source/deletion checksum files but removed original runtime recordings. The supplied runtime matrix is therefore retained as **attributed worker observations**, not mislabelled independently inspected captures. `worker-reported-observations.json` records that distinction, including the synthetic 100/10 footer, unavailable cache counters, and request bytes (not token counts or tool-result byte size).

This review supplies a frozen candidate/binary and fresh actual CLI, migrated restart, staging, Pi surface, pending reload and quit evidence. Other GitHub/attendance, synthetic MCP create/edit/recovery and model-request rows remain worker-reported, supported by unchanged accepted core/harness source and qualified prior R5 evidence; they were not broadly replayed merely to replace removed logs. Do not fabricate historical screenshots or convert report-only/prior rows into newly observed results. The documentation-only repair must retain a compact new manifest/results record instead of deleting all result facts again.

## Remaining gates and next action

- Correct F1/F2 only in README/interface plus one truthful changelog clarification. Keep dev.17, binary/core/Pi logic, dependency pins, retirement and ownership fixed. No new implementation, test framework, full-suite or full-matrix rerun is needed for prose changes.
- Independent re-review follows that bounded doc repair. No source acceptance, staging/commit/push/PR/merge/tag/release or full R6 completion is declared here.
- Actual macOS Pi/migration/local flow is still mandatory before full R6 acceptance. Darwin compile-only and Linux proof do not satisfy it. Live account/model authentication and production writes remain separately unauthorized/unverified.
- Preserve the already owner-accepted durability_uncertain UI qualification: reviewed renderer + accepted R4 backend, not newly UI-fault-proven. Remote uncertainty evidence is not that local fsync case.

Summary: Standards 0 findings; Spec 2 findings, worst P2. Source cutover checks pass; documentation corrections and macOS acceptance gate remain distinct.
