package timesheet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	if got.BlockID != want.BlockID || got.Kind != "work" || got.Start != want.Start || got.End != want.End {
		t.Fatalf("recorded block = %s %s %s-%s, want the draft's id %s over its union span %s-%s",
			got.BlockID, got.Kind, got.Start, got.End, want.BlockID, want.Start, want.End)
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

// --- issue #183 fix round: F-1, F-2, F-3, F-4 --------------------------------

// absorbedEvidenceReview is the F-1 shape: one meeting and three evidence
// topics whose topic anchors sit outside the meeting's span while their item
// anchors sit inside it, so the draft's evidence seed overlaps the meeting and
// is absorbed into it.
func absorbedEvidenceReview() Review {
	untimed := []EvidenceItem{}
	for i := 0; i < 7; i++ {
		untimed = append(untimed, item("commit", fmt.Sprintf("note %d", i), fmt.Sprintf("09:%02d", 45+i)))
	}
	return draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		nil,
		[]TopicCandidate{
			topic("Endpoint research", "08:00", "08:30", item("commit", "read docs", "09:40")),
			topic("Later review", "11:00", "11:30", item("pull_request", "review deck", "10:10")),
			{Title: "Untimed notes", Items: untimed},
		},
		nil,
	)
}

// draftAsReviewed turns the draft the skill hands the judgment step into the
// reviewed file record receives: the merged blocks, with a description per
// block.
func draftAsReviewed(t *testing.T, d Draft) ReviewedDraft {
	t.Helper()
	out := ReviewedDraft{Date: d.Date, Timezone: d.Timezone}
	for _, b := range d.Blocks {
		out.Blocks = append(out.Blocks, ReviewedBlock{DraftBlock: b, Description: b.Title})
	}
	return out
}

// recordedSourcesEqualDraft is the F-1 invariant: for the same evidence the
// recorded entry carries exactly the draft block's sources, in order, with the
// same truncation flag.
func recordedSourcesEqualDraft(t *testing.T, got EntryRecord, want DraftBlock) {
	t.Helper()
	if len(got.Sources) != len(want.Sources) {
		t.Fatalf("recorded sources = %d, draft = %d: %+v", len(got.Sources), len(want.Sources), got.Sources)
	}
	for i := range want.Sources {
		if got.Sources[i] != want.Sources[i] {
			t.Fatalf("recorded source[%d] = %+v, draft = %+v", i, got.Sources[i], want.Sources[i])
		}
	}
	if got.SourcesTruncated != want.SourcesTruncated {
		t.Fatalf("recorded sources_truncated = %v, draft = %v", got.SourcesTruncated, want.SourcesTruncated)
	}
}

// TestRecordSourcesMatchDraftWhenEvidenceAbsorbedIntoMeeting: a cluster that
// absorbed an evidence block keeps the sources the draft attached to it, for
// both reviewed-file shapes record accepts — the older pre-merge blocks and
// the already merged draft (review finding F-1).
func TestRecordSourcesMatchDraftWhenEvidenceAbsorbedIntoMeeting(t *testing.T) {
	review := absorbedEvidenceReview()
	draft := BuildDraft(review, "test")
	if len(draft.Blocks) != 1 {
		t.Fatalf("draft blocks = %+v, want the meeting only", draft.Blocks)
	}
	want := draft.Blocks[0]
	if want.Kind != "meeting" || len(want.Sources) != draftSourcesPerBlock || !want.SourcesTruncated {
		t.Fatalf("draft block = %s sources=%d truncated=%v, want a meeting with %d truncated sources",
			want.Kind, len(want.Sources), want.SourcesTruncated, draftSourcesPerBlock)
	}

	shapes := []struct {
		name string
		file ReviewedDraft
	}{
		{"pre-merge blocks", ReviewedDraft{Date: "2026-10-26", Timezone: "Asia/Ho_Chi_Minh", Blocks: []ReviewedBlock{
			{DraftBlock: DraftBlock{BlockID: "meeting-09:00-10:00", Kind: "meeting", Start: "09:00", End: "10:00"}, Description: "Standup"},
			{DraftBlock: DraftBlock{BlockID: "evidence-09:40-10:10", Kind: "evidence", Start: "09:40", End: "10:10",
				TopicCandidates: []string{"Endpoint research", "Later review", "Untimed notes"}}, Description: "Evidence"},
		}}},
		{"merged draft", draftAsReviewed(t, draft)},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state.json")
			writeStateEvidence(t, statePath, "2026-10-26", review.Evidence.Topics)
			if _, err := RecordEntries(statePath, "test", shape.file, false, time.Now()); err != nil {
				t.Fatalf("record: %v", err)
			}
			f, err := ReadEntries(statePath, "2026-10-26")
			if err != nil || f == nil || len(f.Entries) != 1 {
				t.Fatalf("read entries: %+v %v", f, err)
			}
			recordedSourcesEqualDraft(t, f.Entries[0], want)
		})
	}
}

