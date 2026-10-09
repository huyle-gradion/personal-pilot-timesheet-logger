# Keychain credentials

Owner decision (2026-10-09): store all three pipeline credentials in the macOS
Keychain and have the pipeline read them from there.

## Conflict resolution (2026-10-09)

An earlier revision of this note prohibited plaintext credentials and then, in
the same document, retained an `env → Keychain → .env` precedence that lets a
plaintext `.env` value win. That was self-contradictory. Resolution:

- **Keychain-only secrets.** The three secrets live in macOS Keychain and
  nowhere else.
- **Configuration holds references only.** A config file (`.env` or otherwise)
  may carry endpoint settings and non-secret *references* — Keychain service and
  account names, base URLs, tracked repos — but never a secret value.
- Existing environment / `.env` secret inputs are retained **only** as
  deprecated, transitional overrides for tests and CI. They are not the
  recommended path and should be removed in favor of the Keychain items.

Recommended steady-state order is therefore `explicit flag → Keychain`; the
environment step below exists only for backward compatibility.

## Items (all three created by the owner)

| Credential | Keychain service | account | Status |
| --- | --- | --- | --- |
| Gradion Workspace bearer | `gradion.workspace.ApiKey` | `$USER` | created |
| GitHub access token | `gh_auth_token` | `$USER` | created |
| Google Calendar secret iCal URL | `calendar_feed_url` | `$USER` | created |

`gh_auth_token` is the service read by the GitHub preflight resolver
(`internal/git/preflight.go` via `keychain.ServiceGitHubToken`). The Gradion item
was created as `gradion.workspace.ApiKey` before this note, so that name is
fixed. The `.claude/skills/timesheet/SKILL.md` item name `timesheet_api_token`
is **superseded and stale**; use `gradion.workspace.ApiKey`.

## Resolver — implemented (`internal/keychain`)

One shared resolver reads secrets from the Keychain:

- It execs `security find-generic-password -s <service> -a "$USER" -w`, reads
  stdout into process memory, discards stderr, and bounds the subprocess
  (3s timeout + 16 KiB output cap + process-group kill).
- It returns a typed, redacted `*keychain.OperatingError` on failure. Exit 44
  (item absent) and an unavailable `security` binary both map to
  `OutcomeNotFound`; other failures are `OutcomeTerminal` with stable codes
  (`KEYCHAIN_ERR_*`).
- The secret is never placed on argv, never written to stdout/logs, and never
  included in an error string. `keychain.Redact`, and the API client's
  response-body sanitizer, are available as defense in depth.
- **Redaction is literal only.** These helpers replace exact occurrences of the
  known token; encoded, escaped, split, or otherwise transformed forms are not
  detected. Neither is a universal secret filter, and neither output is
  guaranteed secret-free.
- Service names are non-secret references: `keychain.ServiceGitHubToken`,
  `keychain.ServiceCalendarFeed`, `keychain.ServiceGradionMCP`.

## Resolution order (as implemented)

| Call site | Order (highest priority first) |
| --- | --- |
| GitHub preflight (`internal/git/preflight.go`) | `-token` → `GITHUB_TOKEN` → `GH_TOKEN` → Keychain `gh_auth_token` → `gh auth token` |
| Calendar (`cmd/fetch-calendar`) | `-file` → `-url` → `GCAL_ICS_URL` → Keychain `calendar_feed_url` |
| Gradion sync (`cmd/sync-timesheet`) | `-token` → `TIMESHEET_API_TOKEN` → Keychain `gradion.workspace.ApiKey` |

The environment step (including `.env`-loaded values) is the deprecated,
transitional override described above.

**Keychain failure handling.** A collector falls through to the next, weaker
source **only** when the Keychain result is a genuine NotFound (absent item, or
the `security` binary unavailable). Any other failure — timeout, access denial,
malformed/oversized output, empty secret — stops resolution and is surfaced as
a typed, secret-free `KEYCHAIN_ERR_*` error. A transient Keychain problem must
never silently downgrade a collector to a legacy environment/`.env` credential.

## Create (owner runs; `security` prompts, so the secret is never an argv value)

```bash
security add-generic-password -s "<service>" -a "$USER" -l "<service>" -w
```

## Look up

```bash
security find-generic-password -s "<service>" -a "$USER" -w   # -w prints only the secret
security find-generic-password -s "<service>" -a "$USER" >/dev/null 2>&1; echo $?   # 0 present, 44 absent
```

## Safety

- Never echo, log, or write a secret. The code never passes a secret as a
  subprocess argument, and the resolver's `security` invocation carries only the
  non-secret service/account.
- CLI flags (`-token`, `-url`) accept a secret directly and therefore expose it
  in the process argument list (`ps`). They exist for tests/CI only; the
  recommended path is the Keychain.
- Never modify or replace a Keychain item without explicit approval.
- Verified MCP capability matrix and sanitized reproduction:
  `docs/gradion-mcp-verification.md`.

## Verification status

Read-only verification of all three sources (calendar fetch, GitHub access, MCP
listing) is recorded in the pull request for this change. No Keychain item was
created, modified, or replaced by the implementing agents, and no remote write
was performed. Gradion remote submission remains deferred behind the temporary
`cmd/sync-timesheet -approve` mutation gate.

`-approve` is **not** revision-bound approval: it is a per-run gate, not
approval tied to an exact preview or to a specific local record revision, and no
durable mapping/revision store exists yet (ADR-005 and the redesign remain
`not-started` for that part). Production submission remains **not ready**.
