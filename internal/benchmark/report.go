package benchmark

import (
	"fmt"
	"io"
	"strings"
)

// SummaryStats aggregates metrics across all recorded dates.
type SummaryStats struct {
	TotalDays         int
	AvgWallClockMS    float64
	MaxWallClockMS    int64
	AvgPeakRSSMB      float64
	MaxPeakRSSMB      float64
	AvgTotalTokens    float64
	MaxTotalTokens    int64
	MinTotalTokens    int64
	TotalTokensAllDay int64
	TotalEntries      int
	WithinBudgetDays  int
	BudgetPassRatePct float64
}

// ComputeSummary aggregates statistical performance over metric records.
func ComputeSummary(metrics []DailyMetric, ceiling int64) SummaryStats {
	if ceiling <= 0 {
		ceiling = DefaultTokenCeiling
	}
	if len(metrics) == 0 {
		return SummaryStats{}
	}

	var (
		sumWallClock int64
		maxWallClock int64
		sumRSS       float64
		maxRSS       float64
		sumTokens    int64
		maxTokens    int64
		minTokens    = metrics[0].TotalTokens
		sumEntries   int
		withinBudget int
	)

	for _, m := range metrics {
		sumWallClock += m.WallClockMS
		if m.WallClockMS > maxWallClock {
			maxWallClock = m.WallClockMS
		}

		sumRSS += m.PeakRSSMB
		if m.PeakRSSMB > maxRSS {
			maxRSS = m.PeakRSSMB
		}

		sumTokens += m.TotalTokens
		if m.TotalTokens > maxTokens {
			maxTokens = m.TotalTokens
		}
		if m.TotalTokens < minTokens {
			minTokens = m.TotalTokens
		}

		sumEntries += m.Entries
		if m.TotalTokens <= ceiling {
			withinBudget++
		}
	}

	n := float64(len(metrics))
	return SummaryStats{
		TotalDays:         len(metrics),
		AvgWallClockMS:    float64(sumWallClock) / n,
		MaxWallClockMS:    maxWallClock,
		AvgPeakRSSMB:      sumRSS / n,
		MaxPeakRSSMB:      maxRSS,
		AvgTotalTokens:    float64(sumTokens) / n,
		MaxTotalTokens:    maxTokens,
		MinTotalTokens:    minTokens,
		TotalTokensAllDay: sumTokens,
		TotalEntries:      sumEntries,
		WithinBudgetDays:  withinBudget,
		BudgetPassRatePct: (float64(withinBudget) / n) * 100.0,
	}
}

// RenderReport outputs a formatted summary table of all benchmark metrics and statistical aggregates.
func RenderReport(w io.Writer, metrics []DailyMetric) error {
	Assert(w != nil, AssertBenchmarkNilWriter)

	if len(metrics) == 0 {
		_, err := fmt.Fprintln(w, "No benchmark metrics recorded yet.")
		return err
	}

	var sb strings.Builder
	sb.WriteString("\n=== Empirical Benchmark Metrics Report ===\n\n")
	sb.WriteString(fmt.Sprintf("%-12s  %10s  %10s  %8s  %8s  %8s  %8s  %7s\n",
		"Date", "Wall(ms)", "RSS(MB)", "InTok", "OutTok", "CacheTok", "TotalTok", "Entries"))
	sb.WriteString(strings.Repeat("-", 84) + "\n")

	for _, m := range metrics {
		sb.WriteString(fmt.Sprintf("%-12s  %10d  %10.1f  %8d  %8d  %8d  %8d  %7d\n",
			m.Date,
			m.WallClockMS,
			m.PeakRSSMB,
			m.InputTokens,
			m.OutputTokens,
			m.CacheTokens,
			m.TotalTokens,
			m.Entries,
		))
	}

	sb.WriteString(strings.Repeat("-", 84) + "\n\n")

	stats := ComputeSummary(metrics, DefaultTokenCeiling)
	sb.WriteString("=== Aggregate Performance Summary ===\n")
	sb.WriteString(fmt.Sprintf("- Total days tracked        : %d\n", stats.TotalDays))
	sb.WriteString(fmt.Sprintf("- Total entries logged      : %d\n", stats.TotalEntries))
	sb.WriteString(fmt.Sprintf("- Average wall clock time   : %.1f ms (Peak: %d ms)\n", stats.AvgWallClockMS, stats.MaxWallClockMS))
	sb.WriteString(fmt.Sprintf("- Average peak RSS memory   : %.1f MB (Max: %.1f MB)\n", stats.AvgPeakRSSMB, stats.MaxPeakRSSMB))
	sb.WriteString(fmt.Sprintf("- Average daily token usage : %.1f tokens (Min: %d, Max: %d)\n", stats.AvgTotalTokens, stats.MinTotalTokens, stats.MaxTotalTokens))
	sb.WriteString(fmt.Sprintf("- Total LLM tokens consumed : %d tokens\n", stats.TotalTokensAllDay))
	sb.WriteString(fmt.Sprintf("- Token budget compliance   : %d/%d days (%.1f%%) <= %d tokens\n\n",
		stats.WithinBudgetDays, stats.TotalDays, stats.BudgetPassRatePct, DefaultTokenCeiling))

	_, err := fmt.Fprint(w, sb.String())
	return err
}
