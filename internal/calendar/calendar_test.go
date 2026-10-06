package calendar_test

import (
	"context"
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
