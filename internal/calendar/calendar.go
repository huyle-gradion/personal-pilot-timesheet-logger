package calendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

// Named finite bounds with explicit units (TigerStyle).
const (
	MaxResponseSizeBytes          int64         = 10 * 1024 * 1024  // 10 MiB maximum .ics download (per M0-01)
	MaxResponseHeaderBytes        int64         = 64 * 1024         // 64 KiB maximum HTTP response headers
	HTTPRequestTimeoutSeconds     time.Duration = 5 * time.Second   // 5s deadline per HTTP attempt (per M0-01)
	CacheTTLSeconds               time.Duration = 300 * time.Second // 5 minutes cache lifetime
	MaxParsedEventsCount          int           = 1000              // Maximum calendar events admitted per day
	MaxRecurrenceOccurrencesCount int           = 200               // Maximum expanded recurring occurrences
	MaxSummaryLengthBytes         int           = 512               // Maximum event summary length
	MaxDescriptionLengthBytes     int           = 4096              // Maximum event description length
	MaxPRMatchesCount             int           = 50                // Maximum PRs matched per event
	MaxEmailLengthBytes           int           = 256               // Maximum user email length
)

// Fixed assertion invariant identifiers (TigerStyle).
// Exclusively used for impossible internal states, never for external input errors.
const (
	AssertLocationNil      = "ASSERT_CALENDAR_001_NIL_TIMEZONE_LOCATION"
	AssertDateZero         = "ASSERT_CALENDAR_002_ZERO_TARGET_DATE"
	AssertDurationNegative = "ASSERT_CALENDAR_003_NEGATIVE_DURATION"
)

// Assert checks an internal programmer-contract invariant and panics with a fixed identifier if violated.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// ErrorKind classifies contract-allowed operating errors.
type ErrorKind int

const (
	ErrKindValidation ErrorKind = iota
	ErrKindNetwork
	ErrKindHTTPProtocol
	ErrKindCapacity
	ErrKindParse
	ErrKindCache
)

// OperatingError represents a typed, bounded operating error with a stable identifier and redacted message.
type OperatingError struct {
	Kind    ErrorKind
	Code    string
	Message string
}

func (e *OperatingError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// RedactURL removes sensitive path tokens and queries from a URL for safe error reporting and logging.
func RedactURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "[malformed-url]"
	}
	if parsed.Scheme == "" && parsed.Host == "" {
		// Local file path
		return filepath.Base(rawURL)
	}
	// Return only scheme + host + redacted path
	return fmt.Sprintf("%s://%s/.../redacted.ics", parsed.Scheme, parsed.Host)
}

// SimpleEntry represents a standardized timesheet entry conforming to ADR 0001 (Section 3) and M0-01.
type SimpleEntry struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	Timestamp   string         `json:"timestamp"`
	EndTime     string         `json:"endTime"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Metadata    *EntryMetadata `json:"metadata,omitempty"`
}

type EntryMetadata struct {
	Status          string  `json:"status,omitempty"`
	Location        *string `json:"location,omitempty"`
	DurationMinutes int     `json:"duration_minutes,omitempty"`
	AllDay          bool    `json:"all_day,omitempty"`
	Transparent     bool    `json:"transparent,omitempty"`
}

// ParseOptions controls filtering and parsing parameters for calendar feeds.
type ParseOptions struct {
	TargetDate      time.Time
	Location        *time.Location
	UserEmail       string
	SkipTransparent bool
}

// ExtractPRs finds up to MaxPRMatchesCount distinct '#<num>' occurrences in text.
func ExtractPRs(text string) []string {
	if text == "" {
		return []string{}
	}
	re := regexp.MustCompile(`#(\d+)`)
	matches := re.FindAllStringSubmatch(text, MaxPRMatchesCount+1)

	seen := make(map[int]bool)
	var nums []int
	for _, m := range matches {
		if len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil && !seen[n] {
				seen[n] = true
				nums = append(nums, n)
				if len(nums) >= MaxPRMatchesCount {
					break
				}
			}
		}
	}
	sort.Ints(nums)

	result := make([]string, len(nums))
	for i, n := range nums {
		result[i] = fmt.Sprintf("#%d", n)
	}
	return result
}

