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
	outcomes := []Outcome{OutcomeAcknowledged, OutcomeRetryable, OutcomeTerminal, OutcomeRateLimited, OutcomeUncertain, Outcome(99)}
	for _, oc := range outcomes {
		if oc.String() == "" {
			t.Errorf("empty outcome string for %v", oc)
		}
	}

	// F5: short tokens are redacted too (no len>4 guard).
	shortToken := "abc"
	shortSanitized := sanitizeError(fmt.Errorf("header contains abc end"), shortToken)
	if strings.Contains(shortSanitized, shortToken) {
		t.Errorf("short token leaked in sanitized error: %s", shortSanitized)
	}
	if !strings.Contains(shortSanitized, "<redacted-token>") {
		t.Errorf("expected short token to be redacted: %s", shortSanitized)
	}
}

// TestClient_ErrorBodySnippetBoundedAndSanitized verifies F3: a 4xx error
// embeds only a bounded, token-redacted snippet of the response body.
func TestClient_ErrorBodySnippetBoundedAndSanitized(t *testing.T) {
	secretToken := "test-token"
	// Body echoes the token and is far larger than the snippet bound.
	body := "auth=" + secretToken + " " + strings.Repeat("A", MaxErrorBodySnippetBytes*4)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, secretToken, WithRetryCount(0), WithBackoffBase(1*time.Millisecond))
	_, err := c.GetEntries(context.Background(), "2026-10-08")
	if err == nil {
		t.Fatal("expected 4xx error, got nil")
	}
	errText := err.Error()
	if strings.Contains(errText, secretToken) {
		t.Errorf("token leaked in error text: %s", errText)
	}
	// The tail of the oversized body must not be embedded.
	if strings.Contains(errText, strings.Repeat("A", MaxErrorBodySnippetBytes+100)) {
		t.Errorf("unbounded response body embedded in error text")
	}
}

// TestErrorBodySnippet_SecretCrossesTruncationBoundary is the key regression
// for redaction-before-truncation: a token that straddles the 512-byte display
// bound must not leave a detectable prefix behind.
func TestErrorBodySnippet_SecretCrossesTruncationBoundary(t *testing.T) {
	token := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// Start the token 5 bytes before the display bound so it is split by it.
	body := strings.Repeat("x", MaxErrorBodySnippetBytes-5) + token + strings.Repeat("y", 64)

	snippet := errorBodySnippet([]byte(body), token)
	if len(snippet) > MaxErrorBodySnippetBytes {
		t.Fatalf("snippet exceeds display bound: %d bytes", len(snippet))
	}
	if strings.Contains(snippet, token) {
		t.Fatalf("full token leaked through snippet: %q", snippet)
	}
	// Every prefix of the token must be absent: truncating before redaction
	// would leave the first ~5 bytes of the token at the cut.
	for n := 1; n <= len(token); n++ {
		if strings.Contains(snippet, token[:n]) {
			t.Fatalf("token prefix %q leaked through the truncation boundary", token[:n])
		}
	}
}

// TestErrorBodySnippet_ShortSecretRedacted verifies short secrets (below the
// old len>4 guard) are redacted from the snippet.
func TestErrorBodySnippet_ShortSecretRedacted(t *testing.T) {
	token := "zq7"
	body := []byte("before zq7 middle zq7 after")

	snippet := errorBodySnippet(body, token)
	if strings.Contains(snippet, token) {
		t.Fatalf("short token leaked: %q", snippet)
	}
}

// TestErrorBodySnippet_RepeatedOccurrencesRedacted verifies every occurrence of
// a secret is redacted, not just the first.
func TestErrorBodySnippet_RepeatedOccurrencesRedacted(t *testing.T) {
	token := "repeat-me-token-123"
	body := []byte(token + " middle " + token + " tail " + token)

	snippet := errorBodySnippet(body, token)
	if strings.Contains(snippet, token) {
		t.Fatalf("repeated token occurrence leaked: %q", snippet)
	}
}

// TestWriteStatusClassification is a table-driven check of non-idempotent write
// error classification: ambiguous statuses are OutcomeUncertain with exactly one
// attempt (no retry), definitive rejections stay terminal, and idempotent
// methods are unaffected.
func TestWriteStatusClassification(t *testing.T) {
	cases := []struct {
		name          string
		method        string
		status        int
		wantUncertain bool
		wantCode      string
	}{
		{"POST 408 uncertain", http.MethodPost, http.StatusRequestTimeout, true, ""},
		{"PATCH 409 uncertain", http.MethodPatch, http.StatusConflict, true, ""},
		{"DELETE 425 uncertain", http.MethodDelete, http.StatusTooEarly, true, ""},
		{"POST 429 uncertain", http.MethodPost, http.StatusTooManyRequests, true, ""},
		{"POST 500 uncertain", http.MethodPost, http.StatusInternalServerError, true, ""},
		{"POST 400 terminal", http.MethodPost, http.StatusBadRequest, false, "API_ERR_BAD_REQUEST"},
		{"PATCH 404 terminal", http.MethodPatch, http.StatusNotFound, false, "API_ERR_NOT_FOUND"},
		{"POST 422 terminal", http.MethodPost, http.StatusUnprocessableEntity, false, "API_ERR_CLIENT_ERROR"},
		{"POST 401 terminal", http.MethodPost, http.StatusUnauthorized, false, "API_ERR_UNAUTHORIZED"},
		{"GET 408 terminal (idempotent unaffected)", http.MethodGet, http.StatusRequestTimeout, false, "API_ERR_CLIENT_ERROR"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attempts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("body"))
			}))
			defer server.Close()

			c, _ := NewClient(server.URL, "test-token", WithRetryCount(3), WithBackoffBase(1*time.Millisecond))
			_, _, err := c.executeRequest(context.Background(), tc.method, "/api/entries", []byte(`{}`))
			if err == nil {
				t.Fatal("expected an error")
			}

			if got := IsUncertain(err); got != tc.wantUncertain {
				t.Fatalf("IsUncertain=%v, want %v (err: %v)", got, tc.wantUncertain, err)
			}
			if tc.wantUncertain {
				if !strings.Contains(err.Error(), "API_ERR_UNCERTAIN_WRITE") {
					t.Fatalf("expected API_ERR_UNCERTAIN_WRITE, got: %v", err)
				}
				if got := atomic.LoadInt32(&attempts); got != 1 {
					t.Fatalf("expected exactly 1 attempt (no retry), got %d", got)
				}
				return
			}
			if tc.wantCode != "" && !strings.Contains(err.Error(), tc.wantCode) {
				t.Fatalf("expected code %s, got: %v", tc.wantCode, err)
			}
		})
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
