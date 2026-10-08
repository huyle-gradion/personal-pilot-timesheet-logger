package storage

import (
	"context"
	"errors"
	"os"
)

// Typed operating errors.
var (
	ErrRecordNotFound    = errors.New("timesheet record not found")
	ErrSinkNotConfigured = errors.New("exporter sink not configured")
	ErrInvalidDate       = errors.New("invalid date format: must be valid calendar date YYYY-MM-DD")
	ErrExcessiveBlocks   = errors.New("capacity exceeded: record blocks count exceeds maximum per day")
	ErrRecordTooLarge    = errors.New("payload limit exceeded: record size exceeds maximum allowed bytes")
)

// TigerStyle bounds and permissions.
const (
	MaxRecordSizeBytes   int64       = 10 * 1024 * 1024 // 10 MiB limit on single timesheet JSON
	MaxDaysListedCount   int         = 365              // Bounded query limit
	MaxBlocksPerDayCount int         = 100              // Bounded blocks per record
	DefaultSchemaVersion int         = 1
	FilePermPrivate      os.FileMode = 0600
	DirPermPrivate       os.FileMode = 0750
)

// Invariant assertion identifiers.
const (
	AssertStorageContextNil  = "ASSERT_STORAGE_001_NIL_CONTEXT"
	AssertStorageDirEmpty    = "ASSERT_STORAGE_002_EMPTY_DIR"
	AssertStorageInvalidDate = "ASSERT_STORAGE_003_INVALID_DATE"
)

// Assert terminates the process on violated internal invariants per TigerStyle.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// TimesheetStore defines the persistence contract for daily timesheet records.
type TimesheetStore interface {
	SaveDay(ctx context.Context, record TimesheetRecord) error
	GetDay(ctx context.Context, date string) (*TimesheetRecord, error)
	ListDays(ctx context.Context, fromDate, toDate string) ([]string, error)
}

// ExporterSink defines the pluggable output contract for finalized timesheets.
type ExporterSink interface {
	Name() string
	Export(ctx context.Context, record TimesheetRecord) error
}
