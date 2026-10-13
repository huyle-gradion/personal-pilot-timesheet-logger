# Collection configuration: git `mode`, `local_repos`, and `repos`

This note describes the git collector's configuration and the exact command
sequence for a day. It reflects the current CLI (`go run ./cmd/timesheet help`)
and the git source schema in `internal/evidence/plugins/git.go`.

Sanitized templates:

- `cmd/timesheet/timesheet.example.json` (calendar + local-only git)
- `cmd/timesheet/timesheet.local-git.example.json` (local + remote git)
- `cmd/timesheet/timesheet.journal.example.json` (optional local read-only journal + local git)
- `cmd/timesheet/timesheet.personal.example.json` (personal offline: journal + local-only git, no credential, no remote)

All templates use `keychain:<service>` references and contain **no** secret
values. A user's own config is separate and must never be overwritten.

The guided terminal `run` (`timesheet run --interactive`, `--plain`) and its
review contract are documented in
[the guided workflow note](guided-workflow.md).

The end-to-end operator guide — local vs connected (remote) workflow modes,
setup, the two configs, review decisions, classification, preview/approve/submit
gates, privacy, persistence, and troubleshooting — is
[the product usage guide](product-usage.md).

The optional local read-only **journal** source (`"type": "journal"`) reads the
Obsidian daily note as narrative evidence. Its keys, path confinement, identity,
active/detached history, and privacy rules are documented in
[the journal source note](journal-source.md) and
[ADR 0007](../adr/0007-journal-evidence-source.md).

## Git source keys

The git source is one entry in `evidence.sources` with `"type": "git"` and a
`config` string map:

| Key | Meaning |
| --- | --- |
| `mode` | Which sub-sources to collect: `local`, `remote`, or `both`. Absent ⇒ `both` (backward compatible). |
| `local_repos` | Comma-separated **local checkout paths** (e.g. `/Users/you/src/app`). Absent ⇒ `["."]` (the current working directory). |
| `repos` | Comma-separated **host-less `owner/repo` slugs** for the remote (GitHub) source, e.g. `acme/app,acme/lib`. Required only when `mode` includes `remote`. |
| `author` | Optional local commit author filter (email or name). |
| `project_map` | Optional JSON object of `owner/repo` → project name. |
| `fixture` | **Test-only** fixed input path for fixture-based contract/determinism verification. NEVER required at runtime; not needed by `preflight`. |

Rules:

- **`repos` is host-less `owner/repo`, never a filesystem path.** A raw path in
  `repos` is not a local checkout and is not used as repository identity.
- **Local-only** collection (`"mode": "local"`) uses `local_repos` and never
  contacts GitHub. A `mode:"local"` git source also needs **no** GitHub
  credential (preflight and the credential resolver skip it); an absent mode
  (default `both`), `remote`, or `both` still requires one.
- **Local repository identity** is
  `CanonicalRepoKey(localSource.RepoName(ctx), source id)`:
  - With a `remote.origin.url`, `RepoName` returns `owner/repo` and
    `CanonicalRepoKey` yields the canonical **host-less `owner/repo`** key.
  - With no remote origin, `RepoName` returns the checkout basename (a single
    path segment), which `CanonicalRepoKey` rejects; identity then falls back to
    the source `id` — **not** the basename.
  - No filesystem path is ever written to `SourceRef`. If identity cannot be
    resolved (no remote origin and no source id), the local sub-source fails
    visibly with the stable code `GIT_LOCAL_IDENTITY_UNRESOLVED` (the path is not
    echoed).
- **Dedup/provenance** requires the configured `repos` slug to match the
  checkout's `RepoName()` canonical form. Both must be the **host-less
  `owner/repo`** key: a host-qualified slug such as `github.com/acme/app` does
  **not** merge with the host-less local name `acme/app`, and the same commit is
  then emitted twice (once per identity). Keep `repos` host-less to fold a commit
  claimed by a PR into the PR record exactly once.
- A remote failure does **not** abort collection: local evidence is retained and
  the source is reported `partial` with the stable warning
  `GIT_REMOTE_FETCH_FAILED` (deduplicated across failing repos). A partial
  collection is never presented as complete.

Contract-check codes: `GIT_MODE_INVALID`, `GIT_REPOS_REQUIRED` (mode includes
remote but `repos` is empty), `GIT_PROJECT_MAP_INVALID`.

### Example config (local-only git)

