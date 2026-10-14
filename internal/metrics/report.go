package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// TableInput carries the aggregate plus its runs so the table can render the call
// count and the state breakdown, which are derived from the runs rather than
// stored in the aggregate.
type TableInput struct {
	Aggregate DailyAggregate
	Runs      []RunRecord
}

// RenderJSON writes the DailyAggregate verbatim: struct field order is stable and
// schema_version is first.
func RenderJSON(w io.Writer, agg DailyAggregate) error {
	data, err := json.MarshalIndent(agg, "", "  ")
	if err != nil {
		return fmt.Errorf("metrics: marshal aggregate: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// RenderJSONDocuments writes ONE valid JSON document for a set of aggregates: a
// single object for one day, or a JSON array for a range. It never emits a bare
// concatenation of objects.
func RenderJSONDocuments(w io.Writer, aggs []DailyAggregate) error {
	if len(aggs) == 1 {
		return RenderJSON(w, aggs[0])
	}
	if aggs == nil {
		aggs = []DailyAggregate{}
	}
	data, err := json.MarshalIndent(aggs, "", "  ")
	if err != nil {
		return fmt.Errorf("metrics: marshal aggregates: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// tableColumns is the fixed ASCII column layout.
var tableColumns = []struct {
	header string
	width  int
}{
	{"DATE", 12},
	{"RUNS", 5},
	{"CALLS", 6},
	{"INPUT", 10},
	{"OUTPUT", 10},
	{"CACHER", 10},
	{"CACHEW", 10},
	{"REASON", 10},
	{"UNASSIGNED-CALLS", 16},
	{"EDIT-IN-TO-STEP", 16},
	{"EDIT-TOTAL", 12},
	{"BUDGET", 16},
	{"E2E-MS", 9},
	{"ACTIVE-MS", 11},
	{"WAIT-MS", 9},
	{"SESSIONS", 8},
	{"SESS-CACHEHIT", 12},
	{"SESS-COST", 12},
	{"UNASSOC", 8},
	{"STATES", 0},
}

// RenderTable writes the fixed ASCII table for one day, including the duration
// totals and the unassigned-call count.
func RenderTable(w io.Writer, in TableInput) error {
	agg := in.Aggregate
	calls := 0
	states := make(map[RunState]int)
	for i := range in.Runs {
		calls += len(in.Runs[i].Calls)
		states[in.Runs[i].State]++
	}

	if err := writeHeader(w); err != nil {
		return err
	}
	row := []string{
		agg.Date,
		strconv.Itoa(agg.RunCount),
		strconv.Itoa(calls),
		formatMeasurement(agg.Orchestration.Input),
		formatMeasurement(agg.Orchestration.Output),
		formatMeasurement(agg.Orchestration.CacheRead),
		formatMeasurement(agg.Orchestration.CacheWrite),
		formatMeasurement(agg.Orchestration.Reasoning),
		strconv.Itoa(agg.UnassignedCalls),
		formatMeasurement(agg.EditorialInputToStep),
		formatMeasurement(agg.Budget.Spent),
		agg.Budget.State,
		formatMeasurement(agg.EndToEnd.Value),
		formatMeasurement(agg.Active.Value),
		formatMeasurement(agg.HumanWait.Value),
		strconv.Itoa(agg.SessionCount),
		formatOptionalPercent(agg.SessionCacheHitPct),
		formatOptionalCost(agg.SessionCost),
		strconv.Itoa(agg.UnassociatedSessions),
		formatStates(states),
	}
	if err := writeRow(w, row); err != nil {
		return err
	}
	if agg.Provenance == ProvenanceDeclared {
		if _, err := fmt.Fprintln(w, "[declared schema] one or more runs were parsed from a declared provider schema not verified first-hand on this host"); err != nil {
			return err
		}
	}
	return nil
}

func writeHeader(w io.Writer) error {
	parts := make([]string, 0, len(tableColumns))
	for _, c := range tableColumns {
		parts = append(parts, c.header)
	}
	return writeRow(w, parts)
}

// writeRow pads every column except the last to its fixed width and joins with a
// single space.
func writeRow(w io.Writer, cells []string) error {
	var b strings.Builder
	for i, c := range tableColumns {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if c.width > 0 {
			b.WriteString(pad(cell, c.width))
			b.WriteByte(' ')
		} else {
			b.WriteString(cell)
		}
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// formatMeasurement renders a Known measurement as its integer value and an
// unknown measurement as "unknown" (never "0").
func formatMeasurement(m workflow.Measurement) string {
	if !m.Known {
		return "unknown"
	}
	return strconv.FormatInt(m.Value, 10)
}

// formatOptionalPercent renders a known cache-hit percentage, or "unknown" when
// it is unavailable (nil). It never prints "0" for an unknown value.
func formatOptionalPercent(p *float64) string {
	if p == nil {
		return "unknown"
	}
	return strconv.FormatFloat(*p, 'f', 1, 64) + "%"
}

// formatOptionalCost renders a known USD cost, or "unknown" when it is
// unavailable (nil). It never prints "0" for an unknown value.
func formatOptionalCost(c *float64) string {
	if c == nil {
		return "unknown"
	}
	return strconv.FormatFloat(*c, 'f', -1, 64)
}

// formatStates renders a deterministic state breakdown, e.g. "partial=1 review=2".
func formatStates(states map[RunState]int) string {
	if len(states) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Itoa(states[RunState(k)]))
	}
	return strings.Join(parts, " ")
}
