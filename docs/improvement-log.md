# Improvement log

The assignment's log → review what broke → improve → log again record
(§3.5). Date-keyed; newest first. The Friday review reads this file.

## 2026-10-25 — entry-store consolidation onto the Mac

The pilot runs recorded the missing working days in a development store
on the Linux host. The owner's Mac is the single canonical store from
here; the pilot entry files for 2026-10-12, 10-19, 10-21, 10-22, 10-23
and 10-24 (each collected from real GitHub/calendar evidence on the day
they were drafted) were transferred into the Mac's
`~/.timesheet/entries/`. The Mac already held its own 2026-10-20 and
the agent-recorded 2026-10-25. After this, every working day since
2026-10-06 has an entry in one consistent store. New days are logged
only on the Mac.

## 2026-10-25 — first full agent-run of the skill (Pi) ✅

**Ran:** the owner asked a normal Pi session (repo cwd) to _"log today's
work"_. The agent read `SKILL.md`/`workflow.md`, ran `collect` → `draft`,
authored the block descriptions (judgment step), `record`ed the day
(`entries/2026-10-25.json`, 1 entry), verified with `status`, and ran
`tokens.py` on its own session — the full six-step loop, zero manual help.

**Waste found (owner observation):** before the real commands, the agent
guessed a wrong config path, then ran `bin/timesheet collect --help` and
`bin/timesheet --help` and read `README.md` to verify flags — pure
exploration overhead. Fix: `workflow.md` now states the commands are
final and complete (no `--help`, no flag discovery, no other docs
needed) and names the standard config/env paths up front; `SKILL.md`
says the same. Expected effect: the next agent run's input should drop
noticeably — label the next data point with this change.

**Measured (first agent-run data point):** session input 14,957 / output
950 / cache 97,280 (total 113,187) — recorded in `benchmarks/tokens.csv`
by the agent itself. Honest read against the ≤ 1,000 tokens/day target:
the _output_ side fits, but the input includes the agent's system prompt,
skill files and exploration — the judgment step's actual input (the draft
JSON) is a fraction of that. Improvement candidate: a focused invocation
that passes only the draft into the judgment step.

**Lesson:** `tokens.py` printing "no new skill sessions to record" after
the run confused the owner — it means the row was already captured
(idempotency), not that nothing was found. Improvement: print the CSV
tail so the recorded row is visible.

## 2026-10-25 — first owner-run loop on macOS; two defects found and fixed

**Owner decision:** no Claude account is available. **Pi and manual
terminal runs are the demonstrated runners**; the skill folder stays
runner-agnostic (Claude pickup link kept, unused). Token capture follows
suit: `tokens.py` now reads Pi's usage fields (`input`/`output`/
`cacheRead`/`cacheWrite` under `~/.pi/agent/sessions`) alongside Claude's.
AGENTS.md points agents working in this repo at `workflow.md`, so a
normal Pi session executes the daily log by following repo instructions.
Manual terminal runs consume zero model tokens — an honest §3.6 data
point, not a gap.

**Ran:** the owner executed the full loop manually on macOS (collect →
draft → judgment in an editor → record → status) for 2026-10-20 and
2026-10-27 with the real config and env file. The whole path works
natively; entries landed in `~/.timesheet/entries/`.

**What broke (found by the owner, both fixed same day):**

1. **`record` ignored a mismatched `--date`.** A draft for 2026-10-20
   recorded successfully under `--date 2026-10-24`; the file's date won
   silently. Fix `28db13b`: `record` now rejects the mismatch explicitly.
2. **`status --date` panicked on a day with no entries file** (nil
   dereference in the missing-file path). Fix `e9aa687`: reports
   `entries: 0`; cmd-level regression tests added for both defects.

Also recorded earlier: the owner's first run failed with `write_failed:
state directory cannot be created` because the same `config.json` was
copied across machines — `state_path` is machine-specific (absolute home
path). Not a product defect; worth a config-error hint improvement.

**Improvement queue:** convert provider timestamps to the configured
timezone in `draft` (evidence blocks currently render UTC wall clock);
add `GITHUB_TOKEN` to the Mac env file; surface a clearer diagnostic when
`state_path` cannot be created.

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

## 2026-10-25 — descriptions-only judgment step; run-4 data point ✅

**Changed (PR #153, issue #152):** the AI no longer rewrites the full reviewed
JSON — it writes only per-block descriptions (+ one notes string) and `record`
composes the rest (prs_inline appended by the binary, sources re-attached from
state). Defensive `go build` runs eliminated by a version-check amendment.
Review also fixed a real edge: a prefix-sharing PR id (#123 vs #12) could
silently suppress the prs_inline append.

**Measured (run 4, Pi, 2026-10-24 rerun on the merged binary):**
in 11,533 / out 589 / cache 47,360 — input −11% and output −16% vs run 2
(12,930/701) on a comparable skill loop, and the judgment artifact itself
dropped from a ~2.7 KB reviewed-file rewrite (~700 tokens) to a ~340-byte
descriptions file (~100 tokens). Cache totals are not comparable across runs
and are reported separately. Zero failure cycles: one-pass record.

Honest caveat: run 4 re-recorded an already-recorded day (idempotent per
block), so it is a loop-equivalence data point, not a new-content one.
