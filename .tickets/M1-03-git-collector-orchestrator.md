# [M1-03] Two-Tier Git Collector CLI with PR/Commit Deduplication

## 🎯 Vertical Slice
Implement the user-facing CLI `cmd/fetch-git/main.go` that coordinates Tier 1 (GitHub API) and Tier 2 (Local Git), deduplicates commits belonging to PR branches, and outputs canonical `SimpleEntry` JSON.

## 🔗 Blocking Edges
- **Blocked by**: `[M1-01]`, `[M1-02]`
- **Blocks**: `[M2-01]`, `[M2-02]`

## 📖 Context & Specification
- **Spec Reference**: Section 3.1 & 3.5 (Script vs AI boundary) & Note-2 Edge Case No. 1 (PR vs Commit collision).
- **ADR Reference**: ADR 0001 (Section 2 & 3: Resilient Git Collector & SimpleEntry Contract).
- **Behavior**:
  1. Reads `TRACKED_REPOS` from `.env` or `--repos` flag.
  2. Tries Tier 1 GitHub API.
  3. If Tier 1 fails with 5xx/429/timeout/DNS error, logs warning to stderr and activates Tier 2 local Git.
  4. Deduplicates commits: commits belonging to PR branch are attached under the parent PR entry. Unparented commits remain standalone.
  5. Outputs `[]SimpleEntry` JSON to stdout.

## 📐 Pre-Agreed Seams & Interface Contracts
- **CLI Invocations**:
  ```bash
  go run ./cmd/fetch-git -date=YYYY-MM-DD
  go run ./cmd/fetch-git -date=YYYY-MM-DD -repos=/path/to/repo1,/path/to/repo2
  ```
- **Output JSON Contract**:
  ```json
  [
    {
      "id": "git-pr-42",
      "source": "git",
      "timestamp": "2026-10-07T10:00:00+07:00",
      "endTime": "2026-10-07T11:30:00+07:00",
      "title": "PR #42: feat(git): two-tier collector",
      "description": "Implemented GitHub REST client and local git fallback",
      "metadata": {
        "repo": "assignment-1",
        "branch": "feat/git-collector",
        "pr_number": 42,
        "commit_count": 3,
        "commits": ["9a1b2c3", "4d5e6f7"]
      }
    }
  ]
  ```

## ✅ Acceptance Criteria (DoD)
- [x] Supports `--date` (defaults to today) and `--repos` (defaults to `.env` or CWD).
- [x] Transparently falls back to local Git on network drop or GitHub 5xx/429 error with `"source_warning": "github_api_unavailable_local_fallback"`.
- [x] PR/commit deduplication prevents double-counting commits that belong to PR branches.
- [x] Redacts any tokens or credentials from error logs and `--help`.
- [x] Pre-flight Guided Onboarding (ADR-005): If GitHub credentials/tokens are absent, provides actionable guidance (`gh auth login` or generating a PAT with `repo` and `read:user` scopes).
- [x] Validated with unit and subprocess integration tests.

## 🧪 Verification Commands
```bash
go test -v ./cmd/fetch-git/...
go run ./cmd/fetch-git -date=$(date +%Y-%m-%d)
```
