---
status: accepted
date: 2026-10-09
deciders:
  - Huy Le Minh
supersedes:
  - 0001
  - 0002
  - 0003
  - 0004
vault_mirror: ADR-006
---

# Review-first timesheet architecture

The owner replaced the original assignment requirements because activity aggregation produced an incorrect product model: evidence was too easily treated as working time. We choose one deterministic Go workflow for CLI and skill, with separate evidence, proposals, user confirmations, revision-specific approvals, and remote submission outcomes. This trades automatic completion for explicit review because inaccurate entries cannot be deleted remotely.

## Alternatives and outcome

- Reject automatic duration confirmation from commits, calendar acceptance, or agent runtime. They indicate possible activity, not proven working time.
- Reject independent agent and CLI pipelines. They would duplicate accounting and approval rules.
- Reject blind create retries and silent remote replacements. Uncertain writes must remain unresolved until reconciliation establishes their outcome.
- Prefer direct Workspace MCP integration over maintaining two submission implementations. This is conditional on verified reading, creating, editing, and stable remote identity support. Revisit with the owner if capabilities are insufficient.

## Consequences

Local workflow history persists confirmations, revisions, approvals, and Remote Mappings. Gradion is authoritative for currently submitted records. Refresh preserves user decisions; AI changes text only. Remote changes invalidate affected approvals, and partial submission is a first-class outcome. The full agreed requirements and acceptance scenarios live in [the redesign specification](../timesheet-redesign.md).

This supersedes ADRs 0001 through 0004 as the current baseline, not as an instruction to discard compatible code. Go and working collectors may be reused after assessment. Credential retrieval and MCP capabilities remain unverified. Documentation approval does not authorize implementation or production writes.
