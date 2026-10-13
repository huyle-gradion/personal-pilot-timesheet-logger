# [M2-02] Collision detector and interactive terminal review CLI

## Vertical slice
Implement the interactive review CLI in `cmd/review-timesheet/main.go` and collision detector in `internal/timesheet/collision.go`. Consumes clustered time blocks from M2-01, detects overlaps between calendar meetings and git activity, presents an ASCII timeline table, resolves collisions interactively or via automated policy, persists the finalized daily record to Tier 2 persistent storage (`internal/storage`, M2-03), and prompts for submission handoff to Milestone M3.

## Blocking edges
- **Blocked by**: `[M2-01]` (Time Block Clusterer), `[M2-03]` (Portable Storage Engine)
- **Blocks**: `[M3-01]` (Gradion Timesheet API Client), `[M4-01]` (Claude Skill Packaging)

## Context and specification
- **Spec reference**: Section 3.2 (Grouping & Deduplication) and Note-1 Step 5 (Display daily log and collisions).
- **ADR references**:
  - `[[ADR-002 - Personal Pilot Timesheet Pipeline Architecture]]` (Pipeline data contracts)
  - `[[ADR-003 - Stateless Modular Timesheet Architecture]]` (Stateless CLI execution)
  - `[[ADR-004 - Portable Storage Tiering & Exporter Sinks]]` (Atomic Tier 2 JSON persistence and sink dispatch)
  - `[[ADR-005 - Interactive Preflight Onboarding and Semantic Time Clumping]]` (Schedule alignment and prompt flow)

### Review UX flow
1. **Load record**: If `-date` corresponds to an existing daily record in Tier 2 storage (`storage.GetDay`), load the existing `TimesheetRecord`. If `-input` is also provided, merge newly collected entries with the existing record and identify any new collisions.
2. **Render schedule**: Display visual daily schedule table in the terminal with time spans, duration, source, category, title, and collision status indicators.
3. **Collision resolution**: For each block where `HasCollision == true`:
   ```text
   Collision detected:
     Calendar : Sprint Planning (09:00 - 10:00)
     Git      : 3 commits on feat/collector (09:35 - 09:50)

   Resolution options:
     [1] Keep both (merge into joint entry)
     [2] Separate (keep meeting, shift git block to start after meeting)
     [3] Discard git activity during meeting
   Select [1-3] (default 1):
   ```
   - **Keep both (`keep_both`)**: Set `Source = "joint"`, combine descriptions with structured bullet points, mark `HasCollision = false`.
   - **Separate (`separate`)**: Keep calendar meeting unchanged. Shift git block `StartTime` to match meeting `EndTime`, recalculate duration, adjust grid alignment, mark `HasCollision = false`.
   - **Discard (`discard`)**: Remove overlapping git entries or drop overlapping git block, retaining calendar meeting only.
4. **Interactive editing menu**: Provide an interactive action menu to update existing work logs:
   - `[e <index>]` Edit block: Modify start/end time, category, title, or description bullets.
   - `[a]` Add block: Manually create a new time block.
   - `[d <index>]` Delete block: Remove an unwanted or duplicate time block.
   - `[s]` Save & persist: Proceed to save and persist updates.
   - `[q]` Quit without saving.
5. **Summary and confirmation**: Render updated summary table showing total allocated hours and category breakdown. In interactive mode, prompt confirmation: `Persist and prepare for submission? [Y/n]`.
6. **Atomic persistence**: Write finalized `TimesheetRecord` to Tier 2 persistent JSON store (`~/.timesheet/records/YYYY-MM-DD.json`) via `internal/storage.SaveDay`, automatically incrementing `Version` and stamping `UpdatedAt`.
7. **Output**: If `-output` is specified, write formatted JSON to stdout or destination file.

