package proposal

import (
	"encoding/json"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

func at(day, hour, minute int) time.Time {
	return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC)
}

func testOpts() ProposalOptions {
	return ProposalOptions{Location: time.UTC, Date: at(6, 0, 0), Now: at(6, 12, 0)}
}

// commitEvidence is a standalone local_git commit with its own SourceRef.At.
func commitEvidence(id, repo, sha string, when time.Time, pr int, branch string) evidence.Evidence {
	return evidence.Evidence{
		ID:       id,
		Kind:     evidence.SourceLocalGit,
		Title:    "commit " + sha,
		Start:    when,
		Duration: evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{
			Kind: evidence.SourceLocalGit, Repository: repo, CommitSHA: sha, Branch: branch, At: when,
		}},
		TopicHint: &evidence.TopicHint{Repository: repo, PRNumber: pr, Branch: branch},
	}
}

// prEvidence is a remote_pr that folds several commits, each with its own At.
func prEvidence(id, repo string, pr int, branch string, commits map[string]time.Time) evidence.Evidence {
	shas := make([]string, 0, len(commits))
	for sha := range commits {
		shas = append(shas, sha)
	}
	sort.Strings(shas)

	refs := []evidence.SourceRef{{Kind: evidence.SourceRemotePR, Repository: repo, PRNumber: pr, Branch: branch}}
	var earliest, latest time.Time
	for _, sha := range shas {
		when := commits[sha]
		refs = append(refs, evidence.SourceRef{Kind: evidence.SourceLocalGit, Repository: repo, CommitSHA: sha, Branch: branch, At: when})
		if earliest.IsZero() || when.Before(earliest) {
			earliest = when
		}
		if latest.IsZero() || when.After(latest) {
			latest = when
		}
	}
	return evidence.Evidence{
		ID:         id,
		Kind:       evidence.SourceRemotePR,
		Title:      "PR",
		Start:      earliest,
		End:        latest,
		Duration:   evidence.UnknownDuration(),
		SourceRefs: refs,
		TopicHint:  &evidence.TopicHint{Repository: repo, PRNumber: pr, Branch: branch},
	}
}

func calendarEvidence(id, title string, start, end time.Time, inv evidence.InvitationStatus) evidence.Evidence {
	return evidence.Evidence{
		ID:         id,
		Kind:       evidence.SourceCalendar,
		Title:      title,
		Start:      start,
		End:        end,
		Status:     evidence.EventStatusConfirmed,
		Invitation: inv,
		Duration:   evidence.KnownDuration(DurationMinutes(start, end)),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceCalendar, FeedID: "cal", EventUID: id}},
	}
}

func groupsForPR(p Proposal, pr int) int {
	for _, t := range p.Topics {
		if t.PRNumber == pr {
			return len(t.Groups)
		}
	}
	return -1
}

// TestBuild_IsolatedCommitSpanStartEqualsEnd proves an isolated commit yields a
// point group (SpanStart==SpanEnd) with an unknown duration and no zero block.
func TestBuild_IsolatedCommitSpanStartEqualsEnd(t *testing.T) {
	p, err := Build([]evidence.Evidence{commitEvidence("c1", "acme/app", "sha1", at(6, 9, 0), 0, "main")}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(p.Topics))
	}
	g := p.Topics[0].Groups
	if len(g) != 1 {
		t.Fatalf("groups = %d, want 1", len(g))
	}
	if !g[0].SpanStart.Equal(at(6, 9, 0)) || !g[0].SpanEnd.Equal(at(6, 9, 0)) {
		t.Errorf("span = %v..%v, want 09:00..09:00", g[0].SpanStart, g[0].SpanEnd)
	}
	if g[0].Duration.Known {
		t.Error("git group duration must be unknown")
	}
	if len(p.Meetings) != 0 || len(p.Manual) != 0 {
		t.Error("isolated commit must not produce a zero block")
	}
}

