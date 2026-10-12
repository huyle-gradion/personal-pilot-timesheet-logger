package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrNotAGitRepository indicates the specified directory is not a valid git repository.
var ErrNotAGitRepository = errors.New("not a git repository")

// Regex to extract PR and issue references from commit messages or branches.
// Matches #123, GH-123, pull/123, PR-123, issue-123, issue/123.
var prReferenceRegex = regexp.MustCompile(`(?i)(?:#|GH-|pull/|PR-|issue[-/])(\d+)`)

// ExtractPRReferences extracts unique PR or issue numbers from text.
func ExtractPRReferences(text string) []int {
	matches := prReferenceRegex.FindAllStringSubmatch(text, -1)
	var nums []int
	seen := make(map[int]bool)
	for _, m := range matches {
		if len(m) > 1 {
			num, err := strconv.Atoi(m[1])
			if err == nil && num > 0 && !seen[num] {
				seen[num] = true
				nums = append(nums, num)
			}
		}
	}
	return nums
}

// sanitizeArgs strips absolute path prefixes from command arguments for safe error reporting.
func sanitizeArgs(args []string) string {
	sanitized := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "/") {
			sanitized[i] = ".../" + filepath.Base(a)
		} else {
			sanitized[i] = a
		}
	}
	return strings.Join(sanitized, " ")
}

// LocalGitCollector extracts activity directly from a local git repository via git subprocesses.
type LocalGitCollector struct {
	repoPath string
}

// NewLocalGitCollector constructs a LocalGitCollector for the given repository path.
func NewLocalGitCollector(repoPath string) *LocalGitCollector {
	if repoPath == "" {
		repoPath = "."
	}
	return &LocalGitCollector{repoPath: repoPath}
}

// RepoPath returns the root directory of the local git repository.
func (l *LocalGitCollector) RepoPath() string {
	return l.repoPath
}

// execGit executes a git command in the repository directory with subprocess isolation and bounds.
func (l *LocalGitCollector) execGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = l.repoPath

	// Subprocess safety: Setpgid puts the command into a separate process group.
	// On context cancellation or timeout, send SIGTERM, wait 100ms, then escalate to SIGKILL.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	cmd.Stdout = &limitedWriter{w: stdoutBuf, limit: MaxResponseSizeBytes}
	cmd.Stderr = &limitedWriter{w: stderrBuf, limit: MaxResponseSizeBytes}

	err := cmd.Run()
	if err != nil {
		stderrStr := stderrBuf.String()
		if strings.Contains(stderrStr, "not a git repository") {
			return nil, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "GIT_ERR_NOT_A_REPO",
				Message: fmt.Sprintf("directory %s is not a git repository", sanitizeArgs([]string{l.repoPath})),
				Err:     ErrNotAGitRepository,
			}
		}
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_LOCAL_EXEC",
			Message: fmt.Sprintf("git %s failed: %v", sanitizeArgs(args), err),
			Err:     err,
		}
	}

	return stdoutBuf.Bytes(), nil
}

type limitedWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (l *limitedWriter) Write(p []byte) (n int, err error) {
	if l.n+int64(len(p)) > l.limit {
		return 0, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_CAPACITY_EXCEEDED",
			Message: fmt.Sprintf("git subprocess output exceeded maximum capacity of %d bytes", l.limit),
		}
	}
	written, err := l.w.Write(p)
	l.n += int64(written)
	return written, err
}

// RepoName determines the repository slug or name for the local repository.
func (l *LocalGitCollector) RepoName(ctx context.Context) string {
	out, err := l.execGit(ctx, "config", "--get", "remote.origin.url")
	if err == nil {
		remoteURL := strings.TrimSpace(string(out))
		if remoteURL != "" {
			owner, repo, parseErr := ParseRepoSlug(remoteURL)
			if parseErr == nil && repo != "" {
				if owner != "" {
					return owner + "/" + repo
				}
				return repo
			}
		}
	}

	absPath, err := filepath.Abs(l.repoPath)
	if err == nil {
		return filepath.Base(absPath)
	}
	return l.repoPath
}

// CurrentBranch returns the currently active branch name.
func (l *LocalGitCollector) CurrentBranch(ctx context.Context) string {
	out, err := l.execGit(ctx, "branch", "--show-current")
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// Author resolves the git user email or user name configured for this local repository.
func (l *LocalGitCollector) Author(ctx context.Context) string {
	Assert(l != nil, AssertClientNil)
	Assert(ctx != nil, AssertContextNil)

	out, err := l.execGit(ctx, "config", "--get", "user.email")
	if err == nil {
		email := strings.TrimSpace(string(out))
		if email != "" {
			return email
		}
	}
	out, err = l.execGit(ctx, "config", "--get", "user.name")
	if err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return name
		}
	}
	return ""
}

