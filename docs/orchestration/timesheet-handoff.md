WORKER CLASS: complex-critical
PRESET: openai
MODEL: openai/gpt-6-sol
VARIANT: medium
RISK BASIS: implementation interface, private credentials, durable work state, migration/submission dependencies
GOVERNING ARTIFACT: docs/timesheet-spec.md
SCOPE: R1 local daily workflow only, exact files below
VERIFICATION: compiled binary smoke with synthetic configuration/state/credentials; no live access; report exact output

# R1 owner-mediated worker packet

This is a complete prompt for the owner to run in a capable implementation session. It does not dispatch that session. Routing is the repository's handoff convention, not a claim that this model is available in every harness.

Current status: R1 is accepted at `0.1.0-dev.3`, with F1-F4 closed. Do not rerun this initial packet. See [R1 acceptance](timesheet-r1-acceptance.md); the next owner-mediated action is the [R2 evidence packet](timesheet-r2-handoff.md).

## Task and authority

Task ID: TIMESHEET-R1.

Implement the accepted specification's local/manual daily workflow as a fresh compiled process. R1 must produce usable local review and saved work, not protocol-only scaffolding. GitHub/calendar integration, migration, Pi, and remote submission are later named slices and must not be presented as implemented here.

Authority when the owner runs this packet: bounded source implementation and hermetic local execution. No commit, push, issue/PR mutation, release tag, live read/write, credential retrieval, ai-database access, or direct worker dispatch. Stop after implementation evidence for independent review.

## Base and ownership

Repository: `/home/thearesia/Projects/personal-pilot-timesheet-logger`.

Dedicated implementation branch: `496a6ab8a81e9418a25046995ae40e2b8713e018`, created at base HEAD `4ff992b51a3c9b24bd109eb07df15f6d5b4f8abe` from `main`.

Work only on this dedicated implementation branch, never on `main`. Do not create the previously proposed separate R1 branch. At execution, reconcile the base and changes without overwriting them; preserve the uncommitted kickoff artifacts. Take a source-only rollback snapshot, excluding `.env*`, `transcript_full.jsonl`, real config, workflow records, and private source payloads. Record the sanitized snapshot identifier in the report. If source ownership or base differs, resolve it before mutation.

Never open `.env.local`, the private transcript, real config, real workflow records, or ai-database. Do not run environment-dump commands, source dotenv into a shell, or discover credentials. Use only synthetic data in a private temporary directory.

## Targeted reads

Read the governing specification, this packet, the execution plan's shared contract/R1 row, `go.mod`, `.gitignore`, and `GLOSSARY.md`. Mandatory context instructions supplied by the harness still apply. No source archaeology, broad filesystem search, or unrelated orchestration ledger reading is necessary for this new isolated module. If an additional source dependency is genuinely needed, name it and the reason in the report rather than expanding scope silently.

Minimum Go guidance: <https://go.dev/doc/effective_go>. TigerStyle and `docs/engineering/go-rules.md` are not mandatory. Tests are optional. Use existing toolchain/dependencies; do not change `go.mod` or `go.sum` without a demonstrated need and scope decision.

## Allowed writes

- `cmd/timesheet/main.go`
- `internal/timesheet/config.go`
- `internal/timesheet/model.go`
- `internal/timesheet/store.go`
- `internal/timesheet/session.go`
- `.env.example`, containing placeholders only
- `docs/agents/timesheet-interface.md`
- `CHANGELOG.md`

Do not alter the production CLI, legacy packages/stores, existing skills, real environment/config, orchestration records, or kickoff specification. Use few files; omit allowed files if unnecessary. Any permanent test must be within `internal/timesheet/*_test.go`, deterministic, and exercise consumer-visible behavior. Tests are not required.

## Required implementation

