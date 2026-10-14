package gradion

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// maxReadAttempts bounds a READ's retry budget. Writes never retry.
const maxReadAttempts = 3

// Client is the Gradion domain client over an MCP session.
type Client struct {
	session *mcp.Session

	// verified is set by VerifyCapabilities. Reads and writes fail closed until
	// it is true.
	verified bool

	// hasWeek records whether the server declared timesheet__get_my_week, the
	// verified live source of FULL structured entries. When false, ListEntries uses
	// the bounded legacy list_my_entries compatibility path.
	hasWeek bool

	// Listing bounds are fields so tests can shrink them; production defaults come
	// from the named constants.
	maxPages   int
	maxEntries int
	pageLimit  int
}

// NewClient wraps an initialized-able MCP session.
func NewClient(session *mcp.Session) *Client {
	return &Client{
		session:    session,
		maxPages:   MaxListingPages,
		maxEntries: MaxListingEntries,
		pageLimit:  MaxPageLimit,
	}
}

// Initialize performs the MCP handshake.
func (c *Client) Initialize(ctx context.Context) (mcp.ServerInfo, error) {
	if c == nil || c.session == nil {
		return mcp.ServerInfo{}, &Error{Code: CodeNilSession, Message: "nil MCP session"}
	}
	return c.session.Initialize(ctx)
}

// VerifyCapabilities requires every tool this client uses to appear in
// tools/list. A missing tool returns MCP_ERR_TOOL_UNAVAILABLE and the caller must
// STOP: there is NO REST fallback. On success the client is marked verified, and
// reads/writes then proceed; before that they fail closed.
func (c *Client) VerifyCapabilities(ctx context.Context) error {
	tools, err := c.session.ListTools(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(tools))
	for _, t := range tools {
		have[t.Name] = true
	}
	for _, required := range []string{ToolListEntries, ToolLogTime, ToolEditTime, ToolGetClassifications} {
		if !have[required] {
			return &Error{Code: CodeToolUnavailable, Message: "required MCP tool unavailable: " + required}
		}
	}
	c.hasWeek = have[ToolGetMyWeek]
	c.verified = true
	return nil
}

// guardVerified fails closed when VerifyCapabilities has not succeeded.
func (c *Client) guardVerified() error {
	if !c.verified {
		return &Error{Code: CodeCapabilitiesUnverified, Message: "capabilities not verified: call VerifyCapabilities first"}
	}
	return nil
}

// callRead performs a READ tool call with a bounded retry budget on retryable
// transport/5xx/429 failures. Definitive failures are not retried.
func (c *Client) callRead(ctx context.Context, tool string, args map[string]any) (mcp.CallResult, error) {
	var lastErr error
	for attempt := 1; attempt <= maxReadAttempts; attempt++ {
		res, err := c.session.CallTool(ctx, tool, args)
		if err == nil {
			return res, nil
		}
		if !isRetryableRead(err) {
			return mcp.CallResult{}, err
		}
		lastErr = err
		if ctx != nil && ctx.Err() != nil {
			break
		}
	}
	return mcp.CallResult{}, lastErr
}

func isRetryableRead(err error) bool {
	var me *mcp.Error
	if !errors.As(err, &me) {
		return false
	}
	switch me.Kind {
	case mcp.ErrTransport:
		return true
	case mcp.ErrHTTPStatus:
		return me.StatusCode >= 500 || me.StatusCode == 429
	default:
		return false
	}
}

// ListEntries obtains the structured entries for [dateFrom, dateTo].
//
// When the server declares timesheet__get_my_week (the verified live contract) it
// reads the FULL structured week data; otherwise it uses the bounded legacy
// paginated list_my_entries path. Entries are deduplicated by stable id. Complete
// is true only when every week in the range was fetched successfully with no bound
// hit and no duplicate ambiguity.
func (c *Client) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (Listing, error) {
	if err := c.guardVerified(); err != nil {
		return Listing{}, err
	}
	var (
		listing Listing
		err     error
	)
	if c.hasWeek {
		listing, err = c.listEntriesByWeek(ctx, dateFrom, dateTo)
	} else {
		listing, err = c.listEntriesLegacy(ctx, dateFrom, dateTo, limit)
	}
	if err != nil {
		return Listing{}, err
	}
	// A REAL read always yields an OBSERVED listing, so every consumer can tell it
	// apart from the offline (UNOBSERVED) listing. The two builders leave Observed
	// false; this is the single point where a real read marks it, so no real
	// listing can be mistaken for an offline one.
	listing.Observed = true
	return listing, nil
}

