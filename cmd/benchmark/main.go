package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/metrics"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const (
	ExitOK    = 0
	ExitErr   = 1
	ExitUsage = 64
)

const (
	AssertBenchContextNil = "ASSERT_BENCH_CLI_001_NIL_CONTEXT"
	AssertBenchWritersNil = "ASSERT_BENCH_CLI_002_NIL_WRITERS"
)

// Assert enforces invariant conditions for TigerStyle reliability.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	Assert(ctx != nil, AssertBenchContextNil)
	Assert(out != nil, AssertBenchWritersNil)
	Assert(errOut != nil, AssertBenchWritersNil)

	fs := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var (
		recordMode  bool
		chartMode   bool
		reportMode  bool
		targetDate  string
		csvPath     string
		runCmd      string
		wallClockMS int64
		peakRSSMB   float64
		entries     int
		storageDir  string
		ceiling     int64
		metricsDir  string
	)

	fs.BoolVar(&recordMode, "record", false, "Record a new benchmark metric row")
	fs.BoolVar(&chartMode, "chart", false, "Output terminal ASCII bar chart of the metrics store's daily session tokens")
	fs.BoolVar(&reportMode, "report", false, "Output summary report and statistics of recorded benchmarks")
	fs.StringVar(&targetDate, "date", "", "Date of the benchmark row in YYYY-MM-DD format (REQUIRED with -record; no day is inferred from the clock)")
	fs.StringVar(&csvPath, "csv", benchmark.DefaultCSVPath, "Path to benchmarks metrics.csv file (-record/-report only; -chart reads the metrics store)")
	fs.StringVar(&runCmd, "run", "", "Optional shell command to execute and measure wall clock and peak RSS")
	fs.Int64Var(&wallClockMS, "wall-clock-ms", 0, "Manual wall clock execution time in milliseconds")
	fs.Float64Var(&peakRSSMB, "peak-rss-mb", 0.0, "Manual peak RSS memory in megabytes")
	fs.IntVar(&entries, "entries", 0, "Number of timesheet entries logged (defaults to the day's workflow record)")
	fs.StringVar(&storageDir, "storage-dir", "", "Directory of the workflow record store holding the day's entries and editorial tokens")
	fs.Int64Var(&ceiling, "ceiling", 0, "Chart ceiling in tokens: when set, a FIXED ceiling the bars scale to, overriding the default (largest charted day rounded up to the next 1,000); -report keeps the editorial-child 1,000 when unset")
	fs.StringVar(&metricsDir, "metrics-dir", "", "Directory of the metrics store -chart reads the day aggregates from (default: ~/.timesheet/metrics)")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	// -ceiling is now an EXPLICIT override: unset means the chart derives its
	// ceiling from the data (largest known day snapped up to 1,000), while -report
	// keeps the editorial-child 1,000. A supplied non-positive value is a usage
	// error rather than a silently-defaulted chart.
	ceilingSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ceiling" {
			ceilingSet = true
		}
	})
	if ceilingSet && ceiling <= 0 {
		fmt.Fprintf(errOut, "error: invalid -ceiling %d: must be positive\n", ceiling)
		return ExitUsage
	}

	// Default to report mode if no mode was explicitly selected
	if !recordMode && !chartMode && !reportMode {
		reportMode = true
	}

	// A supplied -date is always validated as a REAL calendar date (workflow's
	// parse, not just the YYYY-MM-DD shape), whichever mode reads it.
	targetDate = strings.TrimSpace(targetDate)
	if targetDate != "" {
		if err := workflow.ValidateDate(targetDate); err != nil {
			fmt.Fprintf(errOut, "error: invalid -date %q: must be a real YYYY-MM-DD date\n", targetDate)
			return ExitUsage
		}
	}

	// 1. Record Mode
	if recordMode {
		// The row's date is the OPERATOR's day: required, never derived from the
		// clock (mirroring metrics.Import's --date gate). -chart/-report do not
		// read it, so it is not required globally.
		if targetDate == "" {
			fmt.Fprintln(errOut, "error: -record requires an explicit -date YYYY-MM-DD; no day is inferred from the clock")
			return ExitUsage
		}

		metric := benchmark.DailyMetric{
			Date:        targetDate,
			WallClockMS: wallClockMS,
			PeakRSSMB:   peakRSSMB,
		}
		if entries > 0 {
			n := entries
			metric.Entries = &n
		}

		// A. Execute command if requested
		if strings.TrimSpace(runCmd) != "" {
			parts := strings.Fields(runCmd)
			if len(parts) > 0 {
				res, err := benchmark.MeasureCommand(ctx, parts[0], parts[1:]...)
				if err != nil {
					fmt.Fprintf(errOut, "error: executing command %q: %v\n", runCmd, err)
					return ExitErr
				}
				metric.WallClockMS = res.WallClockMS
				metric.PeakRSSMB = res.PeakRSSMB
			}
		}

		// B. Record-fed columns. The token columns are the [AI] editorial-child
		// lane, COPIES of the day's workflow record; nothing is parsed from a
		// transcript. A date with no record (or no store) leaves the cells EMPTY
		// (unknown), never 0. A record-read error leaves them empty too and WARNS:
		// it is never swallowed into a zero. Wall clock and RSS come from the run
		// itself and never depend on the store.
		readDayColumns(ctx, targetDate, resolveStorageDir(storageDir), &metric, errOut)

		// If peak RSS was not set, measure current process RSS
		if metric.PeakRSSMB <= 0 {
			metric.PeakRSSMB = benchmark.CurrentProcessRSSMB()
		}

		if err := benchmark.UpsertMetric(csvPath, metric); err != nil {
			fmt.Fprintf(errOut, "error: recording metric in %s: %v\n", csvPath, err)
			return ExitErr
		}

		fmt.Fprintf(out, "Recorded benchmark metric for %s into %s:\n", targetDate, csvPath)
		fmt.Fprintf(out, "  Wall clock: %d ms | Peak RSS: %.1f MB | Tokens (In/Out/Cache/Total): %s/%s/%s/%s | Entries: %s\n",
			metric.WallClockMS, metric.PeakRSSMB,
			formatOptInt64(metric.InputTokens), formatOptInt64(metric.OutputTokens),
			formatOptInt64(metric.CacheTokens), formatOptInt64(metric.TotalTokens),
			formatOptInt(metric.Entries),
		)
	}

	// 2. Chart Mode. The source is the METRICS STORE's day aggregates, not the CSV:
	// this reverses #69's record-fed CSV chart (owner decision, #126) and is carried
	// by ADR-0006's 2026-10-21 amendment. The figure per day is the SESSION total —
	// the same number `metrics report`'s SESS-TOKENS column shows — so there is one
	// harness-token figure with two surfaces. The CSV stays the pipeline store for
	// -record/-report, whose behavior is unchanged.
	if chartMode {
		if err := renderMetricsChart(out, errOut, resolveMetricsDir(metricsDir), ceiling, ceilingSet); err != nil {
			return ExitErr
		}
	}

	// 3. Report Mode
	if reportMode {
		metrics, err := benchmark.ReadMetrics(csvPath)
		if err != nil {
			fmt.Fprintf(errOut, "error: reading metrics from %s: %v\n", csvPath, err)
			return ExitErr
		}
		if err := benchmark.RenderReport(out, metrics, reportCeiling(ceiling, ceilingSet)); err != nil {
			fmt.Fprintf(errOut, "error: rendering report: %v\n", err)
			return ExitErr
		}
	}

	return ExitOK
}

