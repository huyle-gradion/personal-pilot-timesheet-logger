package metrics

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// claudeAdapter parses a Claude Code JSONL transcript. claude is DECLARED
// provenance: the schema is known but was NOT verified first-hand on this host
// (no sample available). Reasoning tokens are UNKNOWN because the declared schema
// carries no reasoning counter. The report prints a `[declared schema]` marker.
type claudeAdapter struct{}

func (claudeAdapter) Name() string           { return "claude" }
func (claudeAdapter) Provenance() Provenance { return ProvenanceDeclared }

// Detect matches a Claude Code header line. It deliberately rejects the pi
// session line so auto-detection never confuses the two providers.
func (claudeAdapter) Detect(header []byte) bool {
	if len(header) == 0 {
		return false
	}
	if jsonLineType(header) == "session" {
		return false
	}
	var probe struct {
		SessionID string `json:"sessionId"`
		LeafUUID  string `json:"leafUuid"`
		Type      string `json:"type"`
	}
	if err := json.Unmarshal(header, &probe); err != nil {
		return false
	}
	if strings.TrimSpace(probe.SessionID) != "" {
		return true
	}
	return probe.Type == "summary" && strings.TrimSpace(probe.LeafUUID) != ""
}

// claudeLine is the minimal decoded shape of a Claude Code JSONL line.
type claudeLine struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	Timestamp json.RawMessage `json:"timestamp"`
	Message   *claudeMessage  `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Usage   *claudeUsage    `json:"usage"`
	Content []claudeContent `json:"content"`
}

type claudeUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

type claudeContent struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Input decodes ONLY the bash `command` field for the subcommand token.
	Input struct {
		Command string `json:"command"`
	} `json:"input"`
}

// Parse reads the Claude Code JSONL transcript.
func (claudeAdapter) Parse(ctx context.Context, r io.Reader) (RunRecord, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), int(MaxTranscriptBytes))

	var (
		sessionID       string
		model           string
		startedAt       time.Time
		endedAt         time.Time
		calls           []CallRecord
		obs             []Observation
		turnIndex       int
		sawAssistant    bool
		lastHasToolCall bool
	)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return RunRecord{}, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var l claudeLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue
		}
		if s := strings.TrimSpace(l.SessionID); s != "" {
			sessionID = s
		}
		if l.Message == nil {
			continue
		}
		m := l.Message
		ts, ok := parseTimestamp(l.Timestamp)
		if m.Role == "assistant" {
			sawAssistant = true
			if mm := strings.TrimSpace(m.Model); mm != "" {
				model = mm
			}
			hasTool := claudeHasToolCall(m.Content)
			if m.Usage == nil && !hasTool {
				continue
			}
			turnIndex++
			step, cat := claudeAttribute(m.Content)
			calls = append(calls, CallRecord{
				TurnIndex:  turnIndex,
				Provider:   "claude",
				Model:      model,
				SessionID:  sessionID,
				Timestamp:  ts,
				Usage:      mapClaudeUsage(m.Usage),
				Category:   cat,
				Step:       step,
				Provenance: ProvenanceDeclared,
				// ContextLength: the declared schema has no totalTokens field, so
				// it is UNKNOWN (never derived from input+cache).
				ContextLength: workflow.UnknownMeasurement(),
			})
			lastHasToolCall = hasTool
			if ok {
				obs = append(obs, Observation{Timestamp: ts, IsTurn: true})
				startedAt = earlier(startedAt, ts)
				endedAt = later(endedAt, ts)
			}
			continue
		}
		// A user line is a tool result or a human turn: an observation point.
		if ok {
			obs = append(obs, Observation{Timestamp: ts, IsTurn: false})
			startedAt = earlier(startedAt, ts)
			endedAt = later(endedAt, ts)
		}
	}
	if err := scanner.Err(); err != nil {
		return RunRecord{}, err
	}

	closingTurn := sawAssistant && !lastHasToolCall
	return buildRun(runParts{
		sessionID:    sessionID,
		provider:     "claude",
		model:        model,
		startedAt:    startedAt,
		endedAt:      endedAt,
		calls:        calls,
		observations: obs,
		closingTurn:  closingTurn,
		provenance:   ProvenanceDeclared,
	}), nil
}

func claudeAttribute(content []claudeContent) (StepID, Category) {
	for _, c := range content {
		if c.Type != "tool_use" {
			continue
		}
		cmd := strings.TrimSpace(c.Input.Command)
		if cmd == "" {
			continue
		}
		step, cat := AttributeCommand(cmd)
		if step != StepUnknown {
			return step, cat
		}
	}
	return StepUnknown, CategoryUnassigned
}

func claudeHasToolCall(content []claudeContent) bool {
	for _, c := range content {
		if c.Type == "tool_use" {
			return true
		}
	}
	return false
}

// mapClaudeUsage maps the declared claude usage schema. cache_creation_input_tokens
// maps to CacheWrite and cache_read_input_tokens to CacheRead; reasoning is
// UNKNOWN (the schema carries no reasoning counter).
func mapClaudeUsage(u *claudeUsage) Usage {
	if u == nil {
		return UnknownUsage()
	}
	return Usage{
		Input:      claudeMeasurement(u.InputTokens),
		Output:     claudeMeasurement(u.OutputTokens),
		CacheRead:  claudeMeasurement(u.CacheReadInputTokens),
		CacheWrite: claudeMeasurement(u.CacheCreationInputTokens),
		Reasoning:  workflow.UnknownMeasurement(),
	}
}

func claudeMeasurement(p *int64) workflow.Measurement {
	if p == nil {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(*p)
}
