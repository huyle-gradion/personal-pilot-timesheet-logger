package timesheet

type SimpleEntry struct {
	ID          string         `json:"id"`
	Source      string         `json:"source"`
	Timestamp   string         `json:"timestamp"`
	EndTime     string         `json:"endTime"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Metadata    *EntryMetadata `json:"metadata,omitempty"`
}

type EntryMetadata struct {
	Status          string  `json:"status,omitempty"`
	Location        *string `json:"location,omitempty"`
	DurationMinutes int     `json:"duration_minutes,omitempty"`
	AllDay          bool    `json:"all_day,omitempty"`
	Transparent     bool    `json:"transparent,omitempty"`
	Repo            string  `json:"repo,omitempty"`
	Branch          string  `json:"branch,omitempty"`
	PRNumber        int     `json:"pr_number,omitempty"`
}
