package benchmark

import (
	"bytes"
	"strings"
	"testing"
)

func TestChartRenderer(t *testing.T) {
	var buf bytes.Buffer

	// Empty metrics
	if err := RenderASCIIChart(&buf, nil, 1000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No benchmark metrics recorded yet") {
		t.Errorf("expected empty message, got: %s", buf.String())
	}

	buf.Reset()
	metrics := []DailyMetric{
		{
			Date:        "2026-10-07",
			TotalTokens: ptrInt64(995),
		},
		{
			Date:        "2026-10-08",
			TotalTokens: ptrInt64(1050),
		},
	}

	if err := RenderASCIIChart(&buf, metrics, 1000); err != nil {
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
	if !strings.Contains(out, "Ceiling: 1000 tokens") {
		t.Errorf("expected the ceiling in the chart header, got: %s", out)
	}

	// A non-positive ceiling is refused, never silently defaulted.
	if err := RenderASCIIChart(&buf, metrics, 0); err == nil {
		t.Error("a non-positive ceiling must be refused")
	}
}

func TestReportRenderer(t *testing.T) {
	var buf bytes.Buffer

	// Empty metrics
	if err := RenderReport(&buf, nil, 1000); err != nil {
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
			InputTokens:  ptrInt64(200),
			OutputTokens: ptrInt64(100),
			CacheTokens:  ptrInt64(0),
			TotalTokens:  ptrInt64(300),
			Entries:      ptrInt(3),
		},
		{
			Date:         "2026-10-07",
			WallClockMS:  1400,
			PeakRSSMB:    18.0,
			InputTokens:  ptrInt64(250),
			OutputTokens: ptrInt64(100),
			CacheTokens:  ptrInt64(50),
			TotalTokens:  ptrInt64(400),
			Entries:      ptrInt(4),
		},
	}

	if err := RenderReport(&buf, metrics, 1000); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Total days tracked        : 2") {
		t.Errorf("expected 2 days tracked, got: %s", out)
	}
	if !strings.Contains(out, "Total entries logged      : 7") {
		t.Errorf("expected 7 entries logged, got: %s", out)
	}
	if !strings.Contains(out, "2/2 known days (100.0%) <= 1000 tokens") {
		t.Errorf("expected compliance over KNOWN days with the ceiling, got: %s", out)
	}

	// Budget compliance for a day nobody measured is UNKNOWN: no percentage, no
	// `0/1 days (0.0%)` tick.
	buf.Reset()
	unknownOnly := []DailyMetric{{Date: "2026-10-08", WallClockMS: 100, PeakRSSMB: 1.0}}
	if err := RenderReport(&buf, unknownOnly, 1000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "- Token budget compliance   : unknown") {
		t.Errorf("expected an unknown compliance line, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "(0.0%)") {
		t.Errorf("an unknown day must never print a compliance percentage, got: %s", buf.String())
	}

	// A known day over the ceiling and a known day under it: shares are over
	// KNOWN days only.
	buf.Reset()
	mixed := []DailyMetric{
		{Date: "2026-10-08", TotalTokens: ptrInt64(500)},
		{Date: "2026-10-09", TotalTokens: ptrInt64(1500)},
		{Date: "2026-10-10"},
	}
	if err := RenderReport(&buf, mixed, 1000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "1/2 known days (50.0%) <= 1000 tokens") {
		t.Errorf("expected 1/2 known days at 50%%, got: %s", buf.String())
	}

	// A non-positive ceiling is refused, never silently defaulted.
	if err := RenderReport(&buf, metrics, 0); err == nil {
		t.Error("a non-positive ceiling must be refused")
	}
}
