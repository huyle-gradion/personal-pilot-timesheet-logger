# Collection configuration: git `mode`, `local_repos`, and `repos`

This note describes the git collector's configuration and the exact command
sequence for a day. It reflects the current CLI (`go run ./cmd/timesheet help`)
and the git source schema in `internal/evidence/plugins/git.go`.

Sanitized templates:

- `cmd/timesheet/timesheet.example.json` (calendar + local-only git)
- `cmd/timesheet/timesheet.local-git.example.json` (local + remote git)

Both templates use `keychain:<service>` references and contain **no** secret
values. A user's own config is separate and must never be overwritten.

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
| `fixture` | Contract-mode fixed input path (verification only). |

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

## Command sequence

`help` (actual output):

```text
$ go run ./cmd/timesheet help
timesheet <subcommand> [flags]

Subcommands:
  preflight   validate timezone, credentials, source contracts, and MCP capabilities
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
  --offline                never contact the remote
  --deep                   deeper contract verification during preflight
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

When `mode` includes remote, validate credentials and contracts first, then
approve and submit:

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

### Known limitation (pre-existing, not fixed here)

Git CONTRACT mode requires a `fixture` path (`config.fixture`). `preflight` runs
contract verification for every enabled source, so a real git source **without** a
`fixture` fails the `contracts` check even though live collection would succeed.
This predates the `mode`/`local_repos` change and is **not** fixed here. The
local-only sequence above therefore does not depend on `preflight`; when a
`fixture` is not configured, run `review` directly (live collection does not use
the fixture).
