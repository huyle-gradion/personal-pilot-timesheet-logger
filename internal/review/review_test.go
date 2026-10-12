package review

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func at(h, m int) time.Time { return time.Date(2026, 10, 6, h, m, 0, 0, time.UTC) }

func testProposalOpts() proposal.ProposalOptions {
	return proposal.ProposalOptions{Location: time.UTC, Date: at(0, 0), Now: at(12, 0)}
}

func meetingEvidence(id string, start, end time.Time, inv evidence.InvitationStatus) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceCalendar, Title: "Meeting " + id,
		Start: start, End: end, Status: evidence.EventStatusConfirmed, Invitation: inv,
		Duration:   evidence.KnownDuration(proposal.DurationMinutes(start, end)),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceCalendar, FeedID: "cal", EventUID: id}},
	}
}

func gitEvidence(id, repo, sha string, when time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceLocalGit, Title: "commit " + sha, Start: when,
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: repo, CommitSHA: sha, At: when}},
		TopicHint:  &evidence.TopicHint{Repository: repo, Branch: "main"},
	}
}

func TestConfirmMeetingsBulk(t *testing.T) {
	// attended=true creates one confirmed interval per meeting.
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		meetingEvidence("m1", at(9, 0), at(10, 0), evidence.InvitationAccepted),
	}}
	if err := ConfirmMeetingsBulk(&r, []string{"m1"}, true); err != nil {
		t.Fatalf("ConfirmMeetingsBulk: %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("confirmed = %d, want 1", len(r.Confirmed))
	}
	if !r.Confirmed[0].Attendance || r.Confirmed[0].Kind != string(proposal.BlockMeeting) {
		t.Errorf("unexpected interval: %+v", r.Confirmed[0])
	}

	// attended=false: no work, decision + exclusion, evidence retained.
	r2 := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		meetingEvidence("m1", at(9, 0), at(10, 0), evidence.InvitationAccepted),
	}}
	if err := ConfirmMeetingsBulk(&r2, []string{"m1"}, false); err != nil {
		t.Fatalf("ConfirmMeetingsBulk: %v", err)
	}
	if len(r2.Confirmed) != 0 {
		t.Error("not-attended meeting must not become work")
	}
	if r2.Review == nil || len(r2.Review.Attendance) != 1 || r2.Review.Attendance[0].Attended {
		t.Errorf("attendance decision missing: %+v", r2.Review)
	}
	if len(r2.Review.ExcludedEvidence) != 1 || r2.Review.ExcludedEvidence[0].Reason != ReasonNotAttended {
		t.Errorf("exclusion missing: %+v", r2.Review.ExcludedEvidence)
	}
	if len(r2.CollectedEvidence) != 1 {
		t.Error("evidence must be retained")
	}
}

func TestJoinCollision_CountsOnce(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		gitEvidence("g1", "acme/app", "sha1", at(9, 0)),
		meetingEvidence("m1", at(9, 20), at(9, 40), evidence.InvitationAccepted),
	}}
	work := proposal.ProposedTimeBlock{
		ID: "w1", Kind: proposal.BlockWork, Date: "2026-10-06",
		Start: "09:00", End: "09:35", StartAt: at(9, 0), EndAt: at(9, 35),
		Duration: evidence.UnknownDuration(), EvidenceIDs: []string{"g1"}, Description: "commit sha1",
	}
	if err := ConfirmProposed(&r, work, ConfirmOptions{Project: "P"}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("confirmed = %d, want 1", len(r.Confirmed))
	}
	workID := r.Confirmed[0].ID

	if err := JoinCollision(&r, workID, "m1"); err != nil {
		t.Fatalf("JoinCollision: %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("joint must be ONE interval, got %d", len(r.Confirmed))
	}
	j := r.Confirmed[0]
	if j.Kind != string(proposal.BlockJoint) {
		t.Errorf("kind = %q, want joint", j.Kind)
	}
	if j.StartTime != "09:00" || j.EndTime != "09:40" {
		t.Errorf("union span = %s-%s, want 09:00-09:40", j.StartTime, j.EndTime)
	}
	if len(j.EvidenceIDs) != 2 {
		t.Errorf("evidence = %v, want both", j.EvidenceIDs)
	}
	if !strings.Contains(j.Description, "commit sha1") || !strings.Contains(j.Description, "Meeting m1") {
		t.Errorf("description = %q, want both texts", j.Description)
	}
}

