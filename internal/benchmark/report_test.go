package benchmark

import (
	"bytes"
	"strings"
	"testing"
)

func TestChartRenderer(t *testing.T) {
	var buf bytes.Buffer

	// Empty metrics
	if err := RenderASCIIChart(&buf, nil, 0); err != nil {
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

	// ceiling 0 DERIVES the ceiling: the 1,050 peak snaps up to 2,000, and the peak
	// day is the full bar.
	if err := RenderASCIIChart(&buf, metrics, 0); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Ceiling: 2,000 tokens") {
		t.Errorf("expected the snapped ceiling in the chart header, got: %s", out)
	}
	if strings.Contains(out, "[PASS]") || strings.Contains(out, "[OVER]") || strings.Contains(out, "budget") {
		t.Errorf("the chart must carry no verdict or budget language, got: %s", out)
	}
	if !strings.Contains(out, "2026-10-08    1050      "+strings.Repeat("█", 40)) {
		t.Errorf("the peak day must be the full 40-char bar, got: %s", out)
	}
	if !strings.Contains(out, "2026-10-07    995       "+strings.Repeat("█", 20)) {
		t.Errorf("other days must scale proportionally (995/2000 -> 20), got: %s", out)
	}

	// A fixed ceiling scales to the caller's number instead of the data.
	buf.Reset()
	if err := RenderASCIIChart(&buf, metrics, 1000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Ceiling: 1,000 tokens") {
		t.Errorf("expected the fixed ceiling in the chart header, got: %s", buf.String())
	}

	// A NEGATIVE ceiling is refused, never silently defaulted.
	if err := RenderASCIIChart(&buf, metrics, -1); err == nil {
		t.Error("a negative ceiling must be refused")
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
