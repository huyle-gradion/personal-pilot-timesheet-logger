# Agent Instructions

## Agent skills

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
