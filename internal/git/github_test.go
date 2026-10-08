package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewGitHubClient(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("SKIP_GH_AUTH", "1")

	// Default timeout
	c1 := NewGitHubClient("", 0)
	if c1.timeout != HTTPRequestTimeoutSeconds {
		t.Fatalf("expected timeout %v, got %v", HTTPRequestTimeoutSeconds, c1.timeout)
	}
	if c1.BaseURL() != DefaultGitHubBaseURL {
		t.Fatalf("expected baseURL %s, got %s", DefaultGitHubBaseURL, c1.BaseURL())
	}
	if c1.Token() != "" {
		t.Fatalf("expected empty token, got %s", c1.Token())
	}

	// Custom timeout & token
	c2 := NewGitHubClient("my-token", 2*time.Second)
	if c2.timeout != 2*time.Second {
		t.Fatalf("expected timeout 2s, got %v", c2.timeout)
	}
	if c2.Token() != "my-token" {
		t.Fatalf("expected token 'my-token', got %s", c2.Token())
	}
}

func TestSettersAndGetters(t *testing.T) {
	c := NewGitHubClient("init-token", 5*time.Second)
	c.SetBaseURL("http://example.com/api/")
	if c.BaseURL() != "http://example.com/api" {
		t.Fatalf("expected trimmed baseURL, got %s", c.BaseURL())
	}

	c.SetAuthor("monalisa")
	if c.Author() != "monalisa" {
		t.Fatalf("expected author monalisa, got %s", c.Author())
	}

	c.SetToken("new-token")
	if c.Token() != "new-token" {
		t.Fatalf("expected new-token, got %s", c.Token())
	}
}

func TestResolveToken(t *testing.T) {
	// Explicit argument has highest precedence
	t.Setenv("GITHUB_TOKEN", "env-token")
	if tok := ResolveToken("explicit-token"); tok != "explicit-token" {
		t.Fatalf("expected explicit-token, got %s", tok)
	}

	// Environment variable fallback
	if tok := ResolveToken(""); tok != "env-token" {
		t.Fatalf("expected env-token, got %s", tok)
	}

	// Empty when env unset and SKIP_GH_AUTH=1
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("SKIP_GH_AUTH", "1")
	if tok := ResolveToken(""); tok != "" {
		t.Fatalf("expected empty token, got %s", tok)
	}
}

func TestParseRepoSlug(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"owner/repo", "owner", "repo", false},
		{"owner/repo.git", "owner", "repo", false},
		{"owner/repo/", "owner", "repo", false},
		{"https://github.com/owner/repo", "owner", "repo", false},
		{"https://github.com/owner/repo.git", "owner", "repo", false},
		{"git@github.com:owner/repo.git", "owner", "repo", false},
		{"just-repo", "", "just-repo", false},
		{"", "", "", true},
		{"   ", "", "", true},
		{"///", "", "", true},
	}

	for _, tt := range tests {
		owner, repo, err := ParseRepoSlug(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseRepoSlug(%q) unexpected err status: %v", tt.input, err)
			continue
		}
		if !tt.wantErr {
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Errorf("ParseRepoSlug(%q) = (%q, %q), want (%q, %q)", tt.input, owner, repo, tt.wantOwner, tt.wantRepo)
			}
		}
	}
}

func TestResolveRepo(t *testing.T) {
	ctx := context.Background()

	// 1. Explicit owner/repo
	c := NewGitHubClient("", 5*time.Second)
	owner, repo, err := c.resolveRepo(ctx, "octocat/Hello-World")
	if err != nil || owner != "octocat" || repo != "Hello-World" {
		t.Fatalf("failed resolving explicit repo: %v, owner=%s, repo=%s", err, owner, repo)
	}

	// 2. Unauthenticated slug without owner -> error
	t.Setenv("GITHUB_USER", "")
	t.Setenv("GITHUB_ACTOR", "")
	t.Setenv("SKIP_GH_AUTH", "1")
	cUnauth := NewGitHubClient("", 5*time.Second)
	_, _, err = cUnauth.resolveRepo(ctx, "my-repo")
	if err == nil || !IsTerminal(err) {
		t.Fatalf("expected terminal error for missing owner without auth, got: %v", err)
	}

	// 3. With author configured
	cUnauth.SetAuthor("author-user")
	owner, repo, err = cUnauth.resolveRepo(ctx, "my-repo")
	if err != nil || owner != "author-user" || repo != "my-repo" {
		t.Fatalf("failed resolving repo via author: %v, owner=%s, repo=%s", err, owner, repo)
	}

	// 4. With GITHUB_USER env
	cNoAuthor := NewGitHubClient("", 5*time.Second)
	t.Setenv("GITHUB_USER", "env-octocat")
	owner, repo, err = cNoAuthor.resolveRepo(ctx, "my-repo")
	if err != nil || owner != "env-octocat" || repo != "my-repo" {
		t.Fatalf("failed resolving repo via GITHUB_USER: %v, owner=%s, repo=%s", err, owner, repo)
	}

	// 5. Authenticated via /user
	t.Setenv("GITHUB_USER", "")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"login":"api-user"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cAuth := NewGitHubClient("dummy-token", 5*time.Second)
	cAuth.SetBaseURL(ts.URL)
	owner, repo, err = cAuth.resolveRepo(ctx, "api-repo")
	if err != nil || owner != "api-user" || repo != "api-repo" {
		t.Fatalf("failed resolving repo via /user: %v, owner=%s, repo=%s", err, owner, repo)
	}
}

