# [M2-03] Portable 3-tier storage engine and exporter sinks

## Vertical slice
Implement the portable storage package in `internal/storage/` conforming to ADR-004. Provides a decoupled `TimesheetStore` interface, a zero-dependency primary JSON file store (`~/.timesheet/records/YYYY-MM-DD.json`) with atomic write guarantees, and pluggable exporter sinks (stdout console table and optional Obsidian AI database vault sink).

## Blocking edges
- **Blocked by**: `[M2-01]` (Time Block Clusterer)
- **Blocks**: `[M2-02]` (Collision Detector & Review CLI), `[M3-01]` (Gradion API Client)

## Context and specification
- **Spec reference**: Section 3.2 (Persistence & 30-day continuous audit requirement) and Section 6.
- **ADR reference**:
  - `[[ADR-004 - Portable Storage Tiering & Exporter Sinks]]` (`docs/adr/0003-portable-storage-architecture.md`)
- **Key requirements**:
  1. **Zero external dependencies**: Standard library only (`os`, `path/filepath`, `encoding/json`, `time`, `context`).
  2. **Storage tiering separation**:
     - **Tier 1 (Ephemeral Scratch)**: `.cache/timesheet/` for transient collector outputs and session logs.
     - **Tier 2 (Primary Persistent Store)**: Structured daily JSON records stored at `~/.timesheet/records/YYYY-MM-DD.json`.
     - **Tier 3 (Pluggable Sinks)**: Terminal console, optional Obsidian vault append, and Gradion API sync (M3-01).
  3. **Atomic file operations**: Prevent half-written corruption during unexpected SIGINT or crash by writing to a hidden temporary file in the target directory and atomically renaming with `os.Rename`.
  4. **Directory resolution hierarchy**:
     - 1st: Environment variable `TIMESHEET_DATA_DIR` (if set and non-empty).
     - 2nd: User home directory path `~/.timesheet/records/` (expanded via `os.UserHomeDir()`).
     - 3rd: Local fallback `./data/timesheets/` (if home directory cannot be determined).

## Pre-agreed seams and interface contracts
- **Package**: `internal/storage`
- **Data models and interfaces**:
  ```go
  package storage

  import (
      "context"
      "time"
  )

  // TimesheetRecord represents the persisted daily timesheet document.
  type TimesheetRecord struct {
      Date        string      `json:"date"`        // "YYYY-MM-DD"
      UpdatedAt   time.Time   `json:"updated_at"`  // ISO-8601
      Version     int         `json:"version"`     // Schema version (default 1)
      TotalHours  float64     `json:"total_hours"`
      Blocks      []TimeBlock `json:"blocks"`
  }

  // TimesheetStore defines the persistence contract for daily timesheet records.
  type TimesheetStore interface {
      SaveDay(ctx context.Context, record TimesheetRecord) error
      GetDay(ctx context.Context, date string) (*TimesheetRecord, error)
      ListDays(ctx context.Context, fromDate, toDate string) ([]string, error)
  }

  // ExporterSink defines the pluggable output contract for finalized timesheets.
  type ExporterSink interface {
      Name() string
      Export(ctx context.Context, record TimesheetRecord) error
  }
  ```

- **JSONFileStore implementation details**:
  ```go
  type JSONFileStore struct {
      baseDir string
  }

  func NewJSONFileStore(customDir string) (*JSONFileStore, error)
  ```
  - `SaveDay` algorithm:
    1. Validate `record.Date` (must match `YYYY-MM-DD` regex).
    2. Ensure `baseDir` exists with permissions `0750` (`os.MkdirAll`).
    3. Target path: `filepath.Join(baseDir, date+".json")`.
    4. Upsert versioning: If target path exists, load existing header to increment `record.Version = existing.Version + 1`; otherwise set `record.Version = 1`.
    5. Set `record.UpdatedAt = time.Now().UTC()`.
    6. Temporary path: `filepath.Join(baseDir, fmt.Sprintf(".%s.tmp.%d", date, time.Now().UnixNano()))`.
    7. Write formatted JSON payload with `json.MarshalIndent(record, "", "  ")`.
    8. Write file with permissions `0600` (`os.WriteFile` or `os.OpenFile` + `Sync`).
    9. Atomically rename temporary file to target path (`os.Rename`).
  - `GetDay` algorithm:
    1. Read `filepath.Join(baseDir, date+".json")`.
    2. If file does not exist, return typed error `ErrRecordNotFound`.
    3. Unmarshal into `TimesheetRecord`.
  - `ListDays` algorithm:
    1. Scan `baseDir` for `*.json` files.
    2. Extract dates matching `YYYY-MM-DD`.
    3. Filter dates falling within `[fromDate, toDate]` inclusively.
    4. Return chronologically sorted date strings.

- **ObsidianSink implementation details**:
  ```go
  type ObsidianSink struct {
      vaultPath string
  }

  func NewObsidianSink() (*ObsidianSink, error)
  ```
  - Vault path lookup: `TIMESHEET_VAULT_PATH` or `AI_DATABASE_PATH`.
  - If neither environment variable is configured, `Export` returns `ErrSinkNotConfigured` or no-op depending on configuration.
  - Appends or updates the target note: `filepath.Join(vaultPath, "02 - Daily & Logs", record.Date+".md")`.
  - Formats markdown block with timesheet summary table and bullet items.

- **TigerStyle constants and invariants**:
  ```go
  const (
      MaxRecordSizeBytes   int64 = 10 * 1024 * 1024 // 10 MiB limit on timesheet JSON record
      MaxDaysListedCount   int   = 365              // Bounded query limit
      DefaultSchemaVersion int   = 1
  )

  const (
      AssertStorageContextNil = "ASSERT_STORAGE_001_NIL_CONTEXT"
      AssertStorageInvalidDate = "ASSERT_STORAGE_002_INVALID_DATE"
  )
  ```

## Acceptance criteria
- [ ] Directory resolution honors precedence: `TIMESHEET_DATA_DIR` -> `~/.timesheet/records/` -> `./data/timesheets/`.
- [ ] `SaveDay` writes atomically via temp-file rename so concurrent readers or crashes never encounter a corrupt JSON payload.
- [ ] `SaveDay` performs idempotent upserts when updating existing dates, incrementing `Version` and stamping `UpdatedAt`.
- [ ] Sets restrictive file permissions (`0750` for directories, `0600` for JSON files).
- [ ] `GetDay` returns typed `ErrRecordNotFound` when queried date has no record.
- [ ] `ListDays` supports date range filtering and returns sorted dates up to `MaxDaysListedCount`.
- [ ] `ObsidianSink` activates only when vault path environment variable is set, otherwise returning a distinct typed error or skipping without panic.
- [ ] `ObsidianSink` formats valid markdown without corrupting existing daily log sections.
- [ ] TigerStyle bounds and assertions verified.
- [ ] Comprehensive unit tests using `t.TempDir()` verifying atomic writes, concurrent reads, missing files, and invalid dates.

## Verification commands
```bash
go test -v -race ./internal/storage/...
```
