package timesheet

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// noOverlapReview is a single-POW day: two meetings, their separate confirmed
// work intervals, one topic per block and one unallocated topic. No block
// overlaps another, so issue #183 rule 7 requires a byte-identical draft.
func noOverlapReview() Review {
	return draftReview(
		[]ProposedMeeting{
			{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T09:30:00+07:00", Title: "Standup"},
			{Ref: "m2", Start: "2026-10-26T14:00:00+07:00", End: "2026-10-26T15:00:00+07:00", Title: "Design review"},
		},
		[]Segment{
			{IntervalID: "i1", Date: "2026-10-26", Start: "10:00", End: "11:00", Project: "WORK", Description: "Bridge PRs"},
			{IntervalID: "i2", Date: "2026-10-26", Start: "16:00", End: "17:30", Project: "WORK", Description: "Deep work"},
		},
		[]TopicCandidate{
			{Title: "Calendar filtering", FirstAt: "2026-10-26T09:05:00+07:00", LastAt: "2026-10-26T09:25:00+07:00",
				Items: []EvidenceItem{
					{Kind: "commit", Repo: "acme/api", Title: "fix window", Time: "2026-10-26T09:10:00+07:00"},
					{Kind: "pull_request", Repo: "acme/api", Resource: "pr:acme/api:12", Title: "Add filter", Time: "2026-10-26T09:20:00+07:00"},
				}},
			{Title: "Pull request #21 (acme/web)", FirstAt: "2026-10-26T10:10:00+07:00", LastAt: "2026-10-26T10:40:00+07:00",
				Items: []EvidenceItem{{Kind: "pull_request", Repo: "acme/web", Resource: "pr:acme/web:21", Title: "Ship table", Time: "2026-10-26T10:20:00+07:00"}}},
			{Title: "Evening research", FirstAt: "2026-10-26T19:00:00+07:00", LastAt: "2026-10-26T20:00:00+07:00",
				Items: []EvidenceItem{{Kind: "commit", Repo: "acme/api", Title: "read notes", Time: "2026-10-26T19:30:00+07:00"}}},
		},
		[]ComposedSourceStatus{
			{Source: "github", Enabled: true, Status: StatusComplete},
			{Source: "calendar", Enabled: true, Status: StatusComplete},
		},
	)
}

// TestBuildDraftNoOverlapByteIdentical pins the exact draft JSON a no-overlap
// day produced before the overlap merge (issue #183 rule 7). The golden was
// captured from the pre-change build and is asserted byte for byte.
func TestBuildDraftNoOverlapByteIdentical(t *testing.T) {
	raw, err := json.Marshal(BuildDraft(noOverlapReview(), "test"))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"date":"2026-10-26","timezone":"Asia/Ho_Chi_Minh","app_version":"test","blocks":[{"block_id":"meeting-09:00-09:30","kind":"meeting","start":"09:00","end":"09:30","title":"Standup","topic_candidates":["Calendar filtering"],"prs":["#12 acme/api"],"prs_inline":"PRs: acme/api: #12","completeness":{"calendar":"complete","github":"complete"}},{"block_id":"work-10:00-11:00","kind":"work","start":"10:00","end":"11:00","title":"Bridge PRs","topic_candidates":["Pull request #21 (acme/web)"],"prs":["#21 acme/web"],"prs_inline":"PRs: acme/web: #21","completeness":{"calendar":"complete","github":"complete"}},{"block_id":"meeting-14:00-15:00","kind":"meeting","start":"14:00","end":"15:00","title":"Design review","completeness":{"calendar":"complete","github":"complete"}},{"block_id":"work-16:00-17:30","kind":"work","start":"16:00","end":"17:30","title":"Deep work","completeness":{"calendar":"complete","github":"complete"}},{"block_id":"evidence-19:30-19:30","kind":"evidence","start":"19:30","end":"19:30","topic_candidates":["Evening research"],"completeness":{"calendar":"complete","github":"complete"}}]}`
	if string(raw) != want {
		t.Fatalf("no-overlap draft changed:\n got %s\nwant %s", raw, want)
	}
}

func blockByID(t *testing.T, d Draft, id string) DraftBlock {
	t.Helper()
	for _, b := range d.Blocks {
		if b.BlockID == id {
			return b
		}
	}
	t.Fatalf("no block %q in %+v", id, d.Blocks)
	return DraftBlock{}
}

func sourceKeys(b DraftBlock) []string {
	out := make([]string, 0, len(b.Sources))
	for _, s := range b.Sources {
		out = append(out, s.Kind+"|"+s.Title)
	}
	return out
}

func segment(desc, start, end string) Segment {
	return Segment{IntervalID: "i-" + start, Date: "2026-10-26", Start: start, End: end, Project: "WORK", Description: desc}
}

func topic(title, first, last string, items ...EvidenceItem) TopicCandidate {
	return TopicCandidate{
		Title:   title,
		FirstAt: "2026-10-26T" + first + ":00+07:00",
		LastAt:  "2026-10-26T" + last + ":00+07:00",
		Items:   items,
	}
}

func item(kind, title, at string) EvidenceItem {
	return EvidenceItem{Kind: kind, Repo: "acme/api", Title: title, Time: "2026-10-26T" + at + ":00+07:00"}
}

// TestBuildDraftOverlappingWorkBlocksMerge: two concurrent POWs from different
// projects become one block over the union span, naming both titles, carrying
// both topic candidate sets and both source sets, with the id of the head
// (earliest start).
func TestBuildDraftOverlappingWorkBlocksMerge(t *testing.T) {
	review := draftReview(nil,
		[]Segment{segment("Bridge PRs", "09:00", "11:00"), segment("Ops deploy", "10:00", "12:00")},
		[]TopicCandidate{
			topic("API bridge", "09:10", "09:20", item("commit", "bridge handler", "09:15")),
			topic("Ops deploy", "11:00", "11:30", item("pull_request", "deploy runbook", "11:15")),
		},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one merged block", d.Blocks)
	}
	b := d.Blocks[0]
	if b.BlockID != "work-09:00-12:00" || b.Kind != "work" || b.Start != "09:00" || b.End != "12:00" {
		t.Fatalf("merged block = %s %s-%s, want work-09:00-12:00 over the union span", b.BlockID, b.Start, b.End)
	}
	if b.Title != "Bridge PRs + Ops deploy" {
		t.Fatalf("merged title = %q, want both titles", b.Title)
	}
	wantTopics := []string{"API bridge", "Ops deploy"}
	if len(b.TopicCandidates) != len(wantTopics) {
		t.Fatalf("topic candidates = %+v, want %+v", b.TopicCandidates, wantTopics)
	}
	for i, want := range wantTopics {
		if b.TopicCandidates[i] != want {
			t.Fatalf("topic candidates = %+v, want %+v", b.TopicCandidates, wantTopics)
		}
	}
	wantSources := []string{"commit|bridge handler", "pull_request|deploy runbook"}
	gotSources := sourceKeys(b)
	if len(gotSources) != len(wantSources) {
		t.Fatalf("sources = %+v, want %+v", gotSources, wantSources)
	}
	for i, want := range wantSources {
		if gotSources[i] != want {
			t.Fatalf("sources = %+v, want %+v", gotSources, wantSources)
		}
	}
	dAgain := BuildDraft(review, "test")
	first, _ := json.Marshal(d)
	second, _ := json.Marshal(dAgain)
	if !bytes.Equal(first, second) {
		t.Fatalf("same input produced different drafts:\n%s\n%s", first, second)
	}
}

// TestBuildDraftChainedOverlapIsOneBlock: A overlaps B and B overlaps C, so all
// three are one cluster over the union span.
func TestBuildDraftChainedOverlapIsOneBlock(t *testing.T) {
	review := draftReview(nil,
		[]Segment{
			segment("A", "09:00", "10:00"),
			segment("B", "09:30", "11:00"),
			segment("C", "10:30", "12:00"),
		},
		nil, nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one chained cluster", d.Blocks)
	}
	if b := d.Blocks[0]; b.Start != "09:00" || b.End != "12:00" || b.Title != "A + B + C" {
		t.Fatalf("chained block = %+v, want 09:00-12:00 naming all three", b)
	}
}

// TestBuildDraftTouchingBlocksStaySeparate: end == start is not overlap, so
// the two blocks keep their own spans and ids.
func TestBuildDraftTouchingBlocksStaySeparate(t *testing.T) {
	review := draftReview(nil,
		[]Segment{segment("First", "09:00", "10:00"), segment("Second", "10:00", "11:00")},
		nil, nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 2 {
		t.Fatalf("blocks = %+v, want two touching blocks kept apart", d.Blocks)
	}
	if first := blockByID(t, d, "work-09:00-10:00"); first.Title != "First" {
		t.Fatalf("first block = %+v, want its own title", first)
	}
	if second := blockByID(t, d, "work-10:00-11:00"); second.Title != "Second" {
		t.Fatalf("second block = %+v, want its own title", second)
	}
}

// TestBuildDraftMeetingAbsorbsWorkBlock: a meeting and the work block it
// overlaps become one meeting block over the union span, carrying the work
// block's topics; the separate work block is not emitted.
func TestBuildDraftMeetingAbsorbsWorkBlock(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		[]Segment{segment("Joined standup", "09:30", "10:30")},
		[]TopicCandidate{
			topic("Calendar filtering", "09:10", "09:20", item("commit", "fix window", "09:15")),
			topic("Pipeline fix", "10:00", "10:20", item("pull_request", "green build", "10:10")),
		},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want the meeting only", d.Blocks)
	}
	b := d.Blocks[0]
	if b.Kind != "meeting" || b.Title != "Standup" || b.Start != "09:00" || b.End != "10:30" {
		t.Fatalf("absorbing block = %+v, want the meeting over 09:00-10:30", b)
	}
	if len(b.TopicCandidates) != 2 {
		t.Fatalf("topic candidates = %+v, want both POWs", b.TopicCandidates)
	}
	if len(b.Sources) != 2 {
		t.Fatalf("sources = %+v, want both contributors' sources", b.Sources)
	}
}

// TestBuildDraftEvidenceInsideMeetingsMergesToOneBlock is the 2026-10-21 draft
// shape: one evidence span 02:16-13:00 across two meetings inside it. Every
// block is connected, so one meeting block covers the union span and carries
// every topic.
func TestBuildDraftEvidenceInsideMeetingsMergesToOneBlock(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{
			{Ref: "m1", Start: "2026-10-26T06:40:00+07:00", End: "2026-10-26T07:15:00+07:00", Title: "Workflow training"},
			{Ref: "m2", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T09:30:00+07:00", Title: "Standup"},
		},
		nil,
		[]TopicCandidate{
			topic("Early commit", "02:16", "02:16", item("commit", "early", "02:16")),
			topic("Between meetings", "08:00", "08:15", item("commit", "mid", "08:00")),
			topic("After standup", "10:00", "10:00", item("pull_request", "late", "10:00")),
			topic("Last item", "13:00", "13:00", item("commit", "last", "13:00")),
		},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one merged block", d.Blocks)
	}
	b := d.Blocks[0]
	if b.Kind != "meeting" || b.Start != "02:16" || b.End != "13:00" {
		t.Fatalf("block = %s %s-%s, want the meeting over the union span 02:16-13:00", b.Kind, b.Start, b.End)
	}
	if len(b.TopicCandidates) != 4 {
		t.Fatalf("topic candidates = %+v, want every contributor's topics", b.TopicCandidates)
	}
}

// TestRecordEntriesMergesOverlappingBlocks is issue #183 rule 5: a reviewed
// file that still carries the two overlapping work blocks an older draft
// rendered records one merged entry whose span, topics and re-attached sources
// are the ones the current draft shows for the same evidence.
func TestRecordEntriesMergesOverlappingBlocks(t *testing.T) {
	review := draftReview(nil,
		[]Segment{segment("Bridge PRs", "09:00", "11:00"), segment("Ops deploy", "10:00", "12:00")},
		[]TopicCandidate{
			topic("API bridge", "09:10", "09:20", item("commit", "bridge handler", "09:15")),
			topic("Ops deploy", "11:00", "11:30", item("pull_request", "deploy runbook", "11:15")),
		},
		nil,
	)
	draft := BuildDraft(review, "test")
	if len(draft.Blocks) != 1 {
		t.Fatalf("draft blocks = %+v, want one merged block", draft.Blocks)
	}
	want := draft.Blocks[0]

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	writeStateEvidence(t, statePath, "2026-10-26", review.Evidence.Topics)
	stale := ReviewedDraft{Date: "2026-10-26", Timezone: "Asia/Ho_Chi_Minh", Blocks: []ReviewedBlock{
		{DraftBlock: DraftBlock{BlockID: "work-09:00-11:00", Kind: "work", Start: "09:00", End: "11:00",
			TopicCandidates: []string{"API bridge"}}, Description: "Bridge PRs"},
		{DraftBlock: DraftBlock{BlockID: "work-10:00-12:00", Kind: "work", Start: "10:00", End: "12:00",
			TopicCandidates: []string{"Ops deploy"}}, Description: "Ops deploy"},
	}}
	if _, err := RecordEntries(statePath, "test", stale, false, time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	f, err := ReadEntries(statePath, "2026-10-26")
	if err != nil || f == nil || len(f.Entries) != 1 {
		t.Fatalf("read entries: %+v %v", f, err)
	}
	got := f.Entries[0]
	if got.BlockID != "work-09:00-11:00" || got.Kind != "work" || got.Start != want.Start || got.End != want.End {
		t.Fatalf("recorded block = %s %s %s-%s, want the head id over the draft's union span %s-%s",
			got.BlockID, got.Kind, got.Start, got.End, want.Start, want.End)
	}
	if !strings.Contains(got.Description, "Bridge PRs") || !strings.Contains(got.Description, "Ops deploy") {
		t.Fatalf("recorded description = %q, want both contributors", got.Description)
	}
	if len(got.TopicCandidates) != len(want.TopicCandidates) {
		t.Fatalf("recorded topics = %+v, want the draft's %+v", got.TopicCandidates, want.TopicCandidates)
	}
	for i := range want.TopicCandidates {
		if got.TopicCandidates[i] != want.TopicCandidates[i] {
			t.Fatalf("recorded topics = %+v, want the draft's %+v", got.TopicCandidates, want.TopicCandidates)
		}
	}
	if len(got.Sources) != len(want.Sources) {
		t.Fatalf("recorded sources = %+v, want the draft's %+v", got.Sources, want.Sources)
	}
	for i := range want.Sources {
		if got.Sources[i] != want.Sources[i] {
			t.Fatalf("recorded sources = %+v, want the draft's %+v", got.Sources, want.Sources)
		}
	}
	// Merging is idempotent: recording the already merged file again keeps one
	// entry, so a retried run never duplicates the day.
	merged := ReviewedDraft{Date: "2026-10-26", Timezone: "Asia/Ho_Chi_Minh", Blocks: []ReviewedBlock{
		{DraftBlock: DraftBlock{BlockID: got.BlockID, Kind: got.Kind, Start: got.Start, End: got.End}, Description: got.Description},
	}}
	if _, err := RecordEntries(statePath, "test", merged, false, time.Now()); err != nil {
		t.Fatalf("re-record: %v", err)
	}
	f2, err := ReadEntries(statePath, "2026-10-26")
	if err != nil || f2 == nil || len(f2.Entries) != 1 {
		t.Fatalf("after re-record: %+v %v, want one entry", f2, err)
	}
}