func TestFetchDayActivity_Success(t *testing.T) {
	loc := time.FixedZone("ICT", 7*3600)
	targetDate := time.Date(2026, 10, 7, 10, 30, 0, 0, loc)

	var receivedAuthHeader string
	var receivedUserAgent string
	var receivedAccept string
	var receivedSince string
	var receivedUntil string
	var receivedAuthor string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		receivedUserAgent = r.Header.Get("User-Agent")
		receivedAccept = r.Header.Get("Accept")

		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/huyle/assignment-1/commits"):
			receivedSince = r.URL.Query().Get("since")
			receivedUntil = r.URL.Query().Get("until")
			receivedAuthor = r.URL.Query().Get("author")

			w.Header().Set("Content-Type", "application/json")
			commitsJSON := `[
				{
					"sha": "a1b2c3d4e5f67890",
					"commit": {
						"author": {
							"name": "Huy Le",
							"email": "huy@gradion.com",
							"date": "2026-10-07T03:00:00Z"
						},
						"message": "feat: implement github rest api client"
					}
				},
				{
					"sha": "e4f5g6h7i8j90123",
					"commit": {
						"author": {
							"name": "Huy Le",
							"email": "huy@gradion.com",
							"date": "2026-10-07T04:30:00Z"
						},
						"message": "test: add httptest mocks for github api"
					}
				}
			]`
			_, _ = w.Write([]byte(commitsJSON))

		case r.URL.Path == "/repos/huyle/assignment-1/pulls":
			w.Header().Set("Content-Type", "application/json")
			pullsJSON := `[
				{
					"number": 42,
					"title": "feat: two-tier git collector",
					"state": "open",
					"created_at": "2026-10-07T02:00:00Z",
					"updated_at": "2026-10-07T05:00:00Z",
					"head": {
						"ref": "feat/github-api-client",
						"sha": "a1b2c3d4e5f67890"
					},
					"user": {
						"login": "huyle"
					}
				}
			]`
			_, _ = w.Write([]byte(pullsJSON))

		case r.URL.Path == "/repos/huyle/assignment-1/pulls/42/commits":
			w.Header().Set("Content-Type", "application/json")
			prCommitsJSON := `[
				{"sha": "a1b2c3d4e5f67890"},
				{"sha": "e4f5g6h7i8j90123"}
			]`
			_, _ = w.Write([]byte(prCommitsJSON))

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-secret-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("huyle")

	commits, prs, err := client.FetchDayActivity(context.Background(), "huyle/assignment-1", targetDate)
	if err != nil {
		t.Fatalf("FetchDayActivity failed unexpectedly: %v", err)
	}

	// Verify headers
	if receivedAuthHeader != "Bearer test-secret-token" {
		t.Errorf("expected Bearer token, got %q", receivedAuthHeader)
	}
	if receivedUserAgent != "Personal-Pilot-Timesheet/1.0" {
		t.Errorf("unexpected User-Agent: %q", receivedUserAgent)
	}
	if receivedAccept != "application/vnd.github+json" {
		t.Errorf("unexpected Accept header: %q", receivedAccept)
	}

	// Verify query params
	if receivedAuthor != "huyle" {
		t.Errorf("expected author huyle, got %q", receivedAuthor)
	}
	if receivedSince == "" || receivedUntil == "" {
		t.Errorf("expected non-empty since and until query parameters")
	}

	// Verify Commits
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].SHA != "a1b2c3d4e5f67890" || commits[0].Message != "feat: implement github rest api client" {
		t.Errorf("unexpected commit 0: %+v", commits[0])
	}
	if commits[0].Repo != "huyle/assignment-1" {
		t.Errorf("expected repo huyle/assignment-1, got %s", commits[0].Repo)
	}
	if commits[0].Timestamp.Location().String() != loc.String() {
		t.Errorf("expected commit timestamp in location %v, got %v", loc, commits[0].Timestamp.Location())
	}

	// Verify PRs
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	pr := prs[0]
	if pr.Number != 42 || pr.Title != "feat: two-tier git collector" || pr.Branch != "feat/github-api-client" {
		t.Errorf("unexpected PR fields: %+v", pr)
	}
	if len(pr.Commits) != 2 || pr.Commits[0] != "a1b2c3d4e5f67890" || pr.Commits[1] != "e4f5g6h7i8j90123" {
		t.Errorf("unexpected PR commits: %v", pr.Commits)
	}
}

