package submit

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
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

// DEFECT-6: marking a row not applied keeps the originating failure code in the
// ADDITIVE OriginErrorCode instead of erasing it. The row's ErrorCode stays the
// resolution code, and every pre-write field (all six key inputs, plus
// ClassificationID and IntentAt) is asserted untouched; RecordedAt is stamped
// by the path by design and not part of the claim.
func TestResolveUnresolved_NotAppliedPreservesOriginCode(t *testing.T) {
	rec := reviewedRecord()
	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpEdit), LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	const origin = gradion.CodeWriteContentMismatch
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", RemoteID: "remote-1", Action: string(workflow.PlanOpEdit),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
		Description: "work", ErrorCode: origin,
	}}

	if err := ResolveUnresolved(&rec, completeListing(), []Resolution{{OperationKey: key, Kind: ResolutionNotApplied}}, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	got := rec.Outcomes[0]
	if got.Status != workflow.OutcomeFailed || !got.Resolved {
		t.Fatalf("want failed+resolved, got %+v", got)
	}
	if got.ErrorCode != CodeResolvedNotApplied {
		t.Errorf("the row's resolution code must stay the not-applied code, got %q", got.ErrorCode)
	}
	if got.OriginErrorCode != origin {
		t.Errorf("want the originating failure preserved, got %q", got.OriginErrorCode)
	}
	if got.RecordedAt != testNow {
		t.Errorf("the resolution stamp must be RecordedAt=now, got %v", got.RecordedAt)
	}
	if got.OperationKey != key || got.LocalID != "c1" || got.Action != string(workflow.PlanOpEdit) ||
		got.Date != testDate || got.StartTime != "09:00" || got.EndTime != "10:00" ||
		got.RemoteID != "remote-1" || got.Description != "work" ||
		got.ClassificationID != "" || !got.IntentAt.IsZero() {
		t.Errorf("marking not applied must not touch the row's other fields, got %+v", got)
	}
}

// DEFECT-6: a row that carried no failure code gains no origin artifact.
func TestMarkNotApplied_EmptyOriginStaysEmpty(t *testing.T) {
	rec := reviewedRecord()
	markNotApplied(&rec, workflow.OperationOutcome{
		OperationKey: "k1", LocalID: "c1", Action: string(workflow.PlanOpCreate),
	}, testNow)

	got := rec.Outcomes[0]
	if got.Status != workflow.OutcomeFailed || !got.Resolved || got.ErrorCode != CodeResolvedNotApplied {
		t.Fatalf("want failed+resolved with the not-applied code, got %+v", got)
	}
	if got.OriginErrorCode != "" {
		t.Errorf("an empty origin code must stay empty, got %q", got.OriginErrorCode)
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

	// A CLASSIFICATION-CHANGING edit: the outcome row carries the NEW
	// classification with every other field (Date/Start/End/Description)
	// UNCHANGED from the baseline. The baseline and the observed remote share the
	// description "old text", so the classification is the ONLY field that can
	// distinguish post-edit from pre-edit content. The post-edit projection must
	// carry the dispatched classification for either inference to be correct.
	makeClassRec := func(outcomeClass string) workflow.Record {
		rec := reviewedRecord()
		rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "old text"))}
		rec.Outcomes = []workflow.OperationOutcome{{
			OperationKey: key, LocalID: "c1", RemoteID: "r1", Action: string(workflow.PlanOpEdit),
			Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
			Description: "old text", ClassificationID: outcomeClass,
		}}
		return rec
	}

	// R1a (harm i): the edit DID apply. The listing shows the NEW classification
	// cls-2 with the same description. It must resolve CONFIRMED. Before the fix
	// the projection keeps the pre-edit cls-1, so neither comparison matches and
	// the row stays unresolved.
	t.Run("classification edit applied resolves confirmed", func(t *testing.T) {
		rec := makeClassRec("cls-2")
		if err := ResolveUnresolved(&rec, completeListing(remoteState("r1", "09:00", "10:00", "cls-2", "old text")), nil, testNow); err != nil {
			t.Fatalf("ResolveUnresolved: %v", err)
		}
		if len(rec.Outcomes) != 1 {
			t.Fatalf("want exactly one outcome row, got %d: %+v", len(rec.Outcomes), rec.Outcomes)
		}
		if rec.Outcomes[0].Status != workflow.OutcomeConfirmed || !rec.Outcomes[0].Resolved {
			t.Fatalf("a classification-changing edit that applied must resolve confirmed, got %+v", rec.Outcomes[0])
		}
	})

	// R1b (harm ii): the edit did NOT apply. The listing is UNCHANGED (still
	// cls-1). It must resolve FAILED. Before the fix the classification-blind
	// projection equals the unchanged baseline on every compared field, so
	// RemoteContentEqual wrongly confirms the unapplied edit.
	t.Run("classification-only edit not applied resolves failed", func(t *testing.T) {
		rec := makeClassRec("cls-2")
		if err := ResolveUnresolved(&rec, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "old text")), nil, testNow); err != nil {
			t.Fatalf("ResolveUnresolved: %v", err)
		}
		if len(rec.Outcomes) != 1 {
			t.Fatalf("want exactly one outcome row, got %d: %+v", len(rec.Outcomes), rec.Outcomes)
		}
		if rec.Outcomes[0].Status != workflow.OutcomeFailed || !rec.Outcomes[0].Resolved {
			t.Fatalf("a classification-only edit that did NOT apply must resolve failed, got %+v", rec.Outcomes[0])
		}
	})
}

