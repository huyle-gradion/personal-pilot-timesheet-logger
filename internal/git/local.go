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
// Matches #123, GH-123, pull/123, PR-123.
var prReferenceRegex = regexp.MustCompile(`(?i)(?:#|GH-|pull/|PR-)(\d+)`)

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
	// On context cancellation or timeout, kill the entire process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil && cmd.Process.Pid > 0 {
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
			return nil, ErrNotAGitRepository
		}
		return nil, fmt.Errorf("git %s failed: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(stderrStr))
	}

	return stdoutBuf.Bytes(), nil
}

type limitedWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (l *limitedWriter) Write(p []byte) (n int, err error) {
	if l.n >= l.limit {
		return len(p), nil
	}
	toWrite := p
	remaining := l.limit - l.n
	if int64(len(toWrite)) > remaining {
		toWrite = toWrite[:remaining]
	}
	written, err := l.w.Write(toWrite)
	l.n += int64(written)
	if err != nil {
		return written, err
	}
	return len(p), nil
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
		loc = time.Local
	}
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	endOfDay := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 999999999, loc)

	// Since and until boundaries in ISO 8601 format.
	sinceStr := startOfDay.Format(time.RFC3339)
	untilStr := endOfDay.Format(time.RFC3339)

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
				parsedTime = startOfDay
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
		matches := prReferenceRegex.FindAllStringSubmatch(c.Message, -1)
		for _, m := range matches {
			if len(m) > 1 {
				num, err := strconv.Atoi(m[1])
				if err == nil && num > 0 {
					grouped[num] = append(grouped[num], c)
				}
			}
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
