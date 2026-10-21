package storage

import "fmt"

// dailyNoteFrontmatterTemplate is the ONE daily-note YAML frontmatter schema,
// used by the managed export (internal/export). Format verb: the plain, unquoted
// YYYY-MM-DD date. The journal collector reads the `date` key to certify a
// note's date, so every created note must carry it.
const dailyNoteFrontmatterTemplate = `---
type: log
date: %s
log_type: daily
tags:
  - daily-log
  - timesheet
---
`

// DailyNoteFrontmatter returns the daily-note frontmatter for date. It is the
// single source of the template: callers must never mirror it.
func DailyNoteFrontmatter(date string) string {
	return fmt.Sprintf(dailyNoteFrontmatterTemplate, date)
}
