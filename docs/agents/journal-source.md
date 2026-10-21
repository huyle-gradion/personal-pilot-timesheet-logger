# Journal evidence source (`journal`)

This note describes the optional, local, read-only **journal** source: it reads
the owner's Obsidian daily note (ai-database) as supplementary narrative
evidence. It reflects the current CLI (`go run ./cmd/timesheet help`) and the
adapter in `internal/evidence/plugins/journal.go`. The architectural decision is
[ADR 0007](../adr/0007-journal-evidence-source.md).

ai-database was previously export-only. As a journal source it is a
supplementary evidence source, **never the primary workflow store**. The adapter
only ever reads `<vault>/<daily_dir>/<date>.md`; it never writes the vault.

Sanitized template: `cmd/timesheet/timesheet.journal.example.json` (contains no
secret values; `vault`/`daily_dir` are paths, not secrets).

## Configuration keys

A journal source is one entry in `evidence.sources` with `"type": "journal"`:

| Key | Meaning |
| --- | --- |
| `vault` | **Required** vault root path. Never a secret. |
| `daily_dir` | **Required** daily-note directory, **relative** to the vault, e.g. `02 - Daily & Logs`. An absolute path or a `..` escape is refused. |
| `fixture` | **Test-only** fixed note path for contract-mode verification. NEVER required at runtime. |
| `fixture_date` | **Test-only** contract-mode target date when the fixture has no frontmatter date. |

Rules:

- **Path confinement.** The reader builds `<vault>/<daily_dir>/<date>.md` and
  refuses an absolute or `..`-escaping `daily_dir`, an invalid date, a missing
  or non-directory vault, or a note whose **resolved** path leaves the vault
  through a symlink. Only `{date}.md` is read.
- **Visibly incomplete, never fatal.** A missing vault, a missing note, an
  unreadable note, a malformed (unterminated) managed-marker region, or
  duplicate explicit ids makes the source `incomplete` with a stable warning code
  (`JOURNAL_VAULT_MISSING`, `JOURNAL_NOTE_MISSING`, `JOURNAL_NOTE_UNREADABLE`,
  `JOURNAL_DAILY_DIR_INVALID`, `JOURNAL_MANAGED_MARKER_MALFORMED`,
  `JOURNAL_EXPLICIT_ID_DUPLICATE`). No warning ever embeds raw note text.
- **A present note with no recognized sections is a COMPLETE empty source.** It
  is not an error.
- **Contract-check codes:** `JOURNAL_VAULT_MISSING`, `JOURNAL_DAILY_DIR_INVALID`.

### Example config

```json
{
  "evidence": {
    "id": "default",
    "timezone": "America/Los_Angeles",
    "sources": [
      {
        "id": "journal-work",
        "type": "journal",
        "enabled": true,
        "config": {
          "vault": "~/vault",
          "daily_dir": "02 - Daily & Logs"
        }
      }
    ]
  }
}
```

## What is read

Narrative items are collected only from recognized headings, including nested
descendants (the full heading ancestry is retained):

- `Work journal`
- `quick capture`
- `Daily impact` / `Daily impact and brag bullets (for performance review)`

Decorative heading forms (e.g. an emoji prefix) are normalized. The following
are always skipped:

- checkbox tasks (`- [ ]` / `- [x]`), even when checked;
- `priorities`, `standup`, `todo`, and `agent session` sections. These four are
  CONTAINS matches on the NORMALIZED heading text (case and emoji decoration
  ignored), so `### Agent session log` is skipped whole; a hyphenated
  `Agent-session log` does not match, because normalization keeps hyphens but
  turns underscores into spaces;
- fenced code blocks;
- the exact `internal/export` managed markers
  (`<!-- timesheet:begin v1 -->` … `<!-- timesheet:end -->`); an **unterminated**
  managed region fails the source closed so exported text never feeds back;
- legacy `## Clustered timesheet` and generated `## Timesheet` sections. These
  match ONLY precise shapes (the exact `clustered timesheet` heading or the exact
  generated `Timesheet — <date>` heading), and skip phrases apply to SECTION
  headings (level ≥ 2), never to a level-1 note title. An arbitrary heading that
  merely contains `timesheet`/`todo` never silently empties the note.

The authoritative, code-grounded list of collected and skipped headings is
[the journal collection note](../journal-collection.md).

