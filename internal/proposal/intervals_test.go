package proposal

import (
	"errors"
	"testing"
	"time"
)

func TestDurationMinutes(t *testing.T) {
	start := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)
	if got := DurationMinutes(start, end); got != 90 {
		t.Errorf("DurationMinutes = %d, want 90", got)
	}
	if got := DurationMinutes(end, start); got != 0 {
		t.Errorf("DurationMinutes(negative) = %d, want 0", got)
	}
}

// TestDurationMinutes_DSTCorrect proves absolute Sub is DST-correct.
func TestDurationMinutes_DSTCorrect(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// Spring forward 2026-03-08: 01:30 -> 03:30 is 60 real minutes.
	if got := DurationMinutes(
		time.Date(2026, 3, 8, 1, 30, 0, 0, ny),
		time.Date(2026, 3, 8, 3, 30, 0, 0, ny),
	); got != 60 {
		t.Errorf("spring-forward = %d, want 60", got)
	}
	// Fall back 2026-11-01: 00:30 -> 02:30 is 180 real minutes.
	if got := DurationMinutes(
		time.Date(2026, 11, 1, 0, 30, 0, 0, ny),
		time.Date(2026, 11, 1, 2, 30, 0, 0, ny),
	); got != 180 {
		t.Errorf("fall-back = %d, want 180", got)
	}
}

func TestSplitAtMidnight(t *testing.T) {
	start := time.Date(2026, 10, 6, 23, 30, 0, 0, time.UTC)
	end := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	frags := SplitAtMidnight(start, end, time.UTC)
	if len(frags) != 2 {
		t.Fatalf("fragments = %d, want 2", len(frags))
	}
	if frags[0].Minutes != 30 || frags[1].Minutes != 60 {
		t.Errorf("minutes = %d,%d, want 30,60", frags[0].Minutes, frags[1].Minutes)
	}
	if total := frags[0].Minutes + frags[1].Minutes; total != 90 {
		t.Errorf("total = %d, want 90", total)
	}
	if frags[0].Carryover || !frags[1].Carryover {
		t.Errorf("carryover flags wrong: %v/%v", frags[0].Carryover, frags[1].Carryover)
	}
	if frags[0].SplitGroupID != frags[1].SplitGroupID {
		t.Error("linked fragments must share SplitGroupID")
	}
	if frags[0].PartIndex != 0 || frags[1].PartIndex != 1 {
		t.Errorf("part indexes = %d,%d, want 0,1", frags[0].PartIndex, frags[1].PartIndex)
	}
	if !frags[1].StartAt.Equal(frags[0].EndAt) {
		t.Error("fragments must be contiguous")
	}
}

func TestSplitAtMaxDuration(t *testing.T) {
	start := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC)
	frags := SplitAtMaxDuration(start, end, 240, time.UTC)
	if len(frags) != 3 {
		t.Fatalf("fragments = %d, want 3", len(frags))
	}
	want := []int{240, 240, 60}
	total := 0
	for i, f := range frags {
		if f.Minutes != want[i] {
			t.Errorf("fragment %d minutes = %d, want %d", i, f.Minutes, want[i])
		}
		total += f.Minutes
		if f.SplitGroupID != frags[0].SplitGroupID {
			t.Error("linked fragments must share SplitGroupID")
		}
		if f.PartIndex != i {
			t.Errorf("part index = %d, want %d", f.PartIndex, i)
		}
		if i > 0 && !f.StartAt.Equal(frags[i-1].EndAt) {
			t.Error("fragments must be contiguous")
		}
	}
	if total != 540 {
		t.Errorf("total = %d, want 540", total)
	}
}

// TestSplitAtMaxDuration_ZoneConsistent proves Date/carryover are derived in the
// supplied location, consistently with SplitAtMidnight.
func TestSplitAtMaxDuration_ZoneConsistent(t *testing.T) {
	syd, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// 2026-10-06 20:00 UTC is already 2026-10-07 in Sydney.
	start := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 6, 23, 0, 0, 0, time.UTC)

	utcFrags := SplitAtMaxDuration(start, end, 120, time.UTC)
	if utcFrags[0].Date != "2026-10-06" {
		t.Errorf("UTC date = %q, want 2026-10-06", utcFrags[0].Date)
	}
	sydFrags := SplitAtMaxDuration(start, end, 120, syd)
	if sydFrags[0].Date != "2026-10-07" {
		t.Errorf("Sydney date = %q, want 2026-10-07", sydFrags[0].Date)
	}
	// Totals are unchanged by the zone choice.
	total := 0
	for _, f := range sydFrags {
		total += f.Minutes
	}
	if total != DurationMinutes(start, end) {
		t.Errorf("total = %d, want %d", total, DurationMinutes(start, end))
	}
}

// TestGridAdjust_NeverExpands proves the grid suggestion preserves the exact
// total minutes and never expands them, and returns the original untouched.
func TestGridAdjust_NeverExpands(t *testing.T) {
	start := time.Date(2026, 10, 6, 9, 7, 0, 0, time.UTC)
	end := time.Date(2026, 10, 6, 9, 20, 0, 0, time.UTC)
	s := GridAdjust(start, end, 15*time.Minute)

	if !s.OriginalStart.Equal(start) || !s.OriginalEnd.Equal(end) {
		t.Error("original interval must be untouched")
	}
	if !s.SuggestedStart.Equal(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("suggested start = %v, want 09:00", s.SuggestedStart)
	}
	orig := DurationMinutes(start, end)
	sugg := DurationMinutes(s.SuggestedStart, s.SuggestedEnd)
	if sugg > orig {
		t.Errorf("grid suggestion expanded time: %d > %d", sugg, orig)
	}
	if sugg != orig {
		t.Errorf("grid suggestion must preserve exact total: %d != %d", sugg, orig)
	}
	if !s.Changed {
		t.Error("expected Changed for an off-grid start")
	}
}

func TestValidateNoOverlap(t *testing.T) {
	a := ProposedTimeBlock{ID: "a", StartAt: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC), EndAt: time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)}
	b := ProposedTimeBlock{ID: "b", StartAt: time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC), EndAt: time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)}

	err := ValidateNoOverlap([]ProposedTimeBlock{a, b})
	if !errors.Is(err, ErrOverlap) {
		t.Fatalf("expected ErrOverlap, got %v", err)
	}
	var oe *OverlapError
	if !errors.As(err, &oe) {
		t.Fatalf("expected *OverlapError, got %v", err)
	}
	if oe.FirstID != "a" || oe.SecondID != "b" {
		t.Errorf("overlap names = %q/%q, want a/b", oe.FirstID, oe.SecondID)
	}

	// Touching intervals are not an overlap.
	touching := ProposedTimeBlock{ID: "c", StartAt: a.EndAt, EndAt: a.EndAt.Add(time.Hour)}
	if err := ValidateNoOverlap([]ProposedTimeBlock{a, touching}); err != nil {
		t.Errorf("touching intervals must not overlap: %v", err)
	}
}