1. `go build -o <private-temp>/timesheet ./cmd/timesheet` produces a real executable.
2. `version` reports application version `0.1.0-dev.1` and protocol schema 1 without loading credentials or collecting evidence. This is an unreleased build, not a release tag.
3. `serve --config <path> --env-file <path>` runs a persistent line-delimited JSON request/response session. Fixed paths are trusted launcher inputs, never model-call parameters. Do not launch a shell.
4. Define and document strict request schemas, request IDs, expected revisions, typed/manual input grammar, and stable error codes for opening a day, adding/editing/removing staged intervals, reviewing, confirming work, saving locally, cancellation, status, and closing the session. Prefer a small interface over exposing internal CRUD. No arbitrary operation dispatch or path fields in model-originated requests.
5. A day includes timezone, proposed/staged work, confirmed intervals, explicit project/classification selections when supplied, revision, and durable local history needed by later slices. Do not implement speculative remote structures or fake collectors. Review shows interval/project/description and accurate totals; GitHub duration inference is absent.
6. The user can enter manual work, review it, explicitly confirm it, and save locally. Revision creates a new reviewable state. Cancel leaves prior confirmed work unchanged. Zero/negative/invalid intervals and unresolved overlaps are rejected with actionable errors; multiple evidence references never add time. Preserve explicit date/timezone semantics and represent cross-midnight work without losing minutes.
7. Load non-secret JSON config and `.env.local` only inside Go. Document the supported dotenv syntax and required/optional variable names for this slice. Inherited environment values override file values. Parse dotenv as data, never execute or source it. Validate needed values only for the selected operation; a local save must not require unavailable remote credentials. Never echo credential values, raw dotenv lines, authenticated URLs, or full config, including in errors. Do not add Keychain lookups.
8. Save real private local state durably with revision checks and single-writer protection. Restart recovers confirmed local work. Cancellation and failed writes must not report success or erase the last confirmed state. No unconditional implementation of unknown legacy stores.
9. stdout is protocol output in serve mode; diagnostics stay separate and sanitized. Invalid input is a compact error, not a stack trace or a request to read source files.
10. Update the public interface document and Unreleased changelog with implemented behavior and explicit R1 limits. Do not add no-op collect/approve/submit commands, mock production fallbacks, TODO implementations, or fake success.

## Acceptance and concrete verification

Use a private temporary directory, placeholder credentials and a synthetic config. Do not load the repository's real `.env.local`. Keep executable and smoke artifacts outside tracked source. Implement the documented requests before building; run checks after the slice, not mid-edit.

- Build the actual command and run `version`; capture application/protocol version and exit status.
- Use a throwaway driver to launch `serve` once and exchange multiple requests through that same process/PID.
- Enter 09:00-10:00 and 10:30-11:15 manual intervals for a synthetic day/project. Review must show exactly 105 minutes. Confirm and save locally, then close and restart; the saved spans, descriptions and 105-minute total must persist.
- Stage a change, cancel it, and demonstrate that the previously confirmed spans and total remain unchanged.
- Submit an overlapping interval and a stale-revision mutation. Demonstrate explicit rejection and unchanged confirmed state, not merely a nonzero exit.
- Exercise a midnight-spanning interval in a configured timezone and show where each day's minutes are recorded. Do not silently wrap or double-count it.
- Load fake dotenv values and inherited overrides; a throwaway driver may inspect internal behavior through the documented interface but must never print even fake secret values. Demonstrate a parser/validation error whose output excludes its input value. Check required-variable diagnostics without live network calls.
- Attempt a second writer against the same synthetic state; demonstrate a clear conflict rather than state loss.
- Produce the actual review output and local-save receipt. No network, model provider, or remote service is needed for R1.

Remove throwaway verification scripts after the smoke; report the commands and observed outputs. Permanent unit tests are optional. A build alone is not acceptance.

## Stop conditions

Stop before exposing secrets, accessing live sources, changing files outside scope, altering legacy stores, running a second writer over another worker's state, or inventing remote/migration behavior. If runtime requirements cannot be met, report the exact missing prerequisite; do not ship placeholders.

## Required report

Use the implementation-report fields in `docs/orchestration/handoffs.md`: branch/base and sanitized snapshot, files/behavior, exact commands and outputs, acceptance pass/fail/not-run, known limitations, scope deviations, and current ownership. State that no live access, commit, publication, or vault writes occurred.

End at review-ready. A fresh independent reviewer evaluates this exact snapshot. Do not self-accept or proceed to R2 automatically.
