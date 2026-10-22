package benchmark

import (
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	chartBarWidth = 40
	// chartCeilingGrid is the step the data-derived ceiling snaps UP to.
	chartCeilingGrid = int64(1000)
)

// ChartCeiling returns the chart's ceiling for metrics: the LARGEST known day
// total snapped UP to the next multiple of 1,000 (a total that is already a
// multiple stays). hasKnown is false when no charted day carries a known total;
// there is then no ceiling to derive and callers must not invent one.
func ChartCeiling(metrics []DailyMetric) (ceiling int64, hasKnown bool) {
	max, hasKnown := chartKnownMax(metrics)
	if !hasKnown {
		return 0, false
	}
	if max <= 0 {
		// A measured zero needs no scale; it is known, not unknown.
		return 0, true
	}
	return ((max + chartCeilingGrid - 1) / chartCeilingGrid) * chartCeilingGrid, true
}

// chartKnownMax is the largest KNOWN day total, excluding unknown (nil) days.
func chartKnownMax(metrics []DailyMetric) (max int64, hasKnown bool) {
	for _, m := range metrics {
		if m.TotalTokens == nil {
			continue
		}
		if !hasKnown || *m.TotalTokens > max {
			max = *m.TotalTokens
		}
		hasKnown = true
	}
	return max, hasKnown
}

// RenderASCIIChart prints a terminal ASCII bar chart of the daily harness
// session tokens. The ceiling argument has three honest modes:
//
//   - ceiling > 0: a FIXED ceiling supplied by the caller (cmd's -ceiling). Bars
//     scale proportionally to it; a day over it clamps to a full bar and no
//     overage marker or verdict is rendered.
//   - ceiling == 0: DERIVED from the data (ChartCeiling). The largest known day
//     is the full 40-char bar and every other day scales proportionally to the
//     snapped ceiling; with no known total the title says the ceiling is unknown.
//   - ceiling < 0: refused.
//
// A day whose token total is UNKNOWN takes the nil path: its bar is OMITTED and
// its Tokens cell prints `unknown`. It is never a dereference, never a zero-fill
// and never a `0 <= ceiling` tick for a day nobody measured. Bar length is
// rounded half up and clamped to [0, 40], so a tiny value may bottom out at 0
// chars; no `░` remainder track is drawn, because nothing here is a budget.
func RenderASCIIChart(w io.Writer, metrics []DailyMetric, ceiling int64) error {
	Assert(w != nil, AssertBenchmarkNilWriter)

	if ceiling < 0 {
		return fmt.Errorf("chart: ceiling must not be negative, got %d", ceiling)
	}

	if len(metrics) == 0 {
		_, err := fmt.Fprintln(w, "No benchmark metrics recorded yet.")
		return err
	}

	derived := ceiling == 0
	effective := ceiling
	maxKnown, hasKnown := chartKnownMax(metrics)
	if derived {
		effective, hasKnown = ChartCeiling(metrics)
	}

	var sb strings.Builder
	if derived && !hasKnown {
		sb.WriteString("\n=== Daily LLM Token Consumption Trend (Ceiling: unknown) ===\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("\n=== Daily LLM Token Consumption Trend (Ceiling: %s tokens) ===\n\n", FormatTokens(effective)))
	}
	sb.WriteString(fmt.Sprintf("%-12s  %-8s  %s\n", "Date", "Tokens", "Bar (40 chars)"))
	sb.WriteString(strings.Repeat("-", 64) + "\n")

	for _, m := range metrics {
		if m.TotalTokens == nil {
			row := fmt.Sprintf("%-12s  %-8s", m.Date, "unknown")
			sb.WriteString(strings.TrimRight(row, " ") + "\n")
			continue
		}
		total := *m.TotalTokens
		// In the derived mode the PEAK day is the reference the ceiling was
		// snapped from, so it is always the full bar even when the snap rose
		// above it. That full bar is a MARKER, not a proportion; the title carries
		// the real ceiling. In fixed mode nothing is the "peak": the flag's number is.
		bars := chartBars(total, effective, maxKnown, derived && hasKnown)
		row := fmt.Sprintf("%-12s  %-8d  %s", m.Date, total, strings.Repeat("█", bars))
		sb.WriteString(strings.TrimRight(row, " ") + "\n")
	}

	sb.WriteString(strings.Repeat("-", 64) + "\n")
	sb.WriteString("Legend: [█] session tokens — bar length scales to the ceiling; the top day fills the bar; unknown days draw no bar.\n\n")

	_, err := fmt.Fprint(w, sb.String())
	return err
}

// chartBars is the filled-bar length for one known day: the full 40 chars for
// the peak in derived mode — a marker rather than a proportion, because the snap
// can put the real ceiling above the peak — otherwise total/ceiling*40 rounded
// half up and clamped to [0, 40]. A tiny value therefore bottoms out at 0 chars,
// and a day over a fixed ceiling is a full bar, never a marker.
func chartBars(total, ceiling, maxKnown int64, fullTop bool) int {
	if ceiling <= 0 {
		return 0
	}
	if fullTop && total == maxKnown {
		return chartBarWidth
	}
	bars := int(math.Round(float64(total) / float64(ceiling) * chartBarWidth))
	if bars < 0 {
		return 0
	}
	if bars > chartBarWidth {
		return chartBarWidth
	}
	return bars
}

// FormatTokens renders an integer with comma separators (4758000 ->
// "4,758,000"), so a ceiling reads verbatim wherever it is printed.
func FormatTokens(n int64) string {
	s := fmt.Sprintf("%d", n)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return sign + b.String()
}
