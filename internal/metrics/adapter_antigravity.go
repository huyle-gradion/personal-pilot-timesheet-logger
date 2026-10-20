package metrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// antigravityAdapter parses an Antigravity (agy) JSONL transcript. antigravity is
// OBSERVED provenance: the header shape and the three counters were verified
// first-hand on this host, unlike claude's declared-only schema, so the report
// prints no `[declared schema]` marker.
//
// Decoded fields are ONLY `type`, `source`, `step_index`, `created_at`, the three
// counters, and `truncated_fields` as a trust marker. `content`, `thinking`
// (thinking TEXT), tool-call names/arguments/results, `status` and `error` are
// never named by the decode shape, so they are never retained (ADR-0006:37-39).
type antigravityAdapter struct{}

func (antigravityAdapter) Name() string           { return "antigravity" }
func (antigravityAdapter) Provenance() Provenance { return ProvenanceObserved }

// Detect matches an Antigravity header line on the five-scalar discriminator
// recorded at state.md:2372 and nothing more: `step_index` numeric (number or
// numeric string), `created_at` non-empty, `source` in {MODEL, USER_EXPLICIT},
// `type` in {GENERIC, PLANNER_RESPONSE, USER_INPUT}, and `sessionId` ABSENT.
//
// Mutual exclusivity with the other adapters holds by construction: pi requires a
// `type:"session"` line (Antigravity has none), and claude requires `sessionId`
// (or `summary`+`leafUuid`), which Antigravity never carries. `auto` order is
// therefore immaterial.
func (antigravityAdapter) Detect(header []byte) bool {
	if len(header) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(header, &probe); err != nil {
		return false
	}
	// A key that is PRESENT (even as null) is not this shape.
	if _, present := probe["sessionId"]; present {
		return false
	}
	if !antigravityNumeric(probe["step_index"]) {
		return false
	}
	if !antigravityNonEmpty(probe["created_at"]) {
		return false
	}
	if !antigravityMember(probe["source"], "MODEL", "USER_EXPLICIT") {
		return false
	}
	return antigravityMember(probe["type"], "GENERIC", "PLANNER_RESPONSE", "USER_INPUT")
}

// antigravityNumeric reports whether raw is a JSON number or a string coercible to
// one. It probes no other field.
func antigravityNumeric(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return false
		}
		_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return err == nil
	}
	var n json.Number
	return json.Unmarshal(raw, &n) == nil
}

// antigravityNonEmpty reports whether raw is a non-blank string or a JSON number.
// Objects, arrays, booleans and null are not a timestamp.
func antigravityNonEmpty(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return false
		}
		return strings.TrimSpace(s) != ""
	}
	var n json.Number
	return json.Unmarshal(raw, &n) == nil
}

// antigravityMember reports whether raw decodes to one of the allowed strings.
func antigravityMember(raw json.RawMessage, allowed ...string) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}

// antigravityLine is the minimal decoded shape of an Antigravity line. Parse
// reads only `source`, `created_at`, the three counters, and `truncated_fields`;
// `type` and `step_index` are NOT decoded because nothing consumes them. The
// semantic `type` values are the Detect discriminator's business.
type antigravityLine struct {
	Source    string          `json:"source"`
	CreatedAt json.RawMessage `json:"created_at"`
	// truncatedFields names TEXT fields that were abbreviated. It is a trust
	// marker only: it never promotes or invents a counter.
	TruncatedFields []string `json:"truncated_fields"`
	InputTokens     *int64   `json:"input_tokens"`
	OutputTokens    *int64   `json:"output_tokens"`
	CacheReadTokens *int64   `json:"cache_read_tokens"`
}

