package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestNewGitHubClient(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

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
	t.Setenv("GH_TOKEN", "gh-env-token")
	if tok := ResolveToken("explicit-token"); tok != "explicit-token" {
		t.Fatalf("expected explicit-token, got %s", tok)
	}

	// GITHUB_TOKEN environment variable fallback
	if tok := ResolveToken(""); tok != "env-token" {
		t.Fatalf("expected env-token, got %s", tok)
	}

	// GH_TOKEN fallback when GITHUB_TOKEN is unset
	t.Setenv("GITHUB_TOKEN", "")
	if tok := ResolveToken(""); tok != "gh-env-token" {
		t.Fatalf("expected gh-env-token, got %s", tok)
	}

	// Empty when both envs are unset
	t.Setenv("GH_TOKEN", "")
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
		{"https://github.com/owner/repo/pull/12", "owner", "repo", false},
		{"https://github.com/owner/repo/tree/feat/branch", "owner", "repo", false},
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

	// 2. Unauthenticated slug without owner -> terminal error
	t.Setenv("GITHUB_USER", "")
	t.Setenv("GITHUB_ACTOR", "")
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

	// 6. Authenticated via /user returning 401 Unauthorized propagates error directly
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer tsErr.Close()

	cAuthErr := NewGitHubClient("bad-token", 5*time.Second)
	cAuthErr.SetBaseURL(tsErr.URL)
	_, _, err = cAuthErr.resolveRepo(ctx, "api-repo")
	if err == nil {
		t.Fatal("expected error propagating from /user 401, got nil")
	}
	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_UNAUTHORIZED" {
		t.Fatalf("expected propagated GIT_ERR_UNAUTHORIZED, got: %#v", err)
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

func TestFetchDayActivity_Unauthenticated_WithAuthor(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	var receivedAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	client := NewGitHubClient("", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("unauth-user")

	commits, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("FetchDayActivity unauthenticated with author failed: %v", err)
	}
	if receivedAuthHeader != "" {
		t.Errorf("expected no Authorization header, got: %s", receivedAuthHeader)
	}
	if len(commits) != 0 || len(prs) != 0 {
		t.Errorf("expected empty commits and prs, got %d and %d", len(commits), len(prs))
	}
}

func TestFetchDayActivity_MissingAuthor_Unauthenticated(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	client := NewGitHubClient("", 5*time.Second)
	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected error for unauthenticated call without author, got nil")
	}
	if !IsTerminal(err) {
		t.Fatalf("expected IsTerminal true, got: %v", err)
	}
	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_MISSING_AUTHOR" {
		t.Fatalf("expected GIT_ERR_MISSING_AUTHOR, got: %#v", err)
	}
}

