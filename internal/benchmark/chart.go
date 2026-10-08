package benchmark

import (
	"fmt"
	"io"
	"strings"
)

const (
	chartBarWidth = 40
)

// RenderASCIIChart prints a terminal ASCII bar chart representing daily token consumption
// measured against the specified daily ceiling (defaulting to DefaultTokenCeiling if <= 0).
func RenderASCIIChart(w io.Writer, metrics []DailyMetric, ceiling int64) error {
	Assert(w != nil, AssertBenchmarkNilWriter)

	if ceiling <= 0 {
		ceiling = DefaultTokenCeiling
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

		pct := float64(m.TotalTokens) / float64(ceiling) * 100.0

		if m.TotalTokens <= ceiling {
			bars := int((float64(m.TotalTokens) / float64(ceiling)) * chartBarWidth)
			if bars > chartBarWidth {
				bars = chartBarWidth
			}
			remaining := chartBarWidth - bars
			bar = strings.Repeat("█", bars) + strings.Repeat("░", remaining)
			status = fmt.Sprintf("[PASS] %5.1f%%", pct)
		} else {
			excessTokens := m.TotalTokens - ceiling
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

		sb.WriteString(fmt.Sprintf("%-12s  %-8d  %-42s  %s\n", m.Date, m.TotalTokens, bar, status))
	}

	sb.WriteString(strings.Repeat("-", 78) + "\n")
	sb.WriteString(fmt.Sprintf("Legend: [█] Consumed tokens  [░] Available budget  [!] Ceiling overage (>%d)\n\n", ceiling))

	_, err := fmt.Fprint(w, sb.String())
	return err
}
