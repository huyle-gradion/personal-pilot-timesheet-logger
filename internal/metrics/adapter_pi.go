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

// piAdapter parses a pi JSONL transcript. pi is OBSERVED provenance: the usage
// objects are read first-hand.
type piAdapter struct{}

func (piAdapter) Name() string           { return "pi" }
func (piAdapter) Provenance() Provenance { return ProvenanceObserved }

// Detect matches the pi session header line ({"type":"session",...}).
func (piAdapter) Detect(header []byte) bool {
	if len(header) == 0 {
		return false
	}
	return jsonLineType(header) == "session"
}

// piLine is the minimal decoded shape of a pi JSONL line. It deliberately does
// NOT decode message text, tool arguments beyond the bash command token, or tool
// results.
type piLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Cwd       string          `json:"cwd"`
	Timestamp json.RawMessage `json:"timestamp"`
	Provider  string          `json:"provider"`
	ModelID   string          `json:"modelId"`
	Message   *piMessage      `json:"message"`
}

type piMessage struct {
	Role      string          `json:"role"`
	Timestamp json.RawMessage `json:"timestamp"`
	Usage     *piUsage        `json:"usage"`
	Content   []piContent     `json:"content"`
}

type piContent struct {
	Type string `json:"type"`
	Name string `json:"name"`
	// Arguments decodes ONLY the bash `command` field so the subcommand token can
	// be extracted. No other tool argument is retained.
	Arguments struct {
		Command string `json:"command"`
	} `json:"arguments"`
}

type piUsage struct {
	Input       *int64  `json:"input"`
	Output      *int64  `json:"output"`
	CacheRead   *int64  `json:"cacheRead"`
	CacheWrite  *int64  `json:"cacheWrite"`
	Reasoning   *int64  `json:"reasoning"`
	TotalTokens *int64  `json:"totalTokens"`
	Cost        *piCost `json:"cost"`
}

// piCost is the provider-native numeric cost object. It is preserved as an
// annotation only and is never aggregated.
type piCost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cacheRead"`
	CacheWrite *float64 `json:"cacheWrite"`
	Total      *float64 `json:"total"`
}

// Parse reads the pi JSONL transcript.
func (piAdapter) Parse(ctx context.Context, r io.Reader) (RunRecord, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), int(MaxTranscriptBytes))

	var (
		sessionID       string
		provider        string
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
		var l piLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue
		}
		switch l.Type {
		case "session":
			sessionID = l.ID
			if t, ok := parseTimestamp(l.Timestamp); ok {
				startedAt = earlier(startedAt, t)
			}
		case "model_change":
			if p := strings.TrimSpace(l.Provider); p != "" {
				provider = p
			}
			if m := strings.TrimSpace(l.ModelID); m != "" {
				model = m
			}
		case "message":
			if l.Message == nil {
				continue
			}
			m := l.Message
			ts, ok := parseTimestamp(m.Timestamp)
			if !ok {
				ts, ok = parseTimestamp(l.Timestamp)
			}
			switch m.Role {
			case "assistant":
				sawAssistant = true
				hasTool := piHasToolCall(m.Content)
				if m.Usage == nil && !hasTool {
					continue
				}
				turnIndex++
				step, cat := piAttribute(m.Content)
				calls = append(calls, CallRecord{
					TurnIndex:     turnIndex,
					Provider:      provider,
					Model:         model,
					SessionID:     sessionID,
					Timestamp:     ts,
					Usage:         mapPIUsage(m.Usage),
					Category:      cat,
					Step:          step,
					ContextLength: piContextLength(m.Usage),
					Cost:          mapPICost(m.Usage),
					Provenance:    ProvenanceObserved,
				})
				lastHasToolCall = hasTool
				if ok {
					obs = append(obs, Observation{Timestamp: ts, IsTurn: true})
					endedAt = later(endedAt, ts)
				}
			case "toolResult", "user", "system":
				if ok {
					obs = append(obs, Observation{Timestamp: ts, IsTurn: false})
					endedAt = later(endedAt, ts)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return RunRecord{}, err
	}

	closingTurn := sawAssistant && !lastHasToolCall
	return buildRun(runParts{
		sessionID:    sessionID,
		provider:     provider,
		model:        model,
		startedAt:    startedAt,
		endedAt:      endedAt,
		calls:        calls,
		observations: obs,
		closingTurn:  closingTurn,
		provenance:   ProvenanceObserved,
	}), nil
}

// piAttribute returns the first recognized step among the turn's tool calls, else
// StepUnknown + CategoryUnassigned.
func piAttribute(content []piContent) (StepID, Category) {
	for _, c := range content {
		if c.Type != "toolCall" {
			continue
		}
		cmd := strings.TrimSpace(c.Arguments.Command)
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

// piHasToolCall reports whether the turn issued any tool call.
func piHasToolCall(content []piContent) bool {
	for _, c := range content {
		if c.Type == "toolCall" {
			return true
		}
	}
	return false
}

func mapPIUsage(u *piUsage) Usage {
	if u == nil {
		return UnknownUsage()
	}
	return Usage{
		Input:      piMeasurement(u.Input),
		Output:     piMeasurement(u.Output),
		CacheRead:  piMeasurement(u.CacheRead),
		CacheWrite: piMeasurement(u.CacheWrite),
		Reasoning:  piMeasurement(u.Reasoning),
	}
}

// piContextLength maps the provider totalTokens to ContextLength (VALIDATION
// ONLY). It is never aggregated.
func piContextLength(u *piUsage) workflow.Measurement {
	if u == nil || u.TotalTokens == nil {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(*u.TotalTokens)
}

// mapPICost preserves the provider-native numeric cost object. It returns nil when
// the provider reports no cost. The value is never aggregated.
func mapPICost(u *piUsage) *CallCost {
	if u == nil || u.Cost == nil {
		return nil
	}
	return &CallCost{
		Input:      u.Cost.Input,
		Output:     u.Cost.Output,
		CacheRead:  u.Cost.CacheRead,
		CacheWrite: u.Cost.CacheWrite,
		Total:      u.Cost.Total,
	}
}

func piMeasurement(p *int64) workflow.Measurement {
	if p == nil {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(*p)
}
