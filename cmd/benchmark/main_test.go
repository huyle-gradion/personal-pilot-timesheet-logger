package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/metrics"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// TestCLI_Record_InvalidDate_ExitUsage distinguishes the flag's shape check from a
// REAL calendar date: 2026/10/08 is not YYYY-MM-DD, and 2026-02-31 matches the
// pattern but is not a date, so workflow.ValidateDate's calendar parse is what
// rejects it.
func TestCLI_Record_InvalidDate_ExitUsage(t *testing.T) {
	cases := []struct {
		name string
		date string
	}{
		{"wrong-separators", "2026/10/08"},
		{"not-a-real-date", "2026-02-31"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := []string{"-record", "-date=" + tc.date, "-csv=" + filepath.Join(t.TempDir(), "metrics.csv")}
			code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
			if code != ExitUsage {
				t.Fatalf("expected ExitUsage (64), got %d (err: %s)", code, errOut.String())
			}
			if !strings.Contains(errOut.String(), "invalid -date") {
				t.Errorf("expected invalid -date in error output, got: %s", errOut.String())
			}
		})
	}
}

// TestCLI_Record_RequiresDate is the §7 date-column analogue of R5: -record with
// no -date fails, even though the machine clock could "supply" a day. No row is
// written.
func TestCLI_Record_RequiresDate(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-wall-clock-ms=1420",
		"-peak-rss-mb=18.4",
		"-csv=" + csvPath,
		"-storage-dir=" + filepath.Join(tempDir, "records"),
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d (out: %s err: %s)", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "requires an explicit -date") {
		t.Fatalf("expected the explicit-date message, got: %s", errOut.String())
	}
	if _, err := os.Stat(csvPath); !os.IsNotExist(err) {
		t.Fatalf("a refused record must write no row, stat err = %v", err)
	}

	// -chart and -report do not read the date, so they are NOT gated. -chart reads
	// the metrics store, so it gets an explicit (empty) -metrics-dir: the test never
	// reads the developer's real ~/.timesheet/metrics.
	emptyMetricsDir := filepath.Join(tempDir, "metrics")
	if code := run(context.Background(), []string{"-chart", "-csv=" + csvPath, "-metrics-dir=" + emptyMetricsDir}, strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("-chart must not require -date, got code %d (err: %s)", code, errOut.String())
	}
	if code := run(context.Background(), []string{"-report", "-csv=" + csvPath}, strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("-report must not require -date, got code %d (err: %s)", code, errOut.String())
	}
}

// seedMetricsDay writes ONE daily aggregate into dir for date, with the given
// session lane. It is the chart's input now that -chart reads the metrics store
// instead of the CSV.
func seedMetricsDay(t *testing.T, dir, date string, sessionCount int, total *int64) {
	t.Helper()
	store, err := metrics.NewStore(dir)
	if err != nil {
		t.Fatalf("metrics store: %v", err)
	}
	agg := metrics.DailyAggregate{
		SchemaVersion: metrics.SchemaVersion,
		Date:          date,
		RunIDs:        []string{},
		SessionIDs:    []string{},
		SessionCount:  sessionCount,
		Budget: metrics.BudgetReport{
			Target: workflow.EditorialTokenCeiling,
			Spent:  workflow.UnknownMeasurement(),
			State:  metrics.BudgetUnknownState,
		},
	}
	if sessionCount > 0 {
		agg.SessionIDs = []string{"s-" + date}
	}
	if total != nil {
		agg.SessionTotalTokens = workflow.KnownMeasurement(*total)
	}
	if _, err := store.WriteAggregate(agg); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}
}

func TestCLI_InvalidCeiling_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-ceiling=0"}, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "invalid -ceiling") {
		t.Errorf("expected invalid -ceiling in error output, got: %s", errOut.String())
	}
}

func TestCLI_FlagParseError_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-unrecognized-flag"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d", code)
	}
}

