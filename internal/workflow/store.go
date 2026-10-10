package workflow

import (
	"context"
	"errors"
	"os"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
)

// Typed operating errors. These are stable sentinels: callers branch on
// errors.Is and never on error strings.
var (
	ErrRecordNotFound   = errors.New("workflow record not found")
	ErrRecordCorrupt    = errors.New("workflow record corrupt: failed to decode persisted JSON")
	ErrWriterBusy       = errors.New("workflow writer busy: day lock is already held")
	ErrInvalidDate      = errors.New("invalid date format: must be valid calendar date YYYY-MM-DD")
	ErrRecordTooLarge   = errors.New("payload limit exceeded: workflow record size exceeds maximum allowed bytes")
	ErrCapacityExceeded = errors.New("capacity exceeded: workflow record collection exceeds maximum allowed count")
	// ErrMappingWithoutRemoteID is returned when a remote mapping has no stable
	// remote identity. Identity must never be fabricated from time-slot equality.
	ErrMappingWithoutRemoteID = errors.New("remote mapping requires a non-empty remote ID; identity must never be inferred from time-slot equality")
	// ErrMappingWithoutLocalID is returned when a remote mapping has no local
	// identity to key on, which would otherwise collapse distinct mappings.
	ErrMappingWithoutLocalID = errors.New("remote mapping requires a non-empty local ID")
)

// TigerStyle named bounds with explicit units.
const (
	SchemaVersion      int         = 1
	MaxRecordSizeBytes int64       = 10 * 1024 * 1024 // 10 MiB limit on single workflow JSON
	MaxEvidenceCount   int         = 500              // bounded evidence references per day
	MaxConfirmedCount  int         = 100              // bounded confirmed intervals per day
	MaxMappingsCount   int         = 100              // bounded remote mappings per day
	MaxOutcomesCount   int         = 1000             // bounded operation outcomes per day
	MaxDaysListedCount int         = 365              // bounded ListDays result
	FilePermPrivate    os.FileMode = 0600
	DirPermPrivate     os.FileMode = 0750
)

// Invariant assertion identifiers.
const (
	AssertWorkflowContextNil = "ASSERT_WORKFLOW_001_NIL_CONTEXT"
	AssertWorkflowDirEmpty   = "ASSERT_WORKFLOW_002_EMPTY_DIR"
	AssertWorkflowNilMutate  = "ASSERT_WORKFLOW_003_NIL_MUTATE"
)

// Assert terminates the process on violated internal programmer invariants per
// TigerStyle. It is never used to handle recoverable operating errors.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// Store defines the durable persistence contract for per-day workflow records.
// It provides the REPRESENTATION for remote mappings and operation outcomes and
// populates mappings only from confirmed WRITE responses. Populating mappings
// from remote reads, skip, or adoption decisions belongs to the reconciliation
// slice (S6), not this contract.
//
// SaveDay is deliberately absent: it is a CAS-free whole-record overwrite kept
// only on the concrete *JSONFileStore for tests. Callers must use UpdateDay so
// mutations compose against the latest revision.
type Store interface {
	GetDay(ctx context.Context, date string) (*Record, error)
	ListDays(ctx context.Context, fromDate, toDate string) ([]string, error)
	UpdateDay(ctx context.Context, date string, mutate func(*Record) error) (*Record, error)
	AcquireDayLock(ctx context.Context, date string) (release func() error, err error)
	LoadLegacyDay(ctx context.Context, date string) (*storage.TimesheetRecord, error)
}
