package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
	code, _, errOut := runArgs(t, "metrics", "import", "--transcript", bad, "--date", "2026-10-11", "--metrics-dir", filepath.Join(dir, "m"))
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

// readOnlyStoredSession reads the ONE session stored in metricsDir. It is used
// where the session id is a sha256 fallback whose filename sanitization the test does
// not want to re-derive.
func readOnlyStoredSession(t *testing.T, metricsDir string) (metrics.SessionRecord, []byte) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(metricsDir, "sessions"))
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("stored sessions = %d, want 1", len(entries))
	}
	return readStoredSession(t, metricsDir, strings.TrimSuffix(entries[0].Name(), ".json"))
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

// TestMetricsCLI_ProviderMismatchRefused (R6, fail-before) asserts a forced wrong
// provider against another REGISTERED provider's transcript refuses with
// ErrProviderMismatch and writes NOTHING: before the guard it wrote a cancelled
// phantom run.
func TestMetricsCLI_ProviderMismatchRefused(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	fixture := "../../internal/metrics/testdata/claude_declared.jsonl"

	code, out, errOut := runArgs(t, "metrics", "import",
		"--provider", "pi", "--transcript", fixture,
		"--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitError {
		t.Fatalf("forced provider mismatch: code=%d out=%q err=%q, want exit %d", code, out, errOut, exitError)
	}
	if !strings.Contains(errOut, `detected "claude"`) {
		t.Fatalf("error = %q, want it to name the detected provider claude", errOut)
	}
	if entries, err := os.ReadDir(filepath.Join(metricsDir, "runs")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused import must write no run file, found %d: %v", len(entries), entries)
	}
}

// antigravityFixture is the SYNTHETIC antigravity transcript: invented values with
// the real field names; the owner's real transcript is never copied or referenced.
const antigravityFixture = "../../internal/metrics/testdata/antigravity_synthetic.jsonl"

// TestCLI_MetricsImportAntigravityHonestUnknowns asserts `--provider antigravity`
// imports the synthetic transcript and prints honest unknowns for the values the
// shape cannot supply (total tokens, cache-hit, model, price, cost), and that the
// report path prints no declared-schema marker (OBSERVED provenance).
func TestCLI_MetricsImportAntigravityHonestUnknowns(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	storageDir := filepath.Join(dir, "records")

	code, out, errOut := runArgs(t, "metrics", "import",
		"--provider", "antigravity", "--transcript", antigravityFixture,
		"--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("import: code=%d out=%q err=%q", code, out, errOut)
	}
	for _, want := range []string{
		"imported run", "(antigravity, 2026-10-16, 3 call(s)", "state=unknown",
		"session sha256:", "model=unknown", "total_tokens=unknown",
		"cache_hit=unknown", "price=unknown", "cost=unknown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("import output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "state=cancelled") {
		t.Fatalf("a complete antigravity transcript must not read cancelled:\n%s", out)
	}

	code, out, errOut = runArgs(t, "metrics", "aggregate",
		"--date", "2026-10-16", "--metrics-dir", metricsDir, "--storage-dir", storageDir)
	if code != exitOK {
		t.Fatalf("aggregate: code=%d out=%q err=%q", code, out, errOut)
	}
	code, out, errOut = runArgs(t, "metrics", "report",
		"--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("report: code=%d out=%q err=%q", code, out, errOut)
	}
	if strings.Contains(out, "[declared schema]") {
		t.Fatalf("observed antigravity report printed the declared-schema marker:\n%s", out)
	}
}

// TestCLI_MetricsImportAntigravityZeroCallRefused asserts a detected but
// usage-free antigravity transcript refuses instead of writing an empty record.
func TestCLI_MetricsImportAntigravityZeroCallRefused(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	zero := filepath.Join(dir, "zero.jsonl")
	content := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-16T09:00:00Z","content":"INVENTED-ONLY-TEXT"}` + "\n"
	if err := os.WriteFile(zero, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	code, out, errOut := runArgs(t, "metrics", "import",
		"--provider", "antigravity", "--transcript", zero,
		"--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitError {
		t.Fatalf("zero-call antigravity: code=%d out=%q err=%q, want exit %d", code, out, errOut, exitError)
	}
	if !strings.Contains(errOut, "unreadable") {
		t.Fatalf("error = %q, want the unreadable refusal", errOut)
	}
	if entries, err := os.ReadDir(filepath.Join(metricsDir, "runs")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused import must write no run file, found %d: %v", len(entries), entries)
	}
}

// TestCLI_MetricsImportAntigravityAgainstClaudeRefused asserts a forced
// antigravity provider against another provider's transcript refuses naming the
// detected adapter.
func TestCLI_MetricsImportAntigravityAgainstClaudeRefused(t *testing.T) {
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "m")
	fixture := "../../internal/metrics/testdata/claude_declared.jsonl"

	code, out, errOut := runArgs(t, "metrics", "import",
		"--provider", "antigravity", "--transcript", fixture,
		"--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitError {
		t.Fatalf("forced provider mismatch: code=%d out=%q err=%q, want exit %d", code, out, errOut, exitError)
	}
	if !strings.Contains(errOut, `requested "antigravity"`) || !strings.Contains(errOut, `detected "claude"`) {
		t.Fatalf("error = %q, want requested antigravity and detected claude", errOut)
	}
	if entries, err := os.ReadDir(filepath.Join(metricsDir, "runs")); err == nil && len(entries) != 0 {
		t.Fatalf("a refused import must write no run file, found %d: %v", len(entries), entries)
	}
}

