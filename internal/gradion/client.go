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

// ListEntries pages list_my_entries for [dateFrom, dateTo], deduplicating by
// stable id. limit is the page size: 0 means the default page limit, and a value
// above the page cap is rejected locally (zero HTTP). Complete is true only when
// the scan terminated on a short page with no duplicates, no truncation, AND the
// first-page stability re-check passed.
func (c *Client) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (Listing, error) {
	if err := c.guardVerified(); err != nil {
		return Listing{}, err
	}
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

// Classifications lists the caller's projects/activities.
func (c *Client) Classifications(ctx context.Context, query string) ([]Classification, error) {
	if err := c.guardVerified(); err != nil {
		return nil, err
	}
	args := map[string]any{}
	if q := strings.TrimSpace(query); q != "" {
		args["search"] = q
	}
	res, err := c.callRead(ctx, ToolGetClassifications, args)
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, &Error{Code: CodeReadToolError, Message: ToolGetClassifications + " returned a tool-level error"}
	}
	classes, err := parseClassifications(res)
	if err != nil {
		return nil, err
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
	return c.write(ctx, ToolLogTime, args, req.Planned())
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
	return c.write(ctx, ToolEditTime, args, req.Planned())
}

// write performs one tool call and classifies the outcome. It never retries and
// never returns a non-nil error for a dispatched write: the classification is in
// WriteResult.Status. A non-nil error is reserved for local/precondition failures
// (arg bounds, uninitialized).
func (c *Client) write(ctx context.Context, tool string, args map[string]any, planned PlannedEntry) (WriteResult, error) {
	res, err := c.session.CallTool(ctx, tool, args)
	if err != nil {
		return classifyWrite(err), nil
	}
	if res.IsError {
		return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}, nil
	}
	row := parseWriteEntry(res)
	if row == nil || strings.TrimSpace(row.ID) == "" {
		return WriteResult{Status: WriteUnidentified, ErrorCode: CodeWriteUnidentified}, nil
	}
	state := mergeObserved(*row, planned, time.Now().UTC())
	return WriteResult{Status: WriteConfirmed, Entry: &state}, nil
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
