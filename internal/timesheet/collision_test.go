package timesheet

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCollision_NoOverlap(t *testing.T) {
	ctx := context.Background()
	blocks := []TimeBlock{
		{
			ID:        "cal-1",
			Source:    "calendar",
			Category:  CategoryMeeting,
			StartTime: "09:00",
			EndTime:   "10:00",
			Date:      "2026-10-08",
			Title:     "Daily Standup",
		},
		{
			ID:        "git-1",
			Source:    "git",
			Category:  CategoryDevelopment,
			StartTime: "10:15",
			EndTime:   "11:00",
			Date:      "2026-10-08",
			Title:     "feat(storage): json store",
		},
	}

	cols, err := DetectCollisions(ctx, blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 0 {
		t.Fatalf("expected 0 collisions, got %d", len(cols))
	}
}

func TestCollision_DirectOverlap(t *testing.T) {
	ctx := context.Background()
	blocks := []TimeBlock{
		{
			ID:        "cal-1",
			Source:    "calendar",
			Category:  CategoryMeeting,
			StartTime: "10:00",
			EndTime:   "11:00",
			Date:      "2026-10-08",
			Title:     "Sprint Planning",
		},
		{
			ID:        "git-1",
			Source:    "git",
			Category:  CategoryDevelopment,
			StartTime: "10:15",
			EndTime:   "10:45",
			Date:      "2026-10-08",
			Title:     "3 commits on feat/collector",
		},
	}

	cols, err := DetectCollisions(ctx, blocks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("expected 1 collision, got %d", len(cols))
	}

	col := cols[0]
	if col.BlockID != "git-1" {
		t.Errorf("expected BlockID 'git-1', got %s", col.BlockID)
	}
	if col.MeetingTitle != "Sprint Planning" {
		t.Errorf("expected MeetingTitle 'Sprint Planning', got %s", col.MeetingTitle)
	}
	if col.GitTitle != "3 commits on feat/collector" {
		t.Errorf("expected GitTitle '3 commits on feat/collector', got %s", col.GitTitle)
	}
	if col.StartTime != "10:15" || col.EndTime != "10:45" {
		t.Errorf("expected 10:15-10:45, got %s-%s", col.StartTime, col.EndTime)
	}
}

func TestCollision_ResolveKeepBoth(t *testing.T) {
	meeting := TimeBlock{
		ID:          "cal-1",
		Source:      "calendar",
		Category:    CategoryMeeting,
		StartTime:   "09:00",
		EndTime:     "10:00",
		Date:        "2026-10-08",
		Title:       "Sprint Planning",
		Description: "- Discuss sprint backlog",
		Entries: []SimpleEntry{
			{ID: "entry-cal-1", Source: "calendar", Title: "Sprint Planning"},
		},
		HasCollision: true,
	}
	gitBlock := TimeBlock{
		ID:          "git-1",
		Source:      "git",
		Category:    CategoryDevelopment,
		StartTime:   "09:30",
		EndTime:     "10:00",
		Date:        "2026-10-08",
		Title:       "Collector Refactoring",
		Description: "- Refactor internal/timesheet",
		Entries: []SimpleEntry{
			{ID: "entry-git-1", Source: "git", Title: "Collector Refactoring"},
		},
		HasCollision: true,
	}

	res, err := ResolveCollision(meeting, gitBlock, ResolutionKeepBoth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 joint block, got %d", len(res))
	}

	joint := res[0]
	if joint.Source != "joint" {
		t.Errorf("expected Source 'joint', got %s", joint.Source)
	}
	if joint.StartTime != "09:00" || joint.EndTime != "10:00" {
		t.Errorf("expected 09:00 - 10:00, got %s - %s", joint.StartTime, joint.EndTime)
	}
	if joint.HasCollision {
		t.Errorf("expected HasCollision == false")
	}
	if !strings.Contains(joint.Title, "Sprint Planning") || !strings.Contains(joint.Title, "Collector Refactoring") {
		t.Errorf("expected combined title, got %s", joint.Title)
	}
	if !strings.Contains(joint.Description, "Discuss sprint backlog") || !strings.Contains(joint.Description, "Refactor internal/timesheet") {
		t.Errorf("expected combined descriptions, got %s", joint.Description)
	}
	if len(joint.Entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(joint.Entries))
	}
}

