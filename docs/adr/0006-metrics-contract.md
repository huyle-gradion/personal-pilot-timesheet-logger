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
not a replacement for those unrelated code paths. *(Amended 2026-10-21 by #126:
the extractor's CSV chart path no longer charts. `cmd/benchmark -chart` reads the
METRICS STORE's day aggregates; the CSV stays the pipeline store for `-record` and
`-report`, whose behavior is unchanged. See [Amendments](#amendments).)*

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
states no budget. *(Amended 2026-10-21 by #126: the ceiling's home and single value
are unchanged, and the number still governs the editorial-child lane only — but the
CHART no longer charts that lane. It now compares the day's `session_total_tokens`
as a REFERENCE line against the same 1,000, labelled as a harness-session reference;
no harness budget is claimed and no number is invented for harness tokens. See
[Amendments](#amendments).)*

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

### 2026-10-20 — the antigravity adapter: observed shape, honest counter unknowns (#73)

The registration anticipated by the previous subsection is now in place. This
amendment records the adapter's shape and the two owner decisions taken on it; the
rest of this ADR stands as written.

- **Registration and provenance.** `antigravity` is a registered provider in
  `internal/metrics`, reachable through `--provider antigravity` AND through
  `--provider auto`. Adding it is one entry in `adapters()` and one case in
  `SelectAdapter`; there is no dispatch table. Its provenance is **OBSERVED**: the
  header shape and counters were verified first-hand on this host, unlike the
  `claude` declared-only schema, so the report does **not** print the
  `[declared schema]` marker. `Detect` is the five-scalar discriminator and nothing
  more: `step_index` numeric, `created_at` non-empty, `source` in
  {`MODEL`, `USER_EXPLICIT`}, `type` in {`GENERIC`, `PLANNER_RESPONSE`, `USER_INPUT`},
  and `sessionId` absent. pi needs a `type:"session"` line and claude needs
  `sessionId` (or `summary`+`leafUuid`), so the three adapters are mutually exclusive
  by construction and auto-detection order is immaterial. The antigravity arm of the
  refusal guard therefore now takes `ErrProviderMismatch` naming antigravity when
  another provider is forced.
- **Decoded fields.** Only `type`, `source`, `step_index`, `created_at`, the three
  counters, and `truncated_fields` as a trust marker. `content`, `thinking`,
  tool-call names/arguments/results, `status` and `error` are never decoded.
  **`thinking` is thinking TEXT, never a counter**: no reasoning count is read from
  it, and `Usage.Reasoning` stays UNKNOWN.
- **Closing turn and state.** A transcript that ends on a MODEL-source turn is
  complete; one that ends on USER_INPUT reads cancelled because the record stopped
  after the user's turn. The closing turn is derived only from the already-decoded
  `source`, so the state is never a claim stronger than the transcript supports.
- **Counter semantics.** `input_tokens` → `Usage.Input`, `output_tokens` →
  `Usage.Output`, `cache_read_tokens` → `Usage.CacheRead`. There is **no cache-write
  counter**, so `Usage.CacheWrite` is UNKNOWN, and there is no model identifier, so
  price and cost are UNKNOWN. The existing contracts do the rest with no new type:
  `ConsumedTokens()` is UNKNOWN because one component is unknown;
  `CacheHitPercent()` is nil because the denominator needs a cache-write count. The
  three per-component counts remain individually meaningful and their table columns
  still read. A total is **never** emitted from `input+output+cache_read`: an
  unavailable value is UNKNOWN, never a lower bound presented as a total.
- **`truncated_fields` is a trust marker.** The line is still COUNTED because its
  counters are intact, and any counter the marker names is downgraded to UNKNOWN
  rather than trusted. The recorded shape names only TEXT fields, so this is a guard
  rather than an expected path.
- **Recorded owner decisions.** (1) **REFUSE, not label**, for a forced mismatched
  provider: an unreadable import is refused through the existing arms above, never
  written as a phantom empty record. (2) **NO MODEL CHANGE** for the missing
  cache-write counter: `total_tokens` and `cache_hit_pct` stay `unknown` for an
  antigravity import, recorded as a limitation rather than patched by deriving a
  total.
- **Limited value on this harness, stated plainly.** Because the total and the
  cache-hit rate stay unknown, the instrument is of LIMITED VALUE on antigravity.
  The owner accepts this: benchmarking is done on `pi`, where the adapter is
  complete.
- **Reading guidance.** The Antigravity transcript lives at
  `~/.gemini/antigravity/brain/<conversation-id>/.system_generated/logs/transcript.jsonl`,
  with the fuller `transcript_full.jsonl` beside it. The skill documents that path
  and the equivalent pi and Claude Code locations; `metrics import --transcript
  <path> --date "$DATE"` reads any of them through `--provider auto` or an explicit
  provider name.

### 2026-10-21 — harness-agnostic token record: chart source, ceiling reference, de-pi defaults (#126)

The adaptation target is a harness-agnostic skill, so this amendment removes the
last harness-specific defaults and makes the session token record honest on its own
terms. It records one reversal and four additions; everything not named here stands
as written.

- **The chart's source moves to the metrics store.** `cmd/benchmark -chart` no
  longer charts the CSV's editorial-child token columns. It reads the metrics
  store's day aggregates (`metrics.ListAggregateDates` / `metrics.ReadAggregateFile`
  — non-creating reads, so charting never mkdirs a store) and maps each day's
  `session_total_tokens` onto the unchanged `benchmark.RenderASCIIChart`. The output
  names its source on one line (`source: … day aggregates (harness session tokens,
  reasoning excluded; day totals may include synthesized lower bounds; …)`) so the
  number is never mistaken for the retired lane. An empty store
  prints one line, not an empty axis; a corrupt aggregate exits 1 naming the date
  rather than charting around it. `-record` and `-report` are untouched: the CSV
  remains the pipeline store for those, and `internal/benchmark` gains no import and
  no provider name (the mapping lives in `cmd/benchmark`).
- **`metrics report` gains `SESS-TOKENS`, the DAY's summed session lane.** The
  column is the day's sum over its sessions' totals — the same figure the chart
  draws — printed through the existing unknown rule: `unknown` when any session
  total is not established (the sum is unknown-propagating, never a partial sum),
  and the honest `0` only for a day with zero sessions. `SESS-TOKENS` is the SESSION
  lane, never the editorial or the step-attributed lane.
  **The DAY sum can contain a lower bound**, because a session total stamped by
  `metrics import --declare-total` is `total_source=synthesized`. The per-session
  label is NOT rendered as a column: it is carried per session on the import summary
  line and in `metrics sessions --format json` (field `total_source`), which is the
  authoritative place to look up one session. The day-level figure is labelled where
  it is RENDERED: the report table prints a `[synthesized] N session total(s) …`
  footnote after the batch when the day includes such sessions, and the chart's
  source line names the same caveat. No JSON key is added, so the aggregate schema is
  unchanged.
- **The ceiling becomes a REFERENCE for the chart, reversing the previous
  clause.** This supersedes the sentence above that harness-session tokens have "no
  ceiling" and `docs/timesheet-redesign.md:147` ("no ceiling for harness tokens"):
  the chart now draws the day's session total against the same single 1,000. The
  label is PRINTED, not implied — after the chart the command emits one line:

      reference: 1,000/day is the editorial-child soft target; [PASS] compares harness
      session tokens against it — it is not a harness budget. (use -ceiling to change)

  The verdict token is `[PASS]` when every charted day with a KNOWN total is at or
  below the ceiling, `[OVER]` when any is above it. When NO charted day carries a
  known total the comparison clause is not borrowed: the line reads
  `… [n/a] — no charted day carries a known total. This is not a harness budget.
  (use -ceiling to change)`, so an unmeasured chart is never called a pass and the
  `[n/a]` sentence claims nothing about harness tokens. `-ceiling`
  changes the number in the line and in the chart alike. This is a REFERENCE, not a
  budget and not a claim about any harness: the number's home and value are
  unchanged, it still governs the editorial-child lane as a soft target, and no
  harness budget is asserted in the report or the ADR.
- **No harness has a default catalog, and no transcript path is privileged.**
  `pi` was the implicit default three ways and all three are removed: the
  `$PI_MODEL_CATALOG` environment variable, the `~/.pi/agent/models-store.json`
  fallback, and the pi-transcript default in the skill. Price resolution is
  `--model-catalog` flag > the config document's `metrics.model_catalog` > no
  catalog, which means prices are UNKNOWN (never 0). A config value may be written
  as `~/…`: it is home-expanded when READ and the document is never rewritten.
  Transcript locations come from `metrics.transcripts` in the config document — no
  harness is privileged; when it is unset the skill asks the user. The `--config`
  document is therefore the single place a harness is configured, which is what
  makes the skill harness-agnostic rather than pi-shaped. An explicit `--config`
  path that cannot be read WARNS; an absent default document stays silent; neither
  changes an exit code.
- **Declared model and declared total, labelled.** `metrics import` gains `--model`
  (the session's model when the transcript carries none; recorded as
  `model_source=declared` and losing to an observed model) and the BOOLEAN
  `--declare-total` (no value; it stamps an operator-declared session total for a
  transcript whose components cannot yield one, recorded as
  `total_source=synthesized` and presented as a LOWER BOUND, never as observed). An
  observed total outranks either declaration, and a positional left after
  `--declare-total` is a usage error rather than a silently ignored number.
  `ConsumedTokens()` and the component counters are unchanged: a declaration fills
  the total field only, the synthesized figure is exactly the sum of the four
  CONSUMED components (input, output, cache-read, cache-write; reasoning is excluded,
  as in `ConsumedTokens`), and a run with no known consumed component is still
  refused rather than written as a synthesized 0. The provider-native field mapping
  in the antigravity subsection ("Counter semantics") remains exactly as recorded — a
  declaration never re-derives a component from a total. Declarations also PERSIST:
  a bare re-import of the same transcript keeps the stored `model`/`model_source` and
  `total`/`total_source` rather than replacing them with the declaration-free build,
  printing one `preserved stored <id> declarations: …` note. A preserved model is an
  ID-only preservation when the content hash DIFFERS (the transcript was reparsed,
  e.g. it grew): the per-model usage/price/cost come from the CURRENT parse, so
  `sum(Models[].TotalTokens)` still equals the session total and the cost reflects the
  tokens actually seen; only when the hash matches is the stored evidence itself
  reused. A catalog price fix
  still does not rewrite the session (the content hash governs).