// TruncateString bounds a string to a maximum byte length without splitting UTF-8 characters.
func TruncateString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s[:maxBytes])
	// Find last valid rune boundary
	for len(b) > 0 && (b[len(b)-1]&0xc0) == 0x80 {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && (b[len(b)-1]&0x80) != 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// readCachedFeed safely reads the cached .ics file without traversing symlinks.
func readCachedFeed(cachePath string) ([]byte, bool) {
	if cachePath == "" {
		return nil, false
	}
	f, err := os.OpenFile(cachePath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) >= CacheTTLSeconds {
		return nil, false
	}
	if info.Size() > MaxResponseSizeBytes {
		return nil, false
	}

	limited := io.LimitReader(f, MaxResponseSizeBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil || int64(len(data)) > MaxResponseSizeBytes {
		return nil, false
	}
	return data, true
}

// FetchICS retrieves calendar bytes from an HTTP URL or local file, enforcing bounded sizes and timeouts.
func FetchICS(ctx context.Context, source string, cachePath string, noCache bool) ([]byte, error) {
	if source == "" {
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_EMPTY_SOURCE",
			Message: "no iCal source provided",
		}
	}

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		// Check local cache first
		if !noCache {
			if data, ok := readCachedFeed(cachePath); ok {
				return data, nil
			}
		}

		// Enforce single-deadline context
		reqCtx, cancel := context.WithTimeout(ctx, HTTPRequestTimeoutSeconds)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, source, nil)
		if err != nil {
			return nil, &OperatingError{
				Kind:    ErrKindValidation,
				Code:    "CAL_ERR_INVALID_REQUEST",
				Message: fmt.Sprintf("failed to construct HTTP request for %s", RedactURL(source)),
			}
		}
		req.Header.Set("User-Agent", "Personal-Pilot-Timesheet/1.0")

		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// TigerStyle Go Rule: Disable redirects. A redirect is a terminal protocol result.
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				MaxResponseHeaderBytes: MaxResponseHeaderBytes,
				DisableKeepAlives:      true,
				ResponseHeaderTimeout:  HTTPRequestTimeoutSeconds,
				TLSHandshakeTimeout:   HTTPRequestTimeoutSeconds,
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, &OperatingError{
				Kind:    ErrKindNetwork,
				Code:    "CAL_ERR_NETWORK_FAILURE",
				Message: fmt.Sprintf("HTTP request failed for %s", RedactURL(source)),
			}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, &OperatingError{
				Kind:    ErrKindHTTPProtocol,
				Code:    "CAL_ERR_HTTP_STATUS",
				Message: fmt.Sprintf("unexpected HTTP status %d from %s", resp.StatusCode, RedactURL(source)),
			}
		}

		// Enforce response body bound (read up to MaxResponseSizeBytes + 1)
		limitedReader := io.LimitReader(resp.Body, MaxResponseSizeBytes+1)
		body, err := io.ReadAll(limitedReader)
		if err != nil {
			return nil, &OperatingError{
				Kind:    ErrKindNetwork,
				Code:    "CAL_ERR_READ_BODY",
				Message: "failed to read HTTP response body",
			}
		}

		if int64(len(body)) > MaxResponseSizeBytes {
			return nil, &OperatingError{
				Kind:    ErrKindCapacity,
				Code:    "CAL_ERR_RESPONSE_TOO_LARGE",
				Message: fmt.Sprintf("response body exceeded limit of %d bytes", MaxResponseSizeBytes),
			}
		}

		// Persist cache with atomic write, fsync, and restricted permissions (0600 file, 0700 dir)
		if cachePath != "" {
			if err := writeCacheFile(cachePath, body); err != nil {
				return nil, &OperatingError{
					Kind:    ErrKindCache,
					Code:    "CAL_ERR_CACHE_WRITE",
					Message: fmt.Sprintf("failed to write cache file: %v", err),
				}
			}
		}

		return body, nil
	}

	// Local file source
	info, err := os.Lstat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &OperatingError{
				Kind:    ErrKindValidation,
				Code:    "CAL_ERR_FILE_NOT_FOUND",
				Message: fmt.Sprintf("file not found: %s", filepath.Base(source)),
			}
		}
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_FILE_STAT",
			Message: "failed to access calendar file",
		}
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_SYMLINK_REJECTED",
			Message: "symlinks are not permitted for calendar files",
		}
	}

	if !info.Mode().IsRegular() {
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_NOT_REGULAR_FILE",
			Message: "source is not a regular file",
		}
	}

	if info.Size() > MaxResponseSizeBytes {
		return nil, &OperatingError{
			Kind:    ErrKindCapacity,
			Code:    "CAL_ERR_FILE_TOO_LARGE",
			Message: fmt.Sprintf("file size %d exceeds limit of %d bytes", info.Size(), MaxResponseSizeBytes),
		}
	}

	f, err := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_FILE_OPEN",
			Message: "failed to open calendar file",
		}
	}
	defer f.Close()

	limitedReader := io.LimitReader(f, MaxResponseSizeBytes+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, &OperatingError{
			Kind:    ErrKindValidation,
			Code:    "CAL_ERR_FILE_READ",
			Message: "failed to read calendar file",
		}
	}
	if int64(len(data)) > MaxResponseSizeBytes {
		return nil, &OperatingError{
			Kind:    ErrKindCapacity,
			Code:    "CAL_ERR_FILE_TOO_LARGE",
			Message: fmt.Sprintf("file size exceeds limit of %d bytes", MaxResponseSizeBytes),
		}
	}
	return data, nil
}

