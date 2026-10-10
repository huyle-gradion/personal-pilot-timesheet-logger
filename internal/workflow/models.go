package workflow

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// RemoteState captures the last observed remote shape for a mapped entry. It is
// built exclusively from a remote API response and never carries credentials,
// bearer tokens, iCal URLs, or raw private API error bodies.
type RemoteState struct {
	RemoteID         string    `json:"remote_id"`
	Date             string    `json:"date"`
	StartTime        string    `json:"start_time"`
	EndTime          string    `json:"end_time"`
	ClassificationID string    `json:"classification_id"`
	Description      string    `json:"description"`
	ObservedAt       time.Time `json:"observed_at"`
}

// EvidenceReference records a non-secret provenance pointer (repo/PR/branch)
// that supports a confirmed interval. Ref holds only repository provenance.
type EvidenceReference struct {
	ID         string    `json:"id"`
	Source     string    `json:"source"`
	Timestamp  string    `json:"timestamp"`
	EndTime    string    `json:"end_time"`
	Title      string    `json:"title"`
	Ref        string    `json:"ref"`
	ObservedAt time.Time `json:"observed_at"`
}

// ConfirmedInterval is a user-confirmed interval. User-authored Description and
// Project are preserved across evidence refreshes.
type ConfirmedInterval struct {
	ID          string    `json:"id"`
	Date        string    `json:"date"`
	StartTime   string    `json:"start_time"`
	EndTime     string    `json:"end_time"`
	Project     string    `json:"project"`
	Description string    `json:"description"`
	Attendance  bool      `json:"attendance"`
	ConfirmedAt time.Time `json:"confirmed_at"`
	Revision    int       `json:"revision"`
}

// RemoteMapping binds a local interval identity to a stable remote ID. Identity
// is created only from a real remote response; it is never inferred from
// time-slot equality. This model is the mapping REPRESENTATION only: this store
// populates mappings from confirmed WRITE responses. Populating mappings from
// remote reads, skip, or adoption decisions belongs to the reconciliation slice
// (S6), not this model.
type RemoteMapping struct {
	LocalID          string      `json:"local_id"`
	RemoteID         string      `json:"remote_id"`
	Date             string      `json:"date"`
	StartTime        string      `json:"start_time"`
	EndTime          string      `json:"end_time"`
	ClassificationID string      `json:"classification_id"`
	Description      string      `json:"description"`
	LastObserved     RemoteState `json:"last_observed"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// OutcomeStatus classifies the durable result of a single executed operation.
type OutcomeStatus string

const (
	OutcomeConfirmed OutcomeStatus = "confirmed"
	OutcomeFailed    OutcomeStatus = "failed"
	OutcomeUncertain OutcomeStatus = "uncertain"
	OutcomeSkipped   OutcomeStatus = "skipped"
	// OutcomeUnidentified records a remote write that returned success but no
	// stable remote entry ID, so no mapping can be created; reconciliation is
	// required.
	OutcomeUnidentified OutcomeStatus = "unidentified"
	// OutcomeInFlight is a durable PRE-WRITE intent: it records that an
	// operation is about to be dispatched remotely but has not yet produced a
	// terminal outcome. An in_flight row found on restart is UNRESOLVED and
	// must block replay until reconciled (S6 owns reconciliation). It is never
	// a terminal success.
	OutcomeInFlight OutcomeStatus = "in_flight"
)

// OperationOutcome records the durable result of one executed operation. ErrorCode
// carries only a STABLE code string; raw error messages/bodies are never stored.
type OperationOutcome struct {
	OperationKey string        `json:"operation_key"`
	LocalID      string        `json:"local_id"`
	RemoteID     string        `json:"remote_id"`
	Action       string        `json:"action"`
	Status       OutcomeStatus `json:"status"`
	Date         string        `json:"date"`
	StartTime    string        `json:"start_time"`
	EndTime      string        `json:"end_time"`
	Description  string        `json:"description"`
	ErrorCode    string        `json:"error_code"`
	Resolved     bool          `json:"resolved"`
	RecordedAt   time.Time     `json:"recorded_at"`
	// IntentAt is stamped when the operation's pre-write intent was persisted.
	// It is distinct from RecordedAt (last update time) and lets a restart
	// report when an unresolved in_flight operation began.
	IntentAt time.Time `json:"intent_at,omitempty"`
}

// Record is the durable per-day workflow document. It is stored separately from
// the legacy storage.TimesheetRecord and never overwrites or migrates it.
//
// CollectedEvidence and Collection are ADDITIVE S3 fields (omitempty). They hold
// the raw collected evidence and the collection summary for the day. The legacy
// Evidence []EvidenceReference field is a separate representation and is left
// untouched.
type Record struct {
	SchemaVersion     int                         `json:"schema_version"`
	Date              string                      `json:"date"`
	Revision          int                         `json:"revision"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	Evidence          []EvidenceReference         `json:"evidence"`
	Confirmed         []ConfirmedInterval         `json:"confirmed"`
	RemoteMappings    []RemoteMapping             `json:"remote_mappings"`
	Outcomes          []OperationOutcome          `json:"outcomes"`
	CollectedEvidence []evidence.Evidence         `json:"collected_evidence,omitempty"`
	Collection        *evidence.CollectionSummary `json:"collection,omitempty"`
}
