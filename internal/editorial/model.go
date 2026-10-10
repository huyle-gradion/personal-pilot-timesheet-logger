// Package editorial implements the S7 optional editorial synthesis layer: it
// builds a bounded, non-secret request from a proposal, validates an editorial
// agent's response FAIL-CLOSED, falls back to deterministic text, and accounts for
// editorial versus orchestration usage separately.
//
// CRITICAL BOUNDARY: AI output is TEXT ONLY. It can never alter intervals,
// attendance, projects, evidence links, or approvals. Applied text flows into
// ConfirmedInterval.Description through the existing review path, so a text change
// participates in S6 approval invalidation via LocalFingerprint/PlanHash — no new
// S6 code.
//
// The layer is OPTIONAL: with no editor (nil) or a disabled config, synthesis is
// deterministic-only. It performs NO live network access; the command adapter is
// the only subprocess surface and is wired to a concrete command at S8.
package editorial

import (
	"encoding/json"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Named bounds with explicit units (TigerStyle).
const (
	MaxTargets          int   = 100     // bounded targets per request
	MaxFactsPerTarget   int   = 20      // bounded facts per target
	MaxFactBytes        int   = 500     // bounded bytes per fact
	MaxTitleBytes       int   = 200     // bounded title bytes after validation
	MaxDescriptionBytes int   = 3000    // bounded description bytes after validation
	MaxRequestBytes     int64 = 1 << 20 // 1 MiB serialized request cap (stdin)
	MaxResponseBytes    int64 = 1 << 20 // 1 MiB raw response cap: enforced at CAPTURE and re-checked before parse
)

// SoftDailyTokenTarget is the owner-confirmed soft daily editorial budget in
// tokens. It counts editorial input+output+cache tokens across retries and
// reruns. It is an explicit owner override of the specification's 600.
const SoftDailyTokenTarget int64 = 1000

// DefaultCommandTimeout bounds a single command-adapter invocation.
const DefaultCommandTimeout = 60 // seconds

// FactKind classifies a bounded, non-secret, evidence-derived string.
type FactKind string

const (
	FactCommitSubject FactKind = "commit_subject"
	FactPRTitle       FactKind = "pr_title"
	FactEventTitle    FactKind = "event_title"
)

// Fact is a single bounded, non-secret evidence string. Facts NEVER carry times,
// attendance, classification, credentials, or secrets.
type Fact struct {
	Kind FactKind `json:"kind"`
	Text string   `json:"text"`
}

// Target is one editorial target: a proposal block's identity plus its bounded
// facts. It deliberately carries NO times, attendance, or classification: sending
// times invites duration reasoning, which the boundary forbids.
type Target struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Date        string   `json:"date"`
	Repository  string   `json:"repository,omitempty"`
	PRNumber    int      `json:"pr_number,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	Project     string   `json:"project,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	Facts       []Fact   `json:"facts,omitempty"`
}

// Request is the whole editorial request for one day. Warnings records any
// bounded truncation applied while building it (never sent as content to reason
// about).
type Request struct {
	Date     string   `json:"date"`
	Targets  []Target `json:"targets"`
	Warnings []string `json:"warnings,omitempty"`
}

// Text is a proposed title/description pair.
type Text struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Response is the parsed, validated editorial response: one Text per target id.
// On the wire it is a FLAT JSON object mapping target id -> {title, description}.
type Response struct {
	Suggestions map[string]Text `json:"-"`
}

// MarshalJSON emits the flat wire form.
func (r Response) MarshalJSON() ([]byte, error) {
	if r.Suggestions == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(r.Suggestions)
}

// UnmarshalJSON reads the flat wire form. It is lenient; ParseResponse is the
// strict, fail-closed validator.
func (r *Response) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &r.Suggestions)
}

// Origin identifies where a suggestion's text came from.
type Origin string

const (
	OriginEditorial     Origin = "editorial"     // produced by the configured agent
	OriginDeterministic Origin = "deterministic" // produced by the local fallback
	OriginCache         Origin = "cache"         // reused from a prior suggestion
)

// Usage is a token measurement for one editorial call (or the aggregate). An
// unknown component is NOT zero.
type Usage struct {
	Input  workflow.Measurement `json:"input"`
	Output workflow.Measurement `json:"output"`
	Cache  workflow.Measurement `json:"cache"`
}

// UnknownUsage returns an all-unknown Usage: the honest record when a measurement
// is unavailable.
func UnknownUsage() Usage {
	return Usage{
		Input:  workflow.UnknownMeasurement(),
		Output: workflow.UnknownMeasurement(),
		Cache:  workflow.UnknownMeasurement(),
	}
}

// KnownUsage returns a fully-known Usage.
func KnownUsage(input, output, cache int64) Usage {
	return Usage{
		Input:  workflow.KnownMeasurement(input),
		Output: workflow.KnownMeasurement(output),
		Cache:  workflow.KnownMeasurement(cache),
	}
}

// Add accumulates another Usage componentwise. A component is Known only when
// both operands are Known; a mixed/unknown component stays Unknown (never zero).
func (u Usage) Add(o Usage) Usage {
	return Usage{
		Input:  addMeasurement(u.Input, o.Input),
		Output: addMeasurement(u.Output, o.Output),
		Cache:  addMeasurement(u.Cache, o.Cache),
	}
}

func addMeasurement(a, b workflow.Measurement) workflow.Measurement {
	if !a.Known || !b.Known {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(a.Value + b.Value)
}

// Total returns the summed input+output+cache measurement. It is Known only when
// all three components are Known; otherwise it is Unknown (never zero).
func (u Usage) Total() workflow.Measurement {
	total := workflow.KnownMeasurement(0)
	for _, m := range []workflow.Measurement{u.Input, u.Output, u.Cache} {
		if !m.Known {
			return workflow.UnknownMeasurement()
		}
		total.Value += m.Value
	}
	return total
}

// Suggestion is one target's editorial result. RequestHash is the request hash the
// text was produced for, enabling zero-token reuse on an unchanged request.
type Suggestion struct {
	TargetID    string   `json:"target_id"`
	RequestHash string   `json:"request_hash"`
	Text        Text     `json:"text"`
	Origin      Origin   `json:"origin"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Report is the daily editorial/orchestration usage report. Editorial and
// orchestration usage are reported SEPARATELY and are never summed.
type Report struct {
	Date          string  `json:"date"`
	Editorial     Usage   `json:"editorial"`
	Orchestration Usage   `json:"orchestration"`
	EditorialRuns int     `json:"editorial_runs"`
	Invoked       bool    `json:"invoked"`
	RuntimeMS     int64   `json:"runtime_ms"`
	PeakRSSMB     float64 `json:"peak_rss_mb"`
}

// Result is the outcome of one Synthesize call.
type Result struct {
	Date        string       `json:"date"`
	Suggestions []Suggestion `json:"suggestions"`
	Report      Report       `json:"report"`
}

// BudgetState classifies the soft daily editorial budget.
type BudgetState string

const (
	// BudgetKnownAvailable means the spent total is Known and below the target.
	BudgetKnownAvailable BudgetState = "known_available"
	// BudgetKnownExhausted means the spent total is Known and at/above the target.
	BudgetKnownExhausted BudgetState = "known_exhausted"
	// BudgetUnknown means the spent total is not Known. It is NEVER treated as
	// zero and NEVER assumed exhausted.
	BudgetUnknown BudgetState = "unknown"
)
