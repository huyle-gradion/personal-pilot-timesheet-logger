package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Named finite bounds with explicit units (TigerStyle).
const (
	MaxResponseSizeBytes      int64         = 5 * 1024 * 1024 // 5 MiB maximum response payload (per HANDOFF.md)
	MaxResponseHeaderBytes    int64         = 64 * 1024       // 64 KiB maximum HTTP response headers
	HTTPRequestTimeoutSeconds time.Duration = 5 * time.Second // 5s deadline per HTTP attempt (per HANDOFF.md)
	MaxRetriesCount           int           = 1               // 1 bounded retry on transient 5xx or connection drops
	MaxCommitsCount           int           = 100             // Maximum commits admitted per day
	MaxPRsCount               int           = 50              // Maximum pull requests admitted per day
	MaxPRCommitsCount         int           = 100             // Maximum commit SHAs admitted per PR
	MaxPRCommitLookupsCount   int           = 5               // Maximum PR commit lookups per day to eliminate N+1 loops
	DefaultGitHubBaseURL      string        = "https://api.github.com"
	RetryBackoffDuration      time.Duration = 50 * time.Millisecond // Bounded retry backoff
)

// Invariant identifiers for programmer assertions (TigerStyle).
const (
	AssertClientNil  = "ASSERT_GIT_001_NIL_CLIENT"
	AssertContextNil = "ASSERT_GIT_002_NIL_CONTEXT"
)

// Assert checks an internal programmer-contract invariant and panics with invariantID if violated.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// Outcome classifies the operational result of an external interaction (TigerStyle).
type Outcome int

const (
	OutcomeAcknowledged Outcome = iota // Successful or committed outcome
	OutcomeRetryable                   // 5xx server errors, connection resets, transient network failures
	OutcomeTerminal                    // 4xx client errors (excl 429), validation failures, capacity overflow
	OutcomeRateLimited                 // HTTP 429 Too Many Requests or 403 rate limit exceeded
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAcknowledged:
		return "OutcomeAcknowledged"
	case OutcomeRetryable:
		return "OutcomeRetryable"
	case OutcomeTerminal:
		return "OutcomeTerminal"
	case OutcomeRateLimited:
		return "OutcomeRateLimited"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// OperatingError represents a typed, bounded operating error with a stable code and message.
type OperatingError struct {
	Outcome Outcome
	Code    string
	Message string
	Err     error
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		u, parseErr := url.Parse(urlErr.URL)
		if parseErr == nil {
			u.RawQuery = ""
			u.User = nil
			if urlErr.Err != nil {
				return fmt.Sprintf("%s %q: %v", urlErr.Op, u.String(), sanitizeError(urlErr.Err))
			}
			return fmt.Sprintf("%s %q", urlErr.Op, u.String())
		}
		if urlErr.Err != nil {
			return fmt.Sprintf("%s <redacted>: %v", urlErr.Op, sanitizeError(urlErr.Err))
		}
		return fmt.Sprintf("%s <redacted>", urlErr.Op)
	}
	return err.Error()
}

func (e *OperatingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, sanitizeError(e.Err))
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *OperatingError) Unwrap() error {
	return e.Err
}

// Helper predicates for error classification.
func IsRateLimited(err error) bool {
	var opErr *OperatingError
	if errors.As(err, &opErr) {
		return opErr.Outcome == OutcomeRateLimited
	}
	return false
}

func IsRetryable(err error) bool {
	var opErr *OperatingError
	if errors.As(err, &opErr) {
		return opErr.Outcome == OutcomeRetryable
	}
	return false
}

func IsTerminal(err error) bool {
	var opErr *OperatingError
	if errors.As(err, &opErr) {
		return opErr.Outcome == OutcomeTerminal
	}
	return false
}