```json
{
  "evidence": {
    "id": "default",
    "timezone": "America/Los_Angeles",
    "sources": [
      {
        "id": "git-work",
        "type": "git",
        "enabled": true,
        "config": {
          "mode": "local",
          "local_repos": "/Users/you/src/app",
          "author": "you@example.com"
        }
      }
    ]
  }
}
```

### Example config (local + remote, with keychain references)

```json
{
  "evidence": {
    "id": "default",
    "timezone": "America/Los_Angeles",
    "sources": [
      {
        "id": "git-work",
        "type": "git",
        "enabled": true,
        "config": {
          "mode": "both",
          "local_repos": "/Users/you/src/app",
          "repos": "acme/app",
          "author": "you@example.com"
        }
      },
      {
        "id": "cal-work",
        "type": "calendar",
        "enabled": true,
        "config": { "url_ref": "keychain:calendar_feed_url", "user_email": "you@example.com" }
      }
    ]
  },
  "gradion": {
    "endpoint": "https://mcp.gradion.example/mcp",
    "credential_ref": "keychain:gradion.workspace.ApiKey"
  }
}
```

Secrets live in the macOS Keychain and are referenced as
`keychain:<service>`. A raw secret in config is rejected (see
`docs/agents/credentials.md`).

## Calendar source keys

A calendar source is one entry in `evidence.sources` with `"type": "calendar"`:

| Key | Meaning |
| --- | --- |
| `url_ref` | **Required** `keychain:<service>` reference to the ICS feed URL. Never a raw URL. |
| `user_email` | Optional owner email used to match invitations. |
| `timezone` | **Restricted, not freely optional.** Must match the run's configured zone; see the rule below. |
| `cache_path` / `no_cache` | Optional local ICS cache controls. |
| `fixture` / `fixture_date` | **Test-only** fixed input for fixture-based contract/determinism verification. NEVER required at runtime. |

Rules:

- **`url_ref` is resolved in memory at collection time.** Immediately before
  collection, `internal/run` resolves each ENABLED calendar source's `url_ref` and
  places the resolved ICS feed URL in an in-memory copy of that source's
  `config["source"]`. The resolved URL is **never persisted**: it is not written
  to the JSON config, a record, an export, a log, a warning, or an error message.
  `preflight` resolves `url_ref` ONLY to verify the secret is present (a missing
  item fails the check with `RUN_PREFLIGHT_KEYCHAIN_MISSING`); it never populates
  `source` and never persists the value. Populating `source` happens at COLLECTION
  time (in `internal/run.Collect`) on an in-memory copy.
- **The source `id` is the non-secret identity namespace** (the configured feed
  id), never the feed URL or a filesystem path; the contract check refuses an id
  that looks like a URL/path.
- **A missing or unresolvable secret is visibly incomplete, never fatal.** A
  Keychain `NotFound` (or any resolution failure) leaves `source` UNSET, so the
  calendar source reports `incomplete` (`SOURCE_ERROR`) with zero events. The run
  does **not** abort, no source is fabricated, and the source requires an explicit
  acknowledgment (`--ack <source-id>`) before `preview`/`submit`.
- **`--offline` does not suppress a calendar fetch.** The calendar ICS fetch is a
  **source** fetch, not a Gradion remote call; `--offline` skips only the remote.
- **A calendar source's `timezone` must match the run's configured zone.** The run's
  `evidence.timezone` governs date-window interpretation for every source, and a
  calendar source is no longer free to pick its own. A per-source `timezone` equal
  to the run zone is a no-op; a per-source `timezone` that differs is **refused** at
  collection rather than silently re-anchoring that one source to a different day.
  An unset run zone resolves to `UTC`, so a per-source calendar `timezone` other
  than `UTC` fails at collect time. That mismatch is therefore **not** caught at
  configuration or preflight time: the source reports incomplete at collection,
  which is a change in where the mistake surfaces and must be expected rather than
  discovered.

## Command sequence

`help` (actual output):

```text
$ go run ./cmd/timesheet help
timesheet <subcommand> [flags]

Subcommands:
  preflight   validate timezone, credentials, source configuration, and MCP capabilities
  review      collect + propose (+ editorial) and apply --decisions, or print the proposal
  export      write the managed daily-note section (never contacts the remote)
  preview     build and persist the submission plan (zero remote writes)
  approve     bind an approval to a plan: approve --plan <PLANID>
  submit      execute the record's ACTIVE approval: submit --date D [--approval <APPROVALID>]
  resolve     reconcile unresolved operations from --decisions
  run         preflight + collect + review + export convenience

Flags:
  --date YYYY-MM-DD        target date (default: today in the configured timezone)
  --from/--to YYYY-MM-DD   iterate days with per-day review/approval
  --config FILE            JSON config document
  --storage-dir DIR        workflow record directory (default: ~/.timesheet/records)
  --offline                skip the Gradion remote; source fetches (e.g. calendar ICS) still run
  --deep                   validate ALL configured sources (including disabled) during preflight
  --editorial/--no-editorial  toggle the optional editorial agent
  --decisions FILE         decisions JSON ({date} substituted per day)
  --non-interactive        never prompt (the CLI is always non-interactive)
  --ack SOURCE,...         acknowledge incomplete sources
  --plan PLANID            plan id to approve
  --approval APPROVALID    approval id to submit (must match the active approval)
  --vault DIR              vault root for the managed export
  --daily-dir DIR          daily-note directory relative to the vault
```

