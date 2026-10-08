package timesheet_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
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

func TestCluster_CompatibleCategoryMerge(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: add endpoint"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:10:00Z", Title: "fix: nil pointer"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 merged block for compatible dev+maint activities, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].Category != timesheet.CategoryDevelopment {
		t.Errorf("expected CategoryDevelopment for merged block, got %s", blocks[0].Category)
	}
	if len(blocks[0].Entries) != 2 {
		t.Errorf("expected 2 entries in merged block, got %d", len(blocks[0].Entries))
	}
}

func TestCluster_CrossCategoryIncompatibleNoMerge(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: add endpoint"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:10:00Z", Title: "docs: write architecture adr"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks for incompatible categories, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].Category != timesheet.CategoryDevelopment || blocks[1].Category != timesheet.CategoryResearchDocs {
		t.Errorf("expected dev and research_docs blocks, got %s and %s", blocks[0].Category, blocks[1].Category)
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

func TestCluster_GitCollisionWithAdjacentGit(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	// Distinct categories that do not merge, but overlap in time
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: add api"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:15:00Z", Title: "docs: document api architecture"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %v, err: %v", len(blocks), err)
	}
	if !blocks[0].HasCollision || !blocks[1].HasCollision {
		t.Errorf("expected both overlapping git blocks to flag collision, got block0: %v, block1: %v", blocks[0].HasCollision, blocks[1].HasCollision)
	}
}

func TestCluster_PRNumberAggregation(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{
			ID:        "git-1",
			Source:    "git",
			Timestamp: "2026-10-08T10:00:00Z",
			Title:     "feat: client integration",
			Metadata:  &timesheet.EntryMetadata{PRNumber: 1174},
		},
		{
			ID:        "git-2",
			Source:    "git",
			Timestamp: "2026-10-08T10:15:00Z",
			Title:     "feat: client auth flow",
			Metadata:  &timesheet.EntryMetadata{PRNumber: 1182},
		},
		{
			ID:        "git-3",
			Source:    "git",
			Timestamp: "2026-10-08T10:30:00Z",
			Title:     "feat: client tests",
			Metadata:  &timesheet.EntryMetadata{PRNumber: 1174}, // duplicate PR
		},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 merged block, got %v, err: %v", len(blocks), err)
	}
	expectedPRLine := "PRs: #1182, #1174"
	if !strings.Contains(blocks[0].Description, expectedPRLine) {
		t.Errorf("expected block description to contain '%s', got:\n%s", expectedPRLine, blocks[0].Description)
	}
}

