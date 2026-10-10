package submit

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func createKey(localID, start, end string) string {
	return workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpCreate), LocalID: localID,
		StartTime: start, EndTime: end,
	})
}

func TestRestartGate_BlocksOnUnresolved(t *testing.T) {
	rec := reviewedRecord()

	rec.Outcomes = []workflow.OperationOutcome{{OperationKey: "k1", LocalID: "c1", Status: workflow.OutcomeInFlight}}
	if blocked, reasons := RestartGate(rec); !blocked || !hasReason(reasons, "in_flight:k1") {
		t.Fatalf("want an in_flight block, got %v", reasons)
	}

	rec.Outcomes = []workflow.OperationOutcome{
		{OperationKey: "k2", LocalID: "c1", Status: workflow.OutcomeConfirmed, Resolved: true},
		{OperationKey: "k3", LocalID: "c1", Status: workflow.OutcomeFailed, Resolved: true},
	}
	if blocked, reasons := RestartGate(rec); blocked {
		t.Fatalf("resolved outcomes must not block, got %v", reasons)
	}

	rec.Outcomes = []workflow.OperationOutcome{{OperationKey: "k4", LocalID: "c1", Status: workflow.OutcomeUnidentified}}
	if blocked, reasons := RestartGate(rec); !blocked || !hasReason(reasons, "unidentified:k4") {
		t.Fatalf("want an unidentified block, got %v", reasons)
	}
}

func TestResolveUnresolved_AdoptConfirmsAndMaps(t *testing.T) {
	rec := reviewedRecord()
	key := createKey("c1", "09:00", "10:00")
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", Action: string(workflow.PlanOpCreate),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
	}}
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	decisions := []Resolution{{OperationKey: key, Kind: ResolutionAdopt, RemoteID: "r1", LocalID: "c1"}}

	if err := ResolveUnresolved(&rec, completeListing(observed), decisions, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed || !rec.Outcomes[0].Resolved {
		t.Fatalf("want a confirmed resolved outcome, got %+v", rec.Outcomes)
	}
	if rec.Outcomes[0].RemoteID != "r1" {
		t.Errorf("want the adopted remote id, got %q", rec.Outcomes[0].RemoteID)
	}
	if len(rec.RemoteMappings) != 1 || rec.RemoteMappings[0].LocalID != "c1" || rec.RemoteMappings[0].RemoteID != "r1" {
		t.Fatalf("want a mapping c1->r1, got %+v", rec.RemoteMappings)
	}
	if blocked, reasons := RestartGate(rec); blocked {
		t.Errorf("a resolved record must not block the restart gate, got %v", reasons)
	}
}

func TestResolveUnresolved_NotAppliedMarksFailed(t *testing.T) {
	rec := reviewedRecord()
	key := createKey("c1", "09:00", "10:00")
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", Action: string(workflow.PlanOpCreate),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
	}}

	if err := ResolveUnresolved(&rec, completeListing(), []Resolution{{OperationKey: key, Kind: ResolutionNotApplied}}, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if rec.Outcomes[0].Status != workflow.OutcomeFailed || !rec.Outcomes[0].Resolved {
		t.Fatalf("want failed+resolved, got %+v", rec.Outcomes[0])
	}
	if rec.Outcomes[0].ErrorCode != CodeResolvedNotApplied {
		t.Errorf("want the stable not-applied code, got %q", rec.Outcomes[0].ErrorCode)
	}
	if len(rec.RemoteMappings) != 0 {
		t.Errorf("not_applied must not create a mapping, got %+v", rec.RemoteMappings)
	}
	if blocked, reasons := RestartGate(rec); blocked {
		t.Errorf("a resolved failure must not block the restart gate, got %v", reasons)
	}
}

func TestResolveUnresolved_NeverAutoResolvesCreateFromAbsence(t *testing.T) {
	rec := reviewedRecord()
	key := createKey("c1", "09:00", "10:00")
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", Action: string(workflow.PlanOpCreate),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
	}}

	// A COMPLETE listing that lacks the remote is NOT proof the create failed.
	if err := ResolveUnresolved(&rec, completeListing(), nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if rec.Outcomes[0].Status != workflow.OutcomeUncertain || rec.Outcomes[0].Resolved {
		t.Fatalf("a create must stay unresolved without an explicit decision, got %+v", rec.Outcomes[0])
	}
	if blocked, reasons := RestartGate(rec); !blocked {
		t.Errorf("an unresolved create must still block the restart gate, got %v", reasons)
	}
}

func TestResolveUnresolved_EditInference(t *testing.T) {
	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpEdit), RemoteID: "r1", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	makeRec := func() workflow.Record {
		rec := reviewedRecord()
		rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "old text"))}
		rec.Outcomes = []workflow.OperationOutcome{{
			OperationKey: key, LocalID: "c1", RemoteID: "r1", Action: string(workflow.PlanOpEdit),
			Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
			Description: "work",
		}}
		return rec
	}

	// Post-edit content present: confirmed.
	post := makeRec()
	if err := ResolveUnresolved(&post, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "work")), nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if post.Outcomes[0].Status != workflow.OutcomeConfirmed || !post.Outcomes[0].Resolved {
		t.Fatalf("post-edit content must confirm the edit, got %+v", post.Outcomes[0])
	}

	// Pre-edit content present: failed (safe).
	pre := makeRec()
	if err := ResolveUnresolved(&pre, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "old text")), nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if pre.Outcomes[0].Status != workflow.OutcomeFailed || !pre.Outcomes[0].Resolved {
		t.Fatalf("pre-edit content must fail the edit, got %+v", pre.Outcomes[0])
	}

	// Absent remote: left unresolved.
	absent := makeRec()
	if err := ResolveUnresolved(&absent, completeListing(), nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if absent.Outcomes[0].Resolved {
		t.Fatalf("an absent edit remote must stay unresolved, got %+v", absent.Outcomes[0])
	}
}
