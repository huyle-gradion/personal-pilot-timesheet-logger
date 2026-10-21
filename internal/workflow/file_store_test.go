package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
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
	if err := store.saveDay(ctx, record); err != nil {
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
	if err := store.saveDay(ctx, record); err != nil {
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

	if err := store.saveDay(ctx, Record{SchemaVersion: SchemaVersion, Date: date}); !errors.Is(err, ErrRecordCorrupt) {
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

			if err := store.saveDay(ctx, Record{
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

// legacyArtifactJSON is a frozen copy of what the retired timesheet/storage
// packages wrote to <rootDir>/<date>.json. Tests read it as bytes instead of
// building it through those packages, so the workflow package owns no runtime or
// build dependency on code that is being deleted.
const legacyArtifactJSON = `{
  "date": "2026-10-08",
  "updated_at": "2026-10-09T07:18:00.919721Z",
  "version": 1,
  "total_hours": 1.5,
  "blocks": [
    {
      "id": "block-1",
      "source": "git",
      "category": "development",
      "startTime": "09:00",
      "endTime": "10:30",
      "date": "2026-10-08",
      "title": "Legacy work",
      "description": "Frozen legacy artifact.",
      "entries": [
        {
          "id": "entry-1",
          "source": "git",
          "timestamp": "2026-10-08T09:00:00+07:00",
          "endTime": "2026-10-08T10:30:00+07:00",
          "title": "Legacy commit",
          "description": "Frozen entry.",
          "metadata": {
            "status": "confirmed",
            "location": "Hanoi",
            "duration_minutes": 90,
            "all_day": false,
            "transparent": false,
            "repo": "huyle-gradion/personal-pilot-timesheet-logger",
            "branch": "main",
            "pr_number": 1
          }
        }
      ],
      "collision": false
    }
  ]
}`

// TestJSONFileStore_LegacyReadOnly proves legacy storage records are never
// overwritten or migrated, and no remote mapping is ever inferred from them.
func TestJSONFileStore_LegacyReadOnly(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	legacyPath := filepath.Join(root, date+".json")
	if err := os.WriteFile(legacyPath, []byte(legacyArtifactJSON), FilePermPrivate); err != nil {
		t.Fatalf("failed to write legacy fixture: %v", err)
	}

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
	if loaded.Date != date || loaded.Version != 1 {
		t.Errorf("expected legacy date/version preserved, got %q v%d", loaded.Date, loaded.Version)
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

// TestJSONFileStore_LegacyUnknownKeysTolerated proves unknown-key tolerance is a
// property of the LOADER, not just of the structs: a future
// json.Decoder.DisallowUnknownFields inside LoadLegacyDay would fail the owner's
// real files, and only a read through LoadLegacyDay catches that. Extra keys appear
// at root, block, entry, and metadata level.
func TestJSONFileStore_LegacyUnknownKeysTolerated(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	artifact := `{
  "date": "2026-10-08",
  "updated_at": "2026-10-09T07:18:00.919721Z",
  "version": 3,
  "total_hours": 2.25,
  "future_root_key": {"nested": [1, 2, 3]},
  "blocks": [
    {
      "id": "block-1",
      "category": "development",
      "title": "Legacy work",
      "future_block_key": 7,
      "entries": [
        {
          "id": "entry-1",
          "title": "Legacy commit",
          "future_entry_key": "x",
          "metadata": {"repo": "huyle-gradion/personal-pilot-timesheet-logger", "future_meta_key": true}
        }
      ],
      "collision": true
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, date+".json"), []byte(artifact), FilePermPrivate); err != nil {
		t.Fatalf("failed to write legacy fixture: %v", err)
	}

	wf, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("unexpected error creating workflow store: %v", err)
	}

	loaded, err := wf.LoadLegacyDay(ctx, date)
	if err != nil {
		t.Fatalf("LoadLegacyDay must tolerate unknown keys, got %v", err)
	}
	if loaded.Version != 3 || loaded.TotalHours != 2.25 || len(loaded.Blocks) != 1 {
		t.Fatalf("known root fields lost alongside unknown keys: %+v", loaded)
	}
	block := loaded.Blocks[0]
	if block.ID != "block-1" || block.Category != "development" || block.Title != "Legacy work" || !block.HasCollision {
		t.Errorf("known block fields lost alongside unknown keys: %+v", block)
	}
	if len(block.Entries) != 1 || block.Entries[0].ID != "entry-1" || block.Entries[0].Title != "Legacy commit" {
		t.Fatalf("known entry fields lost alongside unknown keys: %+v", block.Entries)
	}
	if block.Entries[0].Metadata == nil || block.Entries[0].Metadata.Repo != "huyle-gradion/personal-pilot-timesheet-logger" {
		t.Errorf("known metadata fields lost alongside unknown keys: %+v", block.Entries[0].Metadata)
	}
}

// TestLegacyRecord_FrozenWireFormat pins the frozen legacy wire format. The real
// owner files at ~/.timesheet/records/*.json must keep decoding after the retired
// timesheet and storage packages are deleted, so the JSON tags on
// LegacyRecord/LegacyBlock/LegacyEntry/LegacyEntryMetadata are a contract, not an
// implementation detail: any rename, retag, or reordering-visible change here is
// silent data loss. The test decodes a fixture that carries every field, checks
// each value, re-marshals it, and compares the exact key set. It also proves
// unknown keys stay tolerated so artifacts written by other versions still read.
// Note the limit: this test pins key NAMES and values, not numeric width — it would
// not notice int widening to int64, which is why the encoder side is not part of the
// compatibility claim (the types are decode-only in production).
func TestLegacyRecord_FrozenWireFormat(t *testing.T) {
	var record LegacyRecord
	if err := json.Unmarshal([]byte(legacyArtifactJSON), &record); err != nil {
		t.Fatalf("frozen artifact no longer decodes: %v", err)
	}

	if record.Date != "2026-10-08" {
		t.Errorf("date: got %q", record.Date)
	}
	if got, want := record.UpdatedAt.UTC().Format(time.RFC3339Nano), "2026-10-09T07:18:00.919721Z"; got != want {
		t.Errorf("updated_at: got %q, want %q", got, want)
	}
	if record.Version != 1 {
		t.Errorf("version: got %d", record.Version)
	}
	if record.TotalHours != 1.5 {
		t.Errorf("total_hours: got %v", record.TotalHours)
	}
	if len(record.Blocks) != 1 {
		t.Fatalf("blocks: got %d", len(record.Blocks))
	}

	block := record.Blocks[0]
	if block.ID != "block-1" || block.Source != "git" || block.Category != "development" ||
		block.StartTime != "09:00" || block.EndTime != "10:30" || block.Date != "2026-10-08" ||
		block.Title != "Legacy work" || block.Description != "Frozen legacy artifact." ||
		block.HasCollision {
		t.Errorf("block fields not preserved: %+v", block)
	}
	if len(block.Entries) != 1 {
		t.Fatalf("entries: got %d", len(block.Entries))
	}

	entry := block.Entries[0]
	if entry.ID != "entry-1" || entry.Source != "git" ||
		entry.Timestamp != "2026-10-08T09:00:00+07:00" || entry.EndTime != "2026-10-08T10:30:00+07:00" ||
		entry.Title != "Legacy commit" || entry.Description != "Frozen entry." {
		t.Errorf("entry fields not preserved: %+v", entry)
	}
	if entry.Metadata == nil {
		t.Fatal("entry metadata: got nil")
	}
	meta := entry.Metadata
	if meta.Status != "confirmed" || meta.Location == nil || *meta.Location != "Hanoi" ||
		meta.DurationMinutes != 90 || meta.AllDay || meta.Transparent ||
		meta.Repo != "huyle-gradion/personal-pilot-timesheet-logger" || meta.Branch != "main" ||
		meta.PRNumber != 1 {
		t.Errorf("entry metadata not preserved: %+v", meta)
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("frozen artifact no longer encodes: %v", err)
	}

	var roundTripped map[string]any
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("re-marshalled artifact is not valid JSON: %v", err)
	}
	assertExactKeys(t, "record", roundTripped, []string{"date", "updated_at", "version", "total_hours", "blocks"})

	blocks, ok := roundTripped["blocks"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("blocks not re-encoded as an array: %v", roundTripped["blocks"])
	}
	blockMap, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatalf("block not re-encoded as an object: %v", blocks[0])
	}
	assertExactKeys(t, "block", blockMap,
		[]string{"id", "source", "category", "startTime", "endTime", "date", "title", "description", "entries", "collision"})

	entries, ok := blockMap["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries not re-encoded as an array: %v", blockMap["entries"])
	}
	entryMap, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("entry not re-encoded as an object: %v", entries[0])
	}
	assertExactKeys(t, "entry", entryMap,
		[]string{"id", "source", "timestamp", "endTime", "title", "description", "metadata"})

	metadataMap, ok := entryMap["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata not re-encoded as an object: %v", entryMap["metadata"])
	}
	// all_day and transparent are `omitempty` bools: a false value is dropped,
	// which is exactly what the retired encoder did. The fixture's false values
	// must therefore not appear, and the true case is pinned below.
	assertExactKeys(t, "metadata", metadataMap,
		[]string{"status", "location", "duration_minutes", "repo", "branch", "pr_number"})

	truthyMeta := *record.Blocks[0].Entries[0].Metadata
	truthyMeta.AllDay = true
	truthyMeta.Transparent = true
	truthyEncoded, err := json.Marshal(truthyMeta)
	if err != nil {
		t.Fatalf("metadata with true bools did not encode: %v", err)
	}
	var truthyMap map[string]any
	if err := json.Unmarshal(truthyEncoded, &truthyMap); err != nil {
		t.Fatalf("re-marshalled metadata is not valid JSON: %v", err)
	}
	assertExactKeys(t, "metadata(true bools)", truthyMap,
		[]string{"status", "location", "duration_minutes", "all_day", "transparent", "repo", "branch", "pr_number"})

	// Unknown keys must stay tolerated: real artifacts may carry fields written
	// by a version of the retired packages newer or older than this copy.
	withUnknown := `{
	  "date": "2026-10-08",
	  "updated_at": "2026-10-09T07:18:00.919721Z",
	  "version": 2,
	  "total_hours": 0.5,
	  "future_top_level": {"nested": true},
	  "blocks": [
	    {
	      "id": "block-1",
	      "category": "development",
	      "future_block_field": 7,
	      "entries": [{"id": "entry-1", "future_entry_field": "x"}],
	      "collision": true
	    }
	  ]
	}`
	var tolerant LegacyRecord
	if err := json.Unmarshal([]byte(withUnknown), &tolerant); err != nil {
		t.Fatalf("unknown keys must not fail decode: %v", err)
	}
	if tolerant.Version != 2 || tolerant.TotalHours != 0.5 || len(tolerant.Blocks) != 1 ||
		tolerant.Blocks[0].ID != "block-1" || tolerant.Blocks[0].Category != "development" ||
		!tolerant.Blocks[0].HasCollision || len(tolerant.Blocks[0].Entries) != 1 ||
		tolerant.Blocks[0].Entries[0].ID != "entry-1" {
		t.Errorf("known fields not preserved alongside unknown keys: %+v", tolerant)
	}
}

func assertExactKeys(t *testing.T, label string, object map[string]any, want []string) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("%s keys: got %v, want %v", label, sortedKeys(object), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Errorf("%s missing key %q (got %v)", label, key, sortedKeys(object))
		}
	}
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

	if err := store.saveDay(ctx, Record{SchemaVersion: SchemaVersion, Date: date}); err == nil {
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
