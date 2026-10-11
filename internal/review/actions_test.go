package review

import (
	"errors"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// confirmedRecord returns a record with one confirmed interval c1.
func confirmedRecord() workflow.Record {
	return workflow.Record{
		Date: "2026-10-06",
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "old", Attendance: true,
		}},
	}
}

func TestSetDescription(t *testing.T) {
	r := confirmedRecord()
	if err := SetDescription(&r, "c1", "  new text  "); err != nil {
		t.Fatalf("SetDescription: %v", err)
	}
	if r.Confirmed[0].Description != "new text" {
		t.Fatalf("want the trimmed description, got %q", r.Confirmed[0].Description)
	}
	// An empty description clears it (no fabricated text).
	if err := SetDescription(&r, "c1", "   "); err != nil {
		t.Fatalf("SetDescription(empty): %v", err)
	}
	if r.Confirmed[0].Description != "" {
		t.Fatalf("want an empty description, got %q", r.Confirmed[0].Description)
	}
	if err := SetDescription(&r, "nope", "x"); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("want ErrBlockNotFound, got %v", err)
	}
	if err := SetDescription(nil, "c1", "x"); !errors.Is(err, ErrNilRecord) {
		t.Fatalf("want ErrNilRecord, got %v", err)
	}
}

func TestSetClassification(t *testing.T) {
	r := confirmedRecord()
	if err := SetClassification(&r, "c1", "cls-1", "internal project"); err != nil {
		t.Fatalf("SetClassification: %v", err)
	}
	ci := r.Confirmed[0]
	if ci.ClassificationID != "cls-1" || ci.ActivityType != "internal project" {
		t.Fatalf("want cls-1/internal project, got %q/%q", ci.ClassificationID, ci.ActivityType)
	}
	// An empty classification id is refused: there is no default.
	if err := SetClassification(&r, "c1", "   ", "x"); !errors.Is(err, ErrInvalidClassification) {
		t.Fatalf("want ErrInvalidClassification, got %v", err)
	}
	if err := SetClassification(&r, "nope", "cls-1", "x"); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("want ErrBlockNotFound, got %v", err)
	}
	if err := SetClassification(nil, "c1", "cls-1", "x"); !errors.Is(err, ErrNilRecord) {
		t.Fatalf("want ErrNilRecord, got %v", err)
	}
}
