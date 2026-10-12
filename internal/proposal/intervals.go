package proposal

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Fragment is a contiguous sub-interval produced by a split. Linked fragments
// share a SplitGroupID with sequential PartIndex values; their exact total
// minutes are preserved and they are contiguous and non-overlapping.
type Fragment struct {
	StartAt      time.Time `json:"start_at"`
	EndAt        time.Time `json:"end_at"`
	Minutes      int       `json:"minutes"`
	Date         string    `json:"date"`
	SplitGroupID string    `json:"split_group_id"`
	PartIndex    int       `json:"part_index"`
	Carryover    bool      `json:"carryover"`
}

// DurationMinutes returns the absolute elapsed minutes between start and end.
// It uses time.Time.Sub, which is DST-correct: it measures real elapsed time
// rather than wall-clock difference.
func DurationMinutes(start, end time.Time) int {
	if !end.After(start) {
		return 0
	}
	return int(end.Sub(start) / time.Minute)
}

// SplitAtMidnight splits [start, end) at each local midnight. Fragments on a
// later local date than the first are marked Carryover. It returns nil for a
// non-positive interval.
func SplitAtMidnight(start, end time.Time, loc *time.Location) []Fragment {
	if loc == nil {
		loc = time.UTC
	}
	if !end.After(start) {
		return nil
	}

	groupID := splitGroupID(start, end)
	var frags []Fragment
	cur := start
	idx := 0
	for cur.Before(end) {
		curLocal := cur.In(loc)
		nextMidnight := time.Date(curLocal.Year(), curLocal.Month(), curLocal.Day()+1, 0, 0, 0, 0, loc)
		segEnd := end
		if nextMidnight.Before(end) {
			segEnd = nextMidnight
		}
		if !segEnd.After(cur) {
			segEnd = end
		}
		frags = append(frags, Fragment{
			StartAt:      cur,
			EndAt:        segEnd,
			Minutes:      DurationMinutes(cur, segEnd),
			Date:         cur.In(loc).Format(defaultDateLayout),
			SplitGroupID: groupID,
			PartIndex:    idx,
		})
		cur = segEnd
		idx++
		if idx > 10000 {
			break
		}
	}

	markCarryover(frags)
	return frags
}

// SplitAtMaxDuration splits [start, end) into contiguous fragments of at most
// maxMinutes, preserving the exact total minutes. The final fragment carries the
// remainder (a sub-grid remainder is allowed because exact total is mandatory).
// Date and carryover are derived in loc, consistently with SplitAtMidnight; loc
// nil means UTC.
func SplitAtMaxDuration(start, end time.Time, maxMinutes int, loc *time.Location) []Fragment {
	if loc == nil {
		loc = time.UTC
	}
	if maxMinutes <= 0 {
		maxMinutes = DefaultMaxEntryMinutes
	}
	if !end.After(start) {
		return nil
	}

	total := DurationMinutes(start, end)
	groupID := splitGroupID(start, end)
	var frags []Fragment
	cur := start
	remaining := total
	idx := 0
	for remaining > 0 {
		chunk := maxMinutes
		if remaining < chunk {
			chunk = remaining
		}
		segEnd := cur.Add(time.Duration(chunk) * time.Minute)
		if segEnd.After(end) {
			segEnd = end
		}
		frags = append(frags, Fragment{
			StartAt:      cur,
			EndAt:        segEnd,
			Minutes:      DurationMinutes(cur, segEnd),
			Date:         cur.In(loc).Format(defaultDateLayout),
			SplitGroupID: groupID,
			PartIndex:    idx,
		})
		cur = segEnd
		remaining -= chunk
		idx++
	}

	markCarryover(frags)
	return frags
}

