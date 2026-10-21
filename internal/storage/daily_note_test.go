package storage

import "testing"

func TestDailyNoteFrontmatter(t *testing.T) {
	// Pin the shared template's exact schema/order: the journal collector reads
	// `date`, and the managed export seeds this same block on note creation.
	wantFrontmatter := "---\ntype: log\ndate: 2026-10-08\nlog_type: daily\ntags:\n  - daily-log\n  - timesheet\n---\n"
	if got := DailyNoteFrontmatter("2026-10-08"); got != wantFrontmatter {
		t.Fatalf("shared frontmatter template drifted:\n%q\nwant:\n%q", got, wantFrontmatter)
	}
}
