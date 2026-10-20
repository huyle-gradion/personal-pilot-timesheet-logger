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

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}

func parseWith(t *testing.T, a Adapter, data []byte) RunRecord {
	t.Helper()
	rec, err := a.Parse(context.Background(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return rec
}

// TestPIAdapter_ObservedExactMapping asserts the pi fixture's usage objects map
// verbatim into components and that the run is partial (never completed).
func TestPIAdapter_ObservedExactMapping(t *testing.T) {
	data := readTestdata(t, "pi_partial_offline.jsonl")
	a := piAdapter{}
	if !a.Detect(firstNonEmptyLine(data)) {
		t.Fatal("pi Detect did not match the session header")
	}
	rec := parseWith(t, a, data)

	if rec.Provenance != ProvenanceObserved {
		t.Fatalf("provenance = %q, want observed", rec.Provenance)
	}
	if len(rec.Calls) != 8 {
		t.Fatalf("calls = %d, want 8", len(rec.Calls))
	}
	assertKnown(t, "input", rec.Usage.Input, 24924)
	assertKnown(t, "output", rec.Usage.Output, 2250)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 118633)
	assertKnown(t, "cacheWrite", rec.Usage.CacheWrite, 0)
	assertKnown(t, "reasoning", rec.Usage.Reasoning, 855)

	// ContextLength is validation-only: it is the provider totalTokens per call.
	for i, c := range rec.Calls {
		if !c.ContextLength.Known {
			t.Fatalf("call %d context length unknown", i)
		}
		if c.Step != StepCollect {
			t.Fatalf("call %d step = %q, want collect_propose", i, c.Step)
		}
		if c.Category != CategoryOrchestration {
			t.Fatalf("call %d category = %q, want orchestration", i, c.Category)
		}
		if c.Attempt != i+1 {
			t.Fatalf("call %d attempt = %d, want %d", i, c.Attempt, i+1)
		}
	}

	if rec.State != RunPartial {
		t.Fatalf("state = %q, want partial", rec.State)
	}
	if len(rec.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(rec.Steps))
	}
	if rec.Steps[0].Step != StepCollect || rec.Steps[0].Calls != 8 || rec.Steps[0].Retries != 7 {
		t.Fatalf("step = %+v, want collect calls=8 retries=7", rec.Steps[0])
	}
}

// TestClaudeAdapter_DeclaredMapping asserts the declared claude schema maps
// cache_creation_input_tokens to CacheWrite and leaves reasoning UNKNOWN.
func TestClaudeAdapter_DeclaredMapping(t *testing.T) {
	data := readTestdata(t, "claude_declared.jsonl")
	a := claudeAdapter{}
	if !a.Detect(firstNonEmptyLine(data)) {
		t.Fatal("claude Detect did not match the header")
	}
	rec := parseWith(t, a, data)

	if rec.Provenance != ProvenanceDeclared {
		t.Fatalf("provenance = %q, want declared", rec.Provenance)
	}
	if len(rec.Calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(rec.Calls))
	}
	assertKnown(t, "input", rec.Usage.Input, 1500)
	assertKnown(t, "output", rec.Usage.Output, 300)
	assertKnown(t, "cacheRead", rec.Usage.CacheRead, 60)
	assertKnown(t, "cacheWrite", rec.Usage.CacheWrite, 50)
	if rec.Usage.Reasoning.Known {
		t.Fatalf("reasoning = %+v, want UNKNOWN", rec.Usage.Reasoning)
	}
	// ContextLength is UNKNOWN for claude: the schema has no totalTokens.
	if rec.Calls[0].ContextLength.Known {
		t.Fatalf("claude context length = %+v, want UNKNOWN", rec.Calls[0].ContextLength)
	}
	// A bare review then a preview: highest < submit and collect occurred.
	if rec.State != RunPartial {
		t.Fatalf("state = %q, want partial", rec.State)
	}
}

// TestMissingUsage_UnknownNotZero asserts a null usage object yields UNKNOWN, not
// zero, and is never silently read as zero.
func TestMissingUsage_UnknownNotZero(t *testing.T) {
	// A pi assistant turn that issued a tool call but carried usage:null.
	line := `{"type":"message","message":{"role":"assistant","timestamp":1791695713126,"content":[{"type":"toolCall","name":"bash","arguments":{"command":"timesheet review"}}],"usage":null}}`
	rec := parseWith(t, piAdapter{}, []byte(line))
	if len(rec.Calls) != 1 {
		t.Fatalf("calls = %d, want 1 (attributed turn kept)", len(rec.Calls))
	}
	if rec.Usage.Input.Known || rec.Usage.Output.Known || rec.Usage.CacheRead.Known {
		t.Fatalf("usage = %+v, want all UNKNOWN", rec.Usage)
	}
	if rec.Usage.ConsumedTokens().Known {
		t.Fatalf("consumed tokens = %+v, want UNKNOWN", rec.Usage.ConsumedTokens())
	}
}

