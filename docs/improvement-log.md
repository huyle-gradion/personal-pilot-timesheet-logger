# Improvement log

The assignment's log → review what broke → improve → log again record
(§3.5). Date-keyed; newest first. The Friday review reads this file.

## 2026-10-24 — skill surface kickoff and missing-day backfill

**Ran:** first live loop of the new headless skill (`collect` → `draft` →
judgment → `record` → `status`) for 2026-10-12, 10-19, 10-20, 10-21,
10-22, 10-23 (retrospective backfill) and 2026-10-24 (same day). Real
GitHub and calendar evidence, real ICS feed; 10 entries across 7 days,
each with source references kept. Entry files: `<state dir>/entries/`.

**What broke / what to improve:**

1. **GitHub evidence times render as UTC wall clock.** Evidence blocks
   span e.g. 02:15–10:15 because GitHub API timestamps are UTC ("Z") and
   the draft takes the event's own wall clock. Improvement: convert
   provider timestamps to the configured timezone in `draft`.
2. **`.env.local` has no `GITHUB_TOKEN`.** GitHub collection reported
   `missing_credentials` until a token was supplied via the process
   environment (the git credential helper; never written to any file or
   output). Improvement: add the token name to the owner's `.env.local`.
3. **Topic cap hits on heavy days.** 10-20/10-21 exceeded the 50-item
   topic cap (`source_incomplete:github`, 22 attributable items dropped on
   10-21). Honest, but the cap or the retention rule may need tuning.
4. **`gh` CLI hangs in this environment** (keyring); PR #143 was opened
   through the REST API with the same account's credential. Environment
   issue, not a product defect.

**Skill change made:** none yet — the UTC-timezone conversion (item 1) is
the first candidate change for the next `workflow.md`/binary iteration.

**Historical arc (for the Week-4 stats):** legacy skill (Claude re-derived
entries; ~300 tokens/day recorded 10-06…10-08) → redesign with the
1,000-token editorial ceiling and chart (ADR-0006) → rewrite: deterministic
binary, zero model tokens for local logging → skill surface: one judgment
step over a ~300–800-token draft. `benchmarks/metrics.csv` holds the
10-06…10-16 history; `benchmarks/tokens.csv` records runs from here on
(Claude Code and Pi runtimes).

## Pre-2026-10-24 (consolidated pointer)

Earlier daily notes live in `docs/lessons-learned.md`, the ADR amendments
(0006 §Amendments) and `docs/orchestration/` records (R1–R6 reviews and
fixes). The rewrite week 10-19…10-23 consumed the tool's own daily use;
those days are backfilled retrospectively with real evidence, as recorded
above.
