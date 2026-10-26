package main

import (
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func spansOf(t *testing.T, m map[string][][2]string, id string) [][2]string {
	t.Helper()
	got, ok := m[id]
	if !ok {
		t.Fatalf("no effective spans for %s in %v", id, m)
	}
	return got
}

func assertSpans(t *testing.T, id string, got, want [][2]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s spans = %v, want %v", id, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s spans[%d] = %v, want %v", id, i, got[i], want[i])
		}
	}
}

// TestEffectiveEntrySpansMeetingInsideEvidence is the live 2026-10-21 shape:
// workflow training 06:40-07:15 inside evidence 02:16-10:15. The specific
// meeting claims its span; the broad evidence span splits around it. No minute
// is lost or double-claimed.
func TestEffectiveEntrySpansMeetingInsideEvidence(t *testing.T) {
	m := timesheet.EffectiveEntrySpans([]timesheet.EntryRecord{
		{BlockID: "evidence-02:16-10:15", Start: "02:16", End: "10:15"},
		{BlockID: "meeting-06:40-07:15", Start: "06:40", End: "07:15"},
	})
	assertSpans(t, "meeting", spansOf(t, m, "meeting-06:40-07:15"), [][2]string{{"06:40", "07:15"}})
	assertSpans(t, "evidence", spansOf(t, m, "evidence-02:16-10:15"),
		[][2]string{{"02:16", "06:40"}, {"07:15", "10:15"}})
}

// TestEffectiveEntrySpansPartialOverlap: equal-duration overlapping blocks —
// the earlier start claims first; the later block keeps only its unclaimed
// tail. Deterministic.
func TestEffectiveEntrySpansPartialOverlap(t *testing.T) {
	m := timesheet.EffectiveEntrySpans([]timesheet.EntryRecord{
		{BlockID: "b-late", Start: "10:00", End: "12:00"},
		{BlockID: "a-early", Start: "09:00", End: "11:00"},
	})
	assertSpans(t, "a-early", spansOf(t, m, "a-early"), [][2]string{{"09:00", "11:00"}})
	assertSpans(t, "b-late", spansOf(t, m, "b-late"), [][2]string{{"11:00", "12:00"}})
}

// TestEffectiveEntrySpansFullyCoveredDuplicate: a duplicate of a more specific
// block yields no spans (the caller skips it honestly).
func TestEffectiveEntrySpansFullyCoveredDuplicate(t *testing.T) {
	m := timesheet.EffectiveEntrySpans([]timesheet.EntryRecord{
		{BlockID: "dup-a", Start: "06:40", End: "07:15"},
		{BlockID: "dup-b", Start: "06:40", End: "07:15"},
	})
	if _, ok := m["dup-b"]; ok {
		t.Fatalf("duplicate block received spans: %v", m["dup-b"])
	}
	if len(m["dup-a"]) != 1 {
		t.Fatalf("first duplicate spans = %v, want exactly one", m["dup-a"])
	}
}

