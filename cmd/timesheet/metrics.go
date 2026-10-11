// This file implements the `timesheet metrics` command surface. internal/metrics
// is the implementation; this file only parses flags, wires the store and the
// workflow RecordSource, executes the optional live command, and renders output. A
// metrics failure prints a warning and NEVER changes a workflow exit code and
// NEVER triggers submission or replay.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/metrics"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const metricsUsage = `timesheet metrics <action> [flags]

Actions:
  import     import a transcript into the metrics store: metrics import --transcript FILE
  aggregate  build and persist the daily aggregate: metrics aggregate --date D
  report     render the daily aggregate: metrics report [--date D | --from/--to]
  compare    compare two days base -> target: metrics compare --base D --target D
  run        run a command and record a live monotonic run: metrics run -- <command...>

Flags:
  --transcript FILE        transcript path (import)
  --provider auto|pi|claude  transcript provider (import; default auto)
  --date YYYY-MM-DD        target date (default: today)
  --from/--to YYYY-MM-DD   date range (report)
  --base/--target D        base and target dates (compare)
  --commit SHA             run annotation (import/run)
  --workflow-version V     run annotation (import/run)
  --config-mode M          run annotation (import/run)
  --optimization-change C  run annotation (import/run)
  --metrics-dir DIR        metrics store directory (default: ~/.timesheet/metrics)
  --storage-dir DIR        workflow record directory for editorial usage (default: ~/.timesheet/records)
  --format table|json      report/compare format (default: table)