// listEntriesByWeek reads full structured entries via get_my_week, iterating the
// weeks that overlap [dateFrom, dateTo]. weekStart may be any date in the week: the
// server resolves the week. It FAILS CLOSED on any unrecognized week shape or
// retrieval error, and marks the listing incomplete on a bound hit or a duplicate.
func (c *Client) listEntriesByWeek(ctx context.Context, dateFrom, dateTo string) (Listing, error) {
	if strings.TrimSpace(dateFrom) == "" || strings.TrimSpace(dateTo) == "" {
		return Listing{}, &Error{Code: CodeReadSchema, Message: "list range requires dateFrom and dateTo"}
	}
	observed := time.Now().UTC()
	listing := Listing{ObservedAt: observed}
	seen := make(map[string]bool)
	cursor := dateFrom
	boundHit := false

	for weeks := 0; weeks < MaxWeeks; weeks++ {
		if cursor > dateTo {
			break
		}
		if len(listing.Entries) >= c.maxEntries {
			boundHit = true
			break
		}
		resp, err := c.fetchWeek(ctx, cursor)
		if err != nil {
			return Listing{}, err
		}
		listing.Pages++
		for _, day := range resp.Days {
			if day.Date < dateFrom || day.Date > dateTo {
				continue
			}
			for _, e := range day.Entries {
				id := strings.TrimSpace(e.ID)
				if id == "" {
					continue
				}
				if seen[id] {
					listing.Duplicates++
					continue
				}
				// Enforce the entry bound INSIDE the loop: a single large week must
				// never append past maxEntries and still report Complete.
				if len(listing.Entries) >= c.maxEntries {
					boundHit = true
					break
				}
				seen[id] = true
				listing.Entries = append(listing.Entries, e.toRemoteState(day.Date, observed))
			}
			if boundHit {
				break
			}
		}
		if boundHit {
			break
		}
		next := nextWeekCursor(resp, cursor)
		if next == "" || next <= cursor {
			break
		}
		cursor = next
	}
	if cursor <= dateTo {
		boundHit = true
	}
	if boundHit {
		listing.Truncated = 1
	}
	listing.Complete = !boundHit && listing.Duplicates == 0
	return listing, nil
}

// fetchWeek performs one get_my_week call (read retry allowed).
func (c *Client) fetchWeek(ctx context.Context, weekStart string) (weekResponse, error) {
	res, err := c.callRead(ctx, ToolGetMyWeek, map[string]any{"weekStart": weekStart})
	if err != nil {
		return weekResponse{}, err
	}
	if res.IsError {
		return weekResponse{}, &Error{Code: CodeReadToolError, Message: ToolGetMyWeek + " returned a tool-level error"}
	}
	return parseWeek(res)
}

// nextWeekCursor returns the next week's start: the day after the latest day the
// response covered, CLAMPED to at most cursor+7 so a response that spans beyond
// the requested week can never skip un-scanned weeks. It falls back to weekStart,
// then cursor+7. It returns "" for an unusable date.
func nextWeekCursor(resp weekResponse, cursor string) string {
	limit := addDays(cursor, 7)
	if limit == "" {
		return ""
	}
	max := ""
	for _, d := range resp.Days {
		if d.Date > max {
			max = d.Date
		}
	}
	if max == "" {
		max = strings.TrimSpace(resp.WeekStart)
	}
	if max == "" {
		return limit
	}
	next := addDays(max, 1)
	if next == "" || next > limit {
		return limit
	}
	return next
}

func addDays(date string, days int) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// listEntriesLegacy pages list_my_entries for [dateFrom, dateTo], deduplicating by
// stable id. It is the bounded compatibility path used when the server does not
// declare get_my_week. limit is the page size: 0 means the default page limit, and
// a value above the page cap is rejected locally (zero HTTP). Complete is true only
// when the scan terminated on a short page with no duplicates, no truncation, AND
// the first-page stability re-check passed.
func (c *Client) listEntriesLegacy(ctx context.Context, dateFrom, dateTo string, limit int) (Listing, error) {
	pageLimit, err := c.resolveLimit(limit)
	if err != nil {
		return Listing{}, err
	}

	observed := time.Now().UTC()
	listing := Listing{ObservedAt: observed}
	seen := make(map[string]bool)
	offset := 0
	terminated := false
	truncated := false
	var firstPageIDs []string

	for page := 0; page < c.maxPages; page++ {
		rows, err := c.fetchPage(ctx, dateFrom, dateTo, pageLimit, offset)
		if err != nil {
			return Listing{}, err
		}
		listing.Pages++
		if page == 0 {
			firstPageIDs = rowIDs(rows)
		}

		for _, row := range rows {
			if row.ID == "" {
				continue
			}
			if seen[row.ID] {
				listing.Duplicates++
				continue
			}
			if len(listing.Entries) >= c.maxEntries {
				truncated = true
				break
			}
			seen[row.ID] = true
			listing.Entries = append(listing.Entries, row.toRemoteState(observed))
		}
		if truncated {
			break
		}
		if len(rows) < pageLimit {
			terminated = true
			break
		}
		offset += len(rows)
	}
	if !terminated && !truncated {
		truncated = true
	}
	if truncated {
		listing.Truncated = 1
	}

	complete := terminated && listing.Duplicates == 0 && !truncated
	if complete {
		// Conservative stability re-check: a remote mutation that shifted rows
		// between pages would otherwise be mislabeled Complete, risking a false
		// absence. Re-fetch page 0 and compare ids/order to the first page.
		stable, err := c.firstPageStable(ctx, dateFrom, dateTo, pageLimit, firstPageIDs)
		if err != nil {
			return Listing{}, err
		}
		if !stable {
			complete = false
		}
	}
	listing.Complete = complete
	return listing, nil
}

