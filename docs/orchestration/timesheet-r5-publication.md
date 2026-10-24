# R5 commit and publication gate

## Authority

The owner said **“next gate”** after the proposed next gate was the scoped R5 commit/publication, with R6 separately gated. Together with the standing instruction to track the application through issues and PRs, this authorizes a local commit of accepted R5 work and a new stacked R5 PR only.

No permission is inferred for merging any PR, modifying main, retargeting existing PRs, starting R6, child dispatch, releases/tags, live accounts/credentials or production timesheet writes.

## Exact stack and ownership

- Accepted R5 baseline: `4dbe08183d6a410a04589885a67541a0c51614e9`, the published head of R4 PR #139 (`4dbe08183d6a410a04589885a67541a0c51614e9`).
- R5 branch: `99a7f23336845ac6858706319e695a750f9116cd`; new PR base: `4dbe08183d6a410a04589885a67541a0c51614e9`. Do not publish R5 onto either earlier accepted PR branch.
- Commit scope: the eight explicitly named production files under `pi/`, AppVersion-only `internal/timesheet/model.go`, `docs/agents/timesheet-interface.md`, `CHANGELOG.md`, the ten R5 handoff/review/acceptance/adjudication records, and this publication record.
- Preserve unstaged owner-governance changes in `.omo/plans/timesheet.md`, `docs/orchestration/README.md` and `docs/orchestration/timesheet-r4-acceptance.md`. Do not absorb those complete files into this scoped commit.
- Do not read or stage `transcript_full.jsonl`, credentials, owner config/stores/profiles, generated evidence, dependencies, fixtures or binaries. Stage explicit paths only.

## Accepted evidence

[R5 acceptance](timesheet-r5-acceptance.md) and [independent lifecycle review](timesheet-r5-lifecycle-review.md) govern the shipped dev.16 snapshot. Source files must match the frozen accepted copies before staging, and staged blobs must match the selected snapshot before committing. Dependency pins/integrities, protocol/store schemas 1/3 and all Go semantics remain unchanged.

Independent review capsule `/tmp/timesheet-r5-lifecycle-review-fi2v_vn_/`: standard manifest SHA-256 `5c171e53723bafed0e5ba3b44e7ac5f038ef9c42f47b41379d7a9852e25f15cc`, 38 records verified. Executable SHA-256 `5dceac1de905e77f3ecac7fdba6f07584807fe9dfb15467f8f9226cbc3fc9641`.

Qualified Linux/hermetic acceptance explicitly retains source-reviewed/prior-R4-backend evidence for `durability_uncertain` UI; it is not a newly exercised TUI fault case. macOS, real provider/model authentication and production writes remain unverified. No source reimplementation or broad test rerun is part of this publication gate.

## Integration and tracking

Record the actual commit and PR identities in issue #136 and epic #131 after successful operations. The PR references `Closes #136`; the issue stays open until main integration. Closing a stacked PR into its feature-branch base is not a substitute for this gate.

Required integration order remains separate: authorize/merge #138, retarget and authorize/merge #139 into main, then retarget and authorize/merge the new R5 PR into main. No merge or retarget is performed by this gate. R6 requires a separate bounded authorized packet even after R5 is committed/published.
