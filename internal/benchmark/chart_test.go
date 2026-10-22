package benchmark

import (
	"bytes"
	"strings"
	"testing"
)

// chartLine returns the rendered row for date, so a test can measure one day's
// bar without the title/legend text interfering.
func chartLine(t *testing.T, out, date string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, date) {
			return line
		}
	}
	t.Fatalf("chart has no row for %s:\n%s", date, out)
	return ""
}

// TestChartCeiling_SnapMath pins the derived ceiling: the largest KNOWN day total
// rounded UP to the next multiple of 1,000, with an exact multiple staying put.
func TestChartCeiling_SnapMath(t *testing.T) {
	cases := []struct {
		name string
		max  int64
		want int64
	}{
		{"observed-peak", 4757111, 4758000},
		{"exact-multiple-stays", 4758000, 4758000},
		{"below-one-grid", 126, 1000},
		{"just-over-a-grid", 1001, 2000},
		{"at-the-grid", 1000, 1000},
		{"known-zero-is-known", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, known := ChartCeiling([]DailyMetric{{Date: "2026-10-12", TotalTokens: ptrInt64(tc.max)}})
			if !known {
				t.Fatalf("a known total must yield a ceiling")
			}
			if got != tc.want {
				t.Fatalf("ChartCeiling(%d) = %d, want %d", tc.max, got, tc.want)
			}
		})
	}

	// Unknown days never contribute to the peak.
	got, known := ChartCeiling([]DailyMetric{
		{Date: "2026-10-11", TotalTokens: ptrInt64(2000)},
		{Date: "2026-10-12"},
	})
	if !known || got != 2000 {
		t.Fatalf("an unknown day must not raise the ceiling: got %d known=%v", got, known)
	}
}

// TestChartRenderer_PeakIsFullBarAndOthersScale pins the 2026-10-12 shape: a
// 4,757,111 peak snaps to 4,758,000, the peak is the full 40-char bar, other days
// scale proportionally (rounded half up), and a tiny day bottoms out at 0 chars.
func TestChartRenderer_PeakIsFullBarAndOthersScale(t *testing.T) {
	var buf bytes.Buffer
	rows := []DailyMetric{
		{Date: "2026-10-11", TotalTokens: ptrInt64(2379000)},
		{Date: "2026-10-12", TotalTokens: ptrInt64(4757111)},
		{Date: "2026-10-13", TotalTokens: ptrInt64(1)},
		{Date: "2026-10-14"},
	}
	if err := RenderASCIIChart(&buf, rows, 0); err != nil {
		t.Fatalf("chart: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(Ceiling: 4,758,000 tokens)") {
		t.Fatalf("the title must render the snapped ceiling verbatim:\n%s", out)
	}
	if !strings.Contains(out, "Legend: [█] session tokens — bar length scales to the ceiling; the top day fills the bar; unknown days draw no bar.") {
		t.Fatalf("the legend must state the real rule, including the top-day marker:\n%s", out)
	}
	if n := strings.Count(chartLine(t, out, "2026-10-12"), "█"); n != chartBarWidth {
		t.Errorf("the peak day must be the full %d-char bar, got %d", chartBarWidth, n)
	}
	if n := strings.Count(chartLine(t, out, "2026-10-11"), "█"); n != 20 {
		t.Errorf("2379000/4758000 of 40 chars must be 20, got %d", n)
	}
	if n := strings.Count(chartLine(t, out, "2026-10-13"), "█"); n != 0 {
		t.Errorf("a tiny value must bottom out at 0 chars, got %d", n)
	}
	if line := chartLine(t, out, "2026-10-13"); !strings.Contains(line, "1 ") && !strings.HasSuffix(line, "1") {
		t.Errorf("a tiny KNOWN day must still print its token count: %q", line)
	}
	if line := chartLine(t, out, "2026-10-14"); !strings.Contains(line, "unknown") || strings.Contains(line, "█") {
		t.Errorf("an unknown day must print unknown and draw no bar: %q", line)
	}
}

// TestChartRenderer_ExactMultiplePeak pins that a peak already on the 1,000 grid
// keeps its value (no gratuitous extra 1,000).
func TestChartRenderer_ExactMultiplePeak(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderASCIIChart(&buf, []DailyMetric{{Date: "2026-10-12", TotalTokens: ptrInt64(4758000)}}, 0); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(Ceiling: 4,758,000 tokens)") {
		t.Fatalf("an exact multiple must stay: %s", buf.String())
	}
}