func TestFetchDayActivity_AutoResolveAuthor_Authenticated(t *testing.T) {
	var requestedUserEndpoint bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user" {
			requestedUserEndpoint = true
			_, _ = w.Write([]byte(`{"login":"auto-user"}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits") {
			if author := r.URL.Query().Get("author"); author != "auto-user" {
				t.Errorf("expected author=auto-user query param, got %s", author)
			}
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/repos/owner/repo/pulls") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := NewGitHubClient("auth-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	// Author deliberately unset: should resolve via /user

	commits, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !requestedUserEndpoint {
		t.Fatal("expected /user endpoint to be requested to resolve author")
	}
	if len(commits) != 0 || len(prs) != 0 {
		t.Errorf("expected empty slices, got %d commits, %d prs", len(commits), len(prs))
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
	client.SetAuthor("test-author")

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
	client.SetAuthor("test-author")

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

func TestFetchDayActivity_RateLimited_403_RetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "rate limit exceeded"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected rate limit error for 403 with Retry-After, got nil")
	}
	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited true for 403 with Retry-After, got: %v", err)
	}
}

func TestFetchDayActivity_RateLimited_403_BodyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "You have triggered a secondary rate limit. Please wait."}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected rate limit error for 403 secondary rate limit body, got nil")
	}
	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited true for secondary rate limit body, got: %v", err)
	}
}

func TestFetchDayActivity_RateLimited_403_AbuseDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "abuse detection mechanism triggered"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected rate limit error for 403 abuse detection body, got nil")
	}
	if !IsRateLimited(err) {
		t.Errorf("expected IsRateLimited true for abuse detection, got: %v", err)
	}
}

func TestFetchDayActivity_403_Forbidden_Regular(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message": "Resource not accessible by integration"}`))
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected error for standard 403 forbidden, got nil")
	}
	if IsRateLimited(err) {
		t.Errorf("expected IsRateLimited false for standard 403 forbidden, got true")
	}
	if !IsTerminal(err) {
		t.Errorf("expected IsTerminal true for standard 403 forbidden, got false")
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
	client.SetAuthor("test-author")

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
	client.SetAuthor("test-author")

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
			client.SetAuthor("test-author")

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

func TestFetchDayActivity_RedirectTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/moved-target", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, _, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err == nil {
		t.Fatal("expected error on redirect, got nil")
	}
	if !IsTerminal(err) {
		t.Fatalf("expected IsTerminal true for redirect, got: %v", err)
	}
	var opErr *OperatingError
	if !errors.As(err, &opErr) || opErr.Code != "GIT_ERR_REDIRECT_NOT_ALLOWED" {
		t.Fatalf("expected GIT_ERR_REDIRECT_NOT_ALLOWED, got: %#v", err)
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
	client.SetAuthor("test-author")

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
	client.SetAuthor("test-author")

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
	client.SetAuthor("test-author")

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
					User: struct {
						Login string `json:"login"`
					}{Login: "test-author"},
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
	client.SetAuthor("test-author")

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

func TestFetchDayActivity_PRCommitLookupBounded(t *testing.T) {
	var prCommitsCalls int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/owner/repo/pulls":
			// Generate 10 active PRs authored by user
			pulls := make([]ghPRItem, 10)
			for i := 0; i < 10; i++ {
				pulls[i] = ghPRItem{
					Number:    i + 1,
					Title:     fmt.Sprintf("PR %d", i+1),
					CreatedAt: time.Now(),
					Head: ghPRHeadBase{
						Ref: fmt.Sprintf("branch-%d", i+1),
						SHA: fmt.Sprintf("sha-%d", i+1),
					},
				}
				pulls[i].User.Login = "test-author"
			}
			data, _ := json.Marshal(pulls)
			_, _ = w.Write(data)
		case strings.Contains(r.URL.Path, "/commits"):
			atomic.AddInt32(&prCommitsCalls, 1)
			_, _ = w.Write([]byte(`[{"sha":"commit-sha"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(prs) != 10 {
		t.Fatalf("expected 10 PRs, got %d", len(prs))
	}

	// Verify PR commits calls are capped at MaxPRCommitLookupsCount (5)
	actualCalls := atomic.LoadInt32(&prCommitsCalls)
	if actualCalls > int32(MaxPRCommitLookupsCount) {
		t.Fatalf("PR commit lookups exceeded bound: expected <= %d, got %d", MaxPRCommitLookupsCount, actualCalls)
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

func TestOperatingError_URLSanitization(t *testing.T) {
	urlErr := &url.Error{
		Op:  "Get",
		URL: "https://api.github.com/repos/owner/repo/commits?author=huyle&token=super_secret_token_12345",
		Err: errors.New("connection reset by peer"),
	}

	opErr := &OperatingError{
		Outcome: OutcomeRetryable,
		Code:    "GIT_ERR_NETWORK",
		Message: "network failure",
		Err:     urlErr,
	}

	errStr := opErr.Error()
	if strings.Contains(errStr, "super_secret_token_12345") {
		t.Fatalf("OperatingError.Error() leaked secret token from URL: %s", errStr)
	}
	if strings.Contains(errStr, "author=huyle") {
		t.Fatalf("OperatingError.Error() leaked query parameters from URL: %s", errStr)
	}
	if !strings.Contains(errStr, "https://api.github.com/repos/owner/repo/commits") {
		t.Fatalf("OperatingError.Error() should retain sanitized base path: %s", errStr)
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
	client.SetAuthor("test-author")

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
	client.SetAuthor("test-author")

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

// GIT-DATE-WINDOW-1: the remote day window is anchored to date.Location(); the
// `since`/`until` query strings are the pinned UTC instants (since over-fetches
// 1s below the local midnight) and the client-side filter is half-open.

// GIT-DATE-WINDOW-1: assert the exact since/until query strings for HCM (+07)
// and New_York (-04) local days.
func TestFetchDayActivity_WindowSinceUntilPinned(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}

	cases := []struct {
		name      string
		day       time.Time
		wantSince string
		wantUntil string
	}{
		{
			"Asia/Ho_Chi_Minh +07",
			time.Date(2026, 10, 6, 0, 0, 0, 0, hcm),
			"2026-10-05T16:59:59Z", // start 2026-10-05T17:00:00Z minus 1s
			"2026-10-06T17:00:00Z", // next local midnight 2026-10-07T00:00+07
		},
		{
			"America/New_York -04",
			time.Date(2026, 10, 6, 0, 0, 0, 0, ny),
			"2026-10-06T03:59:59Z", // start 2026-10-06T04:00:00Z minus 1s
			"2026-10-07T04:00:00Z", // next local midnight 2026-10-07T00:00-04
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var since, until string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits"):
					since = r.URL.Query().Get("since")
					until = r.URL.Query().Get("until")
					_, _ = w.Write([]byte(`[]`))
				case r.URL.Path == "/repos/owner/repo/pulls":
					_, _ = w.Write([]byte(`[]`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()

			client := NewGitHubClient("test-token", 5*time.Second)
			client.SetBaseURL(srv.URL)
			client.SetAuthor("test-author")

			if _, _, err := client.FetchDayActivity(context.Background(), "owner/repo", tc.day); err != nil {
				t.Fatalf("FetchDayActivity: %v", err)
			}
			if since != tc.wantSince {
				t.Errorf("since = %q, want %q", since, tc.wantSince)
			}
			if until != tc.wantUntil {
				t.Errorf("until = %q, want %q", until, tc.wantUntil)
			}
		})
	}
}

// GIT-DATE-WINDOW-1: PR activity is filtered half-open [start, end): updated_at
// exactly at start is INCLUDED, exactly at end is EXCLUDED, at end-1s INCLUDED.
func TestFetchDayActivity_PRWindowHalfOpen(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, hcm)
	start := day
	end := time.Date(2026, 10, 7, 0, 0, 0, 0, hcm)
	far := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	pull := func(n int, updated time.Time) ghPRItem {
		var p ghPRItem
		p.Number = n
		p.Title = fmt.Sprintf("PR %d", n)
		p.State = "open"
		p.CreatedAt = far // outside the window, so only UpdatedAt decides activity
		p.UpdatedAt = updated
		p.Head.Ref = fmt.Sprintf("branch-%d", n)
		p.User.Login = "test-author"
		return p
	}
	pulls := []ghPRItem{
		pull(1, start),                 // exactly start -> included
		pull(2, end),                   // exactly end   -> excluded
		pull(3, end.Add(-time.Second)), // end-1s        -> included
	}
	pullsJSON, err := json.Marshal(pulls)
	if err != nil {
		t.Fatalf("marshal pulls: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/owner/repo/pulls":
			_, _ = w.Write(pullsJSON)
		case strings.Contains(r.URL.Path, "/commits"):
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	_, prs, err := client.FetchDayActivity(context.Background(), "owner/repo", day)
	if err != nil {
		t.Fatalf("FetchDayActivity: %v", err)
	}
	got := map[int]bool{}
	for _, p := range prs {
		got[p.Number] = true
	}
	if !got[1] {
		t.Errorf("PR updated exactly at start must be included")
	}
	if !got[3] {
		t.Errorf("PR updated at end-1s must be included")
	}
	if got[2] {
		t.Errorf("PR updated exactly at end must be excluded")
	}
	if len(prs) != 2 {
		t.Errorf("expected exactly 2 PRs, got %d: %v", len(prs), got)
	}
}

// GIT-DATE-WINDOW-1: the client-side commit filter in fetchCommits is HALF-OPEN
// [start, end): a commit exactly at start is INCLUDED, exactly at end EXCLUDED,
// and start-1s / end-1s land on the correct side. The mocked /commits response
// returns all four regardless of the since/until query, so only the client-side
// filter decides.
func TestFetchDayActivity_CommitWindowHalfOpen(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	day := time.Date(2026, 10, 6, 0, 0, 0, 0, hcm)
	start := day
	end := time.Date(2026, 10, 7, 0, 0, 0, 0, hcm)

	mk := func(sha string, ts time.Time) ghCommitItem {
		var c ghCommitItem
		c.SHA = sha
		c.Commit.Author = ghCommitUser{Name: "Test", Email: "test@example.com", Date: ts}
		c.Commit.Message = "commit " + sha
		return c
	}
	commitsJSON, err := json.Marshal([]ghCommitItem{
		mk("before-start", start.Add(-time.Second)), // excluded (start-1s)
		mk("at-start", start),                       // included (start inclusive)
		mk("before-end", end.Add(-time.Second)),     // included (end-1s)
		mk("at-end", end),                           // excluded (end exclusive)
	})
	if err != nil {
		t.Fatalf("marshal commits: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/owner/repo/commits"):
			_, _ = w.Write(commitsJSON)
		case r.URL.Path == "/repos/owner/repo/pulls":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewGitHubClient("test-token", 5*time.Second)
	client.SetBaseURL(srv.URL)
	client.SetAuthor("test-author")

	commits, _, err := client.FetchDayActivity(context.Background(), "owner/repo", day)
	if err != nil {
		t.Fatalf("FetchDayActivity: %v", err)
	}
	got := map[string]bool{}
	for _, c := range commits {
		got[c.SHA] = true
	}
	if !got["at-start"] {
		t.Errorf("commit exactly at start must be INCLUDED")
	}
	if !got["before-end"] {
		t.Errorf("commit at end-1s must be INCLUDED")
	}
	if got["before-start"] {
		t.Errorf("commit at start-1s must be EXCLUDED")
	}
	if got["at-end"] {
		t.Errorf("commit exactly at end must be EXCLUDED")
	}
	if len(commits) != 2 {
		t.Errorf("expected exactly 2 commits, got %d: %v", len(commits), got)
	}
}