// TestBuildDraftMeetingTitleKeepsDescriptionOnlyWork: a meeting that absorbs a
// work block described only by its confirmed description still names that POW
// in the title, because rule 2 forbids dropping it silently (finding F-2).
func TestBuildDraftMeetingTitleKeepsDescriptionOnlyWork(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		[]Segment{segment("Repair billing outage", "09:30", "10:30")},
		nil, nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want the meeting only", d.Blocks)
	}
	if got := d.Blocks[0].Title; got != "Standup + Repair billing outage" {
		t.Fatalf("title = %q, want the meeting and the absorbed POW named", got)
	}
}

// TestBuildDraftMeetingTitleLeavesTopicBackedWorkToItsTopics: a work block
// that carries topic candidates is named through them, so the meeting title
// stays clean and does not duplicate its prose (finding F-2).
func TestBuildDraftMeetingTitleLeavesTopicBackedWorkToItsTopics(t *testing.T) {
	review := draftReview(
		[]ProposedMeeting{{Ref: "m1", Start: "2026-10-26T09:00:00+07:00", End: "2026-10-26T10:00:00+07:00", Title: "Standup"}},
		[]Segment{segment("Joined standup", "09:30", "10:30")},
		[]TopicCandidate{topic("Pipeline fix", "10:00", "10:20", item("pull_request", "green build", "10:10"))},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want the meeting only", d.Blocks)
	}
	b := d.Blocks[0]
	if b.Title != "Standup" {
		t.Fatalf("title = %q, want the clean meeting title", b.Title)
	}
	if len(b.TopicCandidates) != 1 || b.TopicCandidates[0] != "Pipeline fix" {
		t.Fatalf("topic candidates = %+v, want the work block's topic", b.TopicCandidates)
	}
}

// TestBuildDraftNonMeetingClusterNamesTitlelessContributor: a work block and an
// evidence block with topics merge, and the title names the evidence
// contributor by its first topic candidate in evidence order (finding F-3).
func TestBuildDraftNonMeetingClusterNamesTitlelessContributor(t *testing.T) {
	review := draftReview(nil,
		[]Segment{segment("Project A work", "09:00", "10:00")},
		[]TopicCandidate{
			topic("Project B work", "08:00", "08:30", item("commit", "b1", "09:30")),
			topic("Project B followup", "11:00", "11:30", item("commit", "b2", "10:20")),
		},
		nil,
	)
	d := BuildDraft(review, "test")
	if len(d.Blocks) != 1 {
		t.Fatalf("blocks = %+v, want one merged block", d.Blocks)
	}
	b := d.Blocks[0]
	if b.Title != "Project A work + Project B work" {
		t.Fatalf("title = %q, want one name per contributor", b.Title)
	}
	if strings.Contains(b.Title, "Project B followup") {
		t.Fatalf("title = %q, want one name per contributor, not every topic", b.Title)
	}
}

// TestFoldDraftClusterNamesEachTitlelessContributor: in a cluster where every
// contributor is titleless, each is named by its own first topic candidate in
// evidence order.
func TestFoldDraftClusterNamesEachTitlelessContributor(t *testing.T) {
	topics := []TopicCandidate{{Title: "Alpha"}, {Title: "Beta"}}
	seeds := []draftSeed{
		{blk: DraftBlock{Kind: "evidence", Start: "09:00", End: "09:30", TopicCandidates: []string{"Alpha"}},
			startM: 540, endM: 570, hasM: true, topics: []int{0}},
		{blk: DraftBlock{Kind: "evidence", Start: "09:20", End: "10:00", TopicCandidates: []string{"Beta"}},
			startM: 560, endM: 600, hasM: true, topics: []int{1}},
	}
	out := foldDraftCluster(seeds, []int{0, 1}, topics, time.UTC)
	if out.blk.Title != "Alpha + Beta" {
		t.Fatalf("title = %q, want each titleless contributor named once", out.blk.Title)
	}
}

