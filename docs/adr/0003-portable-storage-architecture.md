# ADR 0003: Portable Storage Tiering and Exporter Sinks

## Status
Superseded by [ADR 0005](0005-review-first-timesheet-architecture.md) on 2026-10-09. Retained as historical context.

## Context
In the initial development iterations, daily timesheet logs and intermediate summaries were coupled to the author's local Obsidian AI Database vault (`~/ai-database/02 - Daily & Logs/`).

While this provided rich personal knowledge graph integration, sharing or porting the timesheet logger across teams, supervisors, or open-source contributors introduces key constraints:
1. **Zero External Dependency**: Non-Obsidian users must not be required to maintain a markdown vault or specific folder layout.
2. **Audit & Persistence Guarantee**: Sections 3.2 and 6 of the project specification mandate preserving a 30-day continuous audit history of daily timesheet entries.
3. **Storage Boundary Hygiene**: Storing primary persistent logs in ephemeral locations like `.cache/` risks accidental data loss during OS or user cache cleanups (`rm -rf ~/.cache`).
4. **Inspectability & Ergonomics**: Stored entries must be easily inspectable with standard Unix tools (`cat`, `jq`), human-readable, and version-control friendly without complex database drivers.

## Decision

We adopt a **3-tier storage architecture** with a decoupled `TimesheetStore` interface and pluggable exporter sinks:

```
+----------------------------------------------------------------+
|                        Data Ingestion                          |
|             (Calendar Collector & Git Collector)               |
+-------------------------------+--------------------------------+
                                |
                                v
+----------------------------------------------------------------+
|             Tier 1: Ephemeral Scratch (.cache/)                |
|      Intermediate collector dumps, temporary session logs      |
+-------------------------------+--------------------------------+
                                |
                                v
+----------------------------------------------------------------+
|              Clustering & Review Engine (M2)                   |
|           Time block synthesis, interactive review             |
+-------------------------------+--------------------------------+
                                |
                                v
+----------------------------------------------------------------+
|         Tier 2: Primary Persistent Store (Zero-Dep JSON)       |
|            ~/.timesheet/records/YYYY-MM-DD.json                |
|             (Configurable via TIMESHEET_DATA_DIR)              |
+-------------------------------+--------------------------------+
                                |
         +----------------------+----------------------+
         |                                             |
         v                                             v
+-----------------------------+               +-----------------------------+
|    Tier 3: Obsidian Sink    |               |  Tier 3: Gradion API Sink   |
| (Active only when configured|               | (POST ts-prod.gradion.com)  |
|  via TIMESHEET_VAULT_PATH)  |               |                             |
+-----------------------------+               +-----------------------------+
```

### 1. Storage Tiering Definition
- **Tier 1 — Ephemeral Scratch (`.cache/timesheet/` or `$XDG_CACHE_HOME/timesheet/`)**:
  - Transient API payload responses and raw collector outputs.
  - May be purged at any time without impacting timesheet integrity.
- **Tier 2 — Primary Persistent Store (`~/.timesheet/records/YYYY-MM-DD.json`)**:
  - The single source of truth for finalized daily timesheets.
  - Zero-dependency: Uses standard library `encoding/json` + `os`.
  - Atomicity: Written via atomic file rename (`write to temp file` -> `os.Rename`) to prevent half-written corruption.
  - Location precedence: `TIMESHEET_DATA_DIR` -> `~/.timesheet/records/` -> `./data/timesheets/`.
- **Tier 3 — Pluggable Exporter Sinks**:
  - Exporters receive finalized `[]TimesheetEntry` after review.
  - **Default**: Terminal stdout summary / table.
  - **Obsidian / AI Database Sink**: Activated only when `TIMESHEET_VAULT_PATH` or `AI_DATABASE_PATH` environment variable is defined. Automatically appends formatted daily markdown entries to `02 - Daily & Logs/YYYY-MM-DD.md`.
  - **Gradion API Sink**: Reconciles and pushes entries to `ts-prod.gradion.com` (Milestone M3).

### 2. Interface Contract (`internal/storage`)
```go
type TimesheetStore interface {
    // SaveDay writes finalized entries for a specific day atomically.
    SaveDay(ctx context.Context, date string, entries []TimesheetEntry) error
    // GetDay retrieves stored entries for auditing or re-submission.
    GetDay(ctx context.Context, date string) ([]TimesheetEntry, error)
    // ListDays returns all recorded date keys (YYYY-MM-DD).
    ListDays(ctx context.Context, from, to string) ([]string, error)
}
```

## Consequences

### Positive
- **Universal Portability**: The core CLI and skill work out of the box on any machine with zero configuration.
- **Resilience**: Accidental `.cache` deletions will not destroy the mandatory 30-day timesheet audit trail.
- **No CGO Overhead**: Eliminates SQLite build complexity and binary bloat while maintaining sub-millisecond retrieval.
- **Seamless Personal Workflow**: Users with Obsidian vaults retain full automated journaling without forcing that structure onto other users.

### Negative / Trade-offs
- Multi-day range querying requires scanning directory JSON files rather than running SQL queries (acceptable given $< 365$ files per year).
