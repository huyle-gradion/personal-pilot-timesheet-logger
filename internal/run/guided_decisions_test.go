package run

import (
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func testRunner(t *testing.T, store workflow.Store) *Runner {
	t.Helper()
	return newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
}

// --- authoritative classification at the shared decision seam ---------------

func TestAuthoritativeClassification_ResolvesAndDerives(t *testing.T) {
	cat := gradion.Catalog{Complete: true, Options: []gradion.ClassificationOption{
		{ID: "cls-1", Code: "INTGRADI2610", Name: "Intern Academy", ActivityType: "internal project"},
	}}

	// Exact id + matching declared type is accepted; the DERIVED type wins.
	id, at, err := AuthoritativeClassification(cat, "cls-1", "internal project")
	if err != nil || id != "cls-1" || at != "internal project" {
		t.Fatalf("want cls-1/internal project, got %q/%q err=%v", id, at, err)
	}
	// An empty declared type is accepted and the derived type is filled in.
	if id, at, err := AuthoritativeClassification(cat, "cls-1", ""); err != nil || at != "internal project" {
		t.Fatalf("an empty declared type must adopt the derived type, got %q/%q err=%v", id, at, err)
	}
	// A CODE resolves to the same option.
	if id, _, err := AuthoritativeClassification(cat, "INTGRADI2610", ""); err != nil || id != "cls-1" {
		t.Fatalf("a code must resolve to cls-1, got %q err=%v", id, err)
	}
}

func TestAuthoritativeClassification_RefusesInconsistentOrUnknown(t *testing.T) {
	cat := gradion.Catalog{Complete: true, Options: []gradion.ClassificationOption{
		{ID: "cls-1", Code: "INTGRADI2610", ActivityType: "internal project"},
	}}
	if _, _, err := AuthoritativeClassification(cat, "cls-1", "billable"); err == nil {
		t.Fatalf("a declared activity type that disagrees with the catalog must be refused")
	}
	if _, _, err := AuthoritativeClassification(cat, "cls-9", ""); err == nil {
		t.Fatalf("an unknown classification id must be refused")
	}
	if _, _, err := AuthoritativeClassification(cat, "", ""); err == nil {
		t.Fatalf("an empty classification id must be refused")
	}
}

// --- description + classification decisions ---------------------------------

func seedGitDay(t *testing.T, store workflow.Store, r *Runner, date string) string {
	t.Helper()
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) != 1 {
		t.Fatalf("want 1 work block, got %+v", prop.Work)
	}
	return prop.Work[0].ID
}

func TestApplyReview_DescriptionAndClassification(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)

	dec := ReviewDecisions{
		Confirm:        []ConfirmDecision{{BlockID: blockID, Project: "Proj"}},
		Trim:           []TrimDecision{{BlockID: blockID, NewStart: "09:00", NewEnd: "10:00"}},
		Description:    []DescriptionDecision{{BlockID: blockID, Text: "reviewed description"}},
		Classification: []ClassificationDecision{{BlockID: blockID, ClassificationID: "cls-1", ActivityType: "internal project"}},
	}
	if err := r.ApplyReview(ctx, date, dec); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed interval, got %+v", rec.Confirmed)
	}
	ci := rec.Confirmed[0]
	if ci.Description != "reviewed description" || ci.ClassificationID != "cls-1" || ci.ActivityType != "internal project" {
		t.Fatalf("want the description + classification applied, got %+v", ci)
	}
	if ci.StartTime != "09:00" || ci.EndTime != "10:00" || ci.Project != "Proj" {
		t.Fatalf("want the trimmed span + project, got %+v", ci)
	}
}

func TestApplyReview_ClassificationDecisionWithEmptyIDFails(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)
	err := r.ApplyReview(ctx, date, ReviewDecisions{
		Confirm:        []ConfirmDecision{{BlockID: blockID, Project: "Proj"}},
		Classification: []ClassificationDecision{{BlockID: blockID, ClassificationID: ""}},
	})
	if err == nil {
		t.Fatalf("an empty classification id must be refused at the decision seam")
	}
}

