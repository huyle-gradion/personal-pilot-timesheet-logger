package plugins

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

const agentTestJSONL = `{"session_id":"s1","event":"start","at":"2026-10-06T09:00:00Z","project":"Proj","topic":"fixing bug"}
{"session_id":"s1","event":"pause","at":"2026-10-06T09:30:00Z"}
{"session_id":"s1","event":"resume","at":"2026-10-06T09:45:00Z"}
{"session_id":"s1","event":"finish","at":"2026-10-06T10:15:00Z"}
{"session_id":"s2","event":"start","at":"2026-10-06T11:00:00Z","topic":"reviewing"}`

func readerAgent(data string) AgentReader {
	return func(context.Context, evidence.SourceConfig, evidence.CollectRequest) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(data)), nil
	}
}

func agentConfig() evidence.SourceConfig {
	return evidence.SourceConfig{ID: "agent", Type: evidence.SourceAgentSession, Enabled: true, Config: map[string]string{AgentConfigPath: "unused.jsonl"}}
}

// TestParseAgentSessionJSONL proves one JSON object per line is parsed and a
// malformed line errors without embedding the line content.
func TestParseAgentSessionJSONL(t *testing.T) {
	events, err := ParseAgentSessionJSONL(strings.NewReader(agentTestJSONL))
	if err != nil {
		t.Fatalf("ParseAgentSessionJSONL: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	if events[0].SessionID != "s1" || events[0].Event != AgentStart {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	if events[4].Event != AgentStart || events[4].SessionID != "s2" {
		t.Errorf("unexpected last event: %+v", events[4])
	}

	const secretLine = `{"session_id":"SENTINEL-SECRET-abc"`
	_, err = ParseAgentSessionJSONL(strings.NewReader(secretLine))
	if err == nil {
		t.Fatal("expected error for malformed JSONL")
	}
	if strings.Contains(err.Error(), "SENTINEL-SECRET-abc") {
		t.Errorf("error embedded raw line content: %v", err)
	}
}

// TestParseAgentSessionJSONL_RejectsShapeInvalid proves shape-invalid lines are
// rejected with a content-free error (never echoing the raw line).
func TestParseAgentSessionJSONL_RejectsShapeInvalid(t *testing.T) {
	cases := map[string]string{
		"empty object":       `{}`,
		"missing session id": `{"event":"start","at":"2026-10-06T09:00:00Z"}`,
		"missing event":      `{"session_id":"s1","at":"2026-10-06T09:00:00Z"}`,
		"zero timestamp":     `{"session_id":"s1","event":"start"}`,
		"zero time string":   `{"session_id":"s1","event":"start","at":"0001-01-01T00:00:00Z"}`,
		"secret content":     `{"event":"start","at":"2026-10-06T09:00:00Z","topic":"SENTINEL-SECRET-xyz"}`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseAgentSessionJSONL(strings.NewReader(line))
			if err == nil {
				t.Fatalf("expected error for %s", name)
			}
			if strings.Contains(err.Error(), line) {
				t.Errorf("error echoed the raw line: %v", err)
			}
			if strings.Contains(err.Error(), "SENTINEL-SECRET-xyz") {
				t.Errorf("error embedded content: %v", err)
			}
		})
	}
}

// TestAgentPlugin_CollectUnknownDuration proves every agent record carries an
// unknown duration, a session ref, and an empty topic hint.
func TestAgentPlugin_CollectUnknownDuration(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(&AgentPlugin{Open: readerAgent(agentTestJSONL)})

	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{agentConfig()}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if len(ev) != 5 {
		t.Fatalf("expected 5 evidence items, got %d", len(ev))
	}
	for _, e := range ev {
		if e.Kind != evidence.SourceAgentSession {
			t.Errorf("unexpected kind %q", e.Kind)
		}
		if e.Duration.Known {
			t.Errorf("agent evidence %q must carry an unknown duration", e.ID)
		}
		if e.TopicHint != nil {
			t.Errorf("agent evidence %q must have an empty topic hint", e.ID)
		}
		if len(e.SourceRefs) != 1 || e.SourceRefs[0].SessionID == "" {
			t.Errorf("agent evidence %q missing session ref: %+v", e.ID, e.SourceRefs)
		}
	}
	if !summary.Complete {
		t.Errorf("expected complete, got %+v", summary)
	}
}

// TestAgentPlugin_MissingFinishWarnsButStaysComplete proves a session without a
// finish event warns but does not mark the source incomplete.
func TestAgentPlugin_MissingFinishWarnsButStaysComplete(t *testing.T) {
	reg := evidence.NewRegistry()
	reg.Register(&AgentPlugin{Open: readerAgent(agentTestJSONL)})

	_, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{agentConfig()}, evidence.CollectRequest{Date: "2026-10-06"})
	if err != nil {
		t.Fatalf("CollectDay: %v", err)
	}
	if summary.Sources[0].Status != evidence.StatusComplete {
		t.Fatalf("missing finish must not mark the source incomplete, got %+v", summary.Sources[0])
	}
	found := false
	for _, w := range summary.Sources[0].Warnings {
		if w == "agent_session_open:s2" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent_session_open:s2 warning, got %v", summary.Sources[0].Warnings)
	}
	for _, w := range summary.Sources[0].Warnings {
		if strings.Contains(w, "s1") {
			t.Errorf("s1 finished and must not warn: %v", w)
		}
	}
}

// TestAgentPlugin_NarrativeExcluded proves agent evidence is excluded from
// narrative notes while other kinds are not.
func TestAgentPlugin_NarrativeExcluded(t *testing.T) {
	if !NarrativeExcluded(evidence.Evidence{Kind: evidence.SourceAgentSession}) {
		t.Error("agent evidence must be excluded from narrative")
	}
	if NarrativeExcluded(evidence.Evidence{Kind: evidence.SourceCalendar}) {
		t.Error("calendar evidence must not be excluded from narrative")
	}
}

// TestAgentPlugin_ContractDeterminism proves the fixed JSONL fixture yields a
// complete determinism contract check.
func TestAgentPlugin_ContractDeterminism(t *testing.T) {
	dir := t.TempDir()
	fixture := dir + "/agent.jsonl"
	if err := os.WriteFile(fixture, []byte(agentTestJSONL), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := evidence.NewRegistry()
	reg.Register(NewAgentPlugin())
	cfg := agentConfig()
	cfg.Config[AgentConfigFixture] = fixture

	results, err := reg.VerifyContracts(context.Background(), []evidence.SourceConfig{cfg})
	if err != nil {
		t.Fatalf("VerifyContracts: %v", err)
	}
	if len(results) != 1 || results[0].Status != evidence.StatusComplete {
		t.Fatalf("expected one complete contract result, got %+v", results)
	}
}
