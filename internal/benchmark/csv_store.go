package benchmark

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// CSVHeader is the frozen column order of the metrics store. The names and
	// their order do not change.
	CSVHeader = "date,wall_clock_ms,peak_rss_mb,input_tokens,output_tokens,cache_tokens,total_tokens,entries"

	// csvFieldCount is the exact field count of one row. A row with any other
	// count is malformed and FAILS LOUDLY; it is never skipped.
	csvFieldCount = 8

	// csvFilePerm is the mode of the rewritten store (the mode the store has
	// always had; CreateTemp would otherwise leave it 0600).
	csvFilePerm = 0644
)

// UpsertMetric writes metric as the ONE row for its date: a row for the same date
// is REPLACED in place (last value wins), never appended next to a duplicate.
//
// The write is temp-file + atomic rename, so a crash leaves either the previous
// store or the new one, never a half-written file. Reading and rewriting are NOT
// bounded by MaxCSVRowsCount, and a malformed existing row fails the rewrite
// loudly instead of being dropped.
func UpsertMetric(csvPath string, metric DailyMetric) error {
	Assert(strings.TrimSpace(metric.Date) != "", AssertBenchmarkDateEmpty)

	cleanPath := filepath.Clean(csvPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	// The write path agrees with the reader's O_NOFOLLOW posture: a symlinked
	// store is refused BEFORE anything is read or replaced.
	mode, err := csvWriteMode(cleanPath)
	if err != nil {
		return err
	}

	existing, err := ReadMetrics(cleanPath)
	if err != nil {
		return err
	}

	rows := make([]DailyMetric, 0, len(existing)+1)
	replaced := false
	for _, m := range existing {
		if m.Date != metric.Date {
			rows = append(rows, m)
			continue
		}
		// One row per date: the first occurrence keeps the position, every
		// duplicate is dropped, and the NEW value wins.
		if !replaced {
			rows = append(rows, metric)
			replaced = true
		}
	}
	if !replaced {
		rows = append(rows, metric)
	}

	return writeMetricsFile(cleanPath, rows, mode)
}

// ReadMetrics reads every recorded metric row from csvPath. A missing file reads
// as no rows (nil, nil). An empty token or entry cell reads back as nil (UNKNOWN),
// and a malformed row is an ERROR naming its line; no row is ever silently
// dropped or truncated.
func ReadMetrics(csvPath string) ([]DailyMetric, error) {
	cleanPath := filepath.Clean(csvPath)
	f, err := os.OpenFile(cleanPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening csv file %s: %w", cleanPath, err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1 // the exact count is validated per row

	// Read header
	if _, err := reader.Read(); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("reading csv header: %w", err)
	}

	var metrics []DailyMetric

	for line := 2; ; line++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading csv line %d: %w", line, err)
		}

		m, pErr := parseCSVRow(record)
		if pErr != nil {
			return nil, fmt.Errorf("parsing csv line %d: %w", line, pErr)
		}
		metrics = append(metrics, m)
	}

	return metrics, nil
}

// csvWriteMode resolves the mode a rewrite must preserve and refuses a symlinked
// store. The reader opens with O_NOFOLLOW, so a write that renamed over a link
// would change which file the store IS without the reader ever following it.
func csvWriteMode(path string) (os.FileMode, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return os.FileMode(csvFilePerm), nil
		}
		return 0, fmt.Errorf("checking csv file mode: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("refusing to replace csv file %s: it is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("refusing to replace csv file %s: it is not a regular file", path)
	}
	return fi.Mode().Perm(), nil
}

// writeMetricsFile writes the whole store atomically: temp file in the same
// directory, then rename over the target, preserving mode.
func writeMetricsFile(path string, rows []DailyMetric, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".metrics-*.csv.tmp")
	if err != nil {
		return fmt.Errorf("creating temp csv file: %w", err)
	}
	tmpName := tmp.Name()
	// A successful rename removes the temp name; this cleans up every failure.
	defer func() { _ = os.Remove(tmpName) }()

	w := csv.NewWriter(tmp)
	if err := w.Write(strings.Split(CSVHeader, ",")); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing csv header: %w", err)
	}
	for _, m := range rows {
		if err := w.Write(formatCSVRow(m)); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("writing csv row for %s: %w", m.Date, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing csv rows: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing temp csv file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp csv file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("setting csv file mode: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing csv file %s: %w", path, err)
	}
	return nil
}

// formatCSVRow renders one row. An UNKNOWN (nil) token or entry cell is written
// EMPTY, never 0.
func formatCSVRow(m DailyMetric) []string {
	return []string{
		m.Date,
		strconv.FormatInt(m.WallClockMS, 10),
		strconv.FormatFloat(m.PeakRSSMB, 'f', 1, 64),
		formatOptionalInt64(m.InputTokens),
		formatOptionalInt64(m.OutputTokens),
		formatOptionalInt64(m.CacheTokens),
		formatOptionalInt64(m.TotalTokens),
		formatOptionalInt(m.Entries),
	}
}

func formatOptionalInt64(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}

func formatOptionalInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// parseCSVRow parses one row. An empty token or entry cell is UNKNOWN (nil), not
// an error and not a zero.
func parseCSVRow(record []string) (DailyMetric, error) {
	var m DailyMetric
	if len(record) != csvFieldCount {
		return m, fmt.Errorf("want %d fields, got %d", csvFieldCount, len(record))
	}
	m.Date = strings.TrimSpace(record[0])

	wc, err := strconv.ParseInt(strings.TrimSpace(record[1]), 10, 64)
	if err != nil {
		return m, fmt.Errorf("wall_clock_ms: %w", err)
	}
	m.WallClockMS = wc

	rss, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
	if err != nil {
		return m, fmt.Errorf("peak_rss_mb: %w", err)
	}
	m.PeakRSSMB = rss

	if m.InputTokens, err = parseOptionalInt64(record[3]); err != nil {
		return m, fmt.Errorf("input_tokens: %w", err)
	}
	if m.OutputTokens, err = parseOptionalInt64(record[4]); err != nil {
		return m, fmt.Errorf("output_tokens: %w", err)
	}
	if m.CacheTokens, err = parseOptionalInt64(record[5]); err != nil {
		return m, fmt.Errorf("cache_tokens: %w", err)
	}
	if m.TotalTokens, err = parseOptionalInt64(record[6]); err != nil {
		return m, fmt.Errorf("total_tokens: %w", err)
	}
	if m.Entries, err = parseOptionalInt(record[7]); err != nil {
		return m, fmt.Errorf("entries: %w", err)
	}

	return m, nil
}

func parseOptionalInt64(field string) (*int64, error) {
	s := strings.TrimSpace(field)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func parseOptionalInt(field string) (*int, error) {
	s := strings.TrimSpace(field)
	if s == "" {
		return nil, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