func TestFetchDayActivity_Unauthenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("SKIP_GH_AUTH", "1")

	var receivedAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := NewGitHubClient("", 5*time.Second)
	client.SetBaseURL(srv.URL)

	commits, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("FetchDayActivity unauthenticated failed: %v", err)
	}
	if receivedAuthHeader != "" {
		t.Errorf("expected no Authorization header, got: %s", receivedAuthHeader)
	}
	if len(commits) != 0 || len(prs) != 0 {
		t.Errorf("expected empty commits and prs, got %d and %d", len(commits), len(prs))
	}
}

func TestFetchDayActivity_RateLimited_429(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message": "API rate limit exceeded"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}

	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited to be true for 429, got false. err: %v", err)
	}

	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Outcome != OutcomeRateLimited || opErr.Code != "GIT_ERR_RATE_LIMITED" {
		t.Errorf("unexpected error structure: %#v", err)
	}

	// TigerStyle check: rate limits should NOT be retried
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected exactly 1 request without retries for rate limit, got %d", requestCount)
	}
}

func TestFetchDayActivity_RateLimited_403_Header(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "API rate limit exceeded"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected rate limit error, got nil")
	}

	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited true for 403 with 0 remaining, got false. err: %v", err)
	}
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected exactly 1 request without retries, got %d", requestCount)
	}
}

func TestFetchDayActivity_5xx_RetrySuccess(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		if count == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	commits, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if len(commits) != 0 || len(prs) != 0 {
		t.Errorf("expected empty slices, got %d commits, %d prs", len(commits), len(prs))
	}
	// Verify 1 initial request + 1 retry = 2 requests
	if atomic.LoadInt32(&requestCount) < 2 {
		t.Errorf("expected at least 2 requests (1 retry), got %d", requestCount)
	}
}

func TestFetchDayActivity_5xx_RetryExhausted(t *testing.T) {
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"message":"bad gateway"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected error after retry exhaustion, got nil")
	}

	if !IsRetryable(err) {
		t.Errorf("expected IsRetryable true for exhausted 5xx, got false: %v", err)
	}

	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Outcome != OutcomeRetryable || opErr.Code != "GIT_ERR_SERVER_ERROR" {
		t.Errorf("unexpected error structure: %#v", err)
	}

	// Exactly 2 attempts (initial + 1 bounded retry)
	if atomic.LoadInt32(&requestCount) != 2 {
		t.Errorf("expected exactly 2 requests for bounded 1-retry, got %d", requestCount)
	}
}

func TestFetchDayActivity_TerminalErrors(t *testing.T) {
	tests := []struct {
		statusCode int
		wantCode   string
	}{
		{http.StatusUnauthorized, "GIT_ERR_UNAUTHORIZED"},
		{http.StatusForbidden, "GIT_ERR_FORBIDDEN"},
		{http.StatusNotFound, "GIT_ERR_NOT_FOUND"},
		{http.StatusBadRequest, "GIT_ERR_HTTP_STATUS"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Status_%d", tt.statusCode), func(t *testing.T) {
			var requestCount int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requestCount, 1)
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(`{"message":"client error"}`))
			}))
			defer srv.Close()

			client := NewGitHubClient("test-token", 5*time.Second)
			client.SetBaseURL(srv.URL)

			_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
			if err == nil {
				t.Fatalf("expected error for HTTP %d, got nil", tt.statusCode)
			}
			if !IsTerminal(err) {
				t.Errorf("expected IsTerminal true for HTTP %d, got false: %v", tt.statusCode, err)
			}
			var opErr *OperatingError
			if !errors.As(err, &opErr) || opErr.Code != tt.wantCode {
				t.Errorf("expected code %s, got %#v", tt.wantCode, err)
			}
			if atomic.LoadInt32(&requestCount) != 1 {
				t.Errorf("terminal errors must not retry; requests made: %d", requestCount)
			}
		})
	}
}

