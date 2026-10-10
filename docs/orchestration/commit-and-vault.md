# Commit and vault protocol

## Commit accepted source

1. Confirm commit authorization and independent acceptance of the exact content. Verify source ownership, branch ancestry, and dependency commits. Resolve changes made after review before staging.
2. Run the task's final verification on the intended snapshot. For Go slices, default to build, vet, full uncached tests, diff checks, and targeted race checks for changed concurrent behavior. Preserve failed output and explain unavailable checks.
3. Stage explicit accepted paths or hunks. Inspect both staged diff and untracked additions. Exclude private records, credentials, benchmarks containing private data, unrelated files, and generated artifacts not required by the slice.
4. Create a focused Conventional Commit. Verify its parent, contents, and remaining working-tree changes. Record the hash. A commit hook that changes source requires renewed checks and review of the resulting changes.
5. Push, publish a PR, or merge only under separately recorded permission. Read `docs/agents/issue-tracker.md` and `triage-labels.md` before tracker changes; use canonical labels. Close a ticket only when its acceptance criteria and required integration state are met.

Keep a dependent branch's ancestry explicit. Do not rewrite other agents' commits, force-push, reset, or delete worktrees as a cleanup shortcut.

Completion: commit contents match accepted source, final checks are recorded, and publication status is unambiguous. If changes are intentionally uncommitted, say so.

## Synchronize ai-database

Vault root: `/Users/huy.leminh/ai-database`. Read `99 - Meta/Database Schema.md` before creating or changing structured notes. The orchestrator owns these updates; workers leave them alone unless explicitly delegated.

1. Read only the relevant project and daily-note sections. Preserve metadata, journal content, historical milestones, and unrelated tasks.
2. Update `01 - Projects/Project - assignment-1.md` with slice status, accepted commit, verification level, blockers, and next planned step. Code committed is not product deployed or production-safe.
3. Append today's journal for significant features, resolved safety findings, or session closure. A minor commit need not create its own daily entry. Add a measured impact statement only when supported by evidence. Use `02 - Daily & Logs/YYYY-MM-DD.md` and the actual local date. Never invent hours, session timestamps, user working intervals, shipped impact, or metrics. Link the project, relevant people, and actual ADRs.
4. Create an ADR only for a meaningful architectural trade-off requiring durable explanation. Check next IDs separately in repo and vault; repo ADR 0005 maps to vault ADR-006. Keep cross-links explicit. A new implementation slice alone does not require an ADR.
5. Write tasks as `- [ ] Description [[@Assignee]] [due:: YYYY-MM-DD]` only with a real assignee and an agreed date. If no deadline is agreed, keep an unscheduled planning row and ask before promoting it to a dated task. Do not invent dates to satisfy the schema.
6. Record one idempotent completion entry per slice/commit in the project ledger; check for an existing entry before appending. Group minor commits into a session-closing daily summary rather than duplicate entries. Preserve raw evidence outside the vault and export only sanitized summaries.

Use valid YAML and sentence case headings. Avoid decorative emojis and em dashes. Preserve historical facts while correcting stale current status with dated evidence. Identify worker-reported results as such when independent artifacts are unavailable.

Completion: project and daily notes agree with the commit ledger, links and YAML are valid, and no secret/private evidence has been copied. If vault access fails, mark `vault-pending` and retry that update separately. Do not re-run source implementation or remote submissions because a documentation write failed.

## Release boundary

Production readiness requires evidence for the entire submission contract, not only this source slice. Document absent server-side idempotency and conditional updates as residual risks. A pre-write read reduces but does not eliminate the remote race.

Distinguish runtime checks available only at a real authorized submission from prerequisites that must be proven beforehand. Seek official server/admin documentation or safe read-only evidence for timezone and limit semantics before proposing a live pilot. If those facts remain unknown, require a specific owner decision; never discover them by silently billing test work.
