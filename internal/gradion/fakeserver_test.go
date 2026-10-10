package gradion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
)

type recordedReq struct {
	Method    string
	Tool      string
	Args      map[string]any
	SessionID string
	Auth      string
}

type callOutcome struct {
	IsError    bool
	Structured json.RawMessage
	Text       string
}

// fakeMCP is a minimal stateful Gradion MCP server for hermetic tests.
type fakeMCP struct {
	mu       sync.Mutex
	requests []recordedReq
	tools    []string
	onCall   func(name string, args map[string]any) callOutcome
}

func newFakeMCP() *fakeMCP {
	return &fakeMCP{tools: []string{ToolListEntries, ToolLogTime, ToolEditTime, ToolGetClassifications}}
}

func (f *fakeMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      *int64          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &params)

	f.mu.Lock()
	f.requests = append(f.requests, recordedReq{
		Method: req.Method, Tool: params.Name, Args: params.Arguments,
		SessionID: r.Header.Get("Mcp-Session-Id"), Auth: r.Header.Get("Authorization"),
	})
	onCall := f.onCall
	tools := append([]string(nil), f.tools...)
	f.mu.Unlock()

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "sess-1")

	switch req.Method {
	case "initialize":
		writeResult(w, req.ID, map[string]any{
			"protocolVersion": mcp.ProtocolVersion,
			"serverInfo":      map[string]any{"name": "gradion", "version": "1"},
		})
	case "tools/list":
		decls := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			decls = append(decls, map[string]any{"name": t})
		}
		writeResult(w, req.ID, map[string]any{"tools": decls})
	case "tools/call":
		if onCall == nil {
			writeResult(w, req.ID, map[string]any{})
			return
		}
		out := onCall(params.Name, params.Arguments)
		result := map[string]any{"isError": out.IsError}
		if len(out.Structured) > 0 {
			result["structuredContent"] = out.Structured
		}
		if out.Text != "" {
			result["content"] = []map[string]any{{"type": "text", "text": out.Text}}
		}
		writeResult(w, req.ID, result)
	default:
		writeResult(w, req.ID, map[string]any{})
	}
}

func writeResult(w http.ResponseWriter, id *int64, result any) {
	rb, _ := json.Marshal(result)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      *id,
		"result":  json.RawMessage(rb),
	})
}

func (f *fakeMCP) countToolCalls(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.Method == "tools/call" && r.Tool == name {
			n++
		}
	}
	return n
}

func (f *fakeMCP) countMethod(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.Method == method {
			n++
		}
	}
	return n
}

// fakeTransport injects faults without real HTTP.
type fakeTransport struct {
	mu      sync.Mutex
	methods []string
	fn      func(*http.Request) (*http.Response, error)
}

func (t *fakeTransport) Do(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.methods = append(t.methods, requestMethod(r))
	t.mu.Unlock()
	return t.fn(r)
}

func (t *fakeTransport) countMethod(method string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, m := range t.methods {
		if m == method {
			n++
		}
	}
	return n
}

func requestMethod(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	var req struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Method
}

func resp(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// requiredToolsJSON returns a tools/list result declaring every required tool.
func requiredToolsJSON() string {
	decls := make([]map[string]any, 0, 4)
	for _, t := range []string{ToolListEntries, ToolLogTime, ToolEditTime, ToolGetClassifications} {
		decls = append(decls, map[string]any{"name": t})
	}
	rb, _ := json.Marshal(map[string]any{"tools": decls})
	return string(rb)
}

// methodAwareTransport completes the handshake + tools/list and delegates
// tools/call.
func methodAwareTransport(onCall func(*http.Request) (*http.Response, error)) *fakeTransport {
	return &fakeTransport{fn: func(r *http.Request) (*http.Response, error) {
		switch requestMethod(r) {
		case "initialize":
			return resp(200, "application/json", `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"`+mcp.ProtocolVersion+`","serverInfo":{"name":"x","version":"1"}}}`), nil
		case "notifications/initialized":
			return resp(http.StatusAccepted, "application/json", ""), nil
		case "tools/list":
			return resp(200, "application/json", `{"jsonrpc":"2.0","id":2,"result":`+requiredToolsJSON()+`}`), nil
		default:
			return onCall(r)
		}
	}}
}

// statusTransport returns a fixed HTTP status for tools/call.
func statusTransport(status int) *fakeTransport {
	return methodAwareTransport(func(*http.Request) (*http.Response, error) {
		return resp(status, "application/json", "error body"), nil
	})
}

// rpcErrorTransport returns a JSON-RPC error (echoing the request id) for
// tools/call.
func rpcErrorTransport(code int) *fakeTransport {
	return methodAwareTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID *int64 `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		id := int64(0)
		if req.ID != nil {
			id = *req.ID
		}
		return resp(200, "application/json",
			fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":"bad"}}`, id, code)), nil
	})
}

// callResp returns a successful tools/call response with the given listing rows
// (echoing the request id).
func callResp(r *http.Request, entries []map[string]any) (*http.Response, error) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID *int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &req)
	id := int64(0)
	if req.ID != nil {
		id = *req.ID
	}
	sc, _ := json.Marshal(map[string]any{"entries": entries})
	result := map[string]any{"isError": false, "structuredContent": json.RawMessage(sc)}
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	return resp(200, "application/json", string(payload)), nil
}

// flakyListTransport fails the first failTimes tools/call requests with failStatus,
// then returns an empty listing.
func flakyListTransport(failStatus, failTimes int) *fakeTransport {
	n := 0
	return methodAwareTransport(func(r *http.Request) (*http.Response, error) {
		n++
		if n <= failTimes {
			return resp(failStatus, "application/json", "err"), nil
		}
		return callResp(r, nil)
	})
}

func newTestClient(t *testing.T, f *fakeMCP) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	sess, err := mcp.NewSession(mcp.Config{Endpoint: srv.URL, Token: "test-token"})
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
	return c
}

func newTestClientWithTransport(t *testing.T, tr mcp.Transport) *Client {
	t.Helper()
	sess, err := mcp.NewSession(mcp.Config{Endpoint: "http://example.invalid", Token: "test-token", Transport: tr})
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
	return c
}
