package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestObsidianSink_Resolution(t *testing.T) {
	tempVault := t.TempDir()

	// 1. Explicit vault path
	sink, err := NewObsidianSink(tempVault)
	if err != nil {
		t.Fatalf("unexpected error with explicit path: %v", err)
	}
	if sink.VaultPath() != tempVault {
		t.Errorf("expected vault path %s, got %s", tempVault, sink.VaultPath())
	}
	if sink.Name() != "obsidian" {
		t.Errorf("expected name 'obsidian', got %s", sink.Name())
	}

	// 2. TIMESHEET_VAULT_PATH environment variable
	t.Setenv("TIMESHEET_VAULT_PATH", tempVault)
	sinkEnv, err := NewObsidianSink("")
	if err != nil {
		t.Fatalf("unexpected error with TIMESHEET_VAULT_PATH: %v", err)
	}
	if sinkEnv.VaultPath() != tempVault {
		t.Errorf("expected vault path %s, got %s", tempVault, sinkEnv.VaultPath())
	}

	// 3. AI_DATABASE_PATH environment variable
	t.Setenv("TIMESHEET_VAULT_PATH", "")
	t.Setenv("AI_DATABASE_PATH", tempVault)
	sinkDB, err := NewObsidianSink("")
	if err != nil {
		t.Fatalf("unexpected error with AI_DATABASE_PATH: %v", err)
	}
	if sinkDB.VaultPath() != tempVault {
		t.Errorf("expected vault path %s, got %s", tempVault, sinkDB.VaultPath())
	}

	// 4. Unconfigured returns ErrSinkNotConfigured
	t.Setenv("TIMESHEET_VAULT_PATH", "")
	t.Setenv("AI_DATABASE_PATH", "")
	_, err = NewObsidianSink("")
	if !errors.Is(err, ErrSinkNotConfigured) {
		t.Errorf("expected ErrSinkNotConfigured, got: %v", err)
	}

	// 5. Non-existent directory returns error
	_, err = NewObsidianSink("/non/existent/path/for/vault")
	if err == nil {
		t.Errorf("expected error for non-existent vault dir, got nil")
	}
}

func TestObsidianSink_ExportNewDailyNote(t *testing.T) {
	// Pin the shared template's exact schema/order: the journal collector reads
	// `date`, and the managed export seeds this same block on note creation.
	wantFrontmatter := "---\ntype: log\ndate: 2026-10-08\nlog_type: daily\ntags:\n  - daily-log\n  - timesheet\n---\n"
	if got := DailyNoteFrontmatter("2026-10-08"); got != wantFrontmatter {
		t.Fatalf("shared frontmatter template drifted:\n%q\nwant:\n%q", got, wantFrontmatter)
	}

	tempVault := t.TempDir()
	sink, err := NewObsidianSink(tempVault)
	if err != nil {
		t.Fatalf("unexpected error creating sink: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"
	record := TimesheetRecord{
		Date:       date,
		UpdatedAt:  time.Date(2026, 10, 8, 17, 30, 0, 0, time.UTC),
		Version:    1,
		TotalHours: 3.5,
		Blocks: []timesheet.TimeBlock{
			{
				StartTime:   "09:00",
				EndTime:     "10:00",
				Source:      "calendar",
				Category:    timesheet.CategoryMeeting,
				Title:       "Standup & Planning",
				Description: "Team sync",
			},
			{
				StartTime:   "10:00",
				EndTime:     "12:30",
				Source:      "git",
				Category:    timesheet.CategoryDevelopment,
				Title:       "Core Feature Dev",
				Description: "feat: storage engine",
			},
		},
	}

	if err := sink.Export(ctx, record); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	notePath := filepath.Join(tempVault, "02 - Daily & Logs", date+".md")
	contentBytes, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatalf("failed reading exported note: %v", err)
	}

	content := string(contentBytes)
	// The sink and the managed export share ONE frontmatter template.
	if !strings.HasPrefix(content, DailyNoteFrontmatter(date)) {
		t.Errorf("new note must begin with the shared frontmatter template: %s", content)
	}
	if !strings.Contains(content, "# Daily work log: 2026-10-08") {
		t.Errorf("missing header in new note: %s", content)
	}
	if !strings.Contains(content, "## Clustered timesheet") {
		t.Errorf("missing timesheet section: %s", content)
	}
	if !strings.Contains(content, "Standup & Planning") {
		t.Errorf("missing block title: %s", content)
	}
	if !strings.Contains(content, "- **Total hours**: 3.50h") {
		t.Errorf("missing total hours: %s", content)
	}
}

func TestObsidianSink_ExportAppendAndReplace(t *testing.T) {
	tempVault := t.TempDir()
	sink, err := NewObsidianSink(tempVault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"
	dailyDir := filepath.Join(tempVault, "02 - Daily & Logs")
	_ = os.MkdirAll(dailyDir, 0755)

	initialNote := `---
type: log
date: 2026-10-08
---

# Daily work log: 2026-10-08

## Work journal
- 09:00: Started morning triage.
`
	notePath := filepath.Join(dailyDir, date+".md")
	if err := os.WriteFile(notePath, []byte(initialNote), 0644); err != nil {
		t.Fatalf("failed writing initial note: %v", err)
	}

	record1 := TimesheetRecord{
		Date:       date,
		UpdatedAt:  time.Now().UTC(),
		Version:    1,
		TotalHours: 1.0,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Source: "calendar", Category: "meeting", Title: "V1 Block"},
		},
	}

	// 1. Append timesheet to existing note
	if err := sink.Export(ctx, record1); err != nil {
		t.Fatalf("Export 1 failed: %v", err)
	}

	after1, _ := os.ReadFile(notePath)
	if !strings.Contains(string(after1), "09:00: Started morning triage.") {
		t.Errorf("existing content was lost: %s", string(after1))
	}
	if !strings.Contains(string(after1), "V1 Block") {
		t.Errorf("timesheet section not appended: %s", string(after1))
	}

	// 2. Replace existing timesheet section idempotently with V2
	record2 := TimesheetRecord{
		Date:       date,
		UpdatedAt:  time.Now().UTC(),
		Version:    2,
		TotalHours: 2.0,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "11:00", Source: "calendar", Category: "meeting", Title: "V2 Block Updated"},
		},
	}

	if err := sink.Export(ctx, record2); err != nil {
		t.Fatalf("Export 2 failed: %v", err)
	}

	after2, _ := os.ReadFile(notePath)
	if strings.Contains(string(after2), "V1 Block") {
		t.Errorf("old block V1 Block was not replaced: %s", string(after2))
	}
	if !strings.Contains(string(after2), "V2 Block Updated") {
		t.Errorf("new block V2 Block Updated not found: %s", string(after2))
	}
	if strings.Count(string(after2), "## Clustered timesheet") != 1 {
		t.Errorf("duplicate timesheet section found: %s", string(after2))
	}
}

