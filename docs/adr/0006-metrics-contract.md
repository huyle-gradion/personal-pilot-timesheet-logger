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
  it never guesses. A run whose date is neither derivable from the transcript nor
  supplied with `--date` FAILS CLOSED rather than being written with an empty date.
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
specification's 600). The legacy `benchmark.DefaultTokenCeiling=600` is stale and is
not changed.
