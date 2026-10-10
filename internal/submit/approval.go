package submit

import (
	"errors"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ApprovalStatus classifies an approval's validity against the current record,
// its plan, and a fresh observation.
type ApprovalStatus string

const (
	ApprovalValid       ApprovalStatus = "valid"
	ApprovalStaleLocal  ApprovalStatus = "stale_local"
	ApprovalStalePlan   ApprovalStatus = "stale_plan"
	ApprovalStaleRemote ApprovalStatus = "stale_remote"
	ApprovalMissing     ApprovalStatus = "missing"
	ApprovalSuperseded  ApprovalStatus = "superseded"
	// ApprovalInvalidated is an approval explicitly invalidated on the record.
	ApprovalInvalidated ApprovalStatus = "invalidated"
)

// ErrPlanHashMismatch is returned by Approve when a plan's content hash does not
// match its PlanID.
var ErrPlanHashMismatch = errors.New("submit: plan content hash does not match plan.PlanID")

// Approve binds an approval to a plan. It recomputes the plan content hash and
// refuses unless it equals plan.PlanID, then supersedes any prior approval for
// the date and records the new one. It mutates rec.
func Approve(rec *workflow.Record, plan workflow.SubmissionPlan, now time.Time) (workflow.Approval, error) {
	if rec == nil {
		return workflow.Approval{}, errors.New("submit: nil record")
	}
	if plan.PlanID == "" || workflow.PlanHash(plan) != plan.PlanID {
		return workflow.Approval{}, ErrPlanHashMismatch
	}
	approval := workflow.Approval{
		ApprovalID:       plan.PlanID,
		PlanID:           plan.PlanID,
		Date:             plan.Date,
		RecordRevision:   plan.RecordRevision,
		RemoteObservedAt: plan.RemoteObservedAt,
		ApprovedAt:       now,
	}
	workflow.SupersedeApproval(rec, approval.ApprovalID, plan.Date)
	workflow.UpsertApproval(rec, approval)
	return approval, nil
}

// ValidateApproval checks an approval against the current record and a fresh
// observation. It returns the first applicable status and stable reasons. Any
// local, plan, or remote change invalidates the approval.
func ValidateApproval(rec workflow.Record, a workflow.Approval, fresh gradion.Listing, now time.Time) (ApprovalStatus, []string) {
	_ = now
	if strings.TrimSpace(a.ApprovalID) == "" {
		return ApprovalMissing, []string{"no_approval"}
	}
	if a.Superseded {
		return ApprovalSuperseded, []string{"superseded"}
	}
	if a.InvalidatedAt != nil {
		return ApprovalInvalidated, []string{"invalidated:" + a.InvalidatedReason}
	}

	plan, ok := workflow.FindPlan(&rec, a.PlanID)
	if !ok {
		return ApprovalStalePlan, []string{"plan_missing"}
	}
	if plan.PlanID != a.PlanID || workflow.PlanHash(plan) != plan.PlanID {
		return ApprovalStalePlan, []string{"plan_mutated"}
	}
	if workflow.LocalFingerprint(rec) != plan.LocalFingerprint {
		return ApprovalStaleLocal, []string{"local_changed"}
	}

	observedByID := make(map[string]workflow.RemoteState, len(fresh.Entries))
	for _, e := range fresh.Entries {
		observedByID[e.RemoteID] = e
	}
	mappedRemoteIDs := make(map[string]bool, len(rec.RemoteMappings))
	for _, m := range rec.RemoteMappings {
		mappedRemoteIDs[m.RemoteID] = true
	}

	var reasons []string
	for _, op := range plan.Operations {
		switch op.Kind {
		case workflow.PlanOpEdit:
			observed, present := observedByID[op.RemoteID]
			if !present {
				reasons = append(reasons, "edit_remote_missing:"+op.LocalID)
				continue
			}
			if op.ExpectedRemote == nil || !RemoteContentEqual(*op.ExpectedRemote, observed) {
				reasons = append(reasons, "edit_remote_changed:"+op.LocalID)
			}
		case workflow.PlanOpCreate:
			if !fresh.Complete {
				reasons = append(reasons, "create_listing_incomplete:"+op.LocalID)
				continue
			}
			if dup := overlappingUnmappedRemote(op.Date, op.StartTime, op.EndTime, fresh.Entries, mappedRemoteIDs); dup != "" {
				reasons = append(reasons, "create_duplicate_remote:"+op.LocalID)
			}
		}
	}
	if len(reasons) > 0 {
		return ApprovalStaleRemote, dedupSorted(reasons)
	}
	return ApprovalValid, nil
}

// RemoteContentEqual compares two remote states' content. It ignores ObservedAt
// and RemoteID (the identity key), but treats Date as content: the server accepts
// date on edit, so a remote that moved to another date is a change.
func RemoteContentEqual(a, b workflow.RemoteState) bool {
	return a.Date == b.Date &&
		a.StartTime == b.StartTime &&
		a.EndTime == b.EndTime &&
		a.ClassificationID == b.ClassificationID &&
		a.Description == b.Description &&
		a.Task == b.Task &&
		boolPtrEqual(a.Billable, b.Billable)
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