// --- GAP-13 (#126): config-driven catalog, declarations, declared totals -------

// hermeticHome points $HOME at a temp dir, so BOTH the default config document and
// the removed ~/.pi/agent/models-store.json arm resolve inside the test. Nothing
// outside the temp dir is ever read or written.
func hermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_MODEL_CATALOG", "")
	return home
}

// plantConfig writes a config document at the DEFAULT path under home.
func plantConfig(t *testing.T, home string, cfg cliConfig) string {
	t.Helper()
	dir := filepath.Join(home, ".timesheet", "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	path := filepath.Join(dir, "timesheet.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestCLI_MetricsCatalogResolution_FlagThenConfigThenNothing (D3/AC3) asserts the
// de-pi'd resolution: the flag wins, then the config's metrics.model_catalog, then
// NOTHING. The two removed arms are proved removed by PLANTING both and asserting a
// bare import still prices nothing.
func TestCLI_MetricsCatalogResolution_FlagThenConfigThenNothing(t *testing.T) {
	home := hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	// Both removed arms carry a catalog that WOULD price test-provider/test-model.
	planted := writeCatalog(t, dir, `{"test-provider":{"models":[{"id":"test-model","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`)
	t.Setenv("PI_MODEL_CATALOG", planted)
	piDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(piDir, 0o700); err != nil {
		t.Fatalf("create ~/.pi/agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "models-store.json"), []byte(`{"test-provider":{"models":[{"id":"test-model","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`), 0o600); err != nil {
		t.Fatalf("plant models-store.json: %v", err)
	}

	// 1. NEITHER arm is used: no flag, no config section ⇒ NO catalog.
	code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	rec, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.PriceSource != metrics.PriceSourceUnknown || rec.Cost != nil {
		t.Fatalf("both removed catalog arms must be gone: price_source=%q cost=%v (env PI_MODEL_CATALOG + ~/.pi/agent/models-store.json were planted)", rec.PriceSource, rec.Cost)
	}
	if !strings.Contains(errOut, "no model catalog configured") {
		t.Fatalf("expected the no-catalog warning, got %q", errOut)
	}

	// 2. The CONFIG arm prices the session. Each step writes into a FRESH store: a
	// price-only difference is not an idempotency term (the declaration terms are),
	// so the same store would correctly no-op the rewrite.
	cfgMetricsDir := filepath.Join(t.TempDir(), "metrics")
	cfgDir := t.TempDir()
	cfgCatalog := writeCatalog(t, cfgDir, `{"test-provider":{"models":[{"id":"test-model","cost":{"input":2,"output":2,"cacheRead":2,"cacheWrite":2}}]}}`)
	cfgPath := plantConfig(t, home, cliConfig{Metrics: metricsConfig{ModelCatalog: cfgCatalog}})
	code, _, errOut = runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", cfgMetricsDir)
	if code != exitOK {
		t.Fatalf("import with config: code=%d err=%q", code, errOut)
	}
	rec, _ = readStoredSession(t, cfgMetricsDir, "test-session-0001")
	if rec.PriceSource != metrics.PriceSourceCatalog || rec.Price.Input == nil || *rec.Price.Input != 2 {
		t.Fatalf("the config's metrics.model_catalog must price the session: source=%q input=%v", rec.PriceSource, rec.Price.Input)
	}

	// 3. The FLAG outranks the config.
	flagCatalog := writeCatalog(t, t.TempDir(), `{"test-provider":{"models":[{"id":"test-model","cost":{"input":7,"output":7,"cacheRead":7,"cacheWrite":7}}]}}`)
	flagMetricsDir := filepath.Join(t.TempDir(), "metrics")
	code, _, errOut = runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", flagMetricsDir, "--config", cfgPath, "--model-catalog", flagCatalog)
	if code != exitOK {
		t.Fatalf("import with flag: code=%d err=%q", code, errOut)
	}
	rec, _ = readStoredSession(t, flagMetricsDir, "test-session-0001")
	if rec.Price.Input == nil || *rec.Price.Input != 7 {
		t.Fatalf("the flag must outrank the config: input=%v", rec.Price.Input)
	}
}

// TestCLI_MetricsConfig_TolerantReadNeverChangesExitCode (D3) asserts a broken
// config document only WARNS on the metrics import path: metrics work is optional,
// so it never inherits the fatal config-load exit.
func TestCLI_MetricsConfig_TolerantReadNeverChangesExitCode(t *testing.T) {
	home := hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	cfgDir := filepath.Join(home, ".timesheet", "config")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	bad := filepath.Join(cfgDir, "timesheet.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write bad config: %v", err)
	}

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("an unparseable config must NOT change the exit code: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "warning") || !strings.Contains(errOut, "unparseable") {
		t.Fatalf("expected an unparseable-config warning, got %q", errOut)
	}
	if !strings.Contains(out, "session test-session-0001") {
		t.Fatalf("the session must still be written: %q", out)
	}

	// An INVALID metrics section warns too (never a fatal load).
	invalid := plantConfig(t, home, cliConfig{Metrics: metricsConfig{ModelCatalog: "https://example.com/models.json"}})
	code, _, errOut = runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", metricsDir, "--config", invalid)
	if code != exitOK {
		t.Fatalf("an invalid metrics section must NOT change the exit code: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "invalid metrics section") {
		t.Fatalf("expected an invalid-metrics-section warning, got %q", errOut)
	}
}

// TestCLI_MetricsImportDeclaredModel (D3/AC3) asserts --model stamps a DECLARED
// model and its (provider, model) price for a transcript that carries none, and that
// an unknown price stays unknown (cost nil, never 0).
func TestCLI_MetricsImportDeclaredModel(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	catalog := writeCatalog(t, dir, `{"antigravity":{"models":[{"id":"gemini-pro","cost":{"input":1,"output":2,"cacheRead":0.5,"cacheWrite":0}}]}}`)

	code, out, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16",
		"--metrics-dir", metricsDir, "--model-catalog", catalog, "--model", "gemini-pro")
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "model=gemini-pro") || !strings.Contains(out, "model_source=declared") {
		t.Fatalf("the summary must show the declared model and its source: %q", out)
	}
	rec, raw := readOnlyStoredSession(t, metricsDir)
	if rec.Model != "gemini-pro" || rec.ModelSource != metrics.ModelSourceDeclared {
		t.Fatalf("stored model = %q/%q, want gemini-pro/declared", rec.Model, rec.ModelSource)
	}
	if rec.PriceSource != metrics.PriceSourceCatalog {
		t.Fatalf("declared model price source = %q, want catalog (the (provider, model) key resolves)", rec.PriceSource)
	}
	// The COST stays nil even with a resolved price: antigravity reports no
	// cache-write counter, so a cost would need an invented component (never 0).
	if rec.Cost != nil {
		t.Fatalf("declared model cost = %v, want nil (a component is unknown)", *rec.Cost)
	}
	for i := range rec.Models {
		m := rec.Models[i]
		if m.Model != "gemini-pro" || m.PriceSource != metrics.PriceSourceCatalog || m.Cost != nil {
			t.Fatalf("per-model declaration not applied: %+v", m)
		}
	}
	if !strings.Contains(string(raw), `"model_source": "declared"`) {
		t.Fatalf("raw session missing the declared model source:\n%s", raw)
	}

	// An UNKNOWN declared model (absent from the catalog) stays unknown: cost nil.
	other := filepath.Join(dir, "other-metrics")
	code, _, errOut = runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16",
		"--metrics-dir", other, "--model", "unlisted-model")
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	unlisted, _ := readOnlyStoredSession(t, other)
	if unlisted.PriceSource != metrics.PriceSourceUnknown || unlisted.Cost != nil {
		t.Fatalf("unlisted declared model = %q/%v, want unknown/nil", unlisted.PriceSource, unlisted.Cost)
	}
	if unlisted.Model != "unlisted-model" || unlisted.ModelSource != metrics.ModelSourceDeclared {
		t.Fatalf("the declaration still applies to the model id: %q/%q", unlisted.Model, unlisted.ModelSource)
	}
}

// TestCLI_MetricsImportDeclaredModelNeverOverridesObserved (D3/AC3) asserts an
// observed (transcript-carried) model outranks a declaration: the declaration is
// ignored with a warning and the stored model is unchanged.
func TestCLI_MetricsImportDeclaredModelNeverOverridesObserved(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", metricsDir, "--model", "some-other-model")
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "--model") || !strings.Contains(errOut, "observed") {
		t.Fatalf("expected the ignored-declaration warning, got %q", errOut)
	}
	if !strings.Contains(out, "model_source=observed") {
		t.Fatalf("the observed source must survive: %q", out)
	}
	rec, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.Model != "test-model" || rec.ModelSource != metrics.ModelSourceObserved {
		t.Fatalf("stored model = %q/%q, want test-model/observed", rec.Model, rec.ModelSource)
	}
}

// TestCLI_MetricsImportDeclaredTotal (D3 owner extension) asserts --declare-total
// stamps a SYNTHESIZED lower-bound total for a transcript with no totals, that the
// report's SESS-TOKENS column shows it, and that a re-import is a no-op until a new
// declaration changes the stamp.
func TestCLI_MetricsImportDeclaredTotal(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	storageDir := filepath.Join(dir, "records")

	// 1. Without the flag: the honest antigravity day — one session, UNKNOWN total.
	code, out, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16", "--metrics-dir", metricsDir, "--model", "gemini-pro")
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "total_tokens=unknown") || !strings.Contains(out, "total_source=unknown") {
		t.Fatalf("an undeclared antigravity import must keep an unknown total: %q", out)
	}
	if code, _, errOut = runArgs(t, "metrics", "aggregate", "--date", "2026-10-16", "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
		t.Fatalf("aggregate: %s", errOut)
	}
	code, out, errOut = runArgs(t, "metrics", "report", "--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("report: %s", errOut)
	}
	if !strings.Contains(out, "SESS-TOKENS") {
		t.Fatalf("report missing the SESS-TOKENS column:\n%s", out)
	}
	if !reportCell(t, out, "SESS-TOKENS", "unknown") {
		t.Fatalf("an antigravity day must read SESS-TOKENS unknown without --declare-total:\n%s", out)
	}
	if strings.Contains(out, "[synthesized]") {
		t.Fatalf("a day with no synthesized total must carry no footnote:\n%s", out)
	}

	// 2. With the flag: the synthesized lower bound (2740+730+1550).
	code, out, errOut = runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16", "--metrics-dir", metricsDir,
		"--model", "gemini-pro", "--declare-total")
	if code != exitOK {
		t.Fatalf("import --declare-total: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "total_tokens=5020") || !strings.Contains(out, "total_source=synthesized") {
		t.Fatalf("the declared total must be the synthesized lower bound, labelled: %q", out)
	}
	rec, _ := readOnlyStoredSession(t, metricsDir)
	if rec.TotalTokensSource != metrics.TotalTokensSourceSynthesized || rec.TotalTokens.Value != 5020 {
		t.Fatalf("stored total = %+v/%q, want 5020/synthesized", rec.TotalTokens, rec.TotalTokensSource)
	}
	if code, _, errOut = runArgs(t, "metrics", "aggregate", "--date", "2026-10-16", "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
		t.Fatalf("aggregate: %s", errOut)
	}
	code, out, errOut = runArgs(t, "metrics", "report", "--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("report: %s", errOut)
	}
	if !reportCell(t, out, "SESS-TOKENS", "5020") {
		t.Fatalf("the report must show the synthesized session total:\n%s", out)
	}
	if !strings.Contains(out, "[synthesized] 1 session total(s) on this day include synthesized lower bounds") {
		t.Fatalf("the report must label the day's synthesized total:\n%s", out)
	}
	if !strings.Contains(out, "SESSIONS") {
		t.Fatalf("report header missing:\n%s", out)
	}
}

// TestCLI_MetricsImportDeclarationIdempotence (D3/AC3) asserts an IDENTICAL
// re-import rewrites nothing, while a NEW declared stamp rewrites ONCE.
func TestCLI_MetricsImportDeclarationIdempotence(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	importArgs := func(model string) []string {
		return []string{"metrics", "import", "--provider", "antigravity", "--transcript", antigravityFixture,
			"--date", "2026-10-16", "--metrics-dir", metricsDir, "--model", model, "--declare-total"}
	}
	if code, out, errOut := runArgs(t, importArgs("model-a")...); code != exitOK {
		t.Fatalf("first import: %s", errOut)
	} else if !strings.Contains(out, "session sha256:") || !strings.Contains(out, "written") {
		t.Fatalf("the session write outcome must be printed on a first import: %q", out)
	}
	sessions, err := os.ReadDir(filepath.Join(metricsDir, "sessions"))
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v err=%v", sessions, err)
	}
	firstBytes, err := os.ReadFile(filepath.Join(metricsDir, "sessions", sessions[0].Name()))
	if err != nil {
		t.Fatalf("read session: %v", err)
	}

	// Identical re-import: a no-op, so the stored bytes are untouched.
	if code, _, errOut := runArgs(t, importArgs("model-a")...); code != exitOK {
		t.Fatalf("repeat import: %s", errOut)
	}
	repeatBytes, err := os.ReadFile(filepath.Join(metricsDir, "sessions", sessions[0].Name()))
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !bytes.Equal(firstBytes, repeatBytes) {
		t.Fatalf("an identical re-import must not rewrite the session:\nfirst: %s\nrepeat: %s", firstBytes, repeatBytes)
	}

	// A NEW declared model rewrites once.
	if code, _, errOut := runArgs(t, importArgs("model-b")...); code != exitOK {
		t.Fatalf("re-declaration import: %s", errOut)
	}
	rec, _ := readStoredSession(t, metricsDir, strings.TrimSuffix(sessions[0].Name(), ".json"))
	if rec.Model != "model-b" {
		t.Fatalf("the new declaration was not applied: %q", rec.Model)
	}

	// 4. A BARE re-import (no --model, no --declare-total) must PRESERVE the stored
	// declarations instead of rebuilding a declaration-free record, and say so once.
	beforeBare, err := os.ReadFile(filepath.Join(metricsDir, "sessions", sessions[0].Name()))
	if err != nil {
		t.Fatalf("read session before base re-import: %v", err)
	}
	bare := []string{"metrics", "import", "--provider", "antigravity", "--transcript", antigravityFixture,
		"--date", "2026-10-16", "--metrics-dir", metricsDir}
	code, out, errOut := runArgs(t, bare...)
	if code != exitOK {
		t.Fatalf("bare re-import: %s", errOut)
	}
	if !strings.Contains(errOut, "preserved stored ") || !strings.Contains(errOut, "model=model-b") || !strings.Contains(errOut, "total_source=synthesized") {
		t.Fatalf("expected ONE preservation note naming the stored declarations, got %q", errOut)
	}
	if strings.Count(errOut, "preserved stored ") != 1 {
		t.Fatalf("the preservation note must be printed exactly once, got %q", errOut)
	}
	preserved, _ := readOnlyStoredSession(t, metricsDir)
	if preserved.Model != "model-b" || preserved.ModelSource != metrics.ModelSourceDeclared {
		t.Fatalf("a bare re-import destroyed the stored model declaration: %q/%q", preserved.Model, preserved.ModelSource)
	}
	if preserved.TotalTokensSource != metrics.TotalTokensSourceSynthesized || !preserved.TotalTokens.Known {
		t.Fatalf("a bare re-import destroyed the stored total declaration: %+v/%q", preserved.TotalTokens, preserved.TotalTokensSource)
	}
	// The stored price evidence survives too (it is derived from the same declared
	// model), and the file was NOT rewritten: preservation makes the write a no-op.
	afterBare, err := os.ReadFile(filepath.Join(metricsDir, "sessions", sessions[0].Name()))
	if err != nil {
		t.Fatalf("read session after bare re-import: %v", err)
	}
	if !bytes.Equal(beforeBare, afterBare) {
		t.Fatalf("a bare re-import must not rewrite the stored session:\nbefore: %s\nafter: %s", beforeBare, afterBare)
	}
	if !strings.Contains(out, "unchanged (declarations match)") {
		t.Fatalf("the write outcome must be printed: %q", out)
	}
}

// TestCLI_MetricsImportDeclaredTotalNeverDowngradesObserved (D3 owner extension)
// asserts --declare-total on a transcript that ALREADY carries totals warns and
// leaves the observed figure and label intact.
func TestCLI_MetricsImportDeclaredTotalNeverDowngradesObserved(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture,
		"--date", "2026-10-11", "--metrics-dir", metricsDir, "--declare-total")
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "--declare-total") || !strings.Contains(errOut, "observed") {
		t.Fatalf("expected the ignored-declaration warning, got %q", errOut)
	}
	if !strings.Contains(out, "total_tokens=145807") || !strings.Contains(out, "total_source=observed") {
		t.Fatalf("the observed total must survive a declaration: %q", out)
	}
	rec, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.TotalTokensSource != metrics.TotalTokensSourceObserved || rec.TotalTokens.Value != 145807 {
		t.Fatalf("stored total = %+v/%q, want 145807/observed", rec.TotalTokens, rec.TotalTokensSource)
	}
}

// TestCLI_MetricsSessionsJSONModelSourceLast (D3/AC3) asserts the sessions JSON
// gains model_source/total_source APPENDED, after provenance, and that the TABLE has
// no model_source column.
func TestCLI_MetricsSessionsJSONModelSourceLast(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	if code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir); code != exitOK {
		t.Fatalf("import: %s", errOut)
	}

	code, out, errOut := runArgs(t, "metrics", "sessions", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("sessions json: code=%d err=%q", code, errOut)
	}
	provIdx := strings.Index(out, `"provenance"`)
	modelIdx := strings.Index(out, `"model_source"`)
	totalIdx := strings.Index(out, `"total_source"`)
	if provIdx < 0 || modelIdx < 0 || totalIdx < 0 {
		t.Fatalf("sessions json missing a source key:\n%s", out)
	}
	if !(provIdx < modelIdx && modelIdx < totalIdx) {
		t.Fatalf("model_source/total_source must be APPENDED after provenance: provenance=%d model=%d total=%d\n%s", provIdx, modelIdx, totalIdx, out)
	}

	code, out, errOut = runArgs(t, "metrics", "sessions", "--metrics-dir", metricsDir, "--format", "table")
	if code != exitOK {
		t.Fatalf("sessions table: code=%d err=%q", code, errOut)
	}
	header := strings.Split(strings.TrimRight(out, "\n"), "\n")[0]
	if strings.Contains(header, "MODEL-SOURCE") || strings.Contains(header, "model_source") {
		t.Fatalf("model_source must NOT be a table column:\n%s", header)
	}
}

// reportCell reports whether the row of the rendered report table for column named
// column holds want.
func reportCell(t *testing.T, out, column, want string) bool {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("report table too short:\n%s", out)
	}
	header := strings.Fields(lines[0])
	row := strings.Fields(lines[1])
	for i, h := range header {
		if h != column {
			continue
		}
		if i >= len(row) {
			return false
		}
		return row[i] == want
	}
	t.Fatalf("column %q missing from header %q", column, lines[0])
	return false
}

// TestCLI_MetricsImportDeclareTotalTakesNoValue (F2) asserts --declare-total is a
// BOOLEAN and a stray positional is a usage error: `--declare-total 99999` must fail
// instead of silently importing and stamping a number nobody declared.
func TestCLI_MetricsImportDeclareTotalTakesNoValue(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	code, out, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16",
		"--metrics-dir", metricsDir, "--declare-total", "99999")
	if code != exitUsage {
		t.Fatalf("a positional after --declare-total must be a usage error: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(errOut, "unexpected argument") || !strings.Contains(errOut, `"99999"`) {
		t.Fatalf("the usage error must name the stray value, got %q", errOut)
	}
	if !strings.Contains(errOut, "--declare-total is a boolean") {
		t.Fatalf("the usage error must explain the boolean flag, got %q", errOut)
	}
	// Nothing was imported: the refusal happens before any store or transcript work.
	if entries, err := os.ReadDir(filepath.Join(metricsDir, "sessions")); err == nil && len(entries) != 0 {
		t.Fatalf("a usage error must write no session, found %d", len(entries))
	}

	// The flag WITHOUT a value still works.
	if code, _, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16",
		"--metrics-dir", metricsDir, "--declare-total"); code != exitOK {
		t.Fatalf("--declare-total without a value must import: code=%d err=%q", code, errOut)
	}
}

// TestCLI_MetricsConfigCatalogTildeExpandsAtRead (F7) asserts a config value of
// "~/cat.json" is home-expanded when it is READ (so it prices the import against the
// isolated HOME) and that the config document on disk is not rewritten.
func TestCLI_MetricsConfigCatalogTildeExpandsAtRead(t *testing.T) {
	home := hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	// The catalog lives under the isolated HOME, addressed ONLY as "~/cat.json".
	catPath := filepath.Join(home, "cat.json")
	if err := os.WriteFile(catPath, []byte(`{"test-provider":{"models":[{"id":"test-model","cost":{"input":3,"output":3,"cacheRead":3,"cacheWrite":3}}]}}`), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	cfgPath := plantConfig(t, home, cliConfig{Metrics: metricsConfig{ModelCatalog: "~/cat.json"}})
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", metricsDir, "--config", cfgPath); code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	rec, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if rec.PriceSource != metrics.PriceSourceCatalog || rec.Price.Input == nil || *rec.Price.Input != 3 {
		t.Fatalf(`a config "~/cat.json" must expand at READ time and price the session: source=%q input=%v`, rec.PriceSource, rec.Price.Input)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the config document must NOT be rewritten (no tilde rewrite on disk):\nbefore: %s\nafter: %s", before, after)
	}
}

// TestCLI_MetricsConfigExplicitMissingWarns (F8) asserts an EXPLICIT --config path
// that cannot be read warns, while an ABSENT DEFAULT config stays silent: neither
// changes an exit code.
func TestCLI_MetricsConfigExplicitMissingWarns(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()

	// 1. Default path absent (a machine that never configured metrics): silent.
	code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", filepath.Join(dir, "m-default"))
	if code != exitOK {
		t.Fatalf("import: code=%d err=%q", code, errOut)
	}
	if strings.Contains(errOut, "--config") {
		t.Fatalf("an absent DEFAULT config must stay silent, got %q", errOut)
	}

	// 2. Explicit path that does not exist: warn and continue.
	missing := filepath.Join(dir, "nope.json")
	code, _, errOut = runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", filepath.Join(dir, "m-explicit"), "--config", missing)
	if code != exitOK {
		t.Fatalf("an unreadable explicit config must NOT change the exit code: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(errOut, "--config "+missing+" unreadable") {
		t.Fatalf("an explicit missing config must warn, got %q", errOut)
	}
	// The import still lands.
	if _, err := os.Stat(filepath.Join(dir, "m-explicit", "sessions")); err != nil {
		t.Fatalf("the import must still write a session: %v", err)
	}
}

// TestCLI_MetricsReport_CorruptSessionIsAdvisory (R2-1) asserts a corrupt session
// file cannot fail a report: the footnote counter is ADVISORY, so BOTH formats exit 0
// and the table still renders (with a warning). `metrics aggregate` keeps its hard
// failure because it needs the whole session set.
func TestCLI_MetricsReport_CorruptSessionIsAdvisory(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	storageDir := filepath.Join(dir, "records")

	// A VALID day first: the aggregate genuinely reads sessions, so it must run
	// before the store is damaged.
	if code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11", "--metrics-dir", metricsDir); code != exitOK {
		t.Fatalf("import: %s", errOut)
	}
	if code, _, errOut := runArgs(t, "metrics", "aggregate", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
		t.Fatalf("aggregate: %s", errOut)
	}

	// Now damage ONLY the sessions lane.
	if err := os.WriteFile(filepath.Join(metricsDir, "sessions", "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("plant corrupt session: %v", err)
	}

	code, out, errOut := runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("a corrupt session file must not fail the table report: code=%d err=%q", code, errOut)
	}
	if !strings.Contains(out, "SESS-TOKENS") {
		t.Fatalf("the table must still render:\n%s", out)
	}
	if !strings.Contains(errOut, "warning: could not read stored sessions; the synthesized-total footnote may be incomplete") {
		t.Fatalf("the degraded footnote must be announced, got %q", errOut)
	}

	code, jsonOut, jsonErr := runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("the json report must also exit 0: code=%d err=%q", code, jsonErr)
	}
	if !strings.HasPrefix(jsonOut, "{\n  \"schema_version\": 1") {
		t.Fatalf("json report must still emit the aggregate:\n%s", jsonOut)
	}

	// The hard failure stays where the full set is genuinely required.
	code, _, aggErr := runArgs(t, "metrics", "aggregate", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--storage-dir", storageDir)
	if code == exitOK {
		t.Fatalf("aggregate must still FAIL on a corrupt session file (it needs the full set), got exit 0")
	}
	if !strings.Contains(aggErr, "corrupt session file") {
		t.Fatalf("aggregate's error must name the corrupt session file, got %q", aggErr)
	}
}

// TestCLI_MetricsImportUnchangedAnnotatesPrintedPrice (R2-3) asserts a no-op import
// does not print this run's price/cost as if they were stored: the values are
// annotated, and the REPORT still shows the stored (earlier) price.
func TestCLI_MetricsImportUnchangedAnnotatesPrintedPrice(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	storageDir := filepath.Join(dir, "records")

	catA := writeCatalog(t, t.TempDir(), `{"test-provider":{"models":[{"id":"test-model","cost":{"input":1,"output":1,"cacheRead":1,"cacheWrite":1}}]}}`)
	catB := writeCatalog(t, t.TempDir(), `{"test-provider":{"models":[{"id":"test-model","cost":{"input":9,"output":9,"cacheRead":9,"cacheWrite":9}}]}}`)

	if code, _, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", metricsDir, "--model-catalog", catA); code != exitOK {
		t.Fatalf("first import: %s", errOut)
	}
	stored, _ := readStoredSession(t, metricsDir, "test-session-0001")
	if stored.Cost == nil {
		t.Fatal("fixture must price under catalog A for this test to mean anything")
	}

	code, out, errOut := runArgs(t, "metrics", "import", "--transcript", piFixture, "--date", "2026-10-11",
		"--metrics-dir", metricsDir, "--model-catalog", catB)
	if code != exitOK {
		t.Fatalf("re-import: %s", errOut)
	}
	if !strings.Contains(out, "unchanged (declarations match)") {
		t.Fatalf("a price-only change must stay a no-op: %q", out)
	}
	if !strings.Contains(out, "figures not stored (the stored session keeps its earlier price)") {
		t.Fatalf("a no-op import must annotate the printed price/cost as not stored: %q", out)
	}
	if !strings.Contains(out, "price source: catalog; figures not stored") {
		t.Fatalf("the note must attach to the price SOURCE label, not read as a stored price: %q", out)
	}
	// The printed cost is B's (this run), never presented as the stored figure.
	wantPrinted := formatOptionalCost(stored.Cost) // A's, i.e. what the store holds
	if strings.Contains(out, "cost="+wantPrinted) {
		t.Fatalf("the no-op import printed A's cost: %q", out)
	}

	// The REPORT still reads the STORED (A) price, proving nothing was downgraded.
	if code, _, errOut := runArgs(t, "metrics", "aggregate", "--date", "2026-10-11", "--metrics-dir", metricsDir, "--storage-dir", storageDir); code != exitOK {
		t.Fatalf("aggregate: %s", errOut)
	}
	code, reportOut, reportErr := runArgs(t, "metrics", "report", "--date", "2026-10-11", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("report: %s", reportErr)
	}
	if !reportCell(t, reportOut, "SESS-COST", wantPrinted) {
		t.Fatalf("the report must still show the stored (catalog A) cost %s:\n%s", wantPrinted, reportOut)
	}
}

// TestCLI_MetricsImportPreservesLegacyObservedModel (R2-5) asserts the model axis
// normalizes a LEGACY record (empty model_source, real model) as observed, so a bare
// re-import preserves it instead of ranking it as unknown.
func TestCLI_MetricsImportPreservesLegacyObservedModel(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")

	// 1. A first import establishes the session and its id.
	if code, _, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16", "--metrics-dir", metricsDir); code != exitOK {
		t.Fatalf("first import: %s", errOut)
	}
	rec, _ := readOnlyStoredSession(t, metricsDir)
	// 2. Rewrite the stored record into the LEGACY shape: a real model with NO
	// model_source field at all.
	rec.Model = "gemini-pro"
	rec.ModelSource = ""
	rec.Models = nil
	legacy, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metricsDir, "sessions", strings.ReplaceAll(rec.SessionID, ":", "_")+".json"), legacy, 0o600); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}

	// 3. A bare re-import must PRESERVE the legacy observed model and say so.
	code, out, errOut := runArgs(t, "metrics", "import", "--provider", "antigravity",
		"--transcript", antigravityFixture, "--date", "2026-10-16", "--metrics-dir", metricsDir)
	if code != exitOK {
		t.Fatalf("bare re-import: %s", errOut)
	}
	if !strings.Contains(errOut, "preserved stored ") || !strings.Contains(errOut, "model=gemini-pro (model_source=observed)") {
		t.Fatalf("a legacy observed model must be preserved and named as observed, got %q", errOut)
	}
	preserved, _ := readOnlyStoredSession(t, metricsDir)
	if preserved.Model != "gemini-pro" {
		t.Fatalf("the legacy observed model was destroyed: %q", preserved.Model)
	}
	// R3-2: the preserved source is written back EFFECTIVE, so neither the record
	// nor the summary line re-emits an empty label.
	if preserved.ModelSource != metrics.ModelSourceObserved {
		t.Fatalf("the written record re-emitted model_source = %q, want observed", preserved.ModelSource)
	}
	if !strings.Contains(out, "model_source=observed") {
		t.Fatalf("the summary line must match the preservation note, got %q", out)
	}
	code, sessionsJSON, sessionsErr := runArgs(t, "metrics", "sessions", "--metrics-dir", metricsDir, "--format", "json")
	if code != exitOK {
		t.Fatalf("sessions --format json: %s", sessionsErr)
	}
	if !strings.Contains(sessionsJSON, `"model_source": "observed"`) {
		t.Fatalf("the stored record must report model_source=observed:\n%s", sessionsJSON)
	}
}

// TestPreserveStoredDeclarations_TotalGatedOnContentHash (R2-7) asserts a
// synthesized total is inherited ONLY when the call set (content hash) is the same:
// a modified transcript must never inherit an old lower bound.
func TestPreserveStoredDeclarations_TotalGatedOnContentHash(t *testing.T) {
	stored := metrics.SessionRecord{
		SessionID:         "s1",
		ContentHash:       "hash-1",
		TotalTokens:       workflow.KnownMeasurement(5020),
		TotalTokensSource: metrics.TotalTokensSourceSynthesized,
		Model:             "gemini-pro",
		ModelSource:       metrics.ModelSourceDeclared,
	}

	// Same content set: the total IS preserved (and the model too).
	same := metrics.SessionRecord{SessionID: "s1", ContentHash: "hash-1",
		TotalTokens: workflow.UnknownMeasurement(), TotalTokensSource: metrics.TotalTokensSourceUnknown}
	note := preserveStoredDeclarations(&same, stored, nil)
	if !strings.Contains(note, "total_source=synthesized") {
		t.Fatalf("same-hash re-import must preserve the synthesized total, note = %q", note)
	}
	if same.TotalTokens.Value != 5020 || same.TotalTokensSource != metrics.TotalTokensSourceSynthesized {
		t.Fatalf("preserved total = %+v/%q, want 5020/synthesized", same.TotalTokens, same.TotalTokensSource)
	}

	// DIFFERENT content set: the synthesized total is NOT inherited; the model
	// declaration still is (a model is a property of the session).
	changed := metrics.SessionRecord{SessionID: "s1", ContentHash: "hash-2",
		TotalTokens: workflow.UnknownMeasurement(), TotalTokensSource: metrics.TotalTokensSourceUnknown}
	note = preserveStoredDeclarations(&changed, stored, nil)
	if strings.Contains(note, "total_source=") {
		t.Fatalf("a changed call set must NOT inherit the old synthesized total, note = %q", note)
	}
	if changed.TotalTokens.Known {
		t.Fatalf("changed-call-set total = %+v, want UNKNOWN", changed.TotalTokens)
	}
	if !strings.Contains(note, "model=gemini-pro") {
		t.Fatalf("the model declaration is not content-gated and must be preserved, note = %q", note)
	}
}

// claudeJSONLLine renders one Claude Code transcript line. No `model` key is ever
// emitted, so the transcript is MODEL-LESS and `--model` is the only way it gets an
// identifier.
func claudeJSONLLine(t *testing.T, typ, sessionID, uuid, parentUUID, timestamp string, input, output int64) string {
	t.Helper()
	if typ == "user" {
		return `{"type":"user","sessionId":"` + sessionID + `","uuid":"` + uuid + `","parentUuid":null,"timestamp":"` + timestamp + `","message":{"role":"user","content":"go"}}` + "\n"
	}
	return `{"type":"assistant","sessionId":"` + sessionID + `","uuid":"` + uuid + `","parentUuid":"` + parentUUID + `","timestamp":"` + timestamp + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"x"}}],"usage":{"input_tokens":` +
		strconv.FormatInt(input, 10) + `,"output_tokens":` + strconv.FormatInt(output, 10) +
		`,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}` + "\n"
}

// TestCLI_MetricsImportReattributesPreservedModelOnGrownTranscript (R3-1) asserts a
// preserved model id never drags the OLD parse's evidence along: when the transcript
// grew, the per-model usage/price/cost are re-resolved from the CURRENT parse, so
// sum(Models[].TotalTokens) == TotalTokens and the cost reflects the full usage —
// not the pre-growth call set.
func TestCLI_MetricsImportReattributesPreservedModelOnGrownTranscript(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	metricsDir := filepath.Join(dir, "metrics")
	transcript := filepath.Join(dir, "claude.jsonl")

	const sid = "test-claude-session"
	first := claudeJSONLLine(t, "user", sid, "u1", "", "2026-10-11T05:00:00.000Z", 0, 0) +
		claudeJSONLLine(t, "assistant", sid, "a1", "u1", "2026-10-11T05:00:01.000Z", 1000, 10)
	if err := os.WriteFile(transcript, []byte(first), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	cat := writeCatalog(t, t.TempDir(), `{"claude":{"models":[{"id":"test-model","cost":{"input":15,"output":75,"cacheRead":1.5,"cacheWrite":18.75}}]}}`)

	if code, _, errOut := runArgs(t, "metrics", "import", "--provider", "claude", "--transcript", transcript,
		"--date", "2026-10-11", "--metrics-dir", metricsDir, "--model", "test-model", "--model-catalog", cat); code != exitOK {
		t.Fatalf("first import: %s", errOut)
	}
	before, _ := readStoredSession(t, metricsDir, sid)
	if before.Model != "test-model" || before.ModelSource != metrics.ModelSourceDeclared {
		t.Fatalf("first import model = %q/%q, want test-model/declared", before.Model, before.ModelSource)
	}
	if !before.TotalTokens.Known || before.TotalTokens.Value != 1010 {
		t.Fatalf("first import total = %+v, want 1010", before.TotalTokens)
	}
	if before.Cost == nil {
		t.Fatal("the catalog must price the first parse for this test to mean anything")
	}
	firstCost := *before.Cost

	// The transcript GROWS: the same session now carries 2,000,000 consumed tokens.
	grownLine := claudeJSONLLine(t, "assistant", sid, "a2", "a1", "2026-10-11T05:00:05.000Z", 1998990, 0)
	if err := os.WriteFile(transcript, []byte(first+grownLine), 0o600); err != nil {
		t.Fatalf("grow transcript: %v", err)
	}

	// A bare re-import: no --model repeated, the catalog still resolves prices.
	code, out, errOut := runArgs(t, "metrics", "import", "--provider", "claude", "--transcript", transcript,
		"--date", "2026-10-11", "--metrics-dir", metricsDir, "--model-catalog", cat)
	if code != exitOK {
		t.Fatalf("bare re-import: %s", errOut)
	}
	if !strings.Contains(errOut, "model=test-model") {
		t.Fatalf("the declared model must be preserved, got %q", errOut)
	}
	grown, _ := readStoredSession(t, metricsDir, sid)
	if !grown.TotalTokens.Known || grown.TotalTokens.Value != 2000000 {
		t.Fatalf("re-import total = %+v, want 2000000", grown.TotalTokens)
	}
	var modelSum int64
	for _, m := range grown.Models {
		modelSum += m.TotalTokens.Value
		if m.Model != "test-model" {
			t.Fatalf("model evidence id = %q, want the preserved test-model", m.Model)
		}
	}
	if modelSum != grown.TotalTokens.Value {
		t.Fatalf("sum(Models[].TotalTokens) = %d but TotalTokens = %d; the preserved model kept the OLD parse's evidence", modelSum, grown.TotalTokens.Value)
	}
	if grown.Cost == nil || grown.Models[0].PriceSource != metrics.PriceSourceCatalog {
		t.Fatalf("the catalog prices the model, so the re-import cost must be known (price_source=%q)", grown.Models[0].PriceSource)
	}
	want := *metrics.EstimateCost(grown.Models[0].Usage, grown.Models[0].Price)
	if math.Abs(*grown.Cost-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v (derived from the CURRENT full usage)", *grown.Cost, want)
	}
	if !(*grown.Cost > firstCost*100) {
		t.Fatalf("cost %v looks derived from the pre-growth usage (first cost %v)", *grown.Cost, firstCost)
	}
	if !strings.Contains(out, "model_source=declared") {
		t.Fatalf("the summary must label the preserved model source, got %q", out)
	}
}