func TestManualInterval_RequiresExplicitConfirm(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	if err := AddManualInterval(&r, ManualInterval{StartAt: at(9, 0), EndAt: at(10, 0), Project: "P", Description: "manual work"}); err != nil {
		t.Fatalf("AddManualInterval: %v", err)
	}
	if len(r.Confirmed) != 0 {
		t.Fatal("manual interval must NOT auto-confirm")
	}

	prop, err := CurrentProposal(r, testProposalOpts())
	if err != nil {
		t.Fatalf("CurrentProposal: %v", err)
	}
	if len(prop.Manual) != 1 {
		t.Fatalf("proposed manual blocks = %d, want 1", len(prop.Manual))
	}
	if err := ConfirmProposed(&r, prop.Manual[0], ConfirmOptions{}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	if len(r.Confirmed) != 1 || !r.Confirmed[0].Manual {
		t.Errorf("explicit manual confirm failed: %+v", r.Confirmed)
	}
}

func TestDecisionsPersistThroughStore(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"
	store, err := workflow.NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	if _, err := store.UpdateDay(ctx, date, func(r *workflow.Record) error {
		r.CollectedEvidence = []evidence.Evidence{meetingEvidence("m1", at(9, 0), at(10, 0), evidence.InvitationAccepted)}
		return ConfirmMeetingsBulk(r, []string{"m1"}, true)
	}); err != nil {
		t.Fatalf("UpdateDay: %v", err)
	}

	fresh, err := workflow.NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(got.Confirmed) != 1 || !got.Confirmed[0].Attendance {
		t.Errorf("confirmed decisions not persisted: %+v", got.Confirmed)
	}
}

// TestRefreshPreservesDecisions proves an evidence refresh (upsert by ID) never
// wipes confirmed decisions or review state.
func TestRefreshPreservesDecisions(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"
	store, err := workflow.NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *workflow.Record) error {
		r.CollectedEvidence = []evidence.Evidence{meetingEvidence("m1", at(9, 0), at(10, 0), evidence.InvitationAccepted)}
		if err := ConfirmMeetingsBulk(r, []string{"m1"}, true); err != nil {
			return err
		}
		return ExcludeEvidence(r, "e9", ReasonUserExcluded)
	}); err != nil {
		t.Fatalf("initial UpdateDay: %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *workflow.Record) error {
		id := r.Confirmed[0].ID
		workflow.MergeConfirmed(r, []workflow.ConfirmedInterval{{ID: id, Description: "edited"}})
		workflow.MergeEvidence(r, []workflow.EvidenceReference{{ID: "e9", Source: "git", Title: "x"}})
		return nil
	}); err != nil {
		t.Fatalf("refresh UpdateDay: %v", err)
	}

	fresh, _ := workflow.NewJSONFileStore(root)
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(got.Confirmed) != 1 || got.Confirmed[0].Description != "edited" {
		t.Errorf("edit lost: %+v", got.Confirmed)
	}
	if !got.Confirmed[0].Attendance {
		t.Error("attendance wiped by refresh")
	}
	if got.Review == nil || len(got.Review.ExcludedEvidence) != 1 {
		t.Errorf("exclusion lost: %+v", got.Review)
	}
}

func TestCurrentProposal_ChangedAndHash(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		gitEvidence("g1", "acme/app", "sha1", at(9, 0)),
	}}
	prop, err := CurrentProposal(r, testProposalOpts())
	if err != nil {
		t.Fatalf("CurrentProposal: %v", err)
	}
	found := false
	for _, id := range prop.Changed {
		if id == "g1" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected g1 in Changed, got %v", prop.Changed)
	}
	if h1, h2 := ProposalHash(prop), ProposalHash(prop); h1 == "" || h1 != h2 {
		t.Errorf("unstable proposal hash: %q / %q", h1, h2)
	}
}

func TestMarkReviewedEmpty_NoZeroBlock(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	if err := MarkReviewedEmpty(&r); err != nil {
		t.Fatalf("MarkReviewedEmpty: %v", err)
	}
	if r.Review == nil || !r.Review.Reviewed || !r.Review.Empty {
		t.Fatalf("review state = %+v", r.Review)
	}
	if len(r.Confirmed) != 0 {
		t.Errorf("empty day must not invent a zero-hour block: %+v", r.Confirmed)
	}
	if rd := Readiness(r); rd.Blocked {
		t.Errorf("reviewed empty day must not be blocked: %+v", rd)
	}
}