func TestFetchDayActivity_ResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Write MaxResponseSizeBytes + 1024 bytes
		chunk := strings.Repeat(" ", 1024)
		for i := 0; i < int(MaxResponseSizeBytes/1024)+2; i++ {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected capacity error for response too large, got nil")
	}

	if !IsTerminal(err) {
		t.Errorf("expected IsTerminal true for response too large: %v", err)
	}

	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_RESPONSE_TOO_LARGE" {
		t.Errorf("expected GIT_ERR_RESPONSE_TOO_LARGE, got %#v", err)
	}
}

func TestFetchDayActivity_InvalidInputs(t *testing.T) {
	client := NewGitHubClient("test-token", 5*time.Second)

	// Zero date
	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Time{})
	if err == nil || !IsTerminal(err) {
		t.Errorf("expected terminal error for zero date, got: %v", err)
	}

	// Empty repo
	_, _, err = client.FetchDayActivity(context.Background(), "", time.Now())
	if err == nil || !IsTerminal(err) {
		t.Errorf("expected terminal error for empty repo, got: %v", err)
	}
}

func TestFetchDayActivity_ContextCancelled(t *testing.T) {
	client := NewGitHubClient("test-token", 5*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before call

	_, _, err := client.FetchDayActivity(ctx, "owner/repo", time.Now())
	if err == nil || !IsTerminal(err) {
		t.Errorf("expected terminal error for cancelled context, got: %v", err)
	}
}

func TestFetchDayActivity_PRCommits404Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/owner/repo/pulls":
			w.Header().Set("Content-Type", "application/json")
			pulls := []ghPRItem{
				{
					Number:    10,
					Title:     "feat: fallback test",
					State:     "open",
					CreatedAt: time.Now(),
					Head: ghPRHeadBase{
						Ref: "feat/fallback",
						SHA: "headsha123",
					},
				},
			}
			data, _ := json.Marshal(pulls)
			_, _ = w.Write(data)
		case strings.Contains(r.URL.Path, "/pulls/10/commits"):
			// Return 404 to test fallback to head.sha
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("expected graceful PR commit fallback, got: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if len(prs[0].Commits) != 1 || prs[0].Commits[0] != "headsha123" {
		t.Errorf("expected fallback to head SHA, got: %v", prs[0].Commits)
	}
}

func TestAssert_ProgrammerInvariant(t *testing.T) {
	// Should not panic on true
	Assert(true, "ASSERT_TEST_PASS")

	// Must panic on false
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic from Assert(false), got none")
		}
		if s, ok := r.(string); !ok || s != "ASSERT_TEST_FAIL" {
			t.Errorf("unexpected panic value: %v", r)
		}
	}()
	Assert(false, "ASSERT_TEST_FAIL")
}

func TestOutcome_String(t *testing.T) {
	if OutcomeAcknowledged.String() != "OutcomeAcknowledged" {
		t.Errorf("unexpected string: %s", OutcomeAcknowledged.String())
	}
	if OutcomeRetryable.String() != "OutcomeRetryable" {
		t.Errorf("unexpected string: %s", OutcomeRetryable.String())
	}
	if OutcomeTerminal.String() != "OutcomeTerminal" {
		t.Errorf("unexpected string: %s", OutcomeTerminal.String())
	}
	if OutcomeRateLimited.String() != "OutcomeRateLimited" {
		t.Errorf("unexpected string: %s", OutcomeRateLimited.String())
	}
	if Outcome(99).String() != "Outcome(99)" {
		t.Errorf("unexpected string: %s", Outcome(99).String())
	}
}

func TestOperatingError_Unwrap(t *testing.T) {
	underlying := fmt.Errorf("root cause")
	opErr := &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "GIT_TEST",
		Message: "test error",
		Err:     underlying,
	}

	if !errors.Is(opErr, underlying) {
		t.Errorf("expected errors.Is to match underlying error")
	}
	if !strings.Contains(opErr.Error(), "GIT_TEST") || !strings.Contains(opErr.Error(), "root cause") {
		t.Errorf("unexpected error string: %s", opErr.Error())
	}
}

func TestMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil || !IsTerminal(err) {
		t.Fatalf("expected terminal parse error for malformed json, got: %v", err)
	}
}

func TestErrorPredicates(t *testing.T) {
	if IsRateLimited(nil) || IsRetryable(nil) || IsTerminal(nil) {
		t.Error("predicates must return false for nil")
	}

	standardErr := fmt.Errorf("standard error")
	if IsRateLimited(standardErr) || IsRetryable(standardErr) || IsTerminal(standardErr) {
		t.Error("predicates must return false for standard non-operating errors")
	}

	rateErr := &OperatingError{Outcome: OutcomeRateLimited, Code: "RATE", Message: "rate limited"}
	if !IsRateLimited(rateErr) || IsRetryable(rateErr) || IsTerminal(rateErr) {
		t.Error("unexpected classification for rateErr")
	}
	if rateErr.Error() != "[RATE] rate limited" {
		t.Errorf("unexpected Error() without underlying err: %s", rateErr.Error())
	}

	retryErr := &OperatingError{Outcome: OutcomeRetryable, Code: "RETRY", Message: "retryable"}
	if IsRateLimited(retryErr) || !IsRetryable(retryErr) || IsTerminal(retryErr) {
		t.Error("unexpected classification for retryErr")
	}

	termErr := &OperatingError{Outcome: OutcomeTerminal, Code: "TERM", Message: "terminal"}
	if IsRateLimited(termErr) || IsRetryable(termErr) || !IsTerminal(termErr) {
		t.Error("unexpected classification for termErr")
	}
}

func TestFetchDayActivity_ThreePointBoundary(t *testing.T) {
	// TigerStyle 3-point boundary testing for MaxResponseSizeBytes
	// Point 1: Below bound (valid small JSON array)
	// Point 2: At bound (valid JSON padded to MaxResponseSizeBytes)
	// Point 3: Above bound (MaxResponseSizeBytes + 1 bytes)

	var currentMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch currentMode {
		case "below":
			_, _ = w.Write([]byte(`[]`))
		case "at_bound":
			padLen := int(MaxResponseSizeBytes) - 2
			_, _ = w.Write([]byte("[" + strings.Repeat(" ", padLen) + "]"))
		case "above_bound":
			padLen := int(MaxResponseSizeBytes) - 1
			_, _ = w.Write([]byte("[" + strings.Repeat(" ", padLen) + "]"))
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)

	// Point 1: Below bound
	currentMode = "below"
	commits, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil || len(commits) != 0 || len(prs) != 0 {
		t.Fatalf("Point 1 (below bound) failed: %v", err)
	}

	// Point 2: At bound
	currentMode = "at_bound"
	commits, prs, err = client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil || len(commits) != 0 || len(prs) != 0 {
		t.Fatalf("Point 2 (at bound) failed: %v", err)
	}

	// Point 3: Above bound
	currentMode = "above_bound"
	_, _, err = client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil || !IsTerminal(err) {
		t.Fatalf("Point 3 (above bound) expected terminal error, got: %v", err)
	}
	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_RESPONSE_TOO_LARGE" {
		t.Fatalf("Point 3 expected GIT_ERR_RESPONSE_TOO_LARGE, got: %#v", err)
	}
}

// TestAssertion_Subprocess verifies fatal invariant assertions terminate the process per TigerStyle.
func TestAssertion_Subprocess(t *testing.T) {
	if os.Getenv("BE_FATAL_ASSERTION_CLIENT_NIL") == "1" {
		var c *GitHubClient
		_, _, _ = c.FetchDayActivity(context.Background(), "owner/repo", time.Now())
		return
	}
	if os.Getenv("BE_FATAL_ASSERTION_CONTEXT_NIL") == "1" {
		c := NewGitHubClient("token", 5*time.Second)
		/* #nosec G104 */
		_, _, _ = c.FetchDayActivity(nil, "owner/repo", time.Now())
		return
	}

	tests := []struct {
		name    string
		envKey  string
		wantTag string
	}{
		{"ClientNil", "BE_FATAL_ASSERTION_CLIENT_NIL=1", AssertClientNil},
		{"ContextNil", "BE_FATAL_ASSERTION_CONTEXT_NIL=1", AssertContextNil},
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
