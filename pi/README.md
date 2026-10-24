# Timesheet Pi profile (`0.1.0`)

This directory is the ONLY supported Pi integration for the timesheet rewrite:
a restricted launcher plus one trusted adapter extension that renders the
compiled Go child's local workflow and exposes exactly one bounded
preparation tool to the model. It is not a general Pi configuration.

R6 retires the legacy CLI and its temporary rewrite entry point: the one child
is the production-named `bin/timesheet` built from `cmd/timesheet`, with the
same `version` / `serve --config --env-file` surface. The launcher, adapter,
transport and view code are unchanged from the accepted R5 lifecycle repair.

The owner authorized this release with the known Pi/Undici dependency risk
documented in the root README. Pins remain unchanged; R6 remediation stays open.

## What runs

- `launch.mjs` — the only daily entry point. It validates absolute trusted
  paths, resolves the pinned Pi CLI from this directory's own
  `node_modules/@earendil-works/pi-coding-agent` (exact `1.4.0` pin, never a
  PATH lookup), and spawns it with `shell: false` and a fixed isolation set:
  isolated working directory, dedicated profile directory (`PI_CODING_AGENT_DIR`),
  no ambient extensions/context files/skills/prompt templates/MCP, no built-in
  tools, no session persistence, offline automatic refresh/telemetry
  (`PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`). The only
  extension it loads is the trusted adapter next to the launcher; no test
  provisioning flags exist.
- `timesheet.ts` — the trusted adapter. One session-scoped compiled child
  (`cmd/timesheet serve`) through `transport.ts`; all operator authority
  (typed intervals, cross-midnight locked spill, confirmation, durable save,
  attendance, attempt-bound acknowledgment, classification search/select,
  reconciliation and explicit identity decisions, exact plan preview → explicit
  approval → one-shot submission, status/recovery, migration preview/apply)
  lives in local `/timesheet` forms. No operator capability is ever registered
  as a model tool. The human form flow and the one preparation tool are
  mutually exclusive: a preparation call while a review form is open returns a
  bounded `status=busy` result and mutates no day, draft or collection.
- `transport.ts` — serialized single-child JSONL transport: bounded frames and
  queue, exact id correlation, idempotent shutdown, fail-closed identity
  negotiation (`AppVersion`, protocol/store schema), explicit reopen only.
  Readiness means negotiation finished; one shared startup is awaited by every
  caller, so no ordinary operation can be dispatched before identity is
  accepted. Every active or queued caller settles exactly once on failure,
  cancellation, shutdown or replacement; a failure after dispatch is reported
  as an UNKNOWN outcome, never as a no-op. Frames are decoded from buffered raw
  bytes with a strict UTF-8 decoder, so a multi-byte character split across pipe
  chunks is preserved exactly and invalid bytes are a transport failure rather
  than substituted text. Every complete raw frame is checked against the byte
  bound BEFORE it is decoded or dispatched, and an unterminated remainder is
  bounded too, so a newline, trailing whitespace or chunk alignment cannot
  exempt an oversized frame. The JSON-RPC discriminator (`id`, boolean `ok`,
  error object) and the minimal status identity are validated, not asserted: a
  malformed envelope or a null/missing identity fails the transport, stops the
  child and settles callers instead of manufacturing a child refusal; a failed
  startup never implicitly spawns a second child, and only an explicit reopen
  starts a clean one. `write()` returning false is ordinary backpressure, not
  evidence that nothing was sent. Frames and callbacks from a replaced child
  are ignored by generation.
- `view.ts` — terminal safety (control/CSI/OSC/DCS stripping) and the
  editor-slot screen primitive. Detail rows are wrapped with the pinned Pi
  display-cell/grapheme-aware wrapper at the real content width of the detail
  component; paging counts those same physical rows, not UTF-16 code units. The
  window budget subtracts this component's measured chrome, the pinned Pi rows
  reserved outside the editor slot (transcript minimum plus footer, plus one
  safety row) and the paging label, with no forced minimum window. A long
  ASCII/CJK/emoji/combining row can therefore be reached end-to-end with
  `j`/`k` or `g`/`G` at narrow widths, including on a full plan/detail surface;
  the action menu and footer remain on screen. Empty confirmed/staged views
  render as a valid `(none)` total, not as a missing capability.

