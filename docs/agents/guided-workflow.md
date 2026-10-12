# Guided workflow (`timesheet run --interactive`)

This note describes the guided, terminal-first `run` and the exact command
sequence around it. It reflects the current CLI (`go run ./cmd/timesheet help`)
and the shared core in `internal/run`. Product requirements stay in
[the redesign spec](../timesheet-redesign.md); the git/journal source keys are in
[collection configuration](collection-config.md).

Sanitized offline template:
`cmd/timesheet/timesheet.personal.example.json` (journal + local-only git, no
credential, no remote). It uses placeholder paths and contains **no** secret
values.

## What the guided run is

`timesheet run --interactive` is the same deterministic pipeline as `run`, with a
review step that asks the user for the day's decisions. It is:

preflight -> collect -> proposal -> optional editorial -> staged interactive
review -> authoritative classification -> deliberate local-review confirmation ->
exact submission preview -> export.

It **never** approves or submits. Approval and submission stay separate, explicit
commands (`approve --plan`, `submit`). The guided run prints the exact operations,
the total, and a single `approve --plan <PLANID>` follow-up. It does not print a
submit instruction: production submission is not performed or authorized here.

A guided run is NOT inherently hermetic. It runs the same stages as `run`
(preflight, collection, proposal, editorial, preview, export), so its live
behavior follows the CONFIGURED sources: a calendar source fetches its ICS feed
and resolves its `keychain:` URL reference, a remote git source contacts GitHub,
and a submission preview performs a read-only remote re-observe when a Gradion
endpoint is configured. `--offline` skips ONLY the Gradion remote; source fetches
still run (see [collection configuration](collection-config.md)). Only the
hermetic HARNESS test below is free of live services.

## Binary provenance for an interactive session

Run a **compiled binary** for a guided session: build once with
`go build -o ./bin/timesheet ./cmd/timesheet`, then invoke
`./bin/timesheet run --interactive` (or `--plain`). `go run ./cmd/timesheet`
recompiles and links the program on every invocation, so a guided, multi-step run
pays that build cost at each call; build once and reuse the binary. This is a
build/provenance point only — it changes no command semantics.

## Interaction modes and precedence

`run` picks one of three modes. Precedence, highest first:

1. `--decisions FILE` (or `--non-interactive`): the scripted path, unchanged.
2. `--plain`: the safe line-based path (works on a pipe; EOF aborts).
3. `--interactive`: the huh terminal UI, only when **both** stdin and stdout are
   real terminals and the environment is not CI. Otherwise the run **refuses**
   and points at `--plain`/`--decisions`; it never silently falls back.
4. Default (no flag): scripted, so existing headless behavior and exit codes are
   unchanged.

Terminal detection reads the actual descriptors (an ioctl on stdin and stdout), so
a piped or `tee`d stdout is not a terminal. `--plain` is allowed on a pipe and
under CI because it is the safe path. `--plain` and `--interactive` apply to `run`
only. The guided run reviews one day at a time; a `--from/--to` range is refused
with a clear message (use `--date`, or the scripted path, for a range).

## What is staged, and when it is written

Every review choice (confirm, exclude, join, trim, project, description,
attendance, manual interval, acknowledge, classification) is held in memory and
applied in ONE atomic `ApplyReview` only after the deliberate final confirmation.
Cancelling (Ctrl-C, EOF, or a form error) at any prompt applies nothing: no review,
no approval, no submission.

Collection and optional editorial synthesis run **before** the review and may
write their own durable state (collected evidence, editorial suggestions). So an
abort is not a claim of "zero storage writes"; it is a claim that no unfinished
confirmation, approval, or submission change is applied and that prior confirmed
work, approvals, and selections are preserved.

## Review contents

- **Header**: the configured local date and timezone, then the preflight checks.
- **Completeness**: "No evidence sources configured", a distinct "no evidence
  collected" line, and one line per incomplete/failed source. Incomplete sources
  are shown and offered for acknowledgment; without acknowledgment the preview
  blocks on `incomplete_collection`.
- **Proposed blocks**: work, meetings, and manual blocks with their span (or an
  explicit `duration unknown`), project, and description. Meetings ask attendance;
  a not-attended meeting is excluded from work but kept as evidence.
- **Collisions**: an overlapping meeting can be joined with the interval it
  overlaps (counted once, both texts and evidence kept) or resolved by trim or
  exclude.
- **Journal narratives**: active journal items are shown as UNTIMED candidates
  with their provenance (relative note path, heading, line range, and the note's
  own stated date, quoted and never rewritten) and an explicit unknown duration.
  They may be skipped, excluded, or attached to one user-timed interval.
- **Missing work**: asked exactly once per day, always. An explicitly timed
  interval can be added and linked to several supporting sources; the links add no
  minutes, and the interval's own span is authoritative.
- **Classification**: an explicit, authoritative choice resolved from the catalog
  via the shared selection contract. The activity type is the catalog's DERIVED
  value, never free-form. Config-map hints, up to the 3 most recent selections, and
  the first-run suggestion are shown as `suggestion (unapplied)` and are never
  auto-applied (a recent selection takes precedence over the first-run suggestion).
  A catalog search that returns no match says the catalog is **partial** and offers
  search again, never asserting absence. An interval that already carries a
  classification is kept, and the run prints an actionable
  `timesheet classify select ...` follow-up to change it. The **Bulk actions** step
  may also **bulk-assign a classification** to the classifiable intervals (exact
  preview + explicit consent, staged only). If no catalog is
  available, choices stay unset and the preview blocks honestly. A scripted
  `--decisions` file bypasses this review-time resolution; its classification id
  (and activity type) is validated against a FRESH catalog at submission, which
  blocks with zero writes on an invalid choice.
- **Existing confirmed work** is shown and can be edited (times, project,
  description) through staged decisions; it is never re-proposed as new and never
  dropped on a refresh.
- **Empty days**: a day with no confirmed work is saved as reviewed with no
  entries. A day that has existing confirmed work is never marked empty.

## Offline retest

Only the HARNESS test is hermetic. A real guided run follows the configured
sources, so pass `--offline` (and configure no remote endpoint) to keep a manual
retest from contacting Gradion. `--offline` still runs source fetches.

The template ships with PLACEHOLDER paths (`/Users/you/...`) that MUST be replaced
before use. Point them at a normal local private location (your own vault and a
private storage directory under your home), not an arbitrary shared `/tmp` path.

```bash
# Replace the template's placeholder paths first, then run offline. Use a PRIVATE
# storage directory (not a shared /tmp path).
go run ./cmd/timesheet run --plain --offline \
  --config ./cmd/timesheet/timesheet.personal.example.json \
  --date 2026-10-06 --storage-dir "$HOME/.timesheet/records-personal-retest"

# The hermetic guided harness (no terminal, no credential, no live service):
go test ./cmd/timesheet -run TestGuidedOfflineHarness -v
```

A manual run needs a real journal note at `<vault>/<daily_dir>/<date>.md`. A
missing note is a NON-FATAL incomplete source with the warning
`JOURNAL_NOTE_MISSING` (distinct from a hard collection error): it is reported as
incomplete, it blocks the preview until the source is acknowledged, and the run
does not abort. Only explicitly recorded work is captured: a note never
establishes working time by itself.
