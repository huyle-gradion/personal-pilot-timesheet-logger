---
name: timesheet
description: Log today's work into the personal timesheet from real git, pull-request and calendar evidence. Run once per day when the user asks to "log today's work" or "log the timesheet", or on request for a past working day.
---

# Timesheet daily log

This skill records one day's work as timesheet entries, built from real
evidence the compiled Timesheet binary collects: GitHub commits, pull
requests and reviews, and calendar events. Every deterministic step is a
fixed script subcommand of the binary; the only judgment step is reviewing
one compact draft. See `workflow.md` for the exact daily procedure.

The workflow is runner-agnostic: any agent runtime with shell access can
execute it, including Claude Code (which picks this folder up through the
`.claude/skills/timesheet` link) and the Pi agent.

## When to use

- The user asks to log today's (or a named past working day's) work.
- Once per day is the normal cadence; never twice for the same time block
  (re-running is safe: recording is idempotent per time block).

## Prerequisites

- The binary built from this repository: `go build -trimpath -o bin/timesheet ./cmd/timesheet`.
- A configuration file (timezone, state path, projects, optional sources).
- An environment file with credentials when GitHub/calendar sources are
  enabled (may be an empty file for local-only logging).

Never read, print, or copy the environment file's contents; only the binary
reads it, via `--env-file`.

## What this skill must never do

- No remote timesheet submission: approval-gated submission stays with the
  interactive human workflow (`pi/launch.mjs`), never with this skill.
- No editing of collected evidence, block times, or PR lists during review.
- No sending of anything except the day's draft JSON into the judgment step.
