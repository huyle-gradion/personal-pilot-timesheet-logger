package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/metrics"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const piFixture = "../../internal/metrics/testdata/pi_partial_offline.jsonl"

// TestMetricsCLI_ImportAggregateReport exercises the full metrics command surface
// and asserts the editorial lane is read from the workflow record.
func TestMetricsCLI_ImportAggregateReport(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	storageDir := filepath.Join(dir, "records")

	seedRecord(t, storageDir, "2026-10-11", func(rec *workflow.Record) {
		rec.Usage = &workflow.EditorialUsage{
			Date:            "2026-10-11",
			Editorial:       workflow.KnownMeasurement(170),
			EditorialInput:  workflow.KnownMeasurement(100),
			EditorialOutput: workflow.KnownMeasurement(50),
			EditorialCache:  workflow.KnownMeasurement(20),
			EditorialRuns:   1,
			Invoked:         true,
		}
	})

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("import: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "imported run") {
		t.Fatalf("import output = %q", out)
	}

	// Reimport is a no-op.
	code, out, _ = runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK || !strings.Contains(out, "unchanged") {
		t.Fatalf("reimport: code=%d out=%q", code, out)
	}

	code, out, errOut = runArgs(t, "metrics", "aggregate", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--storage-dir", storageDir)
	if code != exitOK {
		t.Fatalf("aggregate: code=%d out=%q err=%q", code, out, errOut)
	}

	code, out, errOut = runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("report json: code=%d err=%q", code, errOut)
	}
	if !strings.HasPrefix(out, "{\n  \"schema_version\": 1") {
		t.Fatalf("json report schema_version not first:\n%s", out)
	}
	if !strings.Contains(out, "\"editorial_input_to_step\": {") || !strings.Contains(out, "\"value\": 100") {
		t.Fatalf("json report missing editorial input 100:\n%s", out)
	}
	if !strings.Contains(out, "\"budget\"") || !strings.Contains(out, "\"state\": \"known_available\"") {
		t.Fatalf("json report missing budget state:\n%s", out)
	}

	code, out, errOut = runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("report table: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "2026-10-11") || !strings.Contains(out, "partial=1") {
		t.Fatalf("table report missing date/state:\n%s", out)
	}
}

