# [M1-02] Tier 2 Local Git History Parser with PR Discoverability Regex

## 🎯 Vertical Slice
Implement a resilient local Git repository activity extractor in `internal/git/local.go`. Uses subprocess-safe `git log` and `git branch` invocations to discover commits, active branches, and PR issue references from commit messages when GitHub REST API is unavailable.

## 🔗 Blocking Edges
- **Blocked by**: None
- **Blocks**: `[M1-03]` (Git Collector Orchestrator)

## 📖 Context & Specification
- **Spec Reference**: Section 3.1 & Note-1 Step 1.
- **ADR Reference**: ADR 0001 (Section 2: Two-Tier Resilient Git Collector).
- **Engineering Guidelines**:
  - Subprocess safety: Set `Setpgid: true`, kill process group on context cancellation.
  - Zero shell injection: Use `exec.CommandContext(ctx, "git", args...)` without shell interpretation.
  - Parse commit hashes, author timestamps (ISO-8601), and commit subjects.
  - Extract PR/issue references via regex (e.g. `(#\d+)`, `GH-\d+`, `pull/\d+`).

## 📐 Pre-Agreed Seams & Interface Contracts
- **Go Interface**:
  ```go
  type LocalGitCollector struct {
      repoPath string
  }

  func (l *LocalGitCollector) FetchDayCommits(ctx context.Context, authorEmail string, date time.Time) ([]LocalCommit, error)
  func (l *LocalGitCollector) DiscoverPRReferences(commits []LocalCommit) map[int][]LocalCommit
  ```

## ✅ Acceptance Criteria (DoD)
- [x] Executes `git log` with precise `--since` and `--until` boundaries and formatted output (`--pretty=format:%H%x00%an%x00%ae%x00%aI%x00%s`).
- [x] Filters commits by author email or author name matching environment config.
- [x] Extracts PR numbers from branch names (`feat/issue-123`, `PR-45`) and commit message strings (`#45`).
- [x] Handles non-git directories gracefully with typed error `ErrNotAGitRepository`.
- [x] Subprocess process-group termination tested on timeout.

## 🧪 Verification Commands
```bash
go test -v ./internal/git -run TestLocalGitCollector
```