// GitHubCommit represents a commit record retrieved from the GitHub REST API.
type GitHubCommit struct {
	SHA       string    `json:"sha"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Repo      string    `json:"repo"`
}

// GitHubPR represents a pull request record retrieved from the GitHub REST API.
type GitHubPR struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Branch  string   `json:"branch"`
	Repo    string   `json:"repo"`
	Commits []string `json:"commits"`
}

// GitHubClient interacts with the GitHub REST API with TigerStyle bounds and retries.
type GitHubClient struct {
	client  *http.Client
	token   string
	baseURL string
	timeout time.Duration
	author  string
	mu      sync.Mutex
}

// ResolveToken obtains a GitHub token from the provided argument, GITHUB_TOKEN environment variable,
// or GH_TOKEN environment variable as fallback.
func ResolveToken(token string) string {
	if token != "" {
		return token
	}
	if env := os.Getenv("GITHUB_TOKEN"); env != "" {
		return env
	}
	if env := os.Getenv("GH_TOKEN"); env != "" {
		return env
	}
	return ""
}

// NewGitHubClient constructs a GitHubClient enforcing bounded timeouts and TigerStyle HTTP transport.
func NewGitHubClient(token string, timeout time.Duration) *GitHubClient {
	if timeout <= 0 {
		timeout = HTTPRequestTimeoutSeconds
	}
	resolvedToken := ResolveToken(token)

	transport := &http.Transport{
		MaxResponseHeaderBytes: MaxResponseHeaderBytes,
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  timeout,
		TLSHandshakeTimeout:    timeout,
	}

	httpClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// TigerStyle Go Rule: Disable redirects. A redirect is a terminal protocol result.
			return http.ErrUseLastResponse
		},
		Transport: transport,
	}

	return &GitHubClient{
		client:  httpClient,
		token:   resolvedToken,
		baseURL: DefaultGitHubBaseURL,
		timeout: timeout,
	}
}

// SetBaseURL overrides the API base URL (primarily for testing with httptest.Server).
func (c *GitHubClient) SetBaseURL(baseURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseURL = strings.TrimRight(baseURL, "/")
}

// BaseURL returns the configured base URL.
func (c *GitHubClient) BaseURL() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.baseURL
}

// SetAuthor sets the author filter for commits and PR discovery.
func (c *GitHubClient) SetAuthor(author string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.author = author
}

// Author returns the author filter.
func (c *GitHubClient) Author() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.author
}

// SetToken sets or updates the authentication token.
func (c *GitHubClient) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
}

// Token returns the configured authentication token.
func (c *GitHubClient) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// ParseRepoSlug parses a repo input (e.g. "owner/repo", full URL, or slug) into owner and repository name.
func ParseRepoSlug(raw string) (string, string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_EMPTY_REPO",
			Message: "repository identifier cannot be empty",
		}
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")

	if strings.HasPrefix(s, "git@") {
		idx := strings.Index(s, ":")
		if idx != -1 {
			s = s[idx+1:]
		}
	} else if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err == nil && u.Path != "" {
			s = strings.TrimPrefix(u.Path, "/")
		}
	}

	var parts []string
	for _, p := range strings.Split(s, "/") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}

	if len(parts) >= 2 {
		owner := parts[0]
		repo := parts[1]
		return owner, repo, nil
	} else if len(parts) == 1 {
		return "", parts[0], nil
	}

	return "", "", &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "GIT_ERR_INVALID_REPO",
		Message: fmt.Sprintf("invalid repository identifier: %s", raw),
	}
}

func (c *GitHubClient) resolveRepo(ctx context.Context, repo string) (string, string, error) {
	owner, repoName, err := ParseRepoSlug(repo)
	if err != nil {
		return "", "", err
	}
	if owner != "" {
		return owner, repoName, nil
	}

	if c.Author() != "" {
		return c.Author(), repoName, nil
	}

	if envUser := os.Getenv("GITHUB_USER"); envUser != "" {
		return envUser, repoName, nil
	}
	if envActor := os.Getenv("GITHUB_ACTOR"); envActor != "" {
		return envActor, repoName, nil
	}

	if c.Token() != "" {
		userLogin, err := c.fetchAuthenticatedUser(ctx)
		if err != nil {
			return "", "", err
		}
		if userLogin != "" {
			return userLogin, repoName, nil
		}
	}

	return "", "", &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "GIT_ERR_MISSING_OWNER",
		Message: fmt.Sprintf("repository '%s' requires 'owner/repo' format when unauthenticated", repoName),
	}
}

func (c *GitHubClient) fetchAuthenticatedUser(ctx context.Context) (string, error) {
	body, err := c.doRequest(ctx, http.MethodGet, "/user")
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return "", &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_PARSE_RESPONSE",
			Message: "failed to parse GitHub user response",
			Err:     err,
		}
	}
	return u.Login, nil
}

// classifyHTTPResponse maps an HTTP status code, headers, and body to a typed Outcome and error.
func classifyHTTPResponse(resp *http.Response, body []byte) (Outcome, error) {
	// 2xx Success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return OutcomeAcknowledged, nil
	}

	// 429 Too Many Requests: Rate Limited
	if resp.StatusCode == http.StatusTooManyRequests {
		return OutcomeRateLimited, &OperatingError{
			Outcome: OutcomeRateLimited,
			Code:    "GIT_ERR_RATE_LIMITED",
			Message: fmt.Sprintf("GitHub API rate limit exceeded (HTTP %d)", resp.StatusCode),
		}
	}

	// 403 Forbidden: check for primary or secondary rate limits or abuse detection
	if resp.StatusCode == http.StatusForbidden {
		hasRetryAfter := resp.Header.Get("Retry-After") != ""
		isZeroRemaining := resp.Header.Get("X-RateLimit-Remaining") == "0"
		bodyLower := strings.ToLower(string(body))
		isAbuseOrRateLimit := strings.Contains(bodyLower, "rate limit") || strings.Contains(bodyLower, "abuse")

		if hasRetryAfter || isZeroRemaining || isAbuseOrRateLimit {
			return OutcomeRateLimited, &OperatingError{
				Outcome: OutcomeRateLimited,
				Code:    "GIT_ERR_RATE_LIMITED",
				Message: fmt.Sprintf("GitHub API rate limit or abuse detection triggered (HTTP %d)", resp.StatusCode),
			}
		}

		return OutcomeTerminal, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_FORBIDDEN",
			Message: fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode),
		}
	}

	// 5xx Server Errors: Retryable
	if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		return OutcomeRetryable, fmt.Errorf("HTTP server error %d", resp.StatusCode)
	}

	// 3xx Redirects: Terminal (redirects disabled per TigerStyle)
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return OutcomeTerminal, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_REDIRECT_NOT_ALLOWED",
			Message: fmt.Sprintf("HTTP redirect %d not followed", resp.StatusCode),
		}
	}

	// Other 4xx Client Errors: Terminal
	code := "GIT_ERR_HTTP_STATUS"
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		code = "GIT_ERR_UNAUTHORIZED"
	case http.StatusNotFound:
		code = "GIT_ERR_NOT_FOUND"
	}

	return OutcomeTerminal, &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    code,
		Message: fmt.Sprintf("GitHub API returned HTTP %d", resp.StatusCode),
	}
}

// readBoundedBody reads an HTTP response body up to MaxResponseSizeBytes + 1.
func readBoundedBody(body io.ReadCloser) ([]byte, error) {
	defer func() { _ = body.Close() }()

	limited := io.LimitReader(body, MaxResponseSizeBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeRetryable,
			Code:    "GIT_ERR_READ_BODY",
			Message: "failed to read response body",
			Err:     err,
		}
	}
	if int64(len(data)) > MaxResponseSizeBytes {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_RESPONSE_TOO_LARGE",
			Message: fmt.Sprintf("response body exceeded limit of %d bytes", MaxResponseSizeBytes),
		}
	}
	return data, nil
}

func (c *GitHubClient) doRequest(ctx context.Context, method, endpoint string) ([]byte, error) {
	Assert(c != nil, AssertClientNil)
	Assert(ctx != nil, AssertContextNil)

	if err := ctx.Err(); err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_CONTEXT_CANCELLED",
			Message: "context already cancelled",
			Err:     err,
		}
	}

	c.mu.Lock()
	baseURL := c.baseURL
	token := c.token
	timeout := c.timeout
	c.mu.Unlock()

	fullURL := baseURL + "/" + strings.TrimLeft(endpoint, "/")

	var lastErr error
	for attempt := 0; attempt <= MaxRetriesCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, &OperatingError{
					Outcome: OutcomeTerminal,
					Code:    "GIT_ERR_CONTEXT_CANCELLED",
					Message: "context cancelled during retry backoff",
					Err:     ctx.Err(),
				}
			case <-time.After(RetryBackoffDuration):
			}
		}

		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(attemptCtx, method, fullURL, nil)
		if err != nil {
			cancel()
			return nil, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "GIT_ERR_INVALID_REQUEST",
				Message: "failed to construct HTTP request",
				Err:     err,
			}
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "Personal-Pilot-Timesheet/1.0")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			if ctx.Err() != nil {
				return nil, &OperatingError{
					Outcome: OutcomeTerminal,
					Code:    "GIT_ERR_CONTEXT_CANCELLED",
					Message: "context cancelled during request",
					Err:     ctx.Err(),
				}
			}
			continue
		}

		body, readErr := readBoundedBody(resp.Body)
		cancel()

		if readErr != nil {
			var opErr *OperatingError
			if errors.As(readErr, &opErr) && opErr.Outcome == OutcomeTerminal {
				return nil, readErr
			}
			lastErr = readErr
			continue
		}

		outcome, classErr := classifyHTTPResponse(resp, body)
		switch outcome {
		case OutcomeAcknowledged:
			return body, nil
		case OutcomeRateLimited, OutcomeTerminal:
			return nil, classErr
		case OutcomeRetryable:
			lastErr = classErr
			continue
		}
	}

	return nil, &OperatingError{
		Outcome: OutcomeRetryable,
		Code:    "GIT_ERR_SERVER_ERROR",
		Message: "GitHub API request failed after retry",
		Err:     lastErr,
	}
}

type ghCommitUser struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Date  time.Time `json:"date"`
}

type ghCommitItem struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author    ghCommitUser `json:"author"`
		Committer ghCommitUser `json:"committer"`
		Message   string       `json:"message"`
	} `json:"commit"`
}

func (c *GitHubClient) fetchCommits(
	ctx context.Context,
	owner, repoName, repoOrig string,
	author string,
	sinceStr, untilStr string,
	loc *time.Location,
) ([]GitHubCommit, error) {
	if author == "" {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_MISSING_AUTHOR",
			Message: "author must be specified to prevent fetching entire repository commits",
		}
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/commits?since=%s&until=%s&author=%s&per_page=100",
		url.PathEscape(owner),
		url.PathEscape(repoName),
		url.QueryEscape(sinceStr),
		url.QueryEscape(untilStr),
		url.QueryEscape(author),
	)

	body, err := c.doRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, err
	}

	var rawCommits []ghCommitItem
	if err := json.Unmarshal(body, &rawCommits); err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_PARSE_RESPONSE",
			Message: "failed to parse GitHub commits response",
			Err:     err,
		}
	}

	commits := make([]GitHubCommit, 0, len(rawCommits))
	for i, item := range rawCommits {
		if i >= MaxCommitsCount {
			break
		}
		ts := item.Commit.Author.Date
		if ts.IsZero() {
			ts = item.Commit.Committer.Date
		}
		if loc != nil && !ts.IsZero() {
			ts = ts.In(loc)
		}
		commits = append(commits, GitHubCommit{
			SHA:       item.SHA,
			Message:   item.Commit.Message,
			Timestamp: ts,
			Repo:      repoOrig,
		})
	}
	return commits, nil
}

type ghPRHeadBase struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

type ghPRItem struct {
	Number    int          `json:"number"`
	Title     string       `json:"title"`
	State     string       `json:"state"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	ClosedAt  *time.Time   `json:"closed_at"`
	MergedAt  *time.Time   `json:"merged_at"`
	Head      ghPRHeadBase `json:"head"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type ghPRCommitItem struct {
	SHA string `json:"sha"`
}

func (c *GitHubClient) fetchPRCommits(
	ctx context.Context,
	owner, repoName string,
	prNumber int,
) ([]string, error) {
	endpoint := fmt.Sprintf("/repos/%s/%s/pulls/%d/commits?per_page=100",
		url.PathEscape(owner),
		url.PathEscape(repoName),
		prNumber,
	)

	body, err := c.doRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		var opErr *OperatingError
		if errors.As(err, &opErr) && opErr.Code == "GIT_ERR_NOT_FOUND" {
			// Sub-resource 404: fallback gracefully to empty
			return []string{}, nil
		}
		return nil, err
	}

	var rawCommits []ghPRCommitItem
	if err := json.Unmarshal(body, &rawCommits); err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_PARSE_RESPONSE",
			Message: "failed to parse GitHub PR commits response",
			Err:     err,
		}
	}

	shas := make([]string, 0, len(rawCommits))
	for i, item := range rawCommits {
		if i >= MaxPRCommitsCount {
			break
		}
		if item.SHA != "" {
			shas = append(shas, item.SHA)
		}
	}
	return shas, nil
}

func (c *GitHubClient) fetchPRs(
	ctx context.Context,
	owner, repoName, repoOrig string,
	author string,
	startOfDay, endOfDay time.Time,
	commits []GitHubCommit,
) ([]GitHubPR, error) {
	if author == "" {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_MISSING_AUTHOR",
			Message: "author must be specified to prevent fetching entire repository PRs",
		}
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/pulls?state=all&sort=updated&direction=desc&per_page=100",
		url.PathEscape(owner),
		url.PathEscape(repoName),
	)

	body, err := c.doRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, err
	}

	var rawPRs []ghPRItem
	if err := json.Unmarshal(body, &rawPRs); err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_PARSE_RESPONSE",
			Message: "failed to parse GitHub PRs response",
			Err:     err,
		}
	}

	commitSHAs := make(map[string]bool, len(commits))
	commitMessages := make([]string, 0, len(commits))
	for _, commit := range commits {
		if commit.SHA != "" {
			commitSHAs[commit.SHA] = true
		}
		if commit.Message != "" {
			commitMessages = append(commitMessages, commit.Message)
		}
	}

	prs := make([]GitHubPR, 0)
	commitLookupsCount := 0

	for _, item := range rawPRs {
		if len(prs) >= MaxPRsCount {
			break
		}

		isUserPR := strings.EqualFold(item.User.Login, author)
		hasHeadCommitMatch := commitSHAs[item.Head.SHA]

		// Check if any day commit message references this PR number (#42) or branch
		hasCommitRefMatch := false
		prRef := fmt.Sprintf("#%d", item.Number)
		for _, msg := range commitMessages {
			if strings.Contains(msg, prRef) || (item.Head.Ref != "" && strings.Contains(msg, item.Head.Ref)) {
				hasCommitRefMatch = true
				break
			}
		}

		isActiveToday := isTimeWithin(item.CreatedAt, startOfDay, endOfDay) ||
			isTimeWithin(item.UpdatedAt, startOfDay, endOfDay) ||
			(item.MergedAt != nil && isTimeWithin(*item.MergedAt, startOfDay, endOfDay)) ||
			(item.ClosedAt != nil && isTimeWithin(*item.ClosedAt, startOfDay, endOfDay))

		// Only consider PRs that either:
		// 1) Match a day commit (head SHA or commit ref/branch), OR
		// 2) Are authored by the user and were active today
		if !hasHeadCommitMatch && !hasCommitRefMatch && !(isUserPR && isActiveToday) {
			continue
		}

		var prCommits []string
		// Bounded commit lookup: at most MaxPRCommitLookupsCount per day
		if commitLookupsCount < MaxPRCommitLookupsCount {
			commitLookupsCount++
			var err error
			prCommits, err = c.fetchPRCommits(ctx, owner, repoName, item.Number)
			if err != nil {
				return nil, err
			}
		}

		// If commit lookup was skipped or returned empty, populate with head SHA if present
		if len(prCommits) == 0 && item.Head.SHA != "" {
			prCommits = []string{item.Head.SHA}
		}

		prs = append(prs, GitHubPR{
			Number:  item.Number,
			Title:   item.Title,
			Branch:  item.Head.Ref,
			Repo:    repoOrig,
			Commits: prCommits,
		})
	}

	return prs, nil
}

func isTimeWithin(t, start, end time.Time) bool {
	if t.IsZero() {
		return false
	}
	return (t.Equal(start) || t.After(start)) && (t.Equal(end) || t.Before(end))
}

// FetchDayActivity retrieves commits and pull requests authored or modified on target date for the specified repository.
func (c *GitHubClient) FetchDayActivity(ctx context.Context, repo string, date time.Time) ([]GitHubCommit, []GitHubPR, error) {
	Assert(c != nil, AssertClientNil)
	Assert(ctx != nil, AssertContextNil)

	if date.IsZero() {
		return nil, nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "GIT_ERR_INVALID_DATE",
			Message: "target date cannot be zero",
		}
	}

	owner, repoName, err := c.resolveRepo(ctx, repo)
	if err != nil {
		return nil, nil, err
	}

	// Resolve author: must be configured or resolved from authenticated user
	author := c.Author()
	if author == "" {
		if c.Token() != "" {
			userLogin, err := c.fetchAuthenticatedUser(ctx)
			if err != nil {
				return nil, nil, err
			}
			author = userLogin
		} else {
			return nil, nil, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "GIT_ERR_MISSING_AUTHOR",
				Message: "author must be specified when unauthenticated to prevent fetching entire repository activity",
			}
		}
	}

	loc := date.Location()
	if loc == nil {
		loc = time.UTC
	}
	startOfDay := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	endOfDay := time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 999999999, loc)

	sinceStr := startOfDay.UTC().Format(time.RFC3339)
	untilStr := endOfDay.UTC().Format(time.RFC3339)

	commits, err := c.fetchCommits(ctx, owner, repoName, repo, author, sinceStr, untilStr, loc)
	if err != nil {
		return nil, nil, err
	}

	prs, err := c.fetchPRs(ctx, owner, repoName, repo, author, startOfDay, endOfDay, commits)
	if err != nil {
		return nil, nil, err
	}

	return commits, prs, nil
}
