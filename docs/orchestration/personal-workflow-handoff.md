# PERSONAL-WORKFLOW-1 handoff

Resumable handoff for the personal daily workflow work (slices A, B, and the
records/usage documentation). This is a status document, not a grant of authority.
Read it with [orchestration state](state.md) and [the implementation plan](implementation-plan.md).

**Source baseline at handoff creation:** branch `feat/s9-integrated-acceptance`,
HEAD `6d28f7a`, with two modified tracked pointer files and two new untracked
documentation files. The **usage guide** (`docs/agents/product-usage.md`,
new), a pointer in `docs/agents/collection-config.md`, a pointer in
`docs/orchestration/README.md`, and **this handoff**
(`docs/orchestration/personal-workflow-handoff.md`, new) are uncommitted
changes at drafting time. A subsequent documentation-only commit may contain
them. A next agent must run `git status` and `git rev-parse HEAD` first and treat
the tree, not this dated snapshot, as truth.

## Read first

- `AGENTS.md`, `docs/orchestration/README.md`, [control policy](control-policy.md),
  [handoff contracts](handoffs.md), [commit and vault protocol](commit-and-vault.md).
- Product spec: [the redesign spec](../timesheet-redesign.md) and repo
  [ADR 0005](../adr/0005-review-first-timesheet-architecture.md) (vault mirror
  ADR-006). Journal source: [ADR 0007](../adr/0007-journal-evidence-source.md)
  (vault mirror ADR-007).
- Operator guide: [product usage](../agents/product-usage.md). Source detail:
  [collection configuration](../agents/collection-config.md),
  [journal source](../agents/journal-source.md),
  [guided workflow](../agents/guided-workflow.md),
  [classification selection](../agents/classification-selection.md).

## Accepted slices

All three are committed locally only — **nothing pushed, merged, or published.**

| Slice | Commit (parent) | Files | Independent review |
| --- | --- | --- | --- |
| A — optional read-only journal evidence source | `195fb4e` (`60a7398`) | 28 | reviewer @oracle `ses_ed519bb97ffezdIVnHYg2SdFIB`; F1–F5 fixed, re-reviewed; F6 no action |
| B — guided daily review through explicit local gates | `543966d` (`195fb4e`) | 20 | reviewer @oracle `ses_ed4bf8b7affeX1oncc9R5PtUMb`; B1–B8 fixed, re-reviewed |
| Records — accepted personal workflow and shared skill docs | `6d28f7a` (`543966d`) | 7 | documentation; skill/records tests |

Implementers: A @fixer `ses_ed53249a6ffeR6icLqiQSDDMsI`; B @designer
`ses_ed53dd0c6ffeUfMyeIix2B7kMp`. Contract review @oracle
`ses_ed53e2522ffezLk6e5Lfc1rodp`.

### Verification artifacts (parent-verified)

Approved temp root:
`/private/var/folders/r4/j1g3f1_57sq15v3sfz922wpw0000gq/T/opencode`.

| Artifact | Contents | Snapshot HEAD |
| --- | --- | --- |
| `personal-workflow-a-fix1-review/` | `manifest.json` (29 files), `HEAD.txt`, `tracked.diff` | `60a7398` |
| `personal-workflow-b-fix1-review/` | `manifest.json` (21 files), `HEAD.txt`, `tracked.diff` | `195fb4e` |
| `personal-workflow-records-final-fix1-review/` | `manifest.json` (7 files), `HEAD.txt`, `tracked.diff` | `543966d` |

Independent reports: `pw1-fix1-review-report.md`,
`pw1b-fix1-independent-review-report.md`,
`pw1-final-records-fix1-independent-review-report.md`. Parent check logs:
`personal-workflow-a-final-checks.log`, `personal-workflow-b-final-checks.log`.
The manifests are **SHA-256 of working bytes**, not git-blob hashes. Temp
artifacts **may expire**; the committed contracts and tests reconstruct the source,
so never cite a temp path as an immutable permanent reference.

Parent final checks were all green: `go build ./...`, `go vet ./...`, uncached
`go test ./...` (25 packages), targeted `-race` (A 6, B 4 packages), hermetic e2e,
the guided harness, and `git diff --check`. The documentation-only reruns (skill
tests, guided offline harness) are green.

### Source acceptance vs production readiness

Source acceptance means the slices are independently reviewed, tests pass, and
the committed behavior is honest. It does **not** establish production readiness:
live Gradion create/edit semantics remain unverified, no live vault, real terminal,
or Windows run has been done, and no production submission is authorized.

## Workflow modes (owner definitions)

- **LOCAL** — collect evidence from both local **and** configured remote sources
  (and do Gradion **read-only** calls if a `gradion` endpoint is configured), then
  review and export. **Stops before submission.**
