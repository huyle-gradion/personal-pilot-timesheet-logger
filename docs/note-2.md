# Note No.2

## Researching

### Google Calendar Integration - Option 1: Secret iCal (.ics) Feed

- **Approach**: Fetch Google Calendar events via the private iCal feed URL without needing Google Cloud project or OAuth consent.
- **Implementation**:
  - Go: `cmd/fetch-calendar/main.go` (fast standalone binary, 0 runtime python dependency)
  - Python (archived): `archive/fetch_calendar.py`
- **Setup Requirements**:
  1. Open Google Calendar on the web.
  2. Click **Settings** (gear icon) -> click your calendar under **Settings for my calendars**.
  3. Scroll down to **Integrate calendar**.
  4. Copy **Secret address in iCal format** (`https://calendar.google.com/calendar/ical/.../private-.../basic.ics`).
  5. Add to `.env` as `GCAL_ICS_URL="<URL>"`.
- **Key Features**:
  - Automatically handles recurring events via RRULE expansion (`rrule-go`).
  - Converts UTC/source timestamps to local timezone (`Asia/Ho_Chi_Minh`).
  - Defaults to JSON output (Simple Entry array for Step 3 AI input).
  - Supports `-minified` flag for token-budget efficiency.
  - Supports `-format entry` for Section 3.2 markdown inspection.
  - Local caching (5-minute TTL) to prevent rate limits during development.

#### Disadventages

The iCal will be delayed from the real Google Calendar web version from 5 minutes

## Edge case

### No.1: PRs and Commits collision

Because of PR is a combination of (multiple) commit into one so it could cause duplicate version of commits if you combine everything at once

### No.2: Calendar Cancelled & Declined Events

- **Cancelled tombstones**: RFC 5545 `STATUS:CANCELLED` events remain in the private iCal feed after deletion. If not explicitly filtered, deleted meetings appear as active timesheet entries.
- **Declined invitations**: When an attendee clicks "No" (declines) on a meeting invitation, Google Calendar keeps the event in the private feed with `PARTSTAT=DECLINED` in the `ATTENDEE` property matching the user's email. If participation status is ignored, declined meetings are incorrectly logged as worked time.

### No.3: Recurring Event Exceptions (EXDATE & RECURRENCE-ID)

- When a single instance of a recurring series (e.g. daily standup or weekly sync) is cancelled or rescheduled, Google Calendar emits an `EXDATE` property on the master event or a separate `VEVENT` with a matching `RECURRENCE-ID`.
- A naive `RRULE` expansion generates occurrences blindly without subtracting `EXDATE` timestamps, resulting in ghost meetings appearing on days when that instance was cancelled.

### No.4: Missing Event Identity (UID) for Idempotent Reconciliation

- Every iCal event has a unique RFC 5545 `UID`. If the intermediate data contract (`SimpleEntry`) omits a stable `id` (e.g. `"id": "gcal-<UID>"`), downstream Time Blocker (M2) and Timesheet Reconciliation (M3) cannot deterministically deduplicate events across re-runs, risking duplicate timesheet submissions.

### No.5: Transparent Calendar Events (TRANSP:TRANSPARENT)

- Events marked as `TRANSP:TRANSPARENT` represent "Available / Free" time (e.g. reminders, all-day notes, working location notices) rather than active commitments (`TRANSP:OPAQUE`). Treating transparent events as busy work blocks inflates timesheet hours.

