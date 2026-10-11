package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

type fakeSource struct {
	rec *workflow.Record
	err error
}

func (f fakeSource) GetDay(context.Context, string) (*workflow.Record, error) {
	return f.rec, f.err
}

func piRun(t *testing.T, name string) RunRecord {
	t.Helper()
	return parseWith(t, piAdapter{}, readTestdata(t, name))
}

// TestUsage_ConsumedTokensExcludesReasoning asserts components-only accounting:
// reasoning is never billed into consumed tokens.
func TestUsage_ConsumedTokensExcludesReasoning(t *testing.T) {
	u := KnownUsage(1, 2, 3, 4, 5)
	assertKnown(t, "consumed", u.ConsumedTokens(), 10)
	// One unknown component makes the whole consumed total UNKNOWN.
	u.CacheWrite = workflow.UnknownMeasurement()
	if u.ConsumedTokens().Known {
		t.Fatalf("consumed with unknown component should be UNKNOWN")
	}
}

// TestBuildAggregate_EditorialSeparateFromOrchestration asserts the two lanes are
// separate and that editorial components are copied from the record.
func TestBuildAggregate_EditorialSeparateFromOrchestration(t *testing.T) {
	run := piRun(t, "pi_partial_offline.jsonl")
	rec := &workflow.Record{
		SchemaVersion: 1,
		Date:          "2026-10-11",
		Usage: &workflow.EditorialUsage{
			Date:            "2026-10-11",
			Editorial:       workflow.KnownMeasurement(170),
			EditorialInput:  workflow.KnownMeasurement(100),
			EditorialOutput: workflow.KnownMeasurement(50),
			EditorialCache:  workflow.KnownMeasurement(20),
			EditorialRuns:   1,
			Invoked:         true,
		},
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{run}, fakeSource{rec: rec})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertKnown(t, "orch input", agg.Orchestration.Input, 24924)
	assertKnown(t, "orch reasoning", agg.Orchestration.Reasoning, 855)
	assertKnown(t, "editorial input", agg.Editorial.Input, 100)
	assertKnown(t, "editorial output", agg.Editorial.Output, 50)
	assertKnown(t, "editorial cacheRead", agg.Editorial.CacheRead, 20)
	assertKnown(t, "editorial-input-to-step", agg.EditorialInputToStep, 100)
	assertKnown(t, "budget spent", agg.Budget.Spent, 170)
	if agg.Budget.State != BudgetKnownAvailable {
		t.Fatalf("budget state = %q, want known_available", agg.Budget.State)
	}
	if agg.Budget.Target != 1000 {
		t.Fatalf("budget target = %d, want 1000", agg.Budget.Target)
	}
	if agg.Orchestration.Input.Value == agg.Editorial.Input.Value {
		t.Fatal("orchestration and editorial must be separate lanes")
	}
	if agg.RunCount != 1 || len(agg.RunIDs) != 1 {
		t.Fatalf("run ids = %+v count=%d", agg.RunIDs, agg.RunCount)
	}
}

// TestBuildAggregate_NoEditorialFallback asserts a record with NO usage (zero
// editorial CALLS) yields a Known(0) input-to-step but an UNKNOWN editorial total
// (never "zero total tokens").
func TestBuildAggregate_NoEditorialFallback(t *testing.T) {
	run := piRun(t, "pi_partial_offline.jsonl")

	// Case A: no record at all.
	agg, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{run}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertKnown(t, "input-to-step", agg.EditorialInputToStep, 0)
	if agg.Budget.Spent.Known {
		t.Fatalf("budget spent = %+v, want UNKNOWN", agg.Budget.Spent)
	}
	if agg.Budget.State != BudgetUnknownState {
		t.Fatalf("budget state = %q, want unknown", agg.Budget.State)
	}
	if agg.Editorial.Input.Known {
		t.Fatalf("editorial input = %+v, want UNKNOWN", agg.Editorial.Input)
	}

	// Case B: a record whose Usage exists but the editor never ran.
	rec := &workflow.Record{Usage: &workflow.EditorialUsage{Invoked: false, EditorialRuns: 0}}
	agg, err = BuildAggregate(context.Background(), "2026-10-11", []RunRecord{run}, fakeSource{rec: rec})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertKnown(t, "input-to-step (no calls)", agg.EditorialInputToStep, 0)
	if agg.Budget.Spent.Known {
		t.Fatalf("budget spent must be UNKNOWN when no editorial total is recorded")
	}
}

