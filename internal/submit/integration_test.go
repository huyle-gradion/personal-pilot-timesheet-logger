package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fakeGradionMCP is a minimal stateful Gradion MCP server used to drive the REAL
// gradion.Client end to end over httptest.
type fakeGradionMCP struct {
	mu      sync.Mutex
	entries []map[string]any
}

func (f *fakeGradionMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req struct {
		ID     *int64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &params)

	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "sess-int")
	writeRes := func(result any) {
		rb, _ := json.Marshal(result)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": json.RawMessage(rb)})
	}

	switch req.Method {
	case "initialize":
		writeRes(map[string]any{"protocolVersion": mcp.ProtocolVersion, "serverInfo": map[string]any{"name": "gradion", "version": "1"}})
	case "tools/list":
		writeRes(map[string]any{"tools": toolDecls()})
	case "tools/call":
		f.mu.Lock()
		defer f.mu.Unlock()
		switch params.Name {
		case gradion.ToolListEntries:
			writeRes(map[string]any{"isError": false, "structuredContent": map[string]any{"entries": f.entries}})
		case gradion.ToolLogTime:
			entry := map[string]any{
				"id":               "int-1",
				"date":             params.Arguments["date"],
				"startTime":        params.Arguments["startTime"],
				"endTime":          params.Arguments["endTime"],
				"description":      params.Arguments["description"],
				"classificationId": params.Arguments["classificationId"],
			}
			f.entries = append(f.entries, entry)
			writeRes(map[string]any{"isError": false, "structuredContent": map[string]any{"entry": entry}})
		default:
			writeRes(map[string]any{"isError": false})
		}
	default:
		writeRes(map[string]any{})
	}
}

func TestExecute_IntegrationRealClientHappyPath(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	server := httptest.NewServer(&fakeGradionMCP{})
	t.Cleanup(server.Close)

	sess, err := mcp.NewSession(mcp.Config{Endpoint: server.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	client := gradion.NewClient(sess)
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := client.VerifyCapabilities(ctx); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}

	_, approval := seedApprovedDay(t, store, reviewedRecord(), completeListing(), alwaysResolver("cls-1"))

	result, err := Execute(ctx, store, client, client, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Blocked || result.Partial {
		t.Fatalf("want a clean run, got blocked=%v partial=%v reasons=%v", result.Blocked, result.Partial, result.BlockReasons)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("want one confirmed outcome, got %+v", result.Outcomes)
	}
	got := loadRecord(t, store, testDate)
	if len(got.RemoteMappings) != 1 || got.RemoteMappings[0].RemoteID != "int-1" {
		t.Fatalf("want a mapping to the real client's remote id, got %+v", got.RemoteMappings)
	}
	if result.RemainingPlan == nil || len(result.RemainingPlan.Operations) != 0 {
		t.Fatalf("want an empty remaining plan (no replay), got %+v", result.RemainingPlan)
	}
}

// --- real-client transport fake (no httptest) for the auth-failure path --------

type stubTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (s *stubTransport) Do(r *http.Request) (*http.Response, error) { return s.fn(r) }

func rpcRequestInfo(r *http.Request) (method string, id int64, tool string) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		ID     *int64 `json:"id"`
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &req)
	if req.ID != nil {
		id = *req.ID
	}
	return req.Method, id, req.Params.Name
}

func jsonRPCResponse(id int64, result any) *http.Response {
	rb, _ := json.Marshal(result)
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": json.RawMessage(rb)})
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}
}

func acceptedResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func toolDecls() []map[string]any {
	names := []string{gradion.ToolListEntries, gradion.ToolLogTime, gradion.ToolEditTime, gradion.ToolGetClassifications}
	decls := make([]map[string]any, 0, len(names))
	for _, n := range names {
		decls = append(decls, map[string]any{"name": n})
	}
	return decls
}

func realClient(t *testing.T, tr mcp.Transport) *gradion.Client {
	t.Helper()
	sess, err := mcp.NewSession(mcp.Config{Endpoint: "http://example.invalid", Token: "test-token", Transport: tr})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c := gradion.NewClient(sess)
	if _, err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := c.VerifyCapabilities(context.Background()); err != nil {
		t.Fatalf("VerifyCapabilities: %v", err)
	}
	return c
}

// forbiddenWriteTransport completes the handshake and reads, but rejects writes
// with HTTP 403 (a definitive rejection).
func forbiddenWriteTransport() mcp.Transport {
	return &stubTransport{fn: func(r *http.Request) (*http.Response, error) {
		method, id, tool := rpcRequestInfo(r)
		switch method {
		case "initialize":
			return jsonRPCResponse(id, map[string]any{"protocolVersion": mcp.ProtocolVersion, "serverInfo": map[string]any{"name": "x", "version": "1"}}), nil
		case "notifications/initialized":
			return acceptedResponse(), nil
		case "tools/list":
			return jsonRPCResponse(id, map[string]any{"tools": toolDecls()}), nil
		case "tools/call":
			switch tool {
			case gradion.ToolLogTime, gradion.ToolEditTime:
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader("forbidden")),
				}, nil
			default:
				return jsonRPCResponse(id, map[string]any{"isError": false, "structuredContent": map[string]any{"entries": []any{}}}), nil
			}
		}
		return jsonRPCResponse(id, map[string]any{}), nil
	}}
}

func TestExecute_AuthFailureIsDefinitiveFailed(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	client := realClient(t, forbiddenWriteTransport())
	_, approval := seedApprovedDay(t, store, reviewedRecord(), completeListing(), alwaysResolver("cls-1"))

	result, err := Execute(ctx, store, client, client, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Status != workflow.OutcomeFailed || !result.Outcomes[0].Resolved {
		t.Fatalf("a 403 must classify as a definitive failed outcome, got %+v", result.Outcomes)
	}
	if !result.Partial {
		t.Errorf("a failed write must mark the run partial")
	}
}