// TestBuild_AdjacencySplitsByGap proves the 45-minute adjacency heuristic using
// per-commit SourceRef.At times.
func TestBuild_AdjacencySplitsByGap(t *testing.T) {
	pr7 := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 9, 0), "b": at(6, 16, 0),
	})
	pr9 := prEvidence("pr9", "acme/app", 9, "feature/b", map[string]time.Time{
		"c": at(6, 9, 0), "d": at(6, 9, 35),
	})
	p, err := Build([]evidence.Evidence{pr7, pr9}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := groupsForPR(p, 7); got != 2 {
		t.Errorf("PR7 groups = %d, want 2 (09:00 & 16:00)", got)
	}
	if got := groupsForPR(p, 9); got != 1 {
		t.Errorf("PR9 groups = %d, want 1 (09:00 & 09:35)", got)
	}
}

// TestBuild_MeetingInGapBreaksGroup proves a meeting inside an open gap breaks
// the group.
func TestBuild_MeetingInGapBreaksGroup(t *testing.T) {
	pr := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 9, 0), "b": at(6, 9, 35),
	})
	meeting := calendarEvidence("m1", "Standup", at(6, 9, 15), at(6, 9, 20), evidence.InvitationNone)
	p, err := Build([]evidence.Evidence{pr, meeting}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := groupsForPR(p, 7); got != 2 {
		t.Errorf("groups = %d, want 2 (meeting breaks the gap)", got)
	}
}

// TestBuild_MeetingOverlapCollisionInterrupted proves a meeting overlapping a git
// group marks the group InterruptedBy and the meeting Collision.
func TestBuild_MeetingOverlapCollisionInterrupted(t *testing.T) {
	pr := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 9, 0), "b": at(6, 9, 35),
	})
	meeting := calendarEvidence("m1", "Standup", at(6, 9, 20), at(6, 9, 40), evidence.InvitationAccepted)
	p, err := Build([]evidence.Evidence{pr, meeting}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(p.Meetings))
	}
	meetingID := p.Meetings[0].ID
	if !p.Meetings[0].Collision {
		t.Error("overlapping meeting must be Collision")
	}
	if !p.Meetings[0].Preselected {
		t.Error("accepted invitation must preselect attendance")
	}
	if p.Meetings[0].Attendance != AttendanceUnresolved {
		t.Errorf("attendance = %q, want unresolved", p.Meetings[0].Attendance)
	}

	var interrupted []string
	for _, g := range p.Topics[0].Groups {
		if g.SpanStart.Equal(at(6, 9, 35)) {
			interrupted = g.InterruptedBy
		}
	}
	if len(interrupted) != 1 || interrupted[0] != meetingID {
		t.Errorf("InterruptedBy = %v, want [%s]", interrupted, meetingID)
	}
}