// TestRecordAndDraftUseOneMergedBlockID: a multi-member cluster's id is the
// union-derived kind-start-end on both paths, so recording the draft after a
// recovery merge replaces the same entry instead of duplicating it (finding
// F-4).
func TestRecordAndDraftUseOneMergedBlockID(t *testing.T) {
	review := draftReview(nil,
		[]Segment{segment("Bridge PRs", "09:00", "11:00"), segment("Ops deploy", "10:00", "12:00")},
		nil, nil,
	)
	draft := BuildDraft(review, "test")
	if len(draft.Blocks) != 1 || draft.Blocks[0].BlockID != "work-09:00-12:00" {
		t.Fatalf("draft = %+v, want work-09:00-12:00 over the union span", draft.Blocks)
	}
	preMerge := ReviewedDraft{Date: "2026-10-26", Blocks: []ReviewedBlock{
		{DraftBlock: DraftBlock{BlockID: "work-09:00-11:00", Kind: "work", Start: "09:00", End: "11:00"}, Description: "Bridge PRs"},
		{DraftBlock: DraftBlock{BlockID: "work-10:00-12:00", Kind: "work", Start: "10:00", End: "12:00"}, Description: "Ops deploy"},
	}}

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if _, err := RecordEntries(statePath, "test", preMerge, false, time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	f, _ := ReadEntries(statePath, "2026-10-26")
	if f == nil || len(f.Entries) != 1 || f.Entries[0].BlockID != draft.Blocks[0].BlockID {
		t.Fatalf("recorded = %+v, want the draft's id %s", f, draft.Blocks[0].BlockID)
	}
	// Recording the drafted block afterwards is a replace of the same entry.
	if _, err := RecordEntries(statePath, "test", draftAsReviewed(t, draft), false, time.Now()); err != nil {
		t.Fatalf("re-record draft: %v", err)
	}
	f2, _ := ReadEntries(statePath, "2026-10-26")
	if f2 == nil || len(f2.Entries) != 1 || f2.Entries[0].BlockID != draft.Blocks[0].BlockID {
		t.Fatalf("after recording the draft = %+v, want one entry under %s", f2, draft.Blocks[0].BlockID)
	}
	// Re-recording the same pre-merge content in merge mode changes nothing.
	if _, err := RecordEntries(statePath, "test", preMerge, false, time.Now()); err != nil {
		t.Fatalf("re-record same content: %v", err)
	}
	f3, _ := ReadEntries(statePath, "2026-10-26")
	if f3 == nil || len(f3.Entries) != 1 || f3.Entries[0].BlockID != draft.Blocks[0].BlockID ||
		f3.Entries[0].Start != "09:00" || f3.Entries[0].End != "12:00" {
		t.Fatalf("after re-recording = %+v, want one unchanged union entry", f3)
	}
}

// TestRecordReplaceReportsObsoleteMergedContributors: replace mode still
// reports the entries a merge makes obsolete, even though the merged block's
// id changed to the union-derived one (finding F-4).
func TestRecordReplaceReportsObsoleteMergedContributors(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := os.MkdirAll(filepath.Dir(EntriesPath(statePath, "2026-10-26")), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := EntriesFile{Date: "2026-10-26", AppVersion: "test", Entries: []EntryRecord{
		{BlockID: "work-09:00-11:00", Date: "2026-10-26", Start: "09:00", End: "11:00", Description: "Bridge PRs", Kind: "work"},
		{BlockID: "work-10:00-12:00", Date: "2026-10-26", Start: "10:00", End: "12:00", Description: "Ops deploy", Kind: "work"},
	}}
	data, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EntriesPath(statePath, "2026-10-26"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	reviewed := ReviewedDraft{Date: "2026-10-26", Blocks: []ReviewedBlock{
		{DraftBlock: DraftBlock{BlockID: "work-09:00-11:00", Kind: "work", Start: "09:00", End: "11:00"}, Description: "Bridge PRs"},
		{DraftBlock: DraftBlock{BlockID: "work-10:00-12:00", Kind: "work", Start: "10:00", End: "12:00"}, Description: "Ops deploy"},
	}}
	receipt, err := RecordEntries(statePath, "test", reviewed, true, time.Now())
	if err != nil {
		t.Fatalf("record --replace: %v", err)
	}
	if receipt.Removed != 2 {
		t.Fatalf("removed = %d, want both obsolete contributors reported", receipt.Removed)
	}
	f, _ := ReadEntries(statePath, "2026-10-26")
	if f == nil || len(f.Entries) != 1 || f.Entries[0].BlockID != "work-09:00-12:00" {
		t.Fatalf("entries = %+v, want one entry under the union-derived id", f)
	}
}

// TestMergeOverlappingBlocksResolvesUnionIDCollisions: a union-derived id that
// collides with an existing singleton id takes the next free suffix, in
// ascending id order, while the singleton keeps its own id. The draft, record
// and stage projection all use the same resolver.
func TestMergeOverlappingBlocksResolvesUnionIDCollisions(t *testing.T) {
	got := MergeOverlappingBlocks([]ReviewedBlock{
		{DraftBlock: DraftBlock{BlockID: "work-09:00-11:00", Kind: "work", Start: "09:00", End: "11:00"}, Description: "A"},
		{DraftBlock: DraftBlock{BlockID: "work-10:00-12:00", Kind: "work", Start: "10:00", End: "12:00"}, Description: "B"},
		{DraftBlock: DraftBlock{BlockID: "work-09:00-12:00", Kind: "work"}, Description: "C"},
	})
	if len(got) != 2 {
		t.Fatalf("blocks = %+v, want the merged cluster and the singleton", got)
	}
	if got[0].BlockID != "work-09:00-12:00-2" {
		t.Fatalf("merged id = %q, want the union id suffixed past the existing singleton", got[0].BlockID)
	}
	if got[1].BlockID != "work-09:00-12:00" {
		t.Fatalf("singleton id = %q, want it untouched", got[1].BlockID)
	}
}
