package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// Agent config keys.
const (
	AgentConfigPath    = "path"
	AgentConfigFixture = "fixture"
)

// AgentEventType is a structured agent-session event type.
type AgentEventType string

const (
	AgentStart  AgentEventType = "start"
	AgentPause  AgentEventType = "pause"
	AgentResume AgentEventType = "resume"
	AgentFinish AgentEventType = "finish"
)

// AgentSessionEvent is one structured agent-session event. Agent sessions suggest
// possible user work only; they never establish user working time.
type AgentSessionEvent struct {
	SessionID string         `json:"session_id"`
	Event     AgentEventType `json:"event"`
	At        time.Time      `json:"at"`
	Project   string         `json:"project,omitempty"`
	Topic     string         `json:"topic,omitempty"`
}

// maxAgentLineBytes bounds a single JSONL line.
const maxAgentLineBytes = 1 << 20

// ParseAgentSessionJSONL parses one JSON object per line from r. Blank lines are
// skipped. A malformed line yields an error that never embeds the line content.
func ParseAgentSessionJSONL(r io.Reader) ([]AgentSessionEvent, error) {
	if r == nil {
		return nil, errors.New("agent: nil reader")
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxAgentLineBytes)

	var events []AgentSessionEvent
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}
		var ev AgentSessionEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			// Never embed the raw line: it could carry sensitive content.
			return nil, fmt.Errorf("agent: invalid JSONL at line %d", line)
		}
		// Reject shape-invalid events (e.g. `{}` or a missing session_id/at):
		// an empty event would otherwise produce a colliding ID and no warning.
		// The error never echoes the raw line or its content.
		if strings.TrimSpace(string(ev.Event)) == "" ||
			strings.TrimSpace(ev.SessionID) == "" ||
			ev.At.IsZero() {
			return nil, fmt.Errorf("agent: invalid event at line %d", line)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("agent: failed to read JSONL stream")
	}
	return events, nil
}

// AgentReader opens the agent JSONL stream. It is injected so tests avoid disk.
type AgentReader func(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (io.ReadCloser, error)

// AgentPlugin collects structured agent-session evidence from a JSONL stream.
type AgentPlugin struct {
	Open AgentReader
}

// NewAgentPlugin returns an agent plugin using the default opener.
func NewAgentPlugin() *AgentPlugin {
	return &AgentPlugin{Open: DefaultAgentOpen}
}

// DefaultAgentOpen opens the configured JSONL path; in contract mode it opens the
// fixed fixture path.
func DefaultAgentOpen(_ context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (io.ReadCloser, error) {
	path := strings.TrimSpace(cfg.Config[AgentConfigPath])
	if req.IsContract() {
		path = strings.TrimSpace(cfg.Config[AgentConfigFixture])
	}
	if path == "" {
		return nil, errors.New("agent: no path configured")
	}
	return os.Open(path)
}

// Type implements evidence.Plugin.
func (p *AgentPlugin) Type() evidence.SourceKind { return evidence.SourceAgentSession }

// RequiredConfig implements evidence.Plugin.
func (p *AgentPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: AgentConfigPath, Secret: false, Required: true}}
}

// DeclaredEvidenceKinds implements evidence.Plugin.
func (p *AgentPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceAgentSession}
}

// CheckContract implements evidence.Plugin.
func (p *AgentPlugin) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	if strings.TrimSpace(cfg.Config[AgentConfigPath]) == "" {
		return []evidence.ContractCheck{{
			Name:   "agent_path",
			Passed: false,
			Code:   "AGENT_PATH_REQUIRED",
			Detail: "agent JSONL path is required",
		}}
	}
	return []evidence.ContractCheck{{Name: "agent_path", Passed: true}}
}

// Collect implements evidence.Plugin. Every emitted record has an UNKNOWN
// duration: even explicit agent session boundaries never confirm user working
// time. A missing finish event sets a warning but keeps the source complete,
// because collection gathered everything that existed.
func (p *AgentPlugin) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	open := p.Open
	if open == nil {
		open = DefaultAgentOpen
	}
	rc, err := open(ctx, cfg, req)
	if err != nil {
		return evidence.CollectResult{}, errors.New("agent: failed to open stream")
	}
	defer func() { _ = rc.Close() }()

	events, err := ParseAgentSessionJSONL(rc)
	if err != nil {
		return evidence.CollectResult{}, err
	}

	observed := time.Now().UTC()
	evs := make([]evidence.Evidence, 0, len(events))
	for _, e := range events {
		ev := evidence.Evidence{
			ID:          evidence.AgentSessionID(e.SessionID, string(e.Event), e.At.UTC().Format(time.RFC3339Nano)),
			Kind:        evidence.SourceAgentSession,
			Title:       string(e.Event),
			Description: strings.TrimSpace(e.Topic),
			Start:       e.At.UTC(),
			Duration:    evidence.UnknownDuration(),
			SourceRefs: []evidence.SourceRef{{
				Kind:      evidence.SourceAgentSession,
				SessionID: e.SessionID,
			}},
			ObservedAt: observed,
		}
		if proj := strings.TrimSpace(e.Project); proj != "" {
			ev.ProjectHints = []string{proj}
		}
		if ev.Duration.Known {
			// Programmer invariant: agent evidence never carries a known duration.
			return evidence.CollectResult{}, errors.New("agent: agent evidence must carry an unknown duration")
		}
		evs = append(evs, ev)
	}

	return evidence.CollectResult{
		Evidence: evs,
		Result: evidence.SourceResult{
			Status:        evidence.StatusComplete,
			EvidenceCount: len(evs),
			Warnings:      openSessionWarnings(events),
		},
	}, nil
}

// openSessionWarnings returns a stable warning for each session that emitted
// events but no finish. A missing finish never extends the session to day-end.
func openSessionWarnings(events []AgentSessionEvent) []string {
	seen := map[string]bool{}
	finished := map[string]bool{}
	var ids []string
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		if !seen[e.SessionID] {
			seen[e.SessionID] = true
			ids = append(ids, e.SessionID)
		}
		if e.Event == AgentFinish {
			finished[e.SessionID] = true
		}
	}
	sort.Strings(ids)
	var warnings []string
	for _, id := range ids {
		if !finished[id] {
			warnings = append(warnings, "agent_session_open:"+id)
		}
	}
	return warnings
}

// NarrativeExcluded reports whether e must be kept out of narrative journal
// notes. Agent-session evidence is structured activity, never narrative text.
func NarrativeExcluded(e evidence.Evidence) bool {
	return e.Kind == evidence.SourceAgentSession
}