// TestBuild_MidnightSplitCarryover proves a cross-midnight manual interval is
// split into two LINKED fragments on consecutive local dates: the same-day
// fragment stays on day D and the later-day fragment is surfaced as a durable
// carry (prop.Carries). The exact total minutes are preserved.
func TestBuild_MidnightSplitCarryover(t *testing.T) {
	manual := evidence.Evidence{
		ID: "man1", Kind: evidence.SourceManual, Title: "late work",
		Start: at(6, 23, 30), End: at(7, 0, 30), Duration: evidence.UnknownDuration(),
	}
	p, err := Build([]evidence.Evidence{manual}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Manual) != 1 {
		t.Fatalf("manual blocks = %d, want 1 (same-day fragment only)", len(p.Manual))
	}
	b := p.Manual[0]
	if b.Start != "23:30" || b.End != "00:00" {
		t.Errorf("fragment = %s..%s, want 23:30..00:00", b.Start, b.End)
	}
	if !b.Duration.Known || b.Duration.Minutes != 30 {
		t.Errorf("fragment duration = %+v, want known 30", b.Duration)
	}
	if b.Carryover {
		t.Error("the same-day fragment must not be marked Carryover")
	}
	if b.SplitGroupID == "" || b.PartIndex != 0 {
		t.Errorf("same-day fragment must be linked (group non-empty, part 0): group=%q part=%d", b.SplitGroupID, b.PartIndex)
	}

	if len(p.Carries) != 1 {
		t.Fatalf("carries = %d, want 1 (the later-day fragment)", len(p.Carries))
	}
	c := p.Carries[0]
	if c.Date != "2026-10-07" || c.Start != "00:00" || c.End != "00:30" {
		t.Errorf("carry = %s %s-%s, want 2026-10-07 00:00-00:30", c.Date, c.Start, c.End)
	}
	if c.Minutes != 30 {
		t.Errorf("carry minutes = %d, want 30", c.Minutes)
	}
	if c.SplitGroupID != b.SplitGroupID {
		t.Errorf("carry group = %q, want the same group as the same-day fragment %q", c.SplitGroupID, b.SplitGroupID)
	}
	if c.PartIndex != 1 {
		t.Errorf("carry part = %d, want 1", c.PartIndex)
	}
	if b.Duration.Minutes+c.Minutes != 60 {
		t.Errorf("exact total = %d, want 60", b.Duration.Minutes+c.Minutes)
	}

	// The composed split itself marks the D+1 fragment Carryover.
	frags := SplitInterval(at(6, 23, 30), at(7, 0, 30), DefaultMaxEntryMinutes, time.UTC)
	if len(frags) != 2 || frags[0].Carryover || !frags[1].Carryover {
		t.Fatalf("SplitInterval carryover flags wrong: %+v", frags)
	}
	if frags[0].SplitGroupID != frags[1].SplitGroupID {
		t.Error("SplitInterval fragments must share SplitGroupID")
	}

	found := false
	for _, w := range p.Warnings {
		if w == "carryover:2026-10-07" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected carryover:2026-10-07, got %v", p.Warnings)
	}
}

// TestBuild_FragmentIdentityPreSlice pins the PRE-SLICE block identity scheme so
// re-confirming an existing split fragment cannot insert a duplicate: a
// four-hour fragment keeps hashID("split", baseID, part) and a midnight
// same-day fragment keeps the baseID (it was never four-hour-split pre-slice).
func TestBuild_FragmentIdentityPreSlice(t *testing.T) {
	// Four-hour split: baseID is derived from the ORIGINAL interval start.
	manual := evidence.Evidence{
		ID: "man1", Kind: evidence.SourceManual, Title: "long",
		Start: at(6, 8, 0), End: at(6, 14, 0), Duration: evidence.KnownDuration(360),
	}
	p, err := Build([]evidence.Evidence{manual}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Manual) != 2 {
		t.Fatalf("manual fragments = %d, want 2", len(p.Manual))
	}
	base := hashID("manual", "2026-10-06", at(6, 8, 0).UTC().Format(time.RFC3339Nano), "man1")
	for i, b := range p.Manual {
		if want := hashID("split", base, strconv.Itoa(i)); b.ID != want {
			t.Errorf("fragment %d id = %q, want pre-slice %q", i, b.ID, want)
		}
	}

	// Midnight only (same-day portion <= 4h): the same-day fragment keeps baseID.
	cross := evidence.Evidence{
		ID: "man2", Kind: evidence.SourceManual, Title: "late",
		Start: at(6, 23, 30), End: at(7, 0, 30), Duration: evidence.KnownDuration(60),
	}
	p2, err := Build([]evidence.Evidence{cross}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p2.Manual) != 1 {
		t.Fatalf("same-day fragments = %d, want 1", len(p2.Manual))
	}
	base2 := hashID("manual", "2026-10-06", at(6, 23, 30).UTC().Format(time.RFC3339Nano), "man2")
	if p2.Manual[0].ID != base2 {
		t.Errorf("midnight same-day fragment id = %q, want pre-slice base %q", p2.Manual[0].ID, base2)
	}
}

