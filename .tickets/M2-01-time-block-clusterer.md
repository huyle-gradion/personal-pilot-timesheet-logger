# [M2-01] Deterministic time block clusterer and focus span engine

## Vertical slice
Implement the time block clustering engine in `internal/timesheet/clusterer.go`. Merges `SimpleEntry` streams from Calendar and Git, creates fixed blocks for calendar meetings, clusters scattered commits into contiguous focus spans bounded by work hours, and consolidates micro-activities using 30-minute semantic clumping per ADR-005.

## Blocking edges
- **Blocked by**: `[M0-01]` (Calendar Fetcher), `[M1-03]` (Two-Tier Git Collector CLI)
- **Blocks**: `[M2-02]` (Collision Detector & Review CLI), `[M2-03]` (Portable Storage Engine)

## Context and specification
- **Spec reference**: Section 3.2 (Grouping & Deduplication) and Note-1 Steps 3 and 4.
- **ADR references**:
  - `[[ADR-002 - Personal Pilot Timesheet Pipeline Architecture]]` (Two-tier collection and entry schema)
  - `[[ADR-005 - Interactive Preflight Onboarding and Semantic Time Clumping]]` (Core hours and 30-minute semantic consolidation)

### Work hours schedule
- **Advisory core hours**: `09:00` to `12:00` and `13:00` to `18:00` provide informational baseline context representing typical work hours.
- **Unrestricted work logging**: There is no rule preventing employees from committing work logs during lunch (`12:00` to `13:00`), before `09:00`, or after `18:00`. All activity at any hour of the day is valid and logged.
- **Lunch time activity (`12:00` to `13:00`)**: If work or commits occur during lunch, they are clustered into valid focus blocks without artificial splitting or truncation. Morning and afternoon spans separate only when a natural inactivity gap (> 30 to 60 minutes) occurs.
- **Off-hours activity**: Commits or activity before `09:00` or after `18:00` are first-class valid work blocks (for example, early morning focus or evening development).

### 30-minute semantic clumping rules
- **Micro-span identification**: Any task, commit group, or focus span shorter than 30 minutes must not create a fragmented micro-log.
- **Semantic action taxonomy**:
  - `development`: coding, feat, feature, implement, refactor, build.
  - `maintenance`: fix, bugfix, test, chore, deps, lint, style, hotfix.
  - `research_docs`: docs, design, adr, plan, investigate, sync, review.
- **Clustering and merging logic**:
  1. When a micro-activity (< 30 minutes) is detected, inspect adjacent focus blocks within a 60-minute window.
  2. If an adjacent block shares the same semantic category, merge the micro-activity into it, extending `StartTime` or `EndTime` and appending the bulleted descriptions.
  3. If isolated with no matching adjacent block, expand the block to a minimum duration of 30 minutes, aligned to 15-minute grid boundaries (for example, a single commit at 14:10 expands to 14:00 - 14:30 or 14:15 - 14:45).
- **Calendar collisions**:
  - Calendar meetings act as immovable time blocks.
  - If git commits occur during a scheduled meeting, the calendar meeting retains its slot, and the overlapping git activity is tagged with `HasCollision: true` for interactive resolution in M2-02.

## Pre-agreed seams and interface contracts
- **Package**: `internal/timesheet`
- **Data models**:
  ```go
  type SemanticCategory string

  const (
      CategoryDevelopment  SemanticCategory = "development"
      CategoryMaintenance  SemanticCategory = "maintenance"
      CategoryResearchDocs SemanticCategory = "research_docs"
      CategoryMeeting      SemanticCategory = "meeting"
      CategoryOther        SemanticCategory = "other"
  )

  type TimeBlock struct {
      ID          string           `json:"id"`
      Source      string           `json:"source"` // "calendar", "git", "joint"
      Category    SemanticCategory `json:"category"`
      StartTime   string           `json:"startTime"` // "HH:MM"
      EndTime     string           `json:"endTime"`   // "HH:MM"
      Date        string           `json:"date"`      // "YYYY-MM-DD"
      Title       string           `json:"title"`
      Description string           `json:"description"`
      Entries     []SimpleEntry    `json:"entries"`
      HasCollision bool            `json:"collision"`
  }

  type ClusterOptions struct {
      CoreHoursStart string // default "09:00"
      CoreHoursEnd   string // default "18:00"
      LunchStart     string // default "12:00"
      LunchEnd       string // default "13:00"
      MinBlockMinutes int   // default 30
      RoundGridMinutes int  // default 15
  }

  func ClusterEntries(ctx context.Context, entries []SimpleEntry, targetDate time.Time, opts ClusterOptions) ([]TimeBlock, error)
  ```

- **TigerStyle constants and invariants**:
  ```go
  const (
      MaxEntriesPerDayCount = 200
      MaxTimeBlocksCount    = 50
      MinClumpDurationMins  = 30
      GridAlignmentMinutes  = 15
  )
  ```

## Acceptance criteria
- [x] Sorts all incoming `SimpleEntry` records chronologically before clustering.
- [x] Maps calendar entries directly to immovable `TimeBlock` items (`Source: "calendar"`, `Category: CategoryMeeting`).
- [x] Groups git commits into focused work spans with advisory core hours reference.
- [x] Preserves off-hours and lunch hour activity as valid, distinct blocks without artificial truncation.
- [x] Clusters lunch time (12:00-13:00) activity continuously if commits are active, separating only when natural inactivity gaps occur.
- [x] Applies 30-minute semantic clumping: merges < 30-minute micro-tasks sharing semantic category with adjacent blocks within 60 minutes.
- [x] Expands isolated micro-commits to 30-minute minimum focus blocks on a 15-minute grid.
- [x] Flags calendar vs. git collisions with `HasCollision: true` without discarding git activity data.
- [x] Enforces TigerStyle assertions for non-nil context, valid target date, and bounded allocations.
- [x] Achieves >= 90% unit test coverage with parameterized edge cases (lunch split, night owl commits, back-to-back meetings, isolated commits).

## Verification commands
```bash
go test -v -race ./internal/timesheet -run TestClusterEntries
```
