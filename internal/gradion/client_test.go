package gradion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func intArg(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return 0
}

func makeRows(n, start int) []map[string]any {
	rows := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{
			"id":               fmt.Sprintf("uuid-%d", start+i),
			"date":             "2026-10-06",
			"startTime":        "09:00",
			"endTime":          "10:00",
			"classificationId": "c1",
			"description":      "work",
		})
	}
	return rows
}

func entriesJSON(rows []map[string]any) json.RawMessage {
	rb, _ := json.Marshal(map[string]any{"entries": rows})
	return rb
}

func structured(raw json.RawMessage) callOutcome { return callOutcome{Structured: raw} }

func TestVerifyCapabilities_MissingTool(t *testing.T) {
	f := newFakeMCP()
	f.tools = []string{ToolListEntries, ToolEditTime, ToolGetClassifications} // missing ToolLogTime
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	sess, err := mcp.NewSession(mcp.Config{Endpoint: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c := NewClient(sess)
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	err = c.VerifyCapabilities(context.Background())
	if CodeOf(err) != CodeToolUnavailable {
		t.Fatalf("expected %s, got %v", CodeToolUnavailable, err)
	}
}

func TestVerifyCapabilities_OK(t *testing.T) {
	c := newTestClient(t, newFakeMCP())
	if err := c.VerifyCapabilities(context.Background()); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
}

func TestListEntries_MultiPageShortPageTermination(t *testing.T) {
	f := newFakeMCP()
	page0 := makeRows(50, 0)
	page1 := makeRows(10, 50)
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		if intArg(args["offset"]) == 0 {
			return structured(entriesJSON(page0))
		}
		return structured(entriesJSON(page1))
	}
	c := newTestClient(t, f)

	listing, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if !listing.Complete {
		t.Errorf("expected Complete, got %+v", listing)
	}
	if listing.Pages != 2 {
		t.Errorf("pages = %d, want 2", listing.Pages)
	}
	if len(listing.Entries) != 60 {
		t.Errorf("entries = %d, want 60", len(listing.Entries))
	}
	if listing.Entries[0].RemoteID != "uuid-0" || listing.Entries[59].RemoteID != "uuid-59" {
		t.Errorf("unexpected order: first=%q last=%q", listing.Entries[0].RemoteID, listing.Entries[59].RemoteID)
	}
}

func TestListEntries_DuplicateDedup(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		rows := []map[string]any{
			{"id": "a", "date": "2026-10-06"},
			{"id": "a", "date": "2026-10-06"},
			{"id": "b", "date": "2026-10-06"},
		}
		return structured(entriesJSON(rows))
	}
	c := newTestClient(t, f)

	listing, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(listing.Entries) != 2 {
		t.Errorf("entries = %d, want 2 (deduped)", len(listing.Entries))
	}
	if listing.Duplicates != 1 {
		t.Errorf("duplicates = %d, want 1", listing.Duplicates)
	}
	if listing.Complete {
		t.Error("a duplicate must make the listing incomplete")
	}
}

func TestListEntries_TruncatedByPageCap(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		return structured(entriesJSON(makeRows(5, 0)))
	}
	c := newTestClient(t, f)
	c.maxPages = 2
	c.pageLimit = 5

	listing, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if listing.Truncated == 0 || listing.Complete {
		t.Errorf("expected truncated+incomplete, got %+v", listing)
	}
	if listing.Pages != 2 {
		t.Errorf("pages = %d, want 2", listing.Pages)
	}
}

func TestEntryByID_FindsAcrossPages(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		if intArg(args["offset"]) == 0 {
			return structured(entriesJSON(makeRows(50, 0)))
		}
		return structured(entriesJSON(makeRows(10, 50)))
	}
	c := newTestClient(t, f)

	state, found, err := c.EntryByID(context.Background(), "uuid-55", "2026-10-01", "2026-10-06")
	if err != nil {
		t.Fatalf("EntryByID: %v", err)
	}
	if !found || state.RemoteID != "uuid-55" {
		t.Errorf("found=%v state=%+v", found, state)
	}
}

