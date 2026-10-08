package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestJSONFileStore_SaveAndGet(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error creating store: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"

	record := TimesheetRecord{
		Date: date,
		Blocks: []timesheet.TimeBlock{
			{
				ID:        "meeting-1",
				Source:    "calendar",
				Category:  timesheet.CategoryMeeting,
				StartTime: "09:00",
				EndTime:   "10:00",
				Title:     "Sprint Planning",
			},
			{
				ID:        "git-1",
				Source:    "git",
				Category:  timesheet.CategoryDevelopment,
				StartTime: "10:15",
				EndTime:   "12:00",
				Title:     "Feature Dev",
			},
		},
	}

	if err := store.SaveDay(ctx, record); err != nil {
		t.Fatalf("SaveDay failed: %v", err)
	}

	loaded, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}

	if loaded.Date != date {
		t.Errorf("expected date %s, got %s", date, loaded.Date)
	}
	if loaded.Version != 1 {
		t.Errorf("expected initial version 1, got %d", loaded.Version)
	}
	// 09:00 - 10:00 is 1h; 10:15 - 12:00 is 1h45m (1.75h) -> Total = 2.75h
	if loaded.TotalHours != 2.75 {
		t.Errorf("expected TotalHours 2.75, got %f", loaded.TotalHours)
	}
	if len(loaded.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(loaded.Blocks))
	}
	if loaded.Blocks[0].Title != "Sprint Planning" {
		t.Errorf("expected block 0 title 'Sprint Planning', got '%s'", loaded.Blocks[0].Title)
	}
}

func TestJSONFileStore_AtomicRename(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	record := TimesheetRecord{
		Date: "2026-10-08",
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Title: "Work"},
		},
	}

	if err := store.SaveDay(ctx, record); err != nil {
		t.Fatalf("SaveDay failed: %v", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Errorf("found lingering temporary file: %s", entry.Name())
		}
	}
}

func TestJSONFileStore_UpsertVersionIncrement(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"

	record1 := TimesheetRecord{
		Date: date,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Title: "Initial block"},
		},
	}

	if err := store.SaveDay(ctx, record1); err != nil {
		t.Fatalf("SaveDay initial failed: %v", err)
	}

	r1, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay 1 failed: %v", err)
	}
	if r1.Version != 1 {
		t.Errorf("expected Version 1, got %d", r1.Version)
	}
	time1 := r1.UpdatedAt

	// Sleep slightly to guarantee UpdatedAt advances
	time.Sleep(10 * time.Millisecond)

	record2 := TimesheetRecord{
		Date: date,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Title: "Initial block"},
			{StartTime: "10:00", EndTime: "11:00", Title: "Appended block"},
		},
	}

	if err := store.SaveDay(ctx, record2); err != nil {
		t.Fatalf("SaveDay update failed: %v", err)
	}

	r2, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay 2 failed: %v", err)
	}
	if r2.Version != 2 {
		t.Errorf("expected Version 2, got %d", r2.Version)
	}
	if r2.TotalHours != 2.0 {
		t.Errorf("expected TotalHours 2.0, got %f", r2.TotalHours)
	}
	if !r2.UpdatedAt.After(time1) {
		t.Errorf("expected UpdatedAt to be after time1")
	}
}

func TestJSONFileStore_GetNotFound(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	_, err = store.GetDay(ctx, "2026-10-08")
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected ErrRecordNotFound, got: %v", err)
	}
}

func TestJSONFileStore_InvalidDate(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	invalidDates := []string{
		"",
		"2026/10/08",
		"2026-13-01",
		"2026-02-31",
		"invalid-date",
		"2026-10-8",
	}

	for _, invalid := range invalidDates {
		err := store.SaveDay(ctx, TimesheetRecord{Date: invalid})
		if !errors.Is(err, ErrInvalidDate) {
			t.Errorf("expected ErrInvalidDate for SaveDay with '%s', got %v", invalid, err)
		}

		_, err = store.GetDay(ctx, invalid)
		if !errors.Is(err, ErrInvalidDate) {
			t.Errorf("expected ErrInvalidDate for GetDay with '%s', got %v", invalid, err)
		}
	}
}