// TestSafety_SecretNeverPersisted asserts a sentinel secret placed in message
// text and tool results never appears in a marshaled RunRecord.
func TestSafety_SecretNeverPersisted(t *testing.T) {
	const secret = "SENTINEL-SECRET-9f3a1c"
	lines := []string{
		`{"type":"session","id":"s1","cwd":"/repo","timestamp":"2026-10-11T05:15:03.251Z","version":3}`,
		`{"type":"message","message":{"role":"assistant","timestamp":1791695713126,"content":[{"type":"text","text":"` + secret + `"},{"type":"toolCall","name":"bash","arguments":{"command":"timesheet review --decisions ` + secret + `.json"}}],"usage":{"input":10,"output":2,"cacheRead":0,"cacheWrite":0,"reasoning":0,"totalTokens":12}}}`,
		`{"type":"message","message":{"role":"toolResult","timestamp":1791695713200,"toolName":"bash","content":[{"type":"text","text":"` + secret + `"}]}}`,
	}
	rec := parseWith(t, piAdapter{}, []byte(strings.Join(lines, "\n")))
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatalf("marshaled RunRecord leaked the sentinel secret")
	}
	// The subcommand token IS retained (as a step), but not the command text.
	if rec.Calls[0].Step != StepReview {
		t.Fatalf("step = %q, want review (review --decisions)", rec.Calls[0].Step)
	}
}

// TestSelectAdapter_AutoAndExplicit asserts provider selection and the fail-closed
// unknown case.
func TestSelectAdapter_AutoAndExplicit(t *testing.T) {
	piHeader := firstNonEmptyLine(readTestdata(t, "pi_partial_offline.jsonl"))
	claudeHeader := firstNonEmptyLine(readTestdata(t, "claude_declared.jsonl"))
	antigravityHeader := firstNonEmptyLine(readTestdata(t, "antigravity_synthetic.jsonl"))

	a, err := SelectAdapter("auto", piHeader)
	if err != nil || a.Name() != "pi" {
		t.Fatalf("auto/pi: %v %v", a, err)
	}
	a, err = SelectAdapter("auto", claudeHeader)
	if err != nil || a.Name() != "claude" {
		t.Fatalf("auto/claude: %v %v", a, err)
	}
	a, err = SelectAdapter("auto", antigravityHeader)
	if err != nil || a.Name() != "antigravity" {
		t.Fatalf("auto/antigravity: %v %v", a, err)
	}
	a, err = SelectAdapter("claude", piHeader)
	if err != nil || a.Name() != "claude" {
		t.Fatalf("explicit claude: %v %v", a, err)
	}
	a, err = SelectAdapter("antigravity", piHeader)
	if err != nil || a.Name() != "antigravity" {
		t.Fatalf("explicit antigravity: %v %v", a, err)
	}
	if _, err := SelectAdapter("auto", []byte(`{"hello":"world"}`)); err == nil {
		t.Fatal("auto on unknown header should fail closed")
	}
}

func assertKnown(t *testing.T, name string, m workflow.Measurement, want int64) {
	t.Helper()
	if !m.Known {
		t.Fatalf("%s = UNKNOWN, want known %d", name, want)
	}
	if m.Value != want {
		t.Fatalf("%s = %d, want %d", name, m.Value, want)
	}
}