// --- split fragments: EVERY fragment is updated, not only the last ----------

func TestApplyReview_ManualConfirmUpdatesEverySplitFragment(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"

	start := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour) // 300 min -> two fragments (240 + 60)
	dec := ReviewDecisions{
		Manual: []ManualDecision{{
			UUID: "u-1", Start: start, End: end, Project: "Proj", Description: "long session",
			Confirm: true, ClassificationID: "cls-1", ActivityType: "internal project",
			ClassificationCode: "INTGRADI2610", ClassificationName: "Gradion Intern Academy 2026",
		}},
	}
	if err := r.ApplyReview(ctx, date, dec); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Confirmed) != 2 {
		t.Fatalf("want 2 split fragments, got %d: %+v", len(rec.Confirmed), rec.Confirmed)
	}
	total := 0
	for _, ci := range rec.Confirmed {
		if ci.ClassificationID != "cls-1" || ci.ActivityType != "internal project" {
			t.Fatalf("EVERY fragment must carry the classification, got %+v", ci)
		}
		if ci.ClassificationCode != "INTGRADI2610" || ci.ClassificationName != "Gradion Intern Academy 2026" {
			t.Fatalf("EVERY fragment must carry the retrieval metadata, got %+v", ci)
		}
		if len(ci.EvidenceIDs) == 0 {
			t.Fatalf("EVERY fragment must carry the manual evidence id, got %+v", ci)
		}
		total += hhmmMinutes(ci.StartTime, ci.EndTime)
	}
	if total != 300 {
		t.Fatalf("the exact total must be preserved across fragments, got %d", total)
	}
}

// --- approval invalidation --------------------------------------------------

func TestApplyReview_ClassificationInvalidatesApproval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)

	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Classification: []ClassificationDecision{{BlockID: "c1", ClassificationID: "cls-2", ActivityType: "internal project"}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); ok {
		t.Fatalf("a classification change must invalidate the prior approval")
	}
}

func TestApplyReview_DescriptionInvalidatesApproval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)

	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Description: []DescriptionDecision{{BlockID: "c1", Text: "edited text"}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); ok {
		t.Fatalf("a description change must invalidate the prior approval")
	}
}

// --- manual confirm is ADDITIVE: an old decision never auto-confirms ---------

func TestApplyReview_ManualWithoutConfirmDoesNotConfirm(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"

	start := time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC)
	// A legacy decision (no Confirm field) must add evidence WITHOUT confirming.
	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Manual: []ManualDecision{{UUID: "u-2", Start: start, End: start.Add(time.Hour), Project: "Proj", Description: "x"}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Confirmed) != 0 {
		t.Fatalf("a manual decision without Confirm must NOT confirm work, got %+v", rec.Confirmed)
	}
	found := false
	for _, e := range rec.CollectedEvidence {
		if e.Kind == evidence.SourceManual {
			found = true
		}
	}
	if !found {
		t.Fatalf("the manual evidence must still be recorded")
	}
}

// hhmmMinutes returns the exact minutes from "HH:MM" to "HH:MM" (end-before-start
// crosses midnight).
func hhmmMinutes(start, end string) int {
	s, err1 := time.Parse("15:04", start)
	e, err2 := time.Parse("15:04", end)
	if err1 != nil || err2 != nil {
		return 0
	}
	m := int(e.Sub(s).Minutes())
	if m < 0 {
		m += 24 * 60
	}
	return m
}

// commitAt builds a local-git commit evidence at a specific time.
func commitAt(id, repo, sha string, at time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceLocalGit, Title: "commit " + sha, Start: at,
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: repo, CommitSHA: sha, Branch: "feat/x", At: at}},
		ObservedAt: at,
	}
}

func meetingAt(id string, start, end time.Time) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceCalendar, Title: "Meeting", Start: start, End: end,
		Status: evidence.EventStatusConfirmed, Invitation: evidence.InvitationAccepted,
		Duration: evidence.KnownDuration(int(end.Sub(start).Minutes())), ObservedAt: start,
	}
}

