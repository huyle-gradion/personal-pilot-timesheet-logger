package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Author",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test Author",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test Author")
	runGit("config", "user.email", "test@example.com")

	// Create test file and commit 1 referencing PR #42
	file1 := filepath.Join(dir, "file1.txt")
	if err := os.WriteFile(file1, []byte("content 1"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file1.txt")
	runGit("commit", "-m", "feat: implement collector (#42)")

	// Create test file and commit 2 without PR reference
	file2 := filepath.Join(dir, "file2.txt")
	if err := os.WriteFile(file2, []byte("content 2"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file2.txt")
	runGit("commit", "-m", "chore: unparented maintenance")

	return dir
}

func TestLocalGitCollector_FetchDayCommits(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	collector := NewLocalGitCollector(repoDir)

	ctx := context.Background()
	today := time.Now()

	commits, err := collector.FetchDayCommits(ctx, "", today)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}

	// Verify PR discovery
	prGroups := collector.DiscoverPRReferences(commits)
	if len(prGroups[42]) != 1 {
		t.Fatalf("expected 1 commit grouped under PR #42, got %d", len(prGroups[42]))
	}

	// Verify author filter
	filtered, err := collector.FetchDayCommits(ctx, "nonexistent@example.com", today)
	if err != nil {
		t.Fatalf("expected no error for empty match, got: %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("expected 0 commits for non-matching author, got %d", len(filtered))
	}
}

func TestLocalGitCollector_FetchDayActivity(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	collector := NewLocalGitCollector(repoDir)

	ctx := context.Background()
	today := time.Now()

	commits, prs, err := collector.FetchDayActivity(ctx, "", today)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 synthesized PR (#42), got %d", len(prs))
	}
	if prs[0].Number != 42 {
		t.Errorf("expected PR number 42, got %d", prs[0].Number)
	}
}

func TestLocalGitCollector_NotAGitRepository(t *testing.T) {
	emptyDir := t.TempDir()
	collector := NewLocalGitCollector(emptyDir)

	ctx := context.Background()
	_, err := collector.FetchDayCommits(ctx, "", time.Now())
	if err == nil {
		t.Fatal("expected error on non-git directory, got nil")
	}
	if !errors.Is(err, ErrNotAGitRepository) {
		t.Errorf("expected ErrNotAGitRepository, got: %v", err)
	}
}

func TestLocalGitCollector_DiscoverPRReferences(t *testing.T) {
	collector := NewLocalGitCollector(".")
	commits := []GitHubCommit{
		{SHA: "111", Message: "fix: handle rate limit (#42)"},
		{SHA: "222", Message: "refactor: simplify parsing (GH-42)"},
		{SHA: "333", Message: "feat: new feature (pull/99)"},
		{SHA: "444", Message: "chore: update documentation"},
		{SHA: "555", Message: "fix: resolve edge case (PR-101)"},
	}

	groups := collector.DiscoverPRReferences(commits)

	if len(groups[42]) != 2 {
		t.Errorf("expected 2 commits for PR 42, got %d", len(groups[42]))
	}
	if len(groups[99]) != 1 {
		t.Errorf("expected 1 commit for PR 99, got %d", len(groups[99]))
	}
	if len(groups[101]) != 1 {
		t.Errorf("expected 1 commit for PR 101, got %d", len(groups[101]))
	}
	if _, exists := groups[0]; exists {
		t.Errorf("expected no PR 0, got %v", groups[0])
	}
}

func TestExecGit_ProcessGroupTerminationOnTimeout(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	collector := NewLocalGitCollector(repoDir)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	time.Sleep(30 * time.Millisecond)

	_, err := collector.execGit(ctx, "log", "--all")
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}

	var opErr *OperatingError
	if !errors.As(err, &opErr) {
		t.Errorf("expected OperatingError, got: %T (%v)", err, err)
	} else if opErr.Code != "GIT_ERR_LOCAL_EXEC" {
		t.Errorf("expected code GIT_ERR_LOCAL_EXEC, got: %s", opErr.Code)
	}
}

func TestLimitedWriter_CapacityExceeded(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, limit: 10}

	n, err := lw.Write([]byte("12345"))
	if err != nil || n != 5 {
		t.Fatalf("expected 5 bytes written, got n=%d, err=%v", n, err)
	}

	_, err = lw.Write([]byte("67890123"))
	if err == nil {
		t.Fatal("expected error on capacity exceeded, got nil")
	}
	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_CAPACITY_EXCEEDED" {
		t.Fatalf("expected GIT_ERR_CAPACITY_EXCEEDED, got %v", err)
	}
}