The metrics store layout is <metrics-dir>/runs/<run-id>.json and
<metrics-dir>/YYYY-MM-DD.json.
`

// cmdMetrics dispatches the metrics subcommands.
func cmdMetrics(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, metricsUsage)
		return exitUsage
	}
	switch args[0] {
	case "import":
		return cmdMetricsImport(ctx, args[1:], stdout, stderr)
	case "aggregate":
		return cmdMetricsAggregate(ctx, args[1:], stdout, stderr)
	case "report":
		return cmdMetricsReport(ctx, args[1:], stdout, stderr)
	case "compare":
		return cmdMetricsCompare(ctx, args[1:], stdout, stderr)
	case "run":
		return cmdMetricsRun(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, metricsUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "timesheet metrics: unknown action %q\n", args[0])
		fmt.Fprint(stderr, metricsUsage)
		return exitUsage
	}
}

func cmdMetricsImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet metrics import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	transcript := fs.String("transcript", "", "transcript path")
	provider := fs.String("provider", "auto", "transcript provider: auto|pi|claude")
	date := fs.String("date", "", "override the transcript-derived date YYYY-MM-DD")
	metricsDir := fs.String("metrics-dir", "", "metrics store directory")
	commit := fs.String("commit", "", "run commit annotation")
	workflowVersion := fs.String("workflow-version", "", "run workflow-version annotation")
	configMode := fs.String("config-mode", "", "run config-mode annotation")
	optimizationChange := fs.String("optimization-change", "", "run optimization-change annotation")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*transcript) == "" {
		fmt.Fprintln(stderr, "timesheet metrics import: --transcript is required")
		return exitUsage
	}
	if d := strings.TrimSpace(*date); d != "" {
		if err := workflow.ValidateDate(d); err != nil {
			fmt.Fprintf(stderr, "timesheet metrics import: invalid --date %q\n", d)
			return exitUsage
		}
	}

	rec, err := metrics.Import(ctx, *transcript, metrics.ImportOptions{
		Provider:           *provider,
		Date:               *date,
		Commit:             *commit,
		WorkflowVersion:    *workflowVersion,
		ConfigMode:         *configMode,
		OptimizationChange: *optimizationChange,
	})
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics import:", err)
		return exitError
	}

	store, err := metrics.NewStore(resolveMetricsDir(*metricsDir))
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics import:", err)
		return exitError
	}
	wrote, err := store.WriteRun(rec)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics import:", err)
		return exitError
	}
	if wrote {
		fmt.Fprintf(stdout, "imported run %s (%s, %s, %d call(s), state=%s)\n",
			rec.RunID, rec.Provider, rec.Date, len(rec.Calls), rec.State)
	} else {
		fmt.Fprintf(stdout, "run %s unchanged (content hash match; no write)\n", rec.RunID)
	}
	return exitOK
}

func cmdMetricsAggregate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet metrics aggregate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	date := fs.String("date", "", "target date YYYY-MM-DD (default: today)")
	metricsDir := fs.String("metrics-dir", "", "metrics store directory")
	storageDir := fs.String("storage-dir", "", "workflow record directory for editorial usage")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	d := strings.TrimSpace(*date)
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	if err := workflow.ValidateDate(d); err != nil {
		fmt.Fprintf(stderr, "timesheet metrics aggregate: invalid --date %q\n", d)
		return exitUsage
	}

	store, err := metrics.NewStore(resolveMetricsDir(*metricsDir))
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics aggregate:", err)
		return exitError
	}

	src, err := metricsRecordSource(resolveStorageDir(*storageDir))
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics aggregate:", err)
		return exitError
	}

	agg, err := store.Aggregate(ctx, d, src)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics aggregate:", err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s: aggregated %d run(s); orchestration=%s unassigned=%d call(s) editorial=%s budget=%s\n",
		agg.Date, agg.RunCount,
		metricsSummary(agg.Orchestration), agg.UnassignedCalls, metricsSummary(agg.Editorial), agg.Budget.State)
	return exitOK
}

func cmdMetricsReport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet metrics report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	date := fs.String("date", "", "target date YYYY-MM-DD")
	from := fs.String("from", "", "range start YYYY-MM-DD")
	to := fs.String("to", "", "range end YYYY-MM-DD")
	metricsDir := fs.String("metrics-dir", "", "metrics store directory")
	format := fs.String("format", "table", "output format: table|json")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	dates, err := metricsDates(*date, *from, *to)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics report:", err)
		return exitError
	}
	fmtMode := strings.ToLower(strings.TrimSpace(*format))
	if fmtMode != "table" && fmtMode != "json" {
		fmt.Fprintf(stderr, "timesheet metrics report: invalid --format %q\n", *format)
		return exitUsage
	}

	store, err := metrics.NewStore(resolveMetricsDir(*metricsDir))
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics report:", err)
		return exitError
	}

	if fmtMode == "json" {
		// ONE valid JSON document: a single object for one day, an array for a
		// range.
		aggs := make([]metrics.DailyAggregate, 0, len(dates))
		for _, d := range dates {
			agg, err := store.ReadAggregate(d)
			if err != nil {
				if errors.Is(err, metrics.ErrAggregateNotFound) {
					fmt.Fprintf(stderr, "timesheet metrics report: no aggregate for %s (run `timesheet metrics aggregate --date %s`)\n", d, d)
					return exitError
				}
				fmt.Fprintln(stderr, "timesheet metrics report:", err)
				return exitError
			}
			aggs = append(aggs, agg)
		}
		if err := metrics.RenderJSONDocuments(stdout, aggs); err != nil {
			fmt.Fprintln(stderr, "timesheet metrics report:", err)
			return exitError
		}
		return exitOK
	}

	for _, d := range dates {
		agg, err := store.ReadAggregate(d)
		if err != nil {
			if errors.Is(err, metrics.ErrAggregateNotFound) {
				fmt.Fprintf(stderr, "timesheet metrics report: no aggregate for %s (run `timesheet metrics aggregate --date %s`)\n", d, d)
				return exitError
			}
			fmt.Fprintln(stderr, "timesheet metrics report:", err)
			return exitError
		}
		runs, err := store.ListRuns(d)
		if err != nil {
			fmt.Fprintln(stderr, "timesheet metrics report:", err)
			return exitError
		}
		if err := metrics.RenderTable(stdout, metrics.TableInput{Aggregate: agg, Runs: runs}); err != nil {
			fmt.Fprintln(stderr, "timesheet metrics report:", err)
			return exitError
		}
	}
	return exitOK
}

func cmdMetricsCompare(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet metrics compare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base date YYYY-MM-DD")
	target := fs.String("target", "", "target date YYYY-MM-DD")
	metricsDir := fs.String("metrics-dir", "", "metrics store directory")
	format := fs.String("format", "table", "output format: table|json")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	b, tg := strings.TrimSpace(*base), strings.TrimSpace(*target)
	if b == "" || tg == "" {
		fmt.Fprintln(stderr, "timesheet metrics compare: --base and --target are required")
		return exitUsage
	}
	for _, d := range []string{b, tg} {
		if err := workflow.ValidateDate(d); err != nil {
			fmt.Fprintf(stderr, "timesheet metrics compare: invalid date %q\n", d)
			return exitUsage
		}
	}
	fmtMode := strings.ToLower(strings.TrimSpace(*format))
	if fmtMode != "table" && fmtMode != "json" {
		fmt.Fprintf(stderr, "timesheet metrics compare: invalid --format %q\n", *format)
		return exitUsage
	}

	store, err := metrics.NewStore(resolveMetricsDir(*metricsDir))
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics compare:", err)
		return exitError
	}
	baseAgg, err := store.ReadAggregate(b)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet metrics compare: base %s: %v\n", b, err)
		return exitError
	}
	targetAgg, err := store.ReadAggregate(tg)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet metrics compare: target %s: %v\n", tg, err)
		return exitError
	}
	baseCalls, err := metricsCallCount(store, b)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics compare:", err)
		return exitError
	}
	targetCalls, err := metricsCallCount(store, tg)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet metrics compare:", err)
		return exitError
	}

	cmp := metrics.Compare(metrics.CompareInput{
		Base: baseAgg, Target: targetAgg, BaseCalls: baseCalls, TargetCalls: targetCalls,
	})
	if fmtMode == "json" {
		if err := metrics.RenderCompareJSON(stdout, cmp); err != nil {
			fmt.Fprintln(stderr, "timesheet metrics compare:", err)
			return exitError
		}
		return exitOK
	}
	if err := metrics.RenderCompareTable(stdout, cmp); err != nil {
		fmt.Fprintln(stderr, "timesheet metrics compare:", err)
		return exitError
	}
	return exitOK
}

// cmdMetricsRun executes a child command, measures it with the live monotonic
// clock, records a live run, and mirrors the child's exit code. A child that
// cannot start is a metrics error (exit 1). A POST-RUN metrics failure (the store
// cannot be opened or the run cannot be written) must NOT change the child's
// reported outcome: the child already ran, so the wrapper warns on stderr and
// returns the CHILD's exit code (no replay, no submission, no exit-code override).
func cmdMetricsRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet metrics run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	date := fs.String("date", "", "target date YYYY-MM-DD (default: today)")
	metricsDir := fs.String("metrics-dir", "", "metrics store directory")
	commit := fs.String("commit", "", "run commit annotation")
	workflowVersion := fs.String("workflow-version", "", "run workflow-version annotation")
	configMode := fs.String("config-mode", "", "run config-mode annotation")
	optimizationChange := fs.String("optimization-change", "", "run optimization-change annotation")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	parts := fs.Args()
	if len(parts) == 0 {
		fmt.Fprintln(stderr, "timesheet metrics run: a command is required: metrics run -- <command...>")
		return exitUsage
	}

	d := strings.TrimSpace(*date)
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	if err := workflow.ValidateDate(d); err != nil {
		fmt.Fprintf(stderr, "timesheet metrics run: invalid --date %q\n", d)
		return exitUsage
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			code = ee.ExitCode()
		} else {
			fmt.Fprintln(stderr, "timesheet metrics run:", runErr)
			return exitError
		}
	}
	if code < 0 {
		// A signal-killed process reports a negative code; record it as failed.
		code = 1
	}

	rec := metrics.NewLiveRun(start, code, metrics.LiveRunOptions{
		Date:               d,
		Commit:             *commit,
		WorkflowVersion:    *workflowVersion,
		ConfigMode:         *configMode,
		OptimizationChange: *optimizationChange,
	})

	store, err := metrics.NewStore(resolveMetricsDir(*metricsDir))
	if err != nil {
		// The child already ran: a metrics-store failure is a WARNING, never a
		// change to the child's reported outcome. Return the child's exit code.
		fmt.Fprintln(stderr, "timesheet metrics run: warning: metrics store unavailable; run not recorded:", err)
		return code
	}
	if _, err := store.WriteRun(rec); err != nil {
		// Same rule for a failed write: the child's outcome stands.
		fmt.Fprintln(stderr, "timesheet metrics run: warning: run not recorded:", err)
		return code
	}
	fmt.Fprintf(stdout, "live run %s state=%s end_to_end=%dms exit=%d\n",
		rec.RunID, rec.State, rec.EndToEnd.Value.Value, code)
	return code
}

// metricsCallCount returns the total number of calls across a day's runs.
func metricsCallCount(store *metrics.Store, date string) (int, error) {
	runs, err := store.ListRuns(date)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range runs {
		n += len(runs[i].Calls)
	}
	return n, nil
}

// metricsRecordSource builds the narrow workflow RecordSource the aggregate reads
// editorial usage from. The workflow store is the SAME store the rest of the CLI
// uses.
func metricsRecordSource(storageDir string) (metrics.RecordSource, error) {
	store, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// resolveMetricsDir defaults --metrics-dir to ~/.timesheet/metrics.
func resolveMetricsDir(dir string) string {
	if d := strings.TrimSpace(dir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "metrics")
	}
	return ".timesheet/metrics"
}

// resolveStorageDir defaults --storage-dir to ~/.timesheet/records (the same
// default the workflow CLI uses).
func resolveStorageDir(dir string) string {
	if d := strings.TrimSpace(dir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "records")
	}
	return ".timesheet/records"
}

// metricsDates resolves the report date or range (default: today).
func metricsDates(date, from, to string) ([]string, error) {
	if d := strings.TrimSpace(date); d != "" {
		if err := workflow.ValidateDate(d); err != nil {
			return nil, fmt.Errorf("invalid --date %q", d)
		}
		return []string{d}, nil
	}
	f, t := strings.TrimSpace(from), strings.TrimSpace(to)
	if f != "" || t != "" {
		if f == "" || t == "" {
			return nil, errors.New("--from and --to must be given together")
		}
		return dayRange(f, t)
	}
	return []string{time.Now().Format("2006-01-02")}, nil
}

// metricsSummary renders a Usage as a compact non-zero-hiding string.
func metricsSummary(u metrics.Usage) string {
	return fmt.Sprintf("in=%s out=%s cacheR=%s cacheW=%s reasoning=%s",
		formatMeas(u.Input), formatMeas(u.Output), formatMeas(u.CacheRead), formatMeas(u.CacheWrite), formatMeas(u.Reasoning))
}

func formatMeas(m workflow.Measurement) string {
	if !m.Known {
		return "unknown"
	}
	return fmt.Sprintf("%d", m.Value)
}