// TestChartRenderer_BelowGridPeakIsStillFullBar pins the small-range case: a
// 100-token peak snaps to 1,000, and the PEAK is still the full bar because it is
// the day the ceiling was snapped from.
func TestChartRenderer_BelowGridPeakIsStillFullBar(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderASCIIChart(&buf, []DailyMetric{{Date: "2026-10-08", TotalTokens: ptrInt64(100)}}, 0); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(Ceiling: 1,000 tokens)") {
		t.Fatalf("a 100 peak must snap to 1,000: %s", out)
	}
	if n := strings.Count(chartLine(t, out, "2026-10-08"), "█"); n != chartBarWidth {
		t.Fatalf("the peak must be the full bar even below the grid, got %d", n)
	}
}

// TestChartRenderer_KnownZeroPeak pins the measured-zero edge: a known 0 total is
// KNOWN, yields a 0 ceiling and zero bars, and is never confused with unknown.
func TestChartRenderer_KnownZeroPeak(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderASCIIChart(&buf, []DailyMetric{{Date: "2026-10-08", TotalTokens: ptrInt64(0)}}, 0); err != nil {
		t.Fatalf("a known zero peak is not an error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(Ceiling: 0 tokens)") {
		t.Fatalf("a known zero peak must state a 0 ceiling:\n%s", out)
	}
	line := chartLine(t, out, "2026-10-08")
	if strings.Contains(line, "█") {
		t.Fatalf("a 0-token day must draw no bar: %q", line)
	}
	if strings.Contains(line, "unknown") {
		t.Fatalf("a measured 0 is known, never unknown: %q", line)
	}
	if !strings.HasSuffix(line, "0") {
		t.Fatalf("a measured 0 must still print its token count: %q", line)
	}
}

// TestChartRenderer_UnknownOnlyRange pins the honest no-bar chart: no known total
// means an unknown ceiling, no bars, and no invented scale.
func TestChartRenderer_UnknownOnlyRange(t *testing.T) {
	var buf bytes.Buffer
	rows := []DailyMetric{{Date: "2026-10-10"}, {Date: "2026-10-11", TotalTokens: nil}}
	if err := RenderASCIIChart(&buf, rows, 0); err != nil {
		t.Fatalf("an unmeasured range is not an error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(Ceiling: unknown)") {
		t.Fatalf("an unknown ceiling must be stated in the title:\n%s", out)
	}
	if strings.Contains(out, "██") || strings.Contains(out, "░") {
		t.Fatalf("an unknown-only chart must draw no bar:\n%s", out)
	}
	for _, date := range []string{"2026-10-10", "2026-10-11"} {
		if line := chartLine(t, out, date); !strings.Contains(line, "unknown") {
			t.Fatalf("every unmeasured day must print unknown: %q", line)
		}
	}
}

// TestChartRenderer_FixedCeiling has two pinned behaviours of the -ceiling
// override: bars scale to the caller's number, and a day over it clamps to a full
// bar with NO overage marker or budget language.
func TestChartRenderer_FixedCeiling(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderASCIIChart(&buf, []DailyMetric{{Date: "2026-10-08", TotalTokens: ptrInt64(730)}}, 2000); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(chartLine(t, buf.String(), "2026-10-08"), "█"); n != 15 {
		t.Errorf("730/2000 of 40 chars must round to 15, got %d", n)
	}

	buf.Reset()
	if err := RenderASCIIChart(&buf, []DailyMetric{{Date: "2026-10-08", TotalTokens: ptrInt64(900)}}, 500); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(Ceiling: 500 tokens)") {
		t.Fatalf("the fixed ceiling must be stated verbatim:\n%s", out)
	}
	if n := strings.Count(chartLine(t, out, "2026-10-08"), "█"); n != chartBarWidth {
		t.Errorf("an over-ceiling day must be a full bar, got %d", n)
	}
	for _, forbidden := range []string{"[OVER]", "[PASS]", "[n/a]", "!", "░", "budget", "Budget"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("fixed-ceiling output must carry %q nowhere:\n%s", forbidden, out)
		}
	}
}
