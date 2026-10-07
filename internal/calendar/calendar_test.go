package calendar_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/calendar"
)

func TestExtractPRs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty text",
			input:    "",
			expected: []string{},
		},
		{
			name:     "single PR",
			input:    "feat: implement feature #42",
			expected: []string{"#42"},
		},
		{
			name:     "multiple PRs unsorted with duplicates",
			input:    "Reviewing #102, #15, and #102 again with #3",
			expected: []string{"#3", "#15", "#102"},
		},
		{
			name:     "no PRs",
			input:    "Just a normal meeting without numbers",
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calendar.ExtractPRs(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("at index %d: expected %s, got %s", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	ascii := "hello world"
	if got := calendar.TruncateString(ascii, 5); got != "hello" {
		t.Errorf("expected 'hello', got '%s'", got)
	}

	utf8Str := "Xin chào thế giới"
	truncated := calendar.TruncateString(utf8Str, 8)
	if len([]byte(truncated)) > 8 {
		t.Errorf("truncated string exceeds 8 bytes: %d", len([]byte(truncated)))
	}
}

func TestRedactURL(t *testing.T) {
	secretURL := "https://calendar.google.com/calendar/ical/user%40example.com/private-secrettoken12345/basic.ics"
	redacted := calendar.RedactURL(secretURL)

	if strings.Contains(redacted, "secrettoken12345") {
		t.Fatalf("RedactURL leaked secret token: %s", redacted)
	}
	if strings.Contains(redacted, "user%40example.com") {
		t.Fatalf("RedactURL leaked user email: %s", redacted)
	}
	if !strings.HasPrefix(redacted, "https://calendar.google.com/") {
		t.Errorf("expected host preserved, got %s", redacted)
	}
}

func TestParseCalendarEvents_ValidAndRecurring(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-regular@google.com
DTSTART:20261006T020000Z
DTEND:20261006T033000Z
SUMMARY:Sprint Sync & Planning
DESCRIPTION:Discussed roadmap and #101 #42
LOCATION:Google Meet
END:VEVENT
BEGIN:VEVENT
UID:event-recurring@google.com
DTSTART:20261001T070000Z
DTEND:20261001T080000Z
RRULE:FREQ=DAILY;INTERVAL=1
SUMMARY:Daily Standup
DESCRIPTION:Morning check-in
END:VEVENT
END:VCALENDAR`

	loc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	entries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// First event: UTC 02:00 -> +07:00 is 09:00 - 10:30
	e1 := entries[0]
	if e1.StartTime != "09:00" || e1.EndTime != "10:30" {
		t.Errorf("expected 09:00-10:30, got %s-%s", e1.StartTime, e1.EndTime)
	}
	if e1.Topic != "Sprint Sync & Planning" {
		t.Errorf("unexpected topic: %s", e1.Topic)
	}
	if len(e1.PRs) != 2 || e1.PRs[0] != "#42" || e1.PRs[1] != "#101" {
		t.Errorf("unexpected PRs: %v", e1.PRs)
	}
	if !strings.Contains(e1.Description, "PRs: #42, #101") {
		t.Errorf("expected inline PRs in description, got: %s", e1.Description)
	}

	// Second event: Recurring daily standup at UTC 07:00 -> +07:00 is 14:00 - 15:00
	e2 := entries[1]
	if e2.StartTime != "14:00" || e2.EndTime != "15:00" {
		t.Errorf("expected 14:00-15:00, got %s-%s", e2.StartTime, e2.EndTime)
	}
	if e2.Topic != "Daily Standup" {
		t.Errorf("unexpected recurring topic: %s", e2.Topic)
	}
}

func TestParseCalendarEvents_SkipCancelled(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-active@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
STATUS:CONFIRMED
SUMMARY:Active Meeting
END:VEVENT
BEGIN:VEVENT
UID:event-cancelled@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
STATUS:CANCELLED
SUMMARY:Cancelled Meeting
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	entries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 active entry (skipping cancelled), got %d", len(entries))
	}
	if entries[0].Topic != "Active Meeting" {
		t.Errorf("expected 'Active Meeting', got %q", entries[0].Topic)
	}
}

func TestParseCalendarEvents_EventID(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:2v34l62da2s0ltoa9sj3p83gld@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
SUMMARY:Kickoff Meeting
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	entries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	expectedID := "gcal-2v34l62da2s0ltoa9sj3p83gld@google.com"
	if entries[0].ID != expectedID {
		t.Errorf("expected ID %q, got %q", expectedID, entries[0].ID)
	}

	jsonBytes, err := calendar.FormatJSON(entries, false)
	if err != nil {
		t.Fatalf("failed to format JSON: %v", err)
	}
	if !strings.Contains(string(jsonBytes), fmt.Sprintf(`"id": "%s"`, expectedID)) {
		t.Errorf("expected JSON to contain event ID %q, got: %s", expectedID, string(jsonBytes))
	}
}

func TestParseCalendarEvents_DeclinedMeetings(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
X-WR-CALNAME:huy.leminh@gradion.com
BEGIN:VEVENT
UID:event-declined-by-user@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
SUMMARY:Declined Meeting
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=DECLINED;CN=Huy Le:mailto:huy.leminh@gradion.com
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=Colleague:mailto:colleague@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:event-accepted-by-user@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
SUMMARY:Accepted Meeting
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=Huy Le:mailto:huy.leminh@gradion.com
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=DECLINED;CN=Colleague:mailto:colleague@gradion.com
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	// Test with explicit user email option
	entries, err := calendar.ParseCalendarEventsWithOptions([]byte(icsData), calendar.ParseOptions{
		TargetDate: targetDate,
		Location:   loc,
		UserEmail:  "huy.leminh@gradion.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (declined meeting filtered, accepted meeting kept), got %d", len(entries))
	}
	if entries[0].Topic != "Accepted Meeting" {
		t.Errorf("expected 'Accepted Meeting', got %q", entries[0].Topic)
	}

	// Test inference from X-WR-CALNAME when UserEmail option is empty
	inferredEntries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error with inferred owner: %v", err)
	}
	if len(inferredEntries) != 1 || inferredEntries[0].Topic != "Accepted Meeting" {
		t.Fatalf("expected 1 entry with X-WR-CALNAME inference, got %d", len(inferredEntries))
	}
}

func TestParseCalendarEvents_RecurringEXDATE(t *testing.T) {
	// Recurring daily standup at 02:00 UTC with an EXDATE on 2026-10-06
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:daily-standup@google.com
DTSTART:20261001T020000Z
DTEND:20261001T023000Z
RRULE:FREQ=DAILY;INTERVAL=1
EXDATE:20261006T020000Z
SUMMARY:Daily Standup
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	dateExcluded := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)
	dateActive := time.Date(2026, 10, 7, 0, 0, 0, 0, loc)

	// Target date matches EXDATE -> occurrence should be excluded
	entriesExcluded, err := calendar.ParseCalendarEvents([]byte(icsData), dateExcluded, loc)
	if err != nil {
		t.Fatalf("unexpected error on excluded date: %v", err)
	}
	if len(entriesExcluded) != 0 {
		t.Fatalf("expected 0 entries on EXDATE, got %d", len(entriesExcluded))
	}

	// Target date after EXDATE -> occurrence should be present
	entriesActive, err := calendar.ParseCalendarEvents([]byte(icsData), dateActive, loc)
	if err != nil {
		t.Fatalf("unexpected error on active date: %v", err)
	}
	if len(entriesActive) != 1 {
		t.Fatalf("expected 1 entry on active date, got %d", len(entriesActive))
	}
	if entriesActive[0].Topic != "Daily Standup" {
		t.Errorf("expected 'Daily Standup', got %q", entriesActive[0].Topic)
	}
}

func TestParseCalendarEvents_RecurringEXDATE_WithTimezoneAndDateOnly(t *testing.T) {
	loc := time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
	dateExcluded := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	// EXDATE with TZID parameter
	icsDataTZID := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:daily-standup-tz@google.com
DTSTART:20261001T020000Z
DTEND:20261001T023000Z
RRULE:FREQ=DAILY;INTERVAL=1
EXDATE;TZID=Asia/Ho_Chi_Minh:20261006T090000
SUMMARY:Daily Standup
END:VEVENT
END:VCALENDAR`

	entriesTZ, err := calendar.ParseCalendarEvents([]byte(icsDataTZID), dateExcluded, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entriesTZ) != 0 {
		t.Fatalf("expected 0 entries on EXDATE with TZID, got %d", len(entriesTZ))
	}

	// EXDATE with VALUE=DATE
	icsDataDateOnly := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:daily-standup-date@google.com
DTSTART:20261001T020000Z
DTEND:20261001T023000Z
RRULE:FREQ=DAILY;INTERVAL=1
EXDATE;VALUE=DATE:20261006
SUMMARY:Daily Standup
END:VEVENT
END:VCALENDAR`

	entriesDateOnly, err := calendar.ParseCalendarEvents([]byte(icsDataDateOnly), dateExcluded, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entriesDateOnly) != 0 {
		t.Fatalf("expected 0 entries on EXDATE with VALUE=DATE, got %d", len(entriesDateOnly))
	}
}

func TestParseCalendarEvents_RecurrenceIDException(t *testing.T) {
	// Master recurring series daily at 02:00-02:30 UTC (09:00-09:30 local).
	// Exception event on 2026-10-06 has RECURRENCE-ID:20261006T020000Z, moved to 04:00-05:00 UTC (11:00-12:00 local) with modified title.
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:recur-sync@google.com
DTSTART:20261001T020000Z
DTEND:20261001T023000Z
RRULE:FREQ=DAILY;INTERVAL=1
SUMMARY:Daily Sync
END:VEVENT
BEGIN:VEVENT
UID:recur-sync@google.com
RECURRENCE-ID:20261006T020000Z
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
SUMMARY:Rescheduled Sync & Architecture Review
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	entries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have exactly 1 entry: the modified exception instance, not the old master occurrence
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (master occurrence replaced by RECURRENCE-ID exception), got %d", len(entries))
	}
	if entries[0].Topic != "Rescheduled Sync & Architecture Review" {
		t.Errorf("expected modified summary, got %q", entries[0].Topic)
	}
	if entries[0].StartTime != "04:00" || entries[0].EndTime != "05:00" {
		t.Errorf("expected 04:00-05:00, got %s-%s", entries[0].StartTime, entries[0].EndTime)
	}
	if entries[0].ID != "gcal-recur-sync@google.com" {
		t.Errorf("expected stable event ID, got %q", entries[0].ID)
	}
}

func TestParseCalendarEvents_RecurrenceIDException_Cancelled(t *testing.T) {
	// Exception event on 2026-10-06 with STATUS:CANCELLED and RECURRENCE-ID
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:recur-cancel@google.com
DTSTART:20261001T020000Z
DTEND:20261001T023000Z
RRULE:FREQ=DAILY;INTERVAL=1
SUMMARY:Daily Sync
END:VEVENT
BEGIN:VEVENT
UID:recur-cancel@google.com
RECURRENCE-ID:20261006T020000Z
STATUS:CANCELLED
DTSTART:20261006T020000Z
DTEND:20261006T023000Z
SUMMARY:Daily Sync
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	entries, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cancelled occurrence should be completely omitted
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (single occurrence cancelled via RECURRENCE-ID), got %d", len(entries))
	}
}

func TestParseCalendarEvents_TransparentEvents(t *testing.T) {
	icsData := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-transparent@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
TRANSP:TRANSPARENT
SUMMARY:Working from Home Note
END:VEVENT
BEGIN:VEVENT
UID:event-opaque@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
TRANSP:OPAQUE
SUMMARY:Focus Sprint Work
END:VEVENT
END:VCALENDAR`

	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	// Default: transparent events are included and flagged
	entriesDefault, err := calendar.ParseCalendarEvents([]byte(icsData), targetDate, loc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entriesDefault) != 2 {
		t.Fatalf("expected 2 entries by default, got %d", len(entriesDefault))
	}
	if !entriesDefault[0].Metadata.Transparent {
		t.Errorf("expected transparent event to have Metadata.Transparent = true")
	}
	if entriesDefault[1].Metadata.Transparent {
		t.Errorf("expected opaque event to have Metadata.Transparent = false")
	}

	// Formatted JSON should include "transparent": true
	jsonBytes, err := calendar.FormatJSON(entriesDefault, false)
	if err != nil {
		t.Fatalf("failed to format JSON: %v", err)
	}
	if !strings.Contains(string(jsonBytes), `"transparent": true`) {
		t.Errorf("expected JSON to contain '\"transparent\": true', got: %s", string(jsonBytes))
	}

	// With SkipTransparent: true
	entriesFiltered, err := calendar.ParseCalendarEventsWithOptions([]byte(icsData), calendar.ParseOptions{
		TargetDate:      targetDate,
		Location:        loc,
		SkipTransparent: true,
	})
	if err != nil {
		t.Fatalf("unexpected error with SkipTransparent: %v", err)
	}
	if len(entriesFiltered) != 1 {
		t.Fatalf("expected 1 entry when skipping transparent, got %d", len(entriesFiltered))
	}
	if entriesFiltered[0].Topic != "Focus Sprint Work" {
		t.Errorf("expected 'Focus Sprint Work', got %q", entriesFiltered[0].Topic)
	}
}

func TestEmailHelpers(t *testing.T) {
	raw := "mailto:<Huy.LeMinh@Gradion.com>  "
	normalized := calendar.NormalizeEmail(raw)
	if normalized != "huy.leminh@gradion.com" {
		t.Errorf("unexpected normalized email: %q", normalized)
	}

	url1 := "https://calendar.google.com/calendar/ical/huy.leminh%40gradion.com/private-token123/basic.ics"
	extracted1 := calendar.ExtractEmailFromICSURL(url1)
	if extracted1 != "huy.leminh@gradion.com" {
		t.Errorf("unexpected extracted email from URL: %q", extracted1)
	}

	url2 := "https://calendar.google.com/calendar/ical/john.doe@example.com/private-token123/basic.ics"
	extracted2 := calendar.ExtractEmailFromICSURL(url2)
	if extracted2 != "john.doe@example.com" {
		t.Errorf("unexpected extracted email from URL: %q", extracted2)
	}

	invalidURL := "https://calendar.google.com/calendar/invalid/path"
	extracted3 := calendar.ExtractEmailFromICSURL(invalidURL)
	if extracted3 != "" {
		t.Errorf("expected empty email from invalid URL, got %q", extracted3)
	}
}

func TestParseCalendarEvents_MalformedICS(t *testing.T) {
	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	_, err := calendar.ParseCalendarEvents([]byte("NOT A VALID ICS FILE"), targetDate, loc)
	if err == nil {
		t.Fatal("expected error parsing invalid ICS, got nil")
	}

	var opErr *calendar.OperatingError
	if !strings.Contains(err.Error(), "CAL_ERR_PARSE_ICS") {
		t.Errorf("expected CAL_ERR_PARSE_ICS code, got %v", err)
	}
	_ = opErr
}

func TestFetchICS_RedirectDisabled(t *testing.T) {
	// TigerStyle: Redirects must be treated as terminal protocol errors, never followed
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	defer ts.Close()

	ctx := context.Background()
	_, err := calendar.FetchICS(ctx, ts.URL, "", true)
	if err == nil {
		t.Fatal("expected error on redirect, got nil")
	}

	if !strings.Contains(err.Error(), "CAL_ERR_HTTP_STATUS") {
		t.Errorf("expected CAL_ERR_HTTP_STATUS on redirect, got %v", err)
	}
}

func TestFetchICS_CapacityBoundExceeded(t *testing.T) {
	// Server returns payload exceeding MaxResponseSizeBytes
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		// Write MaxResponseSizeBytes + 10 bytes
		data := make([]byte, calendar.MaxResponseSizeBytes+10)
		_, _ = w.Write(data)
	}))
	defer ts.Close()

	ctx := context.Background()
	_, err := calendar.FetchICS(ctx, ts.URL, "", true)
	if err == nil {
		t.Fatal("expected capacity error, got nil")
	}

	if !strings.Contains(err.Error(), "CAL_ERR_RESPONSE_TOO_LARGE") {
		t.Errorf("expected CAL_ERR_RESPONSE_TOO_LARGE, got %v", err)
	}
}