// TestBuild_WorkBlockCrossesMidnightSplits proves F6: a git Work block whose
// group span crosses local midnight is split in the PROPOSAL (via the composed
// SplitInterval), so review previews exactly what ConfirmProposed persists.
func TestBuild_WorkBlockCrossesMidnightSplits(t *testing.T) {
	pr := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 23, 45), "b": at(7, 0, 15),
	})
	p, err := Build([]evidence.Evidence{pr}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Work) != 2 {
		t.Fatalf("a midnight-crossing work block must be split into 2 fragments, got %d: %+v", len(p.Work), p.Work)
	}
	if p.Work[0].Date != "2026-10-06" || p.Work[0].End != "00:00" {
		t.Errorf("fragment 0 = %s %s-%s, want 2026-10-06 23:45-00:00", p.Work[0].Date, p.Work[0].Start, p.Work[0].End)
	}
	if p.Work[1].Date != "2026-10-07" || p.Work[1].Start != "00:00" {
		t.Errorf("fragment 1 = %s %s-%s, want 2026-10-07 00:00-00:15", p.Work[1].Date, p.Work[1].Start, p.Work[1].End)
	}
	if p.Work[0].SplitGroupID == "" || p.Work[0].SplitGroupID != p.Work[1].SplitGroupID {
		t.Error("work fragments must share a SplitGroupID")
	}
	if p.Work[0].PartIndex != 0 || p.Work[1].PartIndex != 1 {
		t.Errorf("work part indexes = %d,%d, want 0,1", p.Work[0].PartIndex, p.Work[1].PartIndex)
	}
}

// TestBuild_CrossRepoSameBranchSeparate proves identical branch names in
// different repositories stay separate topics.
func TestBuild_CrossRepoSameBranchSeparate(t *testing.T) {
	c1 := commitEvidence("c1", "acme/app", "s1", at(6, 9, 0), 0, "feature/x")
	c2 := commitEvidence("c2", "other/app", "s2", at(6, 9, 0), 0, "feature/x")
	p, err := Build([]evidence.Evidence{c1, c2}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Topics) != 2 {
		t.Fatalf("topics = %d, want 2", len(p.Topics))
	}
	if p.Topics[0].Repository == p.Topics[1].Repository {
		t.Error("same branch in two repos must not merge")
	}
}

// TestBuild_ExcludesCancelledDeclinedUnknown proves excluded evidence is retained
// with a reason and agent evidence is context only.
func TestBuild_ExcludesCancelledDeclinedUnknown(t *testing.T) {
	cancelled := calendarEvidence("cal-cancel", "Cancelled", at(6, 9, 0), at(6, 10, 0), evidence.InvitationNone)
	cancelled.Status = evidence.EventStatusCancelled
	declined := calendarEvidence("cal-decline", "Declined", at(6, 11, 0), at(6, 12, 0), evidence.InvitationDeclined)
	unknown := calendarEvidence("cal-unknown", "Unknown", at(6, 13, 0), at(6, 14, 0), evidence.InvitationUnknown)
	agent := evidence.Evidence{
		ID: "ag1", Kind: evidence.SourceAgentSession, Start: at(6, 9, 0),
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceAgentSession, SessionID: "s1", At: at(6, 9, 0)}},
	}

	p, err := Build([]evidence.Evidence{cancelled, declined, unknown, agent}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Meetings) != 0 {
		t.Error("excluded events must not form meetings")
	}
	if len(p.Topics) != 0 {
		t.Error("agent evidence must not form a topic")
	}
	reasons := map[string]ExclusionReason{}
	for _, e := range p.Excluded {
		reasons[e.EvidenceID] = e.Reason
	}
	if reasons["cal-cancel"] != ExcludeCancelled {
		t.Errorf("cancelled reason = %q", reasons["cal-cancel"])
	}
	if reasons["cal-decline"] != ExcludeDeclined {
		t.Errorf("declined reason = %q", reasons["cal-decline"])
	}
	if reasons["cal-unknown"] != ExcludeUnknownInvitation {
		t.Errorf("unknown reason = %q", reasons["cal-unknown"])
	}
	if reasons["ag1"] != ExcludeAgentContextOnly {
		t.Errorf("agent reason = %q", reasons["ag1"])
	}
}

