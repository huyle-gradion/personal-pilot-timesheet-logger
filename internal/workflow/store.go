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
	// ErrWriterLockStale reports a parseable day lock whose recorded owner is
	// not alive on this host. It is never auto-broken; a caller must break it
	// explicitly with a verified token (BreakStaleLock).
	ErrWriterLockStale = errors.New("workflow writer lock is stale: recorded owner is not alive on this host; break it manually with a verified token")
	// ErrRevisionConflict reports a compare-and-swap write whose expected
	// revision did not match the persisted revision. No write is performed.
	ErrRevisionConflict = errors.New("workflow revision conflict: persisted revision does not match the expected revision")
	// ErrMappingWithoutRemoteID is returned when a remote mapping has no stable
	// remote identity. Identity must never be fabricated from time-slot equality.
	ErrMappingWithoutRemoteID = errors.New("remote mapping requires a non-empty remote ID; identity must never be inferred from time-slot equality")
	// ErrMappingWithoutLocalID is returned when a remote mapping has no local
	// identity to key on, which would otherwise collapse distinct mappings.
	ErrMappingWithoutLocalID = errors.New("remote mapping requires a non-empty local ID")
)

// TigerStyle named bounds with explicit units.
const (
	SchemaVersion             int         = 1
	MaxRecordSizeBytes        int64       = 10 * 1024 * 1024 // 10 MiB limit on single workflow JSON
	MaxEvidenceCount          int         = 500              // bounded evidence references per day
	MaxCollectedEvidenceCount int         = 2000             // bounded collected evidence items per day
	MaxConfirmedCount         int         = 100              // bounded confirmed intervals per day
	MaxMappingsCount          int         = 100              // bounded remote mappings per day
	MaxOutcomesCount          int         = 1000             // bounded operation outcomes per day
	MaxPlansCount             int         = 50               // bounded submission plans per day
	MaxApprovalsCount         int         = 50               // bounded approvals per day
	MaxDaysListedCount        int         = 365              // bounded ListDays result
	FilePermPrivate           os.FileMode = 0600
	DirPermPrivate            os.FileMode = 0750
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
// BeginDaySession acquires the exclusive day lock exactly once and returns a
// Session whose Get/Update run under that held lock, so per-operation
// persistence does not re-acquire (and deadlock on) the lock. UpdateDay is the
// one-shot convenience that begins, updates, and closes a session.
//
// SaveDayCAS and the CAS-free saveDay are deliberately absent from this
// interface: saveDay is a whole-record overwrite kept only on the concrete
// *JSONFileStore for tests, and SaveDayCAS is a revision-checked whole-record
// write available on the concrete store.
type Store interface {
	GetDay(ctx context.Context, date string) (*Record, error)
	ListDays(ctx context.Context, fromDate, toDate string) ([]string, error)
	UpdateDay(ctx context.Context, date string, mutate func(*Record) error) (*Record, error)
	BeginDaySession(ctx context.Context, date string) (Session, error)
	LoadLegacyDay(ctx context.Context, date string) (*storage.TimesheetRecord, error)
}

// Session is an exclusive day-writer session. The day lock is acquired by
// BeginDaySession and held until Close. Update composes a mutation against the
// latest persisted revision and durably persists the result under that same
// held lock. Close is idempotent and releases the lock exactly once.
type Session interface {
	// Date returns the session's target date (YYYY-MM-DD).
	Date() string
	// Get reads the current persisted record for the session date.
	Get(ctx context.Context) (*Record, error)
	// Update applies mutate to the current record and persists it, returning
	// the persisted record. It never re-acquires the day lock.
	Update(ctx context.Context, mutate func(*Record) error) (*Record, error)
	// Close releases the day lock. It is idempotent.
	Close() error
}
