# Assignment gap-closure plan

Created: 2026-10-24. Kickoff executed 2026-10-24 (G0+G1, branch
`feat/timesheet-skill-surface`). Target: Track Leader final check
**Fri 2026-10-30**.

Status: **executing under owner direction** recorded 2026-10-24. A plan is
not permission: publication, merge, live access and production writes each
keep their separate owner authorization.

## Owner direction this plan executes

The owner ruled on 2026-10-24 that the current delivery shape — the pinned Pi
consumer in a dedicated environment borrowing a UI to run the workflow —
**does not satisfy Assignment 1**. The assignment's deliverable of record
(§3.5, DoD #1/#2/#9) is a **Skill folder (`SKILL.md` + `workflow.md`) that an
agent can read and run once a day**, with every step tagged [Script]/[AI], a
stated daily token budget, and tokens/day evidence (§3.6). During kickoff the
owner added: the skill is **runner-agnostic** — it must aim to run on Pi as
well as Claude Code, not be a Claude-only approach.

This plan adds that surface on top of the accepted rewrite core. It does not
reintroduce the retired legacy skill (that skill wrapped the retired legacy
CLI); it authors a new skill folder whose [Script] steps call the current
binary. The release line, the Pi consumer and issue #137 remain unchanged;
the Pi TUI stays the interactive human workflow, and a Pi agent with shell
access is an equal executor of the skill.

## Current base (verified 2026-10-24)

- Public `main` = `1945cb9` (Timesheet 0.1.1, PR #142). Attribution rules in
  force: GitHub account `huyle-gradion`, repo-local identity
  `Huy Le <huy.leminh@gradion.com>`, preserve history/tags, PR-only
  integration, no direct `main` commits.
- A complete user-manual + `0.1.2` documentation patch sits uncommitted on
  branch `docs/user-manual-v0.1.2` awaiting its own authorization. G1 is
  based directly on `origin/main`; any overlap (version literals, README,
  changelog) resolves at publication time.
- Terminology after #142: `internal/timesheet` (was `internal/rewrite`),
  `docs/agents/timesheet-interface.md`, product name Timesheet.
- Claude Code CLI 2.1.304 present on the Linux host; Mac authentication
  remains an owner-side check.

## Gap inventory (assignment DoD → state at kickoff)

| # | Assignment requirement | State at kickoff | Closed by |
| --- | --- | --- | --- |
| 1 | Skill folder `SKILL.md` + `workflow.md`, runnable daily | Deleted in cutover `f2c8088`; Pi TUI instead | G1 |
| 2 | Every step tagged [Script]/[AI]; real scripts do [Script] work | Binary exposed only `version`/`serve` | G1 |
| 3 | Pulls commits, PRs, calendar automatically | Implemented + hermetically verified; live creds never used | G1 (surface) / G4 (live) |
| 4 | Entry shape: per time block, grouped topics, inline `PRs:`, source data | Core produces it; not exposed to an agent | G1 |
| 5 | Git discipline §3.4 | Followed | — |
| 6 | Entry every working day since Tue 2026-10-06 | Rows end 10-16; 10-12 and 10-19..10-23 missing | G2 + live days |
| 7 | Incremental git history | Strong | — |
| 8 | Friday-review explanation end to end | Distributed across records | G5 |
| 9 | `workflow.md` states target token budget | No `workflow.md` | G1 |
| 10 | Tokens/day captured from session transcripts + Week-4 stats | Real numbers only 10-06..10-08; tooling deleted | G1/G3/G5 |

## G1 target shape (implemented)

- New headless subcommands on `bin/timesheet`, wrapping the accepted
  `internal/timesheet` core through the same in-process session dispatcher
  `serve` uses (no second protocol):
  - `collect --config [--env-file] --date` — one bounded collection attempt.
  - `draft --config [--env-file] --date` — fixed partitioning into time
    blocks (meetings; confirmed intervals outside meetings; one evidence
    block for unallocated topics; one whole-day block when nothing anchors
    the day), printing the compact draft JSON that is the skill's entire
    model input.
  - `record --config --date --file reviewed.json` — validates the reviewed
    draft (non-empty descriptions, kept `PRs:` lists, unique block ids) and
    merges it idempotently per block into `<state dir>/entries/<date>.json`
    with full source references. Opens no store; never blocks on `serve`.
  - `status --config [--date]` — store status plus the day's entries.
- Skill folder `.agents/skills/timesheet/{SKILL.md,workflow.md}` with the
  `.claude/skills/timesheet` pickup link; `workflow.md` tags all six steps,
  states the ≤ 1,000 tokens/day budget for the single [AI] step, and is
  runner-agnostic (Claude Code or a Pi agent with shell access).
- `scripts/tokens.py` — idempotent token capture from Claude Code
  (`~/.claude/projects`) and optionally Pi (`--pi-dir`, e.g.
  `~/.timesheet/pi-profile`) into `benchmarks/tokens.csv` with session ids.
  The historical `benchmarks/metrics.csv` stays untouched; the Week-4 chart
  merges both.
- Development stamp `0.2.0-dev.1` (protocol 1, state schema 3 unchanged);
  release stamping and publication remain owner-gated.

## Remaining slices

- **G2 (weekend)** — backfill Mon 10-12 and Mon 10-19…Fri 10-23 from real
  still-queryable evidence via the same subcommands, marked retroactive,
  each with a dated line in a consolidated `docs/improvement-log.md`.
- **G3 (Mon 10-26)** — first live daily run through the skill with real
  credentials on the owner's machine; verify token capture end to end.
- **G4 (Tue 10-27)** — one real Gradion submission through the existing
  human-approved plan path; requires that morning's separate owner
  production-write authorization.
- **G5 (Wed–Thu)** — tokens/day chart over 10-06…10-30 labelling each drop
  (legacy skill → 1,000-token editorial ceiling → near-zero model tokens),
  Week-4 review document, dry run.
- **G6 (Fri 10-30)** — final check: every working day logged or explained,
  stats presented, skill demonstrably runnable.

## Verification recorded for G1

- `gofmt` clean, `go vet ./...` clean, full `go test ./...` green including
  new `internal/timesheet/headless_test.go` (draft partitioning, PR
  extraction, validation, idempotent merge, schema round-trip).
- End-to-end local demonstration on Linux with synthetic config: interval
  authored through the public `serve` protocol → `draft` → reviewed JSON →
  `record` (twice, idempotent) → `status --date` showing the entry.
- `tokens.py` demonstrated against synthetic Claude Code and Pi transcripts
  with correct sums, unrelated-session exclusion and idempotent re-runs.

## Explicit non-goals

- No Undici/Pi dependency remediation (stays in #137).
- No further Pi TUI macOS acceptance work for the assignment.
- No revival of the legacy CLI, the retired skill content, the vault
  integration, or the retired metrics engine.
- No scheduling automation (assignment §3.3: human-triggered is fine).
- No approval or submission capability in the skill: remote writes stay on
  the interactive, human-approved workflow.

## Risks

| Risk | Mitigation |
| --- | --- |
| Claude Code unauthenticated on the Mac | Owner check; skill runs equally under a Pi agent; evidence from the Linux host |
| 0.1.2 manual patch and G1 both touch version literals | Resolve at publication; both stay owner-gated |
| Production-write authority not granted for G4 | Submission stays unverified but explained; entry still recorded locally |
| Backfill evidence incomplete for some day | Leave the day as an explained gap in the improvement log (DoD-compliant) |
