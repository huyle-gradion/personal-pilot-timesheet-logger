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
	"path/filepath"
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
	MaxTimezoneLengthBytes int           = 64
	MaxReposLengthBytes    int           = 4096
	MaxAuthorLengthBytes   int           = 256
	MaxTokenLengthBytes    int           = 256
	MaxURLLengthBytes      int           = 2048
	MaxPathLengthBytes     int           = 4096
	CommandTimeoutDuration time.Duration = 60 * time.Second
)

// Operating exit codes conforming to sysexits.h.
const (
	ExitOK    = 0
	ExitUsage = 64 // EX_USAGE (command line usage error / preflight missing config)
	ExitErr   = 1
)

// DefaultTimezone is the fixed, host-independent timezone used when --tz is
// omitted. Owner decision: the collector must NEVER anchor the day window to the
// host's zone (time.Local), so a machine with a different TZ produces identical
// output. Asia/Ho_Chi_Minh (UTC+07:00) is the deliberate default.
const DefaultTimezone = "Asia/Ho_Chi_Minh"

// defaultLocation resolves DefaultTimezone, falling back to a fixed +07:00 zone
// when the IANA database is unavailable, so the default stays host-independent
// even without zoneinfo. It deliberately never consults time.Local.
func defaultLocation() *time.Location {
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		return time.FixedZone("UTC+07:00", 7*60*60)
	}
	return loc
}

// resolveTargetLocation resolves the collector's target timezone: the explicit
// --tz value when set, otherwise the fixed host-independent DefaultTimezone.
func resolveTargetLocation(tz string) (*time.Location, error) {
	if tz != "" {
		return time.LoadLocation(tz)
	}
	return defaultLocation(), nil
}

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
	Timezone      string
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
	tzFlag := fs.String("tz", "", "Timezone for target date (e.g. Asia/Ho_Chi_Minh; default: "+DefaultTimezone+")")
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
	if len(*tzFlag) > MaxTimezoneLengthBytes {
		return nil, ExitUsage, fmt.Errorf("timezone length exceeds limit of %d bytes", MaxTimezoneLengthBytes)
	}
	if len(*reposFlag) > MaxReposLengthBytes {
		return nil, ExitUsage, fmt.Errorf("repos length exceeds limit of %d bytes", MaxReposLengthBytes)
	}
	if len(*authorFlag) > MaxAuthorLengthBytes {
		return nil, ExitUsage, fmt.Errorf("author length exceeds limit of %d bytes", MaxAuthorLengthBytes)
	}
	if len(*tokenFlag) > MaxTokenLengthBytes {
		return nil, ExitUsage, fmt.Errorf("token length exceeds limit of %d bytes", MaxTokenLengthBytes)
	}
	if len(*baseURLFlag) > MaxURLLengthBytes {
		return nil, ExitUsage, fmt.Errorf("github-base-url length exceeds limit of %d bytes", MaxURLLengthBytes)
	}
	if len(*envFlag) > MaxPathLengthBytes {
		return nil, ExitUsage, fmt.Errorf("env path length exceeds limit of %d bytes", MaxPathLengthBytes)
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
		Timezone:      *tzFlag,
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

func maskPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return ".../" + filepath.Base(p)
	}
	return p
}

func sanitizeSlugForID(repo string) string {
	r := strings.ReplaceAll(repo, "/", "-")
	r = strings.ReplaceAll(r, ".", "")
	r = strings.Trim(r, "-")
	if r == "" {
		return "local"
	}
	return r
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

func isLocalRepoMatchingRemote(ctx context.Context, localPath, targetRepoSlug string) bool {
	collector := git.NewLocalGitCollector(localPath)
	localSlug := collector.RepoName(ctx)
	if localSlug == "" {
		return false
	}
	return localSlug == targetRepoSlug || strings.HasSuffix(localSlug, "/"+targetRepoSlug) || strings.HasSuffix(targetRepoSlug, "/"+localSlug)
}

// deduplicateActivity groups commits belonging to PR branches under their parent PR entry.
// Unparented commits become separate SimpleEntry records. Guarantees zero double-counting.
func deduplicateActivity(repo string, commits []git.GitHubCommit, prs []git.GitHubPR, fallback bool, targetDate time.Time) []SimpleEntry {
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
	// 2. By PR reference in commit message
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

		// Check message reference match with sorted keys to avoid map iteration randomness
		if matchedPR == 0 {
			prNums := git.ExtractPRReferences(c.Message)
			sort.Ints(prNums)
			for _, prNum := range prNums {
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
	repoSlug := sanitizeSlugForID(repo)

	// Sort PRs deterministically by number
	sortedPRs := make([]git.GitHubPR, len(prs))
	copy(sortedPRs, prs)
	sort.Slice(sortedPRs, func(i, j int) bool {
		return sortedPRs[i].Number < sortedPRs[j].Number
	})

	loc := targetDate.Location()
	if loc == nil {
		loc = time.UTC
	}
	startOfDay := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, loc).Format(time.RFC3339)
	endOfDay := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 23, 59, 59, 999999999, loc).Format(time.RFC3339)

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
			startTime = startOfDay
			endTime = endOfDay
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
			ID:          fmt.Sprintf("git-pr-%s-%d", repoSlug, pr.Number),
			Source:      "git",
			Timestamp:   startTime,
			EndTime:     endTime,
			Title:       title,
			Description: description,
			Metadata:    meta,
		})
	}

	// Process unparented commits
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
			ID:          fmt.Sprintf("git-commit-%s-%s", repoSlug, short),
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
	tok := strings.TrimSpace(token)
	if tok != "" {
		s = strings.ReplaceAll(s, tok, "[REDACTED]")
	}
	return s
}

