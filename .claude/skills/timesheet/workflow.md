# Timesheet Pipeline Workflow

This document defines the 7-step execution workflow for the timesheet logging skill. In adherence to ADR-0001, ADR-003, and ADR-005, the boundary between deterministic code and generative artificial intelligence is strictly delineated:
- **`[Script]`**: Executed deterministically by compiled Go binaries or scripts with zero LLM token consumption.
- **`[AI]`**: Minimal editorial synthesis executed by the LLM, bounded to < 600 total tokens per day.

---

## Directory layout and data contracts

Ephemeral scratch data is written to `.cache/timesheet/YYYY-MM-DD/` per ADR-004:
- `calendar.json`: Array of `SimpleEntry` objects from Google Calendar.
- `git.json`: Array of `SimpleEntry` objects from GitHub API or local Git history.
- `skeleton.json`: Clustered `TimeBlock` objects with unrefined titles and descriptions.
- `refined.json`: Time blocks updated with editorial topic labels and descriptions.
- `reviewed.json`: Time blocks after collision detection and human review.

Permanent persistence is stored in Tier 2 local JSON archive:
- `~/.timesheet/records/YYYY-MM-DD.json`: Versioned, atomic timesheet record.
- `timesheet/YYYY-MM-DD.md`: Clean Markdown daily summary table.
- `benchmarks/metrics.csv`: Cumulative execution duration, peak RSS memory, and token consumption metrics.

---

## Step-by-step pipeline

### Step 1: `[Script]` Ingest calendar events
Fetch and parse Google Calendar private iCal feeds for the target date. Handles UID stability, declined invitations (`PARTSTAT=DECLINED`), cancelled tombstones, recurring event exceptions (`EXDATE`), and transparent events.

```bash
mkdir -p .cache/timesheet/$TARGET_DATE
go run ./cmd/fetch-calendar -date "$TARGET_DATE" > .cache/timesheet/$TARGET_DATE/calendar.json
```

**Output Seam**: Normalized array of `SimpleEntry` objects.

---

### Step 2: `[Script]` Collect Git activity
Collect Git commits and pull requests across configured repositories. Uses Tier 1 GitHub REST API with automatic failover to Tier 2 local git log history upon HTTP 429, 5xx, or network timeouts. Deduplicates commits under associated PR branches.

```bash
go run ./cmd/fetch-git -date "$TARGET_DATE" > .cache/timesheet/$TARGET_DATE/git.json
```

**Output Seam**: Normalized array of `SimpleEntry` objects with `metadata.repo`, `metadata.branch`, and `metadata.pr_number`.

---

### Step 3: `[Script]` Cluster time blocks
Combine calendar and Git entries into chronological work blocks with 30-minute semantic clumping and 15-minute grid snapping. Calendar events act as fixed anchors. Consecutive Git commits within 45 minutes of the same category are merged into focused spans. Lunch hours (12:00 to 13:00) and off-hours remain unrestricted per user guidance.

```bash
# Combine entries and cluster via review-timesheet non-interactive pass
jq -s 'add' .cache/timesheet/$TARGET_DATE/calendar.json .cache/timesheet/$TARGET_DATE/git.json > .cache/timesheet/$TARGET_DATE/combined.json
go run ./cmd/review-timesheet -date "$TARGET_DATE" -input .cache/timesheet/$TARGET_DATE/combined.json -non-interactive -auto-resolve=keep_both -confirm -output .cache/timesheet/$TARGET_DATE/skeleton.json
```

**Output Seam**: JSON payload containing `TimeBlock` array.

---

### Step 4: `[AI]` Editorial synthesis
Synthesize clear, professional topic titles and concise 1-2 sentence impact descriptions for each clustered block.

#### Compressed AI prompt contract
- **Input budget**: < 300 tokens (skeleton block IDs, categories, raw commit messages, PR numbers).
- **Output budget**: < 150 tokens (compact JSON mapping).
- **Prohibited**: Conversational filler, introductory greetings, markdown commentary, explanations, or prose outside the specified JSON schema.

#### Prompt template
```text
System: You are an expert timesheet editor. Synthesize clustered engineering logs into high-signal topic titles and concise 1-2 sentence impact summaries. Follow Google X-Y-Z formula where applicable.

Input JSON:
{{SKELETON_SUMMARY_JSON}}

Output format:
Output ONLY a single valid JSON object mapping each block ID to its refined "title" and "description". Do not wrap in conversational text.
Example schema:
{
  "git-2026-10-08-0900-1": {
    "title": "Two-Tier Git Collector & Local Fallback",
    "description": "Implemented local git log fallback for GitHub REST outages with zero double-counting across PR commits."
  }
}
```

Apply the refined titles and descriptions to `.cache/timesheet/$TARGET_DATE/skeleton.json` and save to `.cache/timesheet/$TARGET_DATE/refined.json`.

---

### Step 5: `[Script]` Collision review and storage
Render the ASCII timeline schedule and prompt the user to resolve any detected collisions between meetings and Git activity. Persists the finalized schedule to Tier 2 storage (`~/.timesheet/records/YYYY-MM-DD.json`) and exports markdown to Obsidian vault if configured.

```bash
# Interactive mode (default)
go run ./cmd/review-timesheet -date "$TARGET_DATE" -input .cache/timesheet/$TARGET_DATE/refined.json -sink=obsidian

# Or non-interactive mode (for automated batch execution)
go run ./cmd/review-timesheet -date "$TARGET_DATE" -input .cache/timesheet/$TARGET_DATE/refined.json -non-interactive -auto-resolve=keep_both -confirm -sink=obsidian
```

**Output Seam**: Versioned record saved in `~/.timesheet/records/$TARGET_DATE.json`.

---

### Step 6: `[Script]` Gradion Timesheet API reconciliation
Reconcile the saved daily timesheet record with the remote Gradion Timesheet API (`ts-prod.gradion.com`). Fetches existing remote entries, creates missing slots via `POST /api/entries`, updates modified descriptions via `PATCH /api/entries/{id}`, and skips identical entries. Guarantees zero duplicate creates on repeated executions. Exports local clean markdown summary to `timesheet/$TARGET_DATE.md`.

```bash
# Dry run preview (optional)
go run ./cmd/sync-timesheet -date "$TARGET_DATE" -dry-run

# Live reconciliation
go run ./cmd/sync-timesheet -date "$TARGET_DATE"
```

**Output Seam**: Remote entries synchronized and local `timesheet/$TARGET_DATE.md` generated.

---

### Step 7: `[Script]` Empirical benchmark logging
Parse the session JSONL transcript to extract LLM input/output/cache tokens, record wall-clock execution duration and peak resident set size (RSS) memory, and append the daily metric to `benchmarks/metrics.csv`. Renders a terminal ASCII bar chart tracking daily token usage against the 600-token ceiling.

```bash
go run ./cmd/benchmark -record -date "$TARGET_DATE" -transcript "$TRANSCRIPT_JSONL_PATH" -chart
```

**Output Seam**: Appended row in `benchmarks/metrics.csv` and rendered terminal ASCII chart.
