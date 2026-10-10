package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMergeConfirmed_PreservesReviewFields proves a zero-valued re-assert cannot
// wipe S4 review metadata or user decisions.
func TestMergeConfirmed_PreservesReviewFields(t *testing.T) {
	r := &Record{}
	off := 15
	r.Confirmed = []ConfirmedInterval{{
		ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		Project: "P", Description: "work", Attendance: true, Kind: "work",
		EvidenceIDs: []string{"e1"}, Manual: true,
		OriginalStart: "08:45", OriginalEnd: "10:15", GridAdjusted: true,
		SplitGroupID: "sg1", PartIndex: 2, StartOffsetMin: 15, EndOffsetMin: &off,
	}}

	MergeConfirmed(r, []ConfirmedInterval{{ID: "c1"}})

	got := r.Confirmed[0]
	if !got.Attendance {
		t.Error("Attendance wiped")
	}
	if len(got.EvidenceIDs) != 1 || got.EvidenceIDs[0] != "e1" {
		t.Errorf("EvidenceIDs wiped: %v", got.EvidenceIDs)
	}
	if !got.Manual {
		t.Error("Manual wiped")
	}
	if got.Kind != "work" {
		t.Error("Kind wiped")
	}
	if got.OriginalStart != "08:45" || got.OriginalEnd != "10:15" {
		t.Errorf("Originals wiped: %q/%q", got.OriginalStart, got.OriginalEnd)
	}
	if !got.GridAdjusted {
		t.Error("GridAdjusted wiped")
	}
	if got.SplitGroupID != "sg1" {
		t.Error("SplitGroupID wiped")
	}
	if got.PartIndex != 2 {
		t.Error("PartIndex wiped")
	}
	if got.StartOffsetMin != 15 {
		t.Error("StartOffsetMin wiped")
	}
	if got.EndOffsetMin == nil || *got.EndOffsetMin != 15 {
		t.Error("EndOffsetMin wiped")
	}
	if got.StartTime != "09:00" || got.EndTime != "10:00" {
		t.Errorf("times wiped: %q/%q", got.StartTime, got.EndTime)
	}
	if got.Project != "P" || got.Description != "work" {
		t.Error("text wiped")
	}
}

// TestMergeReviewState_Upserts proves exclusions/attendance upsert by EvidenceID
// and acknowledged sources union.
func TestMergeReviewState_Upserts(t *testing.T) {
	r := &Record{}
	MergeReviewState(r, ReviewState{
		Reviewed:            true,
		ReviewedAt:          time.Unix(1000, 0).UTC(),
		ExcludedEvidence:    []ExcludedEvidence{{EvidenceID: "e1", Reason: "not_attended"}},
		Attendance:          []AttendanceDecision{{EvidenceID: "m1", Attended: false}},
		AcknowledgedSources: []string{"git"},
		LastProposalHash:    "h1",
	})
	MergeReviewState(r, ReviewState{
		ExcludedEvidence:    []ExcludedEvidence{{EvidenceID: "e1", Reason: "user_excluded"}, {EvidenceID: "e2", Reason: "not_attended"}},
		AcknowledgedSources: []string{"cal"},
	})

	if r.Review == nil {
		t.Fatal("nil Review")
	}
	if len(r.Review.ExcludedEvidence) != 2 {
		t.Fatalf("excluded = %d, want 2", len(r.Review.ExcludedEvidence))
	}
	updated := false
	for _, ex := range r.Review.ExcludedEvidence {
		if ex.EvidenceID == "e1" && ex.Reason == "user_excluded" {
			updated = true
		}
	}
	if !updated {
		t.Error("e1 was not upserted")
	}
	if len(r.Review.AcknowledgedSources) != 2 {
		t.Errorf("acknowledged = %v, want 2 entries", r.Review.AcknowledgedSources)
	}
	if !r.Review.Reviewed || r.Review.LastProposalHash != "h1" {
		t.Error("scalar review fields lost")
	}
}

// TestReviewCapacityBound proves the new review bounds are enforced.
func TestReviewCapacityBound(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-06"

	cases := map[string]*ReviewState{
		"excluded": {
			ExcludedEvidence: func() []ExcludedEvidence {
				rows := make([]ExcludedEvidence, MaxReviewExcludedCount+1)
				for i := range rows {
					rows[i] = ExcludedEvidence{EvidenceID: "e"}
				}
				return rows
			}(),
		},
		"attendance": {
			Attendance: func() []AttendanceDecision {
				rows := make([]AttendanceDecision, MaxReviewAttendanceCount+1)
				for i := range rows {
					rows[i] = AttendanceDecision{EvidenceID: "m"}
				}
				return rows
			}(),
		},
	}

	for name, review := range cases {
		t.Run(name, func(t *testing.T) {
			store, err := NewJSONFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewJSONFileStore: %v", err)
			}
			_, err = store.UpdateDay(ctx, date, func(r *Record) error {
				r.Review = review
				return nil
			})
			if !errors.Is(err, ErrCapacityExceeded) {
				t.Fatalf("expected ErrCapacityExceeded, got %v", err)
			}
		})
	}
}

// TestWorkflow_OldSchemaV1FileHasNilReview proves a pre-S4 SchemaVersion-1 file
// loads with Review == nil.
func TestWorkflow_OldSchemaV1FileHasNilReview(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	old := `{"schema_version":1,"date":"2026-10-06","revision":1,"updated_at":"2026-10-06T00:00:00Z",` +
		`"evidence":[],"confirmed":[],"remote_mappings":[],"outcomes":[]}`
	if err := os.WriteFile(filepath.Join(root, "workflow", date+".json"), []byte(old), FilePermPrivate); err != nil {
		t.Fatalf("write old file: %v", err)
	}

	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if got.Review != nil {
		t.Errorf("expected nil Review for an old file, got %+v", got.Review)
	}
}

// TestMergeReviewState_NoAliasing proves MergeReviewState deep-copies slices and
// uses a single deterministic (sorted) acknowledgment ordering (S4L2-9).
func TestMergeReviewState_NoAliasing(t *testing.T) {
	r := &Record{}
	excluded := []ExcludedEvidence{{EvidenceID: "e1", Reason: "a"}}
	attendance := []AttendanceDecision{{EvidenceID: "m1"}}
	ack := []string{"git"}

	MergeReviewState(r, ReviewState{
		ExcludedEvidence:    excluded,
		Attendance:          attendance,
		AcknowledgedSources: ack,
	})

	// Mutating the caller's slices must not change the record.
	excluded[0].Reason = "mutated"
	attendance[0].EvidenceID = "mutated"
	ack[0] = "mutated"

	if r.Review.ExcludedEvidence[0].Reason != "a" {
		t.Error("ExcludedEvidence aliased the caller's slice")
	}
	if r.Review.Attendance[0].EvidenceID != "m1" {
		t.Error("Attendance aliased the caller's slice")
	}
	if r.Review.AcknowledgedSources[0] != "git" {
		t.Error("AcknowledgedSources aliased the caller's slice")
	}

	// Acknowledged sources union deterministically (sorted).
	MergeReviewState(r, ReviewState{AcknowledgedSources: []string{"cal"}})
	got := r.Review.AcknowledgedSources
	if len(got) != 2 || got[0] != "cal" || got[1] != "git" {
		t.Errorf("acknowledged sources not sorted/unioned: %v", got)
	}
}
