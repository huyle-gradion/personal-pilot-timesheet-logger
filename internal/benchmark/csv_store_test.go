package benchmark

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptrInt64(v int64) *int64 { return &v }
func ptrInt(v int) *int       { return &v }

// TestCSVStore_RoundTripUnknownCells (R1) asserts append -> read round-trips an
// identical row, an unknown token cell reads back as nil (never 0), the report
// prints `unknown` rather than `0`, and the chart takes its nil path.
func TestCSVStore_RoundTripUnknownCells(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics", "metrics.csv")

	known := DailyMetric{
		Date:         "2026-10-11",
		WallClockMS:  420,
		PeakRSSMB:    12.5,
		InputTokens:  ptrInt64(300),
		OutputTokens: ptrInt64(90),
		CacheTokens:  ptrInt64(30),
		TotalTokens:  ptrInt64(420),
		Entries:      ptrInt(3),
	}
	if err := UpsertMetric(path, known); err != nil {
		t.Fatalf("upsert known row: %v", err)
	}
	unknown := DailyMetric{Date: "2026-10-12", WallClockMS: 10, PeakRSSMB: 1.0}
	if err := UpsertMetric(path, unknown); err != nil {
		t.Fatalf("upsert unknown row: %v", err)
	}

	// The empty token cells are written EMPTY, never as 0.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if !strings.Contains(string(raw), "2026-10-12,10,1.0,,,,,\n") {
		t.Fatalf("unknown row must carry empty cells, got:\n%s", raw)
	}

	rows, err := ReadMetrics(path)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	got := rows[0]
	if got.Date != known.Date || got.WallClockMS != known.WallClockMS || got.PeakRSSMB != known.PeakRSSMB {
		t.Fatalf("row did not round-trip: %+v", got)
	}
	if got.InputTokens == nil || *got.InputTokens != 300 ||
		got.OutputTokens == nil || *got.OutputTokens != 90 ||
		got.CacheTokens == nil || *got.CacheTokens != 30 ||
		got.TotalTokens == nil || *got.TotalTokens != 420 ||
		got.Entries == nil || *got.Entries != 3 {
		t.Fatalf("known row did not round-trip: %+v", got)
	}

	blank := rows[1]
	if blank.InputTokens != nil || blank.OutputTokens != nil || blank.CacheTokens != nil ||
		blank.TotalTokens != nil || blank.Entries != nil {
		t.Fatalf("an empty token cell must read back nil, not zero: %+v", blank)
	}

	// Report: unknown, never 0.
	var report bytes.Buffer
	if err := RenderReport(&report, rows, 1000); err != nil {
		t.Fatalf("report: %v", err)
	}
	for _, line := range strings.Split(report.String(), "\n") {
		if strings.HasPrefix(line, "2026-10-12") {
			if !strings.Contains(line, "unknown") {
				t.Fatalf("report row must print unknown: %q", line)
			}
			if strings.Contains(line, " 0 ") {
				t.Fatalf("report row must never print 0 for an unknown cell: %q", line)
			}
		}
	}
	if !strings.Contains(report.String(), "- Total LLM tokens consumed : 420 tokens") {
		t.Fatalf("report must aggregate only the known day:\n%s", report.String())
	}

	// Chart: the nil path (bar omitted, status unknown, never `0 <= ceiling`).
	var chart bytes.Buffer
	if err := RenderASCIIChart(&chart, rows, 1000); err != nil {
		t.Fatalf("chart: %v", err)
	}
	for _, line := range strings.Split(chart.String(), "\n") {
		if strings.HasPrefix(line, "2026-10-12") {
			if !strings.Contains(line, "unknown") {
				t.Fatalf("chart row must print unknown: %q", line)
			}
			if strings.Contains(line, "[PASS]") || strings.Contains(line, "[OVER]") {
				t.Fatalf("chart must omit the bar and the status badge for an unknown day: %q", line)
			}
			if strings.Contains(line, "█") || strings.Contains(line, "░") {
				t.Fatalf("chart must omit the bar for an unknown day: %q", line)
			}
		}
	}
}

// TestCSVStore_UpsertLastWins (R2) asserts two records for one date yield ONE row,
// the last value wins, and "days tracked" counts distinct dates.
func TestCSVStore_UpsertLastWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.csv")

	write := func(date string, total int64) {
		t.Helper()
		m := DailyMetric{Date: date, WallClockMS: 100, PeakRSSMB: 1.0, TotalTokens: ptrInt64(total)}
		if err := UpsertMetric(path, m); err != nil {
			t.Fatalf("upsert %s: %v", date, err)
		}
	}
	write("2026-10-11", 100)
	write("2026-10-12", 200)
	write("2026-10-11", 150)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if n := strings.Count(string(raw), "2026-10-11"); n != 1 {
		t.Fatalf("one row per date: found %d rows for 2026-10-11 in:\n%s", n, raw)
	}

	rows, err := ReadMetrics(path)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].Date != "2026-10-11" || rows[0].TotalTokens == nil || *rows[0].TotalTokens != 150 {
		t.Fatalf("last value must win: %+v", rows[0])
	}
	if stats := ComputeSummary(rows, 1000); stats.TotalDays != 2 {
		t.Fatalf("days tracked counts distinct dates: got %d", stats.TotalDays)
	}
}

