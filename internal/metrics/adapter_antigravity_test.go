package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAntigravityAdapter_ObservedMapping asserts the five-scalar Detect, OBSERVED
// provenance, and the exact counter mapping of the synthetic transcript. The
// three known components are recorded; cache-write and reasoning are UNKNOWN, so
// the total and the cache-hit rate stay UNKNOWN (never derived from the known
// components).
func TestAntigravityAdapter_ObservedMapping(t *testing.T) {
	data := readTestdata(t, "antigravity_synthetic.jsonl")
	a := antigravityAdapter{}
	if !a.Detect(firstNonEmptyLine(data)) {
		t.Fatal("antigravity Detect did not match the header")
	}
	if a.Name() != "antigravity" {
		t.Fatalf("name = %q, want antigravity", a.Name())
	}
	if a.Provenance() != ProvenanceObserved {
		t.Fatalf("provenance = %q, want observed", a.Provenance())
	}
	rec := parseWith(t, a, data)

	if rec.Provider != "antigravity" || rec.Provenance != ProvenanceObserved {
		t.Fatalf("run provider/provenance = %q/%q, want antigravity/observed", rec.Provider, rec.Provenance)
	}
	if len(rec.Calls) != 3 {
		t.Fatalf("calls = %d, want 3 (one per usage-bearing line)", len(rec.Calls))
	}
	assertKnown(t, "input", rec.Usage.Input, 2740)
	assertKnown(t, "output", rec.Usage.Output, 730)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 1550)
	if rec.Usage.CacheWrite.Known {
		t.Fatalf("cacheWrite = %+v, want UNKNOWN (no cache-write counter)", rec.Usage.CacheWrite)
	}
	if rec.Usage.Reasoning.Known {
		t.Fatalf("reasoning = %+v, want UNKNOWN (thinking is text, not a counter)", rec.Usage.Reasoning)
	}
	if rec.Usage.ConsumedTokens().Known {
		t.Fatalf("consumed tokens = %+v, want UNKNOWN (one component is unknown)", rec.Usage.ConsumedTokens())
	}
	if pct := CacheHitPercent(rec.Usage); pct != nil {
		t.Fatalf("cache-hit = %v, want nil (no cache-write counter)", *pct)
	}

	for i, c := range rec.Calls {
		if c.Step != StepUnknown || c.Category != CategoryUnassigned {
			t.Fatalf("call %d step/category = %q/%q, want unknown/unassigned", i, c.Step, c.Category)
		}
		if c.Model != "" || c.SessionID != "" {
			t.Fatalf("call %d model/session = %q/%q, want empty", i, c.Model, c.SessionID)
		}
		if c.Provenance != ProvenanceObserved {
			t.Fatalf("call %d provenance = %q, want observed", i, c.Provenance)
		}
		if c.ContextLength.Known {
			t.Fatalf("call %d context length = %+v, want UNKNOWN", i, c.ContextLength)
		}
		if c.Cost != nil {
			t.Fatalf("call %d cost = %+v, want nil", i, c.Cost)
		}
		if c.Timestamp.IsZero() {
			t.Fatalf("call %d timestamp is zero", i)
		}
	}
	if len(rec.Steps) != 0 {
		t.Fatalf("steps = %d, want 0 (no derivable step)", len(rec.Steps))
	}
	if rec.Model != "" || rec.SessionID != "" {
		t.Fatalf("run model/session = %q/%q, want empty", rec.Model, rec.SessionID)
	}
	if rec.State != RunUnknown {
		t.Fatalf("state = %q, want unknown (complete transcript, steps empty => rule 5)", rec.State)
	}

	// `thinking`, `content`, tool-call names/args and paths are never decoded.
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, banned := range []string{
		"INVENTED-PROMPT-TEXT", "INVENTED-REPLY-TEXT", "INVENTED-PLAN-TEXT",
		"INVENTED-THINKING-TEXT", "INVENTED-PLAN-THINKING", "INVENTED-FINAL-THINKING",
		"/invented/notes.md", "view_file",
	} {
		if bytes.Contains(raw, []byte(banned)) {
			t.Fatalf("marshaled RunRecord leaked %q", banned)
		}
	}
}

// TestAntigravityAdapter_FullVariant asserts the transcript_full variant (same
// shape, no truncated_fields) parses to the same counters.
func TestAntigravityAdapter_FullVariant(t *testing.T) {
	data := readTestdata(t, "antigravity_synthetic_full.jsonl")
	if !(antigravityAdapter{}).Detect(firstNonEmptyLine(data)) {
		t.Fatal("antigravity Detect did not match the full variant header")
	}
	rec := parseWith(t, antigravityAdapter{}, data)
	if len(rec.Calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(rec.Calls))
	}
	assertKnown(t, "input", rec.Usage.Input, 2740)
	assertKnown(t, "output", rec.Usage.Output, 730)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 1550)
}

