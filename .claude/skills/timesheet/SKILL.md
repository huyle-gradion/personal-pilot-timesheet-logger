---
name: timesheet
description: Automates daily timesheet aggregation, deterministic focus-span clustering, AI editorial synthesis, interactive collision review, and idempotent Gradion API synchronization. Trigger with /timesheet, log timesheet, or daily timesheet.
---

# Timesheet Skill (`timesheet`)

Orchestrates the end-to-end timesheet logging pipeline for knowledge workers and software engineers. Aggregates activity streams across Google Calendar private iCal feeds and GitHub/Git commits, deterministically clusters activity into focused work blocks, performs minimal AI editorial refinement (< 600 tokens/day), provides terminal review and conflict resolution, and reconciles entries with the production Gradion Timesheet API.

## Triggers and invocations

Invoke this skill when the user issues any of the following triggers:
- `/timesheet`
- `log timesheet`
- `daily timesheet`
- `review timesheet`
- `sync timesheet`

### Parameter options

- `--date <YYYY-MM-DD>`: Target date for timesheet aggregation (defaults to current date).
- `--from <YYYY-MM-DD> --to <YYYY-MM-DD>`: Multi-day batch aggregation window.
- `--non-interactive`: Headless review mode bypassing interactive prompts.
- `--auto-resolve <keep_both|separate|discard>`: Automatic collision resolution strategy in non-interactive mode (default: `keep_both`).
- `--dry-run`: Simulates API reconciliation without committing remote mutations to Gradion.
- `--approve`: Temporary mutation gate; required to execute a non-dry-run sync. It is *not* approval tied to an exact preview or to a local record revision — see `workflow.md` Step 6. Production submission remains not ready.
- `--sink <console|obsidian|all|none>`: Exporter destination (default: `obsidian`).
- `--storage-dir <path>`: Custom Tier 2 persistent JSON archive directory (default: `~/.timesheet/records/`).

## Pre-flight configuration check (ADR-005)

Before executing the pipeline steps, verify that necessary credentials exist. Per [[ADR-005 - Review-First Timesheet Architecture]] and `docs/agents/credentials.md`, the macOS Keychain is the authoritative secret store and configuration holds references only. Each collector resolves its secret in this order (highest priority first):
1. Explicit CLI flag (transient override for tests/CI only)
2. macOS Keychain item, account `$USER`: `calendar_feed_url`, `gh_auth_token`, `gradion.workspace.ApiKey`
3. Legacy environment variables / project `.env` (`GCAL_ICS_URL`, `GITHUB_TOKEN`/`GH_TOKEN`, `TIMESHEET_API_TOKEN`) — **deprecated**; do not store new secrets there

The service `gradion.workspace.ApiKey` supersedes the earlier `timesheet_api_token` name.

If any required secret is missing, do not proceed with blind execution. Prompt the user with explicit instructions to create the Keychain item via `security add-generic-password`.

## Execution workflow

Refer to [workflow.md](file:///.claude/skills/timesheet/workflow.md) for the exact 7-step sequence. Every step is strictly delineated as either deterministic Go code (`[Script]`) or minimal language model refinement (`[AI]`):

1. **Step 1: `[Script]` Calendar Ingestion** (`cmd/fetch-calendar`)
2. **Step 2: `[Script]` Two-Tier Git Collection** (`cmd/fetch-git`)
3. **Step 3: `[Script]` Time Block Clustering** (`internal/timesheet/clusterer`)
4. **Step 4: `[AI]` Editorial Synthesis** (Compressed JSON topic & description refinement)
5. **Step 5: `[Script]` Collision Review & Storage** (`cmd/review-timesheet`)
6. **Step 6: `[Script]` Gradion API Reconciliation** (`cmd/sync-timesheet`)
7. **Step 7: `[Script]` Empirical Benchmark Logging** (`cmd/benchmark`)
