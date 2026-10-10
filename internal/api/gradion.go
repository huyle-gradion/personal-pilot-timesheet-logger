// Package api is DEPRECATED. It retains only the legacy REST READ surface
// (GetEntries, GetRecentClassifications, NewClient) plus its bounded HTTP
// transport and typed OperatingError helpers. Its REST mutation methods
// (CreateEntry, PatchEntry, DeleteEntry), the slot-equality Reconcile identity,
// ActionDelete, and the reconcile-summary representation were removed as part of
// S5 containment so that no production write path can bypass the later
// approval-bound MCP path (S6/S8). The uncertain-write classification helpers
// below are retained only by the generic transport and are now duplicated by
// internal/gradion, which owns write-outcome classification; do not add mutation
// methods or new write logic here.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Assert terminates the process on violated internal programmer invariants per TigerStyle.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// MaxErrorBodySnippetBytes bounds the response-body preview embedded in
// OperatingError messages, in bytes (TigerStyle). The response body itself is
// already bounded by MaxResponseBodyBytes in readBoundedBody; this is the
// smaller display bound for error strings.
const MaxErrorBodySnippetBytes int = 512

// errorBodySnippet returns a bounded preview of a response body with the literal
// token removed by sanitizeError.
//
// Redaction runs over the FULL body before truncation. Truncating first can cut
// a secret at the 512-byte display boundary, leaving a bare prefix that
// whole-token replacement cannot match and therefore leaking part of the token.
// The display bound is applied to the already-redacted text, so the snippet is
// still bounded and the redaction placeholder may be the only thing clipped.
//
// Redaction is literal only (see sanitizeError): encoded, escaped, or split
// forms of the token are not detected. This snippet is a best-effort preview, not
// a universal secret filter, and must not be treated as guaranteed secret-free.
func errorBodySnippet(body []byte, token string) string {
	sanitized := sanitizeError(errors.New(string(body)), token)
	if len(sanitized) > MaxErrorBodySnippetBytes {
		sanitized = sanitized[:MaxErrorBodySnippetBytes]
	}
	return sanitized
}

// Outcome classifies the result of an API operation per go-rules.md.
type Outcome int

const (
	OutcomeAcknowledged Outcome = iota
	OutcomeRetryable
	OutcomeTerminal
	OutcomeRateLimited
	OutcomeUncertain
)

func (o Outcome) String() string {
	switch o {
	case OutcomeAcknowledged:
		return "OutcomeAcknowledged"
	case OutcomeRetryable:
		return "OutcomeRetryable"
	case OutcomeTerminal:
		return "OutcomeTerminal"
	case OutcomeRateLimited:
		return "OutcomeRateLimited"
	case OutcomeUncertain:
		return "OutcomeUncertain"
	default:
		return fmt.Sprintf("Outcome(%d)", int(o))
	}
}

// OperatingError represents a typed, bounded operating error with a stable code.
type OperatingError struct {
	Outcome Outcome
	Code    string
	Message string
	Err     error
}

// sanitizeError removes the literal bearer token and URL credentials from an
// error message.
//
// Redaction is literal only: it replaces exact occurrences of the token as a
// substring. Encoded, escaped, split, or otherwise transformed forms of the
// token (for example percent-encoded, base64, or JSON-escaped) are NOT detected
// and are NOT redacted. Treat the result as best-effort defense in depth, not as
// a guaranteed secret-free string.
func sanitizeError(err error, token string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "<redacted-token>")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		u, parseErr := url.Parse(urlErr.URL)
		if parseErr == nil {
			u.RawQuery = ""
			u.User = nil
			return fmt.Sprintf("%s %q", urlErr.Op, u.String())
		}
		return fmt.Sprintf("%s <redacted-url>", urlErr.Op)
	}
	return msg
}

func (e *OperatingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *OperatingError) Unwrap() error {
	return e.Err
}

// IsUncertain reports whether err describes an ambiguous non-idempotent write
// whose remote outcome is unknown and must be reconciled before any retry.
//
// DEPRECATED/DUPLICATED: write-outcome classification is owned by
// internal/gradion (over internal/mcp). This helper is retained only because the
// generic transport below still classifies non-idempotent methods; no production
// caller uses it.
func IsUncertain(err error) bool {
	var opErr *OperatingError
	if errors.As(err, &opErr) {
		return opErr.Outcome == OutcomeUncertain
	}
	return false
}