## Reload and runtime replacement (dev.16, unchanged by R6)

Pi replaces the extension runtime on `/reload`, `/new`, resume and fork. The
old runtime's handlers stay alive long enough to run continuations, so the
adapter treats each factory invocation as its own generation:

- State is created inside the factory, never at module scope. A stale
  continuation can only observe its own generation's transport, day, review,
  draft state and guards; it can never read or clear the replacement's guard,
  overwrite its day/review/collection, or release its workflow.
- `session_shutdown` is idempotent and **returns** the transport shutdown, so
  Pi awaits it before invalidating the runtime and starting the replacement.
  This joins the prior writer: the child can hold the durable store's exclusive
  lock while a slow operation (for example a collect awaiting its provider) is
  still in flight, and the replacement child must not start against a held
  lock. The shutdown remains bounded (polite close, then signals); nothing is
  replayed and an interrupted dispatched write stays an unknown outcome.
- Every pending UI call is owned by the generation. Signal-aware dialogs are
  aborted and custom screens are closed through their `done` callback on
  shutdown; every awaited UI call also races the generation's closed signal,
  because the pinned Pi `ui.custom()` and `ui.editor()` promises are not
  settled when Pi detaches their component. A disposed generation drops
  notifications and refuses to open a dialog.
- The command handler treats disposal as its outcome: it unwinds instead of
  leaving the old command pending. Pi's interactive loop awaits each submitted
  command, so a command that never returns would queue every later submission
  and silently block the next `/timesheet`. That was the C1-L1 failure: during
  a pending reload the old collect's failure continuation opened a recovery
  screen after Pi had already reset extension UI; the screen was hidden without
  settling its promise, the command never returned, and the visibly entered
  recovery command was queued but never dispatched. It now unwinds and the next
  command runs in the same Pi process.

The replacement runtime remains usable without restarting Pi. A transient
startup failure is still reported as an explicit failure with an explicit
Reopen action; there is no automatic respawn or retry loop.

## What the model can do

Exactly one tool, `timesheet_prepare`, declared through the launcher's
`--tools timesheet_prepare` + `--no-builtin-tools` isolation and re-checked
defensively at `session_start` (`pi.getActiveTools()`; any mismatch calls
`ctx.shutdown()`; every non-`timesheet_prepare` `tool_call` is blocked and
terminates the turn). The tool takes one civil date, drives the child's
`open_day` → `collect` → `review`, and returns a ≤512-byte bounded status plus
only opaque local ids (e.g. `collection_id`). Review data never enters the
model-visible payload; the result ends the turn (`terminate: true`).

User `!`/`!!` shell dispatch is refused unconditionally by the `user_bash`
gate: pasted, model or evidence text is never executed locally.

## Provisioning (operator inputs, never model arguments)

```
node pi/launch.mjs \
  --binary <abs compiled go binary> \
  --config <abs non-secret config.json> \
  --env-file <abs dotenv> \
  --profile-dir <abs dedicated pi profile> \
  [--workdir <abs isolated workdir>] \
  [--model <name>] [--provider <name>] [--forward-env NAME,NAME,...]
```

The launcher and adapter handle PATHS only. Neither ever reads the config or
dotenv contents; the compiled child loads them. The Pi process environment is
a whitelist (required OS variables plus the explicitly named credential
variables via `--forward-env`, values preserved verbatim) plus the
`TIMESHEET_CHILD_{BINARY,CONFIG,ENVFILE,WORKDIR}` path channels and
`TIMESHEET_FORWARD_ENV`. Chat-provider secrets are never forwarded wholesale,
and nothing is logged.