// writeCacheFile durably writes data to path using a temp file, fsync, and atomic rename.
func writeCacheFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmpFile, err := os.CreateTemp(dir, "cal_cache_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if err := tmpFile.Chmod(0600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// NormalizeEmail cleans an email address by stripping mailto:, brackets, whitespace, and lowercasing.
func NormalizeEmail(email string) string {
	s := strings.TrimSpace(strings.ToLower(email))
	s = strings.TrimPrefix(s, "mailto:")
	s = strings.Trim(s, "<>")
	return strings.TrimSpace(s)
}

// ExtractEmailFromICSURL retrieves the calendar owner email from Google Calendar private URL path.
func ExtractEmailFromICSURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	for i, part := range parts {
		if strings.EqualFold(part, "ical") && i+1 < len(parts) {
			candidate, err := url.PathUnescape(parts[i+1])
			if err == nil && strings.Contains(candidate, "@") {
				return NormalizeEmail(candidate)
			}
		}
	}
	return ""
}

// extractCalendarOwnerEmail checks X-WR-CALNAME property for an email.
func extractCalendarOwnerEmail(cal *ics.Calendar) string {
	if cal == nil {
		return ""
	}
	for _, prop := range cal.CalendarProperties {
		if strings.EqualFold(prop.IANAToken, "X-WR-CALNAME") {
			val := strings.TrimSpace(prop.Value)
			if strings.Contains(val, "@") {
				return NormalizeEmail(val)
			}
		}
	}
	return ""
}

// IsEventDeclinedByUser returns true if the attendee corresponding to userEmail has PARTSTAT=DECLINED.
func IsEventDeclinedByUser(event *ics.VEvent, userEmail string) bool {
	attendees := event.Attendees()
	if len(attendees) == 0 {
		return false
	}

	normUser := NormalizeEmail(userEmail)

	for _, att := range attendees {
		isDeclined := strings.EqualFold(string(att.ParticipationStatus()), string(ics.ParticipationStatusDeclined))
		if !isDeclined {
			if partstat, ok := att.ICalParameters["PARTSTAT"]; ok && len(partstat) > 0 {
				if strings.EqualFold(partstat[0], "DECLINED") {
					isDeclined = true
				}
			}
		}

		if !isDeclined {
			continue
		}

		attEmail := NormalizeEmail(att.Email())
		if attEmail == "" {
			attEmail = NormalizeEmail(att.Value)
		}

		if normUser != "" {
			if attEmail == normUser {
				return true
			}
		} else if len(attendees) == 1 {
			return true
		}
	}

	return false
}

