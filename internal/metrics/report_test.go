package metrics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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

// sessionTokensHeader is the EXACT table header after #126: the new SESS-TOKENS
// column (width 12) sits after SESS-COST and before UNASSOC, and every pre-existing
// column keeps its order, width, and bytes. STATES stays last and unpadded.
const sessionTokensHeader = "DATE         RUNS  CALLS  INPUT      OUTPUT     CACHER     CACHEW     REASON     " +
	"UNASSIGNED-CALLS EDIT-IN-TO-STEP  EDIT-TOTAL   BUDGET           E2E-MS    ACTIVE-MS   WAIT-MS   " +
	"SESSIONS SESS-CACHEHIT SESS-COST    SESS-TOKENS  UNASSOC  STATES\n"

// TestRenderTable_SessionTokensColumn (D1/AC1) asserts the honest session-tokens
// column: the header layout is byte-preserved around it, a known day renders the
// day's summed session totals, a day with no sessions renders 0 (the only honest
// zero), and any unknown session total makes the whole cell UNKNOWN.
func TestRenderTable_SessionTokensColumn(t *testing.T) {
	render := func(agg DailyAggregate) []string {
		t.Helper()
		var buf bytes.Buffer
		if err := RenderTable(&buf, TableInput{Aggregate: agg}); err != nil {
			t.Fatalf("render table: %v", err)
		}
		return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	}
	cell := func(t *testing.T, lines []string, column string) string {
		t.Helper()
		if len(lines) < 2 {
			t.Fatalf("table too short: %v", lines)
		}
		header := strings.Fields(lines[0])
		row := strings.Fields(lines[1])
		for i, h := range header {
			if h != column {
				continue
			}
			if i >= len(row) {
				t.Fatalf("%s cell missing from row %q", column, lines[1])
			}
			return row[i]
		}
		t.Fatalf("column %q missing from header %q", column, lines[0])
		return ""
	}

	// 1. Zero sessions: a genuine zero (beside SESSIONS 0), never unknown.
	empty := DailyAggregate{
		SchemaVersion: SchemaVersion, Date: "2026-10-11", RunIDs: []string{}, SessionIDs: []string{},
		SessionTotalTokens: workflow.KnownMeasurement(0),
		Budget:             BudgetReport{Target: BudgetTargetTokens, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState},
	}
	lines := render(empty)
	if lines[0]+"\n" != sessionTokensHeader {
		t.Fatalf("header bytes changed:\n got: %q\nwant: %q", lines[0]+"\n", sessionTokensHeader)
	}
	if got := cell(t, lines, "SESSIONS"); got != "0" {
		t.Fatalf("SESSIONS = %q, want 0", got)
	}
	if got := cell(t, lines, "SESS-TOKENS"); got != "0" {
		t.Fatalf("SESS-TOKENS = %q, want the honest zero beside SESSIONS 0", got)
	}

	// 2. A day whose sessions carry known totals: the SUM of the session lane.
	known := DailyAggregate{
		SchemaVersion: SchemaVersion, Date: "2026-10-11", RunIDs: []string{}, SessionIDs: []string{"a", "b"},
		SessionCount: 2, SessionTotalTokens: workflow.KnownMeasurement(116 + 230),
		Budget: BudgetReport{Target: BudgetTargetTokens, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState},
	}
	if got := cell(t, render(known), "SESS-TOKENS"); got != "346" {
		t.Fatalf("SESS-TOKENS = %q, want 346 (the summed session totals)", got)
	}

	// 3. One unknown session total makes the day's figure UNKNOWN, never a partial
	// sum: applySessions folds with the unknown-propagating rule.
	agg := DailyAggregate{SchemaVersion: SchemaVersion, Date: "2026-10-11", RunIDs: []string{},
		Budget: BudgetReport{Target: BudgetTargetTokens, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState}}
	applySessions(&agg, []SessionRecord{
		{SessionID: "a", TotalTokens: workflow.KnownMeasurement(116)},
		{SessionID: "b", TotalTokens: workflow.UnknownMeasurement()},
	})
	if got := cell(t, render(agg), "SESS-TOKENS"); got != "unknown" {
		t.Fatalf("SESS-TOKENS = %q, want unknown when any session total is unknown", got)
	}
	// The other session columns keep their own (separate) meanings.
	if got := cell(t, render(agg), "SESS-CACHEHIT"); got != "unknown" {
		t.Fatalf("SESS-CACHEHIT = %q, want unknown", got)
	}
}

// TestRenderTable_SessionTokensAndOrchestrationStaySeparate (D1) asserts the new
// column is the SESSION lane and is never the step-attributed Orchestration sums:
// a day with orchestration tokens and no sessions reads SESSIONS 0 / SESS-TOKENS 0
// while the Orchestration columns keep their own numbers.
func TestRenderTable_SessionTokensAndOrchestrationStaySeparate(t *testing.T) {
	agg := DailyAggregate{
		SchemaVersion: SchemaVersion, Date: "2026-10-11", RunIDs: []string{},
		SessionIDs: []string{}, Orchestration: KnownUsage(1000, 100, 0, 0, 0),
		SessionTotalTokens: workflow.KnownMeasurement(0),
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, TableInput{Aggregate: agg}); err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
	for i, h := range header {
		if h == "SESS-TOKENS" && row[i] != "0" {
			t.Fatalf("SESS-TOKENS = %q, want 0: no tokens are attributed to the session lane", row[i])
		}
		if h == "INPUT" && row[i] != "1000" {
			t.Fatalf("INPUT = %q, want the orchestration 1000 untouched", row[i])
		}
	}
}

