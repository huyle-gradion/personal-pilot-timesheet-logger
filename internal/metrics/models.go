// Package metrics implements the bounded, additive METRICS-1 slice: a transcript
// adapter layer, a pure attribution/duration model, idempotent storage, and a
// deterministic daily aggregate for the review-first timesheet workflow.
//
// Dependency boundary: this package imports the Go standard library and
// internal/workflow ONLY. It deliberately does NOT import internal/benchmark,
// internal/editorial, or internal/run, so the legacy benchmark extractor and the
// editorial synthesizer are never coupled to metrics accounting.
//
// Honesty boundary (inherited from workflow.Measurement): an unavailable
// measurement is recorded as UNKNOWN, never as zero. This package never defines a
// second unknown-vs-zero type; every measurement is a workflow.Measurement.
package metrics

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// SchemaVersion is the DailyAggregate/RunRecord schema version. It is independent
// of workflow.SchemaVersion (which stays 1 and is not bumped by this slice).
const SchemaVersion int = 1

// Named bounds with explicit units (TigerStyle).
const (
	// MaxTranscriptBytes caps a single transcript read so a giant blob cannot
	// exhaust memory. The whole transcript is read once for hashing + parsing.
	MaxTranscriptBytes int64 = 64 << 20 // 64 MiB
	// MaxRunFilesCount caps how many run files a single aggregate scans.
	MaxRunFilesCount int = 5000
	// MaxRunIDLen caps a sanitized run id (also a filename stem).
	MaxRunIDLen int = 128
	// BudgetTargetTokens is the soft daily editorial token target (owner override
	// of the specification's 600; mirrors editorial.SoftDailyTokenTarget without
	// importing internal/editorial).
	BudgetTargetTokens int64 = 1000
)

// Provenance records how a record's measurements were obtained. It is distinct
// from RunState and from workflow provenance fields.
type Provenance string

const (
	// ProvenanceObserved is a first-hand measurement read from a harness-native
	// transcript (e.g. a pi JSONL usage object).
	ProvenanceObserved Provenance = "observed"
	// ProvenanceDeclared is a measurement declared by a provider schema but not
	// verified first-hand on this host (e.g. the claude JSONL schema).
	ProvenanceDeclared Provenance = "declared"
	// ProvenanceEstimated is a transcript-derived interval (durations).
	ProvenanceEstimated Provenance = "estimated"
	// ProvenanceUnknown is an unavailable provenance. It is never guessed.
	ProvenanceUnknown Provenance = "unknown"
)

// Category classifies a call's accounting lane. Editorial and orchestration are
// reported SEPARATELY and are never summed.
type Category string

const (
	// CategoryEditorial labels the editorial lane. It is DELIBERATELY not produced
	// by any transcript adapter: editorial usage is DAY-granular and is copied from
	// the workflow record (the [AI] editorial child call is not a harness turn), so
	// no CallRecord carries this category today. It is retained as the lane label
	// because editorial and orchestration are reported separately.
	CategoryEditorial     Category = "editorial"
	CategoryOrchestration Category = "orchestration"
	// CategoryUnassigned is a call with no recognized timesheet subcommand. It is
	// never folded into a step or into the orchestration lane.
	CategoryUnassigned Category = "unassigned"
)

// StepID identifies a workflow step. StepUnknown is a REAL value: an
// unrecognized subcommand is never guessed into a step.
type StepID string

const (
	StepUnknown   StepID = "unknown"
	StepPreflight StepID = "preflight"
	// StepCollect is the collect-and-propose step (a bare `timesheet review`).
	StepCollect StepID = "collect_propose"
	// StepEditorial is a RESERVED ordering slot. No transcript call is ever
	// attributed to it: the [AI] editorial child call is not a harness turn, so
	// editorial usage is DAY-granular from the workflow record, never per call. It
	// is retained so the step progression is complete and stable.
	StepEditorial StepID = "editorial"
	StepReview    StepID = "review"
	StepPreview   StepID = "preview"
	StepApprove   StepID = "approve"
	StepSubmit    StepID = "submit"
	StepExport    StepID = "export"
)

// stepOrder is the step progression used to derive RunState. StepUnknown has no
// order and is excluded.
var stepOrder = map[StepID]int{
	StepPreflight: 1,
	StepCollect:   2,
	StepEditorial: 3,
	StepReview:    4,
	StepPreview:   5,
	StepApprove:   6,
	StepSubmit:    7,
	StepExport:    8,
}

// RunState classifies a whole run. It is never a claim stronger than the
// transcript supports: a review-stopped run is NEVER labelled completed.
type RunState string

const (
	RunCompleted RunState = "completed"
	RunBlocked   RunState = "blocked"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
	RunPartial   RunState = "partial"
	RunUnknown   RunState = "unknown"
)

