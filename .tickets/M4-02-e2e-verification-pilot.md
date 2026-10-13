# [M4-02] End-to-End Integration Verification & 30-Day Simulation

## 🎯 Vertical Slice
Implement the comprehensive end-to-end integration test suite in `tests/e2e_test.go` and run a full multi-day simulated pilot execution to verify the entire pipeline (DoD 1 through DoD 6).

## 🔗 Blocking Edges
- **Blocked by**: `[M4-01]`
- **Blocks**: None (Final Project Verification)

## 📖 Context & Specification
- **Spec Reference**: Section 1 (Overview) & Section 4 (Grading Rubric).
- **Verification Objectives**:
  - Run pipeline end-to-end against mock servers (mock Google Calendar iCal, mock GitHub API, mock Gradion Timesheet API).
  - Verify complete workflow execution under both normal and GitHub degraded conditions (Tier 2 local git fallback).
  - Confirm zero duplicate entries when pipeline is run repeatedly for the same day (DoD 3.3).
  - Confirm benchmark logging records wall-clock time, RSS memory, and token consumption to `benchmarks/metrics.csv`.

## 📐 Pre-Agreed Seams & Interface Contracts
- **Test Invocations**:
  ```bash
  go test -v ./tests/... -tags=e2e
  ```

## ✅ Acceptance Criteria (DoD)
- [ ] End-to-end integration test runs the full 6-step pipeline in headless mode.
- [ ] Verifies idempotency: running twice produces identical state in mock Gradion API.
- [ ] Verifies GitHub outage resilience: pipeline succeeds even when GitHub returns HTTP 503.
- [ ] Validates `timesheet/YYYY-MM-DD.md` output formatting.
- [ ] Validates `benchmarks/metrics.csv` row creation and token accounting.

## 🧪 Verification Commands
```bash
go test -v -race ./...
```