A loopback model used for acceptance evidence is pure operator provisioning:
an OpenAI-compatible entry in `models.json` inside the dedicated profile
directory, generated outside this package. No test-only provisioning code
ships in the adapter or launcher.

## Corrected consumer contract (dev.14, dev.15)

- Evidence lists may be JSON `null` when a day has no topics, meetings or
  source attempts; that is a normal R1-only/empty day, not an error. The adapter
  treats `null` and `[]` identically.
- Interval editing pre-fills from the canonical interval, not an allocation's
  display endpoints: `23:00-01:00` stays `23:00-01:00`, and a midnight-ending
  interval is `23:00-00:00`, never the `24:00` display label. After navigating
  from a locked spill to its origin day, the origin day, interval id and that
  day's current revision are sent together.
- Action availability follows lifecycle and revisions: removing the final
  interval keeps `Cancel staged changes` and `Confirm`; a confirmed but
  unsaved revision (including a confirmed empty draft) keeps `Save local`.
  The header distinguishes staged dirtiness from a confirmed-but-unsaved
  revision.
- `accept_observed` offers mapped AND changed entries and sends the actual
  `remote_id` with `local_id`, the observation id and the current revision.
  Address-bound `resolve_write` adoption offers every observed entry, including
  the already-bound identity an exact edit leaves behind after a lost
  acknowledgment. Go remains authoritative for content and one-to-one checks.
- Submission receipts consume the child's `status` field per reference
  (`confirmed`/`uncertain`/`unidentified`/`in_flight`/`stopped_unattempted`)
  and render each aggregate count. `persistence_failed` is shown as
  non-consumption; `durability_uncertain` is distinct. A transport failure
  during submission is presented as an UNKNOWN remote outcome with read-only
  status/reconciliation next, never as "zero remote mutations".
- Submission selects the exact ACTIVE approval for the CURRENT day and exact
  plan from the existing bounded `submission_status` read. There is no cached
  approval id: switching from an approved day A to an already-approved day B
  (without restarting) shows and sends B's approval, never A's. A consumed,
  revoked, or changed-plan approval is not selectable; the child remains
  authoritative and a stale id is refused before dispatch.
- Only a validated Go error envelope is tagged as a child refusal. Malformed
  wire data (including a non-boolean `ok` or missing error object) is a
  transport failure, so it can never be presented as a pre-dispatch refusal or
  as evidence of zero effects.

## Honest limits

- This is an adapter/transport slice, not an OS sandbox; isolation comes from
  the pinned launcher flag set, the dedicated profile and the isolated workdir.
- The adapter keeps no accounting of its own: the Go child is authoritative
  for grammar, minutes, revisions, identity, persistence and readiness.
- Nothing is replayed across a child restart: reopening is explicit, unsaved
  session drafts do not survive, and unresolved writes stay unresolved until a
  human resolves them against a fresh observation.
- Historical stage evidence for the unchanged consumer code (accepted R5
  lifecycle repair, dev.16): Linux with the pinned Pi 1.4.0 TUI, the compiled
  dev.16 child, synthetic loopback ICS/MCP/model fixtures and generated
  isolated profiles: pending collect and pending classification reads across
  reload, a pending approved write applied but unacknowledged across reload
  (read-only status, explicit adoption, contradictory `not_applied` refused,
  exactly one write call), `/new` during a pending model preparation, idle
  reload, and quit with pending work (no orphan child). Cross-day approval
  selection and the UTF-8/byte-bound transport paths were not rerun because
  their source is unchanged from the verified dev.15 review; `durability_uncertain`
  UI behavior remains source-reviewed plus accepted backend evidence, not a
  newly driven case. Subsequent dev.17 Darwin native migration and local
  workflow/lifecycle checks passed, including the owner-run one-command verifier.
  Release `0.1.0` changes identity only, not those behaviors. Live accounts,
  credentials and authenticated subscription readiness remain unverified.