type exDateItem struct {
	time       time.Time
	isDateOnly bool
}

// getEventExDates extracts all EXDATE timestamps and flags date-only exclusions with timezone awareness.
func getEventExDates(event *ics.VEvent) []exDateItem {
	var items []exDateItem
	for _, prop := range event.GetProperties(ics.ComponentPropertyExdate) {
		isDateOnly := false
		if vals, ok := prop.ICalParameters["VALUE"]; ok {
			for _, val := range vals {
				if strings.EqualFold(val, "DATE") {
					isDateOnly = true
					break
				}
			}
		}
		var propLoc *time.Location
		if tzidVals, ok := prop.ICalParameters["TZID"]; ok && len(tzidVals) > 0 {
			if l, err := time.LoadLocation(tzidVals[0]); err == nil {
				propLoc = l
			}
		}
		for _, v := range strings.Split(prop.Value, ",") {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			itemDateOnly := isDateOnly
			if len(v) == 8 && !strings.Contains(v, "T") {
				itemDateOnly = true
			}
			for _, layout := range []string{
				"20060102T150405Z",
				"20060102T150405",
				"20060102",
				time.RFC3339,
			} {
				var t time.Time
				var parseErr error
				if propLoc != nil && !strings.HasSuffix(v, "Z") {
					t, parseErr = time.ParseInLocation(layout, v, propLoc)
				} else {
					t, parseErr = time.Parse(layout, v)
				}
				if parseErr == nil {
					items = append(items, exDateItem{
						time:       t.UTC(),
						isDateOnly: itemDateOnly,
					})
					break
				}
			}
		}
	}
	return items
}

// getEventUID returns the unique ID of an event.
func getEventUID(event *ics.VEvent) string {
	if prop := event.GetProperty(ics.ComponentPropertyUniqueId); prop != nil {
		if val := strings.TrimSpace(prop.Value); val != "" {
			return val
		}
	}
	return strings.TrimSpace(event.Id())
}

// collectRecurrenceExceptions finds any VEVENT with a RECURRENCE-ID to exclude that instance from the master RRULE.
func collectRecurrenceExceptions(cal *ics.Calendar) map[string][]exDateItem {
	exceptions := make(map[string][]exDateItem)
	for _, event := range cal.Events() {
		prop := event.GetProperty(ics.ComponentPropertyRecurrenceId)
		if prop != nil {
			uid := getEventUID(event)
			if uid == "" {
				continue
			}
			isDateOnly := false
			if vals, ok := prop.ICalParameters["VALUE"]; ok {
				for _, val := range vals {
					if strings.EqualFold(val, "DATE") {
						isDateOnly = true
						break
					}
				}
			}
			var propLoc *time.Location
			if tzidVals, ok := prop.ICalParameters["TZID"]; ok && len(tzidVals) > 0 {
				if l, err := time.LoadLocation(tzidVals[0]); err == nil {
					propLoc = l
				}
			}
			recID, err := event.GetRecurrenceID()
			if err == nil {
				exceptions[uid] = append(exceptions[uid], exDateItem{
					time:       recID.UTC(),
					isDateOnly: isDateOnly,
				})
			} else {
				v := strings.TrimSpace(prop.Value)
				itemDateOnly := isDateOnly
				if len(v) == 8 && !strings.Contains(v, "T") {
					itemDateOnly = true
				}
				for _, layout := range []string{
					"20060102T150405Z",
					"20060102T150405",
					"20060102",
					time.RFC3339,
				} {
					var t time.Time
					var parseErr error
					if propLoc != nil && !strings.HasSuffix(v, "Z") {
						t, parseErr = time.ParseInLocation(layout, v, propLoc)
					} else {
						t, parseErr = time.Parse(layout, v)
					}
					if parseErr == nil {
						exceptions[uid] = append(exceptions[uid], exDateItem{
							time:       t.UTC(),
							isDateOnly: itemDateOnly,
						})
						break
					}
				}
			}
		}
	}
	return exceptions
}

