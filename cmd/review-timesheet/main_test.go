package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func sampleCollidingBlocks() []timesheet.TimeBlock {
	return []timesheet.TimeBlock{
		{
			ID:           "cal-001",
			Source:       "calendar",
			Category:     timesheet.CategoryMeeting,
			StartTime:    "09:00",
			EndTime:      "10:00",
			Date:         "2026-10-08",
			Title:        "Sprint Planning",
			Description:  "- Backlog triage",
			HasCollision: true,
		},
		{
			ID:           "git-001",
			Source:       "git",
			Category:     timesheet.CategoryDevelopment,
			StartTime:    "09:30",
			EndTime:      "10:00",
			Date:         "2026-10-08",
			Title:        "feat(storage): implement store",
			Description:  "- Initial commit",
			HasCollision: true,
		},
		{
			ID:           "git-002",
			Source:       "git",
			Category:     timesheet.CategoryDevelopment,
			StartTime:    "14:00",
			EndTime:      "15:00",
			Date:         "2026-10-08",
			Title:        "test(storage): coverage",
			Description:  "- Add test suite",
			HasCollision: false,
		},
	}
}

func writeTempBlocksJSON(t *testing.T, blocks []timesheet.TimeBlock) string {
	t.Helper()
	tmpFile := filepath.Join(t.TempDir(), "blocks.json")
	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshaling blocks: %v", err)
	}
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		t.Fatalf("writing temp json: %v", err)
	}
	return tmpFile
}

func TestCLI_UsageAndInvalidFlags(t *testing.T) {
	ctx := context.Background()

	// 1. Invalid date
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"-date", "invalid-date"}, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for invalid date, got %d", code)
	}

	// 2. Invalid auto-resolve strategy
	out.Reset()
	errOut.Reset()
	code = run(ctx, []string{"-date", "2026-10-08", "-auto-resolve", "invalid"}, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for invalid auto-resolve, got %d", code)
	}

	// 3. No input and no existing store record
	out.Reset()
	errOut.Reset()
	tempDir := t.TempDir()
	code = run(ctx, []string{"-date", "2026-10-08", "-storage-dir", tempDir}, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for missing input, got %d", code)
	}
}

func TestCLI_NonInteractiveFlag(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	inputFile := writeTempBlocksJSON(t, sampleCollidingBlocks())
	outputJSON := filepath.Join(tempDir, "resolved.json")

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-auto-resolve", "keep_both",
		"-storage-dir", tempDir,
		"-output", outputJSON,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (errOut: %s)", code, errOut.String())
	}

	// Verify storage persistence
	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}
	record, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("error retrieving stored record: %v", err)
	}
	if record.Version != 1 {
		t.Errorf("expected Version 1, got %d", record.Version)
	}
	if len(record.Blocks) != 2 { // 1 joint block + 1 non-colliding git block
		t.Errorf("expected 2 resolved blocks, got %d", len(record.Blocks))
	}
	if record.Blocks[0].Source != "joint" {
		t.Errorf("expected first block to be joint, got %s", record.Blocks[0].Source)
	}

	// Verify file output
	outData, err := os.ReadFile(outputJSON)
	if err != nil {
		t.Fatalf("reading output json: %v", err)
	}
	var outRec storage.TimesheetRecord
	if err := json.Unmarshal(outData, &outRec); err != nil {
		t.Fatalf("unmarshaling output json: %v", err)
	}
	if outRec.Date != "2026-10-08" {
		t.Errorf("expected date 2026-10-08, got %s", outRec.Date)
	}
}

func TestCLI_OutputStdout(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	inputFile := writeTempBlocksJSON(t, sampleCollidingBlocks())

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-auto-resolve", "discard",
		"-storage-dir", tempDir,
		"-output", "-",
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	// stdout should contain valid json
	stdoutStr := out.String()
	if !strings.Contains(stdoutStr, `"total_hours"`) {
		t.Errorf("expected json output on stdout, got: %s", stdoutStr)
	}
}

func TestCLI_ExistingRecordLoadAndUpdate(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}

	// Pre-populate day in storage
	initialRec := storage.TimesheetRecord{
		Date:       "2026-10-08",
		Version:    1,
		TotalHours: 2.0,
		Blocks: []timesheet.TimeBlock{
			{
				ID:        "initial-001",
				Source:    "git",
				Category:  timesheet.CategoryDevelopment,
				StartTime: "09:00",
				EndTime:   "11:00",
				Date:      "2026-10-08",
				Title:     "Old commit log",
			},
		},
	}
	if err := store.SaveDay(ctx, initialRec); err != nil {
		t.Fatalf("saving initial record: %v", err)
	}

	// Run review CLI loading from storage without -input
	var out, errOut bytes.Buffer
	args := []string{
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	// Verify record updated to Version 2
	updatedRec, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("fetching updated record: %v", err)
	}
	if updatedRec.Version != 2 {
		t.Errorf("expected Version 2, got %d", updatedRec.Version)
	}
}