func TestSanitizeArgs(t *testing.T) {
	args := []string{"log", "/Users/secret/path/to/repo", "--since=2026-10-08"}
	sanitized := sanitizeArgs(args)
	if strings.Contains(sanitized, "/Users/secret") {
		t.Errorf("expected absolute path to be sanitized, got: %s", sanitized)
	}
	if !strings.Contains(sanitized, ".../repo") {
		t.Errorf("expected base path marker '.../repo', got: %s", sanitized)
	}
}

func TestLocalGitCollector_BranchPRSynthesis(t *testing.T) {
	dir := t.TempDir()

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Branch Dev",
			"GIT_AUTHOR_EMAIL=branch@example.com",
			"GIT_COMMITTER_NAME=Branch Dev",
			"GIT_COMMITTER_EMAIL=branch@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
		}
	}

	runGit("init", "-b", "feat/issue-88")
	runGit("config", "user.name", "Branch Dev")
	runGit("config", "user.email", "branch@example.com")

	f := filepath.Join(dir, "change.txt")
	_ = os.WriteFile(f, []byte("change"), 0644)
	runGit("add", "change.txt")
	runGit("commit", "-m", "implement branch feature without pr tag")

	collector := NewLocalGitCollector(dir)
	commits, prs, err := collector.FetchDayActivity(context.Background(), "", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 synthesized PR from branch name, got %d", len(prs))
	}
	if prs[0].Number != 88 {
		t.Errorf("expected PR number 88 from branch name, got %d", prs[0].Number)
	}
}

