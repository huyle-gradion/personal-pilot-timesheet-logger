# ADR 0001: Personal Pilot Timesheet Pipeline Architecture

## Status
Superseded by [ADR 0005](0005-review-first-timesheet-architecture.md) on 2026-10-09. Retained as historical context.

## Context
Assignment 1 requires creating a Claude Code Skill and deterministic Go pipeline that aggregates daily engineering activity across Git/GitHub and Google Calendar into validated, collision-aware timesheet entries. These entries must be submitted to the production Gradion Timesheet API (`ts-prod.gradion.com`) and recorded locally in Markdown and CSV benchmark files.

The design must satisfy strict TigerStyle engineering standards, prevent duplicate entries across re-runs (DoD 3.3), handle external service outages (e.g., GitHub REST API downtime), preserve human oversight for activity collisions, and minimize LLM token consumption (< 600 tokens/day).

## Decisions

### 1. Repository Scope & Discovery
- Tracked repositories are configured via `TRACKED_REPOS` in `.env` (comma-separated local filesystem paths or GitHub slugs) and can be overridden via the `--repo` CLI flag.
- When omitted, collectors default to the current working directory (`.`).

### 2. Two-Tier Resilient Git Collector (`cmd/fetch-git`)
- **Tier 1 (Remote GitHub REST API)**: Direct HTTPS requests to `https://api.github.com` authenticated via `GITHUB_TOKEN`, bounded by a 5-second TigerStyle timeout and 1 bounded retry.
- **Tier 2 (Local Git Fallback)**: If the GitHub API fails (status 5xx, 429, DNS resolution error, or network timeout), execution fails open to local Git repository analysis (`git log --since=...`, branch discovery, commit regex `#\d+`). Entries are tagged with `"source_warning": "github_api_unavailable_local_fallback"`.
- **Commit & PR Deduplication**: Commits associated with an active or merged PR branch are clustered under the parent PR record. Unparented commits remain standalone entries.

### 3. Intermediate Data Contract (`SimpleEntry`)
- Collectors (`fetch-calendar`, `fetch-git`) output normalized, unescaped JSON conforming to the `SimpleEntry` contract:
  ```json
  {
    "id": "git-commit-a1b2c3d",
    "source": "git",
    "timestamp": "2026-10-07T09:30:00+07:00",
    "endTime": "2026-10-07T10:30:00+07:00",
    "title": "feat: two-tier git collector",
    "description": "Implemented local git log fallback for GitHub API outages",
    "metadata": {
      "repo": "assignment-1",
      "branch": "feat/git-collector",
      "pr_number": 2,
      "commits": ["a1b2c3d", "e4f5g6h"]
    }
  }
  ```

### 4. Time Blocking & Collision Resolution
- **Anchors**: Google Calendar events establish fixed time blocks.
- **Synthesized Blocks**: Git activity outside calendar events is clustered into focused work blocks.
- **Collisions**: Overlaps between meetings and Git commits are explicitly detected (`"collision": true`).
- **Review UX (Step 5)**: An interactive terminal interface displays a daily visual timeline and prompts the engineer to resolve each collision (Merge joint entry, Separate after meeting, or Discard Git activity), ending with an explicit submission prompt (`[y/N]`).

### 5. Script vs. AI Boundary & Token Budget
- **Deterministic Go Scripts (`[Script]`)**: Fetch all raw data, filter by date bounds, link commits to PRs, compute time blocks, detect collisions, assemble the skeleton JSON, and perform API reconciliation.
- **Claude LLM (`[AI]`)**: Only synthesizes human-readable work summaries (1–2 concise sentences with bold topic prefixes) from pre-clustered activity skeletons.
- **Token Budget**: Input payload is strictly compressed to < 400 tokens; output is bounded to < 200 tokens (total < 600 tokens/day).

### 6. Timesheet API Reconciliation & Idempotency
- Endpoints on `ts-prod.gradion.com`:
  - `GET /api/entries?dateFrom=YYYY-MM-DD&dateTo=YYYY-MM-DD`
  - `POST /api/entries`
  - `PATCH /api/entries/{id}`
  - `GET /api/classifications/recent`
- **Reconciliation Algorithm**:
  1. Fetch existing entries for the target date.
  2. If an entry exists with matching `startTime` and `endTime`:
     - If the description changed, issue `PATCH /api/entries/{id}` with `dto.description` and `reason: "Automated timesheet sync update"`.
     - If identical, skip with an idempotent acknowledgement.
  3. If no entry matches the time slot, issue `POST /api/entries`.
- **Classification ID**: Resolved dynamically via `GET /api/classifications/recent` (defaulting to the first entry: `156b7d0e-f29c-470d-b90c-b6b67df4c21a` for *Gradion Intern Academy 2026*), with `.env` fallback (`DEFAULT_CLASSIFICATION_ID`).
- **Authentication**: `Authorization: Bearer <TIMESHEET_API_TOKEN>` loaded from `.env`.

### 7. Benchmarking & Empirical Tracking
- `cmd/benchmark` measures:
  - Total pipeline wall-clock execution time (ms).
  - Peak RSS memory usage across Go binaries.
  - Claude session token consumption (input, output, cache-read) extracted from JSONL transcripts.
- Results are appended to `benchmarks/metrics.csv` and rendered via `cmd/benchmark -chart`.

## Consequences
- Guarantees 100% submission reliability even during GitHub REST API outages.
- Enforces zero duplicate entries on repeated or interrupted runs.
- Maintains strict adherence to TigerStyle (bounded allocations, typed outcomes, zero secret leaks).
- Keeps LLM token costs minimal and predictable across the 30-day pilot.
