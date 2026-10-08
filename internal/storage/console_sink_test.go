package storage

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestConsoleSink_Export(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf)

	if sink.Name() != "console" {
		t.Errorf("expected name 'console', got %s", sink.Name())
	}

	ctx := context.Background()
	record := TimesheetRecord{
		Date:       "2026-10-08",
		UpdatedAt:  time.Now().UTC(),
		Version:    1,
		TotalHours: 4.5,
		Blocks: []timesheet.TimeBlock{
			{
				StartTime:   "09:00",
				EndTime:     "10:00",
				Source:      "calendar",
				Category:    timesheet.CategoryMeeting,
				Title:       "Team Sync",
				Description: "Weekly team checkin",
			},
			{
				StartTime:   "10:00",
				EndTime:     "13:30",
				Source:      "git",
				Category:    timesheet.CategoryDevelopment,
				Title:       "Feature Pipeline",
				Description: "feat: add storage tiering",
			},
			{
				StartTime: "14:00",
				EndTime:   "15:00",
				Source:    "git",
				Category:  timesheet.CategoryMaintenance,
				Title:     "Bug fix",
			},
		},
	}

	if err := sink.Export(ctx, record); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Timesheet for 2026-10-08 (Total: 4.50h, Version: 1)") {
		t.Errorf("header missing in console output: %s", output)
	}
	if !strings.Contains(output, "Team Sync") {
		t.Errorf("meeting block missing: %s", output)
	}
	if !strings.Contains(output, "Feature Pipeline") {
		t.Errorf("git block missing: %s", output)
	}
}

func TestConsoleSink_ContextCancelled(t *testing.T) {
	var buf bytes.Buffer
	sink := NewConsoleSink(&buf)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sink.Export(ctx, TimesheetRecord{Date: "2026-10-08"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestConsoleSink_DefaultStdout(t *testing.T) {
	sink := NewConsoleSink(nil)
	if sink.writer == nil {
		t.Errorf("expected writer to default to os.Stdout")
	}
}