func TestCollision_ResolveSeparate(t *testing.T) {
	meeting := TimeBlock{
		ID:        "cal-1",
		Source:    "calendar",
		Category:  CategoryMeeting,
		StartTime: "10:00",
		EndTime:   "11:00",
		Date:      "2026-10-08",
		Title:     "Sprint Planning",
	}
	gitBlock := TimeBlock{
		ID:        "git-1",
		Source:    "git",
		Category:  CategoryDevelopment,
		StartTime: "10:15",
		EndTime:   "10:45",
		Date:      "2026-10-08",
		Title:     "3 commits on feat/collector",
	}

	res, err := ResolveCollision(meeting, gitBlock, ResolutionSeparate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(res))
	}

	cal := res[0]
	if cal.StartTime != "10:00" || cal.EndTime != "11:00" {
		t.Errorf("meeting should remain 10:00-11:00, got %s-%s", cal.StartTime, cal.EndTime)
	}
	if cal.HasCollision {
		t.Errorf("expected cal.HasCollision == false")
	}

	gitB := res[1]
	if gitB.StartTime != "11:00" || gitB.EndTime != "11:30" {
		t.Errorf("shifted git block should be 11:00-11:30, got %s-%s", gitB.StartTime, gitB.EndTime)
	}
	if gitB.HasCollision {
		t.Errorf("expected gitB.HasCollision == false")
	}
}

func TestCollision_ResolveDiscard(t *testing.T) {
	meeting := TimeBlock{
		ID:        "cal-1",
		Source:    "calendar",
		Category:  CategoryMeeting,
		StartTime: "10:00",
		EndTime:   "11:00",
		Date:      "2026-10-08",
		Title:     "Sprint Planning",
	}
	gitBlock := TimeBlock{
		ID:        "git-1",
		Source:    "git",
		Category:  CategoryDevelopment,
		StartTime: "10:15",
		EndTime:   "10:45",
		Date:      "2026-10-08",
		Title:     "3 commits on feat/collector",
	}

	res, err := ResolveCollision(meeting, gitBlock, ResolutionDiscard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 block (meeting only), got %d", len(res))
	}
	if res[0].ID != "cal-1" || res[0].HasCollision {
		t.Errorf("expected uncollided meeting block, got %+v", res[0])
	}
}