func TestCLI_ExistingRecordWithInputMerge(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}

	// Pre-populate day in storage
	initialRec := storage.TimesheetRecord{
		Date:       "2026-10-08",
		Version:    1,
		TotalHours: 1.0,
		Blocks: []timesheet.TimeBlock{
			{
				ID:        "existing-001",
				Source:    "calendar",
				Category:  timesheet.CategoryMeeting,
				StartTime: "09:00",
				EndTime:   "10:00",
				Date:      "2026-10-08",
				Title:     "Existing Meeting",
			},
		},
	}
	if err := store.SaveDay(ctx, initialRec); err != nil {
		t.Fatalf("saving initial record: %v", err)
	}

	newBlocks := []timesheet.TimeBlock{
		{
			ID:        "new-001",
			Source:    "git",
			Category:  timesheet.CategoryDevelopment,
			StartTime: "10:00",
			EndTime:   "11:00",
			Date:      "2026-10-08",
			Title:     "Newly fetched work",
		},
	}
	inputFile := writeTempBlocksJSON(t, newBlocks)

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	updatedRec, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("fetching updated record: %v", err)
	}
	if updatedRec.Version != 2 {
		t.Errorf("expected Version 2, got %d", updatedRec.Version)
	}
	if len(updatedRec.Blocks) != 2 {
		t.Errorf("expected 2 merged blocks, got %d", len(updatedRec.Blocks))
	}
}

func TestCLI_InteractivePromptResolution(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	inputFile := writeTempBlocksJSON(t, sampleCollidingBlocks())

	// Simulated interactive inputs:
	// "1\n" -> Resolution option 1 (keep both)
	// "s\n" -> Save and persist
	// "y\n" -> Confirm submission
	userInput := "1\n" + "s\n" + "y\n"

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(userInput), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}
	rec, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("retrieving record: %v", err)
	}
	if rec.Blocks[0].Source != "joint" {
		t.Errorf("expected joint block from keep_both, got %s", rec.Blocks[0].Source)
	}
}

func TestCLI_InteractiveSeparateAndDiscard(t *testing.T) {
	ctx := context.Background()

	// 1. Test separate: choice "2"
	t.Run("interactive_separate", func(t *testing.T) {
		tempDir := t.TempDir()
		inputFile := writeTempBlocksJSON(t, sampleCollidingBlocks())
		userInput := "2\n" + "s\n" + "y\n"
		var out, errOut bytes.Buffer
		args := []string{
			"-input", inputFile,
			"-date", "2026-10-08",
			"-storage-dir", tempDir,
			"-sink", "none",
		}
		code := run(ctx, args, strings.NewReader(userInput), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("expected ExitOK, got %d", code)
		}

		store, storeErr := storage.NewJSONFileStore(tempDir)
		if storeErr != nil {
			t.Fatalf("error initializing store: %v", storeErr)
		}
		rec, _ := store.GetDay(ctx, "2026-10-08")
		if len(rec.Blocks) != 3 {
			t.Errorf("expected 3 blocks (meeting, shifted git, afternoon git), got %d", len(rec.Blocks))
		}
	})

	// 2. Test discard: choice "3"
	t.Run("interactive_discard", func(t *testing.T) {
		tempDir := t.TempDir()
		inputFile := writeTempBlocksJSON(t, sampleCollidingBlocks())
		userInput := "3\n" + "s\n" + "y\n"
		var out, errOut bytes.Buffer
		args := []string{
			"-input", inputFile,
			"-date", "2026-10-08",
			"-storage-dir", tempDir,
			"-sink", "none",
		}
		code := run(ctx, args, strings.NewReader(userInput), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("expected ExitOK, got %d", code)
		}

		store, storeErr := storage.NewJSONFileStore(tempDir)
		if storeErr != nil {
			t.Fatalf("error initializing store: %v", storeErr)
		}
		rec, _ := store.GetDay(ctx, "2026-10-08")
		if len(rec.Blocks) != 2 {
			t.Errorf("expected 2 blocks (meeting + afternoon git), got %d", len(rec.Blocks))
		}
	})
}