func TestObsidianSink_InvalidDateAndContext(t *testing.T) {
	tempVault := t.TempDir()
	sink, err := NewObsidianSink(tempVault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	err = sink.Export(ctx, TimesheetRecord{Date: "not-a-date"})
	if !errors.Is(err, ErrInvalidDate) {
		t.Errorf("expected ErrInvalidDate, got: %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	err = sink.Export(cancelledCtx, TimesheetRecord{Date: "2026-10-08"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestObsidianSink_ExportSectionWithTrailingHeading(t *testing.T) {
	tempVault := t.TempDir()
	sink, err := NewObsidianSink(tempVault)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	date := "2026-10-08"
	dailyDir := filepath.Join(tempVault, "02 - Daily & Logs")
	_ = os.MkdirAll(dailyDir, 0755)

	initialNote := `# Daily log

## Clustered timesheet
- old timesheet content

## Evening recap
- Wrapped up day.
`
	notePath := filepath.Join(dailyDir, date+".md")
	_ = os.WriteFile(notePath, []byte(initialNote), 0644)

	record := TimesheetRecord{
		Date:       date,
		UpdatedAt:  time.Now().UTC(),
		Version:    1,
		TotalHours: 1.0,
		Blocks: []timesheet.TimeBlock{
			{StartTime: "09:00", EndTime: "10:00", Title: "New Clustered Work"},
		},
	}

	if err := sink.Export(ctx, record); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	contentBytes, _ := os.ReadFile(notePath)
	content := string(contentBytes)
	if !strings.Contains(content, "New Clustered Work") {
		t.Errorf("expected new content: %s", content)
	}
	if !strings.Contains(content, "## Evening recap") {
		t.Errorf("expected trailing heading to be preserved: %s", content)
	}
	if !strings.Contains(content, "Wrapped up day.") {
		t.Errorf("expected trailing body to be preserved: %s", content)
	}
}

func TestObsidianSink_Export_TargetIsDirectory(t *testing.T) {
	tempVault := t.TempDir()
	sink, _ := NewObsidianSink(tempVault)
	date := "2026-10-08"
	dailyDir := filepath.Join(tempVault, "02 - Daily & Logs")
	_ = os.MkdirAll(filepath.Join(dailyDir, date+".md"), 0755)

	err := sink.Export(context.Background(), TimesheetRecord{Date: date})
	if err == nil {
		t.Errorf("expected error when target note is a directory, got nil")
	}
}

func TestObsidianSink_FormatMarkdownTable_PipesAndNewlines(t *testing.T) {
	record := TimesheetRecord{
		Date: "2026-10-08",
		Blocks: []timesheet.TimeBlock{
			{
				StartTime:   "09:00",
				EndTime:     "10:00",
				Source:      "git",
				Category:    "development",
				Title:       "Title with | pipe",
				Description: "Desc with | pipe\nand newline",
			},
		},
	}
	formatted := FormatMarkdownTable(record)
	if !strings.Contains(formatted, "Title with \\| pipe") {
		t.Errorf("expected pipe to be escaped: %s", formatted)
	}
	if strings.Contains(formatted, "\nand newline") {
		t.Errorf("expected newline in description to be replaced: %s", formatted)
	}
}
