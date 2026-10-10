package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInitialize_CapturesSessionIDAndSendsNotification(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)

	info, err := sess.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if info.Name != "fake" || info.Version != "1" || info.ProtocolVersion != ProtocolVersion {
		t.Errorf("unexpected server info: %+v", info)
	}
	if sess.SessionID() != "sess-123" {
		t.Errorf("session id = %q, want sess-123", sess.SessionID())
	}

	reqs := f.recorded()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (initialize + notifications/initialized)", len(reqs))
	}
	if reqs[0].Method != "initialize" || reqs[0].SessionID != "" {
		t.Errorf("initialize request wrong: %+v", reqs[0])
	}
	if reqs[1].Method != "notifications/initialized" {
		t.Errorf("second request = %q, want notifications/initialized", reqs[1].Method)
	}
	if reqs[1].SessionID != "sess-123" {
		t.Errorf("post-init notification missing session header: %+v", reqs[1])
	}
	if reqs[1].HasID {
		t.Error("notifications/initialized must be a JSON-RPC notification (no id)")
	}
	if reqs[0].Auth != "Bearer test-token" {
		t.Errorf("authorization header = %q", reqs[0].Auth)
	}
}

func TestListTools_ParsedAndCarriesSessionHeader(t *testing.T) {
	f := newFakeMCP()
	f.tools = []ToolDecl{{Name: "a", Description: "first"}, {Name: "b"}}
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" {
		t.Errorf("tools = %+v", tools)
	}

	reqs := f.recorded()
	last := reqs[len(reqs)-1]
	if last.Method != "tools/list" || last.SessionID != "sess-123" {
		t.Errorf("tools/list request wrong: %+v", last)
	}
}

func TestListTools_CapExceeded(t *testing.T) {
	f := newFakeMCP()
	f.tools = make([]ToolDecl, MaxToolsCount+1)
	for i := range f.tools {
		f.tools[i] = ToolDecl{Name: "t"}
	}
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrSchema) || StableCodeOf(err) != CodeTooManyTools {
		t.Fatalf("expected ErrSchema/%s, got %v", CodeTooManyTools, err)
	}
}

func TestCallBeforeInit_Uninitialized(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)

	if _, err := sess.ListTools(context.Background()); !IsUninitialized(err) {
		t.Errorf("ListTools before init = %v, want ErrUninitialized", err)
	}
	if _, err := sess.CallTool(context.Background(), "x", nil); !IsUninitialized(err) {
		t.Errorf("CallTool before init = %v, want ErrUninitialized", err)
	}
	if reqs := f.recorded(); len(reqs) != 0 {
		t.Errorf("pre-init calls must send ZERO requests, got %d: %+v", len(reqs), reqs)
	}
}

// TestJSONRPCErrorMessage_RedactBeforeBound proves the JSON-RPC error path
// redacts the token BEFORE bounding the message, so a token straddling the byte
// cap cannot leave a bare prefix (S5L1-1).
func TestJSONRPCErrorMessage_RedactBeforeBound(t *testing.T) {
	const token = "S3CR3T-TOKEN-DO-NOT-LEAK"
	// 250 filler bytes + the token: the token straddles the 256-byte cap.
	msg := strings.Repeat("A", 250) + token

	f := newFakeMCP()
	f.rpcErr = &rpcError{Code: -32000, Message: msg}
	sess, _ := newServerSession(t, f)
	sess.cfg.Token = token
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := sess.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("full token leaked: %v", err)
	}
	for k := 4; k <= len(token); k++ {
		if strings.Contains(err.Error(), token[:k]) {
			t.Errorf("token prefix %q leaked: %v", token[:k], err)
		}
	}
}

// TestProtocolVersionMismatch_Bounded proves the server-supplied protocol
// version is bounded before formatting (S5L1-2).
func TestProtocolVersionMismatch_Bounded(t *testing.T) {
	f := newFakeMCP()
	f.protocolVersion = strings.Repeat("v", 1000)
	sess, _ := newServerSession(t, f)

	_, err := sess.Initialize(context.Background())
	if !IsKind(err, ErrProtocol) || StableCodeOf(err) != CodeProtocolVersion {
		t.Fatalf("expected ErrProtocol/%s, got %v", CodeProtocolVersion, err)
	}
	if len(err.Error()) > 400 {
		t.Errorf("mismatch message not bounded: len=%d", len(err.Error()))
	}
}