// TestBuild_ManualProposedNotConfirmed proves manual intervals are proposed, not
// confirmed, and resolve a project from hints.
func TestBuild_ManualProposedNotConfirmed(t *testing.T) {
	manual := evidence.Evidence{
		ID: "man1", Kind: evidence.SourceManual, Title: "work", Description: "did stuff",
		Start: at(6, 9, 0), End: at(6, 10, 0),
		Duration:     evidence.KnownDuration(60),
		ProjectHints: []string{"Proj"},
	}
	p, err := Build([]evidence.Evidence{manual}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Manual) != 1 {
		t.Fatalf("manual = %d, want 1", len(p.Manual))
	}
	b := p.Manual[0]
	if b.Kind != BlockManual {
		t.Errorf("kind = %q, want manual", b.Kind)
	}
	if b.Preselected {
		t.Error("manual intervals must not be preselected/confirmed")
	}
	if b.Attendance != AttendanceUnresolved {
		t.Errorf("attendance = %q, want unresolved", b.Attendance)
	}
	if !b.Project.Resolved || b.Project.Project != "Proj" {
		t.Errorf("project = %+v, want resolved Proj", b.Project)
	}
	if !b.Duration.Known || b.Duration.Minutes != 60 {
		t.Errorf("duration = %+v, want known 60", b.Duration)
	}
}

// TestBuild_MeetingDurationKnown proves meetings carry a calendar-known duration.
func TestBuild_MeetingDurationKnown(t *testing.T) {
	meeting := calendarEvidence("m1", "Meeting", at(6, 9, 0), at(6, 10, 0), evidence.InvitationAccepted)
	p, err := Build([]evidence.Evidence{meeting}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(p.Meetings))
	}
	b := p.Meetings[0]
	if !b.Duration.Known || b.Duration.Minutes != 60 {
		t.Errorf("duration = %+v, want known 60", b.Duration)
	}
	if b.Project.Resolved {
		t.Error("calendar titles must not choose a project")
	}
}

// TestBuild_EmptyDay proves a day without evidence is an empty draft.
func TestBuild_EmptyDay(t *testing.T) {
	p, err := Build(nil, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Warnings) != 1 || p.Warnings[0] != "empty_day" {
		t.Errorf("warnings = %v, want [empty_day]", p.Warnings)
	}
	if len(p.Topics) != 0 || len(p.Meetings) != 0 || len(p.Manual) != 0 {
		t.Error("empty day must produce no blocks")
	}
}

// TestBuild_DeterminismShuffledInput proves identical serialized output under
// shuffled input.
func TestBuild_DeterminismShuffledInput(t *testing.T) {
	evs := []evidence.Evidence{
		prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{"a": at(6, 9, 0), "b": at(6, 16, 0)}),
		commitEvidence("c1", "other/app", "s1", at(6, 9, 0), 0, "main"),
		calendarEvidence("m1", "Meeting", at(6, 10, 0), at(6, 11, 0), evidence.InvitationAccepted),
		{ID: "man1", Kind: evidence.SourceManual, Title: "work", Start: at(6, 13, 0), End: at(6, 14, 0), Duration: evidence.KnownDuration(60)},
		func() evidence.Evidence {
			e := calendarEvidence("cal-cancel", "Cancelled", at(6, 9, 0), at(6, 10, 0), evidence.InvitationNone)
			e.Status = evidence.EventStatusCancelled
			return e
		}(),
	}

	p1, err := Build(evs, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	shuffled := make([]evidence.Evidence, len(evs))
	for i := range evs {
		shuffled[i] = evs[len(evs)-1-i]
	}
	p2, err := Build(shuffled, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	if string(b1) != string(b2) {
		t.Errorf("proposal is not deterministic under shuffled input:\n%s\n%s", b1, b2)
	}
}

// TestBuild_MeetingCoveringGapDoesNotSplit pins the P2 intent: a meeting that
// fully covers a gap of at most the adjacency threshold does NOT split the group
// (its start is not strictly inside the open gap). The group still records
// InterruptedBy and the meeting block still records Collision.
func TestBuild_MeetingCoveringGapDoesNotSplit(t *testing.T) {
	pr := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 9, 0), "b": at(6, 9, 40),
	})
	meeting := calendarEvidence("m1", "Long Meeting", at(6, 9, 0), at(6, 9, 40), evidence.InvitationAccepted)
	p, err := Build([]evidence.Evidence{pr, meeting}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := groupsForPR(p, 7); got != 1 {
		t.Fatalf("groups = %d, want 1 (meeting start is not strictly inside the gap)", got)
	}
	group := p.Topics[0].Groups[0]
	if len(p.Meetings) != 1 {
		t.Fatalf("meetings = %d, want 1", len(p.Meetings))
	}
	if len(group.InterruptedBy) != 1 || group.InterruptedBy[0] != p.Meetings[0].ID {
		t.Errorf("InterruptedBy = %v, want [%s]", group.InterruptedBy, p.Meetings[0].ID)
	}
	if !p.Meetings[0].Collision {
		t.Error("overlapping meeting must be Collision")
	}
}

