package gradion

import (
	"encoding/json"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
)

// envelope builds a {"resources":[...]} write response.
func envelope(t *testing.T, items []map[string]any) mcp.CallResult {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"resources": items})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return mcp.CallResult{Structured: payload}
}

// The live server answers log_time/edit_time with a "resources" envelope whose
// element carries only the entry IDENTITY, not its interval. The decoder must
// acknowledge that identity (RemoteID) WITHOUT fabricating content (Entry nil).
func TestParseWriteEntry_ResourcesEnvelopeIsIdentityOnly(t *testing.T) {
	res := envelope(t, []map[string]any{{"kind": "entry", "id": "2622aba2-aed0-47c4-87a9-6be83f0f0c9d", "label": "Entry 2026-10-06"}})

	ack, ok := parseWriteEntry(res)
	if !ok {
		t.Fatal("a single entry resource must be acknowledged")
	}
	if ack.RemoteID != "2622aba2-aed0-47c4-87a9-6be83f0f0c9d" {
		t.Fatalf("remote id = %q", ack.RemoteID)
	}
	if ack.Entry != nil {
		t.Fatalf("a reference envelope must NOT establish content, got Entry=%+v", ack.Entry)
	}
}

// A multi-entry envelope must fail closed: taking the FIRST entry resource would
// confirm the wrong resource.
func TestParseWriteEntry_MultiEntryEnvelopeFailsClosed(t *testing.T) {
	res := envelope(t, []map[string]any{
		{"kind": "entry", "id": "11111111-1111-1111-1111-111111111111"},
		{"kind": "entry", "id": "22222222-2222-2222-2222-222222222222"},
	})
	if _, ok := parseWriteEntry(res); ok {
		t.Fatal("an envelope with more than one entry resource must fail closed")
	}
}

// An attachment plus a single entry resource yields exactly the entry.
func TestParseWriteEntry_AttachmentAndEntry(t *testing.T) {
	res := envelope(t, []map[string]any{
		{"kind": "attachment", "id": "att-1"},
		{"kind": "Entry", "id": "entry-1"},
	})
	ack, ok := parseWriteEntry(res)
	if !ok || ack.RemoteID != "entry-1" || ack.Entry != nil {
		t.Fatalf("want the single entry id (kind case-insensitive), got ok=%v ack=%+v", ok, ack)
	}
}

// An entry resource with an empty id is malformed: a reference must identify.
func TestParseWriteEntry_EntryWithEmptyIDFailsClosed(t *testing.T) {
	res := envelope(t, []map[string]any{{"kind": "entry", "id": "  "}})
	if _, ok := parseWriteEntry(res); ok {
		t.Fatal("an entry resource with an empty id must fail closed")
	}
}

// An empty resources list is NOT an acknowledged entry.
func TestParseWriteEntry_EmptyResourcesFailsClosed(t *testing.T) {
	res := mcp.CallResult{Structured: json.RawMessage(`{"resources":[]}`)}
	if _, ok := parseWriteEntry(res); ok {
		t.Fatal("an empty resources list must fail closed")
	}
}

// Malformed resources (a non-array, or bad JSON) must fail closed.
func TestParseWriteEntry_MalformedResourcesFailsClosed(t *testing.T) {
	cases := map[string]string{
		"object":   `{"resources":{}}`,
		"bad json": `{"resources":[`,
		"null":     `{"resources":null}`,
	}
	for name, body := range cases {
		if _, ok := parseWriteEntry(mcp.CallResult{Structured: json.RawMessage(body)}); ok {
			t.Errorf("%s: malformed resources must fail closed", name)
		}
	}
}

// A complete structured response establishes BOTH identity and content.
func TestParseWriteEntry_CompleteStructuredResponse(t *testing.T) {
	body := `{"entry":{"id":"uuid-1","date":"2026-10-06","startTime":"09:00","endTime":"10:00","classificationId":"c1","description":"work"}}`
	ack, ok := parseWriteEntry(mcp.CallResult{Structured: json.RawMessage(body)})
	if !ok || ack.Entry == nil || ack.RemoteID != "uuid-1" {
		t.Fatalf("a complete structured response must populate Entry, got ok=%v ack=%+v", ok, ack)
	}
	if ack.Entry.Description != "work" {
		t.Errorf("observed content lost: %+v", ack.Entry)
	}
}

// F3: an object with an id but NO interval content (a missing start or end) is
// identity-only, never a content observation. Both the bare and {entry} shapes
// must yield {RemoteID, nil Entry}.
func TestParseWriteEntry_IdOnlyIsIdentityOnly(t *testing.T) {
	cases := map[string]string{
		"bare id-only":          `{"id":"x-1"}`,
		"bare id+start only":    `{"id":"x-2","startTime":"09:00"}`,
		"bare id+end only":      `{"id":"x-3","endTime":"10:00"}`,
		"wrapped id-only":       `{"entry":{"id":"x-4"}}`,
		"wrapped id+start only": `{"entry":{"id":"x-5","startTime":"09:00"}}`,
		"wrapped id+end only":   `{"entry":{"id":"x-6","endTime":"10:00"}}`,
	}
	for name, body := range cases {
		ack, ok := parseWriteEntry(mcp.CallResult{Structured: json.RawMessage(body)})
		if !ok {
			t.Errorf("%s: expected an identity acknowledgement, got ok=false", name)
			continue
		}
		if ack.Entry != nil {
			t.Errorf("%s: id-only must not establish content, got Entry=%+v", name, ack.Entry)
		}
		if ack.RemoteID == "" {
			t.Errorf("%s: remote id must be set", name)
		}
	}
}

// The previously supported shapes must keep working, and an unrecognized payload
// must still fail closed.
func TestParseWriteEntry_LegacyShapes(t *testing.T) {
	bare, _ := json.Marshal(map[string]any{"id": "a-1", "startTime": "09:00", "endTime": "10:00"})
	if ack, ok := parseWriteEntry(mcp.CallResult{Text: string(bare)}); !ok || ack.Entry == nil || ack.RemoteID != "a-1" {
		t.Fatalf("bare complete object: ok=%v ack=%+v", ok, ack)
	}
	wrapped, _ := json.Marshal(map[string]any{"entry": map[string]any{"id": "b-2"}})
	if ack, ok := parseWriteEntry(mcp.CallResult{Text: string(wrapped)}); !ok || ack.Entry != nil || ack.RemoteID != "b-2" {
		t.Fatalf("wrapped id-only object must be identity-only: ok=%v ack=%+v", ok, ack)
	}
	if _, ok := parseWriteEntry(mcp.CallResult{Text: "Entry created."}); ok {
		t.Fatal("unrecognized shape must fail closed")
	}
	if _, ok := parseWriteEntry(mcp.CallResult{}); ok {
		t.Fatal("an empty payload must fail closed")
	}
}
