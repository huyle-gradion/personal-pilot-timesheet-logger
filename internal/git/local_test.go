package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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