func TestCLI_InteractiveEditActions(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	blocks := []timesheet.TimeBlock{
		{
			ID:        "b-1",
			Source:    "git",
			Category:  timesheet.CategoryDevelopment,
			StartTime: "09:00",
			EndTime:   "10:00",
			Date:      "2026-10-08",
			Title:     "Initial Block",
		},
		{
			ID:        "b-2",
			Source:    "git",
			Category:  timesheet.CategoryMaintenance,
			StartTime: "10:00",
			EndTime:   "11:00",
			Date:      "2026-10-08",
			Title:     "Second Block to Delete",
		},
	}
	inputFile := writeTempBlocksJSON(t, blocks)

	// Interactive sequence:
	// "d 2\n" -> delete block 2
	// "a\n11:00\n12:00\nresearch_docs\nManual ADR\nADR design\n" -> add manual block
	// "e 1\n09:15\n10:15\nmaintenance\nUpdated Title\n" -> edit block 1 times, cat, title
	// "s\n" -> save
	// "y\n" -> confirm
	var inputBuilder strings.Builder
	inputBuilder.WriteString("d 2\n")
	inputBuilder.WriteString("a\n11:00\n12:00\nresearch_docs\nManual ADR\nADR design\n")
	inputBuilder.WriteString("e 1\n09:15\n10:15\nmaintenance\nUpdated Title\n")
	inputBuilder.WriteString("s\n")
	inputBuilder.WriteString("y\n")

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(inputBuilder.String()), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}
	rec, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("retrieving record: %v", err)
	}

	if len(rec.Blocks) != 2 {
		t.Fatalf("expected 2 blocks after delete and add, got %d", len(rec.Blocks))
	}
	if rec.Blocks[0].Title != "Updated Title" {
		t.Errorf("expected updated title 'Updated Title', got %q", rec.Blocks[0].Title)
	}
	if rec.Blocks[0].StartTime != "09:15" || rec.Blocks[0].EndTime != "10:15" || rec.Blocks[0].Category != timesheet.CategoryMaintenance {
		t.Errorf("expected updated times and category, got %+v", rec.Blocks[0])
	}
	if rec.Blocks[1].Title != "Manual ADR" || rec.Blocks[1].Category != timesheet.CategoryResearchDocs {
		t.Errorf("expected added manual block, got %+v", rec.Blocks[1])
	}
}

func TestCLI_InteractiveQuitAndAbort(t *testing.T) {
	ctx := context.Background()

	// 1. Quit with 'q'
	t.Run("quit_action", func(t *testing.T) {
		tempDir := t.TempDir()
		inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
			{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Task"},
		})

		var out, errOut bytes.Buffer
		args := []string{"-input", inputFile, "-date", "2026-10-08", "-storage-dir", tempDir, "-sink", "none"}
		code := run(ctx, args, strings.NewReader("q\n"), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("expected ExitOK on quit, got %d", code)
		}
		if !strings.Contains(out.String(), "Exiting without saving.") {
			t.Errorf("expected exit message, got: %s", out.String())
		}
	})

	// 2. Abort on confirmation 'n'
	t.Run("abort_confirm", func(t *testing.T) {
		tempDir := t.TempDir()
		inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
			{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Task"},
		})

		var out, errOut bytes.Buffer
		args := []string{"-input", inputFile, "-date", "2026-10-08", "-storage-dir", tempDir, "-sink", "none"}
		code := run(ctx, args, strings.NewReader("s\nn\n"), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("expected ExitOK on abort, got %d", code)
		}
		if !strings.Contains(out.String(), "Aborted by user.") {
			t.Errorf("expected abort message, got: %s", out.String())
		}
	})

	// 3. EOF in edit loop confirms save
	t.Run("eof_save", func(t *testing.T) {
		tempDir := t.TempDir()
		inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
			{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Task"},
		})

		var out, errOut bytes.Buffer
		args := []string{"-input", inputFile, "-date", "2026-10-08", "-storage-dir", tempDir, "-sink", "none"}
		code := run(ctx, args, strings.NewReader(""), &out, &errOut)
		if code != ExitOK {
			t.Fatalf("expected ExitOK on EOF, got %d", code)
		}
	})
}