func TestEntryByID_RefusesFalseAbsenceOnIncompleteListing(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		return structured(entriesJSON(makeRows(5, 0)))
	}
	c := newTestClient(t, f)
	c.maxPages = 1
	c.pageLimit = 5

	_, found, err := c.EntryByID(context.Background(), "uuid-999", "2026-10-01", "2026-10-06")
	if found {
		t.Error("must not report found on an incomplete listing")
	}
	if CodeOf(err) != CodeListingIncomplete {
		t.Fatalf("expected %s, got %v", CodeListingIncomplete, err)
	}
}

func TestClassifications(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolGetClassifications {
			return callOutcome{}
		}
		rb, _ := json.Marshal(map[string]any{"resources": []map[string]any{
			{"id": "c1", "kind": "classification", "label": ""},
			{"id": "c2", "kind": "classification", "label": "Project Two"},
		}})
		return structured(rb)
	}
	c := newTestClient(t, f)

	classes, err := c.Classifications(context.Background(), "proj")
	if err != nil {
		t.Fatalf("Classifications: %v", err)
	}
	if len(classes) != 2 || classes[0].ID != "c1" || classes[1].Name != "Project Two" {
		t.Errorf("classifications = %+v", classes)
	}
}

func TestCreateEntry_Confirmed(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolLogTime {
			return callOutcome{}
		}
		return structured(json.RawMessage(`{"id":"uuid-1","date":"2026-10-06","startTime":"09:00","endTime":"10:00","classificationId":"c1","description":"work","task":"t","billable":true}`))
	}
	c := newTestClient(t, f)

	res, err := c.CreateEntry(context.Background(), CreateRequest{
		LocalID: "l1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		Description: "work", ClassificationID: "c1", Task: "t",
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteConfirmed || res.Entry == nil || res.Entry.RemoteID != "uuid-1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Entry.Task != "t" || res.Entry.Billable == nil || !*res.Entry.Billable {
		t.Errorf("observed state lost task/billable: %+v", res.Entry)
	}
}

func TestCreateEntry_Unidentified(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolLogTime {
			return callOutcome{}
		}
		return structured(json.RawMessage(`{}`))
	}
	c := newTestClient(t, f)

	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUnidentified {
		t.Errorf("status = %s, want unidentified", res.Status)
	}
}

func TestCreateEntry_ToolLevelErrorIsUncertain(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolLogTime {
			return callOutcome{}
		}
		return callOutcome{IsError: true, Text: "tool failed"}
	}
	c := newTestClient(t, f)

	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUncertain {
		t.Errorf("status = %s, want uncertain", res.Status)
	}
}

func TestWrite_JSONRPCRequestErrorIsFailed(t *testing.T) {
	c := newTestClientWithTransport(t, rpcErrorTransport(-32602))
	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteFailed {
		t.Errorf("status = %s, want failed", res.Status)
	}
}

func TestWrite_NonRequestJSONRPCErrorIsUncertain(t *testing.T) {
	c := newTestClientWithTransport(t, rpcErrorTransport(-32000))
	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUncertain {
		t.Errorf("status = %s, want uncertain", res.Status)
	}
}

func TestWrite_Definitive4xxIsFailed(t *testing.T) {
	c := newTestClientWithTransport(t, statusTransport(400))
	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteFailed {
		t.Errorf("status = %s, want failed", res.Status)
	}
}

func TestWrite_Ambiguous409IsUncertain(t *testing.T) {
	c := newTestClientWithTransport(t, statusTransport(409))
	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUncertain {
		t.Errorf("status = %s, want uncertain", res.Status)
	}
}

func TestWrite_TimeoutIsUncertain(t *testing.T) {
	tr := methodAwareTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	sess, err := mcp.NewSession(mcp.Config{Endpoint: "http://example.invalid", Token: "t", Timeout: 20 * time.Millisecond, Transport: tr})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c := NewClient(sess)
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := c.VerifyCapabilities(context.Background()); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}

	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUncertain {
		t.Errorf("status = %s, want uncertain", res.Status)
	}
}

func TestWrite_Malformed200IsUncertain(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolLogTime {
			return callOutcome{}
		}
		return callOutcome{} // 200 with neither structuredContent nor a text block
	}
	c := newTestClient(t, f)

	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.Status != WriteUncertain {
		t.Errorf("status = %s, want uncertain (S5L1-5)", res.Status)
	}
}