func TestJSONFileStore_ListDays(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	dates := []string{"2026-10-01", "2026-10-03", "2026-10-05", "2026-10-02", "2026-10-04"}
	for _, d := range dates {
		_ = store.SaveDay(ctx, TimesheetRecord{Date: d})
	}

	// Also write a non-json file and an irregular json file to test filtering
	_ = os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("text"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "config.json"), []byte("{}"), 0644)

	// Test full range
	all, err := store.ListDays(ctx, "", "")
	if err != nil {
		t.Fatalf("ListDays failed: %v", err)
	}
	expectedAll := []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05"}
	if fmt.Sprintf("%v", all) != fmt.Sprintf("%v", expectedAll) {
		t.Errorf("expected %v, got %v", expectedAll, all)
	}

	// Test sub-range [2026-10-02, 2026-10-04]
	sub, err := store.ListDays(ctx, "2026-10-02", "2026-10-04")
	if err != nil {
		t.Fatalf("ListDays sub-range failed: %v", err)
	}
	expectedSub := []string{"2026-10-02", "2026-10-03", "2026-10-04"}
	if fmt.Sprintf("%v", sub) != fmt.Sprintf("%v", expectedSub) {
		t.Errorf("expected %v, got %v", expectedSub, sub)
	}

	// Test invalid bounds
	_, err = store.ListDays(ctx, "invalid-bound", "")
	if !errors.Is(err, ErrInvalidDate) {
		t.Errorf("expected ErrInvalidDate for invalid fromDate, got: %v", err)
	}
}

func TestJSONFileStore_ContextCancelled(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel context immediately

	err = store.SaveDay(ctx, TimesheetRecord{Date: "2026-10-08"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on SaveDay, got: %v", err)
	}

	_, err = store.GetDay(ctx, "2026-10-08")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on GetDay, got: %v", err)
	}

	_, err = store.ListDays(ctx, "", "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled on ListDays, got: %v", err)
	}
}

func TestJSONFileStore_CapacityLimits(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// Exactly 100 blocks succeeds
	blocks100 := make([]timesheet.TimeBlock, 100)
	for i := 0; i < 100; i++ {
		blocks100[i] = timesheet.TimeBlock{StartTime: "09:00", EndTime: "09:05", Title: fmt.Sprintf("B%d", i)}
	}
	err = store.SaveDay(ctx, TimesheetRecord{Date: "2026-10-08", Blocks: blocks100})
	if err != nil {
		t.Errorf("expected 100 blocks to succeed, got: %v", err)
	}

	// 101 blocks fails with ErrExcessiveBlocks
	blocks101 := make([]timesheet.TimeBlock, 101)
	err = store.SaveDay(ctx, TimesheetRecord{Date: "2026-10-09", Blocks: blocks101})
	if !errors.Is(err, ErrExcessiveBlocks) {
		t.Errorf("expected ErrExcessiveBlocks for 101 blocks, got: %v", err)
	}
}

func TestJSONFileStore_ResolveDataDirPrecedence(t *testing.T) {
	// 1. Explicit argument overrides everything
	custom := "/tmp/explicit/custom/dir"
	resolved := ResolveDataDir(custom)
	if resolved != filepath.Clean(custom) {
		t.Errorf("expected %s, got %s", custom, resolved)
	}

	// 2. Env var overrides home directory
	envPath := "/tmp/env/timesheet/dir"
	t.Setenv("TIMESHEET_DATA_DIR", envPath)
	resolvedEnv := ResolveDataDir("")
	if resolvedEnv != filepath.Clean(envPath) {
		t.Errorf("expected %s from env var, got %s", envPath, resolvedEnv)
	}

	// 3. Fallback when env var empty
	t.Setenv("TIMESHEET_DATA_DIR", "")
	resolvedHome := ResolveDataDir("")
	if !strings.HasSuffix(resolvedHome, filepath.Join(".timesheet", "records")) && !strings.HasSuffix(resolvedHome, filepath.Join("data", "timesheets")) {
		t.Errorf("unexpected fallback dir: %s", resolvedHome)
	}
}

func TestJSONFileStore_Permissions(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "secure_records")
	store, err := NewJSONFileStore(subDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"
	if err := store.SaveDay(ctx, TimesheetRecord{Date: date}); err != nil {
		t.Fatalf("SaveDay failed: %v", err)
	}

	dirStat, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("Stat dir failed: %v", err)
	}
	if dirStat.Mode().Perm() != DirPermPrivate {
		t.Errorf("expected dir perms %o, got %o", DirPermPrivate, dirStat.Mode().Perm())
	}

	fileStat, err := os.Stat(filepath.Join(subDir, date+".json"))
	if err != nil {
		t.Fatalf("Stat file failed: %v", err)
	}
	if fileStat.Mode().Perm() != FilePermPrivate {
		t.Errorf("expected file perms %o, got %o", FilePermPrivate, fileStat.Mode().Perm())
	}
}