// FetchDayCommits retrieves commits authored on targetDate using bounded git log format.
// Parses output formatted as: %H%x00%an%x00%ae%x00%aI%x00%s%x00.
func (l *LocalGitCollector) FetchDayCommits(ctx context.Context, author string, date time.Time) ([]GitHubCommit, error) {
	Assert(l != nil, AssertClientNil)
	Assert(ctx != nil, AssertContextNil)

	if date.IsZero() {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_INVALID_DATE",
			Message: "target date cannot be zero",
		}
	}

	loc := date.Location()
	if loc == nil {
		loc = time.UTC
	}
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	// The window is HALF-OPEN [start, end): end is the CALENDRICAL next
	// midnight (NOT start+24h), so a DST day that lasts 23h or 25h is still one
	// calendar day. git's --since/--until are INCLUSIVE, so --until is end minus
	// 1ns: this yields START INCLUSIVE / END EXCLUSIVE.
	end := time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, loc)
	// LIMITATION (pre-existing, NOT reconciled here): git's --since/--until
	// filter on the COMMITTER date, while the reported Timestamp below is the
	// AUTHOR date (%aI). When author and committer dates differ, a commit can
	// fall on opposite sides of this boundary for the two collectors; the remote
	// (GitHub) path filters by author date (committer fallback). Changing this
	// is out of scope.

	// Since and until boundaries in ISO 8601 format.
	sinceStr := start.Format(time.RFC3339)
	untilStr := end.Add(-time.Nanosecond).Format(time.RFC3339)

	args := []string{
		"log",
		"--all",
		fmt.Sprintf("--since=%s", sinceStr),
		fmt.Sprintf("--until=%s", untilStr),
		"--pretty=format:%H%x00%an%x00%ae%x00%aI%x00%s%x00",
	}

	out, err := l.execGit(ctx, args...)
	if err != nil {
		return nil, err
	}

	repoName := l.RepoName(ctx)
	chunks := bytes.Split(out, []byte{0x00})
	// Each commit consists of 5 fields: SHA, AuthorName, AuthorEmail, Timestamp, Subject.
	const fieldsPerCommit = 5

	var commits []GitHubCommit
	authorLower := strings.ToLower(strings.TrimSpace(author))

	for i := 0; i+fieldsPerCommit <= len(chunks); i += fieldsPerCommit {
		sha := strings.TrimSpace(string(chunks[i]))
		if sha == "" {
			continue
		}
		authorName := strings.TrimSpace(string(chunks[i+1]))
		authorEmail := strings.TrimSpace(string(chunks[i+2]))
		timestampStr := strings.TrimSpace(string(chunks[i+3]))
		subject := strings.TrimSpace(string(chunks[i+4]))

		// Filter by author if author is specified
		if authorLower != "" {
			matchesName := strings.Contains(strings.ToLower(authorName), authorLower)
			matchesEmail := strings.Contains(strings.ToLower(authorEmail), authorLower)
			if !matchesName && !matchesEmail {
				continue
			}
		}

		parsedTime, parseErr := time.Parse(time.RFC3339, timestampStr)
		if parseErr != nil {
			// Fallback ISO format without timezone offset or with space
			parsedTime, parseErr = time.Parse("2006-01-02T15:04:05", timestampStr)
			if parseErr != nil {
				parsedTime = start
			}
		}

		commits = append(commits, GitHubCommit{
			SHA:       sha,
			Message:   subject,
			Timestamp: parsedTime,
			Repo:      repoName,
		})

		if len(commits) >= MaxCommitsCount {
			break
		}
	}

	return commits, nil
}

// DiscoverPRReferences finds pull request / issue numbers from commit messages.
func (l *LocalGitCollector) DiscoverPRReferences(commits []GitHubCommit) map[int][]GitHubCommit {
	grouped := make(map[int][]GitHubCommit)
	for _, c := range commits {
		for _, num := range ExtractPRReferences(c.Message) {
			grouped[num] = append(grouped[num], c)
		}
	}
	return grouped
}

// FetchDayActivity retrieves commits and synthesizes PR entries from local git history.
func (l *LocalGitCollector) FetchDayActivity(ctx context.Context, author string, date time.Time) ([]GitHubCommit, []GitHubPR, error) {
	commits, err := l.FetchDayCommits(ctx, author, date)
	if err != nil {
		return nil, nil, err
	}

	repoName := l.RepoName(ctx)
	currentBranch := l.CurrentBranch(ctx)
	prGroups := l.DiscoverPRReferences(commits)

	// If commits lack PR tags in their messages, check if currentBranch contains a PR/issue reference.
	if len(currentBranch) > 0 {
		branchPRNums := ExtractPRReferences(currentBranch)
		for _, bNum := range branchPRNums {
			if _, exists := prGroups[bNum]; !exists {
				var unattached []GitHubCommit
				for _, c := range commits {
					if len(ExtractPRReferences(c.Message)) == 0 {
						unattached = append(unattached, c)
					}
				}
				if len(unattached) > 0 {
					prGroups[bNum] = unattached
				}
			}
		}
	}

	var prs []GitHubPR
	for prNum, prCommits := range prGroups {
		var shas []string
		title := fmt.Sprintf("PR #%d", prNum)
		if len(prCommits) > 0 {
			title = prCommits[0].Message
			for _, pc := range prCommits {
				shas = append(shas, pc.SHA)
			}
		}

		branch := currentBranch
		branchMatches := prReferenceRegex.FindStringSubmatch(currentBranch)
		if len(branchMatches) > 1 {
			bNum, _ := strconv.Atoi(branchMatches[1])
			if bNum != prNum {
				branch = ""
			}
		}

		prs = append(prs, GitHubPR{
			Number:  prNum,
			Title:   title,
			Branch:  branch,
			Repo:    repoName,
			Commits: shas,
		})

		if len(prs) >= MaxPRsCount {
			break
		}
	}

	return commits, prs, nil
}
