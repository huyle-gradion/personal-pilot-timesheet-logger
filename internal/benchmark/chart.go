package benchmark

import (
	"fmt"
	"io"
	"strings"
)

const (
	chartBarWidth = 40
)

// RenderASCIIChart prints a terminal ASCII bar chart representing the daily
// editorial-child token lane measured against ceiling, which must be positive
// (main supplies workflow.EditorialTokenCeiling by default). A day whose token
// total is UNKNOWN takes the nil path: its bar is OMITTED and its status prints
// `unknown`. It is never a dereference, never a zero-fill and never a `0 <=
// ceiling` pass for a day nobody measured.
func RenderASCIIChart(w io.Writer, metrics []DailyMetric, ceiling int64) error {
	Assert(w != nil, AssertBenchmarkNilWriter)

	if ceiling <= 0 {
		return fmt.Errorf("chart: ceiling must be positive, got %d", ceiling)
	}

	if len(metrics) == 0 {
		_, err := fmt.Fprintln(w, "No benchmark metrics recorded yet.")
		return err
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\n=== Daily LLM Token Consumption Trend (Ceiling: %d tokens) ===\n\n", ceiling))
	sb.WriteString(fmt.Sprintf("%-12s  %-8s  %-42s  %s\n", "Date", "Tokens", "Budget Usage Bar (40 chars)", "Status"))
	sb.WriteString(strings.Repeat("-", 78) + "\n")

	for _, m := range metrics {
		var bar string
		var status string

		if m.TotalTokens == nil {
			sb.WriteString(fmt.Sprintf("%-12s  %-8s  %-42s  %s\n", m.Date, "unknown", "", "unknown"))
			continue
		}
		total := *m.TotalTokens
		pct := float64(total) / float64(ceiling) * 100.0

		if total <= ceiling {
			bars := int((float64(total) / float64(ceiling)) * chartBarWidth)
			if bars > chartBarWidth {
				bars = chartBarWidth
			}
			remaining := chartBarWidth - bars
			bar = strings.Repeat("█", bars) + strings.Repeat("░", remaining)
			status = fmt.Sprintf("[PASS] %5.1f%%", pct)
		} else {
			excessTokens := total - ceiling
			excessChars := int((float64(excessTokens) / float64(ceiling)) * chartBarWidth)
			if excessChars < 1 {
				excessChars = 1
			}
			if excessChars > 15 {
				excessChars = 15
			}
			bar = strings.Repeat("█", chartBarWidth) + strings.Repeat("!", excessChars)
			status = fmt.Sprintf("[OVER] +%d tokens (%5.1f%%)", excessTokens, pct)
		}

		sb.WriteString(fmt.Sprintf("%-12s  %-8d  %-42s  %s\n", m.Date, total, bar, status))
	}

	sb.WriteString(strings.Repeat("-", 78) + "\n")
	sb.WriteString(fmt.Sprintf("Legend: [█] Consumed tokens  [░] Available budget  [!] Ceiling overage (>%d)\n\n", ceiling))

	_, err := fmt.Fprint(w, sb.String())
	return err
}
