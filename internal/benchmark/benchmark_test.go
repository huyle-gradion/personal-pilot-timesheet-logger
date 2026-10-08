package benchmark

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTranscript_ClaudeFixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "sample_claude.jsonl")
	usage, err := ParseTranscriptFile(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("failed to parse Claude fixture: %v", err)
	}

	expectedInput := int64(285)  // 250 + 35
	expectedOutput := int64(110) // 95 + 15
	expectedCache := int64(60)   // 50 + 10
	expectedTotal := int64(455)  // 285 + 110 + 60

	if usage.InputTokens != expectedInput {
		t.Errorf("expected input tokens %d, got %d", expectedInput, usage.InputTokens)
	}
	if usage.OutputTokens != expectedOutput {
		t.Errorf("expected output tokens %d, got %d", expectedOutput, usage.OutputTokens)
	}
	if usage.CacheTokens != expectedCache {
		t.Errorf("expected cache tokens %d, got %d", expectedCache, usage.CacheTokens)
	}
	if usage.TotalTokens != expectedTotal {
		t.Errorf("expected total tokens %d, got %d", expectedTotal, usage.TotalTokens)
	}
}

func TestParseTranscript_AntigravityFixture(t *testing.T) {
	fixturePath := filepath.Join("testdata", "sample_antigravity.jsonl")
	usage, err := ParseTranscriptFile(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("failed to parse Antigravity fixture: %v", err)
	}

	expectedInput := int64(200)  // 120 + 80
	expectedOutput := int64(70)  // 45 + 25
	expectedCache := int64(40)   // 30 + 10
	expectedTotal := int64(310)

	if usage.InputTokens != expectedInput {
		t.Errorf("expected input tokens %d, got %d", expectedInput, usage.InputTokens)
	}
	if usage.OutputTokens != expectedOutput {
		t.Errorf("expected output tokens %d, got %d", expectedOutput, usage.OutputTokens)
	}
	if usage.CacheTokens != expectedCache {
		t.Errorf("expected cache tokens %d, got %d", expectedCache, usage.CacheTokens)
	}
	if usage.TotalTokens != expectedTotal {
		t.Errorf("expected total tokens %d, got %d", expectedTotal, usage.TotalTokens)
	}
}

func TestParseTranscript_NestedResponseAndEdgeCases(t *testing.T) {
	raw := `
	not valid json
	{"event": "start"}
	{"response": {"usage": {"input_tokens": 50, "output_tokens": 20, "cache_read_input_tokens": 10}}}
	{"usage": {"input_tokens": 10, "output_tokens": 5, "cache_read_tokens": 5}}
	`
	usage, err := ParseTranscript(context.Background(), strings.NewReader(raw))
	if err != nil {
		t.Fatalf("unexpected error parsing transcript: %v", err)
	}

	if usage.InputTokens != 60 || usage.OutputTokens != 25 || usage.CacheTokens != 15 || usage.TotalTokens != 100 {
		t.Errorf("unexpected usage parsed: %+v", usage)
	}
}

func TestParseTranscript_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := ParseTranscript(ctx, strings.NewReader(`{"input_tokens": 100}`))
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
}

func TestParseTranscriptFile_Errors(t *testing.T) {
	// 1. Non-existent file
	_, err := ParseTranscriptFile(context.Background(), "non_existent_file.jsonl")
	if err == nil {
		t.Errorf("expected error for non-existent file, got nil")
	}

	// 2. Directory as file
	tempDir := t.TempDir()
	_, err = ParseTranscriptFile(context.Background(), tempDir)
	if err == nil {
		t.Errorf("expected error when directory passed as file, got nil")
	}
}

func TestRusageToMB(t *testing.T) {
	if got := RusageToMB(-1); got != 0.0 {
		t.Errorf("expected 0.0 for negative rusage, got %f", got)
	}
	if got := RusageToMB(0); got != 0.0 {
		t.Errorf("expected 0.0 for zero rusage, got %f", got)
	}
	if got := RusageToMB(1048576); got <= 0.0 {
		t.Errorf("expected positive MB for 1048576, got %f", got)
	}
}

func TestCurrentProcessRSSMB(t *testing.T) {
	rss := CurrentProcessRSSMB()
	if rss <= 0.0 {
		t.Errorf("expected positive RSS MB for current process, got %f", rss)
	}
}