// DEFECT-5: the inference-confirm refresh is observation-only. Confirming a
// classification-changing edit carries the new classification into LastObserved
// and leaves the top-level binding snapshot, and thus LocalFingerprint, at the
// values of the last binding event. The top-level Description ("work", the local
// intent) deliberately differs from the observed one ("old text"), so a
// peer-field-only refresh cannot slip through. Under the rejected alternative
// (refreshing the binding fields from the observation) the classification,
// description and fingerprint assertions go red.
func TestResolveEditFromObservation_ConfirmKeepsBindingClassification(t *testing.T) {
	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpEdit), RemoteID: "r1", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	rec := reviewedRecord()
	// The binding snapshot is local intent: its Description need not match the
	// observed content it was bound to.
	rec.RemoteMappings = []workflow.RemoteMapping{{
		LocalID: "c1", RemoteID: "r1", Date: testDate,
		StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work",
		LastObserved: remoteState("r1", "09:00", "10:00", "cls-1", "old text"),
	}}
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", RemoteID: "r1", Action: string(workflow.PlanOpEdit),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
		Description: "old text", ClassificationID: "cls-2",
	}}
	before := workflow.LocalFingerprint(rec)

	observed := remoteState("r1", "09:00", "10:00", "cls-2", "old text")
	if err := ResolveUnresolved(&rec, completeListing(observed), nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}

	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed || !rec.Outcomes[0].Resolved {
		t.Fatalf("the applied classification edit must resolve confirmed, got %+v", rec.Outcomes)
	}
	if got := rec.RemoteMappings[0].LastObserved.ClassificationID; got != "cls-2" {
		t.Errorf("LastObserved must carry the observed classification: want cls-2, got %q", got)
	}
	if got := rec.RemoteMappings[0].LastObserved.Description; got != "old text" {
		t.Errorf("LastObserved must carry the observed description: want old text, got %q", got)
	}
	if got := rec.RemoteMappings[0].ClassificationID; got != "cls-1" {
		t.Errorf("a confirm must not move the binding classification: want cls-1, got %q", got)
	}
	if got := rec.RemoteMappings[0].Description; got != "work" {
		t.Errorf("a confirm must not move the binding description: want work, got %q", got)
	}
	if after := workflow.LocalFingerprint(rec); after != before {
		t.Errorf("an observation-only refresh must not change LocalFingerprint, %q -> %q", before, after)
	}
}

// A legacy plan-echo baseline (LastObserved.ContentObserved false) must NEVER be
// trusted for inference: even post-edit content present leaves the edit
// unresolved rather than confirming against an unverified baseline.
func TestResolveUnresolved_EditInferenceIgnoresUnobservedBaseline(t *testing.T) {
	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpEdit), RemoteID: "r1", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	rec := reviewedRecord()
	baseline := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	baseline.ContentObserved = false
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", baseline)}
	rec.Outcomes = []workflow.OperationOutcome{{
		OperationKey: key, LocalID: "c1", RemoteID: "r1", Action: string(workflow.PlanOpEdit),
		Status: workflow.OutcomeUncertain, Date: testDate, StartTime: "09:00", EndTime: "10:00",
		Description: "work",
	}}

	post := completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "work"))
	if err := ResolveUnresolved(&rec, post, nil, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if rec.Outcomes[0].Resolved {
		t.Fatalf("an unobserved baseline must leave the edit unresolved, got %+v", rec.Outcomes[0])
	}
}