// TestAntigravityAdapter_TruncatedCounterDowngraded asserts the trust marker: a
// named counter is downgraded to UNKNOWN while the line is still counted, and a
// text-only marker (the documented real shape) leaves the counters trusted.
func TestAntigravityAdapter_TruncatedCounterDowngraded(t *testing.T) {
	const namedCounter = `{"step_index":7,"source":"MODEL","type":"GENERIC","created_at":"2026-10-16T09:02:00Z","input_tokens":100,"output_tokens":20,"cache_read_tokens":10,"truncated_fields":["content","input_tokens"]}`
	rec := parseWith(t, antigravityAdapter{}, []byte(namedCounter))
	if len(rec.Calls) != 1 {
		t.Fatalf("calls = %d, want 1 (the line is still counted)", len(rec.Calls))
	}
	if rec.Usage.Input.Known {
		t.Fatalf("input = %+v, want UNKNOWN (named by truncated_fields)", rec.Usage.Input)
	}
	assertKnown(t, "output", rec.Usage.Output, 20)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 10)

	const textOnly = `{"step_index":8,"source":"MODEL","type":"GENERIC","created_at":"2026-10-16T09:02:05Z","input_tokens":100,"output_tokens":20,"cache_read_tokens":10,"truncated_fields":["content","thinking"]}`
	rec = parseWith(t, antigravityAdapter{}, []byte(textOnly))
	if len(rec.Calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(rec.Calls))
	}
	assertKnown(t, "input", rec.Usage.Input, 100)
	assertKnown(t, "output", rec.Usage.Output, 20)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 10)
}

// TestAntigravityAdapter_EndsOnUserInputIsCancelled asserts a transcript whose
// final line is USER_INPUT reads cancelled: it stopped after the user's turn,
// which is a true observation rather than a default.
func TestAntigravityAdapter_EndsOnUserInputIsCancelled(t *testing.T) {
	data := readTestdata(t, "antigravity_synthetic.jsonl")
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatal("fixture too short")
	}
	// Drop the final MODEL line so the last line is USER_INPUT.
	trimmed := []byte(strings.Join(lines[:len(lines)-1], "\n"))
	rec := parseWith(t, antigravityAdapter{}, trimmed)
	if len(rec.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(rec.Calls))
	}
	if rec.State != RunCancelled {
		t.Fatalf("state = %q, want cancelled (the record ends on USER_INPUT)", rec.State)
	}
}

// TestImport_AutoUnknownProviderStillFailsClosed asserts `auto` keeps its
// fail-closed unknown-provider behaviour now that the R6 arm imports
// antigravity-shaped content.
func TestImport_AutoUnknownProviderStillFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unknown.jsonl")
	if err := os.WriteFile(path, []byte(`{"hello":"world"}`+"\nnot json\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Import(context.Background(), path, ImportOptions{Date: "2026-10-16"})
	if !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("error = %v, want ErrUnknownProvider", err)
	}
}

// TestAntigravityDetect_RejectsNonTimestampScalars asserts created_at accepts only
// a non-blank string or a JSON number, and step_index rejects null/non-numeric.
func TestAntigravityDetect_RejectsNonTimestampScalars(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":true}`, false},
		{`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":{}}`, false},
		{`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":[]}`, false},
		{`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":null}`, false},
		{`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":"   "}`, false},
		{`{"step_index":null,"source":"MODEL","type":"GENERIC","created_at":"2026-10-16T09:00:00Z"}`, false},
		{`{"step_index":"abc","source":"MODEL","type":"GENERIC","created_at":"2026-10-16T09:00:00Z"}`, false},
		{`{"step_index":"3","source":"MODEL","type":"GENERIC","created_at":"2026-10-16T09:00:00Z"}`, true},
		{`{"step_index":3,"source":"MODEL","type":"GENERIC","created_at":1791696000000}`, true},
	}
	for _, c := range cases {
		if got := (antigravityAdapter{}).Detect([]byte(c.line)); got != c.want {
			t.Fatalf("Detect(%s) = %v, want %v", c.line, got, c.want)
		}
	}
}

// TestDetectExclusivity_AllAdapters asserts each fixture/discriminator is matched
// by exactly its own adapter: the three adapters are mutually exclusive by
// construction, so `auto` order is immaterial.
func TestDetectExclusivity_AllAdapters(t *testing.T) {
	fixtures := []struct {
		name string
		want string
	}{
		{"pi_partial_offline.jsonl", "pi"},
		{"claude_declared.jsonl", "claude"},
		{"antigravity_synthetic.jsonl", "antigravity"},
		{"antigravity_synthetic_full.jsonl", "antigravity"},
	}
	for _, f := range fixtures {
		header := firstNonEmptyLine(readTestdata(t, f.name))
		for _, a := range adapters() {
			got := a.Detect(header)
			want := a.Name() == f.want
			if got != want {
				t.Fatalf("%s: adapter %q Detect=%v, want %v", f.name, a.Name(), got, want)
			}
		}
	}
}

