package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// MetricComparison is one metric compared base -> target. Delta and Percent are
// nil (JSON null / table "n/a") UNLESS the base value is Known and non-zero — a
// percentage is never fabricated against an unknown or zero baseline.
type MetricComparison struct {
	Name    string               `json:"name"`
	Base    workflow.Measurement `json:"base"`
	Target  workflow.Measurement `json:"target"`
	Delta   *int64               `json:"delta"`
	Percent *float64             `json:"percent"`
}

// CompareInput is the base/target pair plus the per-side call counts (which are
// derived from the runs, not stored on the aggregate).
type CompareInput struct {
	Base, Target           DailyAggregate
	BaseCalls, TargetCalls int
}

// Comparison is the base -> target comparison document.
type Comparison struct {
	BaseDate          string             `json:"base_date"`
	TargetDate        string             `json:"target_date"`
	Metrics           []MetricComparison `json:"metrics"`
	BaseAnnotations   Annotations        `json:"base_annotations"`
	TargetAnnotations Annotations        `json:"target_annotations"`
}

// Compare builds a base -> target comparison. A percentage/delta is reported ONLY
// when the base value is Known and non-zero; otherwise it is omitted (nil).
func Compare(in CompareInput) Comparison {
	return Comparison{
		BaseDate:   in.Base.Date,
		TargetDate: in.Target.Date,
		Metrics: []MetricComparison{
			compareMetric("orchestration_input", in.Base.Orchestration.Input, in.Target.Orchestration.Input),
			compareMetric("orchestration_output", in.Base.Orchestration.Output, in.Target.Orchestration.Output),
			compareMetric("orchestration_cache_read", in.Base.Orchestration.CacheRead, in.Target.Orchestration.CacheRead),
			compareMetric("orchestration_cache_write", in.Base.Orchestration.CacheWrite, in.Target.Orchestration.CacheWrite),
			compareMetric("orchestration_consumed", in.Base.Orchestration.ConsumedTokens(), in.Target.Orchestration.ConsumedTokens()),
			compareMetric("editorial_input_to_step", in.Base.EditorialInputToStep, in.Target.EditorialInputToStep),
			compareMetric("budget_spent", in.Base.Budget.Spent, in.Target.Budget.Spent),
			compareMetric("calls", workflow.KnownMeasurement(int64(in.BaseCalls)), workflow.KnownMeasurement(int64(in.TargetCalls))),
			compareMetric("end_to_end_ms", in.Base.EndToEnd.Value, in.Target.EndToEnd.Value),
			compareMetric("active_ms", in.Base.Active.Value, in.Target.Active.Value),
			compareMetric("human_wait_ms", in.Base.HumanWait.Value, in.Target.HumanWait.Value),
			compareMetric("session_consumed", in.Base.SessionTotalTokens, in.Target.SessionTotalTokens),
			compareMetric("sessions", workflow.KnownMeasurement(int64(in.Base.SessionCount)), workflow.KnownMeasurement(int64(in.Target.SessionCount))),
		},
		BaseAnnotations:   in.Base.Annotations,
		TargetAnnotations: in.Target.Annotations,
	}
}

// compareMetric computes delta/percent with the reportability guard.
func compareMetric(name string, base, target workflow.Measurement) MetricComparison {
	mc := MetricComparison{Name: name, Base: base, Target: target}
	if !base.Known || base.Value == 0 || !target.Known {
		return mc
	}
	delta := target.Value - base.Value
	mc.Delta = &delta
	p := float64(delta) / float64(base.Value) * 100
	mc.Percent = &p
	return mc
}

var compareColumns = []struct {
	header string
	width  int
}{
	{"METRIC", 26},
	{"BASE", 12},
	{"TARGET", 12},
	{"DELTA", 12},
	{"PERCENT", 0},
}

// RenderCompareJSON writes the comparison as one JSON document.
func RenderCompareJSON(w io.Writer, c Comparison) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("metrics: marshal comparison: %w", err)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// RenderCompareTable writes the fixed ASCII comparison table plus each side's
// annotations. An unreportable delta/percent prints "n/a".
func RenderCompareTable(w io.Writer, c Comparison) error {
	if err := writeCompareRow(w, []string{"METRIC", "BASE", "TARGET", "DELTA", "PERCENT"}); err != nil {
		return err
	}
	for _, m := range c.Metrics {
		if err := writeCompareRow(w, []string{
			m.Name,
			formatMeasurement(m.Base),
			formatMeasurement(m.Target),
			formatDelta(m.Delta),
			formatPercent(m.Percent),
		}); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nbase   %s  %s\n", c.BaseDate, formatAnnotations(c.BaseAnnotations)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "target %s  %s\n", c.TargetDate, formatAnnotations(c.TargetAnnotations)); err != nil {
		return err
	}
	return nil
}

func writeCompareRow(w io.Writer, cells []string) error {
	var b strings.Builder
	for i, col := range compareColumns {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		if col.width > 0 {
			b.WriteString(pad(cell, col.width))
			b.WriteByte(' ')
		} else {
			b.WriteString(cell)
		}
	}
	b.WriteByte('\n')
	_, err := io.WriteString(w, b.String())
	return err
}

// formatDelta renders a reportable delta, else "n/a".
func formatDelta(d *int64) string {
	if d == nil {
		return "n/a"
	}
	return strconv.FormatInt(*d, 10)
}

// formatPercent renders a reportable percentage, else "n/a".
func formatPercent(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", *p)
}

// formatAnnotations renders a side's annotations compactly.
func formatAnnotations(a Annotations) string {
	return fmt.Sprintf("commit=%s workflow-version=%s config-mode=%s optimization-change=%s",
		joinOrNone(a.Commits), joinOrNone(a.WorkflowVersions), joinOrNone(a.ConfigModes), joinOrNone(a.OptimizationChanges))
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, ",")
}
