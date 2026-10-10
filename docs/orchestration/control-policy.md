# Control policy

## Authority ledger

At takeover, record the owner's exact authorization and these separate scopes in the session state:

| Scope | Default | Required authorization |
| --- | --- | --- |
| Read repo and run hermetic local checks | Allowed for an assigned review or planning task | Stay within the assigned snapshot and scope |
| Implement or fix source | Not implied by documentation approval | Named slice, allowed files, and acceptance criteria |
| Dispatch workers directly | Owner-mediated prompts | Explicit direct-dispatch mode |
| Create local branch/worktree | Only for an authorized code slice | Preserve existing work and record ancestry |
| Commit | Review acceptance does not itself grant authority | Owner grants commit scope for the accepted slice |
| Push, create/edit issues or PRs, merge | Off | Separate publication/integration permission |
| Read Keychain or live services | Off | Named credential reference, service, read scope, and sanitization plan |
| Modify Keychain | Off | Explicit item-specific owner permission |
| Write Gradion entries | Off | Readiness gates plus exact user-approved daily Submission Plan |
| Update ai-database | Off until granted for the session | Explicit session grant; follow schema and preserve unrelated notes |

Remote timesheet approval is a product operation, not an orchestrator's permission to commit code. A `-approve` flag is not proof of revision-bound approval. Avoid real mutations even if the current binary permits them.

Once granted, vault synchronization covers accepted-slice records and significant-session summaries within that session. Record the grant in the authority ledger; do not ask again for each routine update or carry it silently into a different session.

## Takeover and inventory

1. Inspect branch, HEAD, ancestry, tracked changes, and untracked paths. Classify work by slice; unknown ownership is a blocker to staging it.
2. Read the latest state ledger, reports, and acceptance artifacts. Label claims as owner-reported, worker-reported, independently verified, or unresolved.
3. Identify the active writer and pending reviewer. A resumed session must not spawn a second writer for an already claimed scope.
4. Reconcile stale documentation against dated evidence without treating schema-declared capabilities as observed behavior.

Completion: the active scope, current snapshot, authority, ownership, and next gate are recorded. Stop on ambiguous source ownership or unexpected changes.

## Slice lifecycle

`planned -> authorized -> claimed -> implementing -> review-ready -> reviewing -> fixing -> reviewing -> accepted -> committed -> vault-synced -> closed`

`blocked` can interrupt any pre-close state. A slice may be accepted without permission to commit; keep those gates distinct. Failed vault synchronization produces `committed, vault-pending`, not a source rollback. Source changes after review return the affected scope to `review-ready`.

| Gate | Checkable completion criterion |
| --- | --- |
| Authorized | Owner scope and permissions recorded; no guessed deadlines |
| Claimed | One writer owns an exact branch/worktree and file set |
| Review-ready | Full diff, untracked implementation files, tests, and limitations delivered |
| Accepted | Independent review covers the current snapshot; blockers resolved or shown not applicable with evidence |
| Committed | Explicit staged diff matches the accepted snapshot; commit and ancestry verified |
| Vault-synced | Project/log updates link evidence and distinguish code acceptance from production readiness |
| Closed | Commit, unresolved risks, and next dependency recorded; publication status explicit |

## Worker control

Give workers a task packet, not the whole chat transcript. Assign one task, one owner, one branch, and one focused PR if publication is authorized. Do not branch from `main` automatically when dependencies exist on an unpublished branch; record the intended base and dependent commits first.

Serial writers are the default. Parallel work is allowed only in isolated worktrees with non-overlapping ownership and stable dependencies. Parallel read-only reviews may inspect the same frozen snapshot. Integrate one lane at a time, rerun combined checks, and review integration changes before committing.

In owner-mediated mode, issue one next prompt and wait for its report. In direct mode, track session IDs and ask states; route permission requests through the authority ledger. Use host completion notifications rather than polling where supported. Never assume a tool named in another environment exists here.

## Review and fix loop

For each finding record ID, severity, location, failure scenario, evidence, proposed fix, and disposition. Blocking findings include secret exposure, lost confirmed work, duplicate/unauthorized writes, stale approval execution, corrupt identity, and false success reporting. The orchestrator evaluates impact even if a reviewer calls it a nit.

Fix accepted findings first, then obtain a focused independent review of changed behavior and regression tests. Disputes require a reproduction or documented constraint; do not vote between agents. After two fix rounds without convergence, stop for owner/technical-advisor input with the remaining evidence.

Green tests do not waive findings. Owner acceptance of a bounded slice does not waive the no-production-write boundary. Reversible non-safety limitations may be carried with explicit rationale and follow-up ownership.

## Stop conditions

Stop on exposed secrets, unexpected remote effects, mismatched review snapshots, unavailable authority, unexplained file changes, uncertain remote writes, or failing durability guarantees. If a secret is exposed, contain the output and notify the owner without repeating it; request item-specific rotation authority. Do not automatically edit Keychain items.

Record unavailable checks as not run, with reasons. Missing access is not a pass. Record flaky checks as failures with reproduction evidence, not as green after an unexplained retry.