// TestImport_AntigravityRunIDAndSessionUnknowns asserts the no-session-id
// fallback (RunID = sha256:ContentHash, session id falls back to RunID) and the
// honest unknowns that follow from the missing cache-write counter and model id.
func TestImport_AntigravityRunIDAndSessionUnknowns(t *testing.T) {
	rec, err := Import(context.Background(), "testdata/antigravity_synthetic.jsonl", ImportOptions{
		Provider: "antigravity",
		Date:     "2026-10-16",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rec.SessionID != "" {
		t.Fatalf("session id = %q, want empty (the transcript has none)", rec.SessionID)
	}
	if !strings.HasPrefix(rec.RunID, "sha256:") {
		t.Fatalf("run id = %q, want sha256:ContentHash fallback", rec.RunID)
	}
	if rec.Date != "2026-10-16" {
		t.Fatalf("date = %q, want the operator's 2026-10-16", rec.Date)
	}

	session := BuildSession(rec, nil, time.UTC)
	if session.SessionID != rec.RunID {
		t.Fatalf("session id = %q, want the RunID fallback %q", session.SessionID, rec.RunID)
	}
	if session.TotalTokens.Known {
		t.Fatalf("session total = %+v, want UNKNOWN", session.TotalTokens)
	}
	if session.CacheHitPct != nil {
		t.Fatalf("session cache-hit = %v, want nil", *session.CacheHitPct)
	}
	if session.Model != "" {
		t.Fatalf("session model = %q, want empty", session.Model)
	}
	if session.PriceSource != PriceSourceUnknown || session.Cost != nil {
		t.Fatalf("session price/cost = %q/%v, want unknown/nil", session.PriceSource, session.Cost)
	}

	// `auto` reaches the same adapter.
	auto, err := Import(context.Background(), "testdata/antigravity_synthetic.jsonl", ImportOptions{Date: "2026-10-16"})
	if err != nil {
		t.Fatalf("auto import: %v", err)
	}
	if auto.Provider != "antigravity" || len(auto.Calls) != 3 {
		t.Fatalf("auto record = %q/%d, want antigravity/3", auto.Provider, len(auto.Calls))
	}
}

// TestImport_AntigravityZeroCallRefused asserts a detected-but-empty antigravity
// transcript is refused (refuse-all), not written as an empty record.
func TestImport_AntigravityZeroCallRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "antigravity-zero-call.jsonl")
	content := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-16T09:00:00Z","content":"INVENTED-ONLY-TEXT"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Import(context.Background(), path, ImportOptions{Provider: "antigravity", Date: "2026-10-16"})
	if !errors.Is(err, ErrUnreadableTranscript) {
		t.Fatalf("error = %v, want ErrUnreadableTranscript", err)
	}
}

// TestImport_AntigravityGarbageUnreadable asserts the uniform unreadable arm:
// --provider antigravity on garbage parses to nothing and no registered adapter
// matches, so it refuses ErrUnreadableTranscript.
func TestImport_AntigravityGarbageUnreadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.jsonl")
	if err := os.WriteFile(path, []byte(`{"hello":"world"}`+"\nnot json at all\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Import(context.Background(), path, ImportOptions{Provider: "antigravity", Date: "2026-10-16"})
	if !errors.Is(err, ErrUnreadableTranscript) {
		t.Fatalf("error = %v, want ErrUnreadableTranscript", err)
	}
}

// TestRenderTable_ObservedAntigravityOmitsDeclaredMarker asserts the report path
// prints no `[declared schema]` marker for an OBSERVED antigravity run.
func TestRenderTable_ObservedAntigravityOmitsDeclaredMarker(t *testing.T) {
	rec, err := Import(context.Background(), "testdata/antigravity_synthetic.jsonl", ImportOptions{
		Provider: "antigravity",
		Date:     "2026-10-16",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	agg, err := BuildAggregate(context.Background(), "2026-10-16", []RunRecord{rec}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.Provenance != ProvenanceObserved {
		t.Fatalf("aggregate provenance = %q, want observed", agg.Provenance)
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, TableInput{Aggregate: agg, Runs: []RunRecord{rec}}); err != nil {
		t.Fatalf("render table: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "[declared schema]") {
		t.Fatalf("observed antigravity report printed the declared-schema marker:\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("report should render the unavailable values as unknown:\n%s", out)
	}
}