// SplitInterval composes the midnight split and the four-hour split: it splits
// [start, end) at EACH local midnight FIRST, then applies the maxMinutes bound
// WITHIN each local day. Fragments are contiguous and non-overlapping, none
// crosses a local midnight, they share ONE SplitGroupID with sequential
// PartIndex values across the whole group, each carries its own local Date, and
// their minutes (real elapsed, DST-correct) sum to the exact original total.
// Carryover is set on every fragment whose local date differs from the first
// fragment's. It returns nil for a non-positive interval.
func SplitInterval(start, end time.Time, maxMinutes int, loc *time.Location) []Fragment {
	if loc == nil {
		loc = time.UTC
	}
	if maxMinutes <= 0 {
		maxMinutes = DefaultMaxEntryMinutes
	}
	days := SplitAtMidnight(start, end, loc)
	if len(days) == 0 {
		return nil
	}

	groupID := days[0].SplitGroupID
	var out []Fragment
	idx := 0
	for _, day := range days {
		within := SplitAtMaxDuration(day.StartAt, day.EndAt, maxMinutes, loc)
		if len(within) == 0 {
			// A non-positive day fragment cannot occur for a positive span; keep
			// the day fragment itself rather than dropping time.
			within = []Fragment{day}
		}
		for _, f := range within {
			// Re-index across the WHOLE group so midnight and four-hour
			// composition never produces duplicate or non-sequential parts.
			f.SplitGroupID = groupID
			f.PartIndex = idx
			out = append(out, f)
			idx++
		}
	}

	markCarryover(out)
	return out
}

// markCarryover flags any fragment whose local date differs from the first.
func markCarryover(frags []Fragment) {
	if len(frags) == 0 {
		return
	}
	first := frags[0].Date
	for i := range frags {
		frags[i].Carryover = frags[i].Date != first
	}
}

// GridSuggestion is a grid-aligned proposal that preserves the original total
// minutes exactly and never expands it. The original interval is returned
// untouched alongside the suggestion.
type GridSuggestion struct {
	OriginalStart  time.Time     `json:"original_start"`
	OriginalEnd    time.Time     `json:"original_end"`
	SuggestedStart time.Time     `json:"suggested_start"`
	SuggestedEnd   time.Time     `json:"suggested_end"`
	Grid           time.Duration `json:"grid"`
	Changed        bool          `json:"changed"`
}

// GridAdjust returns a 15-minute-grid-aligned suggestion. The suggestion aligns
// the start down to the grid and preserves the exact original duration, so it
// NEVER expands the total minutes. The original interval is returned untouched.
func GridAdjust(start, end time.Time, grid time.Duration) GridSuggestion {
	if grid <= 0 {
		grid = DefaultGridMinutes * time.Minute
	}
	dur := end.Sub(start)
	if dur < 0 {
		dur = 0
	}
	suggestedStart := floorToGrid(start, grid)
	suggestedEnd := suggestedStart.Add(dur)
	return GridSuggestion{
		OriginalStart:  start,
		OriginalEnd:    end,
		SuggestedStart: suggestedStart,
		SuggestedEnd:   suggestedEnd,
		Grid:           grid,
		Changed:        !suggestedStart.Equal(start) || !suggestedEnd.Equal(end),
	}
}

// floorToGrid rounds t down to the nearest grid boundary in its own location.
func floorToGrid(t time.Time, grid time.Duration) time.Time {
	minutes := int(grid / time.Minute)
	if minutes <= 0 {
		return t
	}
	loc := t.Location()
	m := t.Minute()
	floored := (m / minutes) * minutes
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), floored, 0, 0, loc)
}

// ErrOverlap is the sentinel wrapped by OverlapError.
var ErrOverlap = errors.New("proposal: approved intervals cannot overlap")

// OverlapError names the two overlapping block IDs.
type OverlapError struct {
	FirstID  string
	SecondID string
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("%s: %s and %s", ErrOverlap.Error(), e.FirstID, e.SecondID)
}

// Unwrap lets callers branch with errors.Is(err, ErrOverlap).
func (e *OverlapError) Unwrap() error { return ErrOverlap }

// ValidateNoOverlap returns an OverlapError naming the first overlapping pair
// (in start order), or nil. Touching intervals (next.Start == prev.End) are not
// an overlap.
func ValidateNoOverlap(blocks []ProposedTimeBlock) error {
	sorted := make([]ProposedTimeBlock, len(blocks))
	copy(sorted, blocks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartAt.Equal(sorted[j].StartAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].StartAt.Before(sorted[j].StartAt)
	})
	for i := 0; i+1 < len(sorted); i++ {
		if sorted[i+1].StartAt.Before(sorted[i].EndAt) {
			return &OverlapError{FirstID: sorted[i].ID, SecondID: sorted[i+1].ID}
		}
	}
	return nil
}

func splitGroupID(start, end time.Time) string {
	return hashID("split", start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
}
