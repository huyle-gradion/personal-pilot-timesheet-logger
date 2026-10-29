---
title: "Review-first timesheet redesign"
status: accepted
date: 2026-10-09
implementation_status: not-started
decision: "docs/adr/0005-review-first-timesheet-architecture.md"
---

# Review-first timesheet redesign

## Authority and scope

The owner confirmed this design after 52 interview questions on 2026-10-09. It replaces the original assignment requirements as the product baseline. Existing implementation and milestones are historical deliverables, not evidence that this redesign is implemented.

Source: the owner's private notes vault, followed by the owner's interview answers and final confirmation. Definitions live in [GLOSSARY.md](../GLOSSARY.md). The architectural replacement is recorded in [ADR 0005](adr/0005-review-first-timesheet-architecture.md), mirrored as vault ADR-006.

Target the owner's macOS environment and Gradion first. Additional evidence sources must be possible without building a general timesheet platform. Retain Go and reuse compatible components after assessing them against this specification. Documentation approval is not implementation authorization.

Priority order: correctness, ease of review, token cost and runtime. Optimize memory against concrete limits rather than theoretical scoring alone.

## Domain boundaries

Keep these records distinct:

1. Evidence with source identity, original timestamps, and provenance.
2. Topic Candidates and separated Activity Groups.
3. Proposed Time Blocks, including unknown actual duration and optional duration suggestions.
4. Confirmed Time Blocks with user-confirmed time, project, description, and applicable attendance.
5. Revision-specific Submission Plans and Approvals.
6. Remote Mappings and per-operation submission outcomes.

Evidence never automatically becomes confirmed working time. Confirmation is not submission approval. Gradion is authoritative for currently submitted entries; local records preserve workflow history and user decisions.

Exact schemas, file layout, and transport details require implementation planning. Do not treat the legacy SimpleEntry contract as the approved new evidence schema.

## Shared workflow

The standalone CLI and agent skill call one deterministic Go workflow. The CLI must work without an agent and may invoke a dedicated editorial agent when configured. Do not implement separate accounting or submission logic in the skill.

| Step | Owner | Behavior |
| --- | --- | --- |
| Preflight | Script | Validate timezone, enabled plugins, configuration, credential references, and integration availability. |
| Collection | Script | Collect local and remote evidence, deduplicate it, and fetch existing Gradion entries. |
| Proposal | Script | Form topic candidates and activity groups; propose intervals without confirming work. |
| Editorial synthesis | Optional AI | Propose supported titles and descriptions only. |
| Review | User through script | Confirm attendance, intervals, projects, descriptions, missing work, and collision handling. |
| Local save and export | Script | Persist the reviewed revision and update the managed daily-note section. |
| Submission preview | Script and user | Show exact creates and edits, intervals, descriptions, projects, and total hours; obtain approval. |
| Recheck and submit | Script | Reconcile fresh remote state and execute only approved operations whose assumptions still hold. |
| Outcome and metrics | Script | Persist each outcome, update export status, and report editorial and orchestration usage separately. |

Default to today in the configured timezone. Allow a date or date range, but review and approve each day independently. Split confirmed intervals at local midnight without losing minutes. One process at a time may modify or submit a day's record; a second writer stops clearly. Collection may proceed independently.

The guided terminal interaction that implements this table (interactive review,
authoritative classification, deliberate local-review confirmation, exact preview)
is specified in [the guided workflow note](agents/guided-workflow.md). Approval
and submission remain separate explicit commands; the guided run never approves or
submits.

## Evidence and proposal rules

### Calendar

Exclude cancelled events and invitations the user declined. Preserve invitation status, including unknown status. Accepted invitations are preselected as attended in review, not confirmed automatically. Present meetings together so the user can confirm attendance in bulk and edit exceptions.

