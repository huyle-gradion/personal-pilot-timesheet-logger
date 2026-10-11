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
	// A resource-reference envelope ({resources:[...]}) carries no structured
	// entries: it is never an empty timesheet. Use get_my_week for entry data.
	if _, ok := obj["resources"]; ok {
		return nil, &Error{Code: CodeReadSchema, Message: "listing returned resource references, not structured entries"}
	}
	if len(obj) == 0 {
		return nil, &Error{Code: CodeReadSchema, Message: "listing payload was an empty object"}
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

// --- verified live week envelope (B5) ---------------------------------------

// weekEntry is one entry inside a get_my_week day. It carries the FULL structured
// interval data the live server exposes (unlike list_my_entries, which returns
// references only). The DATE is not on the entry: it comes from the containing day.
// The server also returns classificationName/activityTypeLabel on each entry, but
// they are not decoded here: classification NAMES come from the get_classifications
// `label` (see classificationsFromReferences).
type weekEntry struct {
	ID               string `json:"id"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	ClassificationID string `json:"classificationId"`
	Description      string `json:"description"`
	DurationMinutes  int    `json:"durationMinutes"`
	Status           string `json:"status"`
	Task             string `json:"task"`
}

// weekDay is one day inside a get_my_week response.
type weekDay struct {
	Date    string      `json:"date"`
	Entries []weekEntry `json:"entries"`
}

// weekResponse is the structuredContent of get_my_week (verified live shape).
type weekResponse struct {
	Days      []weekDay `json:"days"`
	WeekStart string    `json:"weekStart"`
	MinDate   string    `json:"minDate"`
	MaxDate   string    `json:"maxDate"`
	Today     string    `json:"today"`
}

// toRemoteState maps a week entry (plus its day's date) into the S1 workflow
// contract. Billable is UNKNOWN: the live week shape does not expose it.
func (e weekEntry) toRemoteState(date string, observed time.Time) workflow.RemoteState {
	return workflow.RemoteState{
		RemoteID:         strings.TrimSpace(e.ID),
		Date:             strings.TrimSpace(date),
		StartTime:        strings.TrimSpace(e.StartTime),
		EndTime:          strings.TrimSpace(e.EndTime),
		ClassificationID: strings.TrimSpace(e.ClassificationID),
		Description:      e.Description,
		Task:             strings.TrimSpace(e.Task),
		ObservedAt:       observed,
	}
}

// parseWeek decodes a get_my_week structuredContent payload FAIL-CLOSED. The
// verified live shape is a JSON object with a "days" list; any other shape
// (including the reference envelope {resources:[...]}, a bare array, or an empty
// payload) is a schema error — a failed or unrecognized retrieval is NEVER treated
// as an empty timesheet.
func parseWeek(res mcp.CallResult) (weekResponse, error) {
	trimmed := bytes.TrimSpace(payloadOf(res))
	if len(trimmed) == 0 {
		return weekResponse{}, &Error{Code: CodeReadSchema, Message: "week payload was empty"}
	}
	if trimmed[0] != '{' {
		return weekResponse{}, &Error{Code: CodeReadSchema, Message: "week payload was not a JSON object"}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return weekResponse{}, &Error{Code: CodeReadSchema, Message: "week payload was not a JSON object"}
	}
	if _, ok := obj["days"]; !ok {
		return weekResponse{}, &Error{Code: CodeReadSchema, Message: "week payload had no days list"}
	}
	var resp weekResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return weekResponse{}, &Error{Code: CodeReadSchema, Message: "week days were not valid JSON"}
	}
	return resp, nil
}

// referenceItem is one item of the MCP resource-reference envelope.
type referenceItem struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// parseReferenceEnvelope decodes the verified {resources:[{id,kind,label}]}
// reference envelope returned by list_my_entries and get_classifications. It
// FAILS CLOSED on any other shape: an unrecognized retrieval is never treated as
// an empty result.
func parseReferenceEnvelope(res mcp.CallResult) ([]referenceItem, error) {
	trimmed := bytes.TrimSpace(payloadOf(res))
	if len(trimmed) == 0 {
		return nil, &Error{Code: CodeReadSchema, Message: "reference payload was empty"}
	}
	if trimmed[0] != '{' {
		return nil, &Error{Code: CodeReadSchema, Message: "reference payload was not a JSON object"}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, &Error{Code: CodeReadSchema, Message: "reference payload was not a JSON object"}
	}
	raw, ok := obj["resources"]
	if !ok {
		return nil, &Error{Code: CodeReadSchema, Message: "reference payload had no resources list"}
	}
	var items []referenceItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, &Error{Code: CodeReadSchema, Message: "reference items were not valid JSON"}
	}
	return items, nil
}