// DurationSource labels where a Duration came from.
type DurationSource string

const (
	// DurationMonotonic is a live monotonic measurement of the metrics command's
	// own work (time.Since).
	DurationMonotonic DurationSource = "monotonic"
	// DurationTranscript is a transcript-derived (estimated) interval.
	DurationTranscript DurationSource = "transcript"
	// DurationUnknown is an unavailable duration. It is never zero.
	DurationUnknown DurationSource = "unknown"
)

// Duration is a possibly-unknown elapsed duration in milliseconds.
type Duration struct {
	Value  workflow.Measurement `json:"value"`
	Source DurationSource       `json:"source"`
}

// Usage is a token measurement with FIVE distinct components. Reasoning is a
// separate component and is EXCLUDED from ConsumedTokens (it is not billed as
// consumed context). There is deliberately NO Total field: a provider total is
// never trusted (see CallRecord.ContextLength).
type Usage struct {
	Input      workflow.Measurement `json:"input"`
	Output     workflow.Measurement `json:"output"`
	CacheRead  workflow.Measurement `json:"cache_read"`
	CacheWrite workflow.Measurement `json:"cache_write"`
	Reasoning  workflow.Measurement `json:"reasoning"`
}

// UnknownUsage returns an all-unknown Usage: the honest record when a measurement
// is unavailable.
func UnknownUsage() Usage {
	return Usage{
		Input:      workflow.UnknownMeasurement(),
		Output:     workflow.UnknownMeasurement(),
		CacheRead:  workflow.UnknownMeasurement(),
		CacheWrite: workflow.UnknownMeasurement(),
		Reasoning:  workflow.UnknownMeasurement(),
	}
}

// ZeroUsage returns a fully-known zero Usage. It is the fold identity for summing
// a NON-EMPTY set of runs and the honest "no calls" value.
func ZeroUsage() Usage {
	return Usage{
		Input:      workflow.KnownMeasurement(0),
		Output:     workflow.KnownMeasurement(0),
		CacheRead:  workflow.KnownMeasurement(0),
		CacheWrite: workflow.KnownMeasurement(0),
		Reasoning:  workflow.KnownMeasurement(0),
	}
}

// KnownUsage returns a fully-known Usage.
func KnownUsage(input, output, cacheRead, cacheWrite, reasoning int64) Usage {
	return Usage{
		Input:      workflow.KnownMeasurement(input),
		Output:     workflow.KnownMeasurement(output),
		CacheRead:  workflow.KnownMeasurement(cacheRead),
		CacheWrite: workflow.KnownMeasurement(cacheWrite),
		Reasoning:  workflow.KnownMeasurement(reasoning),
	}
}

// Add accumulates another Usage componentwise. A component is Known only when
// both operands are Known; a mixed/unknown component stays Unknown (never zero).
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:      addMeasurement(u.Input, o.Input),
		Output:     addMeasurement(u.Output, o.Output),
		CacheRead:  addMeasurement(u.CacheRead, o.CacheRead),
		CacheWrite: addMeasurement(u.CacheWrite, o.CacheWrite),
		Reasoning:  addMeasurement(u.Reasoning, o.Reasoning),
	}
}

func addMeasurement(a, b workflow.Measurement) workflow.Measurement {
	if !a.Known || !b.Known {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(a.Value + b.Value)
}

// ConsumedTokens returns input+output+cacheRead+cacheWrite. Reasoning is EXCLUDED.
// It is Known only when all four components are Known; it is NEVER derived from a
// provider total/context field.
func (u Usage) ConsumedTokens() workflow.Measurement {
	total := int64(0)
	for _, m := range []workflow.Measurement{u.Input, u.Output, u.CacheRead, u.CacheWrite} {
		if !m.Known {
			return workflow.UnknownMeasurement()
		}
		total += m.Value
	}
	return workflow.KnownMeasurement(total)
}

// CallCost preserves the provider-native NUMERIC cost fields (e.g. a pi usage
// object's `cost`). It is a provider ANNOTATION only: it is NEVER aggregated, never
// summed, and never used in a percentage. Pointers preserve absent-vs-zero
// honestly (an absent field is omitted, not written as 0).
type CallCost struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	Total      *float64 `json:"total,omitempty"`
}

