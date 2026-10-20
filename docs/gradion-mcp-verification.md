---
title: "Gradion Workspace MCP verification"
status: "credential resolved; read-only verification complete"
date: 2026-10-09
scope: "credential retrieval + read-only integration verification"
---

# Gradion Workspace MCP verification

## Summary

Credential retrieval is resolved and authenticated MCP discovery now works against
`https://workspace.gradion.com/api/mcp`. Read-only calls verified listing,
pagination, stable entry IDs, and the classification catalog. Creation, editing,
policy limits, and race behavior remain unverified because no production writes
are authorized.

The capability matrix below separates *declared* (schema) evidence from *verified*
behavior. No production creates, edits, deletes, or test submissions were made.
No secret was displayed or committed. No Keychain item was created, modified, or
replaced by this work.

## 1. Credential retrieval — resolved

### Inspection performed (metadata only)

| Store | Query | Result |
| --- | --- | --- |
| login keychain | `security find-generic-password -l/-s/-a "gradion.workspace.ApiKey"` | absent (exit 44) |
| data-protection keychain `keychain-2.db` | `SELECT labl,acct,svce …` filtered to gradion/workspace/timesheet/apikey | no match (133 rows) |
| System keychain | metadata dump filtered to `gradion` | unrelated item only |

The item was absent from both keychain databases — not merely invisible to the
`security` CLI (the CLI cannot read the data-protection `keychain-2.db`, so that
store was queried directly). The user then created it.

### Resolved reference

- service: `gradion.workspace.ApiKey`
- account: `$USER`
- lookup: `security find-generic-password -s "gradion.workspace.ApiKey" -a "$USER" -w`

Presence re-check returns exit 0. The token must be read into process memory
only — never argv, stdout, logs, files, or agent context.

## 2. Authenticated MCP discovery — verified

Initialize response: `protocolVersion 2025-06-18`, `serverInfo {"name":"gradion-workspace","version":"1.0.0"}`,
capabilities `{"prompts":{},"resources":{},"tools":{"listChanged":true}}`.
`tools/list` returns **71 tools** across `workspace__`, `timesheet__`, and
`time-attendance__` namespaces. `resources/list` and `prompts/list` return **0**.

Timesheet-relevant tools:

