package calendar

import (
	"strings"
	"testing"
	"time"

	ics "github.com/arran4/golang-ical"
)

const detailedICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
X-WR-CALNAME:huy.leminh@gradion.com
BEGIN:VEVENT
UID:active@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
STATUS:CONFIRMED
SUMMARY:Active Meeting
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=ACCEPTED;CN=Huy:mailto:huy.leminh@gradion.com
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
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=DECLINED;CN=Huy:mailto:huy.leminh@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:no-partstat@google.com
DTSTART:20261006T080000Z
DTEND:20261006T090000Z
SUMMARY:Unreadable Invitation
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;CN=Huy:mailto:huy.leminh@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:no-attendees@google.com
DTSTART:20261006T100000Z
DTEND:20261006T110000Z
SUMMARY:Personal Block
END:VEVENT
END:VCALENDAR`

func parseTestEvents(t *testing.T, icsData string) []*ics.VEvent {
	t.Helper()
	cal, err := ics.ParseCalendar(strings.NewReader(icsData))
	if err != nil {
		t.Fatalf("parse calendar: %v", err)
	}
	return cal.Events()
}

// TestParseCalendarEventsDetailed_RetainsAllStatuses proves the detailed parser
// does NOT drop cancelled events or declined/unknown invitations, and records
// their status/invitation.
func TestParseCalendarEventsDetailed_RetainsAllStatuses(t *testing.T) {
	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)

	events, err := ParseCalendarEventsDetailed([]byte(detailedICS), ParseOptions{
		TargetDate: targetDate,
		Location:   loc,
		UserEmail:  "huy.leminh@gradion.com",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected all 5 events retained, got %d", len(events))
	}

	byUID := map[string]CalendarEvent{}
	for _, e := range events {
		byUID[e.UID] = e
	}

	if got := byUID["cancelled@google.com"]; got.Status != "cancelled" {
		t.Errorf("cancelled event status = %q, want cancelled", got.Status)
	}
	if got := byUID["declined@google.com"].Invitation; got != InvitationDeclined {
		t.Errorf("declined invitation = %q, want declined", got)
	}
	if got := byUID["no-partstat@google.com"].Invitation; got != InvitationUnknown {
		t.Errorf("attendee with no PARTSTAT = %q, want unknown", got)
	}
	if got := byUID["no-attendees@google.com"].Invitation; got != InvitationNone {
		t.Errorf("no attendee record = %q, want none", got)
	}
	if got := byUID["active@google.com"].Invitation; got != InvitationAccepted {
		t.Errorf("accepted invitation = %q, want accepted", got)
	}
}

// TestClassifyInvitation_NeverDefaultsToAccepted proves the tri-state classifier
// distinguishes none from unknown and never defaults to accepted.
func TestClassifyInvitation_NeverDefaultsToAccepted(t *testing.T) {
	cases := []struct {
		name     string
		ics      string
		user     string
		expected InvitationStatus
	}{
		{
			name:     "no attendees is none",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `END:VEVENT`,
			expected: InvitationNone,
		},
		{
			name:     "single attendee with no partstat is unknown",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `ATTENDEE;CN=Huy:mailto:huy@gradion.com` + "\n" + `END:VEVENT`,
			expected: InvitationUnknown,
		},
		{
			name:     "single attendee accepted",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `ATTENDEE;PARTSTAT=ACCEPTED;CN=Huy:mailto:huy@gradion.com` + "\n" + `END:VEVENT`,
			expected: InvitationAccepted,
		},
		{
			name:     "user attendee declined among several",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `ATTENDEE;PARTSTAT=ACCEPTED;CN=Other:mailto:other@gradion.com` + "\n" + `ATTENDEE;PARTSTAT=DECLINED;CN=Huy:mailto:huy@gradion.com` + "\n" + `END:VEVENT`,
			user:     "huy@gradion.com",
			expected: InvitationDeclined,
		},
		{
			name:     "several attendees no user email is unknown",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `ATTENDEE;PARTSTAT=ACCEPTED;CN=A:mailto:a@gradion.com` + "\n" + `ATTENDEE;PARTSTAT=ACCEPTED;CN=B:mailto:b@gradion.com` + "\n" + `END:VEVENT`,
			expected: InvitationUnknown,
		},
		{
			name:     "user not among attendees is none",
			ics:      `BEGIN:VEVENT` + "\n" + `UID:x` + "\n" + `DTSTART:20261006T020000Z` + "\n" + `DTEND:20261006T030000Z` + "\n" + `ATTENDEE;PARTSTAT=ACCEPTED;CN=Other:mailto:other@gradion.com` + "\n" + `END:VEVENT`,
			user:     "huy@gradion.com",
			expected: InvitationNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapper := "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//Test//EN\n" + tc.ics + "\nEND:VCALENDAR"
			events := parseTestEvents(t, wrapper)
			if len(events) != 1 {
				t.Fatalf("expected 1 event, got %d", len(events))
			}
			if got := ClassifyInvitation(events[0], tc.user); got != tc.expected {
				t.Errorf("ClassifyInvitation = %q, want %q", got, tc.expected)
			}
		})
	}
}

// TestParseCalendarEventsDetailed_TimedAndAllDay proves all-day detection and UTC
// windows are recorded.
func TestParseCalendarEventsDetailed_TimedAndAllDay(t *testing.T) {
	loc := time.UTC
	targetDate := time.Date(2026, 10, 6, 0, 0, 0, 0, loc)
	data := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:timed@google.com
DTSTART:20261006T020000Z
DTEND:20261006T024500Z
SUMMARY:Timed
END:VEVENT
BEGIN:VEVENT
UID:allday@google.com
DTSTART;VALUE=DATE:20261006
DTEND;VALUE=DATE:20261007
SUMMARY:All Day
END:VEVENT
END:VCALENDAR`

	events, err := ParseCalendarEventsDetailed([]byte(data), ParseOptions{TargetDate: targetDate, Location: loc})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	byUID := map[string]CalendarEvent{}
	for _, e := range events {
		byUID[e.UID] = e
	}
	timed := byUID["timed@google.com"]
	if timed.AllDay {
		t.Error("timed event marked all-day")
	}
	if timed.Start.Location() != time.UTC || timed.End.Location() != time.UTC {
		t.Errorf("timed event windows not UTC: %v/%v", timed.Start.Location(), timed.End.Location())
	}
	if !byUID["allday@google.com"].AllDay {
		t.Error("all-day event not marked all-day")
	}
}