func TestFetchICS_LocalFileAndCache(t *testing.T) {
	tmpDir := t.TempDir()
	samplePath := filepath.Join(tmpDir, "test.ics")
	content := []byte("BEGIN:VCALENDAR\nVERSION:2.0\nEND:VCALENDAR")
	if err := os.WriteFile(samplePath, content, 0600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	got, err := calendar.FetchICS(ctx, samplePath, "", true)
	if err != nil {
		t.Fatalf("unexpected error reading local file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %s", string(got))
	}
}

func TestFetchICS_SymlinkRejected(t *testing.T) {
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "real.ics")
	if err := os.WriteFile(realFile, []byte("BEGIN:VCALENDAR\nEND:VCALENDAR"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(tmpDir, "link.ics")
	if err := os.Symlink(realFile, symlinkPath); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, err := calendar.FetchICS(ctx, symlinkPath, "", true)
	if err == nil {
		t.Fatal("expected error reading symlink, got nil")
	}
	if !strings.Contains(err.Error(), "CAL_ERR_SYMLINK_REJECTED") {
		t.Errorf("expected CAL_ERR_SYMLINK_REJECTED, got %v", err)
	}
}

func TestFormatJSON_UnescapedHTML(t *testing.T) {
	calName := "Sprint Review & Retro"
	entries := []calendar.SimpleEntry{
		{
			Date:        "2026-10-06",
			StartTime:   "10:00",
			EndTime:     "11:00",
			Topic:       calName,
			Description: "Review & Retro with <Team>",
			PRs:         []string{"#1"},
			Sources: calendar.EntrySources{
				CalendarEvent: &calName,
				Repo:          nil,
				Commits:       []string{},
			},
		},
	}

	out, err := calendar.FormatJSON(entries, false)
	if err != nil {
		t.Fatal(err)
	}

	jsonStr := string(out)
	if strings.Contains(jsonStr, `\u0026`) {
		t.Errorf("JSON should not escape '&', found \\u0026 in: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `\u003c`) {
		t.Errorf("JSON should not escape '<', found \\u003c in: %s", jsonStr)
	}
}

// TestAssertion_Subprocess tests that programmer-contract violation panics and terminates process
func TestAssertion_Subprocess(t *testing.T) {
	if os.Getenv("BE_FATAL_ASSERTION_TEST") == "1" {
		// Deliberately trigger invariant violation: nil location
		_, _ = calendar.ParseCalendarEvents([]byte("BEGIN:VCALENDAR\nEND:VCALENDAR"), time.Now(), nil)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestAssertion_Subprocess")
	cmd.Env = append(os.Environ(), "BE_FATAL_ASSERTION_TEST=1")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("expected fatal assertion to exit with error, but exited successfully. Output: %s", string(out))
	}

	if !strings.Contains(string(out), calendar.AssertLocationNil) {
		t.Errorf("expected output to contain invariant identifier %s, got: %s", calendar.AssertLocationNil, string(out))
	}
}