func TestMeasureCommand(t *testing.T) {
	// Success command
	res, err := MeasureCommand(context.Background(), "echo", "hello-benchmark")
	if err != nil {
		t.Fatalf("unexpected error executing echo: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello-benchmark") {
		t.Errorf("expected stdout to contain hello-benchmark, got: %s", res.Stdout)
	}
	if res.WallClockMS < 0 {
		t.Errorf("expected non-negative wall clock ms, got: %d", res.WallClockMS)
	}
	if res.PeakRSSMB <= 0 {
		t.Errorf("expected positive peak RSS MB, got: %f", res.PeakRSSMB)
	}

	// Failure command
	_, err = MeasureCommand(context.Background(), "some-invalid-binary-xyz-123")
	if err == nil {
		t.Fatalf("expected error executing invalid command, got nil")
	}
}

func TestCSVStore_AppendAndRead(t *testing.T) {
	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "metrics", "test_metrics.csv")

	m1 := DailyMetric{
		Date:         "2026-10-07",
		WallClockMS:  1420,
		PeakRSSMB:    18.4,
		InputTokens:  285,
		OutputTokens: 110,
		CacheTokens:  0,
		TotalTokens:  395,
		Entries:      4,
	}

	// 1. First append creates file and writes header
	if err := AppendMetric(csvPath, m1); err != nil {
		t.Fatalf("failed to append first metric: %v", err)
	}

	content, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("failed to read created csv: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (header + 1 row), got %d:\n%s", len(lines), string(content))
	}
	if lines[0] != CSVHeader {
		t.Errorf("expected header %q, got %q", CSVHeader, lines[0])
	}
	if !strings.Contains(lines[1], "2026-10-07,1420,18.4,285,110,0,395,4") {
		t.Errorf("unexpected row line: %s", lines[1])
	}

	// 2. Second append adds row without duplicating header
	m2 := DailyMetric{
		Date:         "2026-10-08",
		WallClockMS:  980,
		PeakRSSMB:    16.2,
		InputTokens:  190,
		OutputTokens: 85,
		CacheTokens:  25,
		TotalTokens:  300,
		Entries:      3,
	}
	if err := AppendMetric(csvPath, m2); err != nil {
		t.Fatalf("failed to append second metric: %v", err)
	}

	// 3. Read back
	metrics, err := ReadMetrics(csvPath)
	if err != nil {
		t.Fatalf("failed to read metrics from csv: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics read back, got %d", len(metrics))
	}
	if metrics[0].Date != "2026-10-07" || metrics[0].TotalTokens != 395 {
		t.Errorf("mismatch on metric 0: %+v", metrics[0])
	}
	if metrics[1].Date != "2026-10-08" || metrics[1].TotalTokens != 300 {
		t.Errorf("mismatch on metric 1: %+v", metrics[1])
	}

	// 4. Read non-existent file returns nil, nil
	none, err := ReadMetrics(filepath.Join(tempDir, "does-not-exist.csv"))
	if err != nil || none != nil {
		t.Errorf("expected nil, nil for non-existent file, got %v, %v", none, err)
	}
}

func TestChartRenderer(t *testing.T) {
	var buf bytes.Buffer

	// Empty metrics
	if err := RenderASCIIChart(&buf, nil, 600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No benchmark metrics recorded yet") {
		t.Errorf("expected empty message, got: %s", buf.String())
	}

	buf.Reset()
	metrics := []DailyMetric{
		{
			Date:        "2026-10-07",
			TotalTokens: 395,
		},
		{
			Date:        "2026-10-08",
			TotalTokens: 650,
		},
	}

	if err := RenderASCIIChart(&buf, metrics, 600); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "[PASS]") {
		t.Errorf("expected PASS badge in chart, got: %s", out)
	}
	if !strings.Contains(out, "[OVER]") {
		t.Errorf("expected OVER badge in chart, got: %s", out)
	}
	if !strings.Contains(out, "+50 tokens") {
		t.Errorf("expected +50 tokens overage notice, got: %s", out)
	}
}

func TestReportRenderer(t *testing.T) {
	var buf bytes.Buffer

	// Empty metrics
	if err := RenderReport(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No benchmark metrics recorded yet") {
		t.Errorf("expected empty message, got: %s", buf.String())
	}

	buf.Reset()
	metrics := []DailyMetric{
		{
			Date:         "2026-10-06",
			WallClockMS:  1200,
			PeakRSSMB:    15.0,
			InputTokens:  200,
			OutputTokens: 100,
			CacheTokens:  0,
			TotalTokens:  300,
			Entries:      3,
		},
		{
			Date:         "2026-10-07",
			WallClockMS:  1400,
			PeakRSSMB:    18.0,
			InputTokens:  250,
			OutputTokens: 100,
			CacheTokens:  50,
			TotalTokens:  400,
			Entries:      4,
		},
	}

	if err := RenderReport(&buf, metrics); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Total days tracked        : 2") {
		t.Errorf("expected 2 days tracked, got: %s", out)
	}
	if !strings.Contains(out, "Total entries logged      : 7") {
		t.Errorf("expected 7 entries logged, got: %s", out)
	}
	if !strings.Contains(out, "100.0%") {
		t.Errorf("expected 100.0%% compliance, got: %s", out)
	}
}

func TestBenchmark_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_BENCHMARK_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "nil_ctx_parse":
			_, _ = ParseTranscript(nil, strings.NewReader(""))
		case "nil_reader_parse":
			_, _ = ParseTranscript(context.Background(), nil)
		case "empty_date_append":
			_ = AppendMetric("test.csv", DailyMetric{Date: ""})
		case "nil_writer_chart":
			_ = RenderASCIIChart(nil, []DailyMetric{}, 600)
		case "nil_writer_report":
			_ = RenderReport(nil, []DailyMetric{})
		case "nil_ctx_measure":
			_, _ = MeasureCommand(nil, "echo")
		}
		return
	}

	cases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"nil_ctx_parse", "nil_ctx_parse", AssertBenchmarkContextNil},
		{"nil_reader_parse", "nil_reader_parse", AssertBenchmarkNilReader},
		{"empty_date_append", "empty_date_append", AssertBenchmarkDateEmpty},
		{"nil_writer_chart", "nil_writer_chart", AssertBenchmarkNilWriter},
		{"nil_writer_report", "nil_writer_report", AssertBenchmarkNilWriter},
		{"nil_ctx_measure", "nil_ctx_measure", AssertBenchmarkContextNil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestBenchmark_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_BENCHMARK_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
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