// isIdempotentMethod reports whether method is safe to retry after an
// ambiguous failure (GET/HEAD only per RFC 9110 idempotency semantics).
func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead:
		return true
	default:
		return false
	}
}

// isAmbiguousWriteStatus reports whether status returned for a non-idempotent
// write fails to establish that the mutation was rejected.
//
// RFC 9110 does not make any of these statuses prove that no mutation occurred,
// and Gradion exposes neither an idempotency key nor a conditional-update /
// version guard (see docs/gradion-mcp-verification.md §3 rows 7-8), so a write
// receiving one has an unknown remote outcome and must be reconciled before any
// retry. Reasoning and the unverified server assumptions:
//
//   - 408 Request Timeout: the server may have received and applied the request
//     before timing out. The ambiguity is inherent, and no documented Gradion
//     guarantee proves rejection, so it is treated as uncertain.
//   - 409 Conflict: the status means the request conflicted with current state,
//     but neither RFC 9110 nor any Gradion contract guarantees the server
//     rejected the write atomically without applying anything (no transactional
//     or versioned-update guarantee is documented). It is treated as uncertain
//     for writes. This is deliberately conservative: a wrong "rejected" reading
//     would let later writes create duplicates/overlaps.
//   - 425 Too Early (RFC 8470): the status is defined around anti-replay
//     refusal, but Gradion publishes no replay/idempotency contract, so the
//     "not processed" claim cannot be relied upon and is treated as uncertain.
//
// Definitive 4xx rejections (400/401/403/404/405/413/415/422/428, ...) stay
// OutcomeTerminal because the server rejects the request without applying the
// mutation. Idempotent methods are excluded: a repeated read is harmless.
func isAmbiguousWriteStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly:
		return true
	default:
		return false
	}
}

// uncertainWriteError builds the stable error returned when a non-idempotent
// write (POST/PATCH/DELETE) fails ambiguously and must not be blindly retried.
func uncertainWriteError(method string, detail error) error {
	return &OperatingError{
		Outcome: OutcomeUncertain,
		Code:    "API_ERR_UNCERTAIN_WRITE",
		Message: fmt.Sprintf("%s write outcome unknown; remote state must be re-fetched and reconciled before any retry (never silently resent)", method),
		Err:     detail,
	}
}

// Client communicates with the Gradion Timesheet API service.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	retryCount  int
	backoffBase time.Duration
}

// ClientOption configures optional parameters on the Client.
type ClientOption func(*Client)

// WithHTTPClient overrides the default HTTP client (e.g. for testing with httptest.Server).
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

// WithRetryCount overrides the maximum retry count.
func WithRetryCount(count int) ClientOption {
	return func(c *Client) {
		if count >= 0 {
			c.retryCount = count
		}
	}
}

// WithBackoffBase overrides the initial backoff duration between retries.
func WithBackoffBase(duration time.Duration) ClientOption {
	return func(c *Client) {
		if duration > 0 {
			c.backoffBase = duration
		}
	}
}

// NewClient initializes a Gradion Timesheet API client with TigerStyle bounds.
func NewClient(baseURL, token string, opts ...ClientOption) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)

	Assert(token != "", AssertAPIEmptyToken)
	Assert(baseURL != "", AssertAPIBaseURLEmpty)

	baseURL = strings.TrimRight(baseURL, "/")

	transport := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: false,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   APITimeoutDuration,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	c := &Client{
		baseURL:     baseURL,
		token:       token,
		httpClient:  httpClient,
		retryCount:  MaxAPIRetriesCount,
		backoffBase: BackoffBaseDuration,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// readBoundedBody reads an HTTP response body up to MaxResponseBodyBytes + 1.
func (c *Client) readBoundedBody(body io.ReadCloser) ([]byte, error) {
	defer func() { _ = body.Close() }()

	limited := io.LimitReader(body, MaxResponseBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeRetryable,
			Code:    "API_ERR_READ_BODY",
			Message: "failed to read response body",
			Err:     errors.New(sanitizeError(err, c.token)),
		}
	}
	if int64(len(data)) > MaxResponseBodyBytes {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "API_ERR_RESPONSE_TOO_LARGE",
			Message: fmt.Sprintf("response body exceeded limit of %d bytes", MaxResponseBodyBytes),
		}
	}
	return data, nil
}