func TestCollision_ResolveAllPolicy(t *testing.T) {
	ctx := context.Background()
	blocks := []TimeBlock{
		{
			ID:        "cal-1",
			Source:    "calendar",
			Category:  CategoryMeeting,
			StartTime: "09:00",
			EndTime:   "10:00",
			Date:      "2026-10-08",
			Title:     "Meeting 1",
		},
		{
			ID:        "git-1",
			Source:    "git",
			Category:  CategoryDevelopment,
			StartTime: "09:15",
			EndTime:   "09:45",
			Date:      "2026-10-08",
			Title:     "Git 1",
		},
		{
			ID:        "cal-2",
			Source:    "calendar",
			Category:  CategoryMeeting,
			StartTime: "11:00",
			EndTime:   "12:00",
			Date:      "2026-10-08",
			Title:     "Meeting 2",
		},
		{
			ID:        "git-2",
			Source:    "git",
			Category:  CategoryDevelopment,
			StartTime: "11:30",
			EndTime:   "12:00",
			Date:      "2026-10-08",
			Title:     "Git 2",
		},
		{
			ID:        "cal-3",
			Source:    "calendar",
			Category:  CategoryMeeting,
			StartTime: "14:00",
			EndTime:   "15:00",
			Date:      "2026-10-08",
			Title:     "Meeting 3",
		},
		{
			ID:        "git-3",
			Source:    "git",
			Category:  CategoryDevelopment,
			StartTime: "14:15",
			EndTime:   "14:45",
			Date:      "2026-10-08",
			Title:     "Git 3",
		},
	}

	// 1. ResolveAll with discard
	resolvedDiscard, err := ResolveAll(ctx, blocks, ResolutionDiscard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolvedDiscard) != 3 {
		t.Fatalf("expected 3 meetings remaining after discard, got %d", len(resolvedDiscard))
	}
	for _, b := range resolvedDiscard {
		if b.Source != "calendar" || b.HasCollision {
			t.Errorf("expected clean calendar block, got %+v", b)
		}
	}

	// 2. ResolveAll with keep_both
	resolvedKeep, err := ResolveAll(ctx, blocks, ResolutionKeepBoth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolvedKeep) != 3 {
		t.Fatalf("expected 3 joint blocks, got %d", len(resolvedKeep))
	}
	for _, b := range resolvedKeep {
		if b.Source != "joint" || b.HasCollision {
			t.Errorf("expected clean joint block, got %+v", b)
		}
	}

	// 3. ResolveAll with separate
	resolvedSeparate, err := ResolveAll(ctx, blocks, ResolutionSeparate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolvedSeparate) != 6 {
		t.Fatalf("expected 6 blocks after separate, got %d", len(resolvedSeparate))
	}
	for _, b := range resolvedSeparate {
		if b.HasCollision {
			t.Errorf("expected block HasCollision == false, got %+v", b)
		}
	}
}

func TestCollision_InvalidResolution(t *testing.T) {
	meeting := TimeBlock{Source: "calendar", Date: "2026-10-08", StartTime: "09:00", EndTime: "10:00"}
	gitBlock := TimeBlock{Source: "git", Date: "2026-10-08", StartTime: "09:15", EndTime: "09:45"}

	_, err := ResolveCollision(meeting, gitBlock, "invalid_choice")
	if err != ErrInvalidResolution {
		t.Fatalf("expected ErrInvalidResolution, got %v", err)
	}

	ctx := context.Background()
	_, err = ResolveAll(ctx, []TimeBlock{meeting, gitBlock}, "invalid_choice")
	if err != ErrInvalidResolution {
		t.Fatalf("expected ErrInvalidResolution, got %v", err)
	}
}

func TestCollision_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := DetectCollisions(ctx, []TimeBlock{})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	_, err = ResolveAll(ctx, []TimeBlock{}, ResolutionKeepBoth)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestCollision_ExcessiveCollisions(t *testing.T) {
	ctx := context.Background()
	var blocks []TimeBlock

	// Generate 30 colliding pairs
	for i := 0; i < 30; i++ {
		startHour := 8 + (i / 4)
		startMin := (i % 4) * 15
		calStart := fmt.Sprintf("%02d:%02d", startHour, startMin)
		calEnd := fmt.Sprintf("%02d:%02d", startHour, startMin+10)
		gitStart := fmt.Sprintf("%02d:%02d", startHour, startMin+2)
		gitEnd := fmt.Sprintf("%02d:%02d", startHour, startMin+8)

		blocks = append(blocks, TimeBlock{
			ID:        fmt.Sprintf("cal-%d", i),
			Source:    "calendar",
			StartTime: calStart,
			EndTime:   calEnd,
			Date:      "2026-10-08",
			Title:     fmt.Sprintf("Meeting %d", i),
		})
		blocks = append(blocks, TimeBlock{
			ID:        fmt.Sprintf("git-%d", i),
			Source:    "git",
			StartTime: gitStart,
			EndTime:   gitEnd,
			Date:      "2026-10-08",
			Title:     fmt.Sprintf("Commit %d", i),
		})
	}

	_, err := DetectCollisions(ctx, blocks)
	if err != ErrExcessiveCollisions {
		t.Fatalf("expected ErrExcessiveCollisions, got %v", err)
	}
}