// TestStageDay21ShapeEndToEnd: the live 2026-10-21 ledger shape — evidence
// 02:16-13:00 with two meetings inside — stages, confirms and saves with
// gap-free, non-overlapping, policy-compliant intervals where the previous
// build refused the whole day with `overlap`. Every durable interval ≤ 240
// minutes; the total equals the full ledger span (nothing lost or doubled).
func TestStageDay21ShapeEndToEnd(t *testing.T) {
	cfg := writeTestConfig(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("evidence-02:16-13:00", "02:16", "13:00", "Broad evidence block"),
		timedBlock("meeting-06:40-07:15", "06:40", "07:15", "Workflow training"),
		timedBlock("meeting-09:00-09:30", "09:00", "09:30", "Standup"),
	})
	receipt := runStageCapture(t, []string{"--config", cfg, "--date", testDate})
	if receipt.StagedIntervals != 6 {
		t.Fatalf("staged_intervals = %d, want 6 (evidence parts 02:16-06:40 → 240+24, 07:15-09:00, 09:30-13:00, plus 2 meetings)", receipt.StagedIntervals)
	}
	if err := runConfirm([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := runSave([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("save: %v", err)
	}
	day := readDurableDay(t, cfg, testDate)
	want := map[string]bool{
		"02:16-06:16": true, // 240 (four-hour chunk of 02:16-06:40)
		"06:16-06:40": true, // 24 (remainder)
		"06:40-07:15": true, // 35 (meeting, whole)
		"07:15-09:00": true, // 105 (gap between the meetings)
		"09:00-09:30": true, // 30 (meeting, whole)
		"09:30-13:00": true, // 210 (final gap, under the policy)
	}
	if len(day.Confirmed) != len(want) {
		spans := make([]string, 0, len(day.Confirmed))
		for _, iv := range day.Confirmed {
			spans = append(spans, iv.Start+"-"+iv.End)
		}
		t.Fatalf("confirmed spans = %v, want exactly %v", spans, want)
	}
	// Gap-free, non-overlapping, policy-compliant, total preserved.
	type bound struct{ start, end, minutes int }
	clock := func(s string) int {
		m, ok := 0, false
		if len(s) == 5 {
			h := int(s[0]-'0')*10 + int(s[1]-'0')
			mm := int(s[3]-'0')*10 + int(s[4]-'0')
			m, ok = h*60+mm, true
		}
		if !ok {
			t.Fatalf("unparsable span %q", s)
		}
		return m
	}
	var bounds []bound
	total := 0
	for _, iv := range day.Confirmed {
		s, e := clock(iv.Start), clock(iv.End)
		mins := e - s
		if mins <= 0 || mins > 240 {
			t.Fatalf("interval %s-%s = %d minutes, want (0, 240]", iv.Start, iv.End, mins)
		}
		bounds = append(bounds, bound{s, e, mins})
		total += mins
	}
	for i := 1; i < len(bounds); i++ {
		for j := 0; j < i; j++ {
			if bounds[i].start < bounds[j].end && bounds[j].start < bounds[i].end {
				t.Fatalf("intervals overlap: %+v", bounds)
			}
		}
	}
	if total != 644 {
		t.Fatalf("total confirmed minutes = %d, want 644 (the full ledger span 02:16-13:00, nothing lost or doubled)", total)
	}
}

// TestEffectiveEntrySpansDisjointUnchanged: entries that do not overlap keep
// their single spans (the bridge adds nothing on clean days).
func TestEffectiveEntrySpansDisjointUnchanged(t *testing.T) {
	m := timesheet.EffectiveEntrySpans([]timesheet.EntryRecord{
		{BlockID: "a", Start: "09:00", End: "10:00"},
		{BlockID: "b", Start: "11:00", End: "11:15"},
	})
	assertSpans(t, "a", spansOf(t, m, "a"), [][2]string{{"09:00", "10:00"}})
	assertSpans(t, "b", spansOf(t, m, "b"), [][2]string{{"11:00", "11:15"}})
}

// TestStageUnparsableTimesSkipReason: an entry whose anchors are not times the
// review carries is skipped with its OWN reason naming the bad times — never
// with the fully-claimed reason, which only genuine duplicates may carry
// (issue #173). The day's other entries still stage.
func TestStageUnparsableTimesSkipReason(t *testing.T) {
	cfg := writeTestConfig(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("bad-1", "23:00", "1am", "Block with a malformed end time"),
		timedBlock("good-1", "09:00", "10:00", "Clean block"),
	})
	receipt := runStageCapture(t, []string{"--config", cfg, "--date", testDate})
	if len(receipt.Skipped) != 1 || receipt.Skipped[0].BlockID != "bad-1" {
		t.Fatalf("skipped = %+v, want exactly bad-1", receipt.Skipped)
	}
	reason := receipt.Skipped[0].Reason
	if !strings.Contains(reason, "cannot be parsed") || !strings.Contains(reason, "1am") {
		t.Fatalf("skip reason = %q, want the unparsable-times reason naming the bad end time", reason)
	}
	if strings.Contains(reason, "fully claimed") {
		t.Fatalf("skip reason = %q, want the fully-claimed reason reserved for genuinely claimed entries", reason)
	}
	if len(receipt.Staged) != 1 || receipt.Staged[0].BlockID != "good-1" {
		t.Fatalf("staged = %+v, want only the clean block", receipt.Staged)
	}
}

// TestStageZeroDurationEntrySkipReason: a 09:00→09:00 entry is NOT an overnight
// span — it has zero duration, and the interval op refuses start == end. Passing
// it through the overnight branch aborted the whole stage command with
// `invalid_interval` and blocked every other entry of the day (issue #173). It is
// skipped with its own reason, and a valid block on the same day still stages.
func TestStageZeroDurationEntrySkipReason(t *testing.T) {
	cfg := writeTestConfig(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("zero-1", "09:00", "09:00", "Block with no duration"),
		timedBlock("good-1", "10:00", "11:00", "Clean block"),
	})
	receipt := runStageCapture(t, []string{"--config", cfg, "--date", testDate})
	if len(receipt.Skipped) != 1 || receipt.Skipped[0].BlockID != "zero-1" {
		t.Fatalf("skipped = %+v, want exactly zero-1", receipt.Skipped)
	}
	reason := receipt.Skipped[0].Reason
	if !strings.Contains(reason, "zero duration") || !strings.Contains(reason, "09:00") {
		t.Fatalf("skip reason = %q, want the zero-duration reason naming the entry's times", reason)
	}
	if strings.Contains(reason, "fully claimed") {
		t.Fatalf("skip reason = %q, want the fully-claimed reason reserved for genuinely claimed entries", reason)
	}
	if len(receipt.Staged) != 1 || receipt.Staged[0].BlockID != "good-1" {
		t.Fatalf("staged = %+v, want only the clean block (no whole-day abort)", receipt.Staged)
	}
	if receipt.StagedIntervals != 1 {
		t.Fatalf("staged_intervals = %d, want 1", receipt.StagedIntervals)
	}
}