// TestBuild_ExcludesGitWithoutRepository proves exported Build refuses git
// evidence with no repository identity instead of silently merging repo-less
// items on the same branch.
func TestBuild_ExcludesGitWithoutRepository(t *testing.T) {
	mk := func(id, sha string) evidence.Evidence {
		return evidence.Evidence{
			ID: id, Kind: evidence.SourceLocalGit, Title: "commit " + sha, Start: at(6, 9, 0),
			Duration:   evidence.UnknownDuration(),
			SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, CommitSHA: sha, At: at(6, 9, 0)}},
			TopicHint:  &evidence.TopicHint{Branch: "main"},
		}
	}
	p, err := Build([]evidence.Evidence{mk("g1", "s1"), mk("g2", "s2")}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Topics) != 0 {
		t.Errorf("repo-less git evidence must not form a topic, got %d", len(p.Topics))
	}
	if len(p.Excluded) != 2 {
		t.Fatalf("excluded = %d, want 2", len(p.Excluded))
	}
	for _, e := range p.Excluded {
		if e.Reason != ExcludeMissingRepository {
			t.Errorf("reason = %q, want %q", e.Reason, ExcludeMissingRepository)
		}
	}
}

// TestBuild_WorkBlocksFromGitGroups proves P1: git Activity Groups materialize
// into ProposedTimeBlocks (Kind work, unknown duration) with project hints
// carried through from the git adapter.
func TestBuild_WorkBlocksFromGitGroups(t *testing.T) {
	c := commitEvidence("c1", "acme/app", "sha1", at(6, 9, 0), 0, "main")
	c.ProjectHints = []string{"Proj"}
	p, err := Build([]evidence.Evidence{c}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Work) != 1 {
		t.Fatalf("work blocks = %d, want 1", len(p.Work))
	}
	b := p.Work[0]
	if b.Kind != BlockWork {
		t.Errorf("kind = %q, want work", b.Kind)
	}
	if b.Duration.Known {
		t.Error("work block duration must be unknown")
	}
	if !b.Project.Resolved || b.Project.Project != "Proj" {
		t.Errorf("project = %+v, want resolved Proj from hints", b.Project)
	}
}

// TestBuild_JointBlockForInterruptedGroup proves P1: a git group overlapped by a
// meeting materializes as ONE joint block (union span, both evidence IDs, the
// meeting's known duration, Collision).
func TestBuild_JointBlockForInterruptedGroup(t *testing.T) {
	pr := prEvidence("pr7", "acme/app", 7, "feature/a", map[string]time.Time{
		"a": at(6, 9, 0), "b": at(6, 9, 40),
	})
	meeting := calendarEvidence("m1", "Standup", at(6, 9, 0), at(6, 9, 40), evidence.InvitationAccepted)
	p, err := Build([]evidence.Evidence{pr, meeting}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Work) != 1 {
		t.Fatalf("work blocks = %d, want 1", len(p.Work))
	}
	b := p.Work[0]
	if b.Kind != BlockJoint {
		t.Errorf("kind = %q, want joint", b.Kind)
	}
	if !b.Collision {
		t.Error("joint block must be Collision")
	}
	if len(b.EvidenceIDs) != 2 {
		t.Errorf("evidence = %v, want both work and meeting", b.EvidenceIDs)
	}
	if !b.Duration.Known {
		t.Error("joint duration should be the meeting's known duration")
	}
}
