package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestValidateDate(t *testing.T) {
	for _, date := range []string{"2026-10-08", "2024-02-29"} {
		if err := ValidateDate(date); err != nil {
			t.Errorf("expected %q valid, got %v", date, err)
		}
	}
	for _, date := range []string{"2026-13-01", "2026-02-30", "2026/10/08", "", "20261008"} {
		if err := ValidateDate(date); !errors.Is(err, ErrInvalidDate) {
			t.Errorf("expected %q ErrInvalidDate, got %v", date, err)
		}
	}
}

// TestJSONFileStore_SaveLoadRevisionAndStableRemoteID proves Revision is tracked
// across reloads and a stable RemoteID survives save/load cycles intact.
func TestJSONFileStore_SaveLoadRevisionAndStableRemoteID(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	record := Record{
		SchemaVersion: SchemaVersion,
		Date:          date,
		RemoteMappings: []RemoteMapping{{
			LocalID:          "local-1",
			RemoteID:         "remote-stable-42",
			Date:             date,
			StartTime:        "09:00",
			EndTime:          "10:00",
			ClassificationID: "class-1",
			Description:      "Work",
		}},
	}
	if err := store.SaveDay(ctx, record); err != nil {
		t.Fatalf("SaveDay failed: %v", err)
	}

	loaded, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}
	if loaded.Revision != 1 {
		t.Errorf("expected revision 1, got %d", loaded.Revision)
	}
	if len(loaded.RemoteMappings) != 1 || loaded.RemoteMappings[0].RemoteID != "remote-stable-42" {
		t.Errorf("expected stable remote ID to survive save, got %+v", loaded.RemoteMappings)
	}

	record.Revision = loaded.Revision
	record.RemoteMappings = loaded.RemoteMappings
	if err := store.SaveDay(ctx, record); err != nil {
		t.Fatalf("second SaveDay failed: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating fresh store: %v", err)
	}
	reloaded, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("fresh GetDay failed: %v", err)
	}
	if reloaded.Revision != 2 {
		t.Errorf("expected revision 2 across reload, got %d", reloaded.Revision)
	}
	if len(reloaded.RemoteMappings) != 1 || reloaded.RemoteMappings[0].RemoteID != "remote-stable-42" {
		t.Errorf("expected stable remote ID across reload, got %+v", reloaded.RemoteMappings)
	}
}

// TestJSONFileStore_PartialSuccessSurvivesReload proves a confirmed mapping for
// one local id is retained while an uncertain unresolved outcome exists for
// another, across a fresh store instance.
func TestJSONFileStore_PartialSuccessSurvivesReload(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	_, err = store.UpdateDay(ctx, date, func(r *Record) error {
		if err := UpsertMapping(r, RemoteMapping{
			LocalID: "local-a", RemoteID: "remote-a", Date: date,
			StartTime: "09:00", EndTime: "10:00",
		}); err != nil {
			return err
		}
		UpsertOutcome(r, OperationOutcome{LocalID: "local-a", Action: "create", Status: OutcomeConfirmed, Resolved: true})
		UpsertOutcome(r, OperationOutcome{LocalID: "local-b", Action: "create", Status: OutcomeUncertain, Resolved: false})
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateDay failed: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("fresh GetDay failed: %v", err)
	}

	confirmedMapping := false
	for _, m := range got.RemoteMappings {
		if m.LocalID == "local-a" && m.RemoteID == "remote-a" {
			confirmedMapping = true
		}
	}
	if !confirmedMapping {
		t.Errorf("expected confirmed mapping local-a -> remote-a, got %+v", got.RemoteMappings)
	}

	uncertainUnresolved := false
	for _, o := range got.Outcomes {
		if o.LocalID == "local-b" {
			if o.Status != OutcomeUncertain || o.Resolved {
				t.Errorf("expected unresolved uncertain outcome for local-b, got %+v", o)
			}
			uncertainUnresolved = true
		}
	}
	if !uncertainUnresolved {
		t.Errorf("expected uncertain outcome for local-b, got %+v", got.Outcomes)
	}
}

// TestJSONFileStore_UncertainOutcomePersistsUnresolved proves an uncertain
// outcome survives a fresh store instance with Resolved=false.
func TestJSONFileStore_UncertainOutcomePersistsUnresolved(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		UpsertOutcome(r, OperationOutcome{
			LocalID: "local-x", Action: "create", Status: OutcomeUncertain,
			Date: date, StartTime: "11:00", EndTime: "12:00", Resolved: false,
		})
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay failed: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("fresh GetDay failed: %v", err)
	}
	if len(got.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(got.Outcomes))
	}
	if got.Outcomes[0].Status != OutcomeUncertain {
		t.Errorf("expected uncertain status, got %s", got.Outcomes[0].Status)
	}
	if got.Outcomes[0].Resolved {
		t.Errorf("expected Resolved=false for uncertain outcome")
	}
}