## Pre-agreed seams and interface contracts
- **Package**: `internal/timesheet`
- **Data models**:
  ```go
  package timesheet

  import (
      "context"
      "time"
  )

  type CollisionResolution string

  const (
      ResolutionKeepBoth CollisionResolution = "keep_both"
      ResolutionSeparate CollisionResolution = "separate"
      ResolutionDiscard  CollisionResolution = "discard"
  )

  type Collision struct {
      BlockID       string              `json:"blockId"`
      MeetingTitle  string              `json:"meetingTitle"`
      GitTitle      string              `json:"gitTitle"`
      StartTime     string              `json:"startTime"` // "HH:MM"
      EndTime       string              `json:"endTime"`   // "HH:MM"
      Resolution    CollisionResolution `json:"resolution,omitempty"`
  }

  // DetectCollisions identifies overlapping calendar and git blocks in a clustered timesheet.
  func DetectCollisions(ctx context.Context, blocks []TimeBlock) ([]Collision, error)

  // ResolveCollision applies the chosen resolution strategy to a single colliding block.
  func ResolveCollision(block TimeBlock, resolution CollisionResolution) ([]TimeBlock, error)

  // ResolveAll applies a uniform resolution policy across all colliding blocks in non-interactive mode.
  func ResolveAll(ctx context.Context, blocks []TimeBlock, defaultAction CollisionResolution) ([]TimeBlock, error)
  ```

- **CLI invocation and flags**:
  ```bash
  go run ./cmd/review-timesheet [flags]
  ```
  - `-input`: Path to clustered draft JSON file, or `-` for stdin (required or piped).
  - `-date`: Target date string in `YYYY-MM-DD` format (defaults to current date).
  - `-non-interactive`: Boolean flag for non-TTY or automated pipelines (default `false`).
  - `-auto-resolve`: Resolution strategy when non-interactive (`keep_both`, `separate`, `discard`; default `keep_both`).
  - `-storage-dir`: Directory for Tier 2 persistent storage (overrides `TIMESHEET_DATA_DIR` and default `~/.timesheet/records`).
  - `-output`: Optional file path for resolved JSON output (defaults to stdout if specified).
  - `-confirm`: Boolean flag to bypass interactive confirmation (default `false`).

- **TigerStyle constants and invariants**:
  ```go
  const (
      MaxPromptRetriesCount     = 3
      MaxCollisionsCount        = 25
      MaxReviewTableWidthBytes  = 120
      ReviewCommandTimeoutSecs  = 60 * time.Second
  )

  const (
      AssertReviewContextNil    = "ASSERT_REVIEW_001_NIL_CONTEXT"
      AssertReviewEmptyBlocks   = "ASSERT_REVIEW_002_EMPTY_BLOCKS"
  )
  ```

## Acceptance criteria
- [ ] Detects exact timestamp overlaps between calendar meetings and git activity blocks.
- [ ] Renders formatted terminal schedule table with columns: Time Window, Duration, Source, Category, Title, Collision.
- [ ] Implements interactive selection prompt handling all three actions (`keep_both`, `separate`, `discard`) with input sanitization and retry bounds.
- [ ] Supports non-interactive mode via `-non-interactive` flag using `-auto-resolve` policy (defaulting to `keep_both`).
- [ ] Separate action shifts git block start time to meeting end time and preserves calendar slot.
- [ ] Discard action removes overlapping git activity while preserving calendar meeting.
- [ ] Keep both action merges activity into joint entry (`Source: "joint"`) with consolidated descriptions.
- [ ] Loads existing daily record from Tier 2 storage when `--date` has a previously saved timesheet.
- [ ] Allows user to edit existing time blocks (time, title, description, category), add new manual blocks, or delete blocks.
- [ ] Persists resolved timesheet to Tier 2 storage (`storage.JSONFileStore.SaveDay`) with atomic write semantics, incrementing version on updates.
- [ ] Calculates and displays total work hours and category breakdown summary before exit.
- [ ] Achieves >= 90% unit and integration test coverage across interactive mock readers and non-interactive flag suites.

## Verification commands
```bash
go test -v -race ./internal/timesheet -run TestCollision
go test -v -race ./cmd/review-timesheet/...
```
