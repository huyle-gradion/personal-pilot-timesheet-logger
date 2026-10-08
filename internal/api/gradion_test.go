package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestNewClient(t *testing.T) {
	customClient := &http.Client{Timeout: 5 * time.Second}
	c, err := NewClient("https://ts-prod.gradion.com/", "test-token",
		WithHTTPClient(customClient),
		WithRetryCount(5),
		WithBackoffBase(10*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	if c.baseURL != "https://ts-prod.gradion.com" {
		t.Errorf("expected trimmed base URL without trailing slash, got %q", c.baseURL)
	}
	if c.retryCount != 5 {
		t.Errorf("expected retryCount=5, got %d", c.retryCount)
	}
	if c.backoffBase != 10*time.Millisecond {
		t.Errorf("expected backoffBase=10ms, got %v", c.backoffBase)
	}
}

func TestClient_GetEntries_Envelope(t *testing.T) {
	mockEntries := []RemoteEntry{
		{
			ID:               "entry-1",
			Date:             "2026-10-08",
			StartTime:        "09:00",
			EndTime:          "10:00",
			ClassificationID: DefaultClassificationID,
			Description:      "Morning standup & plan",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/api/entries") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing or invalid Authorization header: %s", r.Header.Get("Authorization"))
		}

		resp := RemoteEntriesResponse{
			Total:   1,
			Entries: mockEntries,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	entries, err := c.GetEntries(context.Background(), "2026-10-08")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != "entry-1" {
		t.Errorf("expected entry-1, got %s", entries[0].ID)
	}
}

func TestClient_GetEntries_DirectList(t *testing.T) {
	mockEntries := []RemoteEntry{
		{
			ID:               "entry-direct",
			Date:             "2026-10-08",
			StartTime:        "13:00",
			EndTime:          "15:00",
			ClassificationID: DefaultClassificationID,
			Description:      "Direct list entry",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockEntries)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	entries, err := c.GetEntries(context.Background(), "2026-10-08")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 1 || entries[0].ID != "entry-direct" {
		t.Errorf("unexpected entries returned: %+v", entries)
	}
}

func TestClient_GetEntries_SchemaMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"not an object or list"`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected schema mismatch error, got nil")
	}

	var opErr *OperatingError
	if !strings.Contains(err.Error(), "API_ERR_SCHEMA_MISMATCH") {
		t.Errorf("expected API_ERR_SCHEMA_MISMATCH code, got: %v", opErr)
	}
}

func TestClient_GetEntries_CapacityExceeded(t *testing.T) {
	var excessive []RemoteEntry
	for i := 0; i < MaxRemoteEntriesCount+10; i++ {
		excessive = append(excessive, RemoteEntry{ID: fmt.Sprintf("id-%d", i)})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(excessive)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected capacity exceeded error, got nil")
	}
	if !strings.Contains(err.Error(), "API_ERR_CAPACITY_EXCEEDED") {
		t.Errorf("expected API_ERR_CAPACITY_EXCEEDED, got: %v", err)
	}
}

func TestClient_CreateEntry(t *testing.T) {
	req := CreateEntryRequest{
		Date:             "2026-10-08",
		StartTime:        "10:00",
		EndTime:          "11:00",
		ClassificationID: DefaultClassificationID,
		Description:      "Code review session",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/entries" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var payload CreateEntryRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}
		if payload.Description != req.Description {
			t.Errorf("expected description %q, got %q", req.Description, payload.Description)
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(RemoteEntry{
			ID:               "new-created-123",
			Date:             payload.Date,
			StartTime:        payload.StartTime,
			EndTime:          payload.EndTime,
			ClassificationID: payload.ClassificationID,
			Description:      payload.Description,
		})
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))

	// Invalid payload check
	_, err := c.CreateEntry(context.Background(), CreateEntryRequest{})
	if err == nil {
		t.Fatal("expected invalid payload error, got nil")
	}

	// Valid creation
	created, err := c.CreateEntry(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error creating entry: %v", err)
	}
	if created.ID != "new-created-123" {
		t.Errorf("expected ID new-created-123, got %s", created.ID)
	}
}

func TestClient_CreateEntry_FallbackResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	req := CreateEntryRequest{
		Date:             "2026-10-08",
		StartTime:        "14:00",
		EndTime:          "15:00",
		ClassificationID: DefaultClassificationID,
		Description:      "Design session",
	}
	created, err := c.CreateEntry(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if created.Date != req.Date || created.StartTime != req.StartTime {
		t.Errorf("expected constructed fallback entry, got: %+v", created)
	}
}

func TestClient_PatchEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		if r.URL.Path != "/api/entries/entry-abc" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		var payload PatchEntryRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("failed to decode request: %v", err)
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(RemoteEntry{
			ID:          "entry-abc",
			Description: payload.DTO.Description,
			Task:        payload.DTO.Task,
		})
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))

	// Empty ID check
	_, err := c.PatchEntry(context.Background(), "", PatchEntryRequest{})
	if err == nil {
		t.Fatal("expected invalid ID error, got nil")
	}

	// Valid patch
	req := PatchEntryRequest{
		DTO: PatchEntryDTO{
			Description: "Updated description text",
			Task:        "Refactor",
		},
		Reason: "Timesheet update sync",
	}
	updated, err := c.PatchEntry(context.Background(), "entry-abc", req)
	if err != nil {
		t.Fatalf("unexpected error patching entry: %v", err)
	}
	if updated.Description != "Updated description text" {
		t.Errorf("expected updated description, got %s", updated.Description)
	}
}

func TestClient_PatchEntry_FallbackResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	req := PatchEntryRequest{
		DTO: PatchEntryDTO{
			Description: "Updated via fallback",
		},
		Reason: "Sync",
	}
	updated, err := c.PatchEntry(context.Background(), "entry-xyz", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ID != "entry-xyz" || updated.Description != "Updated via fallback" {
		t.Errorf("expected fallback entry, got: %+v", updated)
	}
}

func TestClient_DeleteEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/entries/del-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))

	// Empty ID check
	if err := c.DeleteEntry(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty ID")
	}

	// Valid delete
	if err := c.DeleteEntry(context.Background(), "del-123"); err != nil {
		t.Fatalf("unexpected error deleting entry: %v", err)
	}
}

func TestClient_GetRecentClassifications(t *testing.T) {
	mockList := []Classification{
		{ID: "cls-1", Name: "Engineering"},
		{ID: "cls-2", Name: "Mentoring"},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/classifications/recent" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockList)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	list, err := c.GetRecentClassifications(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 2 || list[0].Name != "Engineering" {
		t.Errorf("unexpected classifications: %+v", list)
	}

	// Malformed JSON check
	badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer badServer.Close()

	badClient, _ := NewClient(badServer.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	if _, badErr := badClient.GetRecentClassifications(context.Background()); badErr == nil {
		t.Fatal("expected error on malformed json")
	}
}

func TestClient_ClientErrors_4xxClassification(t *testing.T) {
	codes := []struct {
		statusCode   int
		expectedCode string
	}{
		{http.StatusUnauthorized, "API_ERR_UNAUTHORIZED"},
		{http.StatusForbidden, "API_ERR_FORBIDDEN"},
		{http.StatusNotFound, "API_ERR_NOT_FOUND"},
		{http.StatusBadRequest, "API_ERR_BAD_REQUEST"},
	}

	for _, tc := range codes {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.statusCode)
			_, _ = w.Write([]byte("error response"))
		}))

		c, _ := NewClient(server.URL, "test-token", WithRetryCount(0))
		_, err := c.GetEntries(context.Background(), "2026-10-08")
		server.Close()

		if err == nil {
			t.Fatalf("expected error for status %d, got nil", tc.statusCode)
		}
		if !strings.Contains(err.Error(), tc.expectedCode) {
			t.Errorf("expected code %s for status %d, got error: %v", tc.expectedCode, tc.statusCode, err)
		}
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	c, _ := NewClient("https://example.com", "test-token")

	// 1. Context already canceled
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetEntries(canceledCtx, "2026-10-08")
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
	if !strings.Contains(err.Error(), "API_ERR_CONTEXT_CANCELED") {
		t.Errorf("expected API_ERR_CONTEXT_CANCELED, got: %v", err)
	}

	// 2. Context canceled during retry backoff
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	retryClient, _ := NewClient(server.URL, "test-token", WithRetryCount(3), WithBackoffBase(200*time.Millisecond))
	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTimeout()

	_, err = retryClient.GetEntries(ctxTimeout, "2026-10-08")
	if err == nil {
		t.Fatal("expected timeout error during retry backoff")
	}
	if !strings.Contains(err.Error(), "API_ERR_CONTEXT_CANCELED") {
		t.Errorf("expected API_ERR_CONTEXT_CANCELED, got: %v", err)
	}
}

func TestClient_RetriesOn5xxAnd429(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&attempts, 1)
		if current == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
			return
		}
		if current == 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithRetryCount(3), WithBackoffBase(5*time.Millisecond))
	entries, err := c.GetEntries(context.Background(), "2026-10-08")
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty list, got: %+v", entries)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
}

func TestClient_RetriesExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithRetryCount(2), WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected retries exhausted error, got nil")
	}
	if !strings.Contains(err.Error(), "API_ERR_RETRIES_EXHAUSTED") {
		t.Errorf("expected API_ERR_RETRIES_EXHAUSTED, got: %v", err)
	}
}

func TestClient_RedirectNotAllowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/other", http.StatusFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected redirect error, got nil")
	}
	if !strings.Contains(err.Error(), "API_ERR_REDIRECT_NOT_ALLOWED") {
		t.Errorf("expected API_ERR_REDIRECT_NOT_ALLOWED, got: %v", err)
	}
}

func TestClient_ResponseTooLarge(t *testing.T) {
	largeData := strings.Repeat("A", int(MaxResponseBodyBytes)+100)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(largeData))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "test-token", WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected response too large error, got nil")
	}
	if !strings.Contains(err.Error(), "API_ERR_RESPONSE_TOO_LARGE") {
		t.Errorf("expected API_ERR_RESPONSE_TOO_LARGE, got: %v", err)
	}
}

func TestReconcile_IdempotentSkipAndTwoWayDiff(t *testing.T) {
	localBlocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        "2026-10-08",
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Sprint planning meeting",
		},
		{
			ID:          "block-2",
			Date:        "2026-10-08",
			StartTime:   "10:00",
			EndTime:     "12:00",
			Description: "Updated feature development work",
		},
		{
			ID:          "block-3",
			Date:        "2026-10-08",
			StartTime:   "13:00",
			EndTime:     "15:00",
			Title:       "New afternoon task from title",
			Description: "", // Test fallback to title
		},
	}

	remoteEntries := []RemoteEntry{
		{
			ID:               "remote-1",
			Date:             "2026-10-08",
			StartTime:        "09:00:00", // Test normalization with seconds
			EndTime:          "10:00:00",
			ClassificationID: DefaultClassificationID,
			Description:      "Sprint planning meeting", // Exact match -> Skip
		},
		{
			ID:               "remote-2",
			Date:             "2026-10-08",
			StartTime:        "10:00",
			EndTime:          "12:00",
			ClassificationID: DefaultClassificationID,
			Description:      "Old feature development work", // Modified description -> Update
		},
		{
			ID:               "remote-legacy",
			Date:             "2026-10-08",
			StartTime:        "16:00",
			EndTime:          "17:00",
			ClassificationID: DefaultClassificationID,
			Description:      "Superceded activity not in local blocks",
		},
	}

	items, err := Reconcile(context.Background(), localBlocks, remoteEntries, DefaultClassificationID)
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	// Should have 4 items: block-1 (skip), block-2 (update), block-3 (create), remote-legacy (skip)
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d: %+v", len(items), items)
	}

	summary := BuildSummary("2026-10-08", items)
	if summary.Created != 1 {
		t.Errorf("expected 1 created, got %d", summary.Created)
	}
	if summary.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", summary.Updated)
	}
	if summary.Skipped != 2 {
		t.Errorf("expected 2 skipped, got %d", summary.Skipped)
	}
	if summary.TotalHours <= 0 {
		t.Errorf("expected positive total hours, got %.2f", summary.TotalHours)
	}

	// Verify idempotency: running Reconcile again with remoteEntries updated to match localBlocks produces zero creates/updates
	updatedRemote := []RemoteEntry{
		{
			ID:          "remote-1",
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Sprint planning meeting",
		},
		{
			ID:          "remote-2",
			StartTime:   "10:00",
			EndTime:     "12:00",
			Description: "Updated feature development work",
		},
		{
			ID:          "remote-3",
			StartTime:   "13:00",
			EndTime:     "15:00",
			Description: "New afternoon task from title",
		},
	}

	idempotentItems, err := Reconcile(context.Background(), localBlocks, updatedRemote, "")
	if err != nil {
		t.Fatalf("unexpected error on second reconcile: %v", err)
	}
	idempotentSummary := BuildSummary("2026-10-08", idempotentItems)
	if idempotentSummary.Created != 0 {
		t.Errorf("expected 0 creates on idempotent run, got %d", idempotentSummary.Created)
	}
	if idempotentSummary.Updated != 0 {
		t.Errorf("expected 0 updates on idempotent run, got %d", idempotentSummary.Updated)
	}
	if idempotentSummary.Skipped != 3 {
		t.Errorf("expected 3 skips on idempotent run, got %d", idempotentSummary.Skipped)
	}
}

func TestReconcile_CapacityBounds(t *testing.T) {
	var excessiveBlocks []timesheet.TimeBlock
	for i := 0; i < MaxLocalBlocksCount+5; i++ {
		excessiveBlocks = append(excessiveBlocks, timesheet.TimeBlock{ID: fmt.Sprintf("b-%d", i)})
	}

	_, err := Reconcile(context.Background(), excessiveBlocks, nil, "")
	if err == nil {
		t.Fatal("expected capacity error for local blocks")
	}

	var excessiveRemote []RemoteEntry
	for i := 0; i < MaxRemoteEntriesCount+5; i++ {
		excessiveRemote = append(excessiveRemote, RemoteEntry{ID: fmt.Sprintf("r-%d", i)})
	}

	_, err = Reconcile(context.Background(), nil, excessiveRemote, "")
	if err == nil {
		t.Fatal("expected capacity error for remote entries")
	}
}

func TestOperatingError_Sanitization(t *testing.T) {
	secretToken := "secret-token-abcdef123456"
	rawErr := fmt.Errorf("request failed with token: %s", secretToken)
	sanitized := sanitizeError(rawErr, secretToken)
	if strings.Contains(sanitized, secretToken) {
		t.Fatalf("token leaked in sanitized error: %s", sanitized)
	}
	if !strings.Contains(sanitized, "<redacted-token>") {
		t.Errorf("expected <redacted-token> in sanitized error: %s", sanitized)
	}

	// url.Error sanitization
	urlErr := &url.Error{
		Op:  "Get",
		URL: "https://user:pass@example.com/api?token=" + secretToken,
		Err: fmt.Errorf("network down"),
	}
	urlSanitized := sanitizeError(urlErr, secretToken)
	if strings.Contains(urlSanitized, "pass") || strings.Contains(urlSanitized, secretToken) {
		t.Errorf("credentials leaked in url error: %s", urlSanitized)
	}

	opErr := &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "ERR_TEST",
		Message: "test failure",
		Err:     rawErr,
	}
	if opErr.Unwrap() != rawErr {
		t.Errorf("expected unwrap to match rawErr")
	}

	// Outcome strings
	outcomes := []Outcome{OutcomeAcknowledged, OutcomeRetryable, OutcomeTerminal, OutcomeRateLimited, Outcome(99)}
	for _, oc := range outcomes {
		if oc.String() == "" {
			t.Errorf("empty outcome string for %v", oc)
		}
	}
}

// Subprocess fatal assertion tests per go-rules.md
func TestAPI_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_API_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "empty_token":
			_, _ = NewClient("https://example.com", "")
		case "empty_base_url":
			_, _ = NewClient("", "token")
		case "nil_ctx_entries":
			c, _ := NewClient("https://example.com", "token")
			_, _ = c.GetEntries(nil, "2026-10-08")
		case "empty_date_entries":
			c, _ := NewClient("https://example.com", "token")
			_, _ = c.GetEntries(context.Background(), "")
		case "nil_client_entries":
			var c *Client
			_, _ = c.GetEntries(context.Background(), "2026-10-08")
		case "nil_ctx_reconcile":
			_, _ = Reconcile(nil, nil, nil, "")
		}
		return
	}

	testCases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"empty_token", "empty_token", AssertAPIEmptyToken},
		{"empty_base_url", "empty_base_url", AssertAPIBaseURLEmpty},
		{"nil_ctx_entries", "nil_ctx_entries", AssertAPINilContext},
		{"empty_date_entries", "empty_date_entries", AssertAPIZeroDate},
		{"nil_client_entries", "nil_client_entries", AssertAPINilClient},
		{"nil_ctx_reconcile", "nil_ctx_reconcile", AssertAPINilContext},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestAPI_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_API_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected subprocess to crash with panic, but it succeeded: %s", string(out))
			}
			if !strings.Contains(string(out), tc.expected) {
				t.Fatalf("expected panic output to contain %q, got: %s", tc.expected, string(out))
			}
		})
	}
}