// TestJSONFileStore_ConcurrentWriters proves the exclusive day lock makes
// concurrent UpdateDay fail closed with ErrWriterBusy and succeed after release.
func TestJSONFileStore_ConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	release, err := store.AcquireDayLock(ctx, date)
	if err != nil {
		t.Fatalf("AcquireDayLock failed: %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error { return nil }); !errors.Is(err, ErrWriterBusy) {
		t.Errorf("expected ErrWriterBusy while lock held, got %v", err)
	}

	// Goroutine variant: a second writer must also see ErrWriterBusy.
	var wg sync.WaitGroup
	goroutineErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := store.UpdateDay(ctx, date, func(r *Record) error { return nil })
		goroutineErr <- err
	}()
	wg.Wait()
	close(goroutineErr)
	if err := <-goroutineErr; !errors.Is(err, ErrWriterBusy) {
		t.Errorf("expected goroutine ErrWriterBusy while lock held, got %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	// Release is idempotent.
	if err := release(); err != nil {
		t.Errorf("second release should be safe, got %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		r.Date = date
		UpsertOutcome(r, OperationOutcome{LocalID: "local-a", Action: "create", Status: OutcomeConfirmed, Resolved: true})
		return nil
	}); err != nil {
		t.Errorf("expected UpdateDay to succeed after release, got %v", err)
	}
}

// TestJSONFileStore_CorruptRecordFailsClosed proves a corrupt record yields
// ErrRecordCorrupt from GetDay and prevents SaveDay/UpdateDay from writing.
func TestJSONFileStore_CorruptRecordFailsClosed(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	path := filepath.Join(root, "workflow", date+".json")
	corrupt := []byte("{not valid json")
	if err := os.WriteFile(path, corrupt, FilePermPrivate); err != nil {
		t.Fatalf("failed to pre-write corrupt record: %v", err)
	}

	if _, err := store.GetDay(ctx, date); !errors.Is(err, ErrRecordCorrupt) {
		t.Errorf("expected GetDay ErrRecordCorrupt, got %v", err)
	}

	if err := store.SaveDay(ctx, Record{SchemaVersion: SchemaVersion, Date: date}); !errors.Is(err, ErrRecordCorrupt) {
		t.Errorf("expected SaveDay ErrRecordCorrupt, got %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error { return nil }); !errors.Is(err, ErrRecordCorrupt) {
		t.Errorf("expected UpdateDay ErrRecordCorrupt, got %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to re-read record: %v", err)
	}
	if !bytes.Equal(after, corrupt) {
		t.Errorf("corrupt record bytes were modified: got %q", string(after))
	}
}

// TestJSONFileStore_NonRecordPayloadsFailClosed proves null/empty/foreign-schema
// documents are rejected as corrupt by GetDay/SaveDay/UpdateDay and never
// overwritten (fail-closed decoder guard).
func TestJSONFileStore_NonRecordPayloadsFailClosed(t *testing.T) {
	date := "2026-10-08"
	payloads := map[string][]byte{
		"null":          []byte("null"),
		"empty object":  []byte("{}"),
		"whitespace":    []byte("  \n\t "),
		"future schema": []byte(`{"schema_version":999,"date":"2026-10-08"}`),
		"date mismatch": []byte(`{"schema_version":1,"date":"2000-01-01"}`),
	}

	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()

			store, err := NewJSONFileStore(root)
			if err != nil {
				t.Fatalf("unexpected error creating store: %v", err)
			}

			path := filepath.Join(root, "workflow", date+".json")
			if err := os.WriteFile(path, payload, FilePermPrivate); err != nil {
				t.Fatalf("failed to pre-write payload: %v", err)
			}

			if _, err := store.GetDay(ctx, date); !errors.Is(err, ErrRecordCorrupt) {
				t.Errorf("GetDay: expected ErrRecordCorrupt, got %v", err)
			}

			if err := store.SaveDay(ctx, Record{
				SchemaVersion: SchemaVersion,
				Date:          date,
				Outcomes:      []OperationOutcome{{LocalID: "a", Action: "create", Status: OutcomeConfirmed}},
			}); !errors.Is(err, ErrRecordCorrupt) {
				t.Errorf("SaveDay: expected ErrRecordCorrupt, got %v", err)
			}

			if _, err := store.UpdateDay(ctx, date, func(r *Record) error { return nil }); !errors.Is(err, ErrRecordCorrupt) {
				t.Errorf("UpdateDay: expected ErrRecordCorrupt, got %v", err)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("failed to re-read payload: %v", err)
			}
			if !bytes.Equal(after, payload) {
				t.Errorf("payload bytes were modified: got %q", string(after))
			}
		})
	}
}

