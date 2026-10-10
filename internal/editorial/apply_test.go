package editorial

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
)

// TestApply_OnlyTouchesDescription asserts every non-Description field of every
// block is byte-identical after Apply.
func TestApply_OnlyTouchesDescription(t *testing.T) {
	start := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	orig := proposal.Proposal{
		Date: "2026-10-06",
		Work: []proposal.ProposedTimeBlock{{
			ID: "w1", Kind: proposal.BlockWork, Date: "2026-10-06",
			Start: "09:00", End: "10:00", StartAt: start, EndAt: end,
			Duration:    evidence.KnownDuration(60),
			Project:     proposal.ProjectResolution{Project: "Proj", Source: proposal.ProjectSourceRepoMap, Resolved: true},
			Description: "old", EvidenceIDs: []string{"e1", "e2"},
			Attendance: proposal.AttendanceAttended, Preselected: true, Collision: false,
		}},
		Meetings: []proposal.ProposedTimeBlock{{
			ID: "m1", Kind: proposal.BlockMeeting, Date: "2026-10-06",
			Start: "11:00", End: "12:00", Description: "meet",
			Attendance: proposal.AttendanceUnresolved, EvidenceIDs: []string{"e3"},
		}},
		Manual: []proposal.ProposedTimeBlock{{ID: "man1", Kind: proposal.BlockManual, Description: "manual"}},
	}
	beforeJSON, _ := json.Marshal(orig)

	applied := Apply(orig, []Suggestion{
		{TargetID: "w1", Text: Text{Title: "t", Description: "new work"}},
		{TargetID: "m1", Text: Text{Title: "t", Description: "new meet"}},
		{TargetID: "nope", Text: Text{Title: "t", Description: "ignored"}},
	})

	if applied.Work[0].Description != "new work" {
		t.Errorf("work description not applied: %q", applied.Work[0].Description)
	}
	if applied.Meetings[0].Description != "new meet" {
		t.Errorf("meeting description not applied: %q", applied.Meetings[0].Description)
	}
	if applied.Manual[0].Description != "manual" {
		t.Errorf("an unmatched block must be untouched: %q", applied.Manual[0].Description)
	}

	// Every other field is byte-identical.
	wantWork := orig.Work[0]
	wantWork.Description = "new work"
	if !reflect.DeepEqual(applied.Work[0], wantWork) {
		t.Errorf("Apply changed more than Description:\n got %+v\nwant %+v", applied.Work[0], wantWork)
	}
	wantMeeting := orig.Meetings[0]
	wantMeeting.Description = "new meet"
	if !reflect.DeepEqual(applied.Meetings[0], wantMeeting) {
		t.Errorf("Apply changed more than Description on a meeting:\n got %+v\nwant %+v", applied.Meetings[0], wantMeeting)
	}

	// The input is not mutated.
	if orig.Work[0].Description != "old" {
		t.Errorf("Apply mutated the input proposal")
	}
	afterJSON, _ := json.Marshal(orig)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("Apply mutated the input proposal JSON")
	}
}
