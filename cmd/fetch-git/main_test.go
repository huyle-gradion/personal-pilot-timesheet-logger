package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
)

func setupIntegrationGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test Engineer",
			"GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Test Engineer",
			"GIT_COMMITTER_EMAIL=dev@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.name", "Test Engineer")
	runGit("config", "user.email", "dev@example.com")

	f1 := filepath.Join(dir, "task1.txt")
	if err := os.WriteFile(f1, []byte("task 1"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "task1.txt")
	runGit("commit", "-m", "feat: implement collector (#42)")

	f2 := filepath.Join(dir, "task2.txt")
	if err := os.WriteFile(f2, []byte("task 2"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "task2.txt")
	runGit("commit", "-m", "chore: standalone work item")

	return dir
}

func TestFlagDefaultsAndEnvPrecedence(t *testing.T) {
	var stderr bytes.Buffer
	today := time.Now().Format("2006-01-02")

	// 1. Defaults when no flags and no env
	t.Setenv("TRACKED_REPOS", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GIT_AUTHOR", "")
	t.Setenv("GITHUB_USER", "")

	cfg, code, err := parseFlags([]string{}, &stderr)
	if err != nil || code != ExitOK {
		t.Fatalf("expected ExitOK, got code %d, err: %v", code, err)
	}
	if cfg.Date != today {
		t.Errorf("expected default date %q, got %q", today, cfg.Date)
	}
	if cfg.Repos != "." {
		t.Errorf("expected default repos '.', got %q", cfg.Repos)
	}

	// 2. Env var fallback
	t.Setenv("TRACKED_REPOS", "repo-from-env")
	t.Setenv("GITHUB_TOKEN", "token-from-env")
	t.Setenv("GIT_AUTHOR", "author-from-env")

	cfg, code, err = parseFlags([]string{}, &stderr)
	if err != nil || code != ExitOK {
		t.Fatalf("expected ExitOK, got code %d, err: %v", code, err)
	}
	if cfg.Repos != "repo-from-env" {
		t.Errorf("expected repos from env, got %q", cfg.Repos)
	}
	if cfg.Token != "token-from-env" {
		t.Errorf("expected token from env, got %q", cfg.Token)
	}
	if cfg.Author != "author-from-env" {
		t.Errorf("expected author from env, got %q", cfg.Author)
	}

	// 3. Flags override env vars
	args := []string{
		"-date=2026-10-01",
		"-repos=custom/repo",
		"-token=flag-token",
		"-author=flag-author",
	}
	cfg, code, err = parseFlags(args, &stderr)
	if err != nil || code != ExitOK {
		t.Fatalf("expected ExitOK, got code %d, err: %v", code, err)
	}
	if cfg.Date != "2026-10-01" {
		t.Errorf("expected flag date, got %q", cfg.Date)
	}
	if cfg.Repos != "custom/repo" {
		t.Errorf("expected flag repos, got %q", cfg.Repos)
	}
	if cfg.Token != "flag-token" {
		t.Errorf("expected flag token, got %q", cfg.Token)
	}
	if cfg.Author != "flag-author" {
		t.Errorf("expected flag author, got %q", cfg.Author)
	}
}

func TestExitCode64OnMissingTokenWithGuidance(t *testing.T) {
	ctx := context.Background()
	var stdout, stderr bytes.Buffer

	// Clear tokens and isolate PATH so gh auth token fails
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())

	code := run(ctx, []string{"-token=", "-repos=."}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected exit code %d (EX_USAGE), got %d", ExitUsage, code)
	}

	errOutput := stderr.String()
	if !strings.Contains(errOutput, "Pre-flight check failed:") {
		t.Errorf("expected 'Pre-flight check failed:' in stderr, got:\n%s", errOutput)
	}
	if !strings.Contains(errOutput, "gh auth login") {
		t.Errorf("expected 'gh auth login' guidance in stderr, got:\n%s", errOutput)
	}
	if !strings.Contains(errOutput, "repo") || !strings.Contains(errOutput, "read:user") {
		t.Errorf("expected token scopes 'repo' and 'read:user' in stderr, got:\n%s", errOutput)
	}
}

func TestDeduplicationZeroDoubleCounting(t *testing.T) {
	now := time.Now()
	commits := []git.GitHubCommit{
		{
			SHA:       "1111111111111111111111111111111111111111",
			Message:   "feat: implement feature (#42)",
			Timestamp: now.Add(-2 * time.Hour),
			Repo:      "test-repo",
		},
		{
			SHA:       "2222222222222222222222222222222222222222",
			Message:   "fix: resolve edge cases (#42)",
			Timestamp: now.Add(-1 * time.Hour),
			Repo:      "test-repo",
		},
		{
			SHA:       "3333333333333333333333333333333333333333",
			Message:   "docs: update architecture docs",
			Timestamp: now.Add(-30 * time.Minute),
			Repo:      "test-repo",
		},
		{
			SHA:       "4444444444444444444444444444444444444444",
			Message:   "fix: hotfix bug (GH-99)",
			Timestamp: now.Add(-10 * time.Minute),
			Repo:      "test-repo",
		},
	}

	prs := []git.GitHubPR{
		{
			Number:  42,
			Title:   "feat: implement feature",
			Branch:  "feat/42",
			Repo:    "test-repo",
			Commits: []string{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"},
		},
		{
			Number:  99,
			Title:   "fix: hotfix bug",
			Branch:  "fix/99",
			Repo:    "test-repo",
			Commits: []string{"4444444444444444444444444444444444444444"},
		},
	}

	entries := deduplicateActivity("test-repo", commits, prs, false)

	// Exactly 3 entries expected: PR 42, PR 99, and standalone commit 3
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify PR 42
	if entries[0].ID != "git-pr-42" {
		t.Errorf("expected first entry git-pr-42, got %q", entries[0].ID)
	}
	pr42Commits, ok := entries[0].Metadata["commits"].([]string)
	if !ok || len(pr42Commits) != 2 {
		t.Fatalf("expected 2 commits in PR 42 metadata, got %v", entries[0].Metadata["commits"])
	}

	// Verify PR 99
	if entries[1].ID != "git-pr-99" {
		t.Errorf("expected second entry git-pr-99, got %q", entries[1].ID)
	}
	pr99Commits, ok := entries[1].Metadata["commits"].([]string)
	if !ok || len(pr99Commits) != 1 {
		t.Fatalf("expected 1 commit in PR 99 metadata, got %v", entries[1].Metadata["commits"])
	}

	// Verify Standalone Commit 3
	if entries[2].ID != "git-commit-3333333" {
		t.Errorf("expected standalone commit git-commit-3333333, got %q", entries[2].ID)
	}
	if entries[2].Title != "docs: update architecture docs" {
		t.Errorf("expected standalone commit title, got %q", entries[2].Title)
	}

	// Invariant: Zero double-counting. Commits 1, 2, and 4 must not appear as standalone entries.
	for _, e := range entries {
		if e.ID == "git-commit-1111111" || e.ID == "git-commit-2222222" || e.ID == "git-commit-4444444" {
			t.Fatalf("double-counting violation: found %s as standalone entry", e.ID)
		}
	}
}

func TestTier2FallbackOnHTTP429(t *testing.T) {
	repoDir := setupIntegrationGitRepo(t)

	// Mock server returning HTTP 429
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message": "API rate limit exceeded"}`))
	}))
	defer server.Close()

	ctx := context.Background()
	var stdout, stderr bytes.Buffer
	today := time.Now().Format("2006-01-02")

	args := []string{
		"-token=test-valid-token",
		"-repos=" + repoDir,
		"-date=" + today,
		"-github-base-url=" + server.URL,
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("expected ExitOK (0), got code %d\nStderr: %s", code, stderr.String())
	}

	// Check fallback warning on stderr
	errOutput := stderr.String()
	if !strings.Contains(errOutput, "activating Tier 2 local git fallback") {
		t.Errorf("expected fallback warning on stderr, got:\n%s", errOutput)
	}

	// Check stdout JSON output
	var entries []SimpleEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("failed to parse stdout JSON: %v\nOutput: %s", err, stdout.String())
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries from local git fallback, got %d", len(entries))
	}

	// Verify source_warning metadata exists
	for _, entry := range entries {
		warning, ok := entry.Metadata["source_warning"].(string)
		if !ok || warning != "github_api_unavailable_local_fallback" {
			t.Errorf("expected source_warning 'github_api_unavailable_local_fallback', got: %v", entry.Metadata["source_warning"])
		}
	}
}