// Parse reads the Antigravity JSONL transcript. Every usage-bearing line (one
// carrying at least one of the three counters) becomes one CallRecord with
// StepUnknown + CategoryUnassigned: `tool_calls[].args` carries no shell command,
// so no step can be attributed. The transcript carries no model identifier and no
// session id, so Model is empty, price/cost are unknown, and Import derives
// RunID = "sha256:"+ContentHash.
func (antigravityAdapter) Parse(ctx context.Context, r io.Reader) (RunRecord, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), int(MaxTranscriptBytes))

	var (
		startedAt         time.Time
		endedAt           time.Time
		calls             []CallRecord
		obs               []Observation
		turnIndex         int
		sawModel          bool
		lastSourceIsModel bool
	)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return RunRecord{}, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var l antigravityLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue
		}
		// A transcript ending on a MODEL turn is complete; one ending on
		// USER_INPUT stopped after the user's turn.
		sawModel = sawModel || l.Source == "MODEL"
		lastSourceIsModel = l.Source == "MODEL"
		ts, ok := parseTimestamp(l.CreatedAt)
		if ok {
			// A MODEL line is a turn; a USER_INPUT line is an observation point.
			obs = append(obs, Observation{Timestamp: ts, IsTurn: l.Source == "MODEL"})
			startedAt = earlier(startedAt, ts)
			endedAt = later(endedAt, ts)
		}
		if !antigravityUsageBearing(l) {
			continue
		}
		turnIndex++
		calls = append(calls, CallRecord{
			TurnIndex:  turnIndex,
			Provider:   "antigravity",
			Timestamp:  ts,
			Usage:      mapAntigravityUsage(l),
			Category:   CategoryUnassigned,
			Step:       StepUnknown,
			Provenance: ProvenanceObserved,
			// ContextLength: the observed shape has no totalTokens/context field,
			// so it is UNKNOWN (never derived from the component counters).
			ContextLength: workflow.UnknownMeasurement(),
		})
	}
	if err := scanner.Err(); err != nil {
		return RunRecord{}, err
	}

	return buildRun(runParts{
		provider:     "antigravity",
		startedAt:    startedAt,
		endedAt:      endedAt,
		calls:        calls,
		observations: obs,
		// A closing turn is DERIVED from the already-decoded source: a transcript
		// that ends on a MODEL turn is complete, while one that ends on USER_INPUT
		// stopped after the user's turn and reads cancelled. The state is never a
		// claim stronger than the transcript supports.
		closingTurn: sawModel && lastSourceIsModel,
		provenance:  ProvenanceObserved,
	}), nil
}

// antigravityUsageBearing reports whether a line carries any of the three
// counters. A USER_INPUT line never does; a MODEL line sometimes carries none.
func antigravityUsageBearing(l antigravityLine) bool {
	return l.InputTokens != nil || l.OutputTokens != nil || l.CacheReadTokens != nil
}

// mapAntigravityUsage maps the observed counters. There is NO cache-write counter
// and NO reasoning counter (the `thinking` field is thinking TEXT, never a
// counter), so both stay UNKNOWN. No total is ever synthesized from
// input+output+cache_read: ADR-0006:29-30 requires an unavailable value to be
// UNKNOWN rather than a lower bound presented as a total, which is why
// ConsumedTokens() stays Unknown and CacheHitPercent() stays nil.
func mapAntigravityUsage(l antigravityLine) Usage {
	return Usage{
		Input:      antigravityMeasurement(l.InputTokens, l.TruncatedFields, "input_tokens"),
		Output:     antigravityMeasurement(l.OutputTokens, l.TruncatedFields, "output_tokens"),
		CacheRead:  antigravityMeasurement(l.CacheReadTokens, l.TruncatedFields, "cache_read_tokens"),
		CacheWrite: workflow.UnknownMeasurement(),
		Reasoning:  workflow.UnknownMeasurement(),
	}
}

// antigravityMeasurement reads one counter, downgrading it to UNKNOWN when the
// line's `truncated_fields` marker names it. The marker is documented to name only
// text fields, so this is a guard rather than an expected path; the LINE is still
// counted because its counters are intact unless marked.
func antigravityMeasurement(p *int64, truncated []string, name string) workflow.Measurement {
	if p == nil {
		return workflow.UnknownMeasurement()
	}
	for _, f := range truncated {
		if f == name {
			return workflow.UnknownMeasurement()
		}
	}
	return workflow.KnownMeasurement(*p)
}
