# [M1-01] Tier 1 GitHub REST API Client with TigerStyle Bounds

## 🎯 Vertical Slice
Implement a robust GitHub REST API client in `internal/git/github.go` that queries commits, branches, and PRs authored by the user for a specified date range via `https://api.github.com`. Handles auth tokens, rate limits, and network errors.

## 🔗 Blocking Edges
- **Blocked by**: None
- **Blocks**: `[M1-03]` (Git Collector Orchestrator)

## 📖 Context & Specification
- **Spec Reference**: Section 3.1 (Git/GitHub Activity Source) & Q4 Availability Concern.
- **ADR Reference**: ADR 0001 (Section 2: Two-Tier Resilient Git Collector).
- **Engineering Guidelines**: 
  - Strictly enforce 5s timeout on API calls.
  - 1 bounded retry on transient HTTP 5xx or connection drops.
  - Explicit error typing (`OutcomeRetryable`, `OutcomeTerminal`, `OutcomeRateLimited`).
  - Read response body with `io.LimitReader` (max 5MB).

## 📐 Pre-Agreed Seams & Interface Contracts
- **Go Interface**:
  ```go
  type GitHubClient struct {
      client *http.Client
      token  string
      baseURL string
  }

  type GitHubCommit struct {
      SHA       string
      Message   string
      Timestamp time.Time
      Repo      string
  }

  type GitHubPR struct {
      Number    int
      Title     string
      Branch    string
      Repo      string
      Commits   []string
  }

  func (c *GitHubClient) FetchDayActivity(ctx context.Context, repo string, date time.Time) ([]GitHubCommit, []GitHubPR, error)
  ```

## ✅ Acceptance Criteria (DoD)
- [ ] Connects to GitHub REST API using `GITHUB_TOKEN` (or unauthenticated if not provided, with rate limit checking).
- [ ] Queries commits by author and date bounds (`since`, `until`).
- [ ] Queries associated Pull Requests to identify parent PR for each commit branch.
- [ ] Handles HTTP 429 / rate-limiting and 5xx errors by returning a typed error unblocking Tier 2 fallback.
- [ ] Comprehensive unit tests with mocked HTTP responses (`httptest.Server`).

## 🧪 Verification Commands
```bash
go test -v ./internal/git -run TestGitHubClient
```