func TestCLI_InteractiveExistingRecord(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}

	initialRec := storage.TimesheetRecord{
		Date:       "2026-10-08",
		Version:    1,
		TotalHours: 1.0,
		Blocks: []timesheet.TimeBlock{
			{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Work", Category: timesheet.CategoryDevelopment},
		},
	}
	if err := store.SaveDay(ctx, initialRec); err != nil {
		t.Fatalf("saving initial record: %v", err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-date", "2026-10-08",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader("s\ny\n"), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Found existing record for 2026-10-08") {
		t.Errorf("expected existing record log, got: %s", out.String())
	}
}

func TestCLI_DurationEdgeCases(t *testing.T) {
	// 1. Invalid time formatting fallback
	invalidBlock := timesheet.TimeBlock{StartTime: "invalid", EndTime: "invalid"}
	if d := calculateBlockDurationMinutes(invalidBlock); d != 30 {
		t.Errorf("expected 30m fallback for invalid format, got %d", d)
	}

	// 2. Overnight wrap
	overnightBlock := timesheet.TimeBlock{StartTime: "23:00", EndTime: "01:00"}
	if d := calculateBlockDurationMinutes(overnightBlock); d != 120 {
		t.Errorf("expected 120m for overnight block, got %d", d)
	}
}

func TestCLI_PipedSimpleEntryInput(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	entries := []timesheet.SimpleEntry{
		{
			ID:        "entry-1",
			Source:    "git",
			Timestamp: "2026-10-08T09:15:00Z",
			EndTime:   "2026-10-08T09:45:00Z",
			Title:     "feat(timesheet): support piping",
		},
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshaling entries: %v", err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-input", "-",
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempDir,
		"-sink", "console",
	}

	code := run(ctx, args, bytes.NewReader(data), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK for piped simple entries, got %d (err: %s)", code, errOut.String())
	}

	store, storeErr := storage.NewJSONFileStore(tempDir)
	if storeErr != nil {
		t.Fatalf("error initializing store: %v", storeErr)
	}
	rec, err := store.GetDay(ctx, "2026-10-08")
	if err != nil {
		t.Fatalf("fetching record: %v", err)
	}
	if len(rec.Blocks) != 1 {
		t.Fatalf("expected 1 clustered block, got %d", len(rec.Blocks))
	}
}

func TestCLI_ObsidianSinkExport(t *testing.T) {
	ctx := context.Background()
	tempStorageDir := t.TempDir()
	tempVaultDir := t.TempDir()
	dailyDir := filepath.Join(tempVaultDir, "02 - Daily & Logs")
	if err := os.MkdirAll(dailyDir, 0750); err != nil {
		t.Fatalf("creating daily dir: %v", err)
	}
	dailyNotePath := filepath.Join(dailyDir, "2026-10-08.md")
	if err := os.WriteFile(dailyNotePath, []byte("# Daily log 2026-10-08\n\n## Standup\n- Standup notes\n"), 0600); err != nil {
		t.Fatalf("writing initial note: %v", err)
	}

	t.Setenv("OBSIDIAN_VAULT_DIR", tempVaultDir)

	inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
		{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Work", Category: timesheet.CategoryDevelopment},
	})

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempStorageDir,
		"-sink", "obsidian",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	content, err := os.ReadFile(dailyNotePath)
	if err != nil {
		t.Fatalf("reading exported daily note: %v", err)
	}
	if !strings.Contains(string(content), "## Clustered timesheet") {
		t.Errorf("expected Clustered timesheet section in daily note, got:\n%s", string(content))
	}
}

func TestCLI_InputAsTimesheetRecord(t *testing.T) {
	ctx := context.Background()
	tempStorageDir := t.TempDir()

	record := storage.TimesheetRecord{
		Date:       "2026-10-08",
		Version:    1,
		TotalHours: 1.0,
		Blocks: []timesheet.TimeBlock{
			{ID: "rec-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Record block", Category: timesheet.CategoryDevelopment},
		},
	}
	bytesData, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshaling record: %v", err)
	}
	tempFile := filepath.Join(tempStorageDir, "input_record.json")
	if err := os.WriteFile(tempFile, bytesData, 0600); err != nil {
		t.Fatalf("writing input record: %v", err)
	}

	var out, errOut bytes.Buffer
	args := []string{
		"-input", tempFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempStorageDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
}