// isOccurrenceExcluded checks if an occurrence matches any EXDATE or RECURRENCE-ID exception.
func isOccurrenceExcluded(occUTC time.Time, isAllDay bool, exDates []exDateItem, loc *time.Location) bool {
	for _, ex := range exDates {
		if ex.isDateOnly || isAllDay {
			if loc != nil {
				occLoc := occUTC.In(loc)
				exLoc := ex.time.In(loc)
				if exLoc.Year() == occLoc.Year() &&
					exLoc.Month() == occLoc.Month() &&
					exLoc.Day() == occLoc.Day() {
					return true
				}
			}
			if ex.time.UTC().Year() == occUTC.UTC().Year() &&
				ex.time.UTC().Month() == occUTC.UTC().Month() &&
				ex.time.UTC().Day() == occUTC.UTC().Day() {
				return true
			}
		} else if ex.time.UTC().Truncate(time.Minute).Equal(occUTC.UTC().Truncate(time.Minute)) {
			return true
		}
	}
	return false
}

// ParseCalendarEvents parses iCal data and maps events into SimpleEntry records for targetDate.
// Preserved for backward compatibility, delegating to ParseCalendarEventsWithOptions.
func ParseCalendarEvents(icsData []byte, targetDate time.Time, loc *time.Location) ([]SimpleEntry, error) {
	return ParseCalendarEventsWithOptions(icsData, ParseOptions{
		TargetDate: targetDate,
		Location:   loc,
	})
}

// filterEvent determines whether an event should be processed or skipped.
// Returns keep=false if cancelled, declined by user, or transparent (when skipTransparent=true).
func filterEvent(event *ics.VEvent, userEmail string, skipTransparent bool) (bool, bool, string) {
	status := "CONFIRMED"
	if statusProp := event.GetProperty(ics.ComponentPropertyStatus); statusProp != nil {
		s := strings.ToUpper(strings.TrimSpace(statusProp.Value))
		if s == "CANCELLED" {
			return false, false, ""
		}
		if s != "" {
			status = s
		}
	}

	if IsEventDeclinedByUser(event, userEmail) {
		return false, false, ""
	}

	isTransparent := false
	if transpProp := event.GetProperty(ics.ComponentPropertyTransp); transpProp != nil {
		if strings.EqualFold(strings.TrimSpace(transpProp.Value), "TRANSPARENT") {
			isTransparent = true
		}
	}

	if skipTransparent && isTransparent {
		return false, isTransparent, status
	}

	return true, isTransparent, status
}

// eventTimeWindow represents an occurrence interval in local time.
type eventTimeWindow struct {
	Start       time.Time
	End         time.Time
	IsAllDay    bool
	IsRecurring bool
}

