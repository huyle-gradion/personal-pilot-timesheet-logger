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
	CSVHeader = "date,wall_clock_ms,peak_rss_mb,input_tokens,output_tokens,cache_tokens,total_tokens,entries"
)

// AppendMetric appends a daily metric record to the CSV file at csvPath.
// If the file or parent directories do not exist, it creates them and writes the standard header.
func AppendMetric(csvPath string, metric DailyMetric) error {
	Assert(strings.TrimSpace(metric.Date) != "", AssertBenchmarkDateEmpty)

	cleanPath := filepath.Clean(csvPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	needsHeader := false
	fi, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			needsHeader = true
		} else {
			return fmt.Errorf("checking csv file: %w", err)
		}
	} else if fi.Size() == 0 {
		needsHeader = true
	}

	f, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0644)
	if err != nil {
		return fmt.Errorf("opening csv file for append %s: %w", cleanPath, err)
	}
	defer f.Close()

	if needsHeader {
		if _, err := f.WriteString(CSVHeader + "\n"); err != nil {
			return fmt.Errorf("writing csv header: %w", err)
		}
	}

	row := fmt.Sprintf("%s,%d,%.1f,%d,%d,%d,%d,%d\n",
		metric.Date,
		metric.WallClockMS,
		metric.PeakRSSMB,
		metric.InputTokens,
		metric.OutputTokens,
		metric.CacheTokens,
		metric.TotalTokens,
		metric.Entries,
	)

	if _, err := f.WriteString(row); err != nil {
		return fmt.Errorf("writing csv metric row: %w", err)
	}

	return nil
}

// ReadMetrics reads all metric records from csvPath up to MaxCSVRowsCount.
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
	reader.FieldsPerRecord = -1 // Allow variable or check explicitly

	// Read header
	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, fmt.Errorf("reading csv header: %w", err)
	}

	_ = header // Verified present
	var metrics []DailyMetric

	for i := 0; i < MaxCSVRowsCount; i++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading csv line %d: %w", i+2, err)
		}

		if len(record) < 8 {
			continue // Skip malformed rows
		}

		m, pErr := parseCSVRow(record)
		if pErr != nil {
			continue // Skip unparseable lines
		}
		metrics = append(metrics, m)
	}

	return metrics, nil
}

func parseCSVRow(record []string) (DailyMetric, error) {
	var m DailyMetric
	m.Date = strings.TrimSpace(record[0])

	wc, err := strconv.ParseInt(strings.TrimSpace(record[1]), 10, 64)
	if err != nil {
		return m, err
	}
	m.WallClockMS = wc

	rss, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
	if err != nil {
		return m, err
	}
	m.PeakRSSMB = rss

	inp, err := strconv.ParseInt(strings.TrimSpace(record[3]), 10, 64)
	if err != nil {
		return m, err
	}
	m.InputTokens = inp

	out, err := strconv.ParseInt(strings.TrimSpace(record[4]), 10, 64)
	if err != nil {
		return m, err
	}
	m.OutputTokens = out

	cache, err := strconv.ParseInt(strings.TrimSpace(record[5]), 10, 64)
	if err != nil {
		return m, err
	}
	m.CacheTokens = cache

	total, err := strconv.ParseInt(strings.TrimSpace(record[6]), 10, 64)
	if err != nil {
		return m, err
	}
	m.TotalTokens = total

	entries, err := strconv.Atoi(strings.TrimSpace(record[7]))
	if err != nil {
		return m, err
	}
	m.Entries = entries

	return m, nil
}
