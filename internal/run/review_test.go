package run

import (
	"reflect"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ApplyReview must produce the SAME record as the equivalent interactive review.
func TestApplyReview_ParityWithInteractive(t *testing.T) {
	date := "2026-10-06"
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	review.SetClock(func() time.Time { return clock })
	t.Cleanup(func() { review.SetClock(func() time.Time { return time.Now().UTC() }) })

	seed := func() workflow.Store {
		store := newStore(t)
		seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
		return store
	}

	storeA := seed()
	rA := newRunner(t, Deps{Store: storeA, Registry: evidence.NewRegistry()}, Config{})
	propA, err := rA.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(propA.Work) == 0 {
		t.Fatalf("want a work block, got %+v", propA)
	}
	blockID := propA.Work[0].ID

	if err := rA.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	gotA, _ := storeA.GetDay(ctx, date)

	// The interactive-equivalent path: the same review actions applied directly.
	storeB := seed()
	rB := newRunner(t, Deps{Store: storeB, Registry: evidence.NewRegistry()}, Config{})
	_, err = storeB.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		prop, pErr := review.CurrentProposal(*rec, rB.proposalOptions(date))
		if pErr != nil {
			return pErr
		}
		var block proposal.ProposedTimeBlock
		for _, b := range prop.Work {
			if b.ID == blockID {
				block = b
			}
		}
		if cErr := review.ConfirmProposed(rec, block, review.ConfirmOptions{Now: clock}); cErr != nil {
			return cErr
		}
		final, fErr := review.CurrentProposal(*rec, rB.proposalOptions(date))
		if fErr != nil {
			return fErr
		}
		return review.MarkReviewed(rec, review.ProposalHash(final))
	})
	if err != nil {
		t.Fatalf("interactive review: %v", err)
	}
	gotB, _ := storeB.GetDay(ctx, date)

	if !reflect.DeepEqual(gotA.Confirmed, gotB.Confirmed) {
		t.Fatalf("ApplyReview must match interactive review:\n got %+v\nwant %+v", gotA.Confirmed, gotB.Confirmed)
	}
	if !reflect.DeepEqual(gotA.Review, gotB.Review) {
		t.Fatalf("review state must match:\n got %+v\nwant %+v", gotA.Review, gotB.Review)
	}
}

// S8L1-1: a persisted AI suggestion must reach ConfirmedInterval.Description
// through the review path.
func TestApplyReview_AIDescriptionReachesConfirmedInterval(t *testing.T) {
	date := "2026-10-06"
	store := newStore(t)
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})

	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) == 0 {
		t.Fatalf("want a work block, got %+v", prop)
	}
	blockID := prop.Work[0].ID

	// A prior Synthesize persisted an AI suggestion for this target.
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		workflow.UpsertEditorialSuggestion(rec, workflow.EditorialSuggestion{
			TargetID: blockID, RequestHash: "h",
			Text:   workflow.EditorialText{Title: "AI title", Description: "AI description"},
			Origin: string(editorial.OriginEditorial),
		})
		return nil
	}); err != nil {
		t.Fatalf("persist suggestion: %v", err)
	}

	if err := r.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	got, _ := store.GetDay(ctx, date)
	if len(got.Confirmed) == 0 {
		t.Fatalf("want a confirmed interval, got %+v", got.Confirmed)
	}
	if got.Confirmed[0].Description != "AI description" {
		t.Fatalf("the AI description must reach the confirmed interval, got %q", got.Confirmed[0].Description)
	}
}

