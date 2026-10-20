package benchmark

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// SummaryStats aggregates metrics across all recorded dates. Unknown (nil) daily
// values are EXCLUDED from a numeric aggregate and reported as unknown; they are
// never treated as zero.
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
	// KnownTokenDays is the number of days with a KNOWN token total. When it is
	// zero, every token aggregate below is meaningless and is printed unknown.
	KnownTokenDays int
	TotalEntries   int
	// UnknownEntryDays counts days whose entry count is UNKNOWN. When it is
	// non-zero, the entry total is a lower bound and is printed unknown.
	UnknownEntryDays int
	// WithinBudgetDays counts KNOWN token days at or under the ceiling.
	// BudgetPassRatePct is a share of KNOWN days, never of all rows: a day with no
	// measured total is not a compliance failure and not a pass. When
	// KnownTokenDays is zero the rate is meaningless and the report prints unknown.
	WithinBudgetDays  int
	BudgetPassRatePct float64
}

// ComputeSummary aggregates statistical performance over metric records. A day
// whose token total is UNKNOWN is not counted as within budget: compliance is
// never claimed for a day nobody measured.
func ComputeSummary(metrics []DailyMetric, ceiling int64) SummaryStats {
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
		minTokens    int64
		knownDays    int
		sumEntries   int
		unknownEntry int
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

		// A nil total is EXCLUDED from every token aggregate, never zero-filled.
		if m.TotalTokens != nil {
			total := *m.TotalTokens
			sumTokens += total
			if knownDays == 0 || total > maxTokens {
				maxTokens = total
			}
			if knownDays == 0 || total < minTokens {
				minTokens = total
			}
			knownDays++
			if total <= ceiling {
				withinBudget++
			}
		}

		if m.Entries == nil {
			unknownEntry++
		} else {
			sumEntries += *m.Entries
		}
	}

	n := float64(len(metrics))
	avgTokens := 0.0
	if knownDays > 0 {
		avgTokens = float64(sumTokens) / float64(knownDays)
	}
	return SummaryStats{
		TotalDays:         len(metrics),
		AvgWallClockMS:    float64(sumWallClock) / n,
		MaxWallClockMS:    maxWallClock,
		AvgPeakRSSMB:      sumRSS / n,
		MaxPeakRSSMB:      maxRSS,
		AvgTotalTokens:    avgTokens,
		MaxTotalTokens:    maxTokens,
		MinTotalTokens:    minTokens,
		TotalTokensAllDay: sumTokens,
		KnownTokenDays:    knownDays,
		TotalEntries:      sumEntries,
		UnknownEntryDays:  unknownEntry,
		WithinBudgetDays:  withinBudget,
		BudgetPassRatePct: budgetPassRate(withinBudget, knownDays),
	}
}

// budgetPassRate is the share of KNOWN days within the ceiling. A day with no
// measured total is neither a pass nor a failure, so it is not in the
// denominator; with zero known days the rate is 0 and the report says unknown.
func budgetPassRate(withinBudget, knownDays int) float64 {
	if knownDays == 0 {
		return 0
	}
	return (float64(withinBudget) / float64(knownDays)) * 100.0
}

// displayOptionalInt64 prints `unknown` for an UNKNOWN (nil) token cell. The
// report never prints 0 for a day nobody measured.
func displayOptionalInt64(v *int64) string {
	if v == nil {
		return "unknown"
	}
	return strconv.FormatInt(*v, 10)
}

func displayOptionalInt(v *int) string {
	if v == nil {
		return "unknown"
	}
	return strconv.Itoa(*v)
}

// RenderReport outputs a formatted summary table of all benchmark metrics and
// statistical aggregates. ceiling is the daily ceiling for the editorial-child
// token lane; it must be positive (main supplies workflow.EditorialTokenCeiling
// by default). An unknown token total prints `unknown`, never 0, and is never
// counted as within budget.
func RenderReport(w io.Writer, metrics []DailyMetric, ceiling int64) error {
	Assert(w != nil, AssertBenchmarkNilWriter)

	if ceiling <= 0 {
		return fmt.Errorf("report: ceiling must be positive, got %d", ceiling)
	}

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
		sb.WriteString(fmt.Sprintf("%-12s  %10d  %10.1f  %8s  %8s  %8s  %8s  %7s\n",
			m.Date,
			m.WallClockMS,
			m.PeakRSSMB,
			displayOptionalInt64(m.InputTokens),
			displayOptionalInt64(m.OutputTokens),
			displayOptionalInt64(m.CacheTokens),
			displayOptionalInt64(m.TotalTokens),
			displayOptionalInt(m.Entries),
		))
	}

	sb.WriteString(strings.Repeat("-", 84) + "\n\n")

	stats := ComputeSummary(metrics, ceiling)
	sb.WriteString("=== Aggregate Performance Summary ===\n")
	sb.WriteString(fmt.Sprintf("- Total days tracked        : %d\n", stats.TotalDays))
	if stats.UnknownEntryDays > 0 {
		sb.WriteString("- Total entries logged      : unknown\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Total entries logged      : %d\n", stats.TotalEntries))
	}
	sb.WriteString(fmt.Sprintf("- Average wall clock time   : %.1f ms (Peak: %d ms)\n", stats.AvgWallClockMS, stats.MaxWallClockMS))
	sb.WriteString(fmt.Sprintf("- Average peak RSS memory   : %.1f MB (Max: %.1f MB)\n", stats.AvgPeakRSSMB, stats.MaxPeakRSSMB))
	if stats.KnownTokenDays == 0 {
		sb.WriteString("- Average daily token usage : unknown\n")
		sb.WriteString("- Total LLM tokens consumed : unknown\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Average daily token usage : %.1f tokens (Min: %d, Max: %d)\n", stats.AvgTotalTokens, stats.MinTotalTokens, stats.MaxTotalTokens))
		sb.WriteString(fmt.Sprintf("- Total LLM tokens consumed : %d tokens\n", stats.TotalTokensAllDay))
	}
	if stats.KnownTokenDays == 0 {
		sb.WriteString("- Token budget compliance   : unknown\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Token budget compliance   : %d/%d known days (%.1f%%) <= %d tokens\n\n",
			stats.WithinBudgetDays, stats.KnownTokenDays, stats.BudgetPassRatePct, ceiling))
	}

	_, err := fmt.Fprint(w, sb.String())
	return err
}
