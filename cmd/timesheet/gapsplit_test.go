package main

import (
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
