#!/usr/bin/env bash
set -euo pipefail

# Script to sync tracer-bullet tickets to GitHub Issues for huyle-gradion/personal-pilot-timesheet-logger
# Usage:
#   GITHUB_TOKEN="ghp_xxx" ./scripts/sync-github-issues.sh

REPO="huyle-gradion/personal-pilot-timesheet-logger"
API_URL="https://api.github.com/repos/${REPO}"

# If GITHUB_TOKEN is unset, try loading from macOS Keychain (item: gh_auth_token)
if [ -z "${GITHUB_TOKEN:-}" ] && command -v security >/dev/null 2>&1; then
  GITHUB_TOKEN=$(security find-generic-password -a "$USER" -s "gh_auth_token" -w 2>/dev/null || true)
  export GITHUB_TOKEN
fi

# Fallback: Load from .env if present and not set
if [ -z "${GITHUB_TOKEN:-}" ] && [ -f .env ]; then
  GITHUB_TOKEN=$(grep -E '^GITHUB_TOKEN=' .env | head -n 1 | cut -d '=' -f2- | sed -e 's/^[[:space:]]*["'"'"']//' -e 's/["'"'"'][[:space:]]*$//')
  export GITHUB_TOKEN
fi

if [ -z "${GITHUB_TOKEN:-}" ]; then
  echo "Error: GITHUB_TOKEN environment variable is required (or in macOS Keychain under 'gh_auth_token')."
  echo "Run with: GITHUB_TOKEN=your_token ./scripts/sync-github-issues.sh"
  exit 1
fi

AUTH_HEADER="Authorization: Bearer ${GITHUB_TOKEN}"
JSON_HEADER="Accept: application/vnd.github+json"

echo "==> Creating Milestones on ${REPO}..."

create_milestone() {
  local title="$1"
  local desc="$2"
  
  # Check if exists
  local existing_id
  existing_id=$(curl -s -H "${AUTH_HEADER}" -H "${JSON_HEADER}" "${API_URL}/milestones?state=all" | jq -r ".[] | select(.title==\"${title}\") | .number")
  
  if [ -n "${existing_id}" ] && [ "${existing_id}" != "null" ]; then
    echo "  Milestone '${title}' already exists (#${existing_id})" >&2
    echo "${existing_id}"
  else
    local res
    res=$(curl -s -X POST -H "${AUTH_HEADER}" -H "${JSON_HEADER}" "${API_URL}/milestones" \
      -d "{\"title\":\"${title}\",\"description\":\"${desc}\"}")
    local new_id
    new_id=$(echo "${res}" | jq -r '.number')
    echo "  Created milestone '${title}' (#${new_id})" >&2
    echo "${new_id}"
  fi
}

M0_ID=$(create_milestone "M0: Calendar Foundation" "Calendar iCal fetcher and parser")
M1_ID=$(create_milestone "M1: Resilient Git Activity Collector" "Two-tier Git/GitHub collector with offline fallback")
M2_ID=$(create_milestone "M2: Time Blocker & Interactive Reviewer" "Time block clustering, collision detection, and review UX")
M3_ID=$(create_milestone "M3: Timesheet API Sync & Benchmarking" "Gradion API reconciliation and empirical benchmark CLI")
M4_ID=$(create_milestone "M4: Claude Code Skill Packaging & E2E Verification" "Skill packaging and full pilot validation")

echo ""
echo "==> Syncing Tracer-Bullet Tickets as GitHub Issues..."

create_issue() {
  local file="$1"
  local milestone_num="$2"
  local label="$3"
  
  local title
  title=$(head -n 1 "${file}" | sed 's/^# //')
  local body
  body=$(cat "${file}")

  # Check if issue already exists with matching title
  local existing_issue
  existing_issue=$(curl -s -H "${AUTH_HEADER}" -H "${JSON_HEADER}" "${API_URL}/issues?state=all" | jq -r ".[] | select(.title==\"${title}\") | .number")

  if [ -n "${existing_issue}" ] && [ "${existing_issue}" != "null" ]; then
    echo "  Issue '${title}' already exists (#${existing_issue})"
  else
    local payload
    payload=$(jq -n \
      --arg title "${title}" \
      --arg body "${body}" \
      --argjson ms "${milestone_num}" \
      --arg label "${label}" \
      '{title: $title, body: $body, milestone: $ms, labels: ["tracer-bullet", $label]}')

    local res
    res=$(curl -s -X POST -H "${AUTH_HEADER}" -H "${JSON_HEADER}" "${API_URL}/issues" -d "${payload}")
    local num
    num=$(echo "${res}" | jq -r '.number')
    echo "  Created issue '${title}' (#${num})"
  fi
}

create_issue ".tickets/M0-01-calendar-fetcher.md" "${M0_ID}" "m0"
create_issue ".tickets/M1-01-github-api-client.md" "${M1_ID}" "m1"
create_issue ".tickets/M1-02-local-git-fallback.md" "${M1_ID}" "m1"
create_issue ".tickets/M1-03-git-collector-orchestrator.md" "${M1_ID}" "m1"
create_issue ".tickets/M2-01-time-block-clusterer.md" "${M2_ID}" "m2"
create_issue ".tickets/M2-02-collision-detector-review-cli.md" "${M2_ID}" "m2"
create_issue ".tickets/M3-01-gradion-api-reconciliation.md" "${M3_ID}" "m3"
create_issue ".tickets/M3-02-benchmark-tracker-cli.md" "${M3_ID}" "m3"
create_issue ".tickets/M4-01-claude-skill-packaging.md" "${M4_ID}" "m4"
create_issue ".tickets/M4-02-e2e-verification-pilot.md" "${M4_ID}" "m4"

echo ""
echo "==> Done! All tickets mapped and synced to GitHub Issues."