func TestCluster_IDIncludesDate(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T14:10:00Z", Title: "feat: isolated commit"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %v, err: %v", len(blocks), err)
	}
	if !strings.HasPrefix(blocks[0].ID, "git-2026-10-08-") {
		t.Errorf("expected block ID to include target date prefix 'git-2026-10-08-', got %s", blocks[0].ID)
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

func TestCluster_CapacityBounds(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")

	makeEntries := func(n int) []timesheet.SimpleEntry {
		entries := make([]timesheet.SimpleEntry, n)
		for i := range entries {
			entries[i] = timesheet.SimpleEntry{
				ID:        fmt.Sprintf("e-%d", i),
				Source:    "git",
				Timestamp: "2026-10-08T10:00:00Z",
				Title:     "feat: test",
			}
		}
		return entries
	}

	t.Run("below bound 199 entries succeeds", func(t *testing.T) {
		_, err := timesheet.ClusterEntries(ctx, makeEntries(199), targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if err != nil {
			t.Fatalf("expected nil error for 199 entries, got: %v", err)
		}
	})

	t.Run("at bound 200 entries succeeds", func(t *testing.T) {
		_, err := timesheet.ClusterEntries(ctx, makeEntries(200), targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if err != nil {
			t.Fatalf("expected nil error for 200 entries, got: %v", err)
		}
	})

	t.Run("above bound 201 entries returns ErrExcessiveEntries", func(t *testing.T) {
		_, err := timesheet.ClusterEntries(ctx, makeEntries(201), targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if !errors.Is(err, timesheet.ErrExcessiveEntries) {
			t.Fatalf("expected ErrExcessiveEntries for 201 entries, got: %v", err)
		}
	})
}

func TestCluster_ExcessiveBlocks(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	// Generate 51 non-overlapping calendar entries
	entries := make([]timesheet.SimpleEntry, timesheet.MaxTimeBlocksCount+1)
	for i := range entries {
		hour := i / 60
		minute := i % 60
		entries[i] = timesheet.SimpleEntry{
			ID:        fmt.Sprintf("cal-%d", i),
			Source:    "calendar",
			Timestamp: fmt.Sprintf("2026-10-08T%02d:%02d:00Z", hour, minute),
			EndTime:   fmt.Sprintf("2026-10-08T%02d:%02d:30Z", hour, minute),
			Title:     fmt.Sprintf("Meeting %d", i),
		}
	}
	_, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if !errors.Is(err, timesheet.ErrExcessiveBlocks) {
		t.Fatalf("expected ErrExcessiveBlocks, got: %v", err)
	}
}

func TestCluster_InvalidTimestamps(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")

	t.Run("invalid start timestamp", func(t *testing.T) {
		entries := []timesheet.SimpleEntry{
			{ID: "git-1", Source: "git", Timestamp: "invalid-time", Title: "feat: test"},
		}
		_, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if !errors.Is(err, timesheet.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got: %v", err)
		}
	})

	t.Run("invalid end timestamp", func(t *testing.T) {
		entries := []timesheet.SimpleEntry{
			{ID: "cal-1", Source: "calendar", Timestamp: "2026-10-08T10:00:00Z", EndTime: "invalid-end", Title: "meeting"},
		}
		_, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if !errors.Is(err, timesheet.ErrInvalidTimestamp) {
			t.Fatalf("expected ErrInvalidTimestamp, got: %v", err)
		}
	})
}

func TestAssertion_Subprocess(t *testing.T) {
	if os.Getenv("BE_FATAL_ASSERTION_CONTEXT_NIL") == "1" {
		targetDate := parseTestTime("2026-10-08T00:00:00Z")
		/* #nosec G104 */
		_, _ = timesheet.ClusterEntries(nil, []timesheet.SimpleEntry{}, targetDate, timesheet.ClusterOptions{})
		return
	}
	if os.Getenv("BE_FATAL_ASSERTION_DATE_ZERO") == "1" {
		/* #nosec G104 */
		_, _ = timesheet.ClusterEntries(context.Background(), []timesheet.SimpleEntry{}, time.Time{}, timesheet.ClusterOptions{})
		return
	}

	tests := []struct {
		name    string
		envKey  string
		wantTag string
	}{
		{"ContextNil", "BE_FATAL_ASSERTION_CONTEXT_NIL=1", timesheet.AssertTimesheetContextNil},
		{"DateZero", "BE_FATAL_ASSERTION_DATE_ZERO=1", timesheet.AssertTimesheetDateZero},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestAssertion_Subprocess")
			cmd.Env = append(os.Environ(), tt.envKey)
			out, err := cmd.CombinedOutput()

			if err == nil {
				t.Fatalf("expected fatal assertion to exit with nonzero, but succeeded. Output: %s", string(out))
			}
			if !strings.Contains(string(out), tt.wantTag) {
				t.Errorf("expected output to contain invariant identifier %s, got: %s", tt.wantTag, string(out))
			}
		})
	}
}

func TestClassifyEntry(t *testing.T) {
	tests := []struct {
		name     string
		entry    timesheet.SimpleEntry
		expected timesheet.SemanticCategory
	}{
		{"calendar meeting", timesheet.SimpleEntry{Source: "calendar", Title: "feat: work"}, timesheet.CategoryMeeting},
		{"conventional test prefix over dev word", timesheet.SimpleEntry{Source: "git", Title: "test: add tests for client"}, timesheet.CategoryMaintenance},
		{"conventional fix prefix", timesheet.SimpleEntry{Source: "git", Title: "fix(core): resolve race"}, timesheet.CategoryMaintenance},
		{"conventional feat prefix", timesheet.SimpleEntry{Source: "git", Title: "feat(api): add endpoint"}, timesheet.CategoryDevelopment},
		{"conventional docs prefix", timesheet.SimpleEntry{Source: "git", Title: "docs: update adr"}, timesheet.CategoryResearchDocs},
		{"body development", timesheet.SimpleEntry{Source: "git", Title: "implement auth flow"}, timesheet.CategoryDevelopment},
		{"body maintenance", timesheet.SimpleEntry{Source: "git", Title: "routine bugfix"}, timesheet.CategoryMaintenance},
		{"body docs", timesheet.SimpleEntry{Source: "git", Title: "research caching spike"}, timesheet.CategoryResearchDocs},
		{"other fallback", timesheet.SimpleEntry{Source: "git", Title: "miscellaneous cleanup"}, timesheet.CategoryOther},
		{"bounded text check", timesheet.SimpleEntry{Source: "git", Title: "feat: " + strings.Repeat("a", 5000)}, timesheet.CategoryDevelopment},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := timesheet.ClassifyEntry(tc.entry); got != tc.expected {
				t.Errorf("expected %s, got %s", tc.expected, got)
			}
		})
	}
}

