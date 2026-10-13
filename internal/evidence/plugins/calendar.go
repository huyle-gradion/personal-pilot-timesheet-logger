package plugins

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/calendar"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// Calendar config keys. url_ref is a keychain reference (never a raw secret
// URL); source is an in-memory resolved ICS URL/path supplied by the credential
// layer and never persisted; fixture is the fixed contract-mode input. The
// non-secret identity namespace is the SourceConfig.ID (the configured feed id).
const (
	CalendarConfigURLRef      = "url_ref"
	CalendarConfigSource      = "source"
	CalendarConfigCachePath   = "cache_path"
	CalendarConfigNoCache     = "no_cache"
	CalendarConfigTimezone    = "timezone"
	CalendarConfigUserEmail   = "user_email"
	CalendarConfigFixture     = "fixture"
	CalendarConfigFixtureDate = "fixture_date"
)

// defaultContractDate is the fixed target date used in contract mode when no
// fixture_date is configured, so contract validation is deterministic.
const defaultContractDate = "2026-01-01"

// CalendarFetcher retrieves raw ICS bytes for a configured source. It is
// injected so tests never touch the network. The default wraps
// calendar.FetchICS for live mode and reads a fixed fixture in contract mode.
type CalendarFetcher func(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) ([]byte, error)

// CalendarPlugin collects calendar evidence from an ICS feed.
type CalendarPlugin struct {
	Fetch CalendarFetcher
}

// NewCalendarPlugin returns a calendar plugin using the default fetcher.
func NewCalendarPlugin() *CalendarPlugin {
	return &CalendarPlugin{Fetch: DefaultCalendarFetch}
}

// DefaultCalendarFetch fetches ICS bytes: a fixed fixture in contract mode, else
// the resolved source via calendar.FetchICS.
func DefaultCalendarFetch(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) ([]byte, error) {
	if req.IsContract() {
		path := strings.TrimSpace(cfg.Config[CalendarConfigFixture])
		if path == "" {
			return nil, errors.New("calendar: contract mode requires a fixture path")
		}
		return os.ReadFile(path)
	}
	source := strings.TrimSpace(cfg.Config[CalendarConfigSource])
	if source == "" {
		return nil, errors.New("calendar: no resolved source configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return calendar.FetchICS(ctx, source, strings.TrimSpace(cfg.Config[CalendarConfigCachePath]), isTruthy(cfg.Config[CalendarConfigNoCache]))
}

// Type implements evidence.Plugin.
func (p *CalendarPlugin) Type() evidence.SourceKind { return evidence.SourceCalendar }

// RequiredConfig implements evidence.Plugin.
func (p *CalendarPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{
		{Key: CalendarConfigURLRef, Secret: true, Required: true},
	}
}

// DeclaredEvidenceKinds implements evidence.Plugin.
func (p *CalendarPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceCalendar}
}

// CheckContract implements evidence.Plugin. It verifies the configured source id
// (the identity namespace) is a non-secret identifier, not a URL or filesystem
// path, so identity can never be a secret feed URL or a path.
func (p *CalendarPlugin) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	id := strings.TrimSpace(cfg.ID)
	if id == "" {
		return []evidence.ContractCheck{{
			Name:   "calendar_source_id",
			Passed: false,
			Code:   "CALENDAR_SOURCE_ID_REQUIRED",
			Detail: "source id is required as the non-secret identity namespace",
		}}
	}
	if looksLikeURLOrPath(id) {
		return []evidence.ContractCheck{{
			Name:   "calendar_source_id",
			Passed: false,
			Code:   "CALENDAR_SOURCE_ID_UNSAFE",
			Detail: "source id must be a non-secret identifier, not a URL or filesystem path",
		}}
	}
	return []evidence.ContractCheck{{Name: "calendar_source_id", Passed: true}}
}

