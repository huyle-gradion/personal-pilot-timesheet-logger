package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

const calendarTestICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
X-WR-CALNAME:huy.leminh@gradion.com
BEGIN:VEVENT
UID:active@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
STATUS:CONFIRMED
SUMMARY:Active Meeting
ATTENDEE;PARTSTAT=ACCEPTED;CN=Huy:mailto:huy.leminh@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:cancelled@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
STATUS:CANCELLED
SUMMARY:Cancelled Meeting
END:VEVENT
BEGIN:VEVENT
UID:declined@google.com
DTSTART:20261006T060000Z
DTEND:20261006T070000Z
SUMMARY:Declined Meeting
ATTENDEE;PARTSTAT=DECLINED;CN=Huy:mailto:huy.leminh@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:none@google.com
DTSTART:20261006T080000Z
DTEND:20261006T090000Z
SUMMARY:Personal Block
END:VEVENT
BEGIN:VEVENT
UID:allday@google.com
DTSTART;VALUE=DATE:20261006
DTEND;VALUE=DATE:20261007
SUMMARY:All Day
END:VEVENT
END:VCALENDAR`

func calendarConfig() evidence.SourceConfig {
	return evidence.SourceConfig{
		ID: "cal", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{
			CalendarConfigURLRef:    "keychain:calendar-feed",
			CalendarConfigUserEmail: "huy.leminh@gradion.com",
		},
	}
}

func staticFetcher(data string) CalendarFetcher {
	return func(context.Context, evidence.SourceConfig, evidence.CollectRequest) ([]byte, error) {
		return []byte(data), nil
	}
}

// TestCalendarPlugin_RetainsCancelledDeclinedUnknown proves cancelled events and
// declined/unknown invitations are retained as evidence with their status.
func TestCalendarPlugin_RetainsCancelledDeclinedUnknown(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(&CalendarPlugin{Fetch: staticFetcher(calendarTestICS)})

	ev, summary, err := reg.CollectDay(context.Background(),
		[]evidence.SourceConfig{calendarConfig()},
		evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("expected complete collection, got %+v", summary)
	}
	if len(ev) != 5 {
		t.Fatalf("expected all 5 events retained, got %d", len(ev))
	}

	var cancelled, declined, allDayUnknown, timedKnown bool
	for _, e := range ev {
		if e.Status == evidence.EventStatusCancelled {
			cancelled = true
		}
		if e.Invitation == evidence.InvitationDeclined {
			declined = true
		}
		if e.AllDay && e.Duration.IsUnknown() {
			allDayUnknown = true
		}
		if !e.AllDay && e.Duration.Known && e.Duration.Minutes == 60 {
			timedKnown = true
		}
		// Every calendar SourceRef carries the non-secret source id, never a URL.
		for _, ref := range e.SourceRefs {
			if ref.Kind != evidence.SourceCalendar || ref.FeedID != "cal" {
				t.Errorf("unexpected calendar SourceRef: %+v", ref)
			}
		}
	}
	if !cancelled {
		t.Error("cancelled event was not retained")
	}
	if !declined {
		t.Error("declined invitation was not retained")
	}
	if !allDayUnknown {
		t.Error("all-day event should carry an unknown duration")
	}
	if !timedKnown {
		t.Error("timed 60-minute event should carry a known 60-minute duration")
	}
}

// TestCalendarPlugin_DefaultProposalFilter proves the proposal filter excludes
// cancelled/declined/unknown but keeps accepted.
func TestCalendarPlugin_DefaultProposalFilter(t *testing.T) {
	cases := []struct {
		name string
		e    evidence.Evidence
		keep bool
	}{
		{"cancelled", evidence.Evidence{Status: evidence.EventStatusCancelled}, false},
		{"declined", evidence.Evidence{Invitation: evidence.InvitationDeclined}, false},
		{"unknown invitation", evidence.Evidence{Invitation: evidence.InvitationUnknown}, false},
		{"accepted", evidence.Evidence{Invitation: evidence.InvitationAccepted}, true},
		{"none", evidence.Evidence{Invitation: evidence.InvitationNone}, true},
	}
	for _, tc := range cases {
		if got := DefaultProposalFilter(tc.e); got != tc.keep {
			t.Errorf("%s: DefaultProposalFilter = %v, want %v", tc.name, got, tc.keep)
		}
	}
}

// TestCalendarPlugin_ContractDeterminism proves the default fetcher's contract
// mode uses a fixed fixture and yields a complete determinism check.
func TestCalendarPlugin_ContractDeterminism(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "cal.ics")
	if err := os.WriteFile(fixture, []byte(calendarTestICS), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := evidence.NewRegistry()
	reg.Register(NewCalendarPlugin())
	cfg := calendarConfig()
	cfg.Config[CalendarConfigFixture] = fixture
	cfg.Config[CalendarConfigFixtureDate] = "2026-10-06"

	results, err := reg.VerifyContracts(context.Background(), []evidence.SourceConfig{cfg})
	if err != nil {
		t.Fatalf("VerifyContracts: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusComplete {
		t.Fatalf("expected one complete contract result, got %+v", results)
	}
}

// TestCalendarPlugin_RejectsUnsafeSourceID proves the contract check refuses a
// source id that looks like a URL or filesystem path, so identity can never be a
// secret feed URL or a path.
func TestCalendarPlugin_RejectsUnsafeSourceID(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(&CalendarPlugin{Fetch: staticFetcher(calendarTestICS)})

	for _, id := range []string{"https://example.com/feed.ics", "/Users/x/feed.ics", "~/feed.ics"} {
		cfg := calendarConfig()
		cfg.ID = id
		_, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
		if err != nil {
			t.Fatalf("CollectDay: %v", err)
		}
		if summary.Sources[0].ErrorCode != "CALENDAR_SOURCE_ID_UNSAFE" {
			t.Errorf("id %q: expected CALENDAR_SOURCE_ID_UNSAFE, got %+v", id, summary.Sources[0])
		}
	}
}

// TestCalendarPlugin_SecretFree proves a sentinel secret in config never appears
// in serialized evidence or results.
func TestCalendarPlugin_SecretFree(t *testing.T) {
	const sentinel = "CAL-SENTINEL-3f9a"
	reg := evidence.NewRegistry()
	reg.Register(&CalendarPlugin{Fetch: staticFetcher(calendarTestICS)})

	cfg := calendarConfig()
	cfg.Config[CalendarConfigURLRef] = "keychain:" + sentinel

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{cfg}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	artifact, _ := json.Marshal(struct {
		Evidence []evidence.Evidence        `json:"evidence"`
		Summary  evidence.CollectionSummary `json:"summary"`
	}{Evidence: ev, Summary: summary})
	if strings.Contains(string(artifact), sentinel) {
		t.Errorf("secret leaked into serialized output: %s", artifact)
	}
}
