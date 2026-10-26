package timesheet

import (
	"testing"
	"time"
)

// TestTodayInUsesTheConfiguredZone is the daily-driver regression: the same
// instant must resolve to different civil dates when the configured timezone
// differs from the host's, because the binary - not the model's clock - owns
// "today".
func TestTodayInUsesTheConfiguredZone(t *testing.T) {
	instant := mustParseInstant(t, "2026-10-26T18:00:00Z") // 01:00 next day in +07

	cases := []struct {
		zone string
		want string
	}{
		{"Asia/Ho_Chi_Minh", "2026-10-27"},
		{"UTC", "2026-10-26"},
	}
	for _, tc := range cases {
		got, err := TodayIn(tc.zone, instant)
		if err != nil {
			t.Fatalf("TodayIn(%q): %v", tc.zone, err)
		}
		if got != tc.want {
			t.Fatalf("TodayIn(%q) = %q, want %q", tc.zone, got, tc.want)
		}
	}
}

// TestTodayInRejectsAnUnusableZone: no zone means no honest answer, so the
// helper fails closed instead of assuming UTC.
func TestTodayInRejectsAnUnusableZone(t *testing.T) {
	instant := mustParseInstant(t, "2026-10-26T18:00:00Z")
	for _, zone := range []string{"", "  ", "Not/AZone"} {
		if _, err := TodayIn(zone, instant); err == nil {
			t.Fatalf("TodayIn(%q) must fail closed", zone)
		}
	}
}

func mustParseInstant(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}