iCalendar PARTSTAT indicates an invitation response, not actual attendance. Feed-specific attendee availability remains unverified. Reference: [RFC 5545 section 3.2.12](https://www.rfc-editor.org/rfc/rfc5545.html#section-3.2.12).

### Git and pull requests

Collect both local commits and remote PR activity with deduplication and visible provenance. Prefer repository plus PR identity for a Topic Candidate. Without a PR, repository plus branch is a reviewable grouping suggestion. Identical branch names in different repositories never establish one topic.

Use a recommended 45-minute adjacency gap to separate Activity Groups within a topic. Commits at 09:00 and 09:35 suggest a 35-minute span, while actual duration remains unknown. A 16:00 commit for that PR belongs to a separate group and has no duration suggestion by itself. Commits are activity points, not continuous working intervals.

Meetings interrupt inferred work. Even explicit agent session boundaries do not independently confirm the user's working time.

### Manual input and empty days

Ask once during review whether work is missing. Accept manual intervals with project and description as user-provided evidence. Gaps do not imply work. A day without evidence produces an empty draft, not an invented or zero-hour remote entry. If no work is supplied, save it locally as reviewed with no entries.

### Agent session evidence

Optional agent instrumentation emits structured start, pause, resume, and finish events with timestamps, project, topic, and session ID. Keep them outside narrative journal notes. They suggest possible user work only. Missing finish events remain incomplete; never extend them to day-end. Concurrent sessions do not add elapsed agent time to user working time.

Updating agent workflows to emit these events is a future implementation task, not part of this documentation change.

### Journal (narrative daily-note) evidence

The owner's Obsidian daily note (ai-database) may act as an OPTIONAL, local, read-only evidence source. It is supplementary: ai-database remains optional and is never the primary workflow store, and the adapter only ever reads `<vault>/<daily_dir>/<date>.md` (never writes the vault). See [ADR 0007](adr/0007-journal-evidence-source.md) and [the journal source note](agents/journal-source.md).

Narrative items are collected only from recognized sections (`Work journal`, `quick capture`, `Daily impact`), with their full heading ancestry retained. Checkbox tasks (even checked), `priorities`/`standup`/`todo`/`agent session` sections (the four skip tokens are contains matches on normalized heading text; `agent session` added under #85, see [ADR 0007's Amendments](adr/0007-journal-evidence-source.md#amendments)), fenced code, the exact export managed markers, and legacy `## Clustered timesheet` / generated `## Timesheet` sections are skipped; a malformed managed region fails the source closed so exported text never feeds back. Every journal item is UNTIMED: zero start/end, an UNKNOWN duration, and no classification, activity type, or inferred duration. A stated clock is never a working duration; the stated date is retained unchanged and a mismatch with the target date is warned, never rewritten. Project hints are non-billing suggestions only.

Journal identity is deterministic and reorder-stable; identical text in two different sections stays distinct. A successful COMPLETE collection marks prior items absent from the latest set as detached history (never deleted, never re-proposed); a failed or partial collection never detaches prior history. A manual interval may cite several supporting source records, which add NO minutes: the manual interval's own span is authoritative. Private journal text is never forwarded to an external editorial service.


## Time and review rules

Approved working intervals cannot overlap. During collisions, let the user attach evidence to one interval, adjust intervals, or exclude evidence. Meeting and commit evidence can share one entry; its submitted description mentions both activities without inventing a relationship or adding commit duration.

The 15-minute grid is a review convenience, not a required submission boundary. Preserve original timestamps, display proposed adjustments, and never silently expand time.

The owner states Gradion limits each entry to four hours. Split longer approved intervals into linked entries of at most four hours, preserving exact total minutes. Lunch and off-hours work are valid without special truncation. Verify actual remote validation before implementation.

Suggest projects from configured repository-to-Gradion mappings. Unmapped or ambiguous work requires user resolution. AI text and calendar titles cannot silently choose a billing project.

Unresolved duration, attendance, overlaps, project ambiguity, or remote disagreements block submission. One final daily approval confirms the exact intervals, attendance, projects, descriptions, and remote plan. It is tied to a record revision and permits no future automatic submissions.

## Storage and exports

Persist evidence, confirmations, descriptions, approvals, Remote Mappings, revisions, and submission outcomes separately. Refreshing evidence preserves confirmed intervals and user text. Show changed evidence separately; renew review only if the user incorporates it into the approved schedule.

Retain confirmed records, revisions, approvals, and submission outcomes until explicit removal. Raw evidence retention is configurable. Never automatically erase unresolved submission history.

Detailed evidence and reconciliation state stay outside the vault. Export a readable schedule after local review with `reviewed, not submitted` status. Update that same managed section after submission, including partial, failed, or unresolved outcomes. Preserve unrelated journal content. ai-database is optional and not the primary workflow store; as an optional read-only journal source it may supply narrative evidence (see [ADR 0007](adr/0007-journal-evidence-source.md)), but the exported managed section is never read back as evidence.

## Gradion reconciliation

Use the configured Workspace MCP endpoint directly from the CLI; the skill calls the same workflow. Avoid a second REST path unless MCP lacks a required capability, at which point revisit the design with the owner.

Endpoint: `https://workspace.gradion.com/api/mcp`. Authentication uses a Bearer credential referenced through macOS Keychain. Never include the secret in records, prompts, exports, logs, benchmarks, or documentation.

Remote entries are editable but not removable according to the owner. Never delete an entry or silently create replacements. Show correction plans using existing entry edits where possible. Additional creates require approval. Block corrections that require deletion or cannot preserve accurate, non-overlapping intervals.

Fetch remote state immediately before writing. If an affected entry changed since preview, invalidate the affected approval and show a new plan rather than overwrite it. Conditional-update support must be verified; without it a remote write race remains.

Persist each confirmed write and its stable remote ID. If two writes succeed and the third fails, retain their mappings and mark partial submission. Reconcile before requesting approval for remaining operations. Never blindly resend the day.

After an uncertain write, fetch remote state before considering a retry. If success cannot be established, stop and mark it unresolved. Do not assume time-slot equality is sufficient proof of identity or success. Exact remote matching rules depend on verified capabilities.

## Plugins and credentials

Use an explicit plugin registry. Enabled plugins declare evidence types, required configuration, and contract-check results. Unknown discovered programs are never automatically executed. Plugins collect evidence only; they cannot confirm work, approve submissions, submit entries, or alter another source's evidence.

Before enablement, check declared inputs, output schema, stable evidence IDs, timeouts, and secret redaction. Credential checks are separate from contract tests. Tests do not establish permanent availability or sandbox arbitrary executables. Give each plugin only the credentials it needs.

First-version sources: calendar, local git and remote PR activity, manual evidence, optional agent-session evidence, and an optional local read-only journal (narrative daily-note) source. Test extensibility with a fixture plugin. Defer Slack and issue trackers.

A failed source produces a visibly incomplete draft. Submission requires successful retry or explicit acknowledgment of the missing source. Never silently present partial collection as complete.

Store credentials in macOS Keychain using explicit service/account references. The supplied label `gradion.workspace.ApiKey` failed both service-only and account-only lookups during design. No secret was displayed, and MCP was not authenticated or tested.

## AI and benchmarks

AI may propose titles and descriptions only. It cannot change intervals, attendance, projects, evidence links, or approvals. Descriptions must reflect evidence; a commit does not prove shipment or improved performance.

Use a soft daily target of **1,000** editorial tokens/day, counting input, output, and cache tokens across retries and reruns. This is an explicit owner override of the specification's historical 600-token target (`internal/editorial/model.go`, a named alias of `workflow.EditorialTokenCeiling`). Reuse unchanged summaries. Budget exhaustion falls back to deterministic text or manual edits, never blocks review. Report orchestration separately and record unavailable usage honestly rather than as zero. Record runtime and memory measurements without treating them as proof of asymptotic complexity.

The legacy `benchmark.DefaultTokenCeiling = 600` is **deleted** (`internal/benchmark` now states no budget at all). The 1,000 target is defined ONCE as `workflow.EditorialTokenCeiling` and governs the editorial-child token lane only: harness-session tokens have no ceiling. `internal/benchmark`'s CSV token columns are copied from the day's workflow record (never parsed from a transcript), an unknown cell is empty and prints `unknown` rather than `0`, and `cmd/benchmark -ceiling` governs both the chart and the report's compliance line (amended 2026-10-20 by #69; see ADR-0006's Amendments).

*(Amended 2026-10-21 by #126: the second sentence's "harness-session tokens have no ceiling" still means no harness BUDGET — no number governs a session and none is invented. But the CHART now charts a day's session total, so `cmd/benchmark -chart` draws that number against the same 1,000 as an explicitly labelled REFERENCE line. The chart's source is the metrics store's day aggregates; `-report` keeps the CSV. See ADR-0006's 2026-10-21 amendment.)* *(Amended 2026-10-22 by #128: the CHART no longer draws a fixed 1,000 or any budget language. Its ceiling is DATA-DERIVED — the largest charted day with a known total, snapped UP to the next multiple of 1,000, with bars scaled proportionally — and `-ceiling` is only an explicit fixed override. `-report` still compares the editorial-child lane against 1,000. See ADR-0006's 2026-10-22 amendment.)*

The original bare-JSON and per-call token choices are not additional binding requirements. Choose and validate the editorial output contract during implementation without weakening the text-only boundary.

## Required verification

These are acceptance scenarios, not tests already executed:

1. Recollecting identical evidence creates no duplicate candidates.
2. Evidence refresh preserves confirmed intervals and descriptions.
3. Unknown duration or attendance prevents submission.
4. Meeting plus commit mentions both and counts time once.
5. Midnight and four-hour splits preserve exact total minutes.
6. Remote changes invalidate stale approval.
7. Partial and uncertain writes never cause blind duplicate creates.
8. Plugin failures and malformed outputs are visible.
9. Secrets never enter records, prompts, exports, or logs.
10. CLI works without AI; skill and CLI share the same core.

## Verification blockers

- Resolve the Keychain service/account reference without exposing the credential.
- Verify MCP listing, creation, editing, stable IDs, pagination, validation, and write-result semantics through discovery and read-only checks first. No production test writes are authorized.
- Verify conditional updates and uncertain-write reconciliation options.
- Verify Gradion project/classification fields and the four-hour validation rule.
- Inspect the configured calendar feed's invitation metadata without exposing its secret URL.

## Historical decisions replaced

This design contradicts or replaces repo ADRs 0001 through 0004 and vault ADRs 002 through 005 as the current architecture baseline. Retained ideas include Go reuse, deterministic accounting, optional vault export, and evidence extensibility. Replaced assumptions include inferred continuous working spans, old clumping rules, the mandatory whole-session token ceiling, stateless workflow claims, fixed time-slot reconciliation, and a mandatory dual REST/MCP implementation.

Historical milestone completion remains recorded. It does not establish compliance with this redesign. Implementation planning must assess reusable components and track gaps against this specification.
