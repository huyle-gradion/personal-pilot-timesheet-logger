package benchmark

const (
	// MaxCSVRowsCount is the historical row bound for the CSV store. It no longer
	// bounds reads or rewrites: truncating the store at 1000 rows would delete
	// recorded history, and an upsert must preserve every recorded day. It is kept
	// as the reference bound the truncation regression crosses.
	MaxCSVRowsCount = 1000

	// DefaultCSVPath points to the default CSV metrics store.
	DefaultCSVPath = "benchmarks/metrics.csv"
)

// Invariant assertion identifiers for TigerStyle runtime validation.
const (
	AssertBenchmarkContextNil = "ASSERT_BENCHMARK_001_NIL_CONTEXT"
	AssertBenchmarkDateEmpty  = "ASSERT_BENCHMARK_002_EMPTY_DATE"
	AssertBenchmarkNilWriter  = "ASSERT_BENCHMARK_003_NIL_WRITER"
)

// Assert verifies invariant conditions and panics with invariantID if violated.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// DailyMetric holds the daily PIPELINE measure: wall clock and peak RSS of one
// measured `timesheet` invocation, plus the [AI] editorial-child token lane and
// the day's entry count.
//
// The token and entry fields are POINTERS and follow the nil-vs-zero idiom: nil
// means UNKNOWN (a day with no workflow record, or no editor on that day) and is
// written as an EMPTY CSV cell and printed as `unknown`. It is never written as
// zero, because zero is a measured value. The token lane is the editorial-child
// lane copied from the day's workflow record; it is NOT harness-session usage
// and no ceiling governs harness tokens.
type DailyMetric struct {
	Date         string  `json:"date"`
	WallClockMS  int64   `json:"wall_clock_ms"`
	PeakRSSMB    float64 `json:"peak_rss_mb"`
	InputTokens  *int64  `json:"input_tokens"`
	OutputTokens *int64  `json:"output_tokens"`
	CacheTokens  *int64  `json:"cache_tokens"`
	TotalTokens  *int64  `json:"total_tokens"`
	Entries      *int    `json:"entries"`
}