// executeRequest performs an HTTP request with exponential backoff on retryable outcomes.
func (c *Client) executeRequest(ctx context.Context, method, endpoint string, reqBody []byte) ([]byte, int, error) {
	Assert(ctx != nil, AssertAPINilContext)
	Assert(c != nil, AssertAPINilClient)

	if err := ctx.Err(); err != nil {
		return nil, 0, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "API_ERR_CONTEXT_CANCELED",
			Message: "context canceled before dispatch",
			Err:     err,
		}
	}

	fullURL := c.baseURL + endpoint

	var lastErr error
	backoff := c.backoffBase

	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, 0, &OperatingError{
					Outcome: OutcomeTerminal,
					Code:    "API_ERR_CONTEXT_CANCELED",
					Message: "context canceled during retry backoff",
					Err:     ctx.Err(),
				}
			case <-time.After(backoff):
				backoff *= 2
			}
		}

		var bodyReader io.Reader
		if len(reqBody) > 0 {
			bodyReader = bytes.NewReader(reqBody)
		}

		httpReq, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, 0, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "API_ERR_CREATE_REQUEST",
				Message: "failed to construct HTTP request",
				Err:     errors.New(sanitizeError(err, c.token)),
			}
		}

		httpReq.Header.Set("Authorization", "Bearer "+c.token)
		httpReq.Header.Set("Accept", "application/json")
		if len(reqBody) > 0 {
			httpReq.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			if !isIdempotentMethod(method) {
				// Non-idempotent write: transport failure is ambiguous; the
				// server may have processed the request. Stop, do not retry.
				return nil, 0, uncertainWriteError(method, errors.New(sanitizeError(err, c.token)))
			}
			lastErr = &OperatingError{
				Outcome: OutcomeRetryable,
				Code:    "API_ERR_NETWORK_TRANSPORT",
				Message: "network transport failure",
				Err:     errors.New(sanitizeError(err, c.token)),
			}
			continue
		}

		bodyBytes, readErr := c.readBoundedBody(resp.Body)
		if readErr != nil {
			if !isIdempotentMethod(method) {
				// Non-idempotent write: we cannot read the outcome; treat as
				// uncertain and stop rather than resend.
				return nil, resp.StatusCode, uncertainWriteError(method, readErr)
			}
			lastErr = readErr
			continue
		}

		// 2xx Success: Acknowledged
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return bodyBytes, resp.StatusCode, nil
		}

		// 429 Rate Limited: Retryable within bounds for idempotent methods.
		// For non-idempotent methods the write outcome is ambiguous.
		if resp.StatusCode == http.StatusTooManyRequests {
			if !isIdempotentMethod(method) {
				return nil, resp.StatusCode, uncertainWriteError(method, nil)
			}
			lastErr = &OperatingError{
				Outcome: OutcomeRateLimited,
				Code:    "API_ERR_RATE_LIMITED",
				Message: "Gradion API rate limit triggered (HTTP 429)",
			}
			continue
		}

		// 5xx Server Error: Retryable within bounds for idempotent methods.
		// For non-idempotent methods the write outcome is ambiguous.
		if resp.StatusCode >= 500 && resp.StatusCode <= 599 {
			if !isIdempotentMethod(method) {
				return nil, resp.StatusCode, uncertainWriteError(method, nil)
			}
			lastErr = &OperatingError{
				Outcome: OutcomeRetryable,
				Code:    "API_ERR_SERVER_ERROR",
				Message: fmt.Sprintf("Gradion API server error (HTTP %d)", resp.StatusCode),
			}
			continue
		}

		// 3xx Redirects: Terminal (redirects disabled per TigerStyle)
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			return nil, resp.StatusCode, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "API_ERR_REDIRECT_NOT_ALLOWED",
				Message: fmt.Sprintf("HTTP redirect %d not followed", resp.StatusCode),
			}
		}

		// Ambiguous 4xx outcomes for non-idempotent writes (408/409/425). These
		// do not prove the mutation was rejected, so the remote outcome is
		// unknown: return OutcomeUncertain and never auto-retry. See
		// isAmbiguousWriteStatus for the reasoning and unverified assumptions.
		if !isIdempotentMethod(method) && isAmbiguousWriteStatus(resp.StatusCode) {
			return nil, resp.StatusCode, uncertainWriteError(method, nil)
		}

		// 4xx Client Errors: Terminal
		code := "API_ERR_CLIENT_ERROR"
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			code = "API_ERR_UNAUTHORIZED"
		case http.StatusForbidden:
			code = "API_ERR_FORBIDDEN"
		case http.StatusNotFound:
			code = "API_ERR_NOT_FOUND"
		case http.StatusBadRequest:
			code = "API_ERR_BAD_REQUEST"
		}

		return nil, resp.StatusCode, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    code,
			Message: fmt.Sprintf("Gradion API returned HTTP %d: %s", resp.StatusCode, errorBodySnippet(bodyBytes, c.token)),
		}
	}

	return nil, 0, &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "API_ERR_RETRIES_EXHAUSTED",
		Message: fmt.Sprintf("request exhausted %d retries: %v", c.retryCount, lastErr),
		Err:     lastErr,
	}
}