func TestCLI_InputErrors(t *testing.T) {
	ctx := context.Background()
	tempStorageDir := t.TempDir()

	// 1. Missing input file
	t.Run("missing_file", func(t *testing.T) {
		var out, errOut bytes.Buffer
		args := []string{"-input", "/non/existent/path/never_there.json", "-storage-dir", tempStorageDir}
		code := run(ctx, args, strings.NewReader(""), &out, &errOut)
		if code != ExitUsage {
			t.Fatalf("expected ExitUsage for missing file, got %d", code)
		}
	})

	// 2. Empty input from reader
	t.Run("empty_input", func(t *testing.T) {
		var out, errOut bytes.Buffer
		args := []string{"-input", "-", "-storage-dir", tempStorageDir}
		code := run(ctx, args, strings.NewReader(""), &out, &errOut)
		if code != ExitUsage {
			t.Fatalf("expected ExitUsage for empty input, got %d", code)
		}
	})

	// 3. Unrecognized input json
	t.Run("unrecognized_json", func(t *testing.T) {
		var out, errOut bytes.Buffer
		args := []string{"-input", "-", "-storage-dir", tempStorageDir}
		code := run(ctx, args, strings.NewReader(`{"unexpected": true}`), &out, &errOut)
		if code != ExitUsage {
			t.Fatalf("expected ExitUsage for unrecognized json, got %d", code)
		}
	})
}

func TestCLI_InteractiveEditValidationErrors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
		{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "Task", Category: timesheet.CategoryDevelopment},
	})

	// Sequence of bad inputs:
	// 1. d without args
	// 2. d 999 (out of range)
	// 3. e without args
	// 4. e 999 (out of range)
	// 5. e 1 with bad times (keeps original)
	// 6. a with bad start time
	// 7. a with bad end time
	// 8. unknown action
	// 9. s then y (save)
	var inputBuilder strings.Builder
	inputBuilder.WriteString("d\n")
	inputBuilder.WriteString("d 999\n")
	inputBuilder.WriteString("e\n")
	inputBuilder.WriteString("e 999\n")
	inputBuilder.WriteString("e 1\n")
	inputBuilder.WriteString("badtime\n") // invalid start
	inputBuilder.WriteString("badtime\n") // invalid end
	inputBuilder.WriteString("\n")        // keep category
	inputBuilder.WriteString("\n")        // keep title
	inputBuilder.WriteString("a\n")
	inputBuilder.WriteString("badstart\n") // invalid start time for add
	inputBuilder.WriteString("a\n")
	inputBuilder.WriteString("14:00\n")
	inputBuilder.WriteString("badend\n") // invalid end time for add
	inputBuilder.WriteString("unknown_cmd\n")
	inputBuilder.WriteString("s\n")
	inputBuilder.WriteString("y\n")

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-storage-dir", tempDir,
		"-sink", "none",
	}

	code := run(ctx, args, strings.NewReader(inputBuilder.String()), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
}

func TestCLI_DispatchSinksAll(t *testing.T) {
	ctx := context.Background()
	tempStorageDir := t.TempDir()
	tempVaultDir := t.TempDir()
	dailyDir := filepath.Join(tempVaultDir, "02 - Daily & Logs")
	if err := os.MkdirAll(dailyDir, 0750); err != nil {
		t.Fatalf("creating daily dir: %v", err)
	}
	dailyNotePath := filepath.Join(dailyDir, "2026-10-08.md")
	if err := os.WriteFile(dailyNotePath, []byte("# Daily log 2026-10-08\n"), 0600); err != nil {
		t.Fatalf("writing note: %v", err)
	}

	t.Setenv("OBSIDIAN_VAULT_DIR", tempVaultDir)

	inputFile := writeTempBlocksJSON(t, []timesheet.TimeBlock{
		{ID: "b-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "All Sink Task", Category: timesheet.CategoryDevelopment},
	})

	var out, errOut bytes.Buffer
	args := []string{
		"-input", inputFile,
		"-date", "2026-10-08",
		"-non-interactive",
		"-storage-dir", tempStorageDir,
		"-sink", "all",
	}

	code := run(ctx, args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "All Sink Task") {
		t.Errorf("expected console sink output in stdout, got: %s", out.String())
	}
}

// Subprocess assertion testing per go-rules.md
func TestCLI_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_CLI_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "nil_ctx":
			_ = run(nil, []string{}, strings.NewReader(""), os.Stdout, os.Stderr)
		case "nil_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), nil, os.Stderr)
		case "nil_err_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), os.Stdout, nil)
		}
		return
	}

	testCases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"nil_ctx", "nil_ctx", AssertReviewContextNil},
		{"nil_out", "nil_out", AssertReviewWritersNil},
		{"nil_err_out", "nil_err_out", AssertReviewWritersNil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestCLI_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_CLI_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected subprocess to crash with panic, but it succeeded: %s", string(out))
			}
			if !strings.Contains(string(out), tc.expected) {
				t.Fatalf("expected panic output to contain %q, got: %s", tc.expected, string(out))
			}
		})
	}
}
