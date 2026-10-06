# Note No.2

## Researching

### Google Calendar Integration - Option 1: Secret iCal (.ics) Feed

- **Approach**: Fetch Google Calendar events via the private iCal feed URL without needing Google Cloud project or OAuth consent.
- **Script Location**: `scripts/fetch_calendar.py`
- **Setup Requirements**:
  1. Open Google Calendar on the web.
  2. Click **Settings** (gear icon) -> click your calendar under **Settings for my calendars**.
  3. Scroll down to **Integrate calendar**.
  4. Copy **Secret address in iCal format** (`https://calendar.google.com/calendar/ical/.../private-.../basic.ics`).
  5. Add to `.env` as `GCAL_ICS_URL="<URL>"`.
- **Key Features**:
  - Automatically handles recurring events via `recurring-ical-events`.
  - Converts UTC/source timestamps to local timezone (`Asia/Ho_Chi_Minh`).
  - Supports `--format compact` (optimized for token budget in AI prompt), `--format json`, and `--format table`.
  - Local caching (5-minute TTL) to prevent rate limits during development.