// run is the testable entry point for fetch-git CLI.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	Assert(stdout != nil, AssertWritersNonNil)
	Assert(stderr != nil, AssertWritersNonNil)

	runCtx, runCancel := context.WithTimeout(ctx, CommandTimeoutDuration)
	defer runCancel()

	cfg, exitCode, err := parseFlags(args, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "Error parsing flags: %v\n", err)
		return exitCode
	}

	if err := loadEnv(cfg.EnvFile); err != nil {
		fmt.Fprintf(stderr, "Warning: failed loading env file: %v\n", err)
	}

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
	preflight := git.CheckPreflight(runCtx, cfg.Token, rawRepos)
	if !preflight.Ready {
		fmt.Fprintf(stderr, "Pre-flight check failed:\n%s\n", preflight.Guide)
		return ExitUsage
	}

	token := preflight.Token

	// Parse date with timezone support
	loc, tzErr := resolveTargetLocation(cfg.Timezone)
	if tzErr != nil {
		fmt.Fprintf(stderr, "Error: invalid timezone %q: %v\n", cfg.Timezone, tzErr)
		return ExitUsage
	}

	targetDate, err := time.ParseInLocation("2006-01-02", cfg.Date, loc)
	if err != nil {
		fmt.Fprintf(stderr, "Error: invalid target date %q: expected YYYY-MM-DD\n", cfg.Date)
		return ExitUsage
	}

	// Client reuse: instantiate GitHubClient once before the repository loop
	client := git.NewGitHubClient(token, git.HTTPRequestTimeoutSeconds)
	if cfg.Author != "" {
		client.SetAuthor(cfg.Author)
	}
	if cfg.GitHubBaseURL != "" {
		client.SetBaseURL(cfg.GitHubBaseURL)
	}

	var allEntries []SimpleEntry
	successCount := 0

	for _, repo := range rawRepos {
		tier1Repo := resolveRepoForTier1(runCtx, repo)

		// 2. Resilient Two-Tier Collection (ADR-002)
		var commits []git.GitHubCommit
		var prs []git.GitHubPR
		var usedFallback bool

		commits, prs, err = client.FetchDayActivity(runCtx, tier1Repo, targetDate)
		if err != nil {
			warnMsg := redact(fmt.Sprintf("Warning: Tier 1 GitHub API failed for repo %q: %v; activating Tier 2 local git fallback", maskPath(repo), err), token)
			fmt.Fprintln(stderr, warnMsg)
			usedFallback = true

			// Tier 2: Local Git Collector
			localPath := repo
			if !isDir(localPath) {
				if isDir(".") && isLocalRepoMatchingRemote(runCtx, ".", tier1Repo) {
					localPath = "."
				} else {
					errMsg := redact(fmt.Sprintf("Error: skipping Tier 2 local fallback for repo %q: local directory does not match remote repository", maskPath(repo)), token)
					fmt.Fprintln(stderr, errMsg)
					continue
				}
			}

			localCollector := git.NewLocalGitCollector(localPath)
			repoAuthor := cfg.Author
			if repoAuthor == "" {
				repoAuthor = localCollector.Author(runCtx)
			}
			commits, prs, err = localCollector.FetchDayActivity(runCtx, repoAuthor, targetDate)
			if err != nil {
				errMsg := redact(fmt.Sprintf("Error: Tier 2 local git collector failed for repo %q: %v", maskPath(repo), err), token)
				fmt.Fprintln(stderr, errMsg)
				continue
			}
		}

		// 3. Deduplication: attach commits to parent PRs, unparented commits become standalone
		entries := deduplicateActivity(tier1Repo, commits, prs, usedFallback, targetDate)
		allEntries = append(allEntries, entries...)
		successCount++
	}

	if len(rawRepos) > 0 && successCount == 0 {
		return ExitErr
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