// Collect implements evidence.Plugin. It retains cancelled events and
// declined/unknown invitations with their status.
func (p *CalendarPlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	fetch := p.Fetch
	if fetch == nil {
		fetch = DefaultCalendarFetch
	}

	dateStr := strings.TrimSpace(req.Date)
	if dateStr == "" {
		if req.IsContract() {
			dateStr = strings.TrimSpace(cfg.Config[CalendarConfigFixtureDate])
			if dateStr == "" {
				dateStr = defaultContractDate
			}
		} else {
			return evidence.CollectResult{}, errors.New("calendar: target date is required")
		}
	}
	targetDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return evidence.CollectResult{}, errors.New("calendar: invalid target date")
	}

	// The RUN's configured timezone (req.Location) governs the day window. A
	// per-source calendar `timezone` is an override that must MATCH it or be
	// refused: it may never silently re-anchor a source to a different zone. The
	// default is req.Location, else UTC; the host zone (time.Local) is never used.
	loc := time.UTC
	if req.Location != nil {
		loc = req.Location
	}
	if tz := strings.TrimSpace(cfg.Config[CalendarConfigTimezone]); tz != "" {
		loaded, loadErr := time.LoadLocation(tz)
		if loadErr != nil {
			return evidence.CollectResult{}, errors.New("calendar: invalid timezone")
		}
		// Conflict check is gated on req.Location != nil so contract mode (which
		// builds CollectRequest{Mode: ModeContract} with a nil Location) keeps
		// working with a per-source timezone alone. Zones are compared by name
		// (.String()), never by pointer identity.
		if req.Location != nil && loaded.String() != req.Location.String() {
			return evidence.CollectResult{}, errors.New("calendar: per-source timezone conflicts with the configured timezone")
		}
		loc = loaded
	}

	data, err := fetch(ctx, cfg, req)
	if err != nil {
		return evidence.CollectResult{}, errors.New("calendar: failed to fetch ICS")
	}

	events, err := calendar.ParseCalendarEventsDetailed(data, calendar.ParseOptions{
		TargetDate: targetDate,
		Location:   loc,
		UserEmail:  strings.TrimSpace(cfg.Config[CalendarConfigUserEmail]),
	})
	if err != nil {
		return evidence.CollectResult{}, errors.New("calendar: failed to parse ICS")
	}

	feedID := strings.TrimSpace(cfg.ID)
	if feedID == "" {
		return evidence.CollectResult{}, errors.New("calendar: source id is required")
	}
	observed := time.Now().UTC()
	evs := make([]evidence.Evidence, 0, len(events))
	for _, e := range events {
		desc := e.Description
		if e.Location != "" {
			if desc != "" {
				desc += "\n"
			}
			desc += "Location: " + e.Location
		}

		dur := evidence.UnknownDuration()
		if !e.AllDay {
			mins := int(e.End.Sub(e.Start).Minutes())
			if mins < 0 {
				mins = 0
			}
			dur = evidence.KnownDuration(mins)
		}

		evs = append(evs, evidence.Evidence{
			ID:          evidence.CalendarID(feedID, e.UID, e.Start.Format(time.RFC3339)),
			Kind:        evidence.SourceCalendar,
			Title:       e.Summary,
			Description: desc,
			Start:       e.Start,
			End:         e.End,
			AllDay:      e.AllDay,
			Status:      mapCalendarStatus(e.Status),
			Invitation:  mapCalendarInvitation(e.Invitation),
			Duration:    dur,
			SourceRefs: []evidence.SourceRef{{
				Kind:     evidence.SourceCalendar,
				FeedID:   feedID,
				EventUID: e.UID,
			}},
			ObservedAt: observed,
		})
	}

	return evidence.CollectResult{
		Evidence: evs,
		Result:   evidence.SourceResult{Status: evidence.StatusComplete, EvidenceCount: len(evs)},
	}, nil
}

// DefaultProposalFilter reports whether e is eligible as proposal input. It
// delegates to the pure evidence.IsProposalInput predicate so the plugin and the
// proposal layer share one definition. Cancelled events and declined/unknown
// invitations remain in the evidence record; they are filtered only here, at
// proposal time.
func DefaultProposalFilter(e evidence.Evidence) bool {
	return evidence.IsProposalInput(e)
}

func mapCalendarStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "confirmed":
		return evidence.EventStatusConfirmed
	case "cancelled":
		return evidence.EventStatusCancelled
	case "tentative":
		return evidence.EventStatusTentative
	default:
		return evidence.EventStatusUnknown
	}
}

func mapCalendarInvitation(s calendar.InvitationStatus) evidence.InvitationStatus {
	switch s {
	case calendar.InvitationAccepted:
		return evidence.InvitationAccepted
	case calendar.InvitationDeclined:
		return evidence.InvitationDeclined
	case calendar.InvitationTentative:
		return evidence.InvitationTentative
	case calendar.InvitationNeedsAction:
		return evidence.InvitationNeedsAction
	case calendar.InvitationNone:
		return evidence.InvitationNone
	default:
		return evidence.InvitationUnknown
	}
}

// looksLikeURLOrPath reports whether s looks like a URL or filesystem path and
// therefore must never be used as an identity namespace.
func looksLikeURLOrPath(s string) bool {
	switch {
	case strings.Contains(s, "://"):
		return true
	case strings.HasPrefix(s, "/"),
		strings.HasPrefix(s, "./"),
		strings.HasPrefix(s, "../"),
		strings.HasPrefix(s, "~"):
		return true
	case strings.Contains(s, `\`):
		return true
	case len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')):
		return true
	default:
		return false
	}
}

// isTruthy parses a loose boolean config value.
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