// TestMarkReviewedEmpty_PreservesCollections proves MarkReviewedEmpty keeps prior
// exclusions, attendance decisions, and acknowledgments (S4L2-1).
func TestMarkReviewedEmpty_PreservesCollections(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", Review: &workflow.ReviewState{
		ExcludedEvidence:    []workflow.ExcludedEvidence{{EvidenceID: "e1", Reason: ReasonUserExcluded}},
		Attendance:          []workflow.AttendanceDecision{{EvidenceID: "m1", Attended: false}},
		AcknowledgedSources: []string{"git"},
	}}
	if err := MarkReviewedEmpty(&r); err != nil {
		t.Fatalf("MarkReviewedEmpty: %v", err)
	}
	if !r.Review.Reviewed || !r.Review.Empty {
		t.Error("flags not set")
	}
	if len(r.Review.ExcludedEvidence) != 1 || len(r.Review.Attendance) != 1 || len(r.Review.AcknowledgedSources) != 1 {
		t.Errorf("collections lost: %+v", r.Review)
	}
}

// TestConfirmProposed_MidnightSplitCarryAndLinkage proves R3/R4/R6: confirming a
// cross-midnight interval persists ONLY its same-day fragment on day D and
// records the later-day fragment as a durable carry; confirming that carry on
// day D+1 persists an interval linked to the SAME split group.
func TestConfirmProposed_MidnightSplitCarryAndLinkage(t *testing.T) {
	d := "2026-10-06"
	r := workflow.Record{Date: d}
	block := proposal.ProposedTimeBlock{
		ID: "b1", Kind: proposal.BlockManual, Date: d,
		Start: "23:30", End: "00:30",
		StartAt:     at(23, 30),
		EndAt:       time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC),
		Duration:    evidence.KnownDuration(60),
		Description: "late work",
	}
	if err := ConfirmProposed(&r, block, ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("confirmed = %d, want 1 (same-day fragment only)", len(r.Confirmed))
	}
	ci := r.Confirmed[0]
	if ci.Date != d || ci.StartTime != "23:30" || ci.EndTime != "00:00" {
		t.Errorf("confirmed = %s %s-%s, want %s 23:30-00:00", ci.Date, ci.StartTime, ci.EndTime, d)
	}
	if ci.SplitGroupID == "" || ci.PartIndex != 0 || ci.Carryover {
		t.Errorf("same-day fragment must be linked and not a carry: %+v", ci)
	}
	if len(r.Carries) != 1 {
		t.Fatalf("carries = %d, want 1 (the later-day fragment)", len(r.Carries))
	}
	carry := r.Carries[0]
	if carry.Date != "2026-10-07" || carry.Start != "00:00" || carry.End != "00:30" {
		t.Errorf("carry = %s %s-%s, want 2026-10-07 00:00-00:30", carry.Date, carry.Start, carry.End)
	}
	if carry.SourceDate != d {
		t.Errorf("carry source day = %q, want %q", carry.SourceDate, d)
	}
	if carry.SplitGroupID != ci.SplitGroupID || carry.PartIndex != 1 {
		t.Fatalf("carry linkage wrong: %+v vs group %q", carry, ci.SplitGroupID)
	}
	if carry.Minutes != 30 {
		t.Errorf("carry minutes = %d, want 30", carry.Minutes)
	}

	// D+1: confirming the carry keeps the SAME group/part.
	r2 := workflow.Record{Date: "2026-10-07"}
	if err := ConfirmCarry(&r2, carry, ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("ConfirmCarry: %v", err)
	}
	if len(r2.Confirmed) != 1 {
		t.Fatalf("D+1 confirmed = %d, want 1", len(r2.Confirmed))
	}
	ci2 := r2.Confirmed[0]
	if ci2.Date != "2026-10-07" || ci2.StartTime != "00:00" || ci2.EndTime != "00:30" {
		t.Errorf("D+1 interval = %s %s-%s, want 2026-10-07 00:00-00:30", ci2.Date, ci2.StartTime, ci2.EndTime)
	}
	if ci2.SplitGroupID != carry.SplitGroupID || ci2.PartIndex != carry.PartIndex {
		t.Fatalf("D+1 interval lost the split linkage: %+v vs carry %+v", ci2, carry)
	}
	if !ci2.Carryover {
		t.Error("the D+1 interval must be marked Carryover")
	}
}