// GetEntries queries existing entries on the Gradion Timesheet service for the target date.
func (c *Client) GetEntries(ctx context.Context, date string) ([]RemoteEntry, error) {
	Assert(ctx != nil, AssertAPINilContext)
	Assert(c != nil, AssertAPINilClient)

	date = strings.TrimSpace(date)
	Assert(date != "", AssertAPIZeroDate)

	endpoint := fmt.Sprintf("/api/entries?limit=%d&offset=0&dateFrom=%s&dateTo=%s",
		MaxRemoteEntriesCount, url.QueryEscape(date), url.QueryEscape(date))

	body, _, err := c.executeRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	// Try unmarshaling as envelope object {"total": N, "entries": [...]}
	var envelope RemoteEntriesResponse
	if envErr := json.Unmarshal(body, &envelope); envErr == nil && envelope.Entries != nil {
		if len(envelope.Entries) > MaxRemoteEntriesCount {
			return nil, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "API_ERR_CAPACITY_EXCEEDED",
				Message: fmt.Sprintf("remote entries count %d exceeds maximum allowed %d", len(envelope.Entries), MaxRemoteEntriesCount),
			}
		}
		return envelope.Entries, nil
	}

	// Try unmarshaling as direct array []RemoteEntry
	var list []RemoteEntry
	if listErr := json.Unmarshal(body, &list); listErr == nil {
		if len(list) > MaxRemoteEntriesCount {
			return nil, &OperatingError{
				Outcome: OutcomeTerminal,
				Code:    "API_ERR_CAPACITY_EXCEEDED",
				Message: fmt.Sprintf("remote entries count %d exceeds maximum allowed %d", len(list), MaxRemoteEntriesCount),
			}
		}
		return list, nil
	}

	return nil, &OperatingError{
		Outcome: OutcomeTerminal,
		Code:    "API_ERR_SCHEMA_MISMATCH",
		Message: "failed to parse remote entries response",
	}
}

// GetRecentClassifications queries GET /api/classifications/recent.
func (c *Client) GetRecentClassifications(ctx context.Context) ([]Classification, error) {
	Assert(ctx != nil, AssertAPINilContext)
	Assert(c != nil, AssertAPINilClient)

	body, _, err := c.executeRequest(ctx, http.MethodGet, "/api/classifications/recent", nil)
	if err != nil {
		return nil, err
	}

	var classifications []Classification
	if err := json.Unmarshal(body, &classifications); err != nil {
		return nil, &OperatingError{
			Outcome: OutcomeTerminal,
			Code:    "API_ERR_SCHEMA_MISMATCH",
			Message: "failed to parse classifications response",
			Err:     err,
		}
	}

	return classifications, nil
}
