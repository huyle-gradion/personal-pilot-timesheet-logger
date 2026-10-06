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
	MaxResponseSizeBytes      int64         = 5 * 1024 * 1024   // 5 MiB maximum .ics download
	MaxResponseHeaderBytes    int64         = 64 * 1024         // 64 KiB maximum HTTP response headers
	HTTPRequestTimeoutSeconds time.Duration = 15 * time.Second  // 15s deadline per HTTP attempt
	CacheTTLSeconds           time.Duration = 300 * time.Second // 5 minutes cache lifetime
	MaxParsedEventsCount      int           = 1000              // Maximum calendar events admitted per day
	MaxSummaryLengthBytes     int           = 512               // Maximum event summary length
	MaxDescriptionLengthBytes int           = 4096              // Maximum event description length
	MaxPRMatchesCount         int           = 50                // Maximum PRs matched per event
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

// SimpleEntry represents a standardized timesheet entry conforming to Section 3.2.
type SimpleEntry struct {
	Date        string         `json:"date"`
	StartTime   string         `json:"start_time"`
	EndTime     string         `json:"end_time"`
	Topic       string         `json:"topic"`
	Description string         `json:"description"`
	PRs         []string       `json:"prs"`
	Sources     EntrySources   `json:"sources"`
	Metadata    *EntryMetadata `json:"metadata,omitempty"`
}

type EntrySources struct {
	CalendarEvent *string  `json:"calendar_event"`
	Repo          *string  `json:"repo"`
	Commits       []string `json:"commits"`
}

type EntryMetadata struct {
	DurationMinutes int     `json:"duration_minutes"`
	AllDay          bool    `json:"all_day"`
	Location        *string `json:"location,omitempty"`
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
		if !noCache && cachePath != "" {
			info, err := os.Stat(cachePath)
			if err == nil && time.Since(info.ModTime()) < CacheTTLSeconds {
				data, err := os.ReadFile(cachePath)
				if err == nil && int64(len(data)) <= MaxResponseSizeBytes {
					return data, nil
				}
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
			_ = writeCacheFile(cachePath, body)
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

// ParseCalendarEvents parses iCal data and maps events into SimpleEntry records for targetDate.
func ParseCalendarEvents(icsData []byte, targetDate time.Time, loc *time.Location) ([]SimpleEntry, error) {
	// Assert invariants (programmer contract)
	Assert(loc != nil, AssertLocationNil)
	Assert(!targetDate.IsZero(), AssertDateZero)

	cal, err := ics.ParseCalendar(strings.NewReader(string(icsData)))
	if err != nil {
		return nil, &OperatingError{
			Kind:    ErrKindParse,
			Code:    "CAL_ERR_PARSE_ICS",
			Message: "failed to parse calendar iCal data",
		}
	}

	startOfDay := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, loc)
	endOfDay := startOfDay.AddDate(0, 0, 1)

	var entries []SimpleEntry

	for _, event := range cal.Events() {
		if len(entries) >= MaxParsedEventsCount {
			break
		}

		summaryProp := event.GetProperty(ics.ComponentPropertySummary)
		summary := "(No Title)"
		if summaryProp != nil {
			summary = strings.TrimSpace(summaryProp.Value)
		}
		summary = TruncateString(summary, MaxSummaryLengthBytes)

		descProp := event.GetProperty(ics.ComponentPropertyDescription)
		desc := ""
		if descProp != nil {
			desc = strings.TrimSpace(descProp.Value)
			desc = strings.ReplaceAll(desc, `\n`, " ")
			desc = strings.ReplaceAll(desc, "\n", " ")
		}
		desc = TruncateString(desc, MaxDescriptionLengthBytes)

		locProp := event.GetProperty(ics.ComponentPropertyLocation)
		var locationPtr *string
		if locProp != nil && strings.TrimSpace(locProp.Value) != "" {
			locStr := TruncateString(strings.TrimSpace(locProp.Value), 256)
			locationPtr = &locStr
		}

		dtstartProp := event.GetProperty(ics.ComponentPropertyDtStart)
		if dtstartProp == nil {
			continue
		}

		startAt, err := event.GetStartAt()
		if err != nil {
			continue
		}

		// Detect all-day event
		isAllDay := false
		if valProp := dtstartProp.ICalParameters["VALUE"]; len(valProp) > 0 && valProp[0] == "DATE" {
			isAllDay = true
		} else if len(dtstartProp.Value) == 8 {
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

		// Expand recurring events (RRULE)
		rruleProp := event.GetProperty(ics.ComponentPropertyRrule)
		var eventOccurrences [][2]time.Time

		if rruleProp != nil && rruleProp.Value != "" {
			ruleStr := strings.TrimPrefix(rruleProp.Value, "RRULE:")
			rule, err := rrule.StrToRRule(ruleStr)
			if err == nil {
				rule.DTStart(startAt.UTC())
				utcOccurrences := rule.Between(startOfDay.UTC().Add(-24*time.Hour), endOfDay.UTC().Add(24*time.Hour), true)
				for _, occ := range utcOccurrences {
					occStart := occ.In(loc)
					occEnd := occStart.Add(duration)
					if occStart.Before(endOfDay) && occEnd.After(startOfDay) {
						eventOccurrences = append(eventOccurrences, [2]time.Time{occStart, occEnd})
					}
				}
			}
		}

		if len(eventOccurrences) == 0 {
			eventStartLocal := startAt.In(loc)
			eventEndLocal := endAt.In(loc)
			if eventStartLocal.Before(endOfDay) && eventEndLocal.After(startOfDay) {
				eventOccurrences = append(eventOccurrences, [2]time.Time{eventStartLocal, eventEndLocal})
			}
		}

		for _, occ := range eventOccurrences {
			if len(entries) >= MaxParsedEventsCount {
				break
			}

			s := occ[0]
			e := occ[1]

			var startTimeStr, endTimeStr string
			durMins := int(duration.Minutes())

			if isAllDay {
				startTimeStr = "00:00"
				endTimeStr = "23:59"
				durMins = 24 * 60
			} else {
				startTimeStr = s.Format("15:04")
				endTimeStr = e.Format("15:04")
			}

			prs := ExtractPRs(fmt.Sprintf("%s %s", summary, desc))

			descBody := summary
			if desc != "" {
				descBody = fmt.Sprintf("%s: %s", summary, desc)
			}

			formattedDesc := descBody
			if len(prs) > 0 {
				formattedDesc = fmt.Sprintf("%s. PRs: %s", descBody, strings.Join(prs, ", "))
			}

			calTitle := summary
			entries = append(entries, SimpleEntry{
				Date:        targetDate.Format("2006-01-02"),
				StartTime:   startTimeStr,
				EndTime:     endTimeStr,
				Topic:       summary,
				Description: formattedDesc,
				PRs:         prs,
				Sources: EntrySources{
					CalendarEvent: &calTitle,
					Repo:          nil,
					Commits:       []string{},
				},
				Metadata: &EntryMetadata{
					DurationMinutes: durMins,
					AllDay:          isAllDay,
					Location:        locationPtr,
				},
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].StartTime < entries[j].StartTime
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
		prStr := "None"
		if len(e.PRs) > 0 {
			prStr = strings.Join(e.PRs, ", ")
		}

		calSrc := ""
		if e.Sources.CalendarEvent != nil {
			calSrc = *e.Sources.CalendarEvent
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
			idx+1, e.StartTime, e.EndTime,
			e.Date,
			e.StartTime,
			e.EndTime,
			e.Description,
			prStr,
			calSrc,
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
