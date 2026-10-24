# R6 macOS verification publication

## Explicit owner authority

Owner request: **“push to a branch that i can pull to the agent on macos with pi can run it. also remove the empty directory that you deleted every files inside it”**.

This explicitly authorizes a scoped local commit/push of the current R6 candidate and removal of genuinely empty directories left by its retired paths. It is an exception to the normal acceptance-before-publication gate **for platform verification only**, not acceptance, merge, release, or production readiness. R6-F1/F2 remain open; actual macOS proof is still required. No source/product-document repair is silently performed as part of publishing.

## Branch and exact content

- Publish `feat/timesheet-rewrite-r6-v0.1.0`, based on R5 `99a7f23336845ac6858706319e695a750f9116cd`. No main/earlier branch changes, force push, PR creation, merge or tag.
- Stage only the 225 authorized retired-file deletions, twelve bounded/replacement R6 source/doc/module changes, new root README, and seven new R6 orchestration artifacts: original handoff, machine inventory, compact inventory, review, FIX1 packet, macOS packet and this record.
- Exclude the three pre-existing owner-governance diffs in `.omo/plans/timesheet-rewrite.md`, `docs/orchestration/README.md`, `docs/orchestration/timesheet-rewrite-r4-acceptance.md`; leave private `transcript_full.jsonl` unread/unstaged. No `.env.local`, real config/records/profile, fixture, dependency directory or Linux executable is committed.
- The published macOS task is `timesheet-rewrite-r6-macos-handoff.md`. It is self-contained: older inherited governance task pointers and Linux `/tmp` references are historical, not reasons to search outside its scope. Only a native Mac build can run there.

## Cleanup and proof

Empty-directory cleanup uses only parent paths derived from the retired tracked-file inventory and `rmdir`, deepest first. No recursive deletion or symlink traversal; nonempty directories survive. Git does not represent empty directories, so the local cleanup adds no placeholder files to the commit.

Before commit, reconcile the candidate against the 41-source reviewed snapshot, exact retirement inventory and binary identity. Verify exact staged paths/blobs and commit ancestry. Record actual commit/remote head and cleanup counts in issue #137; do not embed a self-referential commit hash here.

Source identity remains dev.17/protocol 1/state 3. Linux reviewed executable SHA-256 `dc3dbf5054a17d71108aee9f7285e1ca832f285a5f63f2d277da743b840a738a`; it is not the expected hash of a native macOS binary. Review evidence `/tmp/timesheet-r6-review-g99in8nl/` remains Linux-local provenance, not a portable prerequisite.

Known findings: README incorrectly claims staging survives restart and broadly claims extra arguments exit 2; FIX1 documents the precise correction. Publication keeps these findings explicit instead of presenting the candidate as accepted. macOS verification must retain fresh bounded runtime evidence and return for independent review; live account/model authentication and all production writes remain separately unauthorized.