// fetchPage performs one list_my_entries page call (read retry allowed).
func (c *Client) fetchPage(ctx context.Context, dateFrom, dateTo string, limit, offset int) ([]remoteRow, error) {
	args := map[string]any{
		"dateFrom": dateFrom,
		"dateTo":   dateTo,
		"limit":    limit,
		"offset":   offset,
	}
	res, err := c.callRead(ctx, ToolListEntries, args)
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, &Error{Code: CodeReadToolError, Message: ToolListEntries + " returned a tool-level error"}
	}
	return parseRows(res)
}

// firstPageStable re-fetches page 0 and reports whether its id order matches want.
func (c *Client) firstPageStable(ctx context.Context, dateFrom, dateTo string, limit int, want []string) (bool, error) {
	rows, err := c.fetchPage(ctx, dateFrom, dateTo, limit, 0)
	if err != nil {
		return false, err
	}
	got := rowIDs(rows)
	if len(got) != len(want) {
		return false, nil
	}
	for i := range got {
		if got[i] != want[i] {
			return false, nil
		}
	}
	return true, nil
}

func rowIDs(rows []remoteRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			continue
		}
		ids = append(ids, r.ID)
	}
	return ids
}

// EntryByID finds an entry by stable id across a listing. Absence (found=false)
// is claimed ONLY from a listing that is Complete after the first-page stability
// re-check; any other listing returns MCP_ERR_LISTING_INCOMPLETE instead of a
// false "not found". A concurrent remote mutation within the scan window remains
// a documented residual (like the no-idempotency-key B3 residual).
func (c *Client) EntryByID(ctx context.Context, id, from, to string) (workflow.RemoteState, bool, error) {
	if err := c.guardVerified(); err != nil {
		return workflow.RemoteState{}, false, err
	}
	listing, err := c.ListEntries(ctx, from, to, 0)
	if err != nil {
		return workflow.RemoteState{}, false, err
	}
	if !listing.Complete {
		return workflow.RemoteState{}, false, &Error{
			Code:    CodeListingIncomplete,
			Message: "listing incomplete: absence cannot be established",
		}
	}
	for _, e := range listing.Entries {
		if e.RemoteID == id {
			return e, true, nil
		}
	}
	return workflow.RemoteState{}, false, nil
}

// Classifications lists the caller's projects/activities. It is a thin view over
// ClassificationCatalog (the structured reference envelope supplies ID + Name, the
// text rendering supplies Code). An unrecognized shape fails closed.
func (c *Client) Classifications(ctx context.Context, query string) ([]Classification, error) {
	cat, err := c.ClassificationCatalog(ctx, query)
	if err != nil {
		return nil, err
	}
	classes := make([]Classification, 0, len(cat.Options))
	for _, o := range cat.Options {
		classes = append(classes, Classification{ID: o.ID, Name: o.Name, Code: o.Code})
	}
	if len(classes) > MaxClassifications {
		classes = classes[:MaxClassifications]
	}
	return classes, nil
}

// CreateEntry performs EXACTLY ONE log_time attempt (no retry).
func (c *Client) CreateEntry(ctx context.Context, req CreateRequest) (WriteResult, error) {
	if err := c.guardVerified(); err != nil {
		return WriteResult{}, err
	}
	if err := validateDescription(req.Description); err != nil {
		return WriteResult{}, err
	}
	if err := validateTask(req.Task); err != nil {
		return WriteResult{}, err
	}

	args := map[string]any{
		"date":        req.Date,
		"startTime":   req.StartTime,
		"endTime":     req.EndTime,
		"description": req.Description,
	}
	if req.ClassificationID != "" {
		args["classificationId"] = req.ClassificationID
	}
	if req.Task != "" {
		args["task"] = req.Task
	}
	if req.Billable != nil {
		args["billable"] = *req.Billable
	}
	return c.write(ctx, ToolLogTime, args)
}