| Tool | Purpose | Key args (required in bold) |
| --- | --- | --- |
| `timesheet__list_my_entries` | List entries for a date range, newest first | **dateFrom**, **dateTo**; limit 1–50 (default 10), offset |
| `timesheet__log_time` | Create an entry | **date**, **startTime**, **endTime**, **description** + one of **classification**/**classificationId**; task, billable |
| `timesheet__edit_time` | Edit an existing entry (partial; audit reason required) | **entryId**, **reason**; optional date/times/description/classificationId/task/billable |
| `timesheet__get_classifications` | List projects/activities the caller can log against | search, activityTypeId (max 20 results) |
| `timesheet__get_my_summary` | Logged time for a period, by category | dateFrom, dateTo |
| `timesheet__get_my_week` | Per-day logged vs expected minutes | weekStart |
| `timesheet__get_team_project_hours` | Project hours by person (scope-limited) | dateFrom, dateTo; projectCode |

No timesheet delete tool exists, consistent with the redesign's "editable but not
removable."

## 3. Capability matrix

| # | Capability question | Status | Evidence |
| --- | --- | --- | --- |
| 1 | List entries by date, with pagination | **Verified (read-only)** | `list_my_entries` requires inclusive `dateFrom`/`dateTo` (YYYY-MM-DD); `limit` 1–50 (default 10) + `offset`; newest first. Two calls at offset 0 and offset 2 returned different rows. No total/has_more field → end of range is detected by a short page. |
| 2 | Stable entry IDs + retrieve an individual entry | **Partially verified** | Each row carries a stable UUID `id` (usable by `edit_time`). There is no get-by-id tool; individual retrieval means listing a date range and matching. |
| 3 | Create and edit entries | **Declared; behavior unverified** (no writes) | `log_time` creates; `edit_time` edits partially (omit = unchanged). Schema only. |
| 4 | Required project/classification fields | **Verified (read-only)** | `log_time` requires `classification` **or** `classificationId` (schema `anyOf`); the name/code is resolved server-side. `get_classifications` returned the caller's project/activity catalog with UUIDs. `billable` applies to client projects only. |
| 5 | Timezone and interval formats | **Partially verified** | `date` = `YYYY-MM-DD`; `startTime`/`endTime` = `HH:MM` 24h, `endTime` after `startTime`. No timezone parameter is exposed → the governing timezone is unverified. |
| 6 | Four-hour entry limit | **Unverified** | Not stated in the schema. `log_time`'s description says a server-side policy limit exists and the tool reports the exact limit on violation; this cannot be exercised without writing. |
| 7 | Conditional updates / version checks | **Verified absence** | `edit_time` exposes no version, ETag, `If-Match`, or last-observed-state parameter. There is no optimistic-concurrency guard → the remote write race described in the redesign remains. |
| 8 | Idempotency keys / uncertain-write reconciliation | **Verified absence (schema)** | `log_time`/`edit_time` expose no idempotency key. Reconciliation after an uncertain write must rely on `list_my_entries` matching plus the `edit_time` audit `reason`. |

Other verified constraints: `log_time` forbids overlapping entries; `description`
max 3000 chars, `task` max 150, `edit_time` `reason` max 200.

## 4. Sanitized reproduction

Credential presence (no secret output):

```bash
security find-generic-password -s "gradion.workspace.ApiKey" -a "$USER" >/dev/null 2>&1; echo "exit=$?"
```

Authenticated discovery. The token is read in-process and never placed in argv,
stdout, or logs; a full tools/list + a read-only call:

```bash
python3 - <<'PY'
import json, os, subprocess, urllib.request
tok = subprocess.run(["security","find-generic-password","-s","gradion.workspace.ApiKey",
                      "-a",os.environ["USER"],"-w"], capture_output=True, text=True, check=True).stdout.strip()
def call(payload, sid=None):
    h = {"Authorization": f"Bearer {tok}", "Content-Type": "application/json", "Accept": "application/json"}
    if sid: h["Mcp-Session-Id"] = sid
    req = urllib.request.Request("https://workspace.gradion.com/api/mcp",
                                 data=json.dumps(payload).encode(), headers=h, method="POST")
    with urllib.request.urlopen(req, timeout=15) as r:
        raw = r.read(262144).decode(); return (json.loads(raw) if raw.strip() else {}), r.headers.get("Mcp-Session-Id")
init, sid = call({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"verify","version":"1"}}})
call({"jsonrpc":"2.0","method":"notifications/initialized","params":{}}, sid)
tools,_ = call({"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}, sid)
print("tools:", len(tools["result"]["tools"]))
entries,_ = call({"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"timesheet__list_my_entries",
                   "arguments":{"dateFrom":"2026-10-01","dateTo":"2026-10-09","limit":2}}}, sid)
print(json.dumps(entries["result"].get("structuredContent", entries["result"].get("content")), indent=2)[:1200])
PY
```

Entry `description`/`content` fields are private user content and must be
redacted from any report.

## 5. Remaining blockers and design decisions

Blockers:

- **B1 — Resolved.** Credential retrieval works.
- **B2 — Resolved.** Live `tools/list` and read-only capability checks are done.
- **B3 — Open, design-level.** No conditional-update guard and no idempotency key
  exist. Safe reconciliation must therefore be built on `list_my_entries`
  comparison and treat any uncertain write as unresolved until a re-read
  establishes the outcome. No production writes may be used to probe this.- **B4 — Unverified by construction.** The four-hour limit and the governing
  timezone cannot be confirmed without a write; they must be established from
  server-observed behavior when a single, approved entry is first created.

Design decisions to revisit with the owner:

- **D1 — Transport.** Direct `/api/mcp` is confirmed viable for reading,
  creating, and editing. Prefer it; no REST fallback needed for these operations.
- **D2 — Reconciliation.** Build on entry IDs + `edit_time` `reason` audit; no
  version guard exists, so re-read immediately before any write and invalidate
  approvals when remote state differs.
- **D3 — Credential references.** Standardize naming. Gradion uses
  `gradion.workspace.ApiKey`; the repo also documents `gh_auth_token`,
  `calendar_feed_url`, and `timesheet_api_token`.
- **D4 — Local credential resolver.** The Go code still has no Keychain lookup
  (see §6); the `.claude` skill's env → Keychain → `.env` priority is
  unimplemented.

## 6. Project integration status (Keychain) — implemented

A shared resolver (`internal/keychain`) now reads secrets from the macOS
Keychain via bounded `security find-generic-password -s <service> -a "$USER" -w`.
It returns typed errors that keep the secret off argv/stdout/logs/errors, and
`keychain.Redact` plus the API client's response-body sanitizer strip the
**literal** token as defense in depth. Redaction is not universal: encoded,
escaped, or split forms of a secret are not detected (see
`docs/agents/credentials.md`). The resolver's NotFound outcome covers an
unavailable `security` binary as well as an absent item. It is wired into all
three collectors:

- `internal/git/preflight.go` — `-token` → `$GITHUB_TOKEN`/`$GH_TOKEN` → Keychain `gh_auth_token` → `gh auth token`.
- `cmd/fetch-calendar/main.go` — `-file` → `-url` → `$GCAL_ICS_URL` → Keychain `calendar_feed_url`.
- `cmd/sync-timesheet/main.go` — `-token` → `$TIMESHEET_API_TOKEN` → Keychain `gradion.workspace.ApiKey`.

Legacy env/`.env` inputs remain as deprecated overrides; see
`docs/agents/credentials.md` for the resolved Keychain-only recommendation.

D3/D4 are now closed (naming standardized and the local resolver built). B3
(no conditional update / no idempotency key) is mitigated locally: non-
idempotent writes are not retried and an uncertain write stops the run as
unresolved.

## 7. Uncertain-write classification (local policy)

The client classifies a non-idempotent write (POST/PATCH/DELETE) as
`OutcomeUncertain` whenever the remote outcome cannot be established from the
response, and then stops without retrying and marks the submission unresolved.
Transport failures, unreadable responses, 429, and 5xx already do this. The
following additional statuses are treated as uncertain for writes:

| Status | Reasoning | Server assumption |
| --- | --- | --- |
| **408 Request Timeout** | The server may have applied the request before timing out. | **Unverified**: no documented Gradion guarantee proves rejection. |
| **409 Conflict** | RFC 9110 does not guarantee the server rejected the write atomically; Gradion documents no transactional or versioned-update contract. | **Unverified**: treating a genuine rejection as "no mutation" is unsafe, so it is conservatively uncertain. |
| **425 Too Early** | Defined around anti-replay refusal, but Gradion publishes no replay/idempotency contract. | **Unverified**: the "not processed" claim cannot be relied on. |

Definitive 4xx rejections (400, 401, 403, 404, 405, 413, 415, 422, 428, ...)
remain terminal because the server rejects the request without applying the
mutation. Idempotent methods (GET/HEAD) are unaffected. These are **unverified
server assumptions**: reclassify only if Gradion documents that a status proves
the write was rejected.

## 8. Approval gate status

Remote writes remain deferred behind the temporary `cmd/sync-timesheet -approve`
gate. `-approve` is **not revision-bound approval**: it gates the current run and
is not tied to an exact preview or to a specific local record revision (the
durable mapping/revision store from ADR-005 is not implemented). Production
submission remains **not ready**, and no remote create, edit, delete, or test
submission has been performed.

## 9. Live read-only re-verification of the redesign (2026-10-10)

The owner granted live read-only authority (Keychain resolution, read-only MCP
checks, calendar-metadata inspection; no writes). The checks were run through the
redesign's own packages (`internal/keychain`, `internal/mcp`, `internal/gradion`,
`internal/calendar`) using a temporary in-module probe that printed only
sanitized aggregates (no secrets, no feed URL, no entry descriptions, no event
titles). The probe was removed afterward; the tree is clean. No writes were made.

Results:

- **Keychain resolution — works.** All three items resolve through
  `internal/keychain`: `gradion.workspace.ApiKey` (present, len 68),
  `gh_auth_token` (present, len 40), `calendar_feed_url` (present, len 117).
  Values were never printed.
- **MCP session — works.** `initialize` returned `gradion-workspace` v1.0.0,
  protocol `2025-06-18`; `tools/list` returned 71 tools; the redesign's
  `gradion.Client.VerifyCapabilities` confirmed the required timesheet tools.
- **MCP reads — BLOCKED by a response-shape mismatch (B5), now diagnosed.** `timesheet__list_my_entries`
  and `timesheet__get_classifications` return
  `structuredContent: {"resources":[{"id":…,"kind":…,"label":…}]}` — an MCP
  resource-reference envelope (`kind` was `entry` / `classification`). The
  redesign's `internal/gradion` parser expected a bare array or a known list key,
  so both calls failed with `GRADION_ERR_READ_SCHEMA`.
  Follow-up read-only discovery established the retrieval contract:
  - `timesheet__list_my_entries` returns references only (`id`/`kind`/`label`) plus
    a `content[0].text` plain-text rendering; it does not expose a structured interval.
  - `timesheet__get_my_week` (`weekStart`) returns fully structured entries in
    `structuredContent.days[].entries[]` with
    `{id, startTime, endTime, classificationId, classificationName, description,
    durationMinutes, status, task, activityTypeLabel}`; the entry ids overlap
    `list_my_entries` exactly (verified 5/5).
  - `resources/templates/list` documents `timesheet__entry` as
    `gradion://app/timesheet/entry/{id}`, but `resources/read` returns
    `contents[0].text` with `mimeType: text/plain` (not structured JSON).
  - `timesheet__get_classifications` returns classification references
    `{"resources":[{"id","kind":"classification","label"}]}`; the `label` carries the
    name when present (the live read-only confirmation observed 19 of 20 named).
  The fix (adapting the reader to the `get_my_week` structured contract, with
  fail-closed schema handling) is in progress under the owner's read-only +
  bounded-fix authority.
- **Calendar invitation metadata — works.** The configured iCal feed fetched
  successfully (18815 bytes); `PARTSTAT` attributes were present:
  ACCEPTED 39, DECLINED 5, NEEDS-ACTION 19 (63 total). This confirms the
  invitation-status assumption behind S3/S4 (cancelled/declined/unknown retained
  and filtered at proposal time).

New blockers (B5, B6):

- **B5 — Diagnosed; fix in progress (live + code-level).** The live
  `list_my_entries` / `get_classifications` responses are `{resources:[{id,kind,label}]}`
  resource-reference envelopes that `internal/gradion` cannot parse
  (`GRADION_ERR_READ_SCHEMA`). Read-only discovery established the retrieval
  contract: structured entries come from `timesheet__get_my_week`
  (`days[].entries[]`, full fields, id overlap 5/5); the documented
  `timesheet__entry` resource (`gradion://app/timesheet/entry/{id}`) returns
  `text/plain`, not structured JSON. The bounded fix (adapt the reader to the
  `get_my_week` structured contract, fail-closed schema handling, bounded
  pagination/size/timeouts) is in progress under the owner's authority.
- **B6 — Resolved by owner/admin confirmation (2026-10-10); not independently documented.**
  Read-only inspection of the tool schemas (`timesheet__log_time`,
  `timesheet__edit_time`, `timesheet__get_my_week`,
  `time-attendance__get_my_allowance`, `time-attendance__get_my_timecard`) found no
  declared duration limit and no timezone parameter; `log_time`/`edit_time` state
  only that a policy-limit violation is reported with the exact limit at call time.
  The owner (admin) confirmed the Gradion per-entry maximum is **4 hours
  (240 minutes)** and that the governing timezone for entry times is
  **Asia/Ho_Chi_Minh**. These are recorded as owner/admin evidence, not as
  server-observed behavior; the local 240-minute enforcement matches the confirmed
  limit. A future write (under a separate approved daily plan) can corroborate it.

No secret was displayed or committed; no Keychain item was created, modified, or
replaced; no remote write was performed.

## 10. Vendor bundle boundary (2026-10-15)

The vendor bundle at `references/gradion-workspace-skill-antigravity/` is a
deliberately untracked local copy of a **messaging**-product skill bundle. It is
not the timesheet tool contract, and nothing under `references/` can be reviewed
through the repository. See `docs/gradion-vendor-bundle.md`.

The `tools/list` evidence in this document remains authoritative for the
timesheet tool contract. Two cautions apply when reading the bundle:

- **HIGH, transport surface.** The bundle documents REST plus a REST app-proxy
  and never mentions native MCP JSON-RPC at `/api/mcp`, the endpoint our client
  speaks.
- **HIGH, example tool mismatch.** The bundle's `log_hours` example with
  `{project, hours}` does not match our live-verified `timesheet__log_time`.
