package gradion

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// classifyWrite maps a dispatched write error to a classified WriteResult.
//
// Uncertainty (no retry, must be reconciled) covers outcomes that do not prove
// the write was rejected: transport/timeout/cancellation, ambiguous HTTP statuses
// (408/409/425/429 and 5xx), a tool-level error inside a 200, non-request
// JSON-RPC error codes, and a malformed 200 (mcp.ErrSchema — S5L1-5: a write must
// never be treated as a definitive failure just because its response was
// unreadable). Definitive failures cover request-error JSON-RPC codes and
// unambiguous 4xx rejections.
func classifyWrite(err error) WriteResult {
	var me *mcp.Error
	if errors.As(err, &me) {
		switch me.Kind {
		case mcp.ErrHTTPStatus:
			if isAmbiguousStatus(me.StatusCode) {
				return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}
			}
			return WriteResult{Status: WriteFailed, ErrorCode: CodeWriteFailed}
		case mcp.ErrTransport:
			return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}
		case mcp.ErrProtocol:
			if isRequestErrorCode(me.Code) {
				return WriteResult{Status: WriteFailed, ErrorCode: CodeWriteFailed}
			}
			return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}
		case mcp.ErrSchema:
			// S5L1-5: an unreadable 200 must be treated as uncertain, not failed.
			return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}
		case mcp.ErrUninitialized:
			return WriteResult{Status: WriteFailed, ErrorCode: CodeWriteFailed}
		}
	}
	// Unknown error: conservatively uncertain; never assume rejection.
	return WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}
}

// isAmbiguousStatus reports whether a non-2xx status fails to establish that the
// mutation was rejected. 408/409/425/429 and 5xx are uncertain; other 4xx are
// definitive rejections.
func isAmbiguousStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return status >= 500 && status <= 599
}

// isRequestErrorCode reports whether a JSON-RPC error code is a request error
// (definitive: the request was never executed as a mutation).
func isRequestErrorCode(code int) bool {
	switch code {
	case -32600, -32601, -32602, -32700: // invalid request, method not found, invalid params, parse error
		return true
	}
	return false
}

// BuildMapping binds a local id to a confirmed remote entry. It errors on an
// empty remote id: identity is never fabricated from time-slot equality.
func BuildMapping(localID string, planned PlannedEntry, entry workflow.RemoteState, now time.Time) (workflow.RemoteMapping, error) {
	if strings.TrimSpace(entry.RemoteID) == "" {
		return workflow.RemoteMapping{}, &Error{
			Code:    CodeEmptyRemoteID,
			Message: "remote id required; identity is never fabricated",
		}
	}
	if entry.ObservedAt.IsZero() {
		entry.ObservedAt = now
	}
	return workflow.RemoteMapping{
		LocalID:          localID,
		RemoteID:         entry.RemoteID,
		Date:             planned.Date,
		StartTime:        planned.StartTime,
		EndTime:          planned.EndTime,
		ClassificationID: planned.ClassificationID,
		Description:      planned.Description,
		LastObserved:     entry,
		CreatedAt:        now,
		UpdatedAt:        now,
	}, nil
}

// BuildOutcome builds the durable S1 outcome for a write. Confirmed is resolved
// with the observed remote id; failed is a resolved definitive rejection;
// uncertain and unidentified are UNRESOLVED with a stable error code. The
// operation key is derived from the PRE-WRITE plan so it matches the in_flight
// intent row.
func BuildOutcome(action, localID string, planned PlannedEntry, res WriteResult, now time.Time) workflow.OperationOutcome {
	out := workflow.OperationOutcome{
		LocalID:     localID,
		Action:      action,
		Date:        planned.Date,
		StartTime:   planned.StartTime,
		EndTime:     planned.EndTime,
		Description: planned.Description,
		RecordedAt:  now,
	}

	switch res.Status {
	case WriteConfirmed:
		// A confirmed write MUST carry a stable remote id. An empty/nil entry is
		// downgraded to unidentified: identity is never fabricated.
		if res.Entry == nil || strings.TrimSpace(res.Entry.RemoteID) == "" {
			out.Status = workflow.OutcomeUnidentified
			out.ErrorCode = orDefault(res.ErrorCode, CodeWriteUnidentified)
			out.Resolved = false
			break
		}
		out.RemoteID = res.Entry.RemoteID
		out.Status = workflow.OutcomeConfirmed
		out.Resolved = true
	case WriteUnidentified:
		out.Status = workflow.OutcomeUnidentified
		out.ErrorCode = orDefault(res.ErrorCode, CodeWriteUnidentified)
		out.Resolved = false
	case WriteFailed:
		out.Status = workflow.OutcomeFailed
		out.ErrorCode = orDefault(res.ErrorCode, CodeWriteFailed)
		out.Resolved = true
	default: // uncertain
		out.Status = workflow.OutcomeUncertain
		out.ErrorCode = orDefault(res.ErrorCode, CodeWriteUncertain)
		out.Resolved = false
	}

	out.OperationKey = workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date:      planned.Date,
		Action:    action,
		RemoteID:  planned.RemoteID,
		LocalID:   localID,
		StartTime: planned.StartTime,
		EndTime:   planned.EndTime,
	})
	return out
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
