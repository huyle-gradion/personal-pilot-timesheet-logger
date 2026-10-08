package api

import (
	"time"
)

// DefaultClassificationID is the standard classification ID for primary dev work.
const DefaultClassificationID = "156b7d0e-f29c-470d-b90c-b6b67df4c21a"

// DefaultBaseURL is the production Gradion Timesheet API URL.
const DefaultBaseURL = "https://ts-prod.gradion.com"

// Named TigerStyle bounds with explicit units.
const (
	MaxRemoteEntriesCount int           = 200
	MaxLocalBlocksCount   int           = 100
	MaxAPIRetriesCount    int           = 3
	APITimeoutDuration    time.Duration = 10 * time.Second
	BackoffBaseDuration   time.Duration = 250 * time.Millisecond
	MaxResponseBodyBytes  int64         = 5 * 1024 * 1024 // 5 MiB
)

// Invariant assertion identifiers per TigerStyle.
const (
	AssertAPINilContext     = "ASSERT_API_001_NIL_CONTEXT"
	AssertAPINilClient      = "ASSERT_API_002_NIL_CLIENT"
	AssertAPIZeroDate       = "ASSERT_API_003_ZERO_DATE"
	AssertAPIEmptyToken     = "ASSERT_API_004_EMPTY_TOKEN"
	AssertAPIBaseURLEmpty   = "ASSERT_API_005_EMPTY_BASE_URL"
	AssertAPISyncWritersNil = "ASSERT_SYNC_001_NIL_WRITERS"
)

// RemoteEntry represents a timesheet entry as returned by the Gradion API.
type RemoteEntry struct {
	ID               string `json:"id"`
	Date             string `json:"date"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	ClassificationID string `json:"classificationId"`
	Description      string `json:"description"`
	Task             string `json:"task,omitempty"`
}

// RemoteEntriesResponse represents the envelope for entries queries.
type RemoteEntriesResponse struct {
	Total   int           `json:"total"`
	Entries []RemoteEntry `json:"entries"`
}

// CreateEntryRequest represents the payload for creating a new timesheet entry.
type CreateEntryRequest struct {
	Date             string `json:"date"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	ClassificationID string `json:"classificationId"`
	Description      string `json:"description"`
}

// PatchEntryRequest represents the payload for modifying an existing timesheet entry.
type PatchEntryRequest struct {
	DTO    PatchEntryDTO `json:"dto"`
	Reason string        `json:"reason"`
}

// PatchEntryDTO represents the inner update object for a timesheet entry.
type PatchEntryDTO struct {
	Description string `json:"description"`
	Task        string `json:"task,omitempty"`
}

// Classification represents a timesheet classification category.
type Classification struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReconciliationAction represents the planned mutation or skip decision.
type ReconciliationAction string

const (
	ActionCreate ReconciliationAction = "create"
	ActionUpdate ReconciliationAction = "update"
	ActionSkip   ReconciliationAction = "skip"
	ActionDelete ReconciliationAction = "delete"
)

// ReconciledItem represents the diff outcome for a specific time slot.
type ReconciledItem struct {
	Action           ReconciliationAction `json:"action"`
	RemoteID         string               `json:"remote_id,omitempty"`
	LocalBlockID     string               `json:"local_block_id,omitempty"`
	Date             string               `json:"date"`
	StartTime        string               `json:"start_time"`
	EndTime          string               `json:"end_time"`
	Description      string               `json:"description"`
	ClassificationID string               `json:"classification_id"`
	Reason           string               `json:"reason,omitempty"`
}

// SyncSummary aggregates the execution metrics for a reconciliation run.
type SyncSummary struct {
	Date       string           `json:"date"`
	Created    int              `json:"created"`
	Updated    int              `json:"updated"`
	Skipped    int              `json:"skipped"`
	Deleted    int              `json:"deleted"`
	TotalHours float64          `json:"total_hours"`
	Items      []ReconciledItem `json:"items"`
}