// TestStageOvernightBlockEndToEnd is the issue #173 regression: the gap-aware
// resolver excludes a span whose end is not after its start, and the derivation
// then reported the block as "fully claimed by more specific entries" and
// dropped it — silent loss of a real overnight block. A 23:00→01:00 block must
// stage as ONE overnight interval, survive confirm and save, and never carry
// the false fully-claimed reason.
func TestStageOvernightBlockEndToEnd(t *testing.T) {
	cfg := writeTestConfig(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("night-1", "23:00", "01:00", "Overnight release watch"),
	})
	receipt := runStageCapture(t, []string{"--config", cfg, "--date", testDate})
	for _, skip := range receipt.Skipped {
		if strings.Contains(skip.Reason, "fully claimed") {
			t.Fatalf("overnight block skipped as %q, want it staged as one interval", skip.Reason)
		}
	}
	if len(receipt.Skipped) != 0 {
		t.Fatalf("skipped = %+v, want no skips", receipt.Skipped)
	}
	if len(receipt.Staged) != 1 {
		t.Fatalf("staged = %+v, want the block's single overnight interval", receipt.Staged)
	}
	if ref := receipt.Staged[0]; ref.BlockID != "night-1" || ref.Chunk != 0 || ref.Chunks != 0 {
		t.Fatalf("staged ref = %+v, want night-1 as one unsplit interval", ref)
	}
	if err := runConfirm([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := runSave([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("save: %v", err)
	}
	day := readDurableDay(t, cfg, testDate)
	if len(day.Confirmed) != 1 {
		t.Fatalf("confirmed = %+v, want the single overnight interval", day.Confirmed)
	}
	if iv := day.Confirmed[0]; iv.Start != "23:00" || iv.End != "01:00" {
		t.Fatalf("confirmed interval = %s-%s, want the recorded span 23:00-01:00", iv.Start, iv.End)
	}
	// The saved revision's view counts only the part on the starting local day
	// (23:00-24:00); the 01:00 tail is the next day's allocation — the same
	// cross-midnight split every overnight interval gets.
	if n := len(day.History); n == 0 || day.History[n-1].TotalMinutes != 60 {
		t.Fatalf("history = %+v, want the saved revision's 60 minutes on the starting day", day.History)
	}
}