// renderMetricsChart draws the day-by-day chart from the metrics store's day
// aggregates. Three honest outcomes, none of which invents a number:
//
//   - no store or no aggregate yet: exit 0 with ONE line naming the command that
//     creates one (an empty chart is not an error);
//   - a corrupt aggregate: an error naming the date (exit 1), never a skipped day;
//   - a day with no sessions or any unknown session total: a nil (UNKNOWN) figure,
//     so internal/benchmark's own nil path omits the bar and prints `unknown`.
//     A day nobody measured is never drawn as a 0 bar.
//
// The ceiling is passed through unchanged: 0 asks the renderer to derive it from
// the largest known charted day (snapped up to the next 1,000), while a positive
// value is the operator's fixed -ceiling override.
func renderMetricsChart(out, errOut io.Writer, dir string, ceiling int64, ceilingFixed bool) error {
	dates, err := metrics.ListAggregateDates(dir)
	if err != nil {
		fmt.Fprintf(errOut, "error: reading metrics day aggregates from %s: %v\n", dir, err)
		return err
	}
	if len(dates) == 0 {
		fmt.Fprintf(out, "no metrics day aggregates in %s yet (run: timesheet metrics aggregate --date YYYY-MM-DD)\n", dir)
		return nil
	}

	rows := make([]benchmark.DailyMetric, 0, len(dates))
	for _, date := range dates {
		agg, err := metrics.ReadAggregateFile(dir, date)
		if err != nil {
			fmt.Fprintf(errOut, "error: metrics day aggregate %s: %v\n", date, err)
			return err
		}
		rows = append(rows, benchmark.DailyMetric{Date: date, TotalTokens: sessionTotalCell(agg)})
	}
	if err := benchmark.RenderASCIIChart(out, rows, ceiling); err != nil {
		fmt.Fprintf(errOut, "error: rendering chart: %v\n", err)
		return err
	}
	// One printed line naming the source, so the figure is never mistaken for the
	// editorial-child lane the CSV carries. The second clause keeps the day-level
	// honesty: a day's total may be a lower bound, and the label for it lives on the
	// session record.
	fmt.Fprintf(out, "source: %s day aggregates (harness session tokens, reasoning excluded; day totals may include synthesized lower bounds; see: timesheet metrics sessions --format json -> total_source)\n", dir)
	// And ONE line stating the ceiling rule the bar length follows, so the chart
	// can never read as a harness budget.
	fmt.Fprintln(out, chartCeilingLine(rows, ceiling, ceilingFixed))
	return nil
}