func TestAssertion_Subprocess(t *testing.T) {
	if os.Getenv("BE_FATAL_ASSERTION_CONTEXT") == "1" {
		store := &JSONFileStore{baseDir: "/tmp"}
		_ = store.SaveDay(nil, TimesheetRecord{Date: "2026-10-08"}) // should panic
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestAssertion_Subprocess")
	cmd.Env = append(os.Environ(), "BE_FATAL_ASSERTION_CONTEXT=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected fatal assertion panic to exit with non-zero exit code, but succeeded")
	}
	if !strings.Contains(string(output), AssertStorageContextNil) {
		t.Errorf("expected output to contain %s, got: %s", AssertStorageContextNil, string(output))
	}
}

func TestJSONFileStore_BaseDir(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.BaseDir() != tempDir {
		t.Errorf("expected baseDir %s, got %s", tempDir, store.BaseDir())
	}
}

func TestJSONFileStore_CalculateTotalHours_MidnightAndInvalid(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	record := TimesheetRecord{
		Date: "2026-10-08",
		Blocks: []timesheet.TimeBlock{
			{StartTime: "23:00", EndTime: "01:00", Title: "Midnight crossing"}, // 2 hours
			{StartTime: "invalid", EndTime: "02:00", Title: "Invalid time"},    // skipped
		},
	}

	if err := store.SaveDay(ctx, record); err != nil {
		t.Fatalf("SaveDay failed: %v", err)
	}

	loaded, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}
	if loaded.TotalHours != 2.0 {
		t.Errorf("expected TotalHours 2.0, got %f", loaded.TotalHours)
	}
}

func TestJSONFileStore_ReadRecordFile_MalformedAndTooLarge(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewJSONFileStore(tempDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()

	// Write malformed JSON
	dateBad := "2026-10-08"
	_ = os.WriteFile(filepath.Join(tempDir, dateBad+".json"), []byte("{malformed json"), 0600)
	_, err = store.GetDay(ctx, dateBad)
	if err == nil || !strings.Contains(err.Error(), "failed to parse timesheet JSON") {
		t.Errorf("expected JSON parse error, got: %v", err)
	}

	// Write oversized payload exceeding MaxRecordSizeBytes
	dateLarge := "2026-10-09"
	largeFile, err := os.Create(filepath.Join(tempDir, dateLarge+".json"))
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	// Truncate to MaxRecordSizeBytes + 10 bytes
	_ = largeFile.Truncate(MaxRecordSizeBytes + 10)
	_ = largeFile.Close()

	_, err = store.GetDay(ctx, dateLarge)
	if !errors.Is(err, ErrRecordTooLarge) {
		t.Errorf("expected ErrRecordTooLarge, got: %v", err)
	}
}

func TestJSONFileStore_ListDays_MissingDirAndToDateInvalid(t *testing.T) {
	nonExistent := filepath.Join(t.TempDir(), "not_created")
	store := &JSONFileStore{baseDir: nonExistent}

	ctx := context.Background()
	days, err := store.ListDays(ctx, "", "")
	if err != nil {
		t.Fatalf("expected nil error on missing dir, got: %v", err)
	}
	if len(days) != 0 {
		t.Errorf("expected empty list on missing dir, got: %v", days)
	}

	// Invalid toDate
	_, err = store.ListDays(ctx, "2026-10-01", "invalid-to-date")
	if !errors.Is(err, ErrInvalidDate) {
		t.Errorf("expected ErrInvalidDate for toDate, got: %v", err)
	}
}

func TestAssert_TrueCondition(t *testing.T) {
	// Assert with true should not panic
	Assert(true, "NO_PANIC")
}

func TestAssert_RecoverFalse(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic")
		}
	}()
	Assert(false, "EXPECTED_PANIC")
}

func TestJSONFileStore_SaveDay_ExistingMalformed(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewJSONFileStore(tempDir)
	date := "2026-10-08"
	_ = os.WriteFile(filepath.Join(tempDir, date+".json"), []byte("corrupt-data"), 0600)

	err := store.SaveDay(context.Background(), TimesheetRecord{Date: date})
	if err == nil || !strings.Contains(err.Error(), "failed checking existing record for versioning") {
		t.Errorf("expected error checking existing record, got: %v", err)
	}
}

func TestJSONFileStore_SaveDay_RecordTooLarge(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewJSONFileStore(tempDir)
	date := "2026-10-08"

	hugeDesc := strings.Repeat("A", int(MaxRecordSizeBytes)+100)
	record := TimesheetRecord{
		Date: date,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Description: hugeDesc},
		},
	}
	err := store.SaveDay(context.Background(), record)
	if !errors.Is(err, ErrRecordTooLarge) {
		t.Errorf("expected ErrRecordTooLarge, got: %v", err)
	}
}
