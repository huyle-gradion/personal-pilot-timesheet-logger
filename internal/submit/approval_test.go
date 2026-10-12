package submit

import (
	"errors"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// planAndApprove builds a plan, persists it on rec, and records its approval.
func planAndApprove(t *testing.T, rec *workflow.Record, obs gradion.Listing) workflow.Approval {
	t.Helper()
	res, err := BuildPlan(*rec, obs, nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	workflow.UpsertPlan(rec, res.Plan)
	a, err := Approve(rec, res.Plan, testNow)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return a
}

func TestApprove_RefusesPlanHashMismatch(t *testing.T) {
	rec := reviewedRecord()
	plan := workflow.SubmissionPlan{PlanID: "not-the-hash", Date: testDate}
	if _, err := Approve(&rec, plan, testNow); !errors.Is(err, ErrPlanHashMismatch) {
		t.Fatalf("want ErrPlanHashMismatch, got %v", err)
	}
}

func TestApprove_SupersedesPriorApprovalForDate(t *testing.T) {
	rec := reviewedRecord()
	first := planAndApprove(t, &rec, completeListing())

	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}
	second := planAndApprove(t, &rec, completeListing(observed))

	if second.ApprovalID == first.ApprovalID {
		t.Fatalf("distinct plans must produce distinct approval ids")
	}
	if a, ok := workflow.ActiveApproval(&rec, testDate); !ok || a.ApprovalID != second.ApprovalID {
		t.Fatalf("want the second approval active, got %+v (ok=%v)", a, ok)
	}
	superseded := false
	for _, ap := range rec.Approvals {
		if ap.ApprovalID == first.ApprovalID && ap.Superseded {
			superseded = true
		}
	}
	if !superseded {
		t.Errorf("the prior approval for the date must be superseded")
	}
}

func TestValidateApproval_Valid(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	status, reasons := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalValid {
		t.Fatalf("want valid, got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_Missing(t *testing.T) {
	rec := reviewedRecord()
	status, _ := ValidateApproval(rec, workflow.Approval{}, completeListing(), testNow)
	if status != ApprovalMissing {
		t.Fatalf("want missing, got %q", status)
	}
}

func TestValidateApproval_EditedTextInvalidates(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	rec.Confirmed[0].Description = "edited text"
	status, reasons := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStaleLocal {
		t.Fatalf("an edited description must invalidate the approval (stale_local), got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_LocalFingerprintChangeInvalidates(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	rec.Confirmed[0].ClassificationID = "other-cls"
	status, _ := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStaleLocal {
		t.Fatalf("a changed classification override must invalidate (stale_local), got %q", status)
	}
}

// A classification SELECTION change (which also updates the derived ActivityType)
// invalidates the approval: both fields feed LocalFingerprint.
func TestValidateApproval_ActivityTypeChangeInvalidates(t *testing.T) {
	rec := reviewedRecord()
	rec.Confirmed[0].ActivityType = "internal project"
	a := planAndApprove(t, &rec, completeListing())
	rec.Confirmed[0].ActivityType = "client project"
	status, _ := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStaleLocal {
		t.Fatalf("a changed activity type must invalidate (stale_local), got %q", status)
	}
}

func TestValidateApproval_RemoteContentChangeIsStaleRemote(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}
	a := planAndApprove(t, &rec, completeListing(observed))

	changed := remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")
	status, reasons := ValidateApproval(rec, a, completeListing(changed), testNow)
	if status != ApprovalStaleRemote {
		t.Fatalf("a remote content change must be stale_remote, got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_PlanMissingIsStalePlan(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	rec.Plans = nil
	status, reasons := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStalePlan {
		t.Fatalf("a missing plan must be stale_plan, got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_PlanMutationIsStalePlan(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	rec.Plans[0].Operations = append(rec.Plans[0].Operations, workflow.PlanOperation{LocalID: "injected", Kind: workflow.PlanOpCreate})
	status, _ := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStalePlan {
		t.Fatalf("a mutated plan must be stale_plan, got %q", status)
	}
}

func TestValidateApproval_CreateRemoteAppearsIsStaleRemote(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing()) // a create plan
	appeared := remoteState("rNew", "09:00", "10:00", "cls-1", "work")
	status, reasons := ValidateApproval(rec, a, completeListing(appeared), testNow)
	if status != ApprovalStaleRemote {
		t.Fatalf("a newly-appeared overlapping remote must be stale_remote, got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_CreateRequiresCompleteListing(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	incomplete := completeListing()
	incomplete.Complete = false
	status, reasons := ValidateApproval(rec, a, incomplete, testNow)
	if status != ApprovalStaleRemote {
		t.Fatalf("an incomplete fresh listing must be stale_remote for a create, got %q (%v)", status, reasons)
	}
}

func TestValidateApproval_Superseded(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	a.Superseded = true
	if status, _ := ValidateApproval(rec, a, completeListing(), testNow); status != ApprovalSuperseded {
		t.Fatalf("want superseded, got %q", status)
	}
}

func TestValidateApproval_Invalidated(t *testing.T) {
	rec := reviewedRecord()
	a := planAndApprove(t, &rec, completeListing())
	now := testNow
	a.InvalidatedAt = &now
	a.InvalidatedReason = "remote_changed"
	if status, reasons := ValidateApproval(rec, a, completeListing(), testNow); status != ApprovalInvalidated {
		t.Fatalf("want invalidated, got %q (%v)", status, reasons)
	}
}

func TestRemoteContentEqual_IgnoresObservedAt(t *testing.T) {
	a := workflow.RemoteState{
		RemoteID: "r1", StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work", ObservedAt: testNow,
		ContentObserved: true,
	}
	b := a
	b.ObservedAt = testNow.Add(time.Hour)
	b.RemoteID = "different-id"
	if !RemoteContentEqual(a, b) {
		t.Error("ObservedAt and RemoteID must be ignored by RemoteContentEqual")
	}

	c := a
	c.Description = "changed"
	if RemoteContentEqual(a, c) {
		t.Error("a description change must be detected")
	}

	d := a
	d.Billable = boolp(false)
	if RemoteContentEqual(a, d) {
		t.Error("a billable change (nil -> false) must be detected")
	}

	// A non-observation-derived operand (a plan echo / identity-only state) must
	// never compare equal: content is not verified.
	echo := a
	echo.ContentObserved = false
	if RemoteContentEqual(a, echo) || RemoteContentEqual(echo, a) {
		t.Error("an unobserved operand must fail closed, never compare equal")
	}
}

// F4: Date is content (a remote that moved date is a change); RemoteID stays
// ignored as the identity key.
func TestRemoteContentEqual_ComparesDate(t *testing.T) {
	a := workflow.RemoteState{
		RemoteID: "r1", Date: testDate, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work",
		ContentObserved: true,
	}
	moved := a
	moved.Date = "2026-10-07"
	if RemoteContentEqual(a, moved) {
		t.Error("a date change must be treated as content")
	}

	renamed := a
	renamed.RemoteID = "other-id"
	if !RemoteContentEqual(a, renamed) {
		t.Error("RemoteID must remain ignored (it is the identity key)")
	}
}

// T2: a missing edit remote makes the approval stale_remote.
func TestValidateApproval_EditRemoteMissingIsStaleRemote(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}
	a := planAndApprove(t, &rec, completeListing(observed))

	status, reasons := ValidateApproval(rec, a, completeListing(), testNow)
	if status != ApprovalStaleRemote {
		t.Fatalf("a missing edit remote must be stale_remote, got %q (%v)", status, reasons)
	}
	if !hasReason(reasons, "edit_remote_missing:c1") {
		t.Fatalf("want edit_remote_missing:c1, got %v", reasons)
	}
}

// An ExpectedRemote that is NOT observation-derived (a plan echo) must fail
// closed with edit_remote_changed: a plan echo is never trusted as verified
// content, even when its fields happen to match.
func TestValidateApproval_UnobservedExpectedRemoteFailsClosed(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	echo := observed
	echo.ContentObserved = false // a plan echo, not an observation
	plan := workflow.SubmissionPlan{
		Date:             testDate,
		LocalFingerprint: workflow.LocalFingerprint(rec),
		RemoteComplete:   true,
		RemoteObservedAt: testNow,
		Operations: []workflow.PlanOperation{{
			LocalID: "c1", Kind: workflow.PlanOpEdit, RemoteID: "r1",
			Date: testDate, StartTime: "09:00", EndTime: "10:00",
			ClassificationID: "cls-1", Description: "work", Reason: "r",
			ExpectedRemote: &echo,
		}},
	}
	plan.PlanID = workflow.PlanHash(plan)
	workflow.UpsertPlan(&rec, plan)
	a, err := Approve(&rec, plan, testNow)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}

	status, reasons := ValidateApproval(rec, a, completeListing(observed), testNow)
	if status != ApprovalStaleRemote || !hasReason(reasons, "edit_remote_changed:c1") {
		t.Fatalf("an unobserved ExpectedRemote must fail closed with edit_remote_changed, got %q (%v)", status, reasons)
	}
}
