package timesheet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func draftReview(meetings []ProposedMeeting, segments []Segment, topics []TopicCandidate, sources []ComposedSourceStatus) Review {
	r := Review{Date: "2026-10-26", Timezone: "Asia/Ho_Chi_Minh"}
	if meetings != nil || segments != nil || topics != nil || sources != nil {
		r.Evidence = &EvidenceReview{Sources: sources, Topics: topics, Meetings: meetings}
	}
	r.Confirmed = View{Segments: segments}
	return r
}

func TestBuildDraftMeetingBlockCarriesTopicsAndPRs(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		nil,
		[]TopicCandidate{{
			Title:   "Calendar filtering",
			FirstAt: "2026-10-26T09:20:00+07:00", LastAt: "2026-10-26T09:40:00+07:00",
			Items: []EvidenceItem{
				{Kind: "commit", Repo: "acme/api", Title: "fix window", Time: "2026-10-26T09:25:00+07:00"},
				{Kind: "pull_request", Repo: "acme/api", Resource: "pr:acme/api:12", Title: "Add filter", Time: "2026-10-26T09:30:00+07:00", URL: "https://example.com/pr/12"},
			},
		}},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1: %+v", len(d.Blocks), d.Blocks)
	}
	b := d.Blocks[0]
	if b.Kind != "meeting" || b.Start != "09:00" || b.End != "10:00" || b.Title != "Standup" {
		t.Fatalf("meeting block shape wrong: %+v", b)
	}
	if len(b.TopicCandidates) != 1 || b.TopicCandidates[0] != "Calendar filtering" {
		t.Fatalf("topic candidates = %+v", b.TopicCandidates)
	}
	if len(b.PRs) != 1 || b.PRs[0] != "#12 acme/api" {
		t.Fatalf("prs = %+v", b.PRs)
	}
	if len(b.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(b.Sources))
	}
	if d.DayNote != "" {
		t.Fatalf("day note should be empty when meetings exist: %q", d.DayNote)
	}
}

func TestBuildDraftDeclinedAndAllDayMeetingsSkipped(t *testing.T) {
	review := draftReview([]ProposedMeeting{
		{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T09:30:00+07:00", Title: "Skipped", Decision: "no"},
		{Ref: "m2", Start: "2026-10-26T00:00:00+07:00", End: "2026-10-26T00:00:00+07:00", Title: "Away", AllDay: true},
		{Ref: "m3", Start: "2026-10-26T11:00:00+07:00", End: "2026-10-26T11:30:00+07:00", Title: "Kept", Status: "cancelled"},
	}, nil, nil, nil)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 || d.Blocks[0].Kind != "day" {
		t.Fatalf("expected one fallback day block, got %+v", d.Blocks)
	}
	if d.DayNote != "no calendar events" {
		t.Fatalf("day note = %q", d.DayNote)
	}
}

func TestBuildDraftIntervalOutsideMeetingsBecomesWorkBlock(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		[]Segment{
			{IntervalID: "i1", Date: "2026-10-26", Start: "09:15", End: "09:45", Project: "WORK", Description: "joined standup"},
			{IntervalID: "i2", Date: "2026-10-26", Start: "13:00", End: "15:00", Project: "WORK", Description: "deep work"},
		},
		nil, nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2: %+v", len(d.Blocks), d.Blocks)
	}
	kinds := map[string]DraftBlock{}
	for _, b := range d.Blocks {
		kinds[b.Kind] = b
	}
	if w, ok := kinds["work"]; !ok || w.Start != "13:00" || w.End != "15:00" || w.Title != "deep work" {
		t.Fatalf("work block wrong: %+v", w)
	}
	if _, ok := kinds["meeting"]; !ok {
		t.Fatalf("meeting block missing: %+v", d.Blocks)
	}
	if containsString(d.Flags, "no_confirmed_intervals") {
		t.Fatalf("no_confirmed_intervals flag must be absent when intervals exist: %+v", d.Flags)
	}
}

func TestBuildDraftUnallocatedTopicsFormEvidenceBlock(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T09:30:00+07:00", Title: "Standup"}},
		nil,
		[]TopicCandidate{
			{Title: "Evening research", FirstAt: "2026-10-26T20:00:00+07:00", LastAt: "2026-10-26T21:00:00+07:00",
				Items: []EvidenceItem{{Kind: "commit", Repo: "acme/api", Title: "read notes", Time: "2026-10-26T20:10:00+07:00"}}},
		},
		nil,
	)
	d := BuildDraft(review, "test")
	var evidence *DraftBlock
	for i := range d.Blocks {
		if d.Blocks[i].Kind == "evidence" {
			evidence = &d.Blocks[i]
		}
	}
	if evidence == nil {
		t.Fatalf("evidence block missing: %+v", d.Blocks)
	}
	if evidence.Start != "20:10" || evidence.End != "20:10" {
		t.Fatalf("evidence span = %s-%s, want 20:10-20:10", evidence.Start, evidence.End)
	}
	if len(evidence.TopicCandidates) != 1 || evidence.TopicCandidates[0] != "Evening research" {
		t.Fatalf("evidence topics = %+v", evidence.TopicCandidates)
	}
}

func TestBuildDraftFlagsIncompleteSources(t *testing.T) {
	review := draftReview(nil, nil, nil, []ComposedSourceStatus{
		{Source: "github", Enabled: true, Status: StatusComplete},
		{Source: "calendar", Enabled: true, Status: "incomplete"},
	})
	d := BuildDraft(review, "test")
	if !containsString(d.Flags, "source_incomplete:calendar") {
		t.Fatalf("flags = %+v", d.Flags)
	}
	if !containsString(d.Flags, "no_confirmed_intervals") {
		t.Fatalf("flags = %+v", d.Flags)
	}
}

