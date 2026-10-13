# [M0-01] Calendar iCal Fetcher & Parser with TigerStyle Safety

## 🎯 Vertical Slice
Implement the Google Calendar private iCal feed fetcher and parser as a standalone CLI (`cmd/fetch-calendar`) and package (`internal/calendar`). Reads `GCAL_ICS_URL`, filters events within a specified date window, and outputs normalized `SimpleEntry` JSON.

## 🔗 Blocking Edges
- **Blocked by**: None
- **Blocks**: `[M2-01]`, `[M2-02]`

## 📖 Context & Specification
- **Spec Reference**: Section 3.1 (Google Calendar Source) & Note-2 (iCal private URL format).
- **ADR Reference**: ADR 0001 (Section 3: SimpleEntry Data Contract).
- **Engineering Guidelines**: Strict TigerStyle conformance (5-second HTTP timeout, 10MB response read limit, unescaped HTML JSON formatting, typed errors).

## 📐 Pre-Agreed Seams & Interface Contracts
- **CLI Usage**:
  ```bash
  go run ./cmd/fetch-calendar -date=YYYY-MM-DD
  ```
- **Output JSON Contract**:
  ```json
  [
    {
      "id": "gcal-12345",
      "source": "calendar",
      "timestamp": "2026-10-07T09:00:00+07:00",
      "endTime": "2026-10-07T10:00:00+07:00",
      "title": "Daily Standup",
      "description": "Team sync",
      "metadata": {
        "status": "CONFIRMED",
        "location": "Google Meet"
      }
    }
  ]
  ```

## ✅ Acceptance Criteria (DoD)
- [x] Implements private iCal URL fetching with `net/http` and bounded context timeout.
- [x] Parses VEVENT components, DTSTART, DTEND, SUMMARY, DESCRIPTION, and STATUS.
- [x] Filters out cancelled events (`STATUS:CANCELLED`) and events outside the target day.
- [x] Outputs valid `SimpleEntry` JSON array to stdout without HTML escaping.
- [x] Unit test coverage covering edge cases (all-day events, empty calendar, network timeout, malformed iCal).
- [x] Includes stable event `id` (`gcal-<UID>`) in `SimpleEntry` for idempotent downstream reconciliation.
- [x] Filters out declined meetings (`PARTSTAT=DECLINED` for user attendee).
- [x] Handles recurring event exceptions (`EXDATE` timestamps subtracted from `RRULE` expansion).
- [x] Flags or filters transparent events (`TRANSP:TRANSPARENT`).
- [ ] Pre-flight Guided Onboarding (ADR-005): If `GCAL_ICS_URL` is unset, provides step-by-step guidance on obtaining the secret iCal URL from Google Calendar settings.

## 🧪 Verification Commands
```bash
go test -v ./internal/calendar/...
go run ./cmd/fetch-calendar -h
```
