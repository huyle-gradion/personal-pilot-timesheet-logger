// Package gradion is the Gradion Workspace domain client over the generic
// internal/mcp transport. It speaks the verified MCP tool contract (initialize,
// tools/list, tools/call), maps observed rows into the S1 workflow contract, and
// classifies write outcomes (confirmed / uncertain / unidentified / failed).
//
// It is hermetic by construction: no live MCP is performed here, there is no REST
// fallback, and no secret is ever placed in an error or a record.
package gradion

import (
	"fmt"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// MCP tool names (single source of truth).
const (
	ToolListEntries        = "timesheet__list_my_entries"
	ToolGetMyWeek          = "timesheet__get_my_week"
	ToolLogTime            = "timesheet__log_time"
	ToolEditTime           = "timesheet__edit_time"
	ToolGetClassifications = "timesheet__get_classifications"
)

// Named bounds (local policy; the tool schemas are the server's contract).
const (
	MaxPageLimit        = 50
	MaxDescriptionBytes = 3000
	MaxTaskBytes        = 150
	MaxReasonBytes      = 200
	MaxClassifications  = 100
	MaxListingPages     = 20
	MaxListingEntries   = 1000
	// MaxClassificationResults is the get_classifications tool's result cap
	// (verified live: max 20). Reaching it means the catalog may be truncated.
	MaxClassificationResults = 20
	// MaxWeeks bounds the number of get_my_week calls used to cover a date range
	// (roughly a year). Hitting it marks the listing incomplete.
	MaxWeeks = 60
)

// Stable, secret-free domain error codes.
const (
	// CodeToolUnavailable is returned when a required tool is absent from
	// tools/list. There is NO REST fallback: the run stops.
	CodeToolUnavailable = "MCP_ERR_TOOL_UNAVAILABLE"
	// CodeListingIncomplete is returned when absence cannot be established
	// because the listing was not complete. It is never a false "not found":
	// absence is claimed only from a listing that passed the stability re-check.
	// A concurrent remote mutation within the scan window remains a documented
	// residual (like the no-idempotency-key B3 residual).
	CodeListingIncomplete = "MCP_ERR_LISTING_INCOMPLETE"
	// CodeCapabilitiesUnverified is returned when a read/write is attempted
	// before VerifyCapabilities succeeded. It fails closed.
	CodeCapabilitiesUnverified = "GRADION_ERR_CAPABILITIES_UNVERIFIED"
	// CodeNilSession is a programmer error: the client has no MCP session.
	CodeNilSession = "GRADION_ERR_NIL_SESSION"

	CodeReadToolError = "GRADION_ERR_READ_TOOL_ERROR"
	CodeReadSchema    = "GRADION_ERR_READ_SCHEMA"
	// CodeCatalogUnparsed is returned when a get_classifications text block is
	// present but no line matches the classification pattern. The catalog is the
	// only source of Code + Activity Type, so an unparseable catalog fails closed
	// rather than silently yielding an empty (and therefore unusable) selection.
	CodeCatalogUnparsed = "GRADION_ERR_CATALOG_UNPARSED"

	CodeArgOutOfBounds     = "GRADION_ERR_ARG_OUT_OF_BOUNDS"
	CodeReasonRequired     = "GRADION_ERR_REASON_REQUIRED"
	CodeReasonTooLong      = "GRADION_ERR_REASON_TOO_LONG"
	CodeDescriptionTooLong = "GRADION_ERR_DESCRIPTION_TOO_LONG"
	CodeTaskTooLong        = "GRADION_ERR_TASK_TOO_LONG"
	CodeLimitInvalid       = "GRADION_ERR_LIMIT_INVALID"
	CodeEmptyRemoteID      = "GRADION_ERR_EMPTY_REMOTE_ID"

	// Write outcome codes.
	CodeWriteUncertain    = "GRADION_WRITE_UNCERTAIN"
	CodeWriteFailed       = "GRADION_WRITE_FAILED"
	CodeWriteUnidentified = "GRADION_WRITE_UNIDENTIFIED"
	// CodeWriteContentUnverified is recorded when an identity-acknowledged write
	// could not be confirmed by a bounded stable-ID read-back: the entry was
	// absent, the read-back failed, or absence could not be established. The
	// write stays UNRESOLVED (never retried).
	CodeWriteContentUnverified = "GRADION_WRITE_CONTENT_UNVERIFIED"
	// CodeWriteContentMismatch is recorded when the read-back found the entry but
	// its content did not match the plan. The write stays UNRESOLVED (never
	// retried); a server-normalized value reads as a mismatch, never a false
	// confirm.
	CodeWriteContentMismatch = "GRADION_WRITE_CONTENT_MISMATCH"
	// CodeUnobservedEntry is a programmer error: BuildMapping was asked to bind a
	// mapping to a state that was not observation-derived (ContentObserved false),
	// e.g. a plan echo. Identity is never fabricated from a plan.
	CodeUnobservedEntry = "GRADION_ERR_UNOBSERVED_ENTRY"
)

// Error is a typed, secret-free domain error.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }

// CodeOf returns the stable code of a gradion.Error, or "".
func CodeOf(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

// Classification is a project/activity the caller can log against.
type Classification struct {
	ID   string
	Name string
	Code string
}

// Listing is the bounded, deduplicated result of ListEntries. Complete is true
// only when the scan terminated on a short page with no duplicates and no
// truncation.
type Listing struct {
	// Observed is true ONLY when a REAL remote read produced this listing (see
	// Client.ListEntries). The offline path never sets it: an offline listing is
	// UNOBSERVED and carries no ObservedAt, because no local timestamp may stand
	// in for a remote observation. Complete is meaningful only when Observed is
	// true.
	Observed   bool
	Entries    []workflow.RemoteState
	Complete   bool
	Pages      int
	Truncated  int
	Duplicates int
	ObservedAt time.Time
}

// WriteStatus classifies a remote write result. WriteIdentified is an IDENTITY
// acknowledgement only: the server confirmed the stable entry id but its CONTENT
// was not observed in the response, so the write is NOT confirmed and must be
// verified by a bounded read-back before a mapping is recorded.
type WriteStatus string

const (
	WriteConfirmed    WriteStatus = "confirmed"
	WriteIdentified   WriteStatus = "identified"
	WriteUncertain    WriteStatus = "uncertain"
	WriteUnidentified WriteStatus = "unidentified"
	WriteFailed       WriteStatus = "failed"
)

// WriteResult is the classified outcome of one write.
//
// RemoteID is the acknowledged stable id whenever the server identified the
// entry, even for an unconfirmed (WriteIdentified/uncertain) result, so the
// caller can perform a bounded read-back. Entry is non-nil ONLY when content was
// actually observed (a complete structured response, or a verified read-back);
// it always carries ContentObserved=true. A WriteIdentified result has a non-empty
// RemoteID and a nil Entry.
type WriteResult struct {
	Status    WriteStatus
	RemoteID  string
	Entry     *workflow.RemoteState
	ErrorCode string
}

// CreateRequest is a planned create (timesheet__log_time).
type CreateRequest struct {
	LocalID          string
	Date             string
	StartTime        string
	EndTime          string
	Description      string
	ClassificationID string
	Task             string
	Billable         *bool
}

// EditRequest is a planned edit (timesheet__edit_time). Reason is the required
// audit reason.
type EditRequest struct {
	LocalID          string
	EntryID          string
	Reason           string
	Date             string
	StartTime        string
	EndTime          string
	Description      string
	ClassificationID string
	Task             string
	Billable         *bool
}

// PlannedEntry is the pre-write local plan used to build mappings and outcomes.
// RemoteID is the planned remote id: empty for a create, the entry id for an edit.
type PlannedEntry struct {
	RemoteID         string
	Date             string
	StartTime        string
	EndTime          string
	ClassificationID string
	Description      string
	Task             string
	Billable         *bool
}

// Planned projects a CreateRequest onto the pre-write plan.
func (r CreateRequest) Planned() PlannedEntry {
	return PlannedEntry{
		Date: r.Date, StartTime: r.StartTime, EndTime: r.EndTime,
		ClassificationID: r.ClassificationID, Description: r.Description,
		Task: r.Task, Billable: r.Billable,
	}
}

// Planned projects an EditRequest onto the pre-write plan.
func (r EditRequest) Planned() PlannedEntry {
	return PlannedEntry{
		RemoteID: r.EntryID,
		Date:     r.Date, StartTime: r.StartTime, EndTime: r.EndTime,
		ClassificationID: r.ClassificationID, Description: r.Description,
		Task: r.Task, Billable: r.Billable,
	}
}
