package benchmark

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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

func TestBenchmark_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_BENCHMARK_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "empty_date_append":
			_ = UpsertMetric("test.csv", DailyMetric{Date: ""})
		case "nil_writer_chart":
			_ = RenderASCIIChart(nil, []DailyMetric{}, 1000)
		case "nil_writer_report":
			_ = RenderReport(nil, []DailyMetric{}, 1000)
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