// TestPostInitOmitsSessionHeaderWhenUnassigned proves post-init requests omit the
// session header when the server assigned no session id (S5L1-6a).
func TestPostInitOmitsSessionHeaderWhenUnassigned(t *testing.T) {
	f := newFakeMCP()
	f.omitSessionID = true
	sess, _ := newServerSession(t, f)

	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if sess.SessionID() != "" {
		t.Errorf("expected no captured session id, got %q", sess.SessionID())
	}
	if _, err := sess.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	reqs := f.recorded()
	last := reqs[len(reqs)-1]
	if last.Method != "tools/list" || last.SessionID != "" {
		t.Errorf("expected no session header on post-init request, got %+v", last)
	}
}

// Test404SessionExpired proves a post-init 404 maps to MCP_ERR_SESSION_EXPIRED
// (S5L1-6c).
func Test404SessionExpired(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceStatus = http.StatusNotFound
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrHTTPStatus) || StableCodeOf(err) != CodeSessionExpired {
		t.Fatalf("expected ErrHTTPStatus/%s, got %v", CodeSessionExpired, err)
	}
}

// TestResponseIDMismatch proves a response whose id does not match the request is
// rejected as a protocol error (S5L1-8).
func TestResponseIDMismatch(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceBody = `{"jsonrpc":"2.0","id":999,"result":{"tools":[]}}`
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrProtocol) || StableCodeOf(err) != CodeProtocol {
		t.Fatalf("expected ErrProtocol/%s, got %v", CodeProtocol, err)
	}
}

// TestResponseJSONRPCVersionInvalid proves a response with a bad jsonrpc version
// is rejected as a protocol error (S5L1-8).
func TestResponseJSONRPCVersionInvalid(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceBody = `{"jsonrpc":"1.0","id":2,"result":{"tools":[]}}`
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrProtocol) || StableCodeOf(err) != CodeProtocol {
		t.Fatalf("expected ErrProtocol/%s, got %v", CodeProtocol, err)
	}
}

func TestProtocolVersionMismatch(t *testing.T) {
	f := newFakeMCP()
	f.protocolVersion = "1999-01-01"
	sess, _ := newServerSession(t, f)

	_, err := sess.Initialize(context.Background())
	if !IsKind(err, ErrProtocol) || StableCodeOf(err) != CodeProtocolVersion {
		t.Fatalf("expected ErrProtocol/%s, got %v", CodeProtocolVersion, err)
	}
}

func TestCallTool_ToolLevelError(t *testing.T) {
	f := newFakeMCP()
	f.callResultRaw = json.RawMessage(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`)
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := sess.CallTool(context.Background(), "timesheet__log_time", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Error("tool-level isError not surfaced")
	}
	if res.Text != "boom" {
		t.Errorf("text = %q, want boom", res.Text)
	}
}

func TestCallTool_JSONRPCError(t *testing.T) {
	f := newFakeMCP()
	f.rpcErr = &rpcError{Code: -32601, Message: "method not found"}
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := sess.CallTool(context.Background(), "nope", nil)
	if !IsKind(err, ErrProtocol) {
		t.Fatalf("expected ErrProtocol, got %v", err)
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != -32601 {
		t.Errorf("JSON-RPC code not surfaced: %v", err)
	}
}

func TestCallTool_StructuredPreferred(t *testing.T) {
	f := newFakeMCP()
	f.callResultRaw = json.RawMessage(`{"structuredContent":{"total":3},"content":[{"type":"text","text":"ignored"}]}`)
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := sess.CallTool(context.Background(), "t", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Structured) == 0 {
		t.Error("structuredContent not surfaced")
	}
	if res.Text != "" {
		t.Errorf("text should be empty when structuredContent is present, got %q", res.Text)
	}
}

func TestCallTool_TextFallback(t *testing.T) {
	f := newFakeMCP()
	f.callResultRaw = json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`)
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	res, err := sess.CallTool(context.Background(), "t", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text != "hello" || len(res.Structured) != 0 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestCallTool_NeitherOutput(t *testing.T) {
	f := newFakeMCP()
	f.callResultRaw = json.RawMessage(`{"content":[]}`)
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	_, err := sess.CallTool(context.Background(), "t", nil)
	if !IsKind(err, ErrSchema) || StableCodeOf(err) != CodeNoToolOutput {
		t.Fatalf("expected ErrSchema/%s, got %v", CodeNoToolOutput, err)
	}
}

func TestResponseTooLarge(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	sess.cfg.MaxResponseBytes = 200
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceBody = strings.Repeat("x", 1000)
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrSchema) || StableCodeOf(err) != CodeResponseTooLarge {
		t.Fatalf("expected ErrSchema/%s, got %v", CodeResponseTooLarge, err)
	}
}

func TestMalformedJSON(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceBody = "not valid json"
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrSchema) || StableCodeOf(err) != CodeSchema {
		t.Fatalf("expected ErrSchema/%s, got %v", CodeSchema, err)
	}
}