// TestJSONFileStore_LegacyReadOnly proves legacy storage records are never
// overwritten or migrated, and no remote mapping is ever inferred from them.
func TestJSONFileStore_LegacyReadOnly(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	legacyStore, err := storage.NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("failed to create legacy store: %v", err)
	}
	legacy := storage.TimesheetRecord{
		Date:      date,
		UpdatedAt: time.Now().UTC(),
		Version:   1,
		Blocks: []timesheet.TimeBlock{
			{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Title: "Legacy work"},
		},
	}
	if err := legacyStore.SaveDay(ctx, legacy); err != nil {
		t.Fatalf("failed to save legacy day: %v", err)
	}

	legacyPath := filepath.Join(root, date+".json")
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("failed to read legacy file: %v", err)
	}

	wf, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating workflow store: %v", err)
	}

	if _, err := wf.GetDay(ctx, date); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected workflow GetDay ErrRecordNotFound, got %v", err)
	}

	loaded, err := wf.LoadLegacyDay(ctx, date)
	if err != nil {
		t.Fatalf("LoadLegacyDay failed: %v", err)
	}
	if len(loaded.Blocks) != 1 || loaded.Blocks[0].ID != "block-1" {
		t.Errorf("expected legacy blocks returned, got %+v", loaded.Blocks)
	}

	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("failed to re-read legacy file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("legacy file bytes were modified")
	}

	// No workflow record and no mapping must exist.
	if _, err := wf.GetDay(ctx, date); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected no workflow record after legacy read, got %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "workflow"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read workflow dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			t.Errorf("unexpected workflow record %q created from legacy data", entry.Name())
		}
	}
}

// TestJSONFileStore_DirectorySyncFailureSurfaces proves that a directory fsync
// failure after a successful rename is returned rather than swallowed (F1),
// using the package seam to force the failure deterministically. It also proves
// the normal durability path still succeeds once the seam is restored.
func TestJSONFileStore_DirectorySyncFailureSurfaces(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	original := syncWorkflowDir
	defer func() { syncWorkflowDir = original }()

	boom := errors.New("simulated directory fsync failure")
	syncWorkflowDir = func(*os.File) error { return boom }

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		UpsertOutcome(r, OperationOutcome{LocalID: "local-a", Action: "create", Status: OutcomeConfirmed, Resolved: true})
		return nil
	}); err == nil {
		t.Fatal("expected UpdateDay to surface directory sync failure, got nil")
	} else if !errors.Is(err, boom) {
		t.Errorf("expected wrapped directory sync error, got %v", err)
	}

	if err := store.SaveDay(ctx, Record{SchemaVersion: SchemaVersion, Date: date}); err == nil {
		t.Error("expected SaveDay to surface directory sync failure, got nil")
	}

	// Restore the seam; the normal durability path must still succeed.
	syncWorkflowDir = original
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		UpsertOutcome(r, OperationOutcome{LocalID: "local-b", Action: "create", Status: OutcomeConfirmed, Resolved: true})
		return nil
	}); err != nil {
		t.Fatalf("expected UpdateDay to succeed after restoring the seam, got %v", err)
	}
}

// TestRecordSerializationHasNoSecrets proves a serialized record carries only a
// stable error code and structurally has no field for raw messages/bodies.
func TestRecordSerializationHasNoSecrets(t *testing.T) {
	const secret = "super-secret-bearer-token-value"

	record := Record{
		SchemaVersion: SchemaVersion,
		Date:          "2026-10-08",
		Outcomes: []OperationOutcome{{
			LocalID:    "local-a",
			Action:     "create",
			Status:     OutcomeFailed,
			ErrorCode:  "API_ERR_BAD_REQUEST",
			Resolved:   true,
			RecordedAt: time.Now().UTC(),
		}},
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	serialized := string(data)

	if strings.Contains(serialized, secret) {
		t.Errorf("secret leaked into serialized record: %s", serialized)
	}
	if !strings.Contains(serialized, `"error_code":"API_ERR_BAD_REQUEST"`) {
		t.Errorf("expected stable error code in serialized record: %s", serialized)
	}
	for _, forbidden := range []string{`"message"`, `"body"`, `"token"`, `"authorization"`} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("record schema exposes forbidden field %s: %s", forbidden, serialized)
		}
	}
}
