package gradion

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// remoteRow is one remote entry row, tolerantly decoded. Field names follow the
// verified tool schema; unknown fields are ignored.
type remoteRow struct {
	ID               string `json:"id"`
	Date             string `json:"date"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	ClassificationID string `json:"classificationId"`
	Classification   string `json:"classification"`
	Description      string `json:"description"`
	Task             string `json:"task"`
	Billable         *bool  `json:"billable"`
}

// classRow is one classification row, tolerantly decoded.
type classRow struct {
	ID               string `json:"id"`
	ClassificationID string `json:"classificationId"`
	Name             string `json:"name"`
	Title            string `json:"title"`
	Code             string `json:"code"`
}

// toRemoteState maps a row into the S1 workflow contract.
func (r remoteRow) toRemoteState(observed time.Time) workflow.RemoteState {
	classificationID := r.ClassificationID
	if classificationID == "" {
		classificationID = r.Classification
	}
	return workflow.RemoteState{
		RemoteID:         strings.TrimSpace(r.ID),
		Date:             strings.TrimSpace(r.Date),
		StartTime:        strings.TrimSpace(r.StartTime),
		EndTime:          strings.TrimSpace(r.EndTime),
		ClassificationID: strings.TrimSpace(classificationID),
		Description:      r.Description,
		Task:             strings.TrimSpace(r.Task),
		Billable:         r.Billable,
		ObservedAt:       observed,
	}
}

func (c classRow) toClassification() Classification {
	id := strings.TrimSpace(c.ID)
	if id == "" {
		id = strings.TrimSpace(c.ClassificationID)
	}
	name := strings.TrimSpace(c.Name)
	if name == "" {
		name = strings.TrimSpace(c.Title)
	}
	return Classification{ID: id, Name: name, Code: strings.TrimSpace(c.Code)}
}

// payloadOf returns the JSON payload a CallResult carries: structuredContent
// preferred, else the first text block.
func payloadOf(res mcp.CallResult) []byte {
	if p := bytes.TrimSpace(res.Structured); len(p) > 0 {
		return p
	}
	if t := strings.TrimSpace(res.Text); t != "" {
		return []byte(t)
	}
	return nil
}

// parseRows decodes a listing payload. It accepts a bare array or an object under
// a known list key. An empty object is an empty listing; any other unknown shape
// fails closed.
func parseRows(res mcp.CallResult) ([]remoteRow, error) {
	payload := payloadOf(res)
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var rows []remoteRow
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, &Error{Code: CodeReadSchema, Message: "listing payload was not valid JSON"}
		}
		return rows, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, &Error{Code: CodeReadSchema, Message: "listing payload was not a JSON object"}
	}
	if len(obj) == 0 {
		return nil, nil
	}
	for _, key := range []string{"entries", "items", "data", "results", "rows"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var rows []remoteRow
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, &Error{Code: CodeReadSchema, Message: "listing rows were not valid JSON"}
		}
		return rows, nil
	}
	return nil, &Error{Code: CodeReadSchema, Message: "listing payload had no recognized entry list"}
}

// parseClassifications decodes a classifications payload.
func parseClassifications(res mcp.CallResult) ([]Classification, error) {
	payload := payloadOf(res)
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var rows []classRow
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, &Error{Code: CodeReadSchema, Message: "classifications payload was not valid JSON"}
		}
	} else {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return nil, &Error{Code: CodeReadSchema, Message: "classifications payload was not a JSON object"}
		}
		if len(obj) == 0 {
			return nil, nil
		}
		found := false
		for _, key := range []string{"classifications", "items", "data", "results"} {
			raw, ok := obj[key]
			if !ok {
				continue
			}
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nil, &Error{Code: CodeReadSchema, Message: "classification rows were not valid JSON"}
			}
			found = true
			break
		}
		if !found {
			return nil, &Error{Code: CodeReadSchema, Message: "classifications payload had no recognized list"}
		}
	}

	out := make([]Classification, 0, len(rows))
	for _, r := range rows {
		c := r.toClassification()
		if c.ID == "" && c.Name == "" {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// parseWriteEntry extracts the observed entry from a confirmed write result. It
// accepts a bare entry object or a wrapper under "entry".
func parseWriteEntry(res mcp.CallResult) *remoteRow {
	trimmed := bytes.TrimSpace(payloadOf(res))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		if raw, ok := obj["entry"]; ok {
			var row remoteRow
			if json.Unmarshal(raw, &row) == nil {
				return &row
			}
		}
	}
	var row remoteRow
	if err := json.Unmarshal(trimmed, &row); err == nil {
		return &row
	}
	return nil
}

// mergeObserved fills fields the write response omitted from the planned entry.
func mergeObserved(row remoteRow, planned PlannedEntry, now time.Time) workflow.RemoteState {
	state := row.toRemoteState(now)
	if state.Date == "" {
		state.Date = planned.Date
	}
	if state.StartTime == "" {
		state.StartTime = planned.StartTime
	}
	if state.EndTime == "" {
		state.EndTime = planned.EndTime
	}
	if state.ClassificationID == "" {
		state.ClassificationID = planned.ClassificationID
	}
	if state.Description == "" {
		state.Description = planned.Description
	}
	if state.Task == "" {
		state.Task = planned.Task
	}
	if state.Billable == nil {
		state.Billable = planned.Billable
	}
	return state
}
