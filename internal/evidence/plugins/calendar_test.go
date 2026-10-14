package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// GAP-7 (#70): the calendar plugin still exposes the meeting TITLE as Title while
// its Description keeps the ICS body plus a "Location:" line. The meeting
// worklog description is chosen downstream by the proposal layer, so the
// evidence record must retain the detail.
func TestCalendarPlugin_ExposesTitleAndKeepsBodyOnDescription(t *testing.T) {
	const ics = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:with-desc@test
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
SUMMARY:Design Review
DESCRIPTION:Agenda: finalise the spec
LOCATION:Room 4
END:VEVENT
END:VCALENDAR`

	p := &CalendarPlugin{Fetch: staticFetcher(ics)}
	res, err := p.Collect(context.Background(), calendarConfig(),
		evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 {
		t.Fatalf("expected 1 evidence item, got %d", len(res.Evidence))
	}
	e := res.Evidence[0]
	if e.Title != "Design Review" {
		t.Errorf("Title = %q, want the ICS SUMMARY %q", e.Title, "Design Review")
	}
	if !strings.Contains(e.Description, "Agenda: finalise the spec") {
		t.Errorf("Description = %q, want it to keep the ICS body", e.Description)
	}
	if !strings.Contains(e.Description, "Location: Room 4") {
		t.Errorf("Description = %q, want it to keep the Location: line", e.Description)
	}
}

// PREFLIGHT-RUNTIME-1: runtime validation of a calendar source passes WITHOUT a
// fixture when url_ref is present, and fails when it is missing.
func TestCalendarPlugin_RuntimeValidationNoFixture(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(NewCalendarPlugin())
	ctx := context.Background()

	ok := calendarConfig() // url_ref present (keychain ref), no fixture
	res, err := reg.ValidateSources(ctx, []evidence.SourceConfig{ok})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if res[0].Status != evidence.StatusComplete {
		t.Fatalf("calendar with url_ref must validate complete without a fixture, got %+v", res[0])
	}

	missing := evidence.SourceConfig{ID: "cal", Type: evidence.SourceCalendar, Enabled: true}
	res, err = reg.ValidateSources(ctx, []evidence.SourceConfig{missing})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if res[0].Status != evidence.StatusIncomplete || res[0].ErrorCode != evidence.CodeConfigRequiredMissing {
		t.Fatalf("a missing url_ref must fail %s, got %+v", evidence.CodeConfigRequiredMissing, res[0])
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

// COLLECTOR-TIMEZONE-1: the calendar plugin anchors the day window to
// req.Location (the RUN's configured zone), never UTC and never the host zone.
// An event whose UTC instant falls on the target day only under +07 must be
// included; the old `loc := time.UTC` anchor excluded it.
func TestCalendarPlugin_HonorsRequestLocation(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}

	// 2026-10-05T18:00Z is 2026-10-06T01:00 +07: on the target day only under the
	// +07 zone.
	const ics = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:late-night@test
DTSTART:20261005T180000Z
DTEND:20261005T190000Z
SUMMARY:Late Night
END:VEVENT
END:VCALENDAR`

	p := &CalendarPlugin{Fetch: staticFetcher(ics)}
	res, err := p.Collect(context.Background(), calendarConfig(),
		evidence.CollectRequest{Date: "2026-10-06", Location: hcm})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Title != "Late Night" {
		t.Fatalf("an event on the target day in req.Location must be included, got %+v", res.Evidence)
	}
}

// COLLECTOR-TIMEZONE-1: the local-midnight boundary under Asia/Ho_Chi_Minh.
// Local midnight for 2026-10-06 is 2026-10-05T17:00:00Z. The parser uses
// half-open interval overlap ([start,end) intersecting the local day), so an
// event whose window ends AT local midnight is excluded and one starting AT local
// midnight is included. (A bare DTSTART defaults to a 1h duration and would
// overlap midnight, so the window ends are stated explicitly.)
func TestCalendarPlugin_LocalMidnightBoundary(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}

	const ics = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:before-midnight@test
DTSTART:20261005T165959Z
DTEND:20261005T170000Z
SUMMARY:Before Midnight
END:VEVENT
BEGIN:VEVENT
UID:at-midnight@test
DTSTART:20261005T170000Z
DTEND:20261005T180000Z
SUMMARY:At Midnight
END:VEVENT
END:VCALENDAR`

	p := &CalendarPlugin{Fetch: staticFetcher(ics)}
	res, err := p.Collect(context.Background(), calendarConfig(),
		evidence.CollectRequest{Date: "2026-10-06", Location: hcm})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(res.Evidence) != 1 || res.Evidence[0].Title != "At Midnight" {
		t.Fatalf("exactly the at-local-midnight event must be included, got %+v", res.Evidence)
	}
}

// COLLECTOR-TIMEZONE-1: the run's configured timezone governs. A per-source
// calendar `timezone` must MATCH req.Location or be refused; a per-source key
// alone (nil req.Location, as in contract mode) must still work.
func TestCalendarPlugin_PerSourceTimezoneConflict(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatalf("load Asia/Ho_Chi_Minh: %v", err)
	}
	p := &CalendarPlugin{Fetch: staticFetcher(calendarTestICS)}

	// Conflict: req.Location is +07 but the per-source key says UTC -> refused.
	conflict := calendarConfig()
	conflict.Config[CalendarConfigTimezone] = "UTC"
	if _, err := p.Collect(context.Background(), conflict,
		evidence.CollectRequest{Date: "2026-10-06", Location: hcm}); err == nil {
		t.Fatal("a per-source timezone differing from req.Location must be refused")
	}

	// Match by name: allowed.
	match := calendarConfig()
	match.Config[CalendarConfigTimezone] = "Asia/Ho_Chi_Minh"
	if _, err := p.Collect(context.Background(), match,
		evidence.CollectRequest{Date: "2026-10-06", Location: hcm}); err != nil {
		t.Fatalf("a matching per-source timezone must be allowed, got %v", err)
	}

	// Contract-mode compatibility: a per-source timezone with a nil req.Location
	// (Registry.contractSource builds CollectRequest{Mode: ModeContract}) works.
	contract := calendarConfig()
	contract.Config[CalendarConfigTimezone] = "Asia/Ho_Chi_Minh"
	if _, err := p.Collect(context.Background(), contract,
		evidence.CollectRequest{Date: "2026-10-06"}); err != nil {
		t.Fatalf("a per-source timezone with nil req.Location must still work, got %v", err)
	}
}
