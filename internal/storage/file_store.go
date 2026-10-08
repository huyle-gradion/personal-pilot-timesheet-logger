package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

var datePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)
var jsonFilePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.json$`)

// JSONFileStore implements TimesheetStore backed by atomic daily JSON files.
type JSONFileStore struct {
	baseDir string
}

// ResolveDataDir computes the data directory adhering to the precedence hierarchy:
// 1. Explicit customDir argument (if non-empty)
// 2. TIMESHEET_DATA_DIR environment variable (if non-empty)
// 3. ~/.timesheet/records/ (if UserHomeDir is resolvable)
// 4. ./data/timesheets/ fallback
func ResolveDataDir(customDir string) string {
	trimmed := strings.TrimSpace(customDir)
	if trimmed != "" {
		return filepath.Clean(trimmed)
	}

	envDir := strings.TrimSpace(os.Getenv("TIMESHEET_DATA_DIR"))
	if envDir != "" {
		return filepath.Clean(envDir)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		return filepath.Clean(filepath.Join(homeDir, ".timesheet", "records"))
	}

	return filepath.Clean("./data/timesheets")
}

// NewJSONFileStore initializes a JSONFileStore ensuring directory creation with 0750 permissions.
func NewJSONFileStore(customDir string) (*JSONFileStore, error) {
	resolvedDir := ResolveDataDir(customDir)
	Assert(resolvedDir != "", AssertStorageDirEmpty)

	if err := os.MkdirAll(resolvedDir, DirPermPrivate); err != nil {
		return nil, fmt.Errorf("failed to create timesheet storage directory: %w", err)
	}

	return &JSONFileStore{baseDir: resolvedDir}, nil
}

// BaseDir returns the resolved directory path of the store.
func (s *JSONFileStore) BaseDir() string {
	return s.baseDir
}

// ValidateDate ensures the string conforms to a valid calendar date YYYY-MM-DD.
func ValidateDate(date string) error {
	if !datePattern.MatchString(date) {
		return ErrInvalidDate
	}
	_, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ErrInvalidDate
	}
	return nil
}

func calculateTotalHours(blocks []timesheet.TimeBlock) float64 {
	var totalMinutes float64
	for _, b := range blocks {
		start, err1 := time.Parse("15:04", b.StartTime)
		end, err2 := time.Parse("15:04", b.EndTime)
		if err1 == nil && err2 == nil {
			diff := end.Sub(start)
			if diff < 0 {
				diff += 24 * time.Hour
			}
			totalMinutes += diff.Minutes()
		}
	}
	return math.Round((totalMinutes/60.0)*100) / 100
}

// SaveDay atomically writes a daily timesheet record with idempotent version increments.
func (s *JSONFileStore) SaveDay(ctx context.Context, record TimesheetRecord) error {
	Assert(ctx != nil, AssertStorageContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := ValidateDate(record.Date); err != nil {
		return err
	}

	if len(record.Blocks) > MaxBlocksPerDayCount {
		return ErrExcessiveBlocks
	}

	if err := os.MkdirAll(s.baseDir, DirPermPrivate); err != nil {
		return fmt.Errorf("failed to ensure base directory exists: %w", err)
	}

	record.TotalHours = calculateTotalHours(record.Blocks)

	targetPath := filepath.Join(s.baseDir, record.Date+".json")

	// Idempotent upsert versioning.
	existingRecord, err := s.readRecordFile(targetPath)
	if err == nil && existingRecord != nil {
		record.Version = existingRecord.Version + 1
	} else if errors.Is(err, ErrRecordNotFound) {
		record.Version = DefaultSchemaVersion
	} else if err != nil {
		return fmt.Errorf("failed checking existing record for versioning: %w", err)
	}

	record.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal timesheet record: %w", err)
	}

	if int64(len(data)) > MaxRecordSizeBytes {
		return ErrRecordTooLarge
	}

	// Write to temporary file in the same directory to guarantee atomic rename across filesystems.
	tempPath := filepath.Join(s.baseDir, fmt.Sprintf(".%s.tmp.%d", record.Date, time.Now().UnixNano()))
	if err := os.WriteFile(tempPath, data, FilePermPrivate); err != nil {
		return fmt.Errorf("failed to write temporary timesheet file: %w", err)
	}

	if err := os.Rename(tempPath, targetPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to atomically rename timesheet file: %w", err)
	}

	return nil
}

// GetDay retrieves a TimesheetRecord for the specified date, returning ErrRecordNotFound if missing.
func (s *JSONFileStore) GetDay(ctx context.Context, date string) (*TimesheetRecord, error) {
	Assert(ctx != nil, AssertStorageContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	targetPath := filepath.Join(s.baseDir, date+".json")
	return s.readRecordFile(targetPath)
}

func (s *JSONFileStore) readRecordFile(path string) (*TimesheetRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("failed to open timesheet file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect timesheet file: %w", err)
	}

	if stat.Size() > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	limitedReader := io.LimitReader(file, MaxRecordSizeBytes+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("failed reading timesheet file: %w", err)
	}

	if int64(len(data)) > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	var record TimesheetRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("failed to parse timesheet JSON: %w", err)
	}

	return &record, nil
}

// ListDays lists recorded timesheet dates within [fromDate, toDate] in chronological order.
func (s *JSONFileStore) ListDays(ctx context.Context, fromDate, toDate string) ([]string, error) {
	Assert(ctx != nil, AssertStorageContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if fromDate != "" {
		if err := ValidateDate(fromDate); err != nil {
			return nil, err
		}
	}
	if toDate != "" {
		if err := ValidateDate(toDate); err != nil {
			return nil, err
		}
	}

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed reading storage directory: %w", err)
	}

	var dates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := jsonFilePattern.FindStringSubmatch(entry.Name())
		if len(matches) != 2 {
			continue
		}
		date := matches[1]
		if fromDate != "" && date < fromDate {
			continue
		}
		if toDate != "" && date > toDate {
			continue
		}
		dates = append(dates, date)
		if len(dates) >= MaxDaysListedCount {
			break
		}
	}

	sort.Strings(dates)
	return dates, nil
}
