# Timesheet 0.1.1

The owner selected a forward-only correction: retain published commits and tags, use the work identity for new commits, normalize product terminology completely in the current tracked tree, and ship every application changeset under SemVer.

## Publication identity

GitHub account: `huyle-gradion`. Git author and committer: `Huy Le <huy.leminh@gradion.com>`. Repository-local identity is pinned; global Git settings are untouched. The publication rules are recorded in `AGENTS.md` for subsequent sessions. Existing personal attribution remains in historical commits by the owner's explicit choice; this patch does not claim to erase history.

## Changes

- Application, adapter negotiation and package/root-lock stamps: `0.1.1`.
- Private Go namespace: `internal/timesheet`, with all package declarations/imports/callers migrated and no compatibility alias.
- Product, source-path, documentation and operator-facing names normalized throughout the tracked tree. Historical engineering records use normalized labels; original verbatim labels and checksum provenance remain available from their original Git commits. Historical branch references are represented by their recorded commit identifiers, not invented new branches.
- Existing configured data and credential paths remain authoritative. Example directory names are not a migration instruction; no real state, credentials, profiles or remote bindings are moved or replaced.
- Protocol 1, state schema 3, durable JSON field names, dependency pins and accounting/approval semantics remain unchanged. The MCP client display identity is Timesheet.

## Verification

All tracked file contents and paths were audited case-insensitively for the superseded label: zero matches. Go formatting/vet and all retained tests passed, as did strict TypeScript and launcher syntax validation. The actual pinned Pi TUI plus compiled 0.1.1 child passed local confirm/save/cancel/restart/edit/reload/normal shutdown and lock-release checks on Linux.

No new Darwin execution is claimed. Earlier owner-supplied Darwin local workflow/lifecycle and native migration evidence remain attributed prior evidence. Release Darwin assets are cross-compiled. Live model/provider authentication and real Gradion submission remain unverified. Known upstream SDK limitations are unchanged and remain tracked in issue #137; no dependency security fix is claimed.

The integration PR and GitHub release hold the final merged source identity and asset checksums. No direct-to-main record commit, moved historical tag, force-push, real timesheet write or private-file publication is part of this patch.