- **REMOTE** (connected) — the same evidence and review, then the full lifecycle:
  exact `preview` → explicit `approve --plan` → `submit`. **Currently
  production-blocked.**

`--offline` is **unchanged and narrower**: it skips only the Gradion remote seam
(its reads and writes); configured source fetches — calendar ICS, git (including
remote GitHub), and the journal note — still run. It is **not** a synonym for the
LOCAL workflow. The owner withdrew an earlier "strict offline" correction; there is
**no outstanding offline product gap or task**.

Existing CLI entry points only — no invented local/remote flags. Use
`run`/`review`/`classify`/`preview`/`approve`/`submit`/`export`. `run` mode
precedence: `--decisions`/`--non-interactive` → scripted; `--plain` → safe
line-based (pipe/CI, EOF aborts); `--interactive` → TUI (requires a real terminal
on stdin and stdout and a non-CI environment, else refuses); default → scripted.
Cancelling a guided run applies no unfinished confirmation/approval/submission
before the final deliberate confirmation; collection and optional editorial
synthesis may already have persisted their own durable state.

## Privacy, journal, and selection boundaries

- The **journal collector is read-only**; the separate **managed export** writes
  only the `<!-- timesheet:begin v1 -->` … `<!-- timesheet:end -->` section.
  Managed-export content is excluded from collection so it never feeds back.
- Journal evidence is **untimed** (unknown duration, zero start/end), recorded-only,
  with optional explicit `<!-- evidence_id: X -->`; edited implicit items detach as
  retained history (no deletions); duplicate explicit/implicit ids fail visibly;
  source failures are visibly incomplete and require `--ack`.
- Classification is an **explicit** choice; the config map and recent selections
  are **suggestions only**, never silently mapped. The activity type is derived
  server-side and never sent. A scripted raw classification id is validated
  against a fresh catalog **mandatorily at submit**; changing an existing
  classification uses the `classify select` follow-up.
- Private journal text is **never** forwarded to the editorial service, including
  a manually linked journal description.

## Retest and practical config

- Hermetic guided harness (no terminal, credential, remote, or live service):
  `go test ./cmd/timesheet -run '^TestGuidedOfflineHarness$' -count=1 -v`
- Skill/records tests: `go test ./tests/... -count=1`
- Config templates (sanitized, placeholders to replace):
  `cmd/timesheet/timesheet.example.json`, `timesheet.local-git.example.json`,
  `timesheet.personal.example.json`, `timesheet.journal.example.json`. Full
  key semantics and the local/remote mode matrix are in
  [the usage guide](../agents/product-usage.md).

## Production blockers and prior read-only facts

**Current production blocker:** live Gradion create/edit semantics are unverified,
and a residual remote write race remains (no server idempotency key or conditional
update). A real submission additionally requires an owner-approved exact daily
plan and separately granted live access/credentials/services. Do not issue live
requests.

Prior read-only remote facts (verified in an earlier session, planner-handoff
baseline `60a7398`; do not reopen):
- Gradion exposes 71 tools; there is **no standalone activity-type tool** — the
  activity type is **derived** from the classification.
- `get_classifications` is capped at 20 rows with no pagination; targeted code
  searches resolve beyond the first page, while an out-of-page **UUID** remains a
  residual.
- B5 (structured week reader) is fixed and live-read confirmed; B6 (timezone
  `Asia/Ho_Chi_Minh`, max 240 minutes/entry) is owner/admin-confirmed.
- Keychain **service references** (names) are documented; no credential values
  were retrieved or asserted as verified in this session.

Other unscheduled carries (no invented fixes): structural `submit.Execute`
boundary probe, process-group cancellation coverage, calendar resolved-`source`
redaction boundary, and the legacy benchmark `600`/double-count reference. The
original planner handoff and [state.md](state.md) remain authoritative.

Carried limitations: narrow journal boilerplate/metrics matching may produce
possible noise; duplicate implicit bullets in one heading fail incomplete; a
heading edit detaches an implicit item; no all-day coverage is claimed (only
recorded work is captured). Catalog out-of-page UUID resolution is unresolved.

## Vault sync

Project note, the 2026-10-11 daily note, and vault ADR-007 are synced for A+B
(actual date 2026-10-11). The existing unresolved person link is preserved as a
limitation. This documentation-only task adds no vault milestones, hours, or
deadlines.

## Next agent

1. Reconcile Git first: `git status`, `git rev-parse HEAD`; the guide/handoff are
   uncommitted until the parent's final local commit.
2. Use [the usage guide](../agents/product-usage.md) to operate the CLI/skill if
   authorized; it is documentation, not permission.
3. Request explicit scope for any new code, live pilot, or remote write. This
   handoff implies no implementation and no submission.