func TestCLI_Record_ManualValues(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=2026-10-07",
		"-wall-clock-ms=1420",
		"-peak-rss-mb=18.4",
		"-entries=4",
		"-csv=" + csvPath,
		// No record exists here, so the token cells stay unknown.
		"-storage-dir=" + filepath.Join(tempDir, "records"),
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK (0), got %d (err: %s)", code, errOut.String())
	}

	if !strings.Contains(out.String(), "Recorded benchmark metric for 2026-10-07") {
		t.Errorf("expected success message in stdout, got: %s", out.String())
	}

	metrics, err := benchmark.ReadMetrics(csvPath)
	if err != nil {
		t.Fatalf("failed to read recorded metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].WallClockMS != 1420 || metrics[0].PeakRSSMB != 18.4 {
		t.Errorf("unexpected metric content: %+v", metrics[0])
	}
	if metrics[0].Entries == nil || *metrics[0].Entries != 4 {
		t.Errorf("an explicit -entries value must be recorded, got %+v", metrics[0].Entries)
	}

	// The day has no workflow record: the token cells are EMPTY, never 0.
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(raw), "2026-10-07,1420,18.4,,,,,4\n") {
		t.Fatalf("token cells must be empty for a day with no record, got:\n%s", raw)
	}
}

// TestCLI_Record_RecordFedColumns asserts the token columns and the entry count
// are copied from the day's workflow record, and that -storage-dir points at the
// WORKFLOW store (not the legacy one).
func TestCLI_Record_RecordFedColumns(t *testing.T) {
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "records")
	csvPath := filepath.Join(tempDir, "metrics.csv")
	targetDate := "2026-10-08"

	store, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateDay(context.Background(), targetDate, func(rec *workflow.Record) error {
		rec.Confirmed = append(rec.Confirmed,
			workflow.ConfirmedInterval{ID: "c1", Date: targetDate},
			workflow.ConfirmedInterval{ID: "c2", Date: targetDate},
			workflow.ConfirmedInterval{ID: "c3", Date: targetDate},
		)
		rec.Usage = &workflow.EditorialUsage{
			Date:            targetDate,
			Editorial:       workflow.KnownMeasurement(295),
			EditorialInput:  workflow.KnownMeasurement(220),
			EditorialOutput: workflow.KnownMeasurement(75),
			EditorialCache:  workflow.KnownMeasurement(0),
			EditorialRuns:   1,
			Invoked:         true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=" + targetDate,
		"-storage-dir=" + storageDir,
		"-csv=" + csvPath,
		"-wall-clock-ms=340",
		"-peak-rss-mb=14.2",
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	metrics, err := benchmark.ReadMetrics(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.InputTokens == nil || *m.InputTokens != 220 ||
		m.OutputTokens == nil || *m.OutputTokens != 75 ||
		m.CacheTokens == nil || *m.CacheTokens != 0 ||
		m.TotalTokens == nil || *m.TotalTokens != 295 {
		t.Fatalf("token columns must come from the record: %+v", m)
	}
	if m.Entries == nil || *m.Entries != 3 {
		t.Fatalf("entries must come from the record's confirmed intervals: %+v", m.Entries)
	}
	if !strings.Contains(out.String(), "Tokens (In/Out/Cache/Total): 220/75/0/295") {
		t.Errorf("expected the record-fed tokens in stdout, got: %s", out.String())
	}
}

// TestCLI_Record_RecordReadErrorWarns asserts a record-read error leaves the
// entries (and token) cells EMPTY with a stderr warning: never a swallowed zero.
func TestCLI_Record_RecordReadErrorWarns(t *testing.T) {
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "records")
	csvPath := filepath.Join(tempDir, "metrics.csv")
	targetDate := "2026-10-08"

	if _, err := workflow.NewJSONFileStore(storageDir); err != nil {
		t.Fatal(err)
	}
	corrupt := filepath.Join(storageDir, "workflow", targetDate+".json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=" + targetDate,
		"-storage-dir=" + storageDir,
		"-csv=" + csvPath,
		"-wall-clock-ms=340",
		"-peak-rss-mb=14.2",
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("a record-read error is a warning, not a failure: code=%d err=%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "warning: reading the workflow record") {
		t.Fatalf("expected a stderr warning, got: %s", errOut.String())
	}
	raw, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "2026-10-08,340,14.2,,,,,\n") {
		t.Fatalf("a record-read error must leave the cells empty, got:\n%s", raw)
	}
}

func TestCLI_Record_WithRunCommand(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=2026-10-08",
		"-run=echo running-test-cmd",
		"-csv=" + csvPath,
		"-storage-dir=" + filepath.Join(tempDir, "records"),
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	metrics, err := benchmark.ReadMetrics(csvPath)
	if err != nil {
		t.Fatalf("failed to read metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].PeakRSSMB <= 0.0 {
		t.Errorf("expected positive peak RSS, got: %f", metrics[0].PeakRSSMB)
	}
}

func TestCLI_Record_RunCommandError(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=2026-10-08",
		"-run=non-existent-binary-123456",
		"-csv=" + csvPath,
		"-storage-dir=" + filepath.Join(tempDir, "records"),
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Errorf("expected ExitErr for invalid command, got %d", code)
	}
}

func TestCLI_ChartAndReportModes(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")
	metricsDir := filepath.Join(tempDir, "metrics")

	total := int64(420)
	seedMetricsDay(t, metricsDir, "2026-10-08", 1, &total)
	m := benchmark.DailyMetric{
		Date:        "2026-10-08",
		TotalTokens: &total,
	}
	if err := benchmark.UpsertMetric(csvPath, m); err != nil {
		t.Fatal(err)
	}

	// 1. Chart mode
	var out1, errOut1 bytes.Buffer
	code1 := run(context.Background(), []string{"-chart", "-metrics-dir=" + metricsDir, "-csv=" + csvPath}, strings.NewReader(""), &out1, &errOut1)
	if code1 != ExitOK {
		t.Fatalf("expected ExitOK for chart, got %d (err: %s)", code1, errOut1.String())
	}
	if !strings.Contains(out1.String(), "Token Consumption Trend") {
		t.Errorf("expected chart header in stdout, got: %s", out1.String())
	}
	if !strings.Contains(out1.String(), "source: "+metricsDir+" day aggregates") {
		t.Errorf("expected the chart source line, got: %s", out1.String())
	}

	// 2. Report mode
	var out2, errOut2 bytes.Buffer
	code2 := run(context.Background(), []string{"-report", "-csv=" + csvPath}, strings.NewReader(""), &out2, &errOut2)
	if code2 != ExitOK {
		t.Fatalf("expected ExitOK for report, got %d (err: %s)", code2, errOut2.String())
	}
	if !strings.Contains(out2.String(), "Empirical Benchmark Metrics Report") {
		t.Errorf("expected report header in stdout, got: %s", out2.String())
	}

	// 3. Default mode (neither -record nor -chart nor -report passed)
	var out3, errOut3 bytes.Buffer
	code3 := run(context.Background(), []string{"-csv=" + csvPath}, strings.NewReader(""), &out3, &errOut3)
	if code3 != ExitOK {
		t.Fatalf("expected ExitOK for default mode, got %d", code3)
	}
	if !strings.Contains(out3.String(), "Empirical Benchmark Metrics Report") {
		t.Errorf("expected default mode to run report, got: %s", out3.String())
	}
}

// TestCLI_CeilingReachesBothRenderers (R4) asserts -ceiling governs the chart AND
// the report's compliance line, and that the untouched invocation keeps the
// owner's 1000 from the one workflow constant.
func TestCLI_CeilingReachesBothRenderers(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")
	metricsDir := filepath.Join(tempDir, "metrics")

	total := int64(900)
	seedMetricsDay(t, metricsDir, "2026-10-08", 1, &total)
	if err := benchmark.UpsertMetric(csvPath, benchmark.DailyMetric{Date: "2026-10-08", TotalTokens: &total}); err != nil {
		t.Fatal(err)
	}

	runMode := func(args ...string) (string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		full := append([]string{}, args...)
		full = append(full, "-csv="+csvPath, "-metrics-dir="+metricsDir)
		code := run(context.Background(), full, strings.NewReader(""), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("run %v: code=%d err=%s", args, code, errOut.String())
		}
		return out.String(), errOut.String()
	}

	chart, _ := runMode("-chart", "-ceiling=500")
	if !strings.Contains(chart, "Ceiling: 500 tokens") {
		t.Errorf("chart must use -ceiling, got: %s", chart)
	}
	if !strings.Contains(chart, "[OVER]") {
		t.Errorf("chart must flag 900 > 500, got: %s", chart)
	}

	report, _ := runMode("-report", "-ceiling=500")
	if !strings.Contains(report, "<= 500 tokens") {
		t.Errorf("report must use -ceiling, got: %s", report)
	}
	if !strings.Contains(report, "0/1 known days") {
		t.Errorf("report must count 900 > 500 as over budget, got: %s", report)
	}

	// The untouched invocation defaults to the owner's 1000 (one workflow constant).
	chartDefault, _ := runMode("-chart")
	if !strings.Contains(chartDefault, "Ceiling: 1000 tokens") || !strings.Contains(chartDefault, "[PASS]") {
		t.Errorf("the default ceiling must be 1000, got: %s", chartDefault)
	}
	reportDefault, _ := runMode("-report")
	if !strings.Contains(reportDefault, "<= 1000 tokens") || !strings.Contains(reportDefault, "1/1 known days") {
		t.Errorf("the default ceiling must be 1000, got: %s", reportDefault)
	}
}

func TestCLI_ChartAndReport_ReadErrors(t *testing.T) {
	tempDir := t.TempDir()
	// Directory path instead of file path will cause ReadMetrics to fail
	dirAsCSV := filepath.Join(tempDir, "is_a_dir")
	if err := os.MkdirAll(dirAsCSV, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Chart read error: a CORRUPT aggregate is a typed error naming the date. The
	// chart reads the metrics store, so its failure surface is a bad aggregate, not a
	// bad CSV.
	corruptMetricsDir := filepath.Join(tempDir, "corrupt-metrics")
	if err := os.MkdirAll(corruptMetricsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptMetricsDir, "2026-10-08.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out1, errOut1 bytes.Buffer
	code1 := run(context.Background(), []string{"-chart", "-metrics-dir=" + corruptMetricsDir}, strings.NewReader(""), &out1, &errOut1)
	if code1 != ExitErr {
		t.Errorf("expected ExitErr (1) on a corrupt aggregate, got %d", code1)
	}
	if !strings.Contains(errOut1.String(), "2026-10-08") {
		t.Errorf("the corrupt-aggregate error must name the date, got: %s", errOut1.String())
	}

	// 2. Report read error
	var out2, errOut2 bytes.Buffer
	code2 := run(context.Background(), []string{"-report", "-csv=" + dirAsCSV}, strings.NewReader(""), &out2, &errOut2)
	if code2 != ExitErr {
		t.Errorf("expected ExitErr (1) when reading metrics for report fails, got %d", code2)
	}
	if !strings.Contains(errOut2.String(), "error: reading metrics") {
		t.Errorf("expected reading metrics error in errOut, got: %s", errOut2.String())
	}
}

// TestCLI_Chart_ReadsMetricsStoreDayAggregates (D2/AC2) asserts the chart's bar is
// the day's SESSION total from the metrics store — the same number `metrics report`
// shows in SESS-TOKENS — and that the CSV's editorial token column NO LONGER
// reaches the chart. It also asserts the source line names the store.
func TestCLI_Chart_ReadsMetricsStoreDayAggregates(t *testing.T) {
	tempDir := t.TempDir()
	metricsDir := filepath.Join(tempDir, "metrics")
	csvPath := filepath.Join(tempDir, "metrics.csv")

	sessionTotal := int64(730)
	seedMetricsDay(t, metricsDir, "2026-10-08", 1, &sessionTotal)

	// A DIFFERENT editorial figure in the CSV proves which source the chart reads.
	csvTotal := int64(28)
	if err := benchmark.UpsertMetric(csvPath, benchmark.DailyMetric{Date: "2026-10-08", TotalTokens: &csvTotal}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-chart", "-metrics-dir=" + metricsDir, "-csv=" + csvPath}, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("chart: code=%d err=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "730") {
		t.Fatalf("the chart must draw the metrics store's session total (730):\n%s", out.String())
	}
	if strings.Contains(out.String(), " 28 ") {
		t.Fatalf("the chart must NOT read the CSV's editorial column:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "source: "+metricsDir+" day aggregates (harness session tokens, reasoning excluded; day totals may include synthesized lower bounds; see: timesheet metrics sessions --format json -> total_source)") {
		t.Fatalf("missing the honest source line:\n%s", out.String())
	}
	// The ceiling tick is a REFERENCE, never a harness budget, and the verdict is
	// printed beside the chart.
	if !strings.Contains(out.String(), "reference: 1,000/day is the editorial-child soft target; [PASS] compares harness session tokens against it — it is not a harness budget. (use -ceiling to change)") {
		t.Fatalf("missing the reference/verdict line:\n%s", out.String())
	}
}

// TestCLI_Chart_ReferenceVerdict (F5) asserts the verdict follows the ceiling and is
// never claimed over unmeasured days: OVER above it, PASS at/below it, and [n/a]
// when no day carries a known total.
func TestCLI_Chart_ReferenceVerdict(t *testing.T) {
	runChart := func(t *testing.T, dir string, extra ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		args := append([]string{"-chart", "-metrics-dir=" + dir}, extra...)
		if code := run(context.Background(), args, strings.NewReader(""), &out, &errOut); code != ExitOK {
			t.Fatalf("chart: code=%d err=%s", code, errOut.String())
		}
		return out.String()
	}

	// 1. A day over the default ceiling: [OVER], and the ceiling is comma-formatted.
	overDir := filepath.Join(t.TempDir(), "metrics")
	over := int64(1500)
	seedMetricsDay(t, overDir, "2026-10-08", 1, &over)
	out := runChart(t, overDir)
	if !strings.Contains(out, "reference: 1,000/day is the editorial-child soft target; [OVER] compares harness session tokens against it") {
		t.Fatalf("a day above the ceiling must read [OVER]:\n%s", out)
	}

	// 2. -ceiling reaches the reference line too, and a day under it reads [PASS].
	passDir := filepath.Join(t.TempDir(), "metrics")
	low := int64(730)
	seedMetricsDay(t, passDir, "2026-10-09", 1, &low)
	out = runChart(t, passDir, "-ceiling=2000")
	if !strings.Contains(out, "reference: 2,000/day is the editorial-child soft target; [PASS]") {
		t.Fatalf("the flag must reach the reference line and a day under it must read [PASS]:\n%s", out)
	}

	// 3. No known total anywhere: no PASS is claimed, and the comparison clause is
	// not borrowed for a chart that measured nothing.
	unknownDir := filepath.Join(t.TempDir(), "metrics")
	seedMetricsDay(t, unknownDir, "2026-10-10", 1, nil)
	out = runChart(t, unknownDir)
	if !strings.Contains(out, "reference: 1,000/day is the editorial-child soft target; [n/a] — no charted day carries a known total. This is not a harness budget. (use -ceiling to change)") {
		t.Fatalf("unmeasured days must state [n/a] in their own words, not a comparison:\n%s", out)
	}
	if strings.Contains(out, "[n/a] compares harness session tokens") {
		t.Fatalf("the unmeasured arm must not borrow the comparison clause:\n%s", out)
	}
}

// TestCLI_Chart_NoSessionsOrUnknownIsUnknown (D2/AC2) asserts a no-session day and
// an unknown session total both render `unknown` with NO bar: never a 0 bar.
func TestCLI_Chart_NoSessionsOrUnknownIsUnknown(t *testing.T) {
	tempDir := t.TempDir()
	metricsDir := filepath.Join(tempDir, "metrics")

	// 2026-10-08 has no sessions at all; 2026-10-09 has one whose total is unknown.
	seedMetricsDay(t, metricsDir, "2026-10-08", 0, nil)
	seedMetricsDay(t, metricsDir, "2026-10-09", 1, nil)

	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-chart", "-metrics-dir=" + metricsDir}, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("chart: code=%d err=%s", code, errOut.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, "2026-10-08") && !strings.Contains(line, "2026-10-09") {
			continue
		}
		if !strings.Contains(line, "unknown") {
			t.Fatalf("an unmeasured day must read unknown: %q", line)
		}
		if strings.Contains(line, "█") || strings.Contains(line, "░") {
			t.Fatalf("an unmeasured day must draw NO bar: %q", line)
		}
		if strings.Contains(line, " 0 ") {
			t.Fatalf("an unmeasured day must never render 0 as its token count: %q", line)
		}
	}
}

// TestCLI_Chart_EmptyStoreIsOneHonestLine (D2/AC2) asserts an absent store is exit 0
// with ONE line naming the aggregating command, and that reading it CREATES no
// directory.
func TestCLI_Chart_EmptyStoreIsOneHonestLine(t *testing.T) {
	tempDir := t.TempDir()
	absent := filepath.Join(tempDir, "not-created", "metrics")

	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-chart", "-metrics-dir=" + absent}, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("an empty store is not an error: code=%d err=%s", code, errOut.String())
	}
	want := "no metrics day aggregates in " + absent + " yet (run: timesheet metrics aggregate --date YYYY-MM-DD)\n"
	if out.String() != want {
		t.Fatalf("expected exactly one honest line:\n got: %q\nwant: %q", out.String(), want)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatalf("reading the chart source must NEVER create the store directory, stat err = %v", err)
	}
}

// TestCLI_Chart_CorruptAggregateNamesDateAndFails (D2/AC2) asserts a corrupt day
// aggregate is exit 1 with the date named — never a silently skipped day.
func TestCLI_Chart_CorruptAggregateNamesDateAndFails(t *testing.T) {
	tempDir := t.TempDir()
	metricsDir := filepath.Join(tempDir, "metrics")
	if err := os.MkdirAll(metricsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metricsDir, "2026-10-08.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"-chart", "-metrics-dir=" + metricsDir}, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("a corrupt aggregate must exit %d, got %d (out=%q)", ExitErr, code, out.String())
	}
	if !strings.Contains(errOut.String(), "2026-10-08") || !strings.Contains(errOut.String(), "corrupt aggregate") {
		t.Fatalf("the error must be typed and name the date, got: %s", errOut.String())
	}
}

func TestCLI_Record_WriteError(t *testing.T) {
	tempDir := t.TempDir()
	// An invalid CSV path where parent is a regular file
	fileAsParent := filepath.Join(tempDir, "regular_file")
	if err := os.WriteFile(fileAsParent, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalidCSV := filepath.Join(fileAsParent, "metrics.csv")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=2026-10-08",
		"-csv=" + invalidCSV,
		"-storage-dir=" + filepath.Join(tempDir, "records"),
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Errorf("expected ExitErr (1) when recording the metric fails, got %d", code)
	}
	if !strings.Contains(errOut.String(), "error: recording metric") {
		t.Errorf("expected recording metric error in errOut, got: %s", errOut.String())
	}
}

func TestCLI_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_BENCH_CLI_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "nil_ctx":
			_ = run(nil, []string{}, strings.NewReader(""), os.Stdout, os.Stderr)
		case "nil_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), nil, os.Stderr)
		case "nil_err_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), os.Stdout, nil)
		}
		return
	}

	cases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"nil_ctx", "nil_ctx", AssertBenchContextNil},
		{"nil_out", "nil_out", AssertBenchWritersNil},
		{"nil_err_out", "nil_err_out", AssertBenchWritersNil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestCLI_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_BENCH_CLI_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected subprocess to crash with panic %q, but succeeded: %s", tc.expected, string(out))
			}
			if !strings.Contains(string(out), tc.expected) {
				t.Fatalf("expected panic %q in output, got: %s", tc.expected, string(out))
			}
		})
	}
}