func TestSSEContentTypeFailsClosed(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	f.mu.Lock()
	f.forceCT = "text/event-stream"
	f.forceBody = "data: {}\n\n"
	f.mu.Unlock()

	_, err := sess.ListTools(context.Background())
	if !IsKind(err, ErrProtocol) || StableCodeOf(err) != CodeSSEUnsupported {
		t.Fatalf("expected ErrProtocol/%s, got %v", CodeSSEUnsupported, err)
	}
}

func TestTimeoutIsTyped(t *testing.T) {
	tr := &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}}
	sess := newTestSession(t, Config{Endpoint: "http://example.invalid", Token: "t", Timeout: 20 * time.Millisecond, Transport: tr})

	_, err := sess.Initialize(context.Background())
	if !IsKind(err, ErrTransport) || StableCodeOf(err) != CodeTimeout {
		t.Fatalf("expected ErrTransport/%s, got %v", CodeTimeout, err)
	}
}

func TestTransportResetIsTyped(t *testing.T) {
	tr := &fakeTransport{fn: func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset by peer")
	}}
	sess := newTestSession(t, Config{Endpoint: "http://example.invalid", Token: "t", Transport: tr})

	_, err := sess.Initialize(context.Background())
	if !IsKind(err, ErrTransport) || StableCodeOf(err) != CodeTransport {
		t.Fatalf("expected ErrTransport/%s, got %v", CodeTransport, err)
	}
}

func TestCallTool_ExactlyOneAttempt(t *testing.T) {
	tr := methodAwareTransport(func(method string, _ *http.Request) (*http.Response, error) {
		return resp(http.StatusInternalServerError, "application/json", "boom"), nil
	})
	sess := newTestSession(t, Config{Endpoint: "http://example.invalid", Token: "t", Transport: tr})
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if _, err := sess.CallTool(context.Background(), "t", nil); err == nil {
		t.Fatal("expected a 5xx error")
	}
	if got := tr.countMethod("tools/call"); got != 1 {
		t.Errorf("tools/call attempts = %d, want exactly 1 (no retry)", got)
	}
}

func TestTokenNeverLeaks(t *testing.T) {
	const token = "S3CR3T-TOKEN-DO-NOT-LEAK"

	// HTTP-status error body contains the token.
	tr := methodAwareTransport(func(string, *http.Request) (*http.Response, error) {
		return resp(http.StatusInternalServerError, "application/json", "leaked "+token), nil
	})
	sess := newTestSession(t, Config{Endpoint: "http://example.invalid", Token: token, Transport: tr})
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err := sess.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token leaked into HTTP-status error: %v", err)
	}

	// JSON-RPC error message contains the token.
	f := newFakeMCP()
	f.rpcErr = &rpcError{Code: -32000, Message: "bad " + token}
	sess2, _ := newServerSession(t, f)
	sess2.cfg.Token = token
	if _, err := sess2.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	_, err = sess2.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("token leaked into JSON-RPC error: %v", err)
	}

	// Serialized config/session never contain the token.
	cfgData, _ := json.Marshal(Config{Endpoint: "http://example.invalid", Token: token})
	if strings.Contains(string(cfgData), token) {
		t.Errorf("token leaked into serialized Config: %s", cfgData)
	}
	sessData, _ := json.Marshal(sess)
	if strings.Contains(string(sessData), token) {
		t.Errorf("token leaked into serialized Session: %s", sessData)
	}
}

func TestClose_IdempotentAndBlocks(t *testing.T) {
	f := newFakeMCP()
	sess, _ := newServerSession(t, f)
	if _, err := sess.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := sess.ListTools(context.Background()); StableCodeOf(err) != CodeSessionClosed {
		t.Errorf("expected %s after Close, got %v", CodeSessionClosed, err)
	}
}

func TestNewSession_RejectsEmptyEndpoint(t *testing.T) {
	if _, err := NewSession(Config{}); err == nil {
		t.Error("expected an error for an empty endpoint")
	}
}
