package metrics

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// TestRenderJSON_StableOrder asserts schema_version is the first field and the
// output is stable.
func TestRenderJSON_StableOrder(t *testing.T) {
	agg := DailyAggregate{SchemaVersion: 1, Date: "2026-10-11", RunIDs: []string{}, Orchestration: UnknownUsage(), Editorial: UnknownUsage()}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, agg); err != nil {
		t.Fatalf("render json: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "{\n  \"schema_version\": 1") {
		t.Fatalf("schema_version is not the first field:\n%s", out)
	}
	var buf2 bytes.Buffer
	_ = RenderJSON(&buf2, agg)
	if buf.String() != buf2.String() {
		t.Fatal("RenderJSON is not deterministic")
	}
}

// TestRenderTable_DeclaredMarkerAndPartial asserts the declared-schema marker and
// that a partial run is rendered as partial, never completed.
func TestRenderTable_DeclaredMarkerAndPartial(t *testing.T) {
	run := RunRecord{RunID: "r1", Date: "2026-10-11", State: RunPartial, Provenance: ProvenanceDeclared,
		Calls: []CallRecord{{RunID: "r1"}}}
	agg := DailyAggregate{
		SchemaVersion: 1,
		Date:          "2026-10-11",
		Orchestration: KnownUsage(10, 2, 0, 0, 0),
		Editorial:     UnknownUsage(),
		Budget:        BudgetReport{Target: 1000, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState},
		RunIDs:        []string{"r1"},
		RunCount:      1,
		Provenance:    ProvenanceDeclared,
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, TableInput{Aggregate: agg, Runs: []RunRecord{run}}); err != nil {
		t.Fatalf("render table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "[declared schema]") {
		t.Fatalf("missing declared schema marker:\n%s", out)
	}
	if !strings.Contains(out, "partial=1") {
		t.Fatalf("missing partial state breakdown:\n%s", out)
	}
	if strings.Contains(out, "completed") {
		t.Fatalf("partial run rendered as completed:\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("unknown measurement not rendered as 'unknown':\n%s", out)
	}
}

// TestRenderTable_Columns asserts the fixed header is present.
func TestRenderTable_Columns(t *testing.T) {
	var buf bytes.Buffer
	agg := DailyAggregate{Date: "2026-10-11", RunIDs: []string{}}
	if err := RenderTable(&buf, TableInput{Aggregate: agg}); err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, col := range []string{"DATE", "RUNS", "CALLS", "INPUT", "OUTPUT", "CACHER", "CACHEW", "REASON", "UNASSIGNED-CALLS", "EDIT-IN-TO-STEP", "EDIT-TOTAL", "BUDGET", "E2E-MS", "ACTIVE-MS", "WAIT-MS", "STATES"} {
		if !strings.Contains(buf.String(), col) {
			t.Fatalf("missing column %q in:\n%s", col, buf.String())
		}
	}
}

// TestRenderJSONDocuments_Range (F6) asserts one valid JSON document: an object
// for a single day, an array for a range.
func TestRenderJSONDocuments_Range(t *testing.T) {
	single := []DailyAggregate{{SchemaVersion: 1, Date: "2026-10-11", RunIDs: []string{}}}
	var one bytes.Buffer
	if err := RenderJSONDocuments(&one, single); err != nil {
		t.Fatalf("single: %v", err)
	}
	if !strings.HasPrefix(one.String(), "{\n  \"schema_version\": 1") {
		t.Fatalf("single day must emit one object:\n%s", one.String())
	}

	rangeDocs := []DailyAggregate{
		{SchemaVersion: 1, Date: "2026-10-10", RunIDs: []string{}},
		{SchemaVersion: 1, Date: "2026-10-11", RunIDs: []string{}},
	}
	var many bytes.Buffer
	if err := RenderJSONDocuments(&many, rangeDocs); err != nil {
		t.Fatalf("range: %v", err)
	}
	var decoded []DailyAggregate
	if err := json.Unmarshal(many.Bytes(), &decoded); err != nil {
		t.Fatalf("range output is not one valid JSON array: %v\n%s", err, many.String())
	}
	if len(decoded) != 2 {
		t.Fatalf("decoded %d aggregates, want 2", len(decoded))
	}
}