// CallRecord is ONE assistant turn that carried a usage object (or issued an
// attributed tool call). It stores only counters, ids, timestamps, the attributed
// step, and provider-native numeric cost: message text, tool arguments, and tool
// results are NEVER decoded into any persisted field.
type CallRecord struct {
	RunID     string    `json:"run_id"`
	TurnIndex int       `json:"turn_index"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Usage     Usage     `json:"usage"`
	Category  Category  `json:"category"`
	Step      StepID    `json:"step"`
	Attempt   int       `json:"attempt"`
	// ContextLength is the provider totalTokens. It is VALIDATION ONLY and is
	// never aggregated.
	ContextLength workflow.Measurement `json:"context_length"`
	// Cost is the provider-native numeric cost (never aggregated). It is nil when
	// the provider reports no cost.
	Cost       *CallCost  `json:"cost,omitempty"`
	Provenance Provenance `json:"provenance"`
}

// StepRecord is the per-step roll-up for one run. Retries == Calls-1.
type StepRecord struct {
	Step    StepID   `json:"step"`
	Calls   int      `json:"calls"`
	Retries int      `json:"retries"`
	Usage   Usage    `json:"usage"`
	Elapsed Duration `json:"elapsed"`
	State   RunState `json:"state"`
}

// RunRecord is ONE imported run (one session). RunID is the session id when
// present, else sha256(content). There is deliberately NO transcript-path field:
// a local filesystem path is never persisted.
type RunRecord struct {
	RunID           string       `json:"run_id"`
	SessionID       string       `json:"session_id"`
	Provider        string       `json:"provider"`
	Model           string       `json:"model"`
	Date            string       `json:"date"`
	StartedAt       time.Time    `json:"started_at"`
	EndedAt         time.Time    `json:"ended_at"`
	State           RunState     `json:"state"`
	EndToEnd        Duration     `json:"end_to_end"`
	Active          Duration     `json:"active"`
	HumanWait       Duration     `json:"human_wait"`
	HarnessStart    Duration     `json:"harness_start"`
	Usage           Usage        `json:"usage"`
	Calls           []CallRecord `json:"calls"`
	Steps           []StepRecord `json:"steps"`
	ContentHash     string       `json:"content_hash"`
	Commit          string       `json:"commit,omitempty"`
	WorkflowVersion string       `json:"workflow_version,omitempty"`
	ConfigMode      string       `json:"config_mode,omitempty"`
	// OptimizationChange annotates the run with the workflow change it exercised
	// (e.g. a narrower [AI] step). It is a non-secret, caller-supplied string.
	OptimizationChange string     `json:"optimization_change,omitempty"`
	Provenance         Provenance `json:"provenance"`
}

// TokenPrice is the per-MILLION-token USD price of one (provider, model). All
// four rates are *float64 so the package's nil-vs-pointer-to-zero idiom is
// preserved: a nil rate is UNKNOWN, and a non-nil pointer to 0 is a KNOWN zero
// rate (e.g. a model that does not bill cache writes). A lookup key is ALWAYS
// (provider, model): the same model id can carry different prices under different
// providers.
type TokenPrice struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// PriceSource labels where a session's price came from. It is never a claim
// stronger than the catalog supports.
type PriceSource string

const (
	// PriceSourceCatalog is a price resolved from the model catalog by
	// (provider, model).
	PriceSourceCatalog PriceSource = "catalog"
	// PriceSourceUnknown is an unavailable price: the model is absent from the
	// catalog. It is never treated as zero.
	PriceSourceUnknown PriceSource = "unknown"
)

// AssociationSource labels how a session's LoggedDate was derived. It records the
// derivation method only; NO tool-argument value is ever decoded (ADR-0006 Safety).
// An import-time `--date` is AssociationImportFlag: the day was stated explicitly
// by the operator, so no transcript value is read to establish it.
type AssociationSource string

const (
	// AssociationLocalStepDate is a LoggedDate derived from the single LOCAL date
	// of the session's workflow-step calls.
	AssociationLocalStepDate AssociationSource = "local_step_date"
	// AssociationUnknown is an unavailable association: the session's workflow-step
	// calls span zero or two-or-more local dates (e.g. a session straddling local
	// midnight). It is never guessed.
	AssociationUnknown AssociationSource = "unknown"
	// AssociationImportFlag is an operator-stated day: the day was stated
	// explicitly by the operator at import time (`metrics import --date`); no
	// transcript tool argument is decoded.
	AssociationImportFlag AssociationSource = "import_flag"
)

// SessionModel is one model's slice of a session's usage, keyed by
// (provider, model). It is carried even for a single-model session so the
// per-model price and cost are always attributable.
type SessionModel struct {
	Provider    string               `json:"provider"`
	Model       string               `json:"model"`
	Usage       Usage                `json:"usage"`
	TotalTokens workflow.Measurement `json:"total_tokens"`
	Price       TokenPrice           `json:"price"`
	Cost        *float64             `json:"cost"`
	PriceSource PriceSource          `json:"price_source"`
}

// SessionRecord is ONE harness session, identified by its transcript session id
// (one transcript = one session). A session is NOT single-model: Model is "" (and
// the per-(provider, model) breakdown in Models is authoritative) when more than
// one distinct model ran.
//
// Unknown price uses the package's nil-vs-pointer-to-zero idiom: a nil rate in
// Price (or a nil Cost) is UNKNOWN, a non-nil pointer to 0 is a known zero. Cost
// is nil when ANY contributing token count or rate is unknown.
//
// LoggedDate/Association are settable by a caller BEFORE WriteSession (the CLI
// lane's caller-supplied day overrides the transcript-derived association).
// WriteSession's idempotency key is ContentHash, so such an override never causes
// a spurious rewrite loop.
type SessionRecord struct {
	SchemaVersion int       `json:"schema_version"`
	SessionID     string    `json:"session_id"`
	Provider      string    `json:"provider"`
	ContentHash   string    `json:"content_hash"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	// LocalDates is the distinct sorted LOCAL dates of the session's workflow-step
	// calls. Only timestamps and the already-derived StepID are used; no
	// tool-argument value is decoded.
	LocalDates []string `json:"local_dates"`
	// LoggedDate is the day this session is associated with, or "" when unknown.
	LoggedDate  string            `json:"logged_date"`
	Association AssociationSource `json:"association"`
	// Model is the single model when exactly one distinct (provider, model) ran,
	// else "" (a multi-model session is never attributed to one model).
	Model  string         `json:"model"`
	Models []SessionModel `json:"models"`
	Usage  Usage          `json:"usage"`
	// TotalTokens is the consumed total (input+output+cacheRead+cacheWrite).
	// Reasoning is EXCLUDED.
	TotalTokens workflow.Measurement `json:"total_tokens"`
	CacheHitPct *float64             `json:"cache_hit_pct"`
	Price       TokenPrice           `json:"price"`
	Cost        *float64             `json:"cost"`
	PriceSource PriceSource          `json:"price_source"`
	Provenance  Provenance           `json:"provenance"`
}