// TestPIAdapter_CostPreserved (F12) asserts the provider-native numeric cost is
// preserved on the call and never appears in an aggregate.
func TestPIAdapter_CostPreserved(t *testing.T) {
	rec := parseWith(t, piAdapter{}, readTestdata(t, "pi_partial_offline.jsonl"))
	if len(rec.Calls) == 0 {
		t.Fatal("no calls")
	}
	c := rec.Calls[0]
	if c.Cost == nil {
		t.Fatal("cost was dropped")
	}
	if c.Cost.Total == nil || *c.Cost.Total != 0 {
		t.Fatalf("cost.total = %v, want 0", c.Cost.Total)
	}
	if c.Cost.Input == nil || *c.Cost.Input != 0 {
		t.Fatalf("cost.input = %v, want 0", c.Cost.Input)
	}

	agg, err := BuildAggregate(context.Background(), "2026-10-11", []RunRecord{rec}, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	data, _ := json.Marshal(agg)
	if bytes.Contains(data, []byte("cost")) {
		t.Fatalf("cost leaked into the aggregate: %s", data)
	}
}

// TestImport_AnnotationsSet (F4) asserts import sets the run annotations.
func TestImport_AnnotationsSet(t *testing.T) {
	rec, err := Import(context.Background(), "testdata/pi_partial_offline.jsonl", ImportOptions{
		Date:               "2026-10-11",
		Commit:             "abc1234",
		WorkflowVersion:    "v1",
		ConfigMode:         "offline",
		OptimizationChange: "narrow-ai-step",
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rec.Commit != "abc1234" || rec.WorkflowVersion != "v1" || rec.ConfigMode != "offline" || rec.OptimizationChange != "narrow-ai-step" {
		t.Fatalf("annotations not set: %+v", rec)
	}
}

// TestImport_RequiresExplicitDate (R5, fail-before) asserts that a transcript
// that CARRIES timestamps is still refused when no --date is supplied: no day is
// inferred from the transcript or the clock. With --date the same transcript
// imports and takes exactly that day.
func TestImport_RequiresExplicitDate(t *testing.T) {
	rec, err := Import(context.Background(), "testdata/pi_partial_offline.jsonl", ImportOptions{Provider: "pi"})
	if err == nil {
		t.Fatalf("Import with no --date succeeded (date=%q); no day may be inferred from the transcript or the clock", rec.Date)
	}
	if !errors.Is(err, ErrDateRequired) {
		t.Fatalf("error = %v, want ErrDateRequired", err)
	}
	if !strings.Contains(err.Error(), "requires an explicit --date") {
		t.Fatalf("error = %q, want it to state that an explicit --date is required", err)
	}
	if !strings.Contains(err.Error(), "no day is inferred from the transcript or the clock") {
		t.Fatalf("error = %q, want the no-inference rule stated", err)
	}

	// With an explicit day the same transcript imports, and the day is the
	// operator's, not the transcript's.
	rec, err = Import(context.Background(), "testdata/pi_partial_offline.jsonl", ImportOptions{Provider: "pi", Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("Import with --date: %v", err)
	}
	if rec.Date != "2026-10-11" {
		t.Fatalf("date = %q, want 2026-10-11", rec.Date)
	}
}

// TestImport_ProviderMismatchRefused (R6) asserts a forced wrong provider against
// another REGISTERED provider's transcript refuses, naming requested and detected.
func TestImport_ProviderMismatchRefused(t *testing.T) {
	_, err := Import(context.Background(), "testdata/claude_declared.jsonl", ImportOptions{
		Provider: "pi",
		Date:     "2026-10-11",
	})
	if !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("error = %v, want ErrProviderMismatch", err)
	}
	if !strings.Contains(err.Error(), `requested "pi"`) || !strings.Contains(err.Error(), `detected "claude"`) {
		t.Fatalf("error = %q, want it to name requested pi and detected claude", err)
	}
}

// TestImport_AntigravityShapedMismatchRefused (R6, antigravity-shaped arm,
// UPGRADED by #73) asserts a transcript that ONLY the registered antigravity
// adapter recognizes is refused by a forced wrong provider with
// ErrProviderMismatch naming the detected antigravity. #69 asserted
// ErrUnreadableTranscript here because no antigravity adapter existed; with the
// adapter registered the diagnosis names it. The lines are INVENTED values
// carrying the real field names; no real transcript is copied.
func TestImport_AntigravityShapedMismatchRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "antigravity-shaped.jsonl")
	content := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-10-11T09:00:00Z","content":"<invented>"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"GENERIC","created_at":"2026-10-11T09:00:05Z","input_tokens":11,"output_tokens":5,"cache_read_tokens":2}` + "\n" +
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-10-11T09:00:09Z","input_tokens":7,"output_tokens":3,"cache_read_tokens":0}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, provider := range []string{"pi", "claude"} {
		_, err := Import(context.Background(), path, ImportOptions{Provider: provider, Date: "2026-10-11"})
		if !errors.Is(err, ErrProviderMismatch) {
			t.Fatalf("provider %q: error = %v, want ErrProviderMismatch", provider, err)
		}
		if !strings.Contains(err.Error(), `requested "`+provider+`"`) || !strings.Contains(err.Error(), `detected "antigravity"`) {
			t.Fatalf("provider %q: error = %q, want it to name requested %q and detected antigravity", provider, err, provider)
		}
	}
	// `auto` now detects antigravity and imports the two usage-bearing lines.
	rec, err := Import(context.Background(), path, ImportOptions{Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("auto on antigravity-shaped transcript: %v", err)
	}
	if rec.Provider != "antigravity" || len(rec.Calls) != 2 {
		t.Fatalf("auto record = provider %q calls %d, want antigravity/2", rec.Provider, len(rec.Calls))
	}
}

// TestImport_ReadableResumedFileNotRefused (R6 companion) asserts the guard never
// refuses a NON-empty parse whose first line is not the probe line: a resumed file
// with a leading queue line imports exactly as before.
func TestImport_ReadableResumedFileNotRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resumed.jsonl")
	content := `{"type":"queue","id":"q-1"}` + "\n" +
		`{"type":"session","id":"s-resumed","timestamp":"2026-10-11T09:00:00Z"}` + "\n" +
		`{"type":"message","timestamp":"2026-10-11T09:00:10Z","message":{"role":"assistant","content":[{"type":"toolCall","name":"bash","arguments":{"command":"timesheet review"}}],"usage":{"input":30,"output":10,"cacheRead":5,"cacheWrite":0,"reasoning":2,"totalTokens":45}}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec, err := Import(context.Background(), path, ImportOptions{Provider: "pi", Date: "2026-10-11"})
	if err != nil {
		t.Fatalf("a readable import must never refuse: %v", err)
	}
	if len(rec.Calls) != 1 || rec.SessionID != "s-resumed" || rec.Date != "2026-10-11" {
		t.Fatalf("unexpected record: calls=%d session=%q date=%q", len(rec.Calls), rec.SessionID, rec.Date)
	}
}
