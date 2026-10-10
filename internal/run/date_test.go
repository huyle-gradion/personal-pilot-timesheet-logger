package run

import (
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// F5: the default date is "today" in the CONFIGURED timezone; a pinned date wins.
func TestRunner_DateUsesConfiguredTimezone(t *testing.T) {
	store := newStore(t)
	now := time.Date(2026, 10, 6, 2, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	utc := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Now: clock}, Config{})
	if d, err := utc.Date(); err != nil || d != "2026-10-06" {
		t.Fatalf("want 2026-10-06 in UTC, got %q (%v)", d, err)
	}

	la := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Now: clock}, Config{
		Evidence: evidence.Config{Timezone: "America/Los_Angeles"},
	})
	if d, err := la.Date(); err != nil || d != "2026-10-05" {
		t.Fatalf("want 2026-10-05 in America/Los_Angeles (UTC-7), got %q (%v)", d, err)
	}

	pinned := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Now: clock}, Config{Date: "2026-01-02"})
	if d, err := pinned.Date(); err != nil || d != "2026-01-02" {
		t.Fatalf("a pinned date must win, got %q (%v)", d, err)
	}
}