// TestBuildAggregate_PoisonedTotalTokensIgnored asserts a garbage provider
// totalTokens does not change the aggregate (byte-identical) and that
// ContextLength is never aggregated.
func TestBuildAggregate_PoisonedTotalTokensIgnored(t *testing.T) {
	clean := piRun(t, "pi_partial_offline.jsonl")
	poisoned := piRun(t, "pi_poisoned_total.jsonl")

	// The runs differ only in ContextLength (totalTokens).
	if clean.Calls[0].ContextLength.Value == poisoned.Calls[0].ContextLength.Value {
		t.Fatal("fixtures should differ in ContextLength")
	}

	aggClean, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{clean}, nil)
	if err != nil {
		t.Fatalf("aggregate clean: %v", err)
	}
	aggPoisoned, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{poisoned}, nil)
	if err != nil {
		t.Fatalf("aggregate poisoned: %v", err)
	}
	bClean, _ := json.Marshal(aggClean)
	bPoisoned, _ := json.Marshal(aggPoisoned)
	if !bytes.Equal(bClean, bPoisoned) {
		t.Fatalf("aggregate changed when only totalTokens changed:\n%s\n%s", bClean, bPoisoned)
	}
}

// TestAggregate_ContextLengthNotAnyField asserts ContextLength (provider
// totalTokens) is never aggregated: the marshaled aggregate carries no
// context-length field, and sum(totalTokens) is not any aggregate measurement.
// The poisoned fixture makes totalTokens deliberately inconsistent with the
// component sum so the two cannot coincide.
func TestAggregate_ContextLengthNotAnyField(t *testing.T) {
	run := piRun(t, "pi_poisoned_total.jsonl")
	var sumTotal int64
	for _, c := range run.Calls {
		if c.ContextLength.Known {
			sumTotal += c.ContextLength.Value
		}
	}
	if sumTotal == 0 {
		t.Fatal("fixture should carry totalTokens")
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{run}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	for name, m := range map[string]workflow.Measurement{
		"orch.input":      agg.Orchestration.Input,
		"orch.output":     agg.Orchestration.Output,
		"orch.cacheRead":  agg.Orchestration.CacheRead,
		"orch.cacheWrite": agg.Orchestration.CacheWrite,
		"orch.reasoning":  agg.Orchestration.Reasoning,
		"orch.consumed":   agg.Orchestration.ConsumedTokens(),
		"budget.spent":    agg.Budget.Spent,
	} {
		if m.Known && m.Value == sumTotal {
			t.Fatalf("%s equals sum(totalTokens)=%d; context length must never be aggregated", name, sumTotal)
		}
	}
	data, _ := json.Marshal(agg)
	if bytes.Contains(data, []byte("context_length")) {
		t.Fatalf("aggregate JSON carries a context_length field: %s", data)
	}
}

// TestBuildAggregate_RunIDsSortedUnion asserts the run id list is a sorted union.
func TestBuildAggregate_RunIDsSortedUnion(t *testing.T) {
	runs := []RunRecord{
		{RunID: "b", Usage: ZeroUsage(), Provenance: ProvenanceObserved},
		{RunID: "a", Usage: ZeroUsage(), Provenance: ProvenanceObserved},
		{RunID: "b", Usage: ZeroUsage(), Provenance: ProvenanceObserved},
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", runs, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(agg.RunIDs) != 2 || agg.RunIDs[0] != "a" || agg.RunIDs[1] != "b" {
		t.Fatalf("run ids = %+v, want [a b]", agg.RunIDs)
	}
	if agg.RunCount != 2 {
		t.Fatalf("run count = %d, want 2", agg.RunCount)
	}
}

// TestBuildAggregate_UnassignedLane (F3) asserts an unattributable call lands in
// Unassigned and is never folded into Orchestration.
func TestBuildAggregate_UnassignedLane(t *testing.T) {
	run := RunRecord{
		RunID:      "r1",
		Date:       "2026-10-11",
		Provenance: ProvenanceObserved,
		Calls: []CallRecord{
			{Category: CategoryOrchestration, Usage: KnownUsage(10, 1, 0, 0, 0)},
			{Category: CategoryUnassigned, Usage: KnownUsage(99, 9, 0, 0, 0)},
			{Category: CategoryUnassigned, Usage: KnownUsage(1, 1, 0, 0, 0)},
		},
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{run}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertKnown(t, "orchestration input", agg.Orchestration.Input, 10)
	assertKnown(t, "unassigned input", agg.Unassigned.Input, 100)
	if agg.UnassignedCalls != 2 {
		t.Fatalf("unassigned calls = %d, want 2", agg.UnassignedCalls)
	}
}

// TestBuildAggregate_Annotations (F4) asserts the sorted-union annotations.
func TestBuildAggregate_Annotations(t *testing.T) {
	runs := []RunRecord{
		{RunID: "a", Provenance: ProvenanceObserved, Commit: "c2", WorkflowVersion: "v2", ConfigMode: "offline", OptimizationChange: "x"},
		{RunID: "b", Provenance: ProvenanceObserved, Commit: "c1", WorkflowVersion: "v1", ConfigMode: "offline", OptimizationChange: "y"},
		{RunID: "c", Provenance: ProvenanceObserved},
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", runs, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertStrings(t, "commits", agg.Annotations.Commits, []string{"c1", "c2"})
	assertStrings(t, "workflow versions", agg.Annotations.WorkflowVersions, []string{"v1", "v2"})
	assertStrings(t, "config modes", agg.Annotations.ConfigModes, []string{"offline"})
	assertStrings(t, "optimization changes", agg.Annotations.OptimizationChanges, []string{"x", "y"})
}

// TestBuildAggregate_DurationTotals (F5) asserts known-only duration sums.
func TestBuildAggregate_DurationTotals(t *testing.T) {
	runs := []RunRecord{
		{RunID: "a", Provenance: ProvenanceObserved,
			EndToEnd:  Duration{Value: workflow.KnownMeasurement(100), Source: DurationTranscript},
			Active:    Duration{Value: workflow.KnownMeasurement(60), Source: DurationTranscript},
			HumanWait: Duration{Value: workflow.KnownMeasurement(40), Source: DurationTranscript}},
		{RunID: "b", Provenance: ProvenanceObserved,
			EndToEnd:  Duration{Value: workflow.KnownMeasurement(200), Source: DurationTranscript},
			Active:    Duration{Value: workflow.KnownMeasurement(150), Source: DurationTranscript},
			HumanWait: Duration{Value: workflow.KnownMeasurement(50), Source: DurationTranscript}},
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-11", runs, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	assertKnown(t, "endToEnd", agg.EndToEnd.Value, 300)
	assertKnown(t, "active", agg.Active.Value, 210)
	assertKnown(t, "humanWait", agg.HumanWait.Value, 90)
	if agg.EndToEnd.Source != DurationTranscript {
		t.Fatalf("source = %q, want transcript", agg.EndToEnd.Source)
	}

	// A single unknown contributor makes the sum UNKNOWN.
	runs[1].EndToEnd = Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
	agg, err = BuildAggregate(context.Background(), "2026-10-11", runs, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.EndToEnd.Value.Known {
		t.Fatalf("endToEnd = %+v, want UNKNOWN", agg.EndToEnd)
	}
}

func assertStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}
