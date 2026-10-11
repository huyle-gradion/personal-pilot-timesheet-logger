package metrics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Storage permissions and layout.
const (
	// DirPermPrivate is the metrics directory permission.
	DirPermPrivate os.FileMode = 0750
	// FilePermPrivate is the metrics file permission.
	FilePermPrivate os.FileMode = 0600
	// runsSubdir holds one JSON file per run, keyed by run id.
	runsSubdir = "runs"
)

// ErrAggregateNotFound is returned when a day has no aggregate file.
var ErrAggregateNotFound = errors.New("metrics: daily aggregate not found")

// Store is the durable metrics store. Layout:
//
//	<dir>/runs/<run-id>.json   one RunRecord
//	<dir>/YYYY-MM-DD.json      one DailyAggregate
type Store struct {
	dir string
}

// NewStore creates the metrics store rooted at dir, creating <dir>/runs.
func NewStore(dir string) (*Store, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("metrics: empty metrics directory")
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(filepath.Join(dir, runsSubdir), DirPermPrivate); err != nil {
		return nil, fmt.Errorf("metrics: create metrics directory: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir returns the metrics root directory.
func (s *Store) Dir() string { return s.dir }

func (s *Store) runPath(runID string) string {
	return filepath.Join(s.dir, runsSubdir, runID+".json")
}

func (s *Store) aggregatePath(date string) string {
	return filepath.Join(s.dir, date+".json")
}

// WriteRun persists a run file. It writes ONLY when the run file is absent or its
// ContentHash differs from rec.ContentHash, so a reimport of an unchanged
// transcript is a no-op. It returns whether a write occurred.
func (s *Store) WriteRun(rec RunRecord) (bool, error) {
	id, err := sanitizeRunID(rec.RunID)
	if err != nil {
		return false, err
	}
	path := s.runPath(id)

	existing, rerr := readRunFile(path)
	switch {
	case rerr == nil:
		if existing.ContentHash != "" && existing.ContentHash == rec.ContentHash {
			return false, nil
		}
	case errors.Is(rerr, os.ErrNotExist):
		// fall through to write
	default:
		return false, rerr
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, fmt.Errorf("metrics: marshal run: %w", err)
	}
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return false, err
	}
	return true, nil
}

// ReadRun reads a single run by id.
func (s *Store) ReadRun(runID string) (RunRecord, error) {
	id, err := sanitizeRunID(runID)
	if err != nil {
		return RunRecord{}, err
	}
	return readRunFile(s.runPath(id))
}

// ListRuns returns the runs for a date (empty date = every run), ordered by file
// name. It is bounded by MaxRunFilesCount.
func (s *Store) ListRuns(date string) ([]RunRecord, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, runsSubdir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("metrics: read runs directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	if len(names) > MaxRunFilesCount {
		names = names[:MaxRunFilesCount]
	}
	out := make([]RunRecord, 0, len(names))
	for _, n := range names {
		rec, err := readRunFile(filepath.Join(s.dir, runsSubdir, n))
		if err != nil {
			return nil, err
		}
		if date != "" && rec.Date != date {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// WriteAggregate persists the daily aggregate. It writes only when the on-disk
// bytes differ, so re-aggregating identical inputs is a byte-identical no-op. It
// returns whether a write occurred.
func (s *Store) WriteAggregate(agg DailyAggregate) (bool, error) {
	if err := workflow.ValidateDate(agg.Date); err != nil {
		return false, fmt.Errorf("metrics: invalid aggregate date: %w", err)
	}
	data, err := json.MarshalIndent(agg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("metrics: marshal aggregate: %w", err)
	}
	data = append(data, '\n')
	path := s.aggregatePath(agg.Date)
	if existing, rerr := os.ReadFile(path); rerr == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	if err := atomicWrite(path, data); err != nil {
		return false, err
	}
	return true, nil
}

// ReadAggregate reads the daily aggregate file.
func (s *Store) ReadAggregate(date string) (DailyAggregate, error) {
	if err := workflow.ValidateDate(date); err != nil {
		return DailyAggregate{}, fmt.Errorf("metrics: invalid date: %w", err)
	}
	data, err := os.ReadFile(s.aggregatePath(date))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DailyAggregate{}, ErrAggregateNotFound
		}
		return DailyAggregate{}, fmt.Errorf("metrics: read aggregate: %w", err)
	}
	var agg DailyAggregate
	if err := json.Unmarshal(data, &agg); err != nil {
		return DailyAggregate{}, fmt.Errorf("metrics: corrupt aggregate %s: %w", date, err)
	}
	return agg, nil
}

// Aggregate lists the day's runs, builds the daily aggregate, and persists it. It
// returns the aggregate. Because BuildAggregate is pure and WriteAggregate is
// byte-idempotent, running Aggregate twice produces identical bytes.
func (s *Store) Aggregate(ctx context.Context, date string, src RecordSource) (DailyAggregate, error) {
	runs, err := s.ListRuns(date)
	if err != nil {
		return DailyAggregate{}, err
	}
	agg, err := BuildAggregate(ctx, date, runs, src)
	if err != nil {
		return DailyAggregate{}, err
	}
	if _, err := s.WriteAggregate(agg); err != nil {
		return DailyAggregate{}, err
	}
	return agg, nil
}

// sanitizeRunID maps a run id to a safe filename stem, replacing any character
// outside [A-Za-z0-9._-]. It rejects an empty id. When the sanitized id would
// exceed MaxRunIDLen it is replaced by a FIXED-LENGTH sha256 of the ORIGINAL id,
// so two long ids that share a prefix never collide (no lossy truncation).
func sanitizeRunID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("metrics: empty run id")
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > MaxRunIDLen {
		sum := sha256.Sum256([]byte(id))
		return "sha256-" + hex.EncodeToString(sum[:]), nil
	}
	return s, nil
}

func readRunFile(path string) (RunRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RunRecord{}, err
	}
	var rec RunRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return RunRecord{}, fmt.Errorf("metrics: corrupt run file %s: %w", filepath.Base(path), err)
	}
	return rec, nil
}

// atomicWrite writes data to path via a same-directory temp file and rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-metrics-*")
	if err != nil {
		return fmt.Errorf("metrics: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(FilePermPrivate); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("metrics: chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("metrics: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("metrics: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("metrics: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("metrics: rename temp file: %w", err)
	}
	return nil
}
