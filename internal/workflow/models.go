package workflow

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// RemoteState captures the last observed remote shape for a mapped entry. It is
// built exclusively from a remote API response and never carries credentials,
// bearer tokens, iCal URLs, or raw private API error bodies.
//
// Task and Billable are ADDITIVE S5 fields (omitempty) so a later reconciliation
// (S6) can detect changes to those attributes. SchemaVersion stays 1.
type RemoteState struct {
	RemoteID         string    `json:"remote_id"`
	Date             string    `json:"date"`
	StartTime        string    `json:"start_time"`
	EndTime          string    `json:"end_time"`
	ClassificationID string    `json:"classification_id"`
	Description      string    `json:"description"`
	ObservedAt       time.Time `json:"observed_at"`
	Task             string    `json:"task,omitempty"`
	Billable         *bool     `json:"billable,omitempty"`
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
//
// The fields below Kind are ADDITIVE S4 review fields (omitempty). Kind records
// the block kind (work|meeting|joint|manual) so a joint meeting+commit interval
// is representable. Manual marks a user-entered interval. OriginalStart/End keep
// the pre-adjustment display times; GridAdjusted marks a grid suggestion;
// SplitGroupID/PartIndex link split fragments; StartOffsetMin/EndOffsetMin record
// the trim delta in minutes.
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

	Kind           string   `json:"kind,omitempty"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
	Manual         bool     `json:"manual,omitempty"`
	OriginalStart  string   `json:"original_start,omitempty"`
	OriginalEnd    string   `json:"original_end,omitempty"`
	GridAdjusted   bool     `json:"grid_adjusted,omitempty"`
	SplitGroupID   string   `json:"split_group_id,omitempty"`
	PartIndex      int      `json:"part_index,omitempty"`
	StartOffsetMin int      `json:"start_offset_min,omitempty"`
	EndOffsetMin   *int     `json:"end_offset_min,omitempty"`
	// ClassificationID is the ADDITIVE S6 review override for the Gradion
	// classification. When empty, the configured project->classification resolver
	// supplies it; when neither resolves, submission is blocked.
	ClassificationID string `json:"classification_id,omitempty"`
}

// ExcludedEvidence records a review-time decision to exclude an evidence item
// from confirmed work. The evidence itself is RETAINED (a not-attended meeting
// stays visible calendar evidence); only its inclusion in work is excluded.
type ExcludedEvidence struct {
	EvidenceID string    `json:"evidence_id"`
	Reason     string    `json:"reason"`
	ExcludedAt time.Time `json:"excluded_at"`
}

// AttendanceDecision records an explicit attendance decision for a meeting.
type AttendanceDecision struct {
	EvidenceID string    `json:"evidence_id"`
	Attended   bool      `json:"attended"`
	DecidedAt  time.Time `json:"decided_at"`
}

// ReviewState records the durable review outcome for a day. A reviewed empty day
// is Empty with no Confirmed intervals and no zero-hour block.
type ReviewState struct {
	Reviewed            bool                 `json:"reviewed"`
	Empty               bool                 `json:"empty"`
	ReviewedAt          time.Time            `json:"reviewed_at"`
	ExcludedEvidence    []ExcludedEvidence   `json:"excluded_evidence,omitempty"`
	Attendance          []AttendanceDecision `json:"attendance,omitempty"`
	AcknowledgedSources []string             `json:"acknowledged_sources,omitempty"`
	LastProposalHash    string               `json:"last_proposal_hash,omitempty"`
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

// PlanOpKind classifies a planned remote operation. There is deliberately no
// delete kind: remote entries are editable but not removable.
type PlanOpKind string

const (
	PlanOpCreate PlanOpKind = "create"
	PlanOpEdit   PlanOpKind = "edit"
)

// PlanOperation is one planned remote write. RemoteID is empty for a create.
// ExpectedRemote is the observed remote content the plan assumed for an edit
// (nil for a create). Reason is the fixed audit reason for an edit.
//
// Task and Billable are NOT populated by the S6 planning layer: layer 2 sources
// them from ExpectedRemote for edits (a partial edit preserves remote-only
// fields), and a create has no local source for them.
type PlanOperation struct {
	LocalID          string       `json:"local_id"`
	Kind             PlanOpKind   `json:"kind"`
	RemoteID         string       `json:"remote_id,omitempty"`
	Date             string       `json:"date"`
	StartTime        string       `json:"start_time"`
	EndTime          string       `json:"end_time"`
	ClassificationID string       `json:"classification_id"`
	Description      string       `json:"description"`
	Task             string       `json:"task,omitempty"`
	Billable         *bool        `json:"billable,omitempty"`
	Reason           string       `json:"reason,omitempty"`
	ExpectedRemote   *RemoteState `json:"expected_remote,omitempty"`
}

// SubmissionPlan is a content-addressed plan for one day. PlanID is the plan
// content hash (PlanHash). RecordRevision is audit-only and excluded from the
// hash so a revision bump does not invalidate an otherwise-identical plan.
type SubmissionPlan struct {
	PlanID           string          `json:"plan_id"`
	Date             string          `json:"date"`
	LocalFingerprint string          `json:"local_fingerprint"`
	RecordRevision   int             `json:"record_revision"`
	RemoteObservedAt time.Time       `json:"remote_observed_at"`
	RemoteComplete   bool            `json:"remote_complete"`
	Operations       []PlanOperation `json:"operations"`
	CreatedAt        time.Time       `json:"created_at"`
}

// Approval binds a plan to its content and the remote assumptions it was
// approved against. ApprovalID is the approved plan content hash. An approval is
// single-use: any local, plan, or remote change invalidates it.
type Approval struct {
	ApprovalID        string     `json:"approval_id"`
	PlanID            string     `json:"plan_id"`
	Date              string     `json:"date"`
	RecordRevision    int        `json:"record_revision"`
	RemoteObservedAt  time.Time  `json:"remote_observed_at"`
	ApprovedAt        time.Time  `json:"approved_at"`
	Superseded        bool       `json:"superseded,omitempty"`
	InvalidatedAt     *time.Time `json:"invalidated_at,omitempty"`
	InvalidatedReason string     `json:"invalidated_reason,omitempty"`
}

// Measurement is a possibly-unknown integer measurement (ADDITIVE S7). Known==false
// means the value is UNKNOWN and must never be read as zero: an unavailable
// measurement is recorded honestly, never as zero.
type Measurement struct {
	Known bool  `json:"known"`
	Value int64 `json:"value"`
}

// UnknownMeasurement returns a measurement whose value is not known.
func UnknownMeasurement() Measurement { return Measurement{Known: false} }

// KnownMeasurement returns a concrete measurement.
func KnownMeasurement(value int64) Measurement { return Measurement{Known: true, Value: value} }

// IsUnknown reports whether the measurement is unknown (and must not be read as
// zero).
func (m Measurement) IsUnknown() bool { return !m.Known }

// EditorialText is an editorial title/description pair. It is TEXT ONLY and can
// never alter intervals, attendance, projects, evidence, or approvals.
type EditorialText struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// EditorialSuggestion is one target's durable editorial result (ADDITIVE S7).
// RequestHash is the request hash the text was produced for, so an unchanged
// request can be reused with zero tokens. Editorial suggestions are TEXT ONLY and
// deliberately do NOT feed LocalFingerprint or PlanHash.
type EditorialSuggestion struct {
	TargetID    string        `json:"target_id"`
	RequestHash string        `json:"request_hash"`
	Text        EditorialText `json:"text"`
	Origin      string        `json:"origin"`
	Warnings    []string      `json:"warnings,omitempty"`
}

// EditorialUsage is the durable daily usage report (ADDITIVE S7). Editorial and
// orchestration usage are reported SEPARATELY and are never summed: an unknown
// measurement is not zero.
type EditorialUsage struct {
	Date          string      `json:"date"`
	Editorial     Measurement `json:"editorial"`
	Orchestration Measurement `json:"orchestration"`
	EditorialRuns int         `json:"editorial_runs"`
	Invoked       bool        `json:"invoked"`
	RuntimeMS     int64       `json:"runtime_ms"`
	PeakRSSMB     float64     `json:"peak_rss_mb"`
}

// Record is the durable per-day workflow document. It is stored separately from
// the legacy storage.TimesheetRecord and never overwrites or migrates it.
//
// CollectedEvidence and Collection are ADDITIVE S3 fields (omitempty). They hold
// the raw collected evidence and the collection summary for the day. The legacy
// Evidence []EvidenceReference field is a separate representation and is left
// untouched.
//
// Plans and Approvals are ADDITIVE S6 fields (omitempty). A SubmissionPlan is a
// content-addressed plan of create/edit operations; an Approval binds a plan to
// its content and remote assumptions. SchemaVersion stays 1.
//
// Editorial and Usage are ADDITIVE S7 fields (omitempty). Editorial holds TEXT
// ONLY suggestions (never intervals/attendance/projects/evidence/approvals); Usage
// holds the daily editorial/orchestration measurement report. SchemaVersion stays
// 1.
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
	Review            *ReviewState                `json:"review,omitempty"`
	Plans             []SubmissionPlan            `json:"plans,omitempty"`
	Approvals         []Approval                  `json:"approvals,omitempty"`
	Editorial         []EditorialSuggestion       `json:"editorial,omitempty"`
	Usage             *EditorialUsage             `json:"usage,omitempty"`
}