func TestCategoryCompatibility(t *testing.T) {
	if !timesheet.AreCategoriesCompatible(timesheet.CategoryDevelopment, timesheet.CategoryMaintenance) {
		t.Errorf("expected dev and maint to be compatible")
	}
	if !timesheet.AreCategoriesCompatible(timesheet.CategoryMaintenance, timesheet.CategoryDevelopment) {
		t.Errorf("expected maint and dev to be compatible")
	}
	if !timesheet.AreCategoriesCompatible(timesheet.CategoryDevelopment, timesheet.CategoryDevelopment) {
		t.Errorf("expected dev and dev to be compatible")
	}
	if timesheet.AreCategoriesCompatible(timesheet.CategoryDevelopment, timesheet.CategoryResearchDocs) {
		t.Errorf("expected dev and research_docs to be incompatible")
	}
	if timesheet.AreCategoriesCompatible(timesheet.CategoryDevelopment, timesheet.CategoryMeeting) {
		t.Errorf("expected dev and meeting to be incompatible")
	}
}

func TestClusterOptions_CoreAndLunchHelpers(t *testing.T) {
	opts := timesheet.ClusterOptions{
		CoreHoursStart: "09:00",
		CoreHoursEnd:   "18:00",
		LunchStart:     "12:00",
		LunchEnd:       "13:00",
	}

	if !opts.IsCoreHours("09:00") || !opts.IsCoreHours("14:30") || !opts.IsCoreHours("18:00") {
		t.Errorf("expected 09:00, 14:30, 18:00 to be core hours")
	}
	if opts.IsCoreHours("08:59") || opts.IsCoreHours("18:01") {
		t.Errorf("expected 08:59 and 18:01 to not be core hours")
	}
	if !opts.IsLunchHours("12:00") || !opts.IsLunchHours("12:45") {
		t.Errorf("expected 12:00 and 12:45 to be lunch hours")
	}
	if opts.IsLunchHours("11:59") || opts.IsLunchHours("13:00") {
		t.Errorf("expected 11:59 and 13:00 to not be lunch hours")
	}
}

func TestSnapToGrid_NonGridMultiple(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T14:10:00Z", Title: "feat: test"},
	}
	// minBlockMinutes = 35 (not a multiple of 15), roundGridMinutes = 15
	opts := timesheet.ClusterOptions{
		Location:         time.UTC,
		MinBlockMinutes:  35,
		RoundGridMinutes: 15,
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, opts)
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %v, err: %v", len(blocks), err)
	}
	// 14:10 snapped start is 14:00. 14:00 + 35m = 14:35, rounded up to 15-min grid is 14:45.
	if blocks[0].StartTime != "14:00" || blocks[0].EndTime != "14:45" {
		t.Errorf("expected 14:00-14:45, got %s-%s", blocks[0].StartTime, blocks[0].EndTime)
	}
}

