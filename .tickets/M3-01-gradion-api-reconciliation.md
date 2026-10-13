# [M3-01] Gradion timesheet API client with idempotent reconciliation

## Vertical slice
Implement the Gradion production API client and reconciliation engine in `internal/api/gradion.go` and `cmd/sync-timesheet/main.go`. Loads finalized daily timesheet records from Tier 2 persistent storage (`internal/storage`), queries existing remote entries on `ts-prod.gradion.com` for the target date, reconciles updates (patching modified slots, creating new slots, detecting removed slots), and supports Workspace MCP resource endpoints.

## Blocking edges
- **Blocked by**: `[M2-02]` (Collision Detector & Review CLI), `[M2-03]` (Portable Storage Engine)
- **Blocks**: `[M4-01]` (Claude Skill Packaging), `[M4-02]` (E2E Verification Pilot)

## Context and specification
- **Spec reference**: Section 3.3 (Output & Persistence) & Section 3.4 (Weekly View & Manual Review).
- **ADR references**:
  - `[[ADR-002 - Personal Pilot Timesheet Pipeline Architecture]]`
  - `[[ADR-004 - Portable Storage Tiering & Exporter Sinks]]` (Reading from Tier 2 persistent store)
  - `[[ADR-005 - Interactive Preflight Onboarding and Semantic Time Clumping]]` (Workspace MCP discovery)
- **Target endpoints**:
  - Direct REST (`ts-prod.gradion.com`):
    - `GET /api/entries?limit=200&offset=0&dateFrom=...&dateTo=...`
    - `POST /api/entries` (`{"date","startTime","endTime","classificationId","description"}`)
    - `PATCH /api/entries/{id}` (`{"dto":{"description","task"},"reason"}`)
    - `DELETE /api/entries/{id}`
    - `GET /api/classifications/recent` (Default fallback: `"156b7d0e-f29c-470d-b90c-b6b67df4c21a"`)
  - Workspace App MCP Tools (`https://workspace.gradion.com/api/me/apps/mcp-resources`):
    - Discovery: `GET /api/me/apps/mcp` and `/api/me/apps/mcp-resources`
    - Tool invocation: `POST /api/me/apps/timesheet/tools/log_hours/call`
    - Bulk export: `GET /api/me/apps/timesheet/export/entries?limit=500`

## Pre-agreed seams and interface contracts
- **Package**: `internal/api`
- **CLI invocation and flags**:
  ```bash
  go run ./cmd/sync-timesheet [flags]
  ```
  - `-date`: Target date string in `YYYY-MM-DD` format (defaults to current date).
  - `-dry-run`: Boolean flag to simulate API calls without network mutations (default `false`).
  - `-storage-dir`: Custom persistent storage directory (overrides default `~/.timesheet/records`).
  - `-token`: Direct API token flag (overrides `TIMESHEET_API_TOKEN` env var).

- **Reconciliation algorithm (create and update existing logs)**:
  1. Load local `TimesheetRecord` for target date via `storage.TimesheetStore.GetDay(ctx, date)`.
  2. Fetch existing remote entries from Gradion API for target date via `GET /api/entries`.
  3. Diff local time blocks against remote entries:
     - **Update existing entry**: If matching time slot exists and description or classification changed, issue `PATCH /api/entries/{id}` with `reason: "Timesheet update sync"`.
     - **Create new entry**: If local time block has no matching remote entry, issue `POST /api/entries`.
     - **Identical slot**: If matching time slot and description are identical, skip with no-op log (idempotent acknowledgement).
     - **Superceded entry**: If a remote entry exists that was removed from the local timesheet, report or remove with confirmation.
  4. Write reconciliation status summary and update local metadata.

- **TigerStyle constants and bounds**:
  ```go
  const (
      MaxRemoteEntriesCount    = 200
      APITimeoutDuration       = 10 * time.Second
      MaxAPIRetriesCount       = 3
      BackoffBaseDuration      = 500 * time.Millisecond
  )
  ```

## Acceptance criteria
- [ ] Reads input record from Tier 2 persistent storage (`internal/storage`) rather than unverified scratch.
- [ ] Authenticates using Bearer token from `TIMESHEET_API_TOKEN` environment variable or CLI flag.
- [ ] Query-before-write logic prevents duplicate entries across re-runs.
- [ ] Accurately patches modified existing entries via `PATCH /api/entries/{id}` when user updates timesheet.
- [ ] Creates new entries via `POST /api/entries` when new blocks are introduced.
- [ ] Supports `-dry-run` to output planned REST / MCP mutations without sending network writes.
- [ ] Enforces TigerStyle bounded retry with exponential backoff on 5xx and rate limits.
- [ ] Achieves >= 90% unit test coverage using mock HTTP servers (`httptest.Server`).

## Verification commands
```bash
go test -v -race ./internal/api/...
go run ./cmd/sync-timesheet -date=$(date +%Y-%m-%d) -dry-run=true
```
