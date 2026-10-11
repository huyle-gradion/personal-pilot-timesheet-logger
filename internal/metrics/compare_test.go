package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func compareByName(c Comparison) map[string]MetricComparison {
	out := make(map[string]MetricComparison, len(c.Metrics))
	for _, m := range c.Metrics {
		out[m.Name] = m
	}
	return out
}

// TestCompare_PercentageAndNA (F4) asserts a percentage is reported only for a
// Known, non-zero baseline; a zero or unknown baseline yields n/a / null.
func TestCompare_PercentageAndNA(t *testing.T) {
	base := DailyAggregate{
		Date:                 "2026-10-10",
		Orchestration:        KnownUsage(100, 10, 0, 0, 0),
		EditorialInputToStep: workflow.KnownMeasurement(50),
		Budget:               BudgetReport{Spent: workflow.KnownMeasurement(1000)},
		Annotations:          Annotations{Commits: []string{"c1"}},
	}
	target := DailyAggregate{
		Date:                 "2026-10-11",
		Orchestration:        KnownUsage(50, 5, 0, 0, 0),
		EditorialInputToStep: workflow.UnknownMeasurement(),
		Budget:               BudgetReport{Spent: workflow.KnownMeasurement(500)},
		Annotations:          Annotations{OptimizationChanges: []string{"x"}},
	}
	cmp := Compare(CompareInput{Base: base, Target: target, BaseCalls: 4, TargetCalls: 8})
	byName := compareByName(cmp)

	in := byName["orchestration_input"]
	if in.Percent == nil || *in.Percent != -50 {
		t.Fatalf("orchestration_input percent = %v, want -50", in.Percent)
	}
	if in.Delta == nil || *in.Delta != -50 {
		t.Fatalf("orchestration_input delta = %v, want -50", in.Delta)
	}
	calls := byName["calls"]
	if calls.Percent == nil || *calls.Percent != 100 {
		t.Fatalf("calls percent = %v, want 100", calls.Percent)
	}
	// Base non-zero but target UNKNOWN -> n/a.
	edit := byName["editorial_input_to_step"]
	if edit.Percent != nil || edit.Delta != nil {
		t.Fatalf("editorial_input_to_step = %+v, want n/a (target unknown)", edit)
	}
	if cmp.BaseAnnotations.Commits[0] != "c1" || cmp.TargetAnnotations.OptimizationChanges[0] != "x" {
		t.Fatalf("annotations not carried: %+v", cmp)
	}

	// Zero baseline -> n/a.
	zero := Compare(CompareInput{
		Base:   DailyAggregate{Date: "2026-10-10", Orchestration: KnownUsage(0, 0, 0, 0, 0)},
		Target: DailyAggregate{Date: "2026-10-11", Orchestration: KnownUsage(10, 0, 0, 0, 0)},
	})
	if m := compareByName(zero)["orchestration_input"]; m.Percent != nil || m.Delta != nil {
		t.Fatalf("zero baseline = %+v, want n/a", m)
	}

	// Unknown baseline -> n/a.
	unk := Compare(CompareInput{
		Base:   DailyAggregate{Date: "2026-10-10", Orchestration: UnknownUsage()},
		Target: DailyAggregate{Date: "2026-10-11", Orchestration: KnownUsage(10, 0, 0, 0, 0)},
	})
	if m := compareByName(unk)["orchestration_input"]; m.Percent != nil || m.Delta != nil {
		t.Fatalf("unknown baseline = %+v, want n/a", m)
	}
}

// TestRenderCompare_TableAndJSON (F4) asserts n/a in the table and null in JSON.
func TestRenderCompare_TableAndJSON(t *testing.T) {
	cmp := Compare(CompareInput{
		Base:   DailyAggregate{Date: "2026-10-10", Orchestration: KnownUsage(0, 0, 0, 0, 0), Annotations: Annotations{Commits: []string{"c1"}}},
		Target: DailyAggregate{Date: "2026-10-11", Orchestration: KnownUsage(10, 0, 0, 0, 0), Annotations: Annotations{Commits: []string{"c2"}}},
	})
	var table bytes.Buffer
	if err := RenderCompareTable(&table, cmp); err != nil {
		t.Fatalf("table: %v", err)
	}
	if !strings.Contains(table.String(), "n/a") {
		t.Fatalf("table missing n/a:\n%s", table.String())
	}
	if !strings.Contains(table.String(), "base   2026-10-10") {
		t.Fatalf("table missing base annotation line:\n%s", table.String())
	}

	var js bytes.Buffer
	if err := RenderCompareJSON(&js, cmp); err != nil {
		t.Fatalf("json: %v", err)
	}
	var decoded struct {
		Metrics []struct {
			Name    string   `json:"name"`
			Delta   *int64   `json:"delta"`
			Percent *float64 `json:"percent"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	for _, m := range decoded.Metrics {
		if m.Name == "orchestration_input" {
			if m.Percent != nil || m.Delta != nil {
				t.Fatalf("zero baseline should be null: %+v", m)
			}
			return
		}
	}
	t.Fatal("orchestration_input not found")
}