// B4: Confirm+Trim+Project+Description+Classification+Join on the SAME block must
// succeed (the reorder), the joint must inherit every edit, and it must count once.
func TestApplyReview_CombinedEditThenJoin(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"

	base := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	seedEvidence(t, store, date, []evidence.Evidence{
		commitAt("e1", "acme/repo", "c1", base),
		commitAt("e2", "acme/repo", "c2", base.Add(15*time.Minute)),
		meetingAt("m1", base.Add(30*time.Minute), base.Add(90*time.Minute)),
	})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) != 1 || len(prop.Meetings) != 1 {
		t.Fatalf("want 1 work + 1 meeting block, got %+v", prop)
	}
	workID := prop.Work[0].ID

	dec := ReviewDecisions{
		Confirm:        []ConfirmDecision{{BlockID: workID, Project: "Proj3"}},
		Trim:           []TrimDecision{{BlockID: workID, NewStart: "09:15", NewEnd: "10:15"}},
		Project:        []ProjectDecision{{BlockID: workID, Project: "Proj3"}},
		Description:    []DescriptionDecision{{BlockID: workID, Text: "reviewed"}},
		Classification: []ClassificationDecision{{BlockID: workID, ClassificationID: "cls-1", ActivityType: "internal project"}},
		Join:           []JoinDecision{{WorkBlockID: workID, MeetingEvID: "m1"}},
	}
	if err := r.ApplyReview(ctx, date, dec); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("a combined edit+join must count ONCE, got %d: %+v", len(rec.Confirmed), rec.Confirmed)
	}
	ci := rec.Confirmed[0]
	if ci.Kind != "joint" {
		t.Fatalf("want a joint interval, got %+v", ci)
	}
	if ci.Project != "Proj3" || ci.ClassificationID != "cls-1" {
		t.Fatalf("the joint must inherit the project + classification, got %+v", ci)
	}
	if ci.StartTime != "09:15" || ci.EndTime != "10:30" {
		t.Fatalf("want the trimmed union span 09:15-10:30, got %s-%s", ci.StartTime, ci.EndTime)
	}
	if !containsString(ci.EvidenceIDs, "e1") || !containsString(ci.EvidenceIDs, "e2") || !containsString(ci.EvidenceIDs, "m1") {
		t.Fatalf("the joint must carry all evidence ids, got %v", ci.EvidenceIDs)
	}
}

// B5: eager invalidation for every schedule-affecting action.
func TestApplyReview_TrimInvalidatesApproval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Trim: []TrimDecision{{BlockID: "c1", NewStart: "09:15", NewEnd: "10:15"}}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); ok {
		t.Fatalf("a trim must invalidate the prior approval")
	}
}

func TestApplyReview_ProjectInvalidatesApproval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Project: []ProjectDecision{{BlockID: "c1", Project: "Other"}}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); ok {
		t.Fatalf("a project change must invalidate the prior approval")
	}
}

func TestApplyReview_JoinInvalidatesApproval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.CollectedEvidence = append(rec.CollectedEvidence, meetingAt("m1",
			time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC), time.Date(2026, 10, 6, 10, 30, 0, 0, time.UTC)))
		return nil
	}); err != nil {
		t.Fatalf("seed meeting: %v", err)
	}
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Join: []JoinDecision{{WorkBlockID: "c1", MeetingEvID: "m1"}}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); ok {
		t.Fatalf("a join must invalidate the prior approval")
	}
}

// Acknowledging alone does not change the schedule, so it must NOT eagerly
// invalidate an approval.
func TestApplyReview_AcknowledgeOnlyKeepsApprovalActive(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedApproved(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Acknowledge: []string{"cal"}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if _, ok := workflow.ActiveApproval(rec, date); !ok {
		t.Fatalf("an acknowledge-only review must not invalidate the approval")
	}
}