func TestWrite_ExactlyOneAttemptServerCounted(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolLogTime {
			return callOutcome{}
		}
		return callOutcome{IsError: true, Text: "boom"}
	}
	c := newTestClient(t, f)
	if _, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"}); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if n := f.countToolCalls(ToolLogTime); n != 1 {
		t.Errorf("tools/call attempts = %d, want exactly 1", n)
	}
}

func TestArgBounds_ZeroHTTP(t *testing.T) {
	f := newFakeMCP()
	c := newTestClient(t, f)

	cases := []struct {
		name string
		call func() error
		code string
	}{
		{"description", func() error {
			_, err := c.CreateEntry(context.Background(), CreateRequest{Description: strings.Repeat("x", MaxDescriptionBytes+1)})
			return err
		}, CodeDescriptionTooLong},
		{"task", func() error {
			_, err := c.CreateEntry(context.Background(), CreateRequest{Task: strings.Repeat("x", MaxTaskBytes+1)})
			return err
		}, CodeTaskTooLong},
		{"reason too long", func() error {
			_, err := c.EditEntry(context.Background(), EditRequest{EntryID: "e", Reason: strings.Repeat("x", MaxReasonBytes+1)})
			return err
		}, CodeReasonTooLong},
		{"reason required", func() error {
			_, err := c.EditEntry(context.Background(), EditRequest{EntryID: "e", Reason: "  "})
			return err
		}, CodeReasonRequired},
		{"missing entryId", func() error {
			_, err := c.EditEntry(context.Background(), EditRequest{Reason: "r"})
			return err
		}, CodeArgOutOfBounds},
		{"limit", func() error {
			_, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", MaxPageLimit+1)
			return err
		}, CodeLimitInvalid},
	}
	for _, tc := range cases {
		if code := CodeOf(tc.call()); code != tc.code {
			t.Errorf("%s: code = %q, want %q", tc.name, code, tc.code)
		}
	}
	if n := f.countMethod("tools/call"); n != 0 {
		t.Errorf("arg-bound violations must send ZERO tools/call requests, got %d", n)
	}
}

func TestEditEntry_ConfirmedSendsReason(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		if name != ToolEditTime {
			return callOutcome{}
		}
		return structured(json.RawMessage(`{"id":"uuid-9"}`))
	}
	c := newTestClient(t, f)

	res, err := c.EditEntry(context.Background(), EditRequest{LocalID: "l1", EntryID: "uuid-9", Reason: "fix", Description: "work"})
	if err != nil {
		t.Fatalf("EditEntry: %v", err)
	}
	if res.Status != WriteConfirmed || res.Entry == nil || res.Entry.RemoteID != "uuid-9" {
		t.Fatalf("unexpected result: %+v", res)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, r := range f.requests {
		if r.Method == "tools/call" && r.Tool == ToolEditTime {
			if r.Args["reason"] == "fix" {
				found = true
			}
		}
	}
	if !found {
		t.Error("edit reason was not sent to the tool")
	}
}

