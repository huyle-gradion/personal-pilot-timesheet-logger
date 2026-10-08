package storage

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// TimesheetRecord represents the persisted daily timesheet document.
type TimesheetRecord struct {
	Date       string                `json:"date"`       // Format: "YYYY-MM-DD"
	UpdatedAt  time.Time             `json:"updated_at"` // ISO-8601 UTC timestamp
	Version    int                   `json:"version"`    // Incremented on every upsert
	TotalHours float64               `json:"total_hours"`
	Blocks     []timesheet.TimeBlock `json:"blocks"`
}
