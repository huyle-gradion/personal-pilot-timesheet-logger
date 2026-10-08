package benchmark

const (
	// MaxTranscriptLineSizeBytes limits maximum line length to prevent memory exhaustion on giant blobs.
	MaxTranscriptLineSizeBytes = 10 * 1024 * 1024 // 10 MiB

	// MaxCSVRowsCount sets bounded allocation limit for metric log records.
	MaxCSVRowsCount = 1000

	// DefaultTokenCeiling is the daily budget limit from the project specification.
	DefaultTokenCeiling int64 = 600

	// DefaultCSVPath points to the default CSV metrics store.
	DefaultCSVPath = "benchmarks/metrics.csv"
)

// Invariant assertion identifiers for TigerStyle runtime validation.
const (
	AssertBenchmarkContextNil = "ASSERT_BENCHMARK_001_NIL_CONTEXT"
	AssertBenchmarkDateEmpty  = "ASSERT_BENCHMARK_002_EMPTY_DATE"
	AssertBenchmarkNilWriter  = "ASSERT_BENCHMARK_003_NIL_WRITER"
	AssertBenchmarkNilReader  = "ASSERT_BENCHMARK_004_NIL_READER"
)

// Assert verifies invariant conditions and panics with invariantID if violated.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// DailyMetric holds execution time, peak memory, and token metrics for a specific date.
type DailyMetric struct {
	Date         string  `json:"date"`
	WallClockMS  int64   `json:"wall_clock_ms"`
	PeakRSSMB    float64 `json:"peak_rss_mb"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheTokens  int64   `json:"cache_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Entries      int     `json:"entries"`
}

// TokenUsage holds aggregated token counters parsed from transcripts.
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheTokens  int64 `json:"cache_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}