// expandOccurrences computes the start and end time windows for an event within the target day.
func expandOccurrences(
	event *ics.VEvent,
	targetDate time.Time,
	startOfDay, endOfDay time.Time,
	loc *time.Location,
	recurrenceExceptions map[string][]exDateItem,
) ([]eventTimeWindow, error) {
	dtstartProp := event.GetProperty(ics.ComponentPropertyDtStart)
	if dtstartProp == nil {
		return nil, nil
	}

	startAt, err := event.GetStartAt()
	if err != nil {
		return nil, nil
	}

	// Detect all-day event (case-insensitive VALUE=DATE or 8-digit date)
	isAllDay := false
	if valProp, ok := dtstartProp.ICalParameters["VALUE"]; ok && len(valProp) > 0 {
		if strings.EqualFold(valProp[0], "DATE") {
			isAllDay = true
		}
	} else if len(dtstartProp.Value) == 8 && !strings.Contains(dtstartProp.Value, "T") {
		isAllDay = true
	}

	endAt, err := event.GetEndAt()
	if err != nil || endAt.Before(startAt) {
		if isAllDay {
			endAt = startAt.AddDate(0, 0, 1)
		} else {
			endAt = startAt.Add(time.Hour)
		}
	}

	duration := endAt.Sub(startAt)
	Assert(duration >= 0, AssertDurationNegative)

	rruleProp := event.GetProperty(ics.ComponentPropertyRrule)
	var windows []eventTimeWindow

	if rruleProp != nil && rruleProp.Value != "" {
		ruleStr := strings.TrimPrefix(rruleProp.Value, "RRULE:")
		rule, err := rrule.StrToRRule(ruleStr)
		if err == nil {
			rule.DTStart(startAt.UTC())
			// Bounded recurrence query around target day
			utcOccurrences := rule.Between(startOfDay.UTC().Add(-24*time.Hour), endOfDay.UTC().Add(24*time.Hour), true)
			if len(utcOccurrences) > MaxRecurrenceOccurrencesCount {
				utcOccurrences = utcOccurrences[:MaxRecurrenceOccurrencesCount]
			}

			exDates := getEventExDates(event)
			if rawUID := getEventUID(event); rawUID != "" {
				if recItems, ok := recurrenceExceptions[rawUID]; ok {
					exDates = append(exDates, recItems...)
				}
			}

			for _, occ := range utcOccurrences {
				if isOccurrenceExcluded(occ, isAllDay, exDates, loc) {
					continue
				}
				occStart := occ.In(loc)
				occEnd := occStart.Add(duration)
				if occStart.Before(endOfDay) && occEnd.After(startOfDay) {
					windows = append(windows, eventTimeWindow{
						Start:       occStart,
						End:         occEnd,
						IsAllDay:    isAllDay,
						IsRecurring: true,
					})
				}
			}
			return windows, nil
		}
	}

	eventStartLocal := startAt.In(loc)
	eventEndLocal := endAt.In(loc)
	if eventStartLocal.Before(endOfDay) && eventEndLocal.After(startOfDay) {
		windows = append(windows, eventTimeWindow{
			Start:       eventStartLocal,
			End:         eventEndLocal,
			IsAllDay:    isAllDay,
			IsRecurring: false,
		})
	}

	return windows, nil
}

// buildSimpleEntry constructs a canonical SimpleEntry record from an event occurrence.
func buildSimpleEntry(
	event *ics.VEvent,
	win eventTimeWindow,
	isTransparent bool,
	status string,
	targetDate time.Time,
	loc *time.Location,
) SimpleEntry {
	rawUID := getEventUID(event)
	var entryID string
	if rawUID != "" {
		if win.IsRecurring {
			entryID = fmt.Sprintf("gcal-%s-%s", rawUID, win.Start.Format("20060102T150405"))
		} else {
			entryID = fmt.Sprintf("gcal-%s", rawUID)
		}
	}

	summary := "(No Title)"
	if summaryProp := event.GetProperty(ics.ComponentPropertySummary); summaryProp != nil {
		if s := strings.TrimSpace(summaryProp.Value); s != "" {
			summary = s
		}
	}
	summary = TruncateString(summary, MaxSummaryLengthBytes)

	desc := ""
	if descProp := event.GetProperty(ics.ComponentPropertyDescription); descProp != nil {
		desc = strings.TrimSpace(descProp.Value)
		desc = strings.ReplaceAll(desc, `\n`, " ")
		desc = strings.ReplaceAll(desc, "\n", " ")
	}
	desc = TruncateString(desc, MaxDescriptionLengthBytes)

	var locPtr *string
	if locProp := event.GetProperty(ics.ComponentPropertyLocation); locProp != nil {
		if v := strings.TrimSpace(locProp.Value); v != "" {
			s := TruncateString(v, 256)
			locPtr = &s
		}
	}

	prs := ExtractPRs(fmt.Sprintf("%s %s", summary, desc))
	descBody := desc
	if descBody == "" {
		descBody = summary
	}
	if len(prs) > 0 && !strings.Contains(descBody, "PRs:") {
		descBody = fmt.Sprintf("%s. PRs: %s", descBody, strings.Join(prs, ", "))
	}

	var timestampStr, endTimeStr string
	durMins := int(win.End.Sub(win.Start).Minutes())
	if win.IsAllDay {
		allDayStart := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, loc)
		allDayEnd := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 23, 59, 59, 0, loc)
		timestampStr = allDayStart.Format(time.RFC3339)
		endTimeStr = allDayEnd.Format(time.RFC3339)
		durMins = 24 * 60
	} else {
		timestampStr = win.Start.Format(time.RFC3339)
		endTimeStr = win.End.Format(time.RFC3339)
	}

	return SimpleEntry{
		ID:          entryID,
		Source:      "calendar",
		Timestamp:   timestampStr,
		EndTime:     endTimeStr,
		Title:       summary,
		Description: descBody,
		Metadata: &EntryMetadata{
			Status:          status,
			Location:        locPtr,
			DurationMinutes: durMins,
			AllDay:          win.IsAllDay,
			Transparent:     isTransparent,
		},
	}
}

