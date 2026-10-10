package review

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func hasReason(rd ReadinessResult, want string) bool {
	for _, r := range rd.Reasons {
		if r == want {
			return true
		}
	}
	return false
}

// TestReadiness_BlocksThenClears proves readiness blocks an unconfirmed duration
// and an unresolved project, then clears once both are resolved.
func TestReadiness_BlocksThenClears(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	r.Confirmed = []workflow.ConfirmedInterval{{
		ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "09:00", EvidenceIDs: []string{"e1"},
	}}

	rd := Readiness(r)
	if !rd.Blocked {
		t.Fatalf("expected blocked, got %+v", rd)
	}
	if !hasReason(rd, "unconfirmed_duration:c1") {
		t.Errorf("missing unconfirmed_duration reason: %v", rd.Reasons)
	}
	if !hasReason(rd, "unresolved_project:c1") {
		t.Errorf("missing unresolved_project reason: %v", rd.Reasons)
	}

	if err := TrimWork(&r, "c1", "09:00", "10:00"); err != nil {
		t.Fatalf("TrimWork: %v", err)
	}
	if err := ResolveProject(&r, "c1", "Proj"); err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if err := MarkReviewed(&r, ""); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	if rd := Readiness(r); rd.Blocked {
		t.Errorf("expected ready after confirm+review, got %+v", rd)
	}
}

// TestReadiness_NonEmptyRequiresReview proves a resolved non-empty day still
// blocks until it is reviewed.
func TestReadiness_NonEmptyRequiresReview(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	r.Confirmed = []workflow.ConfirmedInterval{{
		ID: "c1", StartTime: "09:00", EndTime: "10:00", Project: "P",
	}}
	rd := Readiness(r)
	if !rd.Blocked || !hasReason(rd, "not_reviewed") {
		t.Fatalf("expected not_reviewed block, got %+v", rd)
	}
	if err := MarkReviewed(&r, "h"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	if rd := Readiness(r); rd.Blocked {
		t.Errorf("reviewed resolved day must be ready, got %+v", rd)
	}
}

// TestReadiness_EmptyFlagDoesNotBypass proves a stale Reviewed+Empty flag does not
// skip checks when confirmed intervals exist (S4L2-2).
func TestReadiness_EmptyFlagDoesNotBypass(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", Review: &workflow.ReviewState{Reviewed: true, Empty: true}}
	r.Confirmed = []workflow.ConfirmedInterval{{
		ID: "c1", StartTime: "09:00", EndTime: "09:00", EvidenceIDs: []string{"e1"},
	}}
	rd := Readiness(r)
	if !rd.Blocked {
		t.Fatalf("Empty must not bypass checks with confirmed intervals, got %+v", rd)
	}
	if !hasReason(rd, "unconfirmed_duration:c1") || !hasReason(rd, "unresolved_project:c1") {
		t.Errorf("expected unresolved reasons, got %v", rd.Reasons)
	}
}

// TestReadiness_NotAttendedMeetingAsWork proves a not-attended meeting that is
// still represented as work blocks readiness.
func TestReadiness_NotAttendedMeetingAsWork(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	r.Review = &workflow.ReviewState{Attendance: []workflow.AttendanceDecision{{EvidenceID: "m1", Attended: false}}}
	r.Confirmed = []workflow.ConfirmedInterval{{
		ID: "c1", StartTime: "09:00", EndTime: "10:00", Project: "P", EvidenceIDs: []string{"m1"},
	}}

	rd := Readiness(r)
	if !rd.Blocked || !hasReason(rd, "not_attended_as_work:c1") {
		t.Errorf("expected not_attended_as_work block, got %+v", rd)
	}
}

// TestReadiness_ReviewedEmptyNotBlocked proves a reviewed empty day is ready.
func TestReadiness_ReviewedEmptyNotBlocked(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", Review: &workflow.ReviewState{Reviewed: true, Empty: true}}
	if rd := Readiness(r); rd.Blocked {
		t.Errorf("reviewed empty day must not be blocked: %+v", rd)
	}
}

// TestReadiness_IncompleteCollection proves an incomplete collection blocks until
// acknowledged.
func TestReadiness_IncompleteCollection(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", Collection: &evidence.CollectionSummary{
		Date:     "2026-10-06",
		Complete: false,
		Sources:  []evidence.SourceResult{{SourceID: "git", Status: evidence.StatusIncomplete, ErrorCode: evidence.CodeSourceError}},
	}}
	if rd := Readiness(r); !rd.Blocked || !hasReason(rd, "incomplete_collection") {
		t.Fatalf("expected incomplete_collection block, got %+v", rd)
	}
	r.Review = &workflow.ReviewState{AcknowledgedSources: []string{"git"}}
	if rd := Readiness(r); rd.Blocked {
		t.Errorf("acknowledged collection must not block: %+v", rd)
	}
}

// TestReadiness_OverlapManualManual proves overlap is validated across ALL
// confirmed blocks, including manual↔manual (P4).
func TestReadiness_OverlapManualManual(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06"}
	r.Confirmed = []workflow.ConfirmedInterval{
		{ID: "a", StartTime: "09:00", EndTime: "10:00", Project: "P"},
		{ID: "b", StartTime: "09:30", EndTime: "10:30", Project: "P", Manual: true},
	}
	rd := Readiness(r)
	if !rd.Blocked || !hasReason(rd, "overlap:a:b") {
		t.Errorf("expected overlap:a:b block, got %+v", rd)
	}
}
