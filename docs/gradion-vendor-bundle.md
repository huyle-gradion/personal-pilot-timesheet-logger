---
title: "Gradion vendor bundle"
status: "recorded; no change authorized"
date: 2026-10-15
scope: "vendor bundle provenance and non-authority"
---

# Gradion vendor bundle

This note records what the locally copied Gradion vendor bundle is and what it
is not, so it cannot be mistaken for the timesheet tool contract. It records
facts only and authorizes no code or documentation change beyond itself.

## What the bundle is

The bundle is a local copy of a vendor skill bundle for the Gradion Workspace
**messaging** product (channels, direct messages, messages, files, inbox). It
lives at `references/gradion-workspace-skill-antigravity/`, outside the
repository's tracked content.

It contains five files:

- `README.md`
- `SKILL.md`
- `sidecar/inbox-triage.md`
- `sidecar/workflows.yaml`
- `openapi.yaml`

## Git status

The bundle is a deliberately untracked local copy. It is not in the repository
and never has been:

- `.gitignore` line 11 reads
  `# references/ is a vendor bundle deliberately kept out of the repository.`
  and line 12 reads `references/`.
- `git ls-files references` is empty.
- `git log --diff-filter=A -- references/` is empty.
- `git check-ignore -v references/gradion-workspace-skill-antigravity/README.md`
  resolves to `.gitignore:12`.

Because of this, nothing under `references/` can be reviewed through the
repository. The bundle is not a source of record and carries no repository
review weight. This note does not change `.gitignore` and does not start
tracking the bundle.

## What it is not

The bundle is not the timesheet tool contract. It contains no timesheet tool
schema. It points at the catalog at `GET /api/me/apps/mcp` and its
`tools[].inputSchema`, so any schema claim must come from a live read, not from
the bundle.

## HIGH: transport surface mismatch

The bundle documents REST for the messaging product plus a REST app-proxy. It
never mentions native MCP JSON-RPC at `/api/mcp`, which is the endpoint our
client actually speaks. A reader who treats the bundle as our transport
contract would look for the wrong surface and would not find the transport we
use.

## HIGH: the example tool does not match our contract

The bundle's lone timesheet-flavored tool example, `log_hours` with arguments
`{project, hours}`, is a generic app-proxy `tools/call` pattern invoked against
`/api/me/apps/timesheet/tools/log_hours/call`. The vendor text does not itself
mark the example as illustrative, so nothing about the timesheet tool contract
should be inferred from it either way. The bundle names `openapi.yaml` as its
single source of truth for endpoints, parameters and response bodies, while the
shape of an app's tool schema comes from the catalog at `/api/me/apps/mcp`.

The example does not match our live-verified `timesheet__log_time`, whose field
set is
`{date, startTime, endTime, description, classificationId|classification, task, billable}`.
Authoritative discovery of the timesheet tool contract is a native `tools/list`,
not the example. Our contract evidence is in `docs/gradion-mcp-verification.md`.

## Recorded discrepancies, not decided here

Each line below is LATENT: no vendor text contradicts our claims, and where the
vendor is silent nothing was inferred from the silence. The first two have
product-visible potential and are deferred to the owner rather than decided
here.

- MEDIUM, product-visible potential: a documented bulk export endpoint `GET /api/me/apps/{slug}/export/{dataset}` (cursor pagination, `limit` clamped 1..1000, `updated_since` incremental mode with tombstones, a `rows_schema_mismatch` error) that we do not use, because we read only via `get_my_week`/`list_my_entries`. Deferred: treat as a whole-dataset listing source or record why MCP remains the source.
- MEDIUM, product-visible potential: the vendor documents `resources[].deeplink` on the app-proxy tool-call result, while our parser decodes only id/kind/label in the native listing envelope; whether the native listing carries a deeplink is unverified. Deferred: surface them or record the omission.
- LOW: `readOnlyHint` documented on catalog tools but unused, `VerifyCapabilities` only checks presence.
- LOW: the `X-Gradion-Skill-Bundle-Version` header and its `skill_bundle_outdated` error are documented for REST and silent for `/api/mcp`.
- LOW: proxy error categories (`scope_action_not_allowed`, `scope_app_not_allowed`, `tool_not_found`) are not mapped to our codes.
- LOW: our local default page size of 50 versus the documented server default of 10. No protocol breach; local policy worth documenting.
- LOW: optional fields we document but do not exercise (`classification` as an alternative to `classificationId`, `activityTypeId` for `get_classifications`).

## Status and scope

This note records facts and does not authorize a code change or a
documentation change beyond itself. The two product-visible items above are
owner decisions. Production submission remains blocked.

## Provenance

Filed from the LIBRARIAN-1 vendor verification, recorded as issue #58.
