#!/usr/bin/env bash
set -euo pipefail

# Regenerate (or verify) the one generated file this repository mirrors into the
# Obsidian vault:
#
#   ./scripts/sync-vault.sh --write                  regenerate the vault file
#   ./scripts/sync-vault.sh --check                  verify it without writing
#   ./scripts/sync-vault.sh --write --vault DIR      override the vault root
#
# The generator writes exactly one file:
#   <vault>/01 - Projects/assignment-1 (generated).md
# It never touches the authored "Project - assignment-1.md", the dashboard, or
# any daily note, and it never writes a repository file.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

VAULT="${HOME}/ai-database"
MODE=""

while [ $# -gt 0 ]; do
  case "$1" in
    --write|--check)
      MODE="$1"
      shift
      ;;
    --vault)
      VAULT="${2:-}"
      shift 2
      ;;
    *)
      echo "Unknown argument: $1" >&2
      echo "Usage: ./scripts/sync-vault.sh --write|--check [--vault DIR]" >&2
      exit 2
      ;;
  esac
done

if [ -z "${MODE}" ]; then
  echo "Usage: ./scripts/sync-vault.sh --write|--check [--vault DIR]" >&2
  exit 2
fi

cd "${REPO_ROOT}"
exec go run ./scripts/vaultgen "${MODE}" --vault "${VAULT}"