// chartCeilingLine renders the chart's ceiling note — one line, no verdict and no
// budget claim. In fixed mode the operator's -ceiling supplied the number; in the
// default mode it is the largest known charted day snapped up to the next 1,000.
// With no known charted total there is no ceiling to state, and the line says so
// rather than borrowing a number.
func chartCeilingLine(rows []benchmark.DailyMetric, ceiling int64, fixed bool) string {
	if fixed {
		return fmt.Sprintf("ceiling: fixed at %s by -ceiling (overrides the peak-snap default) — not a harness budget.", benchmark.FormatTokens(ceiling))
	}
	if _, hasKnown := benchmark.ChartCeiling(rows); !hasKnown {
		return "ceiling: unknown — no charted day carries a known total; nothing is scaled. Not a harness budget."
	}
	return "ceiling: largest charted day rounded up to the next 1,000 — not a harness budget."
}

// reportCeiling keeps -report's behavior byte-identical: its compliance line still
// compares the editorial-child lane against the shared 1,000, and only an explicit
// -ceiling changes that number. The chart's data-derived default never leaks here.
func reportCeiling(ceiling int64, ceilingSet bool) int64 {
	if ceilingSet {
		return ceiling
	}
	return workflow.EditorialTokenCeiling
}

// sessionTotalCell maps a day's session lane to the chart's nil-vs-value cell. A
// day with ZERO sessions, or one whose session total is unknown, is nil (UNKNOWN):
// the chart then omits the bar rather than printing a 0 it never measured.
func sessionTotalCell(agg metrics.DailyAggregate) *int64 {
	if agg.SessionCount == 0 || !agg.SessionTotalTokens.Known {
		return nil
	}
	total := agg.SessionTotalTokens.Value
	return &total
}

// resolveMetricsDir defaults -metrics-dir to ~/.timesheet/metrics, the same
// resolution idiom cmd/timesheet uses (no shared package, deliberately: the two
// binaries stay independently buildable).
func resolveMetricsDir(dir string) string {
	if d := strings.TrimSpace(dir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "metrics")
	}
	return ".timesheet/metrics"
}

// readDayColumns fills the token columns and the entry count from the day's
// workflow record, the same RecordSource the metrics aggregate reads. An unknown
// measurement becomes a nil cell (UNKNOWN), never a zero.
func readDayColumns(ctx context.Context, date, storageDir string, metric *benchmark.DailyMetric, errOut io.Writer) {
	store, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		fmt.Fprintf(errOut, "warning: workflow record store unavailable for %s; token and entry cells stay unknown: %v\n", date, err)
		return
	}
	var src metrics.RecordSource = store
	rec, err := src.GetDay(ctx, date)
	if err != nil {
		if !errors.Is(err, workflow.ErrRecordNotFound) {
			fmt.Fprintf(errOut, "warning: reading the workflow record for %s; token and entry cells stay unknown: %v\n", date, err)
		}
		return
	}
	if rec.Usage != nil {
		metric.InputTokens = knownValue(rec.Usage.EditorialInput)
		metric.OutputTokens = knownValue(rec.Usage.EditorialOutput)
		metric.CacheTokens = knownValue(rec.Usage.EditorialCache)
		metric.TotalTokens = knownValue(rec.Usage.Editorial)
	}
	// An explicit -entries value is the operator's; otherwise the record's
	// confirmed intervals are the day's entries.
	if metric.Entries == nil {
		n := len(rec.Confirmed)
		metric.Entries = &n
	}
}

// knownValue maps a workflow Measurement to a nil-vs-value cell: an unknown
// measurement is nil, never 0.
func knownValue(m workflow.Measurement) *int64 {
	if !m.Known {
		return nil
	}
	v := m.Value
	return &v
}

// resolveStorageDir defaults --storage-dir to ~/.timesheet/records, the same
// default the workflow CLI uses.
func resolveStorageDir(dir string) string {
	if d := strings.TrimSpace(dir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "records")
	}
	return ".timesheet/records"
}

func formatOptInt64(v *int64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *v)
}

func formatOptInt(v *int) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *v)
}