func TestApplyReview_MarkEmpty(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	if err := r.ApplyReview(ctx, date, ReviewDecisions{MarkEmpty: true}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	if rec.Review == nil || !rec.Review.Reviewed || !rec.Review.Empty {
		t.Fatalf("want a reviewed-empty record, got %+v", rec.Review)
	}
	if len(rec.Confirmed) != 0 {
		t.Fatalf("a reviewed-empty day must invent no confirmed block, got %+v", rec.Confirmed)
	}
}

// S8L1-7: parity across ALL decision kinds, not just Confirm.
func TestApplyReview_ParityForAllActions(t *testing.T) {
	date := "2026-10-06"
	clock := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	review.SetClock(func() time.Time { return clock })
	t.Cleanup(func() { review.SetClock(func() time.Time { return time.Now().UTC() }) })

	seed := func() workflow.Store {
		store := newStore(t)
		seedEvidence(t, store, date, []evidence.Evidence{
			gitEvidence("e1", "acme/repo", "fix parser"),
			gitEvidence("e2", "acme/other", "tidy up"),
			meetingEvidence("m1", "Standup"),
		})
		return store
	}

	storeA := seed()
	rA := newRunner(t, Deps{Store: storeA, Registry: evidence.NewRegistry()}, Config{})
	propA, err := rA.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(propA.Work) == 0 || len(propA.Meetings) == 0 {
		t.Fatalf("want work+meeting blocks, got %+v", propA)
	}
	workID := ""
	for _, b := range propA.Work {
		for _, id := range b.EvidenceIDs {
			if id == "e1" {
				workID = b.ID
			}
		}
	}
	if workID == "" {
		t.Fatalf("want a work block containing e1, got %+v", propA.Work)
	}
	meetID := propA.Meetings[0].ID

	d := ReviewDecisions{
		Confirm:     []ConfirmDecision{{BlockID: workID, Project: "Proj2"}, {BlockID: meetID}},
		Project:     []ProjectDecision{{BlockID: workID, Project: "Proj3"}},
		Trim:        []TrimDecision{{BlockID: workID, NewStart: "09:15", NewEnd: "09:45"}},
		Manual:      []ManualDecision{{UUID: "u1", Start: time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC), End: time.Date(2026, 10, 6, 14, 0, 0, 0, time.UTC), Project: "Proj", Description: "manual work"}},
		Acknowledge: []string{"cal"},
		Exclude:     []ExcludeDecision{{EvidenceID: "e2", Reason: "user_excluded"}},
	}
	if err := rA.ApplyReview(ctx, date, d); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	gotA, _ := storeA.GetDay(ctx, date)

	// The interactive-equivalent path: the same review actions, applied directly.
	storeB := seed()
	rB := newRunner(t, Deps{Store: storeB, Registry: evidence.NewRegistry()}, Config{})
	if _, err := storeB.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		return directDecisions(rec, rB, date, d)
	}); err != nil {
		t.Fatalf("interactive review: %v", err)
	}
	gotB, _ := storeB.GetDay(ctx, date)

	if !reflect.DeepEqual(gotA.Confirmed, gotB.Confirmed) {
		t.Fatalf("Confirmed mismatch:\n got %+v\nwant %+v", gotA.Confirmed, gotB.Confirmed)
	}
	if !reflect.DeepEqual(gotA.Review, gotB.Review) {
		t.Fatalf("Review mismatch:\n got %+v\nwant %+v", gotA.Review, gotB.Review)
	}
	if !reflect.DeepEqual(gotA.CollectedEvidence, gotB.CollectedEvidence) {
		t.Fatalf("CollectedEvidence mismatch:\n got %+v\nwant %+v", gotA.CollectedEvidence, gotB.CollectedEvidence)
	}
}

// directDecisions is the interactive-equivalent sequence of internal/review calls,
// including the same proposal-block → confirmed-interval resolution.
func directDecisions(rec *workflow.Record, r *Runner, date string, d ReviewDecisions) error {
	prop, err := review.CurrentProposal(*rec, r.proposalOptions(date))
	if err != nil {
		return err
	}
	blocks := proposalBlocksByID(prop)

	blockToConfirmed := make(map[string]string, len(d.Confirm))
	for _, c := range d.Confirm {
		before := confirmedIDSet(rec)
		if err := review.ConfirmProposed(rec, blocks[c.BlockID], review.ConfirmOptions{Project: c.Project, Attendance: c.Attendance, Now: r.now()}); err != nil {
			return err
		}
		for _, ci := range rec.Confirmed {
			if !before[ci.ID] {
				blockToConfirmed[c.BlockID] = ci.ID
			}
		}
	}
	resolve := func(id string) string {
		if _, ok := findConfirmedID(rec, id); ok {
			return id
		}
		if confirmedID, ok := blockToConfirmed[id]; ok {
			return confirmedID
		}
		return id
	}

	for _, ex := range d.Exclude {
		if err := review.ExcludeEvidence(rec, ex.EvidenceID, ex.Reason); err != nil {
			return err
		}
	}
	for _, j := range d.Join {
		if err := review.JoinCollision(rec, resolve(j.WorkBlockID), j.MeetingEvID); err != nil {
			return err
		}
	}
	for _, t := range d.Trim {
		if err := review.TrimWork(rec, resolve(t.BlockID), t.NewStart, t.NewEnd); err != nil {
			return err
		}
	}
	for _, p := range d.Project {
		if err := review.ResolveProject(rec, resolve(p.BlockID), p.Project); err != nil {
			return err
		}
	}
	for _, m := range d.Manual {
		if err := review.AddManualInterval(rec, review.ManualInterval{UUID: m.UUID, StartAt: m.Start, EndAt: m.End, Project: m.Project, Description: m.Description, Now: r.now()}); err != nil {
			return err
		}
	}
	if len(d.Acknowledge) > 0 {
		if err := review.AcknowledgeSources(rec, d.Acknowledge); err != nil {
			return err
		}
	}
	final, err := review.CurrentProposal(*rec, r.proposalOptions(date))
	if err != nil {
		return err
	}
	return review.MarkReviewed(rec, review.ProposalHash(final))
}

func TestApplyReview_JoinCollision(t *testing.T) {
	date := "2026-10-06"
	store := newStore(t)
	seedEvidence(t, store, date, []evidence.Evidence{
		gitEvidence("e1", "acme/repo", "fix parser"),
		meetingEvidence("m1", "Standup"),
	})
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work) == 0 {
		t.Fatalf("want a work block, got %+v", prop)
	}
	workID := prop.Work[0].ID

	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Confirm: []ConfirmDecision{{BlockID: workID}},
		Join:    []JoinDecision{{WorkBlockID: workID, MeetingEvID: "m1"}},
	}); err != nil {
		t.Fatalf("ApplyReview: %v", err)
	}
	got, _ := store.GetDay(ctx, date)
	found := false
	for _, ci := range got.Confirmed {
		if ci.Kind == string(proposal.BlockJoint) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a joint interval, got %+v", got.Confirmed)
	}
}