// ParseCalendarEventsWithOptions parses iCal data with configurable filtering options.
func ParseCalendarEventsWithOptions(icsData []byte, opts ParseOptions) ([]SimpleEntry, error) {
	// Assert invariants (programmer contract)
	Assert(opts.Location != nil, AssertLocationNil)
	Assert(!opts.TargetDate.IsZero(), AssertDateZero)

	cal, err := ics.ParseCalendar(bytes.NewReader(icsData))
	if err != nil {
		return nil, &OperatingError{
			Kind:    ErrKindParse,
			Code:    "CAL_ERR_PARSE_ICS",
			Message: "failed to parse calendar iCal data",
		}
	}

	targetDate := opts.TargetDate
	loc := opts.Location

	userEmail := strings.TrimSpace(opts.UserEmail)
	if userEmail == "" {
		userEmail = extractCalendarOwnerEmail(cal)
	}

	startOfDay := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, loc)
	endOfDay := startOfDay.AddDate(0, 0, 1)

	// Collect exception events with RECURRENCE-ID to exclude modified/cancelled occurrences from master RRULE
	recurrenceExceptions := collectRecurrenceExceptions(cal)

	var entries []SimpleEntry

	for _, event := range cal.Events() {
		if len(entries) >= MaxParsedEventsCount {
			break
		}

		keep, isTransparent, status := filterEvent(event, userEmail, opts.SkipTransparent)
		if !keep {
			continue
		}

		windows, err := expandOccurrences(event, targetDate, startOfDay, endOfDay, loc, recurrenceExceptions)
		if err != nil || len(windows) == 0 {
			continue
		}

		for _, win := range windows {
			if len(entries) >= MaxParsedEventsCount {
				break
			}
			entries = append(entries, buildSimpleEntry(event, win, isTransparent, status, targetDate, loc))
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp < entries[j].Timestamp
	})

	return entries, nil
}

// FormatMarkdown outputs SimpleEntry records matching the Section 3.2 "Edit entry" panel shape.
func FormatMarkdown(entries []SimpleEntry) string {
	if len(entries) == 0 {
		return "(No calendar entries found for this date)"
	}

	var blocks []string
	for idx, e := range entries {
		prs := ExtractPRs(e.Description)
		prStr := "None"
		if len(prs) > 0 {
			prStr = strings.Join(prs, ", ")
		}

		startTimeStr := e.Timestamp
		endTimeStr := e.EndTime
		dateStr := ""
		if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
			dateStr = t.Format("2006-01-02")
			startTimeStr = t.Format("15:04")
		}
		if t, err := time.Parse(time.RFC3339, e.EndTime); err == nil {
			endTimeStr = t.Format("15:04")
		}

		block := fmt.Sprintf(
			"### Entry %d: %s - %s\n"+
				"- **Date**: %s\n"+
				"- **Start time**: %s\n"+
				"- **End time**: %s\n"+
				"- **Description**: %s\n"+
				"- **PRs**: %s\n"+
				"- **Sources**:\n"+
				"  - Calendar: \"%s\"",
			idx+1, startTimeStr, endTimeStr,
			dateStr,
			startTimeStr,
			endTimeStr,
			e.Description,
			prStr,
			e.Title,
		)
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

// FormatJSON outputs SimpleEntry records as a formatted or minified JSON byte buffer without HTML escaping.
func FormatJSON(entries []SimpleEntry, minified bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if !minified {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(entries); err != nil {
		return nil, &OperatingError{
			Kind:    ErrKindParse,
			Code:    "CAL_ERR_JSON_ENCODE",
			Message: "failed to encode JSON output",
		}
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
