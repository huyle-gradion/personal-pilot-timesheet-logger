# R1 repair re-review

Verdict: **changes required; F3 remains open. R2 stays blocked.**

Historical re-review of dev.2. The later dev.3 repair closed F3; see [R1 acceptance](timesheet-rewrite-r1-acceptance.md). Do not repeat the completed narrow repair.

## Snapshot and method

Reviewed `/tmp/opencode/r1-fix-final-20261022T144049` on `feat/timesheet-rewrite-v0.1.0`, base `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe`.

- Manifest SHA-256: `a52fd9552a2cfc9e5102b009786cf83692c2985f8803eb65add8ac9fcc15bae9`.
- Binary SHA-256: `7df54fe7c37962e49a0e546aaaef043f07f82e2d522ca504ba2f5111effb9e2b`.
- All eight source-manifest entries and both hashes matched.
- Reviewed repaired model/session/store code, tests, interface, acceptance output and final-check output. No application source changed.
- Conducted inline independent integration re-review under owner-mediated execution. No child reviewers dispatched.

## Standards

No blocking style findings. Effective Go remains the baseline; tests are optional. The permanent repair tests exercise behavioral boundaries rather than source text or mock echoes.

## Spec: original finding disposition

| Finding | Disposition | Evidence |
| --- | --- | --- |
| R1-F1, durable candidate | Closed for the reviewed snapshot | Save validates durable origins, computes a durable receipt, and updates the saved history total. Supplied CLI acceptance covers both save orders and the unsaved-origin case. |
| R1-F2, final interval removal | Closed | Empty confirmation is allowed when removing previously confirmed work. Supplied CLI acceptance and behavioral tests cover save/restart and cancellation. |
| R1-F3, timezone semantics | Partially repaired, still open | Amsterdam spring/fall behavior and recorded-day timezone handling are addressed. Fresh probes expose incorrect local-day boundary resolution described below. |
| R1-F4, post-rename uncertainty | Closed for the local-store scope | Directory open/sync/close errors propagate through the activated/uncertain path; further saves block. Post-rename injection evidence is in Go fault-path tests in `final-checks.txt`, not the regular CLI acceptance log. Directory-open/close failure paths remain source-reviewed, not fault-injected. |

These closures are based on source inspection and the supplied CLI/test evidence, not a claim that this re-review reran those original scenarios. macOS and live integration remain unverified.

## R1-F3 continuation: P2, missing midnight is treated as a missing date

Locations: `internal/rewrite/model.go:408-431,437-455`.

`localDayStart` builds local `00:00` with `time.Date`. When a transition skips midnight, Go may normalize it into the previous date. The function then reports that the whole local date does not exist. `ResolvedAllocations` also resolves the following date boundary for an ordinary same-day interval, so the defect can reject work on the preceding day.

Fresh probes against the exact recorded executable:

| Timezone | Date | Entered interval | Expected elapsed minutes | Actual |
| --- | --- | --- | --- | --- |
| America/Santiago | 2026-09-05 | 09:00-10:00 | 60 | `invalid_interval`: local day does not exist |
| America/Santiago | 2026-09-06 | 09:00-10:00 | 60 | Same rejection |
| America/Havana | 2026-03-08 | 09:00-10:00 | 60 | Same rejection |
| Australia/Lord_Howe | 2026-10-04 | 01:30-03:00 | 60 | Accepted, 60 minutes |

All endpoints are outside the skipped hour. Expected values were checked with Python `zoneinfo` using the system timezone database. Full synthetic responses: `/tmp/timesheet-r1-rereview-uv47huo4/boundary-probes.json`.

Required correction: find the first real instant of an existing local date rather than requiring that date to contain `00:00`. Continue rejecting genuinely nonexistent endpoint wall times and wholly skipped dates. Do not special-case the named zones or fixture dates.

Also inspect `ResolvedAllocations` at lines 464-478: its continuation start label is hard-coded to `00:00`. [INFERENCE] Merely fixing the boundary resolver would render a skipped-midnight continuation with a nonexistent start label. Render the actual segment boundary and prove allocation labels as well as totals.

Concrete additional acceptance: Santiago 2026-09-05 23:00 through 2026-09-06 01:30 must total 90 elapsed minutes, split into 60 minutes before the date boundary and 30 minutes on September 6 starting at real local 01:00. Its total must agree with saved/reopened views. Explicit September 6 00:xx endpoints must still be rejected.

## Verification and next gate

New CLI probes used only synthetic config, dotenv and state, with a minimal child environment. All probe processes were closed. No real `.env.local`, configuration, records, transcript, ai-database, network, model provider, build or test suite was accessed/run by these probes.

Standards: 0 blocking findings. Spec: F1/F2/F4 closed; one remaining P2 continuation of F3. Next action: [narrow timezone repair](timesheet-rewrite-r1-timezone-fix-handoff.md), then independent re-review. Do not rerun the broad repair packet or begin R2.