func reviewed(blocks ...ReviewedBlock) ReviewedDraft {
	return ReviewedDraft{Date: "2026-10-26", Blocks: blocks}
}

func TestRecordEntriesWritesMergesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	now := time.Now()

	first := reviewed(ReviewedBlock{
		DraftBlock:  DraftBlock{BlockID: "meeting-09:00-10:00", Kind: "meeting", Start: "09:00", End: "10:00"},
		Description: "Standup and reviews",
	})
	r1, err := RecordEntries(statePath, "test", first, now)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if r1.Written != 1 || r1.EntriesTotal != 1 {
		t.Fatalf("receipt = %+v", r1)
	}
	f1, err := ReadEntries(statePath, "2026-10-26")
	if err != nil || f1 == nil || len(f1.Entries) != 1 {
		t.Fatalf("read after first: %+v %v", f1, err)
	}

	// Same block with a new description updates in place; a new block adds.
	second := reviewed(
		ReviewedBlock{
			DraftBlock:  DraftBlock{BlockID: "meeting-09:00-10:00", Kind: "meeting", Start: "09:00", End: "10:00"},
			Description: "Standup, filter fix, and reviews",
		},
		ReviewedBlock{
			DraftBlock:  DraftBlock{BlockID: "work-13:00-15:00", Kind: "work", Start: "13:00", End: "15:00"},
			Description: "Deep work on the draft builder",
		},
	)
	r2, err := RecordEntries(statePath, "test", second, now)
	if err != nil {
		t.Fatalf("second record: %v", err)
	}
	if r2.EntriesTotal != 2 || r2.Written != 2 {
		t.Fatalf("receipt = %+v", r2)
	}
	f2, _ := ReadEntries(statePath, "2026-10-26")
	if len(f2.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(f2.Entries))
	}
	found := map[string]string{}
	for _, e := range f2.Entries {
		found[e.BlockID] = e.Description
	}
	if found["meeting-09:00-10:00"] != "Standup, filter fix, and reviews" {
		t.Fatalf("updated description missing: %+v", found)
	}
}

func TestRecordEntriesValidation(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")

	cases := []struct {
		name     string
		draft    ReviewedDraft
		wantCode string
		wantMsg  string
	}{
		{"bad date", ReviewedDraft{Date: "26-10-2026", Blocks: []ReviewedBlock{{DraftBlock: DraftBlock{BlockID: "b"}, Description: "x"}}}, CodeInvalidDate, ""},
		{"no blocks", ReviewedDraft{Date: "2026-10-26"}, CodeBadRequest, "at least one block"},
		{"empty description", reviewed(ReviewedBlock{DraftBlock: DraftBlock{BlockID: "b"}, Description: "   "}), CodeBadRequest, "empty description"},
		{"duplicate block", reviewed(
			ReviewedBlock{DraftBlock: DraftBlock{BlockID: "b"}, Description: "x"},
			ReviewedBlock{DraftBlock: DraftBlock{BlockID: "b"}, Description: "y"},
		), CodeBadRequest, "duplicate block_id"},
		{"missing prs list", reviewed(ReviewedBlock{
			DraftBlock:  DraftBlock{BlockID: "b", PRs: []string{"#12 acme/api"}},
			Description: "work without the list",
		}), CodeBadRequest, "PRs:"},
	}
	for _, tc := range cases {
		_, err := RecordEntries(statePath, "test", tc.draft, time.Now())
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
		if tc.wantCode != "" && !strings.Contains(err.Error(), tc.wantCode) {
			t.Fatalf("%s: error %q lacks code %q", tc.name, err, tc.wantCode)
		}
		if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
			t.Fatalf("%s: error %q lacks %q", tc.name, err, tc.wantMsg)
		}
	}
}

func TestRecordEntriesRejectsMismatchedExistingFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := os.MkdirAll(filepath.Dir(EntriesPath(statePath, "2026-10-26")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EntriesPath(statePath, "2026-10-26"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := RecordEntries(statePath, "test", reviewed(ReviewedBlock{
		DraftBlock: DraftBlock{BlockID: "b"}, Description: "x",
	}), time.Now())
	if err == nil || !strings.Contains(err.Error(), CodeStateInvalid) {
		t.Fatalf("expected state invalid error, got %v", err)
	}
}

func TestDraftJSONRoundTripThroughReviewedSchema(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		nil,
		[]TopicCandidate{{Title: "T", FirstAt: "2026-10-26T09:10:00+07:00", LastAt: "2026-10-26T09:20:00+07:00",
			Items: []EvidenceItem{{Kind: "commit", Repo: "r", Title: "i", Time: "2026-10-26T09:11:00+07:00"}}}},
		nil,
	)
	d := BuildDraft(review, "test")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var reviewed ReviewedDraft
	if err := json.Unmarshal(raw, &reviewed); err != nil {
		t.Fatalf("draft JSON does not fit the reviewed schema: %v", err)
	}
	if reviewed.Date != "2026-10-26" || len(reviewed.Blocks) != 1 {
		t.Fatalf("reviewed = %+v", reviewed)
	}
	reviewed.Blocks[0].Description = "Standup PRs: none"
	if _, err := RecordEntries(filepath.Join(t.TempDir(), "state.json"), "test", reviewed, time.Now()); err != nil {
		t.Fatalf("record from round-tripped draft: %v", err)
	}
}