// TestMetricsCLI_UnknownProvider asserts a non-transcript file fails closed.
func TestMetricsCLI_UnknownProvider(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"not":"a transcript"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, _, errOut := runArgs(t, "metrics", "import", "--transcript", bad, "--metrics-dir", filepath.Join(dir, "m"))
	if code != exitError {
		t.Fatalf("unknown provider: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "unknown provider") {
		t.Fatalf("error = %q, want unknown provider", errOut)
	}
}

// TestMetricsCLI_ReportMissingAggregate asserts a missing aggregate is an error.
func TestMetricsCLI_ReportMissingAggregate(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", filepath.Join(dir, "m"))
	if code != exitError {
		t.Fatalf("missing aggregate: code=%d err=%q", code, errOut)
	}
}

// TestMetricsCLI_Usage asserts an empty metrics invocation is a usage error.
func TestMetricsCLI_Usage(t *testing.T) {
	code, _, errOut := runArgs(t, "metrics")
	if code != exitUsage {
		t.Fatalf("empty metrics: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "timesheet metrics") {
		t.Fatalf("usage not printed: %q", errOut)
	}
}

// TestMetricsCLI_RunStates (F5) asserts `metrics run` records a live monotonic run
// with the exit-code-derived state and mirrors the child exit code.
func TestMetricsCLI_RunStates(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	cases := []struct {
		script    string
		wantState string
		wantCode  int
	}{
		{"exit 0", "completed", 0},
		{"exit 3", "blocked", 3},
		{"exit 1", "failed", 1},
	}
	for _, tc := range cases {
		code, out, errOut := runArgs(t, "metrics", "run", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--", "sh", "-c", tc.script)
		if code != tc.wantCode {
			t.Fatalf("script %q: code=%d want %d (err=%q)", tc.script, code, tc.wantCode, errOut)
		}
		if !strings.Contains(out, "state="+tc.wantState) {
			t.Fatalf("script %q: output %q missing state=%s", tc.script, out, tc.wantState)
		}
	}

	store, err := metrics.NewStore(metricsDir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	runs, err := store.ListRuns("2026-10-11")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != len(cases) {
		t.Fatalf("live runs = %d, want %d", len(runs), len(cases))
	}
	for i := range runs {
		if runs[i].EndToEnd.Source != metrics.DurationMonotonic {
			t.Fatalf("run %s end_to_end source = %q, want monotonic", runs[i].RunID, runs[i].EndToEnd.Source)
		}
		if runs[i].HarnessStart.Value.Known {
			t.Fatalf("run %s harness start = %+v, want UNKNOWN", runs[i].RunID, runs[i].HarnessStart)
		}
	}
}

// writeSessionFixture writes a minimal pi transcript with the given session id.
func writeSessionFixture(t *testing.T, dir, sessionID string) string {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	content := `{"type":"session","id":"` + sessionID + `","cwd":"/repo","timestamp":"2026-10-11T05:15:03.251Z","version":3}` + "\n" +
		`{"type":"message","message":{"role":"assistant","timestamp":1791695713126,"content":[{"type":"toolCall","name":"bash","arguments":{"command":"timesheet review"}}],"usage":{"input":100,"output":10,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":110}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write session fixture: %v", err)
	}
	return path
}

// TestMetricsCLI_RunWriteFailureKeepsChildExit (N1) asserts a post-run metrics
// write failure does NOT override a successful child's exit code: the wrapper
// warns and returns the child's code, and no run file is written.
func TestMetricsCLI_RunWriteFailureKeepsChildExit(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	runsDir := filepath.Join(metricsDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		t.Fatalf("mkdir runs: %v", err)
	}
	// Read-only runs dir: NewStore's MkdirAll still succeeds (the dir exists), but
	// WriteRun's temp-file create fails.
	if err := os.Chmod(runsDir, 0o555); err != nil {
		t.Fatalf("chmod runs: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(runsDir, 0o755) })

	code, out, errOut := runArgs(t, "metrics", "run", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--", "sh", "-c", "exit 0")
	if code != exitOK {
		t.Fatalf("successful child + failed metrics write must return the child code 0, got %d (stderr=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning") {
		t.Fatalf("expected a warning on the failed metrics write, got stderr=%q", errOut)
	}
	if strings.Contains(out, "live run") {
		t.Fatalf("a run must not be reported when the write failed: %q", out)
	}
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("read runs dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a run file was written despite the failure: %v", entries)
	}
}

// TestMetricsCLI_Compare (F4) asserts the compare command prints annotations and
// n/a for a zero baseline.
func TestMetricsCLI_Compare(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	storageDir := filepath.Join(dir, "rec")

	// Distinct sessions so both days have a real run (importing the same session
	// twice is a correct no-op).
	base := writeSessionFixture(t, dir, "sess-base")
	target := writeSessionFixture(t, dir, "sess-target")
	if code, _, e := runArgs(t, "metrics", "import", "--transcript", base, "--date", "2026-10-10", "--metrics-dir", metricsDir, "--commit", "c1", "--optimization-change", "baseline"); code != exitOK {
		t.Fatalf("import base: %s", e)
	}
	if code, _, e := runArgs(t, "metrics", "import", "--transcript", target, "--date", "2026-10-11", "--metrics-dir", metricsDir, "--commit", "c2", "--optimization-change", "narrow"); code != exitOK {
		t.Fatalf("import target: %s", e)
	}
	for _, d := range []string{"2026-10-09", "2026-10-10", "2026-10-11"} {
		if code, _, e := runArgs(t, "metrics", "aggregate", "--date", d, "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
			t.Fatalf("aggregate %s: %s", d, e)
		}
	}

	// Non-zero baseline with identical tokens -> 0.0% and annotation lines.
	code, out, errOut := runArgs(t, "metrics", "compare", "--base", "2026-10-10", "--target", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("compare: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "orchestration_input") || !strings.Contains(out, "0.0%") {
		t.Fatalf("compare table missing metrics/percent:\n%s", out)
	}
	if !strings.Contains(out, "commit=c1") || !strings.Contains(out, "commit=c2") {
		t.Fatalf("compare table missing annotations:\n%s", out)
	}

	// Zero baseline -> n/a.
	code, out, errOut = runArgs(t, "metrics", "compare", "--base", "2026-10-09", "--target", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("compare zero baseline: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "n/a") {
		t.Fatalf("zero baseline should print n/a:\n%s", out)
	}

	// JSON form is one valid document.
	code, out, errOut = runArgs(t, "metrics", "compare", "--base", "2026-10-09", "--target", "2026-10-11", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("compare json: code=%d err=%q", code, errOut)
	}
	var doc metrics.Comparison
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("compare json parse: %v\n%s", err, out)
	}
}

// TestMetricsCLI_ReportRangeJSON (F6) asserts a range emits ONE JSON array and a
// single date emits one object.
func TestMetricsCLI_ReportRangeJSON(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	storageDir := filepath.Join(dir, "rec")
	for _, d := range []struct{ date, session string }{
		{"2026-10-10", "sess-a"},
		{"2026-10-11", "sess-b"},
	} {
		path := writeSessionFixture(t, dir, d.session)
		if code, _, e := runArgs(t, "metrics", "import", "--transcript", path, "--date", d.date, "--metrics-dir", metricsDir); code != exitOK {
			t.Fatalf("import %s: %s", d.date, e)
		}
		if code, _, e := runArgs(t, "metrics", "aggregate", "--date", d.date, "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
			t.Fatalf("aggregate %s: %s", d.date, e)
		}
	}

	code, out, errOut := runArgs(t, "metrics", "report", "--from", "2026-10-10", "--to", "2026-10-11", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("range json: code=%d err=%q", code, errOut)
	}
	var arr []metrics.DailyAggregate
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("range output is not one JSON array: %v\n%s", err, out)
	}
	if len(arr) != 2 {
		t.Fatalf("range array length = %d, want 2", len(arr))
	}

	code, out, _ = runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("single json: code=%d", code)
	}
	if !strings.HasPrefix(out, "{\n  \"schema_version\": 1") {
		t.Fatalf("single date must emit one object:\n%s", out)
	}
}
