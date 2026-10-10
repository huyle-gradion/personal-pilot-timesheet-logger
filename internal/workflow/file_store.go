package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
)

var datePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)
var jsonFilePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.json$`)

// syncWorkflowDir flushes a directory handle so a completed rename survives a
// crash. It is a package-level seam so tests can force a directory-sync failure
// and assert the durability error is surfaced rather than swallowed.
var syncWorkflowDir = func(dir *os.File) error {
	return dir.Sync()
}

// JSONFileStore implements Store backed by atomic per-day JSON files under
// <rootDir>/workflow. Legacy storage.TimesheetRecord files remain at
// <rootDir>/<date>.json and are only ever read.
type JSONFileStore struct {
	rootDir     string
	workflowDir string
}

// NewJSONFileStore initializes the workflow store for the same records directory
// callers already pass to storage.ResolveDataDir. It creates <rootDir>/workflow
// with directory permission 0750.
func NewJSONFileStore(rootDir string) (*JSONFileStore, error) {
	resolved := filepath.Clean(strings.TrimSpace(rootDir))
	Assert(resolved != "", AssertWorkflowDirEmpty)

	workflowDir := filepath.Join(resolved, "workflow")
	if err := os.MkdirAll(workflowDir, DirPermPrivate); err != nil {
		return nil, fmt.Errorf("failed to create workflow storage directory: %w", err)
	}

	return &JSONFileStore{rootDir: resolved, workflowDir: workflowDir}, nil
}

// ValidateDate ensures the string conforms to a valid calendar date YYYY-MM-DD.
func ValidateDate(date string) error {
	if !datePattern.MatchString(date) {
		return ErrInvalidDate
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ErrInvalidDate
	}
	return nil
}

func (s *JSONFileStore) recordPath(date string) string {
	return filepath.Join(s.workflowDir, date+".json")
}

func (s *JSONFileStore) lockPath(date string) string {
	return filepath.Join(s.workflowDir, "."+date+".lock")
}

// GetDay reads a workflow record. Missing -> ErrRecordNotFound; oversized ->
// ErrRecordTooLarge; unparseable -> wrapped ErrRecordCorrupt. Reads never clobber.
func (s *JSONFileStore) GetDay(ctx context.Context, date string) (*Record, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}
	return s.readRecordFile(s.recordPath(date), date)
}

// saveDay atomically persists a caller-scoped workflow record under the exclusive
// day lock. It fails closed: a pre-existing corrupt record returns
// ErrRecordCorrupt without writing and leaves the bytes untouched.
//
// It is UNEXPORTED and test-only. HAZARD: it overwrites the WHOLE record with no
// revision compare-and-swap. A caller holding a stale full snapshot will silently
// overwrite any intervening updates; only the Revision counter is bumped. Callers
// that need a whole-record write must use SaveDayCAS instead.
func (s *JSONFileStore) saveDay(ctx context.Context, record Record) error {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateDate(record.Date); err != nil {
		return err
	}
	if err := checkCapacity(record); err != nil {
		return err
	}

	release, err := s.AcquireDayLock(ctx, record.Date)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	existing, err := s.loadForWrite(record.Date)
	if err != nil {
		return err
	}

	if record.SchemaVersion <= 0 {
		record.SchemaVersion = SchemaVersion
	}
	if existing != nil {
		record.Revision = existing.Revision + 1
	} else {
		record.Revision = 1
	}
	record.UpdatedAt = time.Now().UTC()

	return s.writeRecordFile(record.Date, record)
}

// SaveDayCAS persists a whole record only when the currently persisted Revision
// equals expectedRevision. On mismatch it returns ErrRevisionConflict and writes
// nothing, so a stale snapshot can never clobber an intervening update. On a
// match the record is written with Revision = expectedRevision + 1. An absent
// record has persisted revision 0, so expectedRevision 0 creates revision 1.
func (s *JSONFileStore) SaveDayCAS(ctx context.Context, expectedRevision int, record Record) error {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateDate(record.Date); err != nil {
		return err
	}
	if err := checkCapacity(record); err != nil {
		return err
	}

	release, err := s.AcquireDayLock(ctx, record.Date)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()

	existing, err := s.loadForWrite(record.Date)
	if err != nil {
		return err
	}
	current := 0
	if existing != nil {
		current = existing.Revision
	}
	if current != expectedRevision {
		return ErrRevisionConflict
	}

	if record.SchemaVersion <= 0 {
		record.SchemaVersion = SchemaVersion
	}
	record.Revision = expectedRevision + 1
	record.UpdatedAt = time.Now().UTC()

	return s.writeRecordFile(record.Date, record)
}

// UpdateDay loads (or lazily initializes) a day record under the exclusive lock,
// applies mutate to a fresh non-nil Record, and atomically persists the result.
// It fails closed on a corrupt existing record. Returns the persisted record.
//
// It is the one-shot form of BeginDaySession + Session.Update + Session.Close and
// preserves the self-locking external behavior of S1.
func (s *JSONFileStore) UpdateDay(ctx context.Context, date string, mutate func(*Record) error) (*Record, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	Assert(mutate != nil, AssertWorkflowNilMutate)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	session, err := s.BeginDaySession(ctx, date)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()

	return session.Update(ctx, mutate)
}

// updateLocked is UpdateDay's body without lock acquisition. Callers MUST already
// hold the day lock (via BeginDaySession or AcquireDayLock); otherwise concurrent
// writers are not serialized.
func (s *JSONFileStore) updateLocked(ctx context.Context, date string, mutate func(*Record) error) (*Record, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	Assert(mutate != nil, AssertWorkflowNilMutate)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	existing, err := s.loadForWrite(date)
	if err != nil {
		return nil, err
	}

	record := Record{SchemaVersion: SchemaVersion, Date: date, Revision: 0}
	if existing != nil {
		record = *existing
	}

	if err := mutate(&record); err != nil {
		return nil, err
	}
	if err := checkCapacity(record); err != nil {
		return nil, err
	}

	if existing != nil {
		record.Revision = existing.Revision + 1
	} else {
		record.Revision = 1
	}
	if record.SchemaVersion <= 0 {
		record.SchemaVersion = SchemaVersion
	}
	record.Date = date
	record.UpdatedAt = time.Now().UTC()

	if err := s.writeRecordFile(date, record); err != nil {
		return nil, err
	}
	return &record, nil
}

// BeginDaySession acquires the exclusive day lock exactly once and returns a
// Session bound to the date. Per-operation persistence via Session.Update reuses
// the held lock and never re-acquires it, so a submission that persists an intent
// and an outcome per operation cannot deadlock. The caller must Close the session.
func (s *JSONFileStore) BeginDaySession(ctx context.Context, date string) (Session, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	release, err := s.AcquireDayLock(ctx, date)
	if err != nil {
		return nil, err
	}
	return &daySession{store: s, date: date, release: release}, nil
}

// daySession is the lock-holding Session implementation.
type daySession struct {
	store   *JSONFileStore
	date    string
	release func() error
	once    sync.Once
	err     error
}

func (d *daySession) Date() string { return d.date }

func (d *daySession) Get(ctx context.Context) (*Record, error) {
	return d.store.GetDay(ctx, d.date)
}

func (d *daySession) Update(ctx context.Context, mutate func(*Record) error) (*Record, error) {
	return d.store.updateLocked(ctx, d.date, mutate)
}

func (d *daySession) Close() error {
	d.once.Do(func() { d.err = d.release() })
	return d.err
}

// ListDays lists workflow dates within [fromDate, toDate] inclusive, sorted
// ascending and capped at 365. A missing workflow directory yields no dates.
func (s *JSONFileStore) ListDays(ctx context.Context, fromDate, toDate string) ([]string, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
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

	entries, err := os.ReadDir(s.workflowDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed reading workflow directory: %w", err)
	}

	dates := make([]string, 0, len(entries))
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
	}

	sort.Strings(dates)
	if len(dates) > MaxDaysListedCount {
		dates = dates[:MaxDaysListedCount]
	}
	return dates, nil
}

// LoadLegacyDay reads a legacy storage.TimesheetRecord from <rootDir>/<date>.json.
// It is read-only and never migrates or overwrites the legacy file.
func (s *JSONFileStore) LoadLegacyDay(ctx context.Context, date string) (*storage.TimesheetRecord, error) {
	Assert(ctx != nil, AssertWorkflowContextNil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateDate(date); err != nil {
		return nil, err
	}

	path := filepath.Join(s.rootDir, date+".json")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("failed to open legacy timesheet file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect legacy timesheet file: %w", err)
	}
	if stat.Size() > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxRecordSizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed reading legacy timesheet file: %w", err)
	}
	if int64(len(data)) > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	var record storage.TimesheetRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecordCorrupt, err)
	}
	return &record, nil
}

// loadForWrite returns the existing record, nil when absent, or a fail-closed
// error (ErrRecordCorrupt / ErrRecordTooLarge) that prevents any write.
func (s *JSONFileStore) loadForWrite(date string) (*Record, error) {
	record, err := s.readRecordFile(s.recordPath(date), date)
	if err != nil {
		if errors.Is(err, ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return record, nil
}

// readRecordFile decodes a persisted record and fails closed on any document
// that is not a valid record for expectedDate: empty/whitespace/null payloads,
// a missing or unknown-future schema version, or a date that disagrees with the
// filename. Returning before any write guarantees the file bytes are untouched.
func (s *JSONFileStore) readRecordFile(path, expectedDate string) (*Record, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrRecordNotFound
		}
		return nil, fmt.Errorf("failed to open workflow file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect workflow file: %w", err)
	}
	if stat.Size() > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	data, err := io.ReadAll(io.LimitReader(file, MaxRecordSizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed reading workflow file: %w", err)
	}
	if int64(len(data)) > MaxRecordSizeBytes {
		return nil, ErrRecordTooLarge
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: empty or null workflow document", ErrRecordCorrupt)
	}

	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRecordCorrupt, err)
	}
	if record.SchemaVersion <= 0 || record.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("%w: unsupported schema version %d", ErrRecordCorrupt, record.SchemaVersion)
	}
	if expectedDate != "" && record.Date != expectedDate {
		return nil, fmt.Errorf("%w: record date %q does not match expected %q", ErrRecordCorrupt, record.Date, expectedDate)
	}
	return &record, nil
}

// writeRecordFile performs an atomic, durable write: temp file (0600) in the same
// workflow directory, Sync, close, Rename, then directory fsync. A temp file is
// removed on any failure and never left behind on success.
func (s *JSONFileStore) writeRecordFile(date string, record Record) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow record: %w", err)
	}
	if int64(len(data)) > MaxRecordSizeBytes {
		return ErrRecordTooLarge
	}

	tempPath := filepath.Join(s.workflowDir, fmt.Sprintf(".%s.tmp.%d", date, time.Now().UnixNano()))
	cleanup := func() { _ = os.Remove(tempPath) }

	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, FilePermPrivate)
	if err != nil {
		return fmt.Errorf("failed to create temporary workflow file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanup()
		return fmt.Errorf("failed to write temporary workflow file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return fmt.Errorf("failed to sync temporary workflow file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return fmt.Errorf("failed to close temporary workflow file: %w", err)
	}
	if err := os.Rename(tempPath, s.recordPath(date)); err != nil {
		cleanup()
		return fmt.Errorf("failed to atomically rename workflow file: %w", err)
	}

	dir, err := os.Open(s.workflowDir)
	if err != nil {
		return fmt.Errorf("failed to open workflow directory for sync: %w", err)
	}
	if syncErr := syncWorkflowDir(dir); syncErr != nil {
		_ = dir.Close()
		return fmt.Errorf("failed to sync workflow directory: %w", syncErr)
	}
	if closeErr := dir.Close(); closeErr != nil {
		return fmt.Errorf("failed to close workflow directory after sync: %w", closeErr)
	}
	return nil
}

// S4 review capacity bounds (named constants live here, beside checkCapacity).
const (
	MaxReviewExcludedCount   int = 500 // bounded review exclusions per day
	MaxReviewAttendanceCount int = 200 // bounded attendance decisions per day
)

func checkCapacity(record Record) error {
	if len(record.Evidence) > MaxEvidenceCount ||
		len(record.Confirmed) > MaxConfirmedCount ||
		len(record.RemoteMappings) > MaxMappingsCount ||
		len(record.Outcomes) > MaxOutcomesCount ||
		len(record.CollectedEvidence) > MaxCollectedEvidenceCount {
		return ErrCapacityExceeded
	}
	if record.Review != nil {
		if len(record.Review.ExcludedEvidence) > MaxReviewExcludedCount ||
			len(record.Review.Attendance) > MaxReviewAttendanceCount {
			return ErrCapacityExceeded
		}
	}
	return nil
}