func TestExtractPRReferences(t *testing.T) {
	cases := []struct {
		input string
		want  []int
	}{
		{"feat: implement (#123)", []int{123}},
		{"fix: GH-456 and PR-789", []int{456, 789}},
		{"feat/issue-321", []int{321}},
		{"feat/issue/654", []int{654}},
		{"pull/999", []int{999}},
		{"no references", nil},
	}

	for _, tc := range cases {
		got := ExtractPRReferences(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("ExtractPRReferences(%q) = %v; want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ExtractPRReferences(%q)[%d] = %d; want %d", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestLocalGitCollector_Author(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	collector := NewLocalGitCollector(repoDir)

	ctx := context.Background()
	author := collector.Author(ctx)
	if author != "test@example.com" {
		t.Errorf("expected Author 'test@example.com', got %q", author)
	}

	// Empty repo with no git config
	emptyDir := t.TempDir()
	emptyCollector := NewLocalGitCollector(emptyDir)
	emptyAuthor := emptyCollector.Author(ctx)
	if emptyAuthor != "" {
		t.Errorf("expected empty author for non-repo, got %q", emptyAuthor)
	}
}

// GIT-DATE-WINDOW-1: the local day window is anchored to date.Location() and is
// HALF-OPEN [start, end) with end the CALENDRICAL next midnight (NOT start+24h).

// initRepoWithCommits creates a temp repo and one empty commit per instant with
// author and committer dates pinned to that instant. No network is touched.
func initRepoWithCommits(t *testing.T, instants []time.Time) string {
	t.Helper()
	dir := t.TempDir()
	mustGit := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	base := []string{
		"GIT_AUTHOR_NAME=Window Dev",
		"GIT_AUTHOR_EMAIL=window@example.com",
		"GIT_COMMITTER_NAME=Window Dev",
		"GIT_COMMITTER_EMAIL=window@example.com",
	}
	mustGit(base, "init", "-q")
	mustGit(base, "config", "user.email", "window@example.com")
	mustGit(base, "config", "user.name", "Window Dev")
	for i, ts := range instants {
		iso := ts.Format(time.RFC3339)
		mustGit(append(base, "GIT_AUTHOR_DATE="+iso, "GIT_COMMITTER_DATE="+iso),
			"commit", "-q", "--allow-empty", "-m", fmt.Sprintf("c%d %s", i, iso))
	}
	return dir
}

// assertDayWindow seeds the four canonical in-day instants (00:00:00, 06:59:59,
// 07:00:00, 23:59:59 local), the next-day calendrical midnight (must be
// EXCLUDED), plus the caller's extras, then asserts FetchDayCommits returns
// exactly the included set.
func assertDayWindow(t *testing.T, loc *time.Location, day time.Time, extraIncluded, extraExcluded []time.Time) {
	t.Helper()
	y, m, d := day.Date()
	at := func(h, mi, s int) time.Time { return time.Date(y, m, d, h, mi, s, 0, loc) }
	included := []time.Time{at(0, 0, 0), at(6, 59, 59), at(7, 0, 0), at(23, 59, 59)}
	included = append(included, extraIncluded...)
	excluded := append([]time.Time{time.Date(y, m, d+1, 0, 0, 0, 0, loc)}, extraExcluded...)

	all := append(append([]time.Time{}, included...), excluded...)
	dir := initRepoWithCommits(t, all)
	collector := NewLocalGitCollector(dir)
	got, err := collector.FetchDayCommits(context.Background(), "", day)
	if err != nil {
		t.Fatalf("FetchDayCommits: %v", err)
	}
	if len(got) != len(included) {
		t.Fatalf("expected %d in-day commits, got %d: %+v", len(included), len(got), got)
	}
	for _, want := range included {
		found := false
		for _, g := range got {
			if g.Timestamp.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected commit %s to be INCLUDED", want.Format(time.RFC3339))
		}
	}
	for _, bad := range excluded {
		for _, g := range got {
			if g.Timestamp.Equal(bad) {
				t.Errorf("expected commit %s to be EXCLUDED", bad.Format(time.RFC3339))
			}
		}
	}
}

func TestFetchDayCommits_CalendarDayWindow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}

	t.Run("UTC", func(t *testing.T) {
		assertDayWindow(t, time.UTC, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), nil, nil)
	})
	t.Run("Asia/Ho_Chi_Minh +07", func(t *testing.T) {
		assertDayWindow(t, hcm, time.Date(2026, 10, 6, 0, 0, 0, 0, hcm), nil, nil)
	})
	t.Run("America/New_York -04", func(t *testing.T) {
		assertDayWindow(t, ny, time.Date(2026, 10, 6, 0, 0, 0, 0, ny), nil, nil)
	})
	t.Run("America/New_York DST end 2026-11-01 (25h)", func(t *testing.T) {
		// The local day is 25h. 23:30 local exists only because the day is 25h:
		// start+24h would already have passed it, so this proves the window uses
		// the CALENDRICAL next midnight, not start+24h.
		day := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
		assertDayWindow(t, ny, day, []time.Time{time.Date(2026, 11, 1, 23, 30, 0, 0, ny)}, nil)
	})
	t.Run("America/New_York DST start 2026-03-08 (23h)", func(t *testing.T) {
		// The local day is 23h. 00:30 the next local day would fall inside
		// start+24h (01:00 EDT) but is past the CALENDRICAL next midnight
		// (00:00 EDT), so it must be excluded.
		day := time.Date(2026, 3, 8, 0, 0, 0, 0, ny)
		assertDayWindow(t, ny, day, nil, []time.Time{time.Date(2026, 3, 9, 0, 30, 0, 0, ny)})
	})
}

// GIT-DATE-WINDOW-1: collecting day D and day D+1 must partition the commits
// with no gap and no overlap (no duplicate, none dropped).
func TestFetchDayCommits_AdjacentDaysNoGapNoDup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	day1 := time.Date(2026, 10, 6, 0, 0, 0, 0, hcm)
	day2 := time.Date(2026, 10, 7, 0, 0, 0, 0, hcm)
	instants := []time.Time{
		time.Date(2026, 10, 6, 0, 0, 0, 0, hcm),
		time.Date(2026, 10, 6, 12, 0, 0, 0, hcm),
		time.Date(2026, 10, 6, 23, 59, 59, 0, hcm),
		time.Date(2026, 10, 7, 0, 0, 0, 0, hcm),
		time.Date(2026, 10, 7, 9, 0, 0, 0, hcm),
		time.Date(2026, 10, 7, 23, 59, 59, 0, hcm),
	}
	dir := initRepoWithCommits(t, instants)
	collector := NewLocalGitCollector(dir)

	d1, err := collector.FetchDayCommits(context.Background(), "", day1)
	if err != nil {
		t.Fatalf("FetchDayCommits day1: %v", err)
	}
	d2, err := collector.FetchDayCommits(context.Background(), "", day2)
	if err != nil {
		t.Fatalf("FetchDayCommits day2: %v", err)
	}

	seen := map[string]int{}
	for _, c := range d1 {
		seen[c.SHA]++
	}
	for _, c := range d2 {
		seen[c.SHA]++
	}
	if len(seen) != len(instants) {
		t.Fatalf("adjacent days must cover every commit exactly once: got %d distinct, want %d", len(seen), len(instants))
	}
	for sha, n := range seen {
		if n != 1 {
			t.Errorf("commit %s appeared in %d windows (want exactly 1)", sha, n)
		}
	}
}