// W1: the DERIVED activity type is display/validation only and must NEVER enter
// the log_time/edit_time payload. Only classificationId is sent.
func TestWrite_PayloadHasNoActivityTypeKey(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, _ map[string]any) callOutcome {
		switch name {
		case ToolLogTime:
			return structured(json.RawMessage(`{"id":"uuid-1"}`))
		case ToolEditTime:
			return structured(json.RawMessage(`{"id":"uuid-2"}`))
		}
		return callOutcome{}
	}
	c := newTestClient(t, f)

	if _, err := c.CreateEntry(context.Background(), CreateRequest{
		Date: "2026-10-06", Description: "work", ClassificationID: "cls-1",
	}); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if _, err := c.EditEntry(context.Background(), EditRequest{
		EntryID: "uuid-2", Reason: "fix", Description: "work", ClassificationID: "cls-1",
	}); err != nil {
		t.Fatalf("EditEntry: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	checked := 0
	for _, r := range f.requests {
		if r.Method != "tools/call" || (r.Tool != ToolLogTime && r.Tool != ToolEditTime) {
			continue
		}
		checked++
		for k := range r.Args {
			if strings.Contains(strings.ToLower(k), "activity") {
				t.Errorf("%s payload must not carry an activity-type key, got %q", r.Tool, k)
			}
		}
		if r.Args["classificationId"] != "cls-1" {
			t.Errorf("%s must send classificationId=cls-1, got %v", r.Tool, r.Args["classificationId"])
		}
	}
	if checked != 2 {
		t.Fatalf("want one log_time and one edit_time payload, checked %d", checked)
	}
}

func TestSecretSafety(t *testing.T) {
	const token = "S3CR3T-GRADION-TOKEN"
	tr := methodAwareTransport(func(*http.Request) (*http.Response, error) {
		return resp(500, "application/json", "leaked "+token), nil
	})
	sess, err := mcp.NewSession(mcp.Config{Endpoint: "http://example.invalid", Token: token, Transport: tr})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c := NewClient(sess)
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := c.VerifyCapabilities(context.Background()); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}

	res, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "work"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if data, _ := json.Marshal(res); strings.Contains(string(data), token) {
		t.Errorf("token leaked into WriteResult: %s", data)
	}
	listing := Listing{Entries: []workflow.RemoteState{{RemoteID: "uuid-1", Date: "2026-10-06"}}}
	if data, _ := json.Marshal(listing); strings.Contains(string(data), token) {
		t.Errorf("token leaked into Listing: %s", data)
	}
	if data, _ := json.Marshal(mcp.Config{Endpoint: "x", Token: token}); strings.Contains(string(data), token) {
		t.Errorf("token leaked into serialized Config: %s", data)
	}
}

// TestListEntries_StabilityRecheckCatchesShift simulates a front-row deletion
// between page 0 and page 1: the re-check detects the shift and the listing is
// NOT Complete (F1).
func TestListEntries_StabilityRecheckCatchesShift(t *testing.T) {
	f := newFakeMCP()
	var mu sync.Mutex
	page0Calls := 0
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		if intArg(args["offset"]) == 0 {
			mu.Lock()
			page0Calls++
			n := page0Calls
			mu.Unlock()
			if n == 1 {
				return structured(entriesJSON([]map[string]any{{"id": "A"}, {"id": "B"}, {"id": "C"}}))
			}
			return structured(entriesJSON([]map[string]any{{"id": "B"}, {"id": "C"}, {"id": "D"}}))
		}
		return structured(entriesJSON([]map[string]any{{"id": "E"}}))
	}
	c := newTestClient(t, f)
	c.pageLimit = 3

	listing, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if listing.Complete {
		t.Errorf("expected Complete=false after a front-row shift, got %+v", listing)
	}
}

// TestEntryByID_RefusesFalseAbsenceAfterShift proves EntryByID never returns a
// false absence when the stability re-check fails (F1).
func TestEntryByID_RefusesFalseAbsenceAfterShift(t *testing.T) {
	f := newFakeMCP()
	var mu sync.Mutex
	page0Calls := 0
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		if intArg(args["offset"]) == 0 {
			mu.Lock()
			page0Calls++
			n := page0Calls
			mu.Unlock()
			if n == 1 {
				return structured(entriesJSON([]map[string]any{{"id": "A"}, {"id": "B"}, {"id": "C"}}))
			}
			return structured(entriesJSON([]map[string]any{{"id": "B"}, {"id": "C"}, {"id": "D"}}))
		}
		return structured(entriesJSON([]map[string]any{{"id": "E"}}))
	}
	c := newTestClient(t, f)
	c.pageLimit = 3

	_, found, err := c.EntryByID(context.Background(), "uuid-999", "2026-10-01", "2026-10-06")
	if found {
		t.Error("must not report found")
	}
	if CodeOf(err) != CodeListingIncomplete {
		t.Fatalf("expected %s, got %v", CodeListingIncomplete, err)
	}
}