Markdown **multiline bullets** are supported: an indented continuation block
belongs to the same item. Text is retained unchanged. A narrow, fixed set of
whole-item authority/safety boilerplate strings and metrics-only entries are
excluded with a count warning. A metrics-only entry contains a number and only a
**bounded** unit/filler vocabulary (`tokens`, `ms`/`s`/`min`/`m`/`h`, `calls`,
`packages`, `tests`, `runs`, `files`, `%`, `$`, plus connectors) with numeric
separators; any other word (e.g. `reviewed`, `benchmark`, `shipped`) makes the
item substantive and it is retained.

## What is emitted

Every item is `SourceJournal` with:

- **zero `Start`/`End`** and an **UNKNOWN duration** — a note never establishes
  working time;
- no classification, activity type, or duration inference;
- additive `SourceRef` provenance: relative note path, full heading ancestry,
  section, inclusive line range, stated date, stated clock (`HH:MM`), and inline
  project links;
- project hints = union of frontmatter `focus_projects` (quoted wikilinks) and
  inline `[[wikilinks]]`. Hints are non-billing suggestions only.

The stated date is retained unchanged; a mismatch with the target date raises
`JOURNAL_DATE_MISMATCH:<target>` and preserves both. A quoted YAML date scalar
(`date: "2026-10-08"`) is normalized (surrounding quotes stripped) before
comparison, so it never raises a spurious mismatch; the raw value is never
rewritten in the note. A note whose frontmatter states no `date` raises
`JOURNAL_DATE_UNSTATED` ONCE for the whole note: the source stays `complete`, its
items are still collected, and the target date is never certified by silence. A
stated clock is never a working duration.

## Identity and history

- `JournalID(sourceID, date, explicitID, canonicalSection, contentHash)`:
  an explicit trailing `<!-- evidence_id: X -->` (bounded
  `[A-Za-z0-9._:-]{1,64}`) yields identity `sourceID`/`date`/`X` independent of
  section; otherwise identity is `sourceID`/`date`/**normalized full heading
  ancestry**/normalized content hash. Reordering is stable; two different
  headings that merely share a recognized phrase, and nested sections, stay
  distinct; editing an implicit item changes its identity. Duplicate explicit ids
  and any remaining duplicate **implicit** ids (`JOURNAL_IMPLICIT_ID_DUPLICATE`)
  fail visibly rather than being silently upsert-collapsed. The note path is not
  identity. A cosmetic heading edit (decoration/case that normalizes identically)
  keeps identity; a real heading text change changes the implicit identity, so
  the item detaches as history.
- **Active/detached.** `Evidence.JournalState` is additive (`""` = active). On a
  successful COMPLETE collection, prior journal ids for the same source/date that
  are absent from the latest set become `detached` (retained as history, never
  re-proposed); present ids are upserted active. A failed or partial collection
  never detaches prior history. A detached item is surfaced as advisory with
  human-readable provenance — title, relative note path, heading, inclusive line
  range, stated date, and reason — in the proposal's excluded entries, in
  `Proposal.Changed`, and in the CLI review output (a mechanical
  `excluded <id> reason=detached note=… heading=… lines=… date=… title=…` line),
  never only an opaque id or a bare count.

## Proposals, review, and privacy

- Active journal items appear as additive `Proposal.Narratives` (UNTIMED: no
  time fields, unknown duration). They never become timed work/meeting/manual
  blocks and do not change the time-block count.
- A manual interval may cite several supporting records
  (`decisions.manual[].evidence_ids`), which persist as
  `Evidence.LinkedEvidenceIDs` and propagate to the proposed manual block and
  every four-hour split fragment. Linked evidence adds NO minutes; the manual
  interval's own span is authoritative. Linked ids must exist and must not form
  a cycle.
- Private journal text is **never forwarded** to an external editorial service,
  including a manually linked journal description. The editorial request carries
  no journal or manual facts.

## Command sequence

The journal source needs no credential and no fixture. It participates in the
normal `review` / `run` collection:

```bash
# review collects the journal note alongside any other configured sources.
go run ./cmd/timesheet review --config ./timesheet.journal.json --date 2026-10-06 --no-editorial
```

An incomplete journal source blocks `preview`/`submit` with
`incomplete_collection` until acknowledged (`--ack journal-work`).
