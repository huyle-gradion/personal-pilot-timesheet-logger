package editorial

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func emptyListing() gradion.Listing {
	return gradion.Listing{Observed: true, Complete: true, ObservedAt: testNow}
}

func clsResolver(string) (string, bool) { return "cls-1", true }

// reviewedRecord is a ready-to-submit day with one confirmed interval.
func reviewedRecord() workflow.Record {
	return workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          testDate,
		Revision:      1,
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "old", Attendance: true, ClassificationID: "cls-1",
		}},
		Review: &workflow.ReviewState{Reviewed: true},
	}
}

// An editorial TEXT change flows into ConfirmedInterval.Description and invalidates
// an S6 approval via LocalFingerprint; an editorial rerun ALONE does not.
func TestEditorialTextChangeInvalidatesApproval(t *testing.T) {
	rec := reviewedRecord()
	built, err := submit.BuildPlan(rec, emptyListing(), nil, clsResolver, testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if built.Blocked {
		t.Fatalf("unexpected block: %v", built.Reasons)
	}
	workflow.UpsertPlan(&rec, built.Plan)
	approval, err := submit.Approve(&rec, built.Plan, testNow)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if status, _ := submit.ValidateApproval(rec, approval, emptyListing(), testNow); status != submit.ApprovalValid {
		t.Fatalf("want a valid approval, got %s", status)
	}

	// An editorial rerun ALONE (suggestions + usage) must NOT invalidate.
	rerun := rec
	workflow.UpsertEditorialSuggestion(&rerun, workflow.EditorialSuggestion{
		TargetID: "c1", RequestHash: "h",
		Text: workflow.EditorialText{Title: "t", Description: "editorial text"}, Origin: string(OriginEditorial),
	})
	workflow.SetUsageReport(&rerun, workflow.EditorialUsage{Date: testDate, Editorial: workflow.KnownMeasurement(42), Invoked: true})
	if status, _ := submit.ValidateApproval(rerun, approval, emptyListing(), testNow); status != submit.ApprovalValid {
		t.Fatalf("an editorial rerun alone must NOT invalidate the approval, got %s", status)
	}

	// Applying the text to the confirmed interval DOES invalidate (stale_local).
	applied := rec
	applied.Confirmed[0].Description = "editorial text"
	status, reasons := submit.ValidateApproval(applied, approval, emptyListing(), testNow)
	if status != submit.ApprovalStaleLocal {
		t.Fatalf("a text change must invalidate the approval (stale_local), got %s (%v)", status, reasons)
	}
}

// Apply's output flows through the existing review path into
// ConfirmedInterval.Description (no new S6 code).
func TestApplyFlowsThroughReviewPath(t *testing.T) {
	block := proposal.ProposedTimeBlock{
		ID: "w1", Kind: proposal.BlockWork, Date: testDate, Start: "09:00", End: "10:00",
		Project: proposal.ProjectResolution{Project: "Proj"},
	}
	prop := proposal.Proposal{Date: testDate, Work: []proposal.ProposedTimeBlock{block}}
	applied := Apply(prop, []Suggestion{{TargetID: "w1", Text: Text{Title: "t", Description: "editorial text"}}})

	rec := &workflow.Record{SchemaVersion: workflow.SchemaVersion, Date: testDate}
	if err := review.ConfirmProposed(rec, applied.Work[0], review.ConfirmOptions{Now: testNow}); err != nil {
		t.Fatalf("ConfirmProposed: %v", err)
	}
	found := false
	for _, ci := range rec.Confirmed {
		if ci.Description == "editorial text" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the editorial text must land on the confirmed interval, got %+v", rec.Confirmed)
	}
}
