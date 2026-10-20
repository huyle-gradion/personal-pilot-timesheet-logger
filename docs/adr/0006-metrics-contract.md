---
status: accepted
date: 2026-10-11
deciders:
  - Huy Le Minh
related:
  - 0005
---

# Metrics contract

## Context

Requirement 3.6 asks the skill to prove the 3.5 optimization with a tokens/day
number, not an opinion. The repository already had two disconnected pieces: a
legacy `internal/benchmark` transcript extractor (with a known double-count bug,
left unchanged) and the S7 editorial usage report. Neither gave a per-day,
per-provider, honest accounting that distinguishes a measured zero from an
unavailable value.

## Decision

Add one additive package, `internal/metrics`, as the token/duration accounting
surface for the review-first workflow. The legacy `internal/benchmark` extractor
(its own CSV chart path) and the S7 editorial usage report remain for their own
purposes; this package is the accounting surface for the review-first workflow,
not a replacement for those unrelated code paths.

- **One measurement type.** Every counter is a `workflow.Measurement`; there is no
  second unknown-vs-zero type. An unavailable value is UNKNOWN, never zero.
- **Provider adapters, fail closed.** `pi` is OBSERVED (usage read first-hand from
  a JSONL transcript); `claude` is DECLARED (schema known, not verified on this
  host) and the report prints a `[declared schema]` marker. `--provider auto`
  detects on the first non-empty line and returns `ErrUnknownProvider` on no match;
  it never guesses. *(Amended 2026-10-20 by #69.)* The day is an OPERATOR input: an
  import without an explicit `--date` fails closed with `ErrDateRequired`, and no
  day is derived from the transcript's timestamps or from the clock. A forced
  provider whose parse yields no call is REFUSED (`ErrProviderMismatch` naming the
  requested and the detected provider, or `ErrUnreadableTranscript` when no
  registered adapter recognizes the transcript) rather than written as an empty
  record. See [Amendments](#amendments).
- **Safety.** An adapter extracts only counters, ids, timestamps, the `timesheet`
  subcommand token, and the provider-native NUMERIC cost. Message text, tool
  arguments, and tool results are never decoded into a persisted field.
- **Provider-native field semantics.** The following table records what each
  provider field means and how it maps. Numbers only; no content, paths, or URLs.

  | provider | native field | mapping | semantics |
  | --- | --- | --- | --- |
  | pi | `input` | `Usage.Input` | prompt tokens |
  | pi | `output` | `Usage.Output` | completion tokens |
  | pi | `cacheRead` | `Usage.CacheRead` | cache-read tokens |
  | pi | `cacheWrite` | `Usage.CacheWrite` | cache-write tokens |
  | pi | `reasoning` | `Usage.Reasoning` | reasoning tokens; EXCLUDED from `ConsumedTokens` |
  | pi | `totalTokens` | `CallRecord.ContextLength` | context length; VALIDATION ONLY, NEVER summed |
  | pi | `cost.*` | `CallRecord.Cost` | provider-native numeric cost; NEVER aggregated |
  | claude | `input_tokens` | `Usage.Input` | prompt tokens |
  | claude | `output_tokens` | `Usage.Output` | completion tokens |
  | claude | `cache_read_input_tokens` | `Usage.CacheRead` | cache-read tokens |
  | claude | `cache_creation_input_tokens` | `Usage.CacheWrite` | cache-creation tokens |
  | claude | (none) | `Usage.Reasoning` | the declared schema has no reasoning counter, so it is UNKNOWN |
- **Attribution is a labelled heuristic.** A call is attributed to a step by the
  `timesheet` subcommand token, and ONLY when the command's FIRST token is the
  `timesheet`/`ts` binary (or the `go run ./cmd/timesheet` form); `grep -r timesheet
  submit` is not attributed. An unrecognized subcommand stays `StepUnknown` +
  `CategoryUnassigned` and is never folded into a step. Calls are bucketed by
  category: `Orchestration` holds only `CategoryOrchestration` calls and every other
  call goes to a separate `Unassigned` lane.
- **The attribution clause above is SUPERSEDED, 2026-10-14.** Its first-token
  requirement is no longer the rule and the paragraph is retained only as history.
  Measured on the owner's real transcripts, ZERO of 44 commands mentioning
  `timesheet`/`ts` in one session were recognised under it, because real invocations
  are compound shell commands (`cd <repo> && "$T/ts" review --config ...`,
  `for ...; do "$TS" preview ...; done`), so every `CallRecord.Step` was
  `StepUnknown` and the per-step lane carried no information. The rule now IN EFFECT:
  a call is attributed when a token whose basename is `timesheet` or `ts` appears in
  an INVOCATION POSITION (the first token, after `go run`, or immediately after a
  shell separator) and is IMMEDIATELY followed by a recognised subcommand token. Both
  guards of the superseded paragraph still hold: `grep -r timesheet submit` and
  `echo timesheet review` are still NOT attributed (a mention in argument position is
  not an invocation), a path that merely contains `timesheet`
  (`cat .../timesheet/workflow.md`) is still not attributed, `ls cmd/timesheet` is
  still not attributed because its binary token is not in an invocation position with
  a following subcommand, a bare `timesheet`/`ts` with no following subcommand stays
  `StepUnknown` + `CategoryUnassigned` and is never folded into a step, and category
  bucketing is unchanged. The binary basename is matched case-insensitively while the
  subcommand token is matched case-sensitively, which is the implemented behaviour and
  the safe direction. THE SAFETY RULE ABOVE IS NOT AFFECTED BY THIS SUPERSESSION, and the two
  clauses are deliberately separate: Safety governs WHAT MAY BE PERSISTED (only
  counters, ids, timestamps, the subcommand TOKEN, and the provider-native numeric
  cost; never message text, tool arguments or tool results), whereas attribution
  governs HOW A STEP IS DERIVED from a command that is already being inspected. The
  widened rule still RETURNS ONLY the subcommand token, so it persists no more than
  before, and the change that WAS refused on Safety grounds is a different one that
  remains refused: decoding a `--date` VALUE out of a tool argument to associate a
  session with a day.
- **Editorial usage is DAY-granular.** The [AI] editorial child call is NOT a
  harness turn, so it never appears as a transcript call: editorial usage is copied
  from the workflow record and reported separately from orchestration. There is no
  per-call editorial measurement. `EditorialInputToStep` (the 3.5 metric) is the
  editorial input only; `Budget.Spent` is the editorial total including cache and
  retries.
- **Honest durations.** Live monotonic timing (`time.Since`) is used for the
  metrics command's own work and for `timesheet metrics run`. Transcript durations
  are estimated and labelled; `HarnessStart` is always UNKNOWN, never zero. The
  aggregate carries known-only duration sums (UNKNOWN if any contributing run is
  unknown).
- **Annotations and comparison.** A run may carry `commit`, `workflow-version`,
  `config-mode`, and `optimization-change` annotations; the aggregate exposes their
  sorted union, and `timesheet metrics compare --base D --target D` reports a
  delta/percentage ONLY when the base value is Known and non-zero, else `n/a`
  (table) / `null` (json). A percentage is never fabricated against an unknown or
  zero baseline.
- **Idempotent and pure.** A reimport of an unchanged transcript is a no-op (content
  hash match); the `DailyAggregate` is a pure function of its inputs with no stored
  generation timestamp, so re-aggregation is byte-identical.
- **Additive model fields.** The only cross-package edit is three additive
  `EditorialUsage` component fields (`editorial_input`/`editorial_output`/
  `editorial_cache`) populated in `internal/run/synthesize.go`; `SchemaVersion`
  stays 1 and no workflow/run behavior otherwise changes.

## Alternatives rejected

- Reuse `internal/benchmark`: rejected because its extractor double-counts and
  couples accounting to the legacy CSV chart path; the legacy code is left as-is.
  *(Superseded 2026-10-20 by #69: the legacy parser is RETIRED and
  `benchmark.DefaultTokenCeiling=600` is DELETED. The rejection itself stands; what
  changed is that the legacy code is no longer left as-is. See
  [Amendments](#amendments).)*
- Define a metrics-local unknown/zero type: rejected to keep one honest
  representation across the workflow and metrics layers.
- Derive editorial components from the persisted total: rejected because the total
  cannot be decomposed after the fact; the components are populated at accumulation
  time.

## Consequences

The CLI (`timesheet metrics import|aggregate|report|compare|run`) and the skill's
optional `[Script]` step call the same `internal/metrics` implementation. A metrics
failure prints a warning and never changes a workflow exit code and never triggers
submission or replay.

`RunBlocked` and `RunFailed` are reachable in production ONLY through `timesheet
metrics run` (exit code 3 -> blocked, non-zero/other -> failed, 0 -> completed). A
transcript import cannot observe an exit code, so those states are not derivable
from a transcript.

The soft daily editorial target is 1,000 tokens/day (owner override of the
specification's 600). *(Amended 2026-10-20 by #69.)* **1,000 is the only surviving
ceiling.** It is defined ONCE, in `internal/workflow` (`workflow.EditorialTokenCeiling`),
and `editorial.SoftDailyTokenTarget` and `metrics.BudgetTargetTokens` are named
aliases of it: no mirror, no second number. It governs the EDITORIAL-CHILD token
lane only (the lane the benchmark CSV's token columns carry); harness-session tokens
have no ceiling and no number is invented for them. The legacy
`benchmark.DefaultTokenCeiling=600` is DELETED, not re-pointed: a formatter package
states no budget. See [Amendments](#amendments).

## Amendments

### 2026-10-20 — recorded refusals superseded, and the explicit-date gate (#69)

Two recorded refusals are superseded by the owner-approved #69 design
(`docs/benchmark-rewiring.md`, decisions 1, 2, 5, 6 and 7). The rest of this ADR
stands as written.

- **The legacy parser is RETIRED; the 1,000 ceiling has one home.** `internal/benchmark`
  no longer parses transcripts: `rawTokenRecord`, `extractTokens`, `ParseTranscript`,
  `ParseTranscriptFile`, `ErrEmptyTranscript`, `TokenUsage`, the `cmd/benchmark`
  `-transcript` flag, and `editorial.OrchestrationUsageFromBenchmark` (which had no
  production caller) are gone. The package is stdlib-only again. The benchmark CSV
  keeps its file, its header order, and its reader; its token columns are now COPIES
  of the day's `EditorialInput`/`EditorialOutput`/`EditorialCache` from the workflow
  record, never a transcript reading. This is why the previous "the legacy code is
  left as-is" refusal no longer applies.
- **`benchmark.DefaultTokenCeiling = 600` is DELETED, not re-pointed.** 1,000 is the
  only surviving number, defined once as `workflow.EditorialTokenCeiling` with
  `editorial.SoftDailyTokenTarget` and `metrics.BudgetTargetTokens` as named aliases.
  It governs the editorial-child token lane only; harness-session tokens remain
  ceiling-free. `cmd/benchmark -ceiling` now reaches BOTH renderers (chart and the
  report's compliance line); its default is the shared constant.
- **An unknown cell is EMPTY, never 0.** The CSV token and entry cells are nullable:
  a day with no record (or no editor) writes an empty cell and prints `unknown`; a
  record-read error leaves the cells empty and warns on stderr. The chart omits the
  bar and prints `unknown` rather than a `0 <= ceiling` tick. Rows are upserted one
  per date (last value wins) by temp-file + atomic rename, and reads/rewrites are no
  longer truncated at 1,000 rows.
- **The date gate.** A run's day is an operator input. `metrics.Import` requires an
  explicit `--date YYYY-MM-DD` (validated, `ErrDateRequired` when absent) and no
  longer derives a date from the transcript's first timestamp or from the clock;
  the derivation in the shared run assembly path is removed. With `--date` set, both
  the run and the session `LoggedDate` take that value with `Association = import_flag`.
  The gate is **PROSPECTIVE**: sessions stored before it keep their derived or absent
  associations and can still flow into a day's lane until they age out or are
  re-imported. `metrics run` and `metrics aggregate` keep their `today` default and
  are NOT gated: `run` writes only a run record, and `aggregate` reads a day rather
  than attributing one.
- **Refusal, not an empty record.** A parsed transcript that yields NO call is
  refused: with `ErrProviderMismatch` (naming the requested and the detected
  provider) when another registered adapter's `Detect` matches within the first 32
  non-empty lines, and with `ErrUnreadableTranscript` when nothing matches. A parse
  that yields a call never refuses, so every previously readable import is unchanged.
  `--provider auto` on an unrecognized transcript keeps `ErrUnknownProvider`. When
  #73 registers the antigravity adapter, its arm of this guard is upgraded from
  `ErrUnreadableTranscript` to `ErrProviderMismatch` naming antigravity.
