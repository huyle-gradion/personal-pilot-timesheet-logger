package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
)

// Named finite bounds with explicit units (TigerStyle).
const (
	MaxEnvFileSizeBytes    int64         = 64 * 1024
	MaxEnvLineLengthBytes  int           = 2048
	MaxDateLengthBytes     int           = 32
	MaxReposLengthBytes    int           = 4096
	MaxAuthorLengthBytes   int           = 256
	CommandTimeoutDuration time.Duration = 60 * time.Second
)

// Operating exit codes conforming to sysexits.h.
const (
	ExitOK    = 0
	ExitUsage = 64 // EX_USAGE (command line usage error / preflight missing config)
	ExitErr   = 1
)

// Invariant assertions (TigerStyle).
const (
	AssertWritersNonNil = "ASSERT_GIT_CLI_001_NIL_IO_WRITER"
)

// Assert validates internal programmer invariants.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// SimpleEntry represents a normalized timesheet entry emitted on stdout.
type SimpleEntry struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	Timestamp   string         `json:"timestamp"`
	EndTime     string         `json:"endTime"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// Config holds parsed CLI configuration.
type Config struct {
	Date          string
	Repos         string
	Author        string
	Token         string
	GitHubBaseURL string
	EnvFile       string
}

// loadEnv loads environment variables from a file if not already set in environment.
func loadEnv(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > MaxEnvFileSizeBytes {
		return nil
	}

	limitedReader := io.LimitReader(f, MaxEnvFileSizeBytes+1)
	scanner := bufio.NewScanner(limitedReader)
	scanner.Buffer(make([]byte, MaxEnvLineLengthBytes), MaxEnvLineLengthBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		k := strings.TrimSpace(parts[0])
		v := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return scanner.Err()
}

// parseFlags parses CLI flags and enforces finite bounds on string inputs.
func parseFlags(args []string, stderr io.Writer) (*Config, int, error) {
	fs := flag.NewFlagSet("fetch-git", flag.ContinueOnError)
	fs.SetOutput(stderr)

	today := time.Now().Format("2006-01-02")
	dateFlag := fs.String("date", today, "Target date YYYY-MM-DD (default: today)")
	reposFlag := fs.String("repos", "", "Comma-separated repositories: 'owner/repo' or local directory paths (default: TRACKED_REPOS or '.')")
	authorFlag := fs.String("author", "", "Author username or email to filter commits (default: GIT_AUTHOR or GITHUB_USER)")
	tokenFlag := fs.String("token", "", "GitHub Personal Access Token (default: GITHUB_TOKEN or GH_TOKEN)")
	baseURLFlag := fs.String("github-base-url", "", "GitHub API base URL override (for testing)")
	envFlag := fs.String("env", ".env", "Path to .env file")

	if err := fs.Parse(args); err != nil {
		return nil, ExitUsage, err
	}

	if len(*dateFlag) > MaxDateLengthBytes {
		return nil, ExitUsage, fmt.Errorf("date length exceeds limit of %d bytes", MaxDateLengthBytes)
	}
	if len(*reposFlag) > MaxReposLengthBytes {
		return nil, ExitUsage, fmt.Errorf("repos length exceeds limit of %d bytes", MaxReposLengthBytes)
	}
	if len(*authorFlag) > MaxAuthorLengthBytes {
		return nil, ExitUsage, fmt.Errorf("author length exceeds limit of %d bytes", MaxAuthorLengthBytes)
	}

	repos := *reposFlag
	if repos == "" {
		repos = os.Getenv("TRACKED_REPOS")
	}
	if repos == "" {
		repos = "."
	}

	author := *authorFlag
	if author == "" {
		author = os.Getenv("GIT_AUTHOR")
	}
	if author == "" {
		author = os.Getenv("GITHUB_USER")
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}

	baseURL := *baseURLFlag
	if baseURL == "" {
		baseURL = os.Getenv("GITHUB_BASE_URL")
	}

	return &Config{
		Date:          *dateFlag,
		Repos:         repos,
		Author:        author,
		Token:         token,
		GitHubBaseURL: baseURL,
		EnvFile:       *envFlag,
	}, ExitOK, nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func resolveRepoForTier1(ctx context.Context, repo string) string {
	if repo == "." || strings.HasPrefix(repo, "/") || strings.HasPrefix(repo, "./") || isDir(repo) {
		collector := git.NewLocalGitCollector(repo)
		slug := collector.RepoName(ctx)
		if slug != "" && strings.Contains(slug, "/") {
			return slug
		}
	}
	return repo
}

// deduplicateActivity groups commits belonging to PR branches under their parent PR entry.
// Unparented commits become separate SimpleEntry records. Guarantees zero double-counting.
func deduplicateActivity(repo string, commits []git.GitHubCommit, prs []git.GitHubPR, fallback bool) []SimpleEntry {
	claimedCommits := make(map[string]bool)
	prCommits := make(map[int][]git.GitHubCommit)

	// Pre-build PR commit lookup set
	prShaSet := make(map[int]map[string]bool)
	for _, pr := range prs {
		prShaSet[pr.Number] = make(map[string]bool)
		for _, sha := range pr.Commits {
			prShaSet[pr.Number][sha] = true
			if len(sha) >= 7 {
				prShaSet[pr.Number][sha[:7]] = true
			}
		}
	}

	// Match commits to PRs:
	// 1. By commit SHA listed in PR
	// 2. By PR reference in commit message (#<num>, GH-<num>, pull/<num>, PR-<num>)
	for _, c := range commits {
		matchedPR := 0

		// Check SHA match
		for _, pr := range prs {
			set := prShaSet[pr.Number]
			short := c.SHA
			if len(short) > 7 {
				short = short[:7]
			}
			if set[c.SHA] || set[short] {
				matchedPR = pr.Number
				break
			}
		}

		// Check message reference match
		if matchedPR == 0 {
			collector := git.NewLocalGitCollector(".")
			groups := collector.DiscoverPRReferences([]git.GitHubCommit{c})
			for prNum := range groups {
				for _, pr := range prs {
					if pr.Number == prNum {
						matchedPR = prNum
						break
					}
				}
				if matchedPR != 0 {
					break
				}
			}
		}

		if matchedPR != 0 {
			prCommits[matchedPR] = append(prCommits[matchedPR], c)
			claimedCommits[c.SHA] = true
		}
	}

	var entries []SimpleEntry

	// Sort PRs deterministically by number
	sortedPRs := make([]git.GitHubPR, len(prs))
	copy(sortedPRs, prs)
	sort.Slice(sortedPRs, func(i, j int) bool {
		return sortedPRs[i].Number < sortedPRs[j].Number
	})

	for _, pr := range sortedPRs {
		cList := prCommits[pr.Number]
		// Sort commits chronologically
		sort.Slice(cList, func(i, j int) bool {
			return cList[i].Timestamp.Before(cList[j].Timestamp)
		})

		var startTime, endTime string
		if len(cList) > 0 {
			startTime = cList[0].Timestamp.Format(time.RFC3339)
			endTime = cList[len(cList)-1].Timestamp.Format(time.RFC3339)
		} else {
			nowStr := time.Now().Format(time.RFC3339)
			startTime = nowStr
			endTime = nowStr
		}

		// Title formatting
		title := pr.Title
		prTag := fmt.Sprintf("#%d", pr.Number)
		if !strings.Contains(title, prTag) {
			title = fmt.Sprintf("PR #%d: %s", pr.Number, title)
		}

		// Deduplicated commit list in metadata
		var shaList []string
		seenSha := make(map[string]bool)
		for _, c := range cList {
			short := c.SHA
			if len(short) > 7 {
				short = short[:7]
			}
			if !seenSha[short] {
				seenSha[short] = true
				shaList = append(shaList, short)
			}
		}
		for _, s := range pr.Commits {
			short := s
			if len(short) > 7 {
				short = short[:7]
			}
			if !seenSha[short] {
				seenSha[short] = true
				shaList = append(shaList, short)
			}
		}

		var descParts []string
		for _, c := range cList {
			descParts = append(descParts, c.Message)
		}
		description := pr.Title
		if len(descParts) > 0 {
			description = strings.Join(descParts, "; ")
		}

		meta := map[string]any{
			"repo":         repo,
			"pr_number":    pr.Number,
			"commit_count": len(shaList),
			"commits":      shaList,
		}
		if pr.Branch != "" {
			meta["branch"] = pr.Branch
		}
		if fallback {
			meta["source_warning"] = "github_api_unavailable_local_fallback"
		}

		entries = append(entries, SimpleEntry{
			ID:          fmt.Sprintf("git-pr-%d", pr.Number),
			Source:      "git",
			Timestamp:   startTime,
			EndTime:     endTime,
			Title:       title,
			Description: description,
			Metadata:    meta,
		})
	}

	// Process unparented commits: commits not attached to any PR
	// Sort chronologically
	var unparented []git.GitHubCommit
	for _, c := range commits {
		if !claimedCommits[c.SHA] {
			unparented = append(unparented, c)
		}
	}
	sort.Slice(unparented, func(i, j int) bool {
		return unparented[i].Timestamp.Before(unparented[j].Timestamp)
	})

	for _, c := range unparented {
		short := c.SHA
		if len(short) > 7 {
			short = short[:7]
		}

		meta := map[string]any{
			"repo":       repo,
			"commit_sha": c.SHA,
		}
		if fallback {
			meta["source_warning"] = "github_api_unavailable_local_fallback"
		}

		entries = append(entries, SimpleEntry{
			ID:          fmt.Sprintf("git-commit-%s", short),
			Source:      "git",
			Timestamp:   c.Timestamp.Format(time.RFC3339),
			EndTime:     c.Timestamp.Format(time.RFC3339),
			Title:       c.Message,
			Description: c.Message,
			Metadata:    meta,
		})
	}

	return entries
}

func redact(s, token string) string {
	if token != "" && len(token) > 4 {
		s = strings.ReplaceAll(s, token, "[REDACTED]")
	}
	return s
}

// run is the testable entry point for fetch-git CLI.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	Assert(stdout != nil, AssertWritersNonNil)
	Assert(stderr != nil, AssertWritersNonNil)

	cfg, exitCode, err := parseFlags(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Error parsing flags: %v\n", err)
		return exitCode
	}

	_ = loadEnv(cfg.EnvFile)

	// Re-check env vars in case .env populated them
	if cfg.Token == "" {
		cfg.Token = os.Getenv("GITHUB_TOKEN")
		if cfg.Token == "" {
			cfg.Token = os.Getenv("GH_TOKEN")
		}
	}
	if cfg.Author == "" {
		cfg.Author = os.Getenv("GIT_AUTHOR")
		if cfg.Author == "" {
			cfg.Author = os.Getenv("GITHUB_USER")
		}
	}

	var rawRepos []string
	for _, r := range strings.Split(cfg.Repos, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			rawRepos = append(rawRepos, r)
		}
	}

	// 1. Preflight check (ADR-005)
	preflight := git.CheckPreflight(ctx, cfg.Token, rawRepos)
	if !preflight.Ready {
		fmt.Fprintf(stderr, "Pre-flight check failed:\n%s\n", preflight.Guide)
		return ExitUsage
	}

	token := preflight.Token

	// Parse date
	targetDate, err := time.Parse("2006-01-02", cfg.Date)
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid target date %q: expected YYYY-MM-DD\n", cfg.Date)
		return ExitUsage
	}

	var allEntries []SimpleEntry

	for _, repo := range rawRepos {
		tier1Repo := resolveRepoForTier1(ctx, repo)

		// 2. Resilient Two-Tier Collection (ADR-002)
		// Try Tier 1: GitHub REST API
		client := git.NewGitHubClient(token, git.HTTPRequestTimeoutSeconds)
		if cfg.Author != "" {
			client.SetAuthor(cfg.Author)
		}
		if cfg.GitHubBaseURL != "" {
			client.SetBaseURL(cfg.GitHubBaseURL)
		}

		var commits []git.GitHubCommit
		var prs []git.GitHubPR
		var usedFallback bool

		commits, prs, err = client.FetchDayActivity(ctx, tier1Repo, targetDate)
		if err != nil {
			// Tier 1 failure: rate limit, 5xx, network drop, or missing repo on GitHub
			warnMsg := redact(fmt.Sprintf("Warning: Tier 1 GitHub API failed for repo %q: %v; activating Tier 2 local git fallback", repo, err), token)
			fmt.Fprintln(stderr, warnMsg)
			usedFallback = true

			// Tier 2: Local Git Collector
			localPath := repo
			if !isDir(localPath) {
				if isDir(".") {
					localPath = "."
				}
			}

			localCollector := git.NewLocalGitCollector(localPath)
			commits, prs, err = localCollector.FetchDayActivity(ctx, cfg.Author, targetDate)
			if err != nil {
				errMsg := redact(fmt.Sprintf("Error: Tier 2 local git collector failed for repo %q: %v", repo, err), token)
				fmt.Fprintln(stderr, errMsg)
				continue
			}
		}

		// 3. Deduplication: attach commits to parent PRs, unparented commits become standalone
		entries := deduplicateActivity(repo, commits, prs, usedFallback)
		allEntries = append(allEntries, entries...)
	}

	if allEntries == nil {
		allEntries = []SimpleEntry{}
	}

	// 4. Standard I/O: JSON array to stdout without HTML escaping
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(allEntries); err != nil {
		fmt.Fprintf(stderr, "Error encoding output JSON: %v\n", err)
		return ExitErr
	}

	return ExitOK
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
