# ADR 0002: Stateless, Modular Collector Architecture and Flexible Timesheet Sync

## Status
Superseded by [ADR 0005](0005-review-first-timesheet-architecture.md) on 2026-10-09. Retained as historical context.

## Context
Following the completion and review of Milestone M0 (`feat/calendar-fetcher`), several core architectural requirements have been refined:
1. **Stateless & Harness-Agnostic Workflow**: The skill and workflow must not assume or hardcode a specific vendor (Claude-only), model family, or execution harness. It must run seamlessly in Antigravity (`agy`), Claude Code, Cursor, or POSIX subagents.
2. **Modular Pluggable Collectors**: Non-developer users or different teams should be able to plug in custom tools (e.g. Jira, Slack, Linear) or unplug the GitHub collector without breaking downstream clustering, collision detection, and submission.
3. **Flexible Date Handling**: The pipeline must default to the current local day (`today`), support an explicit single date (`--date YYYY-MM-DD`), and handle multi-day date ranges (`--from ... --to ...`).
4. **Enhanced Terminal Review UX**: Interactive review and collision resolution (Step 5) requires clear visual cues and form controls, ideally leveraging Charm libraries (`charmbracelet/huh` and `charmbracelet/lipgloss`).
5. **Dual Packaging (Skill + Standalone CLI)**: While the primary deliverable is a harness-agnostic AI skill & workflow, the deep Go core modules can also be compiled into a standalone, zero-friction CLI binary (`timesheet-sync`) to achieve minimal token consumption and automated headless operation.

## Decisions

### 1. Harness-Agnostic Skill Specification
- All agent directives, `SKILL.md`, and `workflow.md` files will avoid vendor-specific proprietary tool calling hooks or prompt wrappers.
- The pipeline contract relies purely on POSIX stdin/stdout, environment variables (`.env`), and standard JSON data interchange.

### 2. Pluggable Collector Seam (`SimpleEntry`)
- Any collector plugin executable (Go binary, shell script, or Python utility) is valid if:
  1. It accepts standard flags: `--date YYYY-MM-DD` (and `--timezone`).
  2. It outputs a normalized JSON array conforming to `[]SimpleEntry` on stdout.
- An orchestrator flag or environment variable (`COLLECTORS="cmd/fetch-calendar,cmd/fetch-git"`) dictates active collector plugins. Unconfigured or missing collectors are safely skipped.

### 3. Date Window Processing
- Date flags default to `time.Now().Format("2006-01-02")` in the local system timezone.
- For multi-day date ranges (`--from YYYY-MM-DD --to YYYY-MM-DD`), the orchestrator iterates day-by-day, isolating each daily batch to ensure collisions and time blocks never leak across midnight boundaries.

### 4. Interactive Review UX with Charm
- In `cmd/review-timesheet` (M2-02), use `charmbracelet/lipgloss` for rendered timeline ASCII tables and `charmbracelet/huh` for interactive collision pickers (`[Merge | Keep Calendar | Keep Git | Discard]`).
- Fall back gracefully to standard ANSI / plain text prompts when non-interactive or when `CI=true` / `--non-interactive` is detected.

### 5. Architectural Co-Location: Deep Core, Two Frontends
- Business logic lives in deep Go modules (`internal/calendar`, `internal/git`, `internal/timeblock`, `internal/reconcile`).
- **Frontend 1 (Main Deliverable)**: Agent Skill & Workflow invoking the individual CLI steps with LLM-based summarization.
- **Frontend 2 (Side-Quest CLI Binary)**: Single compiled CLI (`cmd/timesheet-cli`) packing collection, LLM API call (or template summary), review, and Gradion API submission into a single command.

## Consequences
- Decouples project tooling from proprietary AI harnesses.
- Broadens timesheet logging utility to non-engineers and non-GitHub teams.
- Preserves a clean seam for testing and benchmarking across single-day and multi-day workloads.
