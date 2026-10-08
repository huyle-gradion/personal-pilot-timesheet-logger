package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
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

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

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
		transcript  string
		csvPath     string
		runCmd      string
		wallClockMS int64
		peakRSSMB   float64
		entries     int
		storageDir  string
		ceiling     int64
	)

	today := time.Now().UTC().Format("2006-01-02")
	fs.BoolVar(&recordMode, "record", false, "Record a new benchmark metric row")
	fs.BoolVar(&chartMode, "chart", false, "Output terminal ASCII bar chart of daily token consumption")
	fs.BoolVar(&reportMode, "report", false, "Output summary report and statistics of recorded benchmarks")
	fs.StringVar(&targetDate, "date", today, "Date of benchmark in YYYY-MM-DD format")
	fs.StringVar(&transcript, "transcript", "", "Path to Claude Code or Antigravity JSONL transcript file")
	fs.StringVar(&csvPath, "csv", benchmark.DefaultCSVPath, "Path to benchmarks metrics.csv file")
	fs.StringVar(&runCmd, "run", "", "Optional shell command to execute and measure wall clock and peak RSS")
	fs.Int64Var(&wallClockMS, "wall-clock-ms", 0, "Manual wall clock execution time in milliseconds")
	fs.Float64Var(&peakRSSMB, "peak-rss-mb", 0.0, "Manual peak RSS memory in megabytes")
	fs.IntVar(&entries, "entries", 0, "Number of timesheet entries logged (defaults to querying storage store if 0)")
	fs.StringVar(&storageDir, "storage-dir", "", "Directory of Tier 2 storage store to automatically count entries")
	fs.Int64Var(&ceiling, "ceiling", benchmark.DefaultTokenCeiling, "Daily token budget ceiling for chart and compliance check")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	// Validate target date
	targetDate = strings.TrimSpace(targetDate)
	if !datePattern.MatchString(targetDate) {
		fmt.Fprintf(errOut, "error: invalid -date %q: must be YYYY-MM-DD\n", targetDate)
		return ExitUsage
	}

	// Default to report mode if no mode was explicitly selected
	if !recordMode && !chartMode && !reportMode {
		reportMode = true
	}

	// 1. Record Mode
	if recordMode {
		metric := benchmark.DailyMetric{
			Date:        targetDate,
			WallClockMS: wallClockMS,
			PeakRSSMB:   peakRSSMB,
			Entries:     entries,
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

		// B. Parse transcript if provided
		if strings.TrimSpace(transcript) != "" {
			usage, err := benchmark.ParseTranscriptFile(ctx, transcript)
			if err != nil {
				fmt.Fprintf(errOut, "error: parsing transcript %s: %v\n", transcript, err)
				return ExitErr
			}
			metric.InputTokens = usage.InputTokens
			metric.OutputTokens = usage.OutputTokens
			metric.CacheTokens = usage.CacheTokens
			metric.TotalTokens = usage.TotalTokens
		}

		// C. Auto-detect entries count from storage store if not provided
		if metric.Entries <= 0 {
			if store, sErr := storage.NewJSONFileStore(storageDir); sErr == nil {
				if rec, gErr := store.GetDay(ctx, targetDate); gErr == nil {
					metric.Entries = len(rec.Blocks)
				}
			}
		}

		// If peak RSS was not set, measure current process RSS
		if metric.PeakRSSMB <= 0 {
			metric.PeakRSSMB = benchmark.CurrentProcessRSSMB()
		}

		if err := benchmark.AppendMetric(csvPath, metric); err != nil {
			fmt.Fprintf(errOut, "error: appending metric to %s: %v\n", csvPath, err)
			return ExitErr
		}

		fmt.Fprintf(out, "Recorded benchmark metric for %s into %s:\n", targetDate, csvPath)
		fmt.Fprintf(out, "  Wall clock: %d ms | Peak RSS: %.1f MB | Tokens (In/Out/Cache/Total): %d/%d/%d/%d | Entries: %d\n",
			metric.WallClockMS, metric.PeakRSSMB,
			metric.InputTokens, metric.OutputTokens, metric.CacheTokens, metric.TotalTokens,
			metric.Entries,
		)
	}

	// 2. Chart Mode
	if chartMode {
		metrics, err := benchmark.ReadMetrics(csvPath)
		if err != nil {
			fmt.Fprintf(errOut, "error: reading metrics from %s: %v\n", csvPath, err)
			return ExitErr
		}
		if err := benchmark.RenderASCIIChart(out, metrics, ceiling); err != nil {
			fmt.Fprintf(errOut, "error: rendering chart: %v\n", err)
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
		if err := benchmark.RenderReport(out, metrics); err != nil {
			fmt.Fprintf(errOut, "error: rendering report: %v\n", err)
			return ExitErr
		}
	}

	return ExitOK
}