// EditEntry performs EXACTLY ONE edit_time attempt (no retry). Reason is the
// required audit reason.
func (c *Client) EditEntry(ctx context.Context, req EditRequest) (WriteResult, error) {
	if err := c.guardVerified(); err != nil {
		return WriteResult{}, err
	}
	if err := validateReason(req.Reason); err != nil {
		return WriteResult{}, err
	}
	if err := validateDescription(req.Description); err != nil {
		return WriteResult{}, err
	}
	if err := validateTask(req.Task); err != nil {
		return WriteResult{}, err
	}
	if strings.TrimSpace(req.EntryID) == "" {
		return WriteResult{}, &Error{Code: CodeArgOutOfBounds, Message: "entryId is required for an edit"}
	}

	args := map[string]any{"entryId": req.EntryID, "reason": req.Reason}
	if req.Date != "" {
		args["date"] = req.Date
	}
	if req.StartTime != "" {
		args["startTime"] = req.StartTime
	}
	if req.EndTime != "" {
		args["endTime"] = req.EndTime
	}
	if req.Description != "" {
		args["description"] = req.Description
	}
	if req.ClassificationID != "" {
		args["classificationId"] = req.ClassificationID
	}
	if req.Task != "" {
		args["task"] = req.Task
	}
	if req.Billable != nil {
		args["billable"] = *req.Billable
	}
	return c.write(ctx, ToolEditTime, args)
}

// write performs one tool call and classifies the outcome. It never retries and
// never returns a non-nil error for a dispatched write: the classification is in
// WriteResult.Status. A non-nil error is reserved for local/precondition failures
// (arg bounds, uninitialized).
//
// Identity acknowledgement is separated from content observation:
//   - a complete structured response yields WriteConfirmed with an observed Entry
//     (ContentObserved=true);
//   - a reference envelope (or any identity-only shape) yields WriteIdentified
//     with the acknowledged RemoteID and a nil Entry — the caller MUST verify
//     content by a bounded stable-ID read-back before recording a mapping;
//   - an unparseable/empty response yields WriteUnidentified (no id).
//
// Planned content is NEVER copied into an observed state.
func (c *Client) write(ctx context.Context, tool string, args map[string]any) (WriteResult, error) {
	res, err := c.session.CallTool(ctx, tool, args)
	if err != nil {
		return classifyWrite(err), nil
	}
	if res.IsError {
		return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}, nil
	}
	ack, ok := parseWriteEntry(res)
	if !ok {
		return WriteResult{Status: WriteUnidentified, ErrorCode: CodeWriteUnidentified}, nil
	}
	if ack.Entry != nil {
		state := ack.Entry.toRemoteState(time.Now().UTC())
		state.ContentObserved = true
		return WriteResult{Status: WriteConfirmed, RemoteID: ack.RemoteID, Entry: &state}, nil
	}
	return WriteResult{Status: WriteIdentified, RemoteID: ack.RemoteID}, nil
}

// --- local arg bounds (validated BEFORE dispatch; zero HTTP on violation) ----

func validateDescription(s string) error {
	if len(s) > MaxDescriptionBytes {
		return &Error{Code: CodeDescriptionTooLong, Message: "description exceeds the maximum length"}
	}
	return nil
}

func validateTask(s string) error {
	if len(s) > MaxTaskBytes {
		return &Error{Code: CodeTaskTooLong, Message: "task exceeds the maximum length"}
	}
	return nil
}

func validateReason(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return &Error{Code: CodeReasonRequired, Message: "edit reason is required"}
	}
	if len(trimmed) > MaxReasonBytes {
		return &Error{Code: CodeReasonTooLong, Message: "edit reason exceeds the maximum length"}
	}
	return nil
}

// resolveLimit validates an optional page limit BEFORE dispatch. 0 means the
// default page limit; a negative value or one above the cap is rejected locally
// (zero HTTP).
func (c *Client) resolveLimit(limit int) (int, error) {
	if limit == 0 {
		return c.pageLimit, nil
	}
	if limit < 0 || limit > MaxPageLimit {
		return 0, &Error{Code: CodeLimitInvalid, Message: "limit must be between 1 and the page cap"}
	}
	return limit, nil
}
