# TIMESHEET-REWRITE-R1 review

Verdict: **changes required; do not advance to R2**.

Historical first-review findings below. Subsequent repairs closed F1-F4; [R1 acceptance](timesheet-rewrite-r1-acceptance.md) records the accepted `0.1.0-dev.3` snapshot. Do not repeat the historical repair packets.

This is a read-only integration review, independent of the implementation worker. Standards and specification were assessed inline under owner-mediated execution; no child reviewers were dispatched. No application source was modified.

## Reviewed snapshot

- Branch: `feat/timesheet-rewrite-v0.1.0`; base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.
- Worker snapshot: `/tmp/opencode/r1-final-20261022T135057`.
- `MANIFEST.txt` SHA-256: `52dd0ea012592018f0177479ebe23047335481e0ab469a810d99facdfb9be028`.
- All eight entries in `source.sha256` matched the working files. The recorded binary hash matched `/tmp/opencode/r1-verify/timesheet-rewrite`.
- New probes ran that exact executable using synthetic data in `/tmp/timesheet-r1-review-fe_p31dj`. No real configuration, credentials, records, transcript, or ai-database contents were read.
- The worker's acceptance evidence was inspected at `acceptance-output.txt` inside the snapshot. Its ordinary happy-path observations stand; the probes below cover additional boundaries.

## Standards

No blocking style findings. Effective Go is the governing minimum; tests are optional. No TigerStyle, repository Go-rule, or mandatory-unit-test findings are raised. This is not a claim of macOS runtime support.

## Specification

### R1-F1: P1, a day can be saved against another day's unsaved state

Locations: `internal/rewrite/session.go:668-700,790-803`; `internal/rewrite/store.go:139-168`.

`origins` substitutes every opened day's in-memory confirmations for its persisted record. Validation and the save receipt therefore depend on unsaved changes in other days, while `Commit` persists only the requested day.

Reproduced with one process followed by restart:

1. Save October 22 work from 23:00 to 02:00.
2. Edit it to 23:00-01:00 and confirm, but do not save October 22.
3. Add October 23 work from 01:00 to 02:00, confirm and save October 23.
4. Save succeeds and reports 120 minutes for October 23.
5. Restart and open October 23: 180 minutes, with overlapping 00:00-02:00 and 01:00-02:00 segments.

This violates the packet's unresolved-overlap rejection, accurate totals, and restart-safe durable state requirements. Evidence: `initial-findings.json` in the synthetic review directory contains both full responses.

Required result: validate the complete candidate durable state and produce the receipt from that state. Do not silently save another day's changes. If independent saves cannot preserve the invariant, reject the dependent save with a clear action. The saved receipt and reopened state must agree.

### R1-F2: P2, the final confirmed interval cannot be retracted

Location: `internal/rewrite/session.go:623-647`.

After saving a day containing one interval, `remove_interval` successfully stages an empty day. `confirm` then returns `nothing_to_confirm`. A subsequent `save` succeeds but keeps the original confirmed interval and its 60 minutes. The caller cannot commit removal of the last interval through the supported interface.

This makes the required removal/revision workflow incomplete. The worker's removal demonstration only showed a staged total of zero, not a confirmed, saved removal.

Required result: explicit confirmation of a reviewed removal can establish an empty confirmed day, increment history/revision, save, and survive restart. Cancellation before confirmation must still preserve previous work.

### R1-F3: P2, configured timezone is not used for interval validity or duration

Locations: `internal/rewrite/model.go:211-275,297-335`; `internal/rewrite/session.go:879-917`.

The implementation uses clock-minute arithmetic without resolving dates/times in the day's timezone.

Reproduced for `Europe/Amsterdam`, 2026-03-29:

- 02:15-02:45 is accepted as 30 minutes even though those local times do not exist.
- 01:30-03:30 is reported as 120 minutes. Python's system timezone database resolves the endpoints to 60 elapsed minutes.

This violates the packet's explicit date/timezone semantics and accurate totals.

Required result: resolve interval endpoints and midnight allocations using the recorded day's timezone; reject nonexistent local times and handle repeated-hour ambiguity explicitly rather than guessing. Demonstrate spring-forward and fall-back behavior as well as ordinary midnight allocation. Reopening under a changed configuration must not reinterpret already recorded work.

### R1-F4: P1, durable success ignores directory-open/sync failures

Location: `internal/rewrite/store.go:204-212`.

After rename, failure to open the parent directory is ignored, and both `dirFile.Sync()` and `Close()` errors are discarded. `Commit` returns success and the protocol advertises a durable save without checking directory-sync success.

**Source-only finding.** No fsync fault or power loss was injected; `strace` was unavailable. [INFERENCE] An unsuccessful directory sync means the rename's crash durability is not established, despite the success receipt. The worker's tested failure occurs before activation and does not cover this path.

Required result: propagate durability failures and distinguish failures before activation from uncertain durability after rename. Do not simply return an error and roll back only memory while the activated disk file contains a newer revision. Stop further mutation or recover coherently; never falsely claim the previous file is intact after every possible write error. Update interface error guarantees accordingly.

## Exercised verification and limits

- Ran source/binary SHA-256 checks against the worker's manifests; all matched.
- Read all five implementation files, the public interface, governing R1 requirements, and the synthetic acceptance output.
- Executed fresh same-process/restart probes for cross-day save dependencies, final-interval removal, nonexistent local time, and a spring-forward elapsed interval.
- Compared the DST endpoints using Python `zoneinfo` on the system timezone database.
- All four probe processes exited; no service remains running.
- No build, vet, formatting, or unit test result is newly claimed by this review. The worker's reported checks were not relabeled as independent execution.
- No injected fsync error, macOS run, live source, model provider, migration, or remote submission was exercised.

Summary: Standards 0 blocking findings; Spec 4 findings, including persisted-overlap and false-durability risks. R1 is not accepted. Next owner-mediated packet: `timesheet-rewrite-r1-fix-handoff.md`.