// TestCapabilities_UnverifiedFailsClosed proves reads/writes fail closed before
// VerifyCapabilities (F3).
func TestCapabilities_UnverifiedFailsClosed(t *testing.T) {
	f := newFakeMCP()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	sess, err := mcp.NewSession(mcp.Config{Endpoint: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c := NewClient(sess)
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// NOT verified yet.

	if _, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0); CodeOf(err) != CodeCapabilitiesUnverified {
		t.Errorf("ListEntries: %v", err)
	}
	if _, _, err := c.EntryByID(context.Background(), "x", "2026-10-01", "2026-10-06"); CodeOf(err) != CodeCapabilitiesUnverified {
		t.Errorf("EntryByID: %v", err)
	}
	if _, err := c.Classifications(context.Background(), ""); CodeOf(err) != CodeCapabilitiesUnverified {
		t.Errorf("Classifications: %v", err)
	}
	if _, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "w"}); CodeOf(err) != CodeCapabilitiesUnverified {
		t.Errorf("CreateEntry: %v", err)
	}
	if _, err := c.EditEntry(context.Background(), EditRequest{EntryID: "e", Reason: "r"}); CodeOf(err) != CodeCapabilitiesUnverified {
		t.Errorf("EditEntry: %v", err)
	}
	if n := f.countMethod("tools/call"); n != 0 {
		t.Errorf("unverified calls must send ZERO tools/call requests, got %d", n)
	}
}

// TestWrite_ExactlyOnceOnRetryablePaths proves a WRITE is attempted exactly once
// even on a retryable path (T1).
func TestWrite_ExactlyOnceOnRetryablePaths(t *testing.T) {
	cases := []struct {
		name string
		tr   *fakeTransport
	}{
		{"500", statusTransport(500)},
		{"429", statusTransport(429)},
		{"transport", methodAwareTransport(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset")
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClientWithTransport(t, tc.tr)
			if _, err := c.CreateEntry(context.Background(), CreateRequest{Date: "2026-10-06", Description: "w"}); err != nil {
				t.Fatalf("CreateEntry: %v", err)
			}
			if n := tc.tr.countMethod("tools/call"); n != 1 {
				t.Errorf("tools/call attempts = %d, want exactly 1", n)
			}
		})
	}
}

// TestRead_RetryBudget proves reads retry on 5xx/429/transport, exhaust the
// budget, and never retry a definitive 4xx (T2).
func TestRead_RetryBudget(t *testing.T) {
	t.Run("retry on 5xx then succeed", func(t *testing.T) {
		tr := flakyListTransport(500, 1)
		c := newTestClientWithTransport(t, tr)
		if _, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0); err != nil {
			t.Fatalf("ListEntries: %v", err)
		}
		if n := tr.countMethod("tools/call"); n < 2 {
			t.Errorf("expected a retry, got %d attempts", n)
		}
	})
	t.Run("exhaustion", func(t *testing.T) {
		tr := flakyListTransport(500, 100)
		c := newTestClientWithTransport(t, tr)
		if _, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0); err == nil {
			t.Fatal("expected an error after exhausting the read budget")
		}
		if n := tr.countMethod("tools/call"); n != maxReadAttempts {
			t.Errorf("attempts = %d, want %d", n, maxReadAttempts)
		}
	})
	t.Run("definitive 4xx not retried", func(t *testing.T) {
		tr := flakyListTransport(400, 100)
		c := newTestClientWithTransport(t, tr)
		if _, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0); err == nil {
			t.Fatal("expected an error")
		}
		if n := tr.countMethod("tools/call"); n != 1 {
			t.Errorf("attempts = %d, want 1 (no retry on definitive 4xx)", n)
		}
	})
}

// TestListEntries_CrossPageDuplicate proves a duplicate id spanning two pages is
// deduplicated and makes the listing incomplete (T4).
func TestListEntries_CrossPageDuplicate(t *testing.T) {
	f := newFakeMCP()
	f.onCall = func(name string, args map[string]any) callOutcome {
		if name != ToolListEntries {
			return callOutcome{}
		}
		if intArg(args["offset"]) == 0 {
			return structured(entriesJSON([]map[string]any{{"id": "A"}, {"id": "B"}, {"id": "C"}}))
		}
		return structured(entriesJSON([]map[string]any{{"id": "C"}, {"id": "D"}}))
	}
	c := newTestClient(t, f)
	c.pageLimit = 3

	listing, err := c.ListEntries(context.Background(), "2026-10-01", "2026-10-06", 0)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if listing.Duplicates != 1 {
		t.Errorf("duplicates = %d, want 1", listing.Duplicates)
	}
	if listing.Complete {
		t.Error("a cross-page duplicate must make the listing incomplete")
	}
}
