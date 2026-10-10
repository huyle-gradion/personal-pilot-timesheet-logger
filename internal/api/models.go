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

// Classification represents a timesheet classification category.
type Classification struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