func TestClusterOptions_EmptyDefaults(t *testing.T) {
	opts := timesheet.ClusterOptions{}
	if !opts.IsCoreHours("10:00") {
		t.Errorf("expected 10:00 to be core hours with empty defaults")
	}
	if !opts.IsLunchHours("12:30") {
		t.Errorf("expected 12:30 to be lunch hours with empty defaults")
	}
}

func TestCluster_TransparentAndDurationMinutes(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{
			ID:        "cal-trans",
			Source:    "calendar",
			Timestamp: "2026-10-08T09:00:00Z",
			EndTime:   "2026-10-08T10:00:00Z",
			Title:     "Transparent event",
			Metadata:  &timesheet.EntryMetadata{Transparent: true},
		},
		{
			ID:        "git-dur",
			Source:    "git",
			Timestamp: "2026-10-08T10:00:00Z",
			Title:     "feat: duration task",
			Metadata:  &timesheet.EntryMetadata{DurationMinutes: 45},
		},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 block (transparent ignored), got %v, err: %v", len(blocks), err)
	}
	if blocks[0].StartTime != "10:00" || blocks[0].EndTime != "10:45" {
		t.Errorf("expected 10:00-10:45, got %s-%s", blocks[0].StartTime, blocks[0].EndTime)
	}
}

func TestCluster_MicroClumpingGapsAndDirections(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")

	t.Run("micro followed by macro merges", func(t *testing.T) {
		entries := []timesheet.SimpleEntry{
			{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: quick fix"},
			{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:40:00Z", Title: "feat: long coding part 1"},
			{ID: "git-3", Source: "git", Timestamp: "2026-10-08T11:20:00Z", Title: "feat: long coding part 2"},
		}
		blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC, MaxCommitGapMinutes: 30})
		if err != nil || len(blocks) != 1 {
			t.Fatalf("expected 1 merged block, got %v, err: %v", len(blocks), err)
		}
	})

	t.Run("macro followed by micro merges", func(t *testing.T) {
		entries := []timesheet.SimpleEntry{
			{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: long coding part 1"},
			{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:40:00Z", Title: "feat: long coding part 2"},
			{ID: "git-3", Source: "git", Timestamp: "2026-10-08T11:20:00Z", Title: "feat: quick follow-up"},
		}
		blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC, MaxCommitGapMinutes: 30})
		if err != nil || len(blocks) != 1 {
			t.Fatalf("expected 1 merged block, got %v, err: %v", len(blocks), err)
		}
	})

	t.Run("micro clusters with gap greater than 60 min do not merge", func(t *testing.T) {
		entries := []timesheet.SimpleEntry{
			{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "feat: morning micro"},
			{ID: "git-2", Source: "git", Timestamp: "2026-10-08T11:30:00Z", Title: "feat: later micro"},
		}
		blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
		if err != nil || len(blocks) != 2 {
			t.Fatalf("expected 2 distinct blocks when gap > 60 min, got %v, err: %v", len(blocks), err)
		}
	})
}

func TestCluster_MaintenanceThenDevelopmentCategoryMerge(t *testing.T) {
	ctx := context.Background()
	targetDate := parseTestTime("2026-10-08T00:00:00Z")
	entries := []timesheet.SimpleEntry{
		{ID: "git-1", Source: "git", Timestamp: "2026-10-08T10:00:00Z", Title: "fix: repair bug"},
		{ID: "git-2", Source: "git", Timestamp: "2026-10-08T10:15:00Z", Title: "feat: implement enhancement"},
	}
	blocks, err := timesheet.ClusterEntries(ctx, entries, targetDate, timesheet.ClusterOptions{Location: time.UTC})
	if err != nil || len(blocks) != 1 {
		t.Fatalf("expected 1 merged block, got %v, err: %v", len(blocks), err)
	}
	if blocks[0].Category != timesheet.CategoryDevelopment {
		t.Errorf("expected CategoryDevelopment when merging fix then feat, got %s", blocks[0].Category)
	}
}
