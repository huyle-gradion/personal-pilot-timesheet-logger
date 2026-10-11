---
status: accepted
date: 2026-10-11
deciders:
  - Huy Le Minh
related:
  - 0005
---

# Journal evidence source

## Context

ai-database (the owner's Obsidian vault) was previously **export-only**: the
workflow wrote a managed timesheet section into a daily note but never read it
back. The owner wants the daily note's narrative journal to act as an OPTIONAL,
local, read-only **evidence** source so that manually recorded work can support a
timesheet entry. The redesign ([0005](0005-review-first-timesheet-architecture.md),
[the specification](../timesheet-redesign.md)) keeps evidence, proposals,
confirmations, approvals, and submissions distinct, and states that
**ai-database is optional and not the primary workflow store**.

A narrative note is the riskiest evidence source: it is free-form, it contains
private text, it can contain task checkboxes and exported timesheet sections, and
it never states a working duration. Treating it carelessly would either
(a) fabricate working time from prose, (b) leak private text to an external AI
service, or (c) feed an exported timesheet back into itself.

## Decision

Add one compile-time evidence plugin, `journal` (`internal/evidence/plugins/journal.go`),
registered explicitly in the CLI registry. It is a **supplementary, read-only**
evidence source: the vault is never written by it, and ai-database is never the
primary store.

- **Explicit, confined configuration.** A journal source requires `vault` (the
  vault root) and `daily_dir` (a RELATIVE directory, e.g. `02 - Daily & Logs`).
  An absolute or `..`-escaping `daily_dir`, or a resolved path that leaves the
  vault through a symlink, is refused. Only `<vault>/<daily_dir>/<date>.md` is
  ever read. `fixture` is an optional contract-mode test input.
- **Narrative, UNTIMED evidence.** Every emitted item is `SourceJournal` with a
  zero `Start`/`End` and an UNKNOWN `Duration`. A note never establishes working
  time; no classification, activity type, or duration is inferred.
- **Recognized sections only.** Items come from recognized narrative headings
  (`Work journal`, `quick capture`, `Daily impact` / `Daily impact and brag
  bullets (for performance review)`), including nested descendants with their
  full heading ancestry. Checkbox tasks (even checked), `priorities`/`standup`/
  `todo` sections, fenced code, the exact `internal/export` managed markers,
  and legacy `## Clustered timesheet` / generated `## Timesheet` sections are
  skipped. The generated/legacy timesheet skip matches ONLY precise shapes (the
  exact `clustered timesheet` heading or the exact generated `Timesheet — <date>`
  heading), and skip phrases apply to SECTION headings (level ≥ 2), never to a
  level-1 note title: an arbitrary heading that merely contains `timesheet`/`todo`
  never silently empties a note. A malformed (unterminated) managed region fails
  closed so exported text can never feed back.
- **Narrow, fixed exclusions.** A small fixed set of whole-item authority/safety
  boilerplate strings and metrics-only entries are excluded, with a count
  warning. A metrics-only entry contains a number and only a BOUNDED unit/filler
  vocabulary (`tokens`, `ms`/`s`/`min`/`m`/`h`, `calls`, `packages`, `tests`,
  `runs`, `files`, `%`, `$`, plus connectors) with numeric separators; any other
  word (e.g. `reviewed`, `benchmark`, `shipped`) makes the item substantive and it
  is retained. Substantive agent-authored docs/review/research/benchmark work is
  therefore never excluded.
- **Stable identity.** `JournalID` distinguishes an explicit identity
  (`sourceID`/`date`/`X` from a bounded trailing `<!-- evidence_id: X -->`,
  independent of section) from an implicit one (`sourceID`/`date`/NORMALIZED FULL
  heading ancestry/normalized content hash). Reordering is stable; two different
  headings that merely share a recognized phrase, and nested sections, stay
  distinct; editing an implicit item changes its identity. Duplicate explicit ids
  and any remaining duplicate IMPLICIT ids fail visibly rather than being silently
  upsert-collapsed. The note path is NOT identity. A cosmetic heading edit
  (decoration/case that normalizes identically) keeps identity; a real heading
  text change changes the implicit identity, so the item detaches as history.
- **Additive provenance.** `SourceRef` gains relative note path, full heading
  ancestry, section, inclusive line range, stated date, clock, and inline
  project links. Project hints union frontmatter `focus_projects` and inline
  wikilinks. A quoted YAML date scalar is normalized (surrounding quotes
  stripped) before comparison so a quoted frontmatter date never raises a
  spurious mismatch; the raw value is never rewritten in the note. The stated
  date is retained unchanged and a mismatch with the target date is warned,
  never rewritten.
- **Active/detached history.** `Evidence.JournalState` is additive (`""` =
  active). On a successful COMPLETE authoritative journal collection, prior
  journal ids for the same source/date that are absent from the latest set are
  marked detached (history retained, never deleted, never re-proposed); present
  ids are upserted active. A failed or partial collection never detaches prior
  history, and no positional "superseded" guess is made. A detached item is
  surfaced as advisory with human-readable provenance (title, relative note
  path, heading, line range, stated date, reason) in the proposal's excluded
  entries, in `Proposal.Changed`, and in the CLI review output — never only an
  opaque id or a bare count.
- **Narrative proposals.** `proposal.Proposal` gains additive
  `Narratives []NarrativeCandidate` (id/section/date/title/description/evidence
  ids/project hints/unknown duration, NO time fields). Detached items are
  visible as excluded history.
- **Manual linkage.** A manual interval may cite several supporting source
  records via `Evidence.LinkedEvidenceIDs` (`ManualDecision.EvidenceIDs`,
  `review.ManualInterval.EvidenceIDs`). Linked ids must exist and must not form
  a cycle. Linked evidence adds NO minutes: the manual interval's own span is
  authoritative, and it propagates to the proposed manual block and every
  four-hour split fragment without losing provenance.
- **Privacy.** Private journal text is never forwarded to an external editorial
  service, including a manually linked journal description: the editorial request
  carries no journal or manual facts, and a source-aware guard enforces it.

## Alternatives rejected

- **Parse the vault with a general Obsidian/markdown library**: rejected. The
  journal source is a narrow, fixed-shape reader; a general parser would add a
  dependency and broaden the blast radius for a single note shape.
- **Make ai-database the primary store or write the vault**: rejected; the
  redesign keeps the vault optional and export-only except for this read.
- **Infer duration from note prose or timestamps**: rejected; a stated clock is
  not a working duration.
- **Delete or rewrite superseded journal items**: rejected; history is retained
  and only marked detached.

## Consequences

The CLI and the agent skill keep one shared core; the journal source adds no new
subcommand. The exact authority/safety boilerplate strings must be confirmed by
the owner against the real vault (the current set is deliberately small). The
vault mirror ADR id for this record is not assigned here; the parent verifies it
later. No UI or downstream (B) dependency is added.
