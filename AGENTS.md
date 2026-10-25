# Agent Instructions

## Publication and versioning

- Use GitHub account `huyle-gradion` and repository-local Git identity `Huy Le <huy.leminh@gradion.com>`. Verify author, committer and authenticated account before publication; keep global Git settings unchanged.
- Use **Timesheet** as the public product name and neutral product terminology in source, documentation and release metadata.
- Every shipped application changeset updates SemVer consistently across Go, adapter negotiation, package/root-lock stamps, changelog and release tag.
- Preserve published commits and tags unless the owner explicitly authorizes changing history.

## Agent skills

### Orchestration

When coordinating implementation, review, fixes, commits, or vault updates, start at
`docs/orchestration/README.md`. It defines authority, worker handoffs, review gates,
and the staged redesign plan. A plan is not permission to implement or submit work.

### Issue tracker

GitHub issues via `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical 5-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context repo (`GLOSSARY.md` + `docs/adr/`). See `docs/agents/domain.md`.

### Keychain credentials

Owner wants all three pipeline credentials (Gradion Workspace MCP, GitHub token,
iCal URL) stored in the macOS Keychain, with code reading from there. See
`docs/agents/credentials.md`.

## Knowledge base integration

- **Vault**: `/Users/huy.leminh/ai-database`
- **Project Note**: `/Users/huy.leminh/ai-database/01 - Projects/Project - assignment-1.md`
- **Architecture Decisions**: `/Users/huy.leminh/ai-database/05 - Architecture Decisions/`
- **Daily Logs**: `/Users/huy.leminh/ai-database/02 - Daily & Logs/`

### Knowledge base operating rules

1. **ADR & Architecture checks**: Consult `Project - assignment-1.md` and related ADRs before making major architectural changes. Record formal decisions as ADRs in `05 - Architecture Decisions/`.
2. **Project tracking**: Keep `Project - assignment-1.md` updated with completed milestones and active tasks formatted as `- [ ] Task [[@Assignee]] [due:: YYYY-MM-DD]`.
3. **Daily impact logging**: When completing significant features or closing sessions, log standup summary and quantified accomplishment bullets ("Accomplished [X] as measured by [Y] by doing [Z]") into today's log in `02 - Daily & Logs/`.

## Timesheet daily log (skill)

When asked to "log today's work" or "log the timesheet" for a day, follow
`.agents/skills/timesheet/SKILL.md` exactly: run the binary's `run`,
`record` and `status` subcommands as the [Script] steps, perform
only the judgment step (per-block grouped-topic descriptions, ambiguous
events, completeness) on the draft JSON, and record token usage with
`scripts/tokens.py`. The skill can also carry a saved day headless through
`stage`, `classifications`/`classify`, `confirm`, `save`, `plan` (plus
`reconcile`/`resolve` for identity blockers) and read-only `submit-status`.
It is runner-agnostic (Pi or any agent with shell access); it never runs the
TTY-gated `approve`, `submit` or `resolve-write` — those need an interactive
terminal and the exact typed `plan_id`/`approval_id`/`write_ref`, and
attempting them is a contract violation. Submission is never auto-retried;
unknown outcomes stay unresolved until a human resolves them.