// TestCSVStore_RewriteNotTruncatedAtMaxRows (R2) asserts a rewrite of a store with
// MORE than MaxCSVRowsCount rows preserves ALL of them: no silent truncation.
func TestCSVStore_RewriteNotTruncatedAtMaxRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.csv")

	total := MaxCSVRowsCount + 5
	rows := make([]DailyMetric, 0, total)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		rows = append(rows, DailyMetric{Date: date, WallClockMS: int64(i), PeakRSSMB: 1.0})
	}
	if err := writeMetricsFile(path, rows, 0o644); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if err := UpsertMetric(path, DailyMetric{Date: "2030-12-31", WallClockMS: 1, PeakRSSMB: 1.0}); err != nil {
		t.Fatalf("upsert past the bound: %v", err)
	}

	readBack, err := ReadMetrics(path)
	if err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if len(readBack) != total+1 {
		t.Fatalf("rewrite truncated the store: got %d rows, want %d", len(readBack), total+1)
	}
	if readBack[0].Date != "2020-01-01" || readBack[len(readBack)-1].Date != "2030-12-31" {
		t.Fatalf("first/last rows lost: %q .. %q", readBack[0].Date, readBack[len(readBack)-1].Date)
	}
	if stats := ComputeSummary(readBack, 1000); stats.TotalDays != total+1 {
		t.Fatalf("days tracked = %d, want %d", stats.TotalDays, total+1)
	}
}

// TestCSVStore_MalformedRowFailsLoudly asserts a malformed row is an ERROR, never
// the historical silent drop.
func TestCSVStore_MalformedRowFailsLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.csv")
	content := CSVHeader + "\n" + "2026-10-11,not-a-number,1.0,1,2,3,6,1\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ReadMetrics(path); err == nil {
		t.Fatal("a malformed row must fail loudly, not be skipped")
	}
	if err := UpsertMetric(path, DailyMetric{Date: "2026-10-12"}); err == nil {
		t.Fatal("a rewrite must refuse a malformed store rather than drop the row")
	}
}

// TestCSVStore_RecordedRowsReadBack (R7) asserts the three historical rows still
// read back: no migration breakage.
func TestCSVStore_RecordedRowsReadBack(t *testing.T) {
	rows, err := ReadMetrics(filepath.Join("..", "..", "benchmarks", "metrics.csv"))
	if err != nil {
		t.Fatalf("read the recorded store: %v", err)
	}
	want := map[string]int64{
		"2026-10-06": 180,
		"2026-10-07": 295,
		"2026-10-08": 287,
	}
	if len(rows) != len(want) {
		t.Fatalf("expected %d recorded rows, got %d: %s", len(want), len(rows), fmt.Sprint(rows))
	}
	for _, m := range rows {
		total, ok := want[m.Date]
		if !ok {
			t.Fatalf("unexpected recorded date %q", m.Date)
		}
		if m.TotalTokens == nil || *m.TotalTokens != total {
			t.Fatalf("%s total = %v, want %d", m.Date, m.TotalTokens, total)
		}
		if m.CacheTokens == nil || *m.CacheTokens != 0 {
			t.Fatalf("%s cache = %v, want a recorded 0", m.Date, m.CacheTokens)
		}
	}
}

// TestCSVStore_RewritePreservesFileMode (F4) asserts an upsert does not widen an
// existing store's permissions: a 0600 store stays 0600.
func TestCSVStore_RewritePreservesFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.csv")
	if err := os.WriteFile(path, []byte(CSVHeader+"\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpsertMetric(path, DailyMetric{Date: "2026-10-11", WallClockMS: 1, PeakRSSMB: 1.0}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("rewrite changed the store mode: got %04o, want 0600", got)
	}

	// A store created by the upsert gets the documented 0644.
	created := filepath.Join(t.TempDir(), "new.csv")
	if err := UpsertMetric(created, DailyMetric{Date: "2026-10-11", WallClockMS: 1, PeakRSSMB: 1.0}); err != nil {
		t.Fatalf("create: %v", err)
	}
	fi, err = os.Stat(created)
	if err != nil {
		t.Fatalf("stat created: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Fatalf("created store mode: got %04o, want 0644", got)
	}
}

// TestCSVStore_RewriteRefusesSymlinkStore (F4) asserts the write path agrees with
// the reader's O_NOFOLLOW posture: a symlinked store is refused loudly, not
// silently replaced, and the link's target is untouched.
func TestCSVStore_RewriteRefusesSymlinkStore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.csv")
	original := CSVHeader + "\n2026-10-06,320,13.8,140,40,0,180,3\n"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.csv")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := UpsertMetric(link, DailyMetric{Date: "2026-10-11", WallClockMS: 1, PeakRSSMB: 1.0})
	if err == nil {
		t.Fatal("a symlinked store must be refused, not silently replaced")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %q, want it to name the symlink refusal", err)
	}

	// The link survives and its target is unchanged.
	if fi, lErr := os.Lstat(link); lErr != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: fi=%v err=%v", fi, lErr)
	}
	if got, rErr := os.ReadFile(target); rErr != nil || string(got) != original {
		t.Fatalf("the link target changed: %q err=%v", got, rErr)
	}

	// The reader refuses the same path (O_NOFOLLOW): read and write agree.
	if _, rErr := ReadMetrics(link); rErr == nil {
		t.Fatal("the reader must refuse a symlinked store as the writer does")
	}
}