// TestReadAggregateFile_LegacyBlobHasUnknownSessionTokens (AC1b) asserts a
// pre-METRICS-2 aggregate — the JSON blob WITHOUT the session_total_tokens key —
// decodes with an UNKNOWN session total (no SchemaVersion bump) and renders
// `unknown`, never 0. The blob is pinned literally so the absence of the key is what
// is tested.
func TestReadAggregateFile_LegacyBlobHasUnknownSessionTokens(t *testing.T) {
	const legacy = `{
  "schema_version": 1,
  "date": "2026-10-10",
  "orchestration": {"input": {"known": true, "value": 1000}, "output": {"known": true, "value": 10}, "cache_read": {"known": true, "value": 0}, "cache_write": {"known": true, "value": 0}, "reasoning": {"known": true, "value": 0}},
  "editorial": {"input": {"known": false, "value": 0}, "output": {"known": false, "value": 0}, "cache_read": {"known": false, "value": 0}, "cache_write": {"known": false, "value": 0}, "reasoning": {"known": false, "value": 0}},
  "unassigned": {"input": {"known": true, "value": 0}, "output": {"known": true, "value": 0}, "cache_read": {"known": true, "value": 0}, "cache_write": {"known": true, "value": 0}, "reasoning": {"known": true, "value": 0}},
  "editorial_runs": 0,
  "editorial_invoked": false,
  "editorial_input_to_step": {"known": true, "value": 0},
  "budget": {"target": 1000, "spent": {"known": false, "value": 0}, "state": "unknown"},
  "end_to_end": {"value": {"known": false, "value": 0}, "source": "unknown"},
  "active": {"value": {"known": false, "value": 0}, "source": "unknown"},
  "human_wait": {"value": {"known": false, "value": 0}, "source": "unknown"},
  "unassigned_calls": 0,
  "annotations": {"commits": [], "workflow_versions": [], "config_modes": [], "optimization_changes": []},
  "run_ids": ["r1"],
  "run_count": 1,
  "provenance": "observed"
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2026-10-10.json"), []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy aggregate: %v", err)
	}
	agg, err := ReadAggregateFile(dir, "2026-10-10")
	if err != nil {
		t.Fatalf("read legacy aggregate: %v", err)
	}
	if agg.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %d, want %d (MUST NOT be bumped)", agg.SchemaVersion, SchemaVersion)
	}
	if agg.SessionTotalTokens.Known {
		t.Fatalf("legacy aggregate session total = %+v, want UNKNOWN (the key is absent)", agg.SessionTotalTokens)
	}
	if agg.SessionCount != 0 {
		t.Fatalf("legacy aggregate session count = %d, want 0", agg.SessionCount)
	}

	var buf bytes.Buffer
	if err := RenderTable(&buf, TableInput{Aggregate: agg}); err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
	for i, h := range header {
		if h != "SESS-TOKENS" {
			continue
		}
		if row[i] != "unknown" {
			t.Fatalf("legacy SESS-TOKENS = %q, want unknown, never 0", row[i])
		}
	}
}

// TestRenderTable_SynthesizedFootnote (F4) asserts the DAY-level label for a
// synthesized total: one footnote after the batch when the day's session lane
// includes lower bounds, and NO footnote when it does not. The aggregate JSON keeps
// no such key — the note is a rendering concern on TableInput.
func TestRenderTable_SynthesizedFootnote(t *testing.T) {
	agg := DailyAggregate{
		SchemaVersion: SchemaVersion, Date: "2026-10-16", RunIDs: []string{}, SessionIDs: []string{"a"},
		SessionCount: 1, SessionTotalTokens: workflow.KnownMeasurement(5020),
		Budget: BudgetReport{Target: BudgetTargetTokens, Spent: workflow.UnknownMeasurement(), State: BudgetUnknownState},
	}
	const note = "[synthesized] 1 session total(s) on this day include synthesized lower bounds (total_source=synthesized; stamped by: metrics import --declare-total)"

	var withBuf bytes.Buffer
	if err := RenderTable(&withBuf, TableInput{Aggregate: agg, SynthesizedSessions: 1}); err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(withBuf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + row + ONE footnote, got %d lines:\n%s", len(lines), withBuf.String())
	}
	if lines[2] != note {
		t.Fatalf("footnote = %q, want %q", lines[2], note)
	}
	if !strings.Contains(lines[0], "SESS-TOKENS") {
		t.Fatalf("the footnote must follow the table, not replace it:\n%s", withBuf.String())
	}

	var withoutBuf bytes.Buffer
	if err := RenderTable(&withoutBuf, TableInput{Aggregate: agg}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(withoutBuf.String(), "[synthesized]") {
		t.Fatalf("a day with no synthesized total must carry no footnote:\n%s", withoutBuf.String())
	}

	// The JSON is unchanged: no key is added for the note.
	var js bytes.Buffer
	if err := RenderJSON(&js, agg); err != nil {
		t.Fatalf("render json: %v", err)
	}
	if strings.Contains(js.String(), "synthesized_sessions") {
		t.Fatalf("the footnote must not add a JSON key:\n%s", js.String())
	}
}