// Subprocess assertion test per go-rules.md
func TestCollision_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_COLLISION_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "nil_ctx_detect":
			_, _ = DetectCollisions(nil, []TimeBlock{}) // should panic AssertReviewContextNil
		case "nil_ctx_resolve_all":
			_, _ = ResolveAll(nil, []TimeBlock{}, ResolutionKeepBoth) // should panic AssertReviewContextNil
		}
		return
	}

	testCases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"nil_ctx_detect", "nil_ctx_detect", AssertReviewContextNil},
		{"nil_ctx_resolve_all", "nil_ctx_resolve_all", AssertReviewContextNil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestCollision_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_COLLISION_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
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

func TestCollision_EdgeCases(t *testing.T) {
	ctx := context.Background()

	// 1. DetectCollisions with empty slice
	cols, err := DetectCollisions(ctx, []TimeBlock{})
	if err != nil || len(cols) != 0 {
		t.Fatalf("expected empty collisions, got %v, %v", cols, err)
	}

	// 2. DetectCollisions with only calendar or only git
	calOnly := []TimeBlock{{ID: "cal-1", Source: "calendar", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08"}}
	cols, err = DetectCollisions(ctx, calOnly)
	if err != nil || len(cols) != 0 {
		t.Fatalf("expected 0 collisions for calOnly, got %d", len(cols))
	}
	gitOnly := []TimeBlock{{ID: "git-1", Source: "git", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08"}}
	cols, err = DetectCollisions(ctx, gitOnly)
	if err != nil || len(cols) != 0 {
		t.Fatalf("expected 0 collisions for gitOnly, got %d", len(cols))
	}

	// 3. ResolveAll with empty slice
	resolved, err := ResolveAll(ctx, []TimeBlock{}, ResolutionKeepBoth)
	if err != nil || len(resolved) != 0 {
		t.Fatalf("expected empty resolved, got %v, %v", resolved, err)
	}

	// 4. ResolveAll with no collisions
	resolved, err = ResolveAll(ctx, calOnly, ResolutionKeepBoth)
	if err != nil || len(resolved) != 1 {
		t.Fatalf("expected 1 resolved block, got %v, %v", resolved, err)
	}

	// 5. Reverse arguments in ResolveCollision
	meeting := calOnly[0]
	gitBlock := gitOnly[0]
	resReverse, err := ResolveCollision(gitBlock, meeting, ResolutionKeepBoth)
	if err != nil || len(resReverse) != 1 {
		t.Fatalf("expected successful resolution with reversed args, got %v, %v", resReverse, err)
	}

	// 6. Separate with non-colliding additional blocks and multiple meetings
	blocks := []TimeBlock{
		{ID: "cal-1", Source: "calendar", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08", Title: "M1"},
		{ID: "cal-2", Source: "calendar", StartTime: "10:00", EndTime: "10:30", Date: "2026-10-08", Title: "M2"},
		{ID: "git-1", Source: "git", StartTime: "09:15", EndTime: "09:45", Date: "2026-10-08", Title: "G1"},
		{ID: "manual-1", Source: "manual", StartTime: "14:00", EndTime: "15:00", Date: "2026-10-08", Title: "Doc"},
	}
	resMulti, err := ResolveAll(ctx, blocks, ResolutionSeparate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resMulti) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(resMulti))
	}

	// 7. KeepBoth with empty title and description
	emptyMeeting := TimeBlock{Source: "calendar", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-08"}
	emptyGit := TimeBlock{Source: "git", StartTime: "09:15", EndTime: "09:45", Date: "2026-10-08"}
	resEmpty, err := ResolveCollision(emptyMeeting, emptyGit, ResolutionKeepBoth)
	if err != nil || len(resEmpty) != 1 {
		t.Fatalf("expected 1 block for empty titles, got %v, %v", resEmpty, err)
	}
}

