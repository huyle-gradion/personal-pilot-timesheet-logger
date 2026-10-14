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

// writeCatalog writes a models-store.json fixture into dir and returns its path.
// It is written into the test's temp dir; the real ~/.pi catalog is never touched.
func writeCatalog(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "models-store.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

// readStoredSession reads and parses a stored session file, returning both the
// decoded record and its raw JSON bytes.
func readStoredSession(t *testing.T, metricsDir, sessionID string) (metrics.SessionRecord, []byte) {
	t.Helper()
	path := filepath.Join(metricsDir, "sessions", sessionID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read session file %s: %v", path, err)
	}
	var rec metrics.SessionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("parse session %s: %v", path, err)
	}
	return rec, raw
}

// sessionRow returns the rendered table line beginning with sessionID.
func sessionRow(t *testing.T, out, sessionID string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, sessionID) {
			return line
		}
	}
	t.Fatalf("no row for session %q in:\n%s", sessionID, out)
	return ""
}

// TestCLI_MetricsImportWritesSession asserts `metrics import --date --model-catalog`
// writes a session file whose JSON carries the day association and a resolved price.
func TestCLI_MetricsImportWritesSession(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	catalog := writeCatalog(t, dir, `{"test-provider":{"models":[{"id":"test-model","cost":{"input":1.5,"output":3,"cacheRead":0.5,"cacheWrite":1}}]}}`)

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--model-catalog", catalog, "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("import: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "session test-session-0001") {
		t.Fatalf("session summary missing: %q", out)
	}

	rec, raw := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.LoggedDate != "2026-10-11" {
		t.Fatalf("logged_date = %q, want 2026-10-11", rec.LoggedDate)
	}
	if rec.Association != metrics.AssociationImportFlag {
		t.Fatalf("association = %q, want %q", rec.Association, metrics.AssociationImportFlag)
	}
	if rec.PriceSource != metrics.PriceSourceCatalog {
		t.Fatalf("price_source = %q, want catalog (raw=%s)", rec.PriceSource, raw)
	}
	if rec.Cost == nil || rec.Price.Input == nil {
		t.Fatalf("resolved price/cost missing: cost=%v price=%+v", rec.Cost, rec.Price)
	}
	if !strings.Contains(string(raw), `"logged_date": "2026-10-11"`) ||
		!strings.Contains(string(raw), `"association": "import_flag"`) {
		t.Fatalf("raw session missing day association:\n%s", raw)
	}
}

// TestCLI_MetricsImportUnknownModelPriceIsNull asserts a model ABSENT from the
// catalog yields price_source=unknown, cost=null, and four null rate fields —
// never 0.
func TestCLI_MetricsImportUnknownModelPriceIsNull(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	// A catalog that does NOT list test-provider/test-model.
	catalog := writeCatalog(t, dir, `{"other-provider":{"models":[{"id":"other-model","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`)

	code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--model-catalog", catalog, "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}

	rec, raw := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.PriceSource != metrics.PriceSourceUnknown {
		t.Fatalf("session price_source = %q, want unknown", rec.PriceSource)
	}
	if rec.Cost != nil {
		t.Fatalf("session cost = %v, want nil", *rec.Cost)
	}
	for name, p := range map[string]*float64{
		"input":       rec.Price.Input,
		"output":      rec.Price.Output,
		"cache_read":  rec.Price.CacheRead,
		"cache_write": rec.Price.CacheWrite,
	} {
		if p != nil {
			t.Fatalf("session rate %s = %v, want nil", name, *p)
		}
	}
	for i := range rec.Models {
		m := rec.Models[i]
		if m.Cost != nil || m.PriceSource != metrics.PriceSourceUnknown {
			t.Fatalf("model %s/%s: cost=%v price_source=%q, want nil/unknown", m.Provider, m.Model, m.Cost, m.PriceSource)
		}
	}
	for _, want := range []string{`"price_source": "unknown"`, `"cost": null`, `"input": null`, `"output": null`, `"cache_read": null`, `"cache_write": null`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("raw session missing %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"cost": 0`) {
		t.Fatalf("unknown cost rendered as 0:\n%s", raw)
	}
}

// TestCLI_MetricsImportMissingCatalogWarnsAndSucceeds asserts a nonexistent
// --model-catalog exits 0, warns on stderr, and still writes an unknown-price
// session.
func TestCLI_MetricsImportMissingCatalogWarnsAndSucceeds(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	missing := filepath.Join(dir, "does-not-exist.json")

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--model-catalog", missing, "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("a missing catalog must not change the exit code: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "warning") {
		t.Fatalf("expected a warning on stderr, got %q", errOut)
	}
	if !strings.Contains(out, "session test-session-0001") {
		t.Fatalf("session summary missing: %q", out)
	}
	rec, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.PriceSource != metrics.PriceSourceUnknown || rec.Cost != nil {
		t.Fatalf("session should have an unknown price: price_source=%q cost=%v", rec.PriceSource, rec.Cost)
	}
}

// TestCLI_MetricsSessionsTableRendersUnknown asserts the sessions table prints
// "unknown" for an unknown cost, never 0.
func TestCLI_MetricsSessionsTableRendersUnknown(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	catalog := writeCatalog(t, dir, `{"other-provider":{"models":[{"id":"other-model","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`)
	if code, _, e := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--model-catalog", catalog, "--metrics-dir", metricsDir); code != exitOK {
		t.Fatalf("import: %s", e)
	}

	code, out, errOut := runArgs(t, "metrics", "sessions", "--metrics-dir", metricsDir, "--format", "table")
	if code != exitOK {
		t.Fatalf("sessions: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "SESSION-ID") {
		t.Fatalf("table header missing:\n%s", out)
	}
	row := sessionRow(t, out, "test-session-0001")
	fields := strings.Fields(row)
	last := fields[len(fields)-1]
	if last != "unknown" {
		t.Fatalf("cost column = %q, want unknown (row=%q)", last, row)
	}
	if last == "0" {
		t.Fatalf("unknown cost rendered as 0: %q", row)
	}
}

// TestCLI_MetricsSessionsJSON asserts `metrics sessions --format json` emits the
// session fields.
func TestCLI_MetricsSessionsJSON(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	catalog := writeCatalog(t, dir, `{"test-provider":{"models":[{"id":"test-model","cost":{"input":1,"output":2,"cacheRead":0.5,"cacheWrite":0}}]}}`)
	if code, _, e := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--model-catalog", catalog, "--metrics-dir", metricsDir); code != exitOK {
		t.Fatalf("import: %s", e)
	}

	code, out, errOut := runArgs(t, "metrics", "sessions", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("sessions json: code=%d err=%q", code, errOut)
	}
	var sessions []metrics.SessionRecord
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("sessions json parse: %v\n%s", err, out)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != "test-session-0001" {
		t.Fatalf("session id = %q", sessions[0].SessionID)
	}
	for _, want := range []string{`"session_id"`, `"logged_date"`, `"price_source"`, `"cache_hit_pct"`, `"total_tokens"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %s:\n%s", want, out)
		}
	}
}
