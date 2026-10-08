package timesheet_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func parseTestTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCluster_EmptyEntries(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	blocks, err := timesheet.ClusterEntries(ctx, nil, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 0 {
		t.Fatalf("expected empty slice and nil error, got %v, %v", blocks, err)
	}
}

func TestCluster_CalendarOnly(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "cal-1", Source: "calendar", Timestamp: "2026-10-08T09:00:00Z", EndTime: "2026-10-08T10:00:00Z", Title: "Daily Standup"},
		{ID: "cal-2", Source: "calendar", Timestamp: "2026-10-08T14:00:00Z", EndTime: "2026-10-08T15:00:00Z", Title: "Sprint Planning"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "09:00" || blocks[0].EndTime != "10:00" || blocks[0].HasCollision {
		t.Errorf("unexpected block 0: %+v", blocks[0])
	}
	if blocks[1].StartTime != "14:00" || blocks[1].EndTime != "15:00" || blocks[1].HasCollision {
		t.Errorf("unexpected block 1: %+v", blocks[1])
	}
}

func TestCluster_ContinuousLunchCommits(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T11:50:00Z", Title: "feat: init database"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T12:15:00Z", Title: "feat: add migrations"},
		{ID: "git-3", Source: "git", Timestamp: "2026-10-08T12:45:00Z", Title: "feat: add models"},
		{ID: "git-4", Source: "git", Timestamp: "2026-10-08T13:10:00Z", Title: "feat: implement queries"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "11:45" || blocks[0].EndTime != "13:15" || len(blocks[0].Entries) != 4 {
		t.Errorf("expected 11:45-13:15 with 4 entries, got %+v", blocks[0])
	}
}

func TestCluster_NaturalLunchGap(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T11:30:00Z", Title: "feat: morning work"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T13:15:00Z", Title: "feat: afternoon work"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "11:30" || blocks[0].EndTime != "12:00" {
		t.Errorf("expected 11:30-12:00, got %s-%s", blocks[0].StartTime, blocks[0].EndTime)
	}
	if blocks[1].StartTime != "13:15" || blocks[1].EndTime != "13:45" {
		t.Errorf("expected 13:15-13:45, got %s-%s", blocks[1].StartTime, blocks[1].EndTime)
	}
}

func TestCluster_OffHoursActivity(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T07:30:00Z", Title: "feat: early bird feature"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T07:45:00Z", Title: "feat: early bird tests"},
		{ID: "git-3", Source: "git", Timestamp: "2026-10-08T21:00:00Z", Title: "feat: night owl coding"},
		{ID: "git-4", Source: "git", Timestamp: "2026-10-08T21:30:00Z", Title: "feat: night owl finish"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "07:30" || blocks[0].EndTime != "08:00" {
		t.Errorf("expected 07:30-08:00, got %s-%s", blocks[0].StartTime, blocks[0].EndTime)
	}
	if blocks[1].StartTime != "21:00" || blocks[1].EndTime != "21:30" {
		t.Errorf("expected 21:00-21:30, got %s-%s", blocks[1].StartTime, blocks[1].EndTime)
	}
}

func TestCluster_IsolatedMicroCommit(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T14:10:00Z", Title: "feat: isolated commit"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "14:00" || blocks[0].EndTime != "14:30" || blocks[0].Category != timesheet.CategoryDevelopment {
		t.Errorf("expected 14:00-14:30 dev block, got %+v", blocks[0])
	}
}

func TestCluster_AdjacentClumping(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:05:00Z", Title: "feat: init work"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:30:00Z", Title: "feat: continue"},
		{ID: "git-3", Source: "git", Timestamp: "2026-10-08T11:00:00Z", Title: "feat: finish"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 merged block, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "10:00" || blocks[0].EndTime != "11:00" {
		t.Errorf("expected 10:00-11:00, got %s-%s", blocks[0].StartTime, blocks[0].EndTime)
	}
}

func TestCluster_CrossCategoryNoMerge(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: add endpoint"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:10:00Z", Title: "fix: nil pointer"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].Category != timesheet.CategoryDevelopment || blocks[1].Category != timesheet.CategoryMaintenance {
		t.Errorf("expected dev and maint blocks, got %s and %s", blocks[0].Category, blocks[1].Category)
	}
}

func TestCluster_CalendarCollisionFlag(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "cal-1", Source: "calendar", Timestamp: "2026-10-08T10:00:00Z", EndTime: "2026-10-08T11:00:00Z", Title: "Team Sync"},
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:20:00Z", Title: "feat: coding during meeting"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	var calBlock, gitBlock timesheet.TimeBlock
	for _, b := range blocks {
		if b.Source == "calendar" {
			calBlock = b
		} else {
			gitBlock = b
		}
	}
	if calBlock.HasCollision || !gitBlock.HasCollision {
		t.Errorf("expected git block collision true and calendar block collision false, got cal: %v, git: %v", calBlock.HasCollision, gitBlock.HasCollision)
	}
}

func TestCluster_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: commit"},
	}
	_, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
}

func TestCluster_Assertions(t *testing.T) {
	targetDate := parseTestTime("2026-10-08T00:00:00Z")

	t.Run("nil context panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r != timesheet.AssertTimesheetContextNil {
				t.Fatalf("expected %s, got %v", timesheet.AssertTimesheetContextNil, r)
			}
		}()
		timesheet.ClusterEntries(nil, []timesheet.SimpleEntry{}, targetDate, timesheet.ClusterOptions{})
	})

	t.Run("zero target date panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r != timesheet.AssertTimesheetDateZero {
				t.Fatalf("expected %s, got %v", timesheet.AssertTimesheetDateZero, r)
			}
		}()
		timesheet.ClusterEntries(context.Background(), []timesheet.SimpleEntry{}, time.Time{}, timesheet.ClusterOptions{})
	})

	t.Run("excessive entries panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r != timesheet.AssertTimesheetExcessData {
				t.Fatalf("expected %s, got %v", timesheet.AssertTimesheetExcessData, r)
			}
		}()
		excessive := make([]timesheet.SimpleEntry, timesheet.MaxEntriesPerDayCount+1)
		for i := range excessive {
			excessive[i] = timesheet.SimpleEntry{
				ID:        fmt.Sprintf("e-%d", i),
				Source:    "git",
				Timestamp: "2026-10-08T10:00:00Z",
				Title:     "feat: test",
			}
		}
		timesheet.ClusterEntries(context.Background(), excessive, targetDate, timesheet.ClusterOptions{})
	})
}

func TestClassifyEntry(t *testing.T) {
	tests := []struct {
		name     string
		entry    timesheet.SimpleEntry
		expected timesheet.SemanticCategory
	}{
		{"calendar", timesheet.SimpleEntry{Source: "calendar", Title: "feat: work"}, timesheet.CategoryMeeting},
		{"development", timesheet.SimpleEntry{Source: "git", Title: "feat: client"}, timesheet.CategoryDevelopment},
		{"maintenance", timesheet.SimpleEntry{Source: "git", Title: "chore: bump", Description: "fix leak"}, timesheet.CategoryMaintenance},
		{"research_docs", timesheet.SimpleEntry{Source: "git", Title: "docs: write adr"}, timesheet.CategoryResearchDocs},
		{"other", timesheet.SimpleEntry{Source: "git", Title: "misc update"}, timesheet.CategoryOther},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := timesheet.ClassifyEntry(tc.entry); got != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}