### Local-only sequence (no remote, no GitHub credential)

For a `mode:"local"` git source there is no remote and no credential, so the day
proceeds without a preflight dependency:

```bash
# 1. collect + review: `review` collects local evidence and prints the proposal.
go run ./cmd/timesheet review --config ./timesheet.json --date 2026-10-06 --no-editorial

# 2. confirm: apply a decisions JSON (confirm/exclude/join/trim/project/manual).
go run ./cmd/timesheet review --config ./timesheet.json --date 2026-10-06 \
  --decisions ./decisions.json

# 3. preview: build and persist the submission plan (zero remote writes).
go run ./cmd/timesheet preview --config ./timesheet.json --date 2026-10-06 --offline

# 4. export: write the managed daily-note section (never contacts the remote).
go run ./cmd/timesheet export --config ./timesheet.json --date 2026-10-06
```

### Remote sequence (mode `remote`/`both`; requires a GitHub credential)

When `mode` includes remote, validate the credentials and the source
configuration first (`preflight` checks configuration, not live availability —
the fetch itself happens in `review`), then approve and submit:

```bash
go run ./cmd/timesheet preflight --config ./timesheet.json
go run ./cmd/timesheet review    --config ./timesheet.json --date 2026-10-06 --no-editorial
go run ./cmd/timesheet preview   --config ./timesheet.json --date 2026-10-06
go run ./cmd/timesheet approve   --config ./timesheet.json --date 2026-10-06 --plan <PLANID>
go run ./cmd/timesheet submit    --config ./timesheet.json --date 2026-10-06 --approval <APPROVALID>
go run ./cmd/timesheet export    --config ./timesheet.json --date 2026-10-06
```

Notes:

- Collection is performed by `review` (and by `run`); there is no separate
  `collect` subcommand.
- A day with **no configured sources** prints `No evidence sources configured.`
  A day whose sources are configured but return no evidence is legitimate and
  produces an empty draft.
- An incomplete collection is visible as a per-source line
  (`source <id>: partial (...)`), blocks `preview`/`submit` with
  `incomplete_collection`, and requires an explicit acknowledgment
  (`--ack <source-id>` or a decisions `acknowledge` entry) before it can proceed.
- A reviewed-empty day exports as `reviewed-empty` / `reviewed-not-submitted`.
- `preview`/`submit` remain blocked with `incomplete_collection` until an
  incomplete source is acknowledged.

## Preflight semantics

`preflight` separates RUNTIME source validation from fixture-based behavioral
testing:

- **Runtime validation** (`preflight`, and the `run` convenience) checks each
  enabled source's CONFIGURATION: required config keys, secret references, and
  each plugin's declared contract (git `mode`/`repos`/`project_map`, calendar id,
  agent path, ...). It performs **no** fixture-based collect, **no** determinism
  re-run, and **no** network or filesystem-availability check. It does NOT prove
  live source availability — that is exercised by `review` (which actually
  fetches). The check is named `configuration` (stable code
  `RUN_PREFLIGHT_CONFIGURATION_INVALID` on failure).
- **`--deep`** validates ALL configured sources, including disabled ones (reported
  `skipped`), still without fixtures.
- **Fixtures are TEST inputs.** Fixture-based determinism/behavioral verification
  (`VerifyContracts`/`PreflightContracts`) requires a `fixture` and is used by
  contract tests, not by `preflight`. A git/calendar/agent source needs NO fixture
  to run.
- A **`mode:"local"`** git source passes `preflight` offline with NO GitHub
  credential and NO fixture (and the checkout path is not stat'd).
- Genuine failures still BLOCK (fail-closed): a malformed config, an invalid
  source mode, a missing required input (calendar `url_ref`, git `repos` when mode
  includes remote), an invalid `project_map`, an unknown plugin type, or a
  terminal credential failure. There is no REST fallback.