// TestRecordCarries_RequiresConfirmedGroup proves an unconfirmed (or
// never-reviewed) day records NO carry, so the later day cannot surface a carry
// from work that was never confirmed.
func TestRecordCarries_RequiresConfirmedGroup(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	if err := RecordCarries(&r, []workflow.CarryItem{{
		Date: "2026-10-07", SplitGroupID: "split_unconfirmed", PartIndex: 1,
	}}); err != nil {
		t.Fatalf("RecordCarries: %v", err)
	}
	if len(r.Carries) != 0 {
		t.Fatalf("an unconfirmed group must not record a carry: %+v", r.Carries)
	}

	r.Confirmed = []workflow.ConfirmedInterval{{ID: "c1", Date: r.Date, EndTime: "00:00", SplitGroupID: "split_ok"}}
	if err := RecordCarries(&r, []workflow.CarryItem{{
		Date: "2026-10-07", SplitGroupID: "split_ok", PartIndex: 1, Minutes: 30,
	}}); err != nil {
		t.Fatalf("RecordCarries: %v", err)
	}
	if len(r.Carries) != 1 || r.Carries[0].SplitGroupID != "split_ok" {
		t.Fatalf("a confirmed group must record its carry: %+v", r.Carries)
	}
}

// TestConfirmProposed_SplitFragmentIdentityPreSlice proves F3: a split fragment
// persists with the PRE-SLICE identity, and re-confirming it does not duplicate.
func TestConfirmProposed_SplitFragmentIdentityPreSlice(t *testing.T) {
	d := "2026-10-06"
	start := at(8, 0)
	r := workflow.Record{Date: d, CollectedEvidence: []evidence.Evidence{{
		ID: "man1", Kind: evidence.SourceManual, Title: "long",
		Start: start, End: at(14, 0), Duration: evidence.KnownDuration(360),
	}}}
	prop, err := CurrentProposal(r, testProposalOpts())
	if err != nil {
		t.Fatalf("CurrentProposal: %v", err)
	}
	if len(prop.Manual) != 2 {
		t.Fatalf("manual fragments = %d, want 2", len(prop.Manual))
	}
	base := hashID("manual", d, start.UTC().Format(time.RFC3339Nano), "man1")
	for i, b := range prop.Manual {
		if want := hashID("split", base, strconv.Itoa(i)); b.ID != want {
			t.Fatalf("fragment %d id = %q, want pre-slice %q", i, b.ID, want)
		}
	}
	if err := ConfirmProposed(&r, prop.Manual[0], ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	wantID := hashID("conf-manual", d, prop.Manual[0].ID)
	if len(r.Confirmed) != 1 || r.Confirmed[0].ID != wantID {
		t.Fatalf("persisted id = %+v, want pre-slice %q", r.Confirmed, wantID)
	}
	if err := ConfirmProposed(&r, prop.Manual[0], ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("re-confirm: %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("re-confirming a split fragment must not duplicate, got %d", len(r.Confirmed))
	}
}

// TestRecordCarries_ReconcilesStaleCarry proves F2: a carry is dropped once its
// source interval is excluded or trimmed so it no longer reaches midnight.
func TestRecordCarries_ReconcilesStaleCarry(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	block := proposal.ProposedTimeBlock{
		ID: "b1", Kind: proposal.BlockManual, Date: "2026-10-06",
		Start: "23:30", End: "00:30",
		StartAt: at(23, 30), EndAt: time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC),
		Duration: evidence.KnownDuration(60),
	}
	if err := ConfirmProposed(&r, block, ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	if len(r.Carries) != 1 {
		t.Fatalf("want a carry after confirming a crossing interval, got %+v", r.Carries)
	}

	// Trimming the source so it no longer reaches midnight drops the carry.
	r.Confirmed[0].EndTime = "23:45"
	if err := RecordCarries(&r, nil); err != nil {
		t.Fatalf("RecordCarries(trim): %v", err)
	}
	if len(r.Carries) != 0 {
		t.Fatalf("a trimmed-away carry must be reconciled, got %+v", r.Carries)
	}

	// Re-confirm and then exclude the source: the carry is dropped again.
	r.Confirmed = nil
	if err := ConfirmProposed(&r, block, ConfirmOptions{Location: time.UTC}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	r.Confirmed = nil // exclusion removed the interval (and its group)
	if err := RecordCarries(&r, nil); err != nil {
		t.Fatalf("RecordCarries(exclude): %v", err)
	}
	if len(r.Carries) != 0 {
		t.Fatalf("an excluded carry must be reconciled, got %+v", r.Carries)
	}
}

// TestConfirmProposed_ClearsEmpty proves confirming a block clears a stale
// reviewed-empty flag so readiness cannot short-circuit (S4L2-2).
func TestConfirmProposed_ClearsEmpty(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", Review: &workflow.ReviewState{Reviewed: true, Empty: true}}
	block := proposal.ProposedTimeBlock{
		ID: "b1", Kind: proposal.BlockWork, Date: "2026-10-06",
		Start: "09:00", End: "10:00", StartAt: at(9, 0), EndAt: at(10, 0),
		Duration: evidence.UnknownDuration(), EvidenceIDs: []string{"g1"},
	}
	if err := ConfirmProposed(&r, block, ConfirmOptions{Project: "P"}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	if r.Review.Empty {
		t.Error("Empty must be cleared once a block is confirmed")
	}
}

// TestAddManualInterval_StableIdentity proves the manual evidence identity is
// evidence.ManualID(date, uuid), so editing the description does not create a
// duplicate (S4L2-4).
func TestAddManualInterval_StableIdentity(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	m := ManualInterval{UUID: "fixed-uuid", StartAt: at(9, 0), EndAt: at(10, 0), Project: "P", Description: "first"}
	if err := AddManualInterval(&r, m); err != nil {
		t.Fatalf("AddManualInterval: %v", err)
	}
	if len(r.CollectedEvidence) != 1 {
		t.Fatalf("collected evidence = %d, want 1", len(r.CollectedEvidence))
	}
	id := r.CollectedEvidence[0].ID
	if want := evidence.ManualID("2026-10-06", "fixed-uuid"); id != want {
		t.Errorf("id = %q, want %q", id, want)
	}

	m.Description = "edited"
	if err := AddManualInterval(&r, m); err != nil {
		t.Fatalf("AddManualInterval edit: %v", err)
	}
	if len(r.CollectedEvidence) != 1 {
		t.Fatalf("editing the description created a duplicate: %d items", len(r.CollectedEvidence))
	}
	if r.CollectedEvidence[0].ID != id {
		t.Error("identity changed on description edit")
	}
	if r.CollectedEvidence[0].Description != "edited" {
		t.Errorf("edit not applied: %q", r.CollectedEvidence[0].Description)
	}
}

// TestConfirmMeetingsBulk_AttendedThenNotAttended proves flipping a meeting to
// not-attended supersedes its confirmed interval while retaining the evidence
// (S4L2-5).
func TestConfirmMeetingsBulk_AttendedThenNotAttended(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		meetingEvidence("m1", at(9, 0), at(10, 0), evidence.InvitationAccepted),
	}}
	if err := ConfirmMeetingsBulk(&r, []string{"m1"}, true); err != nil {
		t.Fatalf("ConfirmMeetingsBulk(true): %v", err)
	}
	if len(r.Confirmed) != 1 {
		t.Fatalf("confirmed = %d, want 1", len(r.Confirmed))
	}
	if err := ConfirmMeetingsBulk(&r, []string{"m1"}, false); err != nil {
		t.Fatalf("ConfirmMeetingsBulk(false): %v", err)
	}
	if len(r.Confirmed) != 0 {
		t.Errorf("not-attended must supersede the confirmed meeting: %+v", r.Confirmed)
	}
	if len(r.CollectedEvidence) != 1 {
		t.Error("evidence must be retained")
	}
	if r.Review == nil || len(r.Review.Attendance) != 1 || r.Review.Attendance[0].Attended {
		t.Errorf("attendance decision missing: %+v", r.Review)
	}
}

// TestCurrentProposal_HonorsExclusions proves review-excluded evidence is not
// re-proposed and is surfaced as excluded, not Changed (S4L2-8).
func TestCurrentProposal_HonorsExclusions(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		gitEvidence("g1", "acme/app", "sha1", at(9, 0)),
	}}
	r.Review = &workflow.ReviewState{ExcludedEvidence: []workflow.ExcludedEvidence{
		{EvidenceID: "g1", Reason: ReasonUserExcluded},
	}}

	prop, err := CurrentProposal(r, testProposalOpts())
	if err != nil {
		t.Fatalf("CurrentProposal: %v", err)
	}
	for _, b := range prop.Work {
		for _, id := range b.EvidenceIDs {
			if id == "g1" {
				t.Error("excluded evidence was re-proposed as a work block")
			}
		}
	}
	for _, topic := range prop.Topics {
		for _, id := range topic.EvidenceIDs {
			if id == "g1" {
				t.Error("excluded evidence was re-proposed as a topic")
			}
		}
	}
	found := false
	for _, ex := range prop.Excluded {
		if ex.EvidenceID == "g1" && string(ex.Reason) == ReasonUserExcluded {
			found = true
		}
	}
	if !found {
		t.Errorf("expected g1 surfaced as excluded, got %+v", prop.Excluded)
	}
	for _, id := range prop.Changed {
		if id == "g1" {
			t.Error("excluded evidence must not appear in Changed")
		}
	}
}
