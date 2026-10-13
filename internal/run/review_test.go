package run

import (
	"reflect"
	"strings"
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

// TestApplyReview_MarkEmptyRefusedWithStagedWork proves a confirm+mark_empty
// decision set fails closed: the day stays non-empty and nothing is persisted.
func TestApplyReview_MarkEmptyRefusedWithStagedWork(t *testing.T) {
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
	err = r.ApplyReview(ctx, date, ReviewDecisions{
		Confirm:   []ConfirmDecision{{BlockID: prop.Work[0].ID}},
		MarkEmpty: true,
	})
	if err == nil {
		t.Fatalf("a confirm+mark_empty decision set must fail closed")
	}
	got, _ := store.GetDay(ctx, date)
	if len(got.Confirmed) != 0 {
		t.Fatalf("nothing may be persisted on the refused apply, got %+v", got.Confirmed)
	}
	if got.Review != nil && got.Review.Empty {
		t.Fatalf("the day must not be marked empty")
	}
}

// TestApplyReview_MarkEmptyRefusedWithExistingConfirmed proves a day that already
// carries confirmed work can never be marked empty.
func TestApplyReview_MarkEmptyRefusedWithExistingConfirmed(t *testing.T) {
	date := "2026-10-06"
	store := newStore(t)
	seedEvidence(t, store, date, nil)
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Confirmed = []workflow.ConfirmedInterval{{
			ID: "c1", Date: date, StartTime: "09:00", EndTime: "10:00", Project: "P",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	if err := r.ApplyReview(ctx, date, ReviewDecisions{MarkEmpty: true}); err == nil {
		t.Fatalf("marking a nonempty day empty must fail closed")
	}
	got, _ := store.GetDay(ctx, date)
	if len(got.Confirmed) != 1 {
		t.Fatalf("confirmed work must be preserved, got %+v", got.Confirmed)
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
// mirroring production applyDecisions: Trim/Project/Description/Classification are
// applied BEFORE Join (so a joined interval inherits them), every decision reaches
// EVERY fragment of a split block, and a manual decision with Confirm also
// confirms its materialized block(s).
func directDecisions(rec *workflow.Record, r *Runner, date string, d ReviewDecisions) error {
	prop, err := review.CurrentProposal(*rec, r.proposalOptions(date))
	if err != nil {
		return err
	}
	blocks := proposalBlocksByID(prop)

	blockToConfirmed := make(map[string][]string, len(d.Confirm))
	for _, c := range d.Confirm {
		ids, err := confirmProposedBlock(rec, blocks[c.BlockID], review.ConfirmOptions{Project: c.Project, Attendance: c.Attendance, Now: r.now()})
		if err != nil {
			return err
		}
		blockToConfirmed[c.BlockID] = ids
	}
	resolveAll := func(id string) []string {
		if _, ok := findConfirmedID(rec, id); ok {
			return []string{id}
		}
		if ids, ok := blockToConfirmed[id]; ok && len(ids) > 0 {
			return ids
		}
		return []string{id}
	}
	resolve := func(id string) string {
		ids := resolveAll(id)
		return ids[len(ids)-1]
	}

	for _, ex := range d.Exclude {
		if err := review.ExcludeEvidence(rec, ex.EvidenceID, ex.Reason); err != nil {
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
	for _, ds := range d.Description {
		for _, id := range resolveAll(ds.BlockID) {
			if err := review.SetDescription(rec, id, ds.Text); err != nil {
				return err
			}
		}
	}
	for _, cs := range d.Classification {
		for _, id := range resolveAll(cs.BlockID) {
			if err := review.SetClassification(rec, id, cs.ClassificationID, cs.ActivityType); err != nil {
				return err
			}
		}
	}
	for _, j := range d.Join {
		if err := review.JoinCollision(rec, resolve(j.WorkBlockID), j.MeetingEvID); err != nil {
			return err
		}
	}
	for _, m := range d.Manual {
		if err := review.AddManualInterval(rec, review.ManualInterval{
			UUID: m.UUID, StartAt: m.Start, EndAt: m.End, Project: m.Project,
			Description: m.Description, EvidenceIDs: m.EvidenceIDs, Now: r.now(),
		}); err != nil {
			return err
		}
	}
	if err := r.confirmManualDecisions(rec, date, d.Manual); err != nil {
		return err
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

// TestProposeAndConfirm_CarrySurfacesUnconfirmed proves a persisted carry from
// the previous day is surfaced on D+1 as an UNCONFIRMED block, is persisted ONLY
// by an explicit Confirm, and keeps the shared split linkage.
func TestProposeAndConfirm_CarrySurfacesUnconfirmed(t *testing.T) {
	d := "2026-10-06"
	next := "2026-10-07"
	store := newStore(t)
	seedEvidence(t, store, d, nil)
	if _, err := store.UpdateDay(ctx, d, func(rec *workflow.Record) error {
		rec.Carries = []workflow.CarryItem{{
			SourceDate: d, Date: next, Start: "00:00", End: "00:30",
			StartAt:      time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
			EndAt:        time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC),
			Minutes:      30,
			SplitGroupID: "split_x", PartIndex: 1,
			Kind: string(proposal.BlockManual), Project: "Proj", Description: "late work",
			EvidenceIDs: []string{"man1"},
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed carry: %v", err)
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})

	prop, err := r.Propose(ctx, next)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	var block proposal.ProposedTimeBlock
	found := false
	for _, b := range prop.Manual {
		if b.SplitGroupID == "split_x" {
			block = b
			found = true
		}
	}
	if !found {
		t.Fatalf("the D+1 proposal must surface the carry as an unconfirmed block, got %+v", prop.Manual)
	}
	if !block.Carryover || block.Preselected || block.PartIndex != 1 || block.Start != "00:00" || block.End != "00:30" {
		t.Fatalf("carry block wrong: %+v", block)
	}

	// Proposing writes nothing: no confirmed interval, no approval, until the
	// explicit confirmation below.
	if recNext, err := store.GetDay(ctx, next); err == nil {
		if len(recNext.Confirmed) != 0 || len(recNext.Approvals) != 0 {
			t.Fatalf("proposing must not confirm or approve: %+v", recNext)
		}
	}

	// A review with NO confirm decision leaves the carry unconfirmed.
	if err := r.ApplyReview(ctx, next, ReviewDecisions{}); err != nil {
		t.Fatalf("ApplyReview(empty): %v", err)
	}
	afterEmpty, _ := store.GetDay(ctx, next)
	if len(afterEmpty.Confirmed) != 0 {
		t.Fatalf("an empty review must not auto-confirm the carry: %+v", afterEmpty.Confirmed)
	}

	// Only an explicit Confirm persists the interval, keeping the linkage.
	if err := r.ApplyReview(ctx, next, ReviewDecisions{
		Confirm: []ConfirmDecision{{BlockID: block.ID, Project: "Proj"}},
	}); err != nil {
		t.Fatalf("ApplyReview(confirm): %v", err)
	}
	got, _ := store.GetDay(ctx, next)
	if len(got.Confirmed) != 1 {
		t.Fatalf("explicit confirm must persist exactly one interval, got %+v", got.Confirmed)
	}
	ci := got.Confirmed[0]
	if ci.SplitGroupID != "split_x" || ci.PartIndex != 1 || !ci.Carryover || ci.Date != next {
		t.Fatalf("the confirmed carry lost its linkage: %+v", ci)
	}
	if len(got.Approvals) != 0 {
		t.Fatalf("the carry path must create NO approval, got %+v", got.Approvals)
	}
}

// TestPropose_SurfacesMultiMidnightCarry proves F1: a carry that targets a day
// more than one day after its originating day is still surfaced, even when an
// intermediate day exists with no carries.
func TestPropose_SurfacesMultiMidnightCarry(t *testing.T) {
	d0, d1, d2 := "2026-10-06", "2026-10-07", "2026-10-08"
	store := newStore(t)
	seedEvidence(t, store, d0, nil)
	seedEvidence(t, store, d1, nil) // an intermediate day with NO carries
	if _, err := store.UpdateDay(ctx, d0, func(rec *workflow.Record) error {
		rec.Carries = []workflow.CarryItem{
			{
				SourceDate: d0, Date: d1, Start: "00:00", End: "00:00",
				StartAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
				EndAt:   time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
				Minutes: 1440, SplitGroupID: "split_multi", PartIndex: 1, Kind: string(proposal.BlockManual),
			},
			{
				SourceDate: d0, Date: d2, Start: "00:00", End: "02:00",
				StartAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
				EndAt:   time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC),
				Minutes: 120, SplitGroupID: "split_multi", PartIndex: 2, Kind: string(proposal.BlockManual),
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed carries: %v", err)
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})

	prop, err := r.Propose(ctx, d2)
	if err != nil {
		t.Fatalf("Propose(D+2): %v", err)
	}
	found := false
	for _, b := range prop.Manual {
		if b.SplitGroupID == "split_multi" && b.PartIndex == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("D+2 must surface the carry from D0, got %+v", prop.Manual)
	}
	if rec, err := store.GetDay(ctx, d2); err == nil && (len(rec.Confirmed) != 0 || len(rec.Approvals) != 0) {
		t.Fatalf("proposing D+2 must not confirm or approve: %+v", rec)
	}
}

// TestMergeCarryBlocks_Dedup proves a carry whose block id already exists in the
// proposal is not added twice.
func TestMergeCarryBlocks_Dedup(t *testing.T) {
	item := workflow.CarryItem{
		SourceDate: "2026-10-06", Date: "2026-10-07", Start: "00:00", End: "00:30",
		StartAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
		EndAt:   time.Date(2026, 10, 7, 0, 30, 0, 0, time.UTC),
		Minutes: 30, SplitGroupID: "split_x", PartIndex: 1, Kind: string(proposal.BlockManual),
	}
	block := review.SurfaceCarry(item)
	prop := proposal.Proposal{Date: "2026-10-07", Manual: []proposal.ProposedTimeBlock{block}}
	got := mergeCarryBlocks(prop, []workflow.CarryItem{item})
	if len(got.Manual) != 1 {
		t.Fatalf("a pre-existing block with the same id must not be duplicated: %+v", got.Manual)
	}
}

// --- REVIEW-UNDO-1: unconfirm + reset ---------------------------------------

// TestApplyReview_UnconfirmUnsubmittedReturnsToProposal proves an unconfirm of an
// unsubmitted interval removes it, keeps every collected evidence item, and lets
// the block re-surface as a fresh proposal.
func TestApplyReview_UnconfirmUnsubmittedReturnsToProposal(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)

	if err := r.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview(confirm): %v", err)
	}
	confirmed, _ := store.GetDay(ctx, date)
	if len(confirmed.Confirmed) != 1 {
		t.Fatalf("want 1 confirmed interval, got %+v", confirmed.Confirmed)
	}
	ci := confirmed.Confirmed[0]
	collectedBefore := len(confirmed.CollectedEvidence)

	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Unconfirm: []UnconfirmDecision{{BlockID: ci.ID, Reason: "user_retracted"}},
	}); err != nil {
		t.Fatalf("ApplyReview(unconfirm): %v", err)
	}
	after, _ := store.GetDay(ctx, date)
	if len(after.Confirmed) != 0 {
		t.Fatalf("unconfirm must remove the interval, got %+v", after.Confirmed)
	}
	if len(after.CollectedEvidence) != collectedBefore {
		t.Fatalf("unconfirm must not drop collected evidence: before=%d after=%d", collectedBefore, len(after.CollectedEvidence))
	}
	prop, err := r.CurrentProposal(ctx, date)
	if err != nil {
		t.Fatalf("CurrentProposal: %v", err)
	}
	found := false
	for _, b := range prop.Work {
		if b.ID == blockID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unconfirmed block must re-surface as a proposal, got %+v", prop.Work)
	}
}

// TestApplyReview_UnconfirmSubmittedBlocksWithSpecificReason proves a submitted
// interval cannot be unconfirmed, and the refusal names the reason, the id, and
// the correct action (edit the remote entry).
func TestApplyReview_UnconfirmSubmittedBlocksWithSpecificReason(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview(confirm): %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	ci := rec.Confirmed[0]
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error {
		x.RemoteMappings = append(x.RemoteMappings, workflow.RemoteMapping{LocalID: ci.ID, RemoteID: "remote-1", Date: date})
		return nil
	}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	err := r.ApplyReview(ctx, date, ReviewDecisions{Unconfirm: []UnconfirmDecision{{BlockID: ci.ID}}})
	if err == nil {
		t.Fatalf("unconfirming a submitted interval must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unconfirm_mapped_remote") || !strings.Contains(msg, ci.ID) {
		t.Fatalf("the error must name the reason and the offending id: %v", err)
	}
	if !strings.Contains(msg, "edit") {
		t.Fatalf("the error must point the operator at the edit path: %v", err)
	}
	after, _ := store.GetDay(ctx, date)
	if len(after.Confirmed) != 1 || after.Confirmed[0].ID != ci.ID {
		t.Fatalf("nothing may be written on the refused unconfirm, got %+v", after.Confirmed)
	}
	if len(after.RemoteMappings) != 1 || after.RemoteMappings[0].LocalID != ci.ID {
		t.Fatalf("the mapping must be intact, got %+v", after.RemoteMappings)
	}
}

// TestApplyReview_ResetClearsReviewKeepsHistory proves a reset clears the whole
// local review decision set while retaining collected evidence, mappings,
// outcomes, and plans, and invalidating (never deleting) an approval. A reset
// that stages no replacement work leaves the day UNREVIEWED.
func TestApplyReview_ResetClearsReviewKeepsHistory(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{
		gitEvidence("e1", "acme/repo", "fix parser"),
		gitEvidence("e2", "acme/other", "tidy up"),
	})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	var blockID string
	for _, b := range prop.Work {
		for _, id := range b.EvidenceIDs {
			if id == "e1" {
				blockID = b.ID
			}
		}
	}
	if blockID == "" {
		t.Fatalf("want a work block with e1, got %+v", prop.Work)
	}
	if err := r.ApplyReview(ctx, date, ReviewDecisions{
		Confirm:     []ConfirmDecision{{BlockID: blockID}},
		Exclude:     []ExcludeDecision{{EvidenceID: "e2", Reason: "user_excluded"}},
		Acknowledge: []string{"src-a"},
	}); err != nil {
		t.Fatalf("ApplyReview(seed decisions): %v", err)
	}
	// Seed history that must SURVIVE the reset: a plan, a terminal outcome, an
	// approval, an attendance decision, and a stale (unmatched) remote mapping.
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		if rec.Review == nil {
			rec.Review = &workflow.ReviewState{}
		}
		rec.Review.Attendance = []workflow.AttendanceDecision{{EvidenceID: "m1", Attended: true}}
		rec.Plans = []workflow.SubmissionPlan{{PlanID: "p1", Date: date}}
		rec.Outcomes = []workflow.OperationOutcome{{OperationKey: "k1", Status: workflow.OutcomeConfirmed, Date: date}}
		rec.Approvals = []workflow.Approval{{ApprovalID: "a1", PlanID: "p1", Date: date}}
		rec.RemoteMappings = append(rec.RemoteMappings, workflow.RemoteMapping{LocalID: "stale-x", RemoteID: "r1", Date: date})
		return nil
	}); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	before, _ := store.GetDay(ctx, date)
	if before.Review == nil || len(before.Review.ExcludedEvidence) == 0 {
		t.Fatalf("want exclusions seeded, got %+v", before.Review)
	}

	if err := r.ApplyReview(ctx, date, ReviewDecisions{Reset: true}); err != nil {
		t.Fatalf("ApplyReview(reset): %v", err)
	}
	after, _ := store.GetDay(ctx, date)
	if len(after.Confirmed) != 0 {
		t.Fatalf("reset must clear confirmed work, got %+v", after.Confirmed)
	}
	if after.Review == nil {
		t.Fatalf("reset must leave a (cleared) review state, got nil")
	}
	if after.Review.Reviewed {
		t.Fatalf("reset must clear Reviewed")
	}
	if after.Review.Empty {
		t.Fatalf("reset must clear Empty")
	}
	if !after.Review.ReviewedAt.IsZero() {
		t.Fatalf("reset must zero ReviewedAt, got %v", after.Review.ReviewedAt)
	}
	if after.Review.LastProposalHash != "" {
		t.Fatalf("reset must clear LastProposalHash, got %q", after.Review.LastProposalHash)
	}
	if len(after.Review.ExcludedEvidence) != 0 {
		t.Fatalf("reset must clear exclusions, got %+v", after.Review.ExcludedEvidence)
	}
	if len(after.Review.Attendance) != 0 {
		t.Fatalf("reset must clear attendance, got %+v", after.Review.Attendance)
	}
	if len(after.Review.AcknowledgedSources) != 0 {
		t.Fatalf("reset must clear acknowledgments, got %+v", after.Review.AcknowledgedSources)
	}
	if len(after.CollectedEvidence) != len(before.CollectedEvidence) {
		t.Fatalf("reset must retain collected evidence: before=%d after=%d", len(before.CollectedEvidence), len(after.CollectedEvidence))
	}
	if len(after.RemoteMappings) != 1 {
		t.Fatalf("reset must retain remote mappings, got %+v", after.RemoteMappings)
	}
	if len(after.Outcomes) != 1 {
		t.Fatalf("reset must retain outcomes, got %+v", after.Outcomes)
	}
	if len(after.Plans) != 1 {
		t.Fatalf("reset must retain plans, got %+v", after.Plans)
	}
	if len(after.Approvals) != 1 || after.Approvals[0].InvalidatedAt == nil {
		t.Fatalf("reset must retain but INVALIDATE the approval, got %+v", after.Approvals)
	}
}

// TestApplyReview_ResetBlocksOnSubmittedInterval proves a reset is refused when a
// confirmed interval has a remote mapping, naming the reason and the id, with
// nothing written.
func TestApplyReview_ResetBlocksOnSubmittedInterval(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview(confirm): %v", err)
	}
	rec, _ := store.GetDay(ctx, date)
	ci := rec.Confirmed[0]
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error {
		x.RemoteMappings = append(x.RemoteMappings, workflow.RemoteMapping{LocalID: ci.ID, RemoteID: "remote-1", Date: date})
		return nil
	}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	err := r.ApplyReview(ctx, date, ReviewDecisions{Reset: true})
	if err == nil {
		t.Fatalf("resetting a day with a submitted interval must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "reset_submitted_interval") || !strings.Contains(msg, ci.ID) {
		t.Fatalf("the error must name the reason and the offending id: %v", err)
	}
	after, _ := store.GetDay(ctx, date)
	if len(after.Confirmed) != 1 || len(after.RemoteMappings) != 1 {
		t.Fatalf("nothing may be written on the refused reset, got confirmed=%+v mappings=%+v", after.Confirmed, after.RemoteMappings)
	}
}

// TestApplyReview_ResetBlocksOnUnresolvedOutcome proves a reset is refused when an
// unresolved (in_flight) remote operation remains, naming the reason, with nothing
// written.
func TestApplyReview_ResetBlocksOnUnresolvedOutcome(t *testing.T) {
	store := newStore(t)
	r := testRunner(t, store)
	date := "2026-10-06"
	blockID := seedGitDay(t, store, r, date)
	if err := r.ApplyReview(ctx, date, ReviewDecisions{Confirm: []ConfirmDecision{{BlockID: blockID}}}); err != nil {
		t.Fatalf("ApplyReview(confirm): %v", err)
	}
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error {
		x.Outcomes = append(x.Outcomes, workflow.OperationOutcome{OperationKey: "op-1", Status: workflow.OutcomeInFlight, Date: date})
		return nil
	}); err != nil {
		t.Fatalf("seed outcome: %v", err)
	}
	err := r.ApplyReview(ctx, date, ReviewDecisions{Reset: true})
	if err == nil {
		t.Fatalf("resetting a day with an unresolved operation must fail")
	}
	if !strings.Contains(err.Error(), "reset_unresolved_submission") {
		t.Fatalf("the error must name the unresolved-submission reason: %v", err)
	}
	after, _ := store.GetDay(ctx, date)
	if len(after.Confirmed) != 1 {
		t.Fatalf("nothing may be written on the refused reset, got %+v", after.Confirmed)
	}
}
