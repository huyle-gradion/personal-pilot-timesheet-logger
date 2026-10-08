package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestCLI_InvalidDate_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-date=2026/10/08"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "invalid -date") {
		t.Errorf("expected invalid -date in error output, got: %s", errOut.String())
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
	if metrics[0].WallClockMS != 1420 || metrics[0].PeakRSSMB != 18.4 || metrics[0].Entries != 4 {
		t.Errorf("unexpected metric content: %+v", metrics[0])
	}
}

func TestCLI_Record_WithTranscript(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")
	fixturePath := filepath.Join("..", "..", "internal", "benchmark", "testdata", "sample_claude.jsonl")

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=2026-10-08",
		"-transcript=" + fixturePath,
		"-csv=" + csvPath,
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
	if metrics[0].TotalTokens != 455 || metrics[0].InputTokens != 285 || metrics[0].OutputTokens != 110 {
		t.Errorf("unexpected token counters parsed: %+v", metrics[0])
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

func TestCLI_Record_Errors(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	// 1. Invalid transcript file
	var out, errOut bytes.Buffer
	args1 := []string{
		"-record",
		"-date=2026-10-08",
		"-transcript=" + filepath.Join(tempDir, "missing.jsonl"),
		"-csv=" + csvPath,
	}
	code1 := run(context.Background(), args1, strings.NewReader(""), &out, &errOut)
	if code1 != ExitErr {
		t.Errorf("expected ExitErr for missing transcript, got %d", code1)
	}

	// 2. Invalid run command
	out.Reset()
	errOut.Reset()
	args2 := []string{
		"-record",
		"-date=2026-10-08",
		"-run=non-existent-binary-123456",
		"-csv=" + csvPath,
	}
	code2 := run(context.Background(), args2, strings.NewReader(""), &out, &errOut)
	if code2 != ExitErr {
		t.Errorf("expected ExitErr for invalid command, got %d", code2)
	}
}

func TestCLI_Record_AutoDetectEntriesFromStorage(t *testing.T) {
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "records")
	csvPath := filepath.Join(tempDir, "metrics.csv")
	targetDate := "2026-10-08"

	store, err := storage.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatal(err)
	}

	rec := storage.TimesheetRecord{
		Date:      targetDate,
		UpdatedAt: time.Now().UTC(),
		Blocks: []timesheet.TimeBlock{
			{ID: "b1", Date: targetDate},
			{ID: "b2", Date: targetDate},
			{ID: "b3", Date: targetDate},
		},
	}
	if err := store.SaveDay(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-record",
		"-date=" + targetDate,
		"-storage-dir=" + storageDir,
		"-csv=" + csvPath,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	metrics, err := benchmark.ReadMetrics(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].Entries != 3 {
		t.Errorf("expected 3 entries auto-detected from storage, got %+v", metrics)
	}
}

func TestCLI_ChartAndReportModes(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics.csv")

	m := benchmark.DailyMetric{
		Date:        "2026-10-08",
		TotalTokens: 420,
		Entries:     2,
	}
	if err := benchmark.AppendMetric(csvPath, m); err != nil {
		t.Fatal(err)
	}

	// 1. Chart mode
	var out1, errOut1 bytes.Buffer
	code1 := run(context.Background(), []string{"-chart", "-csv=" + csvPath}, strings.NewReader(""), &out1, &errOut1)
	if code1 != ExitOK {
		t.Fatalf("expected ExitOK for chart, got %d (err: %s)", code1, errOut1.String())
	}
	if !strings.Contains(out1.String(), "Token Consumption Trend") {
		t.Errorf("expected chart header in stdout, got: %s", out1.String())
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

func TestCLI_ChartAndReport_ReadErrors(t *testing.T) {
	tempDir := t.TempDir()
	// Directory path instead of file path will cause ReadMetrics to fail
	dirAsCSV := filepath.Join(tempDir, "is_a_dir")
	if err := os.MkdirAll(dirAsCSV, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Chart read error
	var out1, errOut1 bytes.Buffer
	code1 := run(context.Background(), []string{"-chart", "-csv=" + dirAsCSV}, strings.NewReader(""), &out1, &errOut1)
	if code1 != ExitErr {
		t.Errorf("expected ExitErr (1) when reading metrics for chart fails, got %d", code1)
	}
	if !strings.Contains(errOut1.String(), "error: reading metrics") {
		t.Errorf("expected reading metrics error in errOut, got: %s", errOut1.String())
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

func TestCLI_Record_AppendError(t *testing.T) {
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
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Errorf("expected ExitErr (1) when appending metric fails, got %d", code)
	}
	if !strings.Contains(errOut.String(), "error: appending metric") {
		t.Errorf("expected appending metric error in errOut, got: %s", errOut.String())
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