// BudgetReport is the soft daily editorial budget report. Spent is the editorial
// total (including cache and retries).
type BudgetReport struct {
	Target int64                `json:"target"`
	Spent  workflow.Measurement `json:"spent"`
	State  string               `json:"state"`
}

// Annotations is the sorted union of the non-empty per-run annotations for a day.
// It lets a comparison cite the commit/workflow-version/config-mode/
// optimization-change on each side without re-reading the runs.
type Annotations struct {
	Commits             []string `json:"commits"`
	WorkflowVersions    []string `json:"workflow_versions"`
	ConfigModes         []string `json:"config_modes"`
	OptimizationChanges []string `json:"optimization_changes"`
}

// DailyAggregate is the durable per-day metrics roll-up. It is a PURE function of
// its inputs: it deliberately stores NO GeneratedAt timestamp, so aggregating
// twice is byte-identical. Orchestration, unassigned, and editorial usage are
// SEPARATE fields and are never summed together.
type DailyAggregate struct {
	SchemaVersion        int                  `json:"schema_version"`
	Date                 string               `json:"date"`
	Orchestration        Usage                `json:"orchestration"`
	Editorial            Usage                `json:"editorial"`
	Unassigned           Usage                `json:"unassigned"`
	EditorialRuns        int                  `json:"editorial_runs"`
	EditorialInvoked     bool                 `json:"editorial_invoked"`
	EditorialInputToStep workflow.Measurement `json:"editorial_input_to_step"`
	Budget               BudgetReport         `json:"budget"`
	EndToEnd             Duration             `json:"end_to_end"`
	Active               Duration             `json:"active"`
	HumanWait            Duration             `json:"human_wait"`
	UnassignedCalls      int                  `json:"unassigned_calls"`
	Annotations          Annotations          `json:"annotations"`
	RunIDs               []string             `json:"run_ids"`
	RunCount             int                  `json:"run_count"`
	// Session-scoped token metrics (METRICS-2). They are SEPARATE from
	// Orchestration/Editorial/Unassigned and are never merged with them.
	// SessionTotalTokens is the sum of each session's consumed total; reasoning is
	// EXCLUDED. A nil SessionCacheHitPct/SessionCost is UNKNOWN, never zero.
	//
	// SessionCost is `omitempty` so a nil cost is absent from the JSON rather than
	// a spurious `session_cost: null`; the report always renders "unknown". This
	// also keeps the aggregate free of any "cost" substring, matching the
	// provider-native-cost exclusion asserted by the existing tests.
	SessionCount         int                  `json:"session_count"`
	SessionIDs           []string             `json:"session_ids"`
	SessionUsage         Usage                `json:"session_usage"`
	SessionTotalTokens   workflow.Measurement `json:"session_total_tokens"`
	SessionCacheHitPct   *float64             `json:"session_cache_hit_pct"`
	SessionCost          *float64             `json:"session_cost,omitempty"`
	UnassociatedSessions int                  `json:"unassociated_sessions"`
	Provenance           Provenance           `json:"provenance"`
}
