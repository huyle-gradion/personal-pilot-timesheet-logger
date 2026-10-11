package workflow

import (
	"encoding/json"
	"testing"
)

// Classification code/name are RETRIEVAL metadata only: adding them must not
// change LocalFingerprint (so a pre-existing approval stays valid), while an
// ID or activity-type edit must still change it.
func TestLocalFingerprint_ClassificationMetadataHintIsInvariant(t *testing.T) {
	base := Record{Date: "2026-10-06", Confirmed: []ConfirmedInterval{{
		ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		Project: "Proj", Description: "work", Attendance: true,
		ClassificationID: "cls-1", ActivityType: "internal project",
	}}, Review: &ReviewState{Reviewed: true}}
	f1 := LocalFingerprint(base)

	hinted := base
	hinted.Confirmed = append([]ConfirmedInterval(nil), base.Confirmed...)
	hinted.Confirmed[0].ClassificationCode = "INTGRADI2610"
	hinted.Confirmed[0].ClassificationName = "Gradion Intern Academy 2026"
	if LocalFingerprint(hinted) != f1 {
		t.Fatalf("adding code/name retrieval metadata must NOT change the fingerprint")
	}

	idEdit := hinted
	idEdit.Confirmed = append([]ConfirmedInterval(nil), hinted.Confirmed...)
	idEdit.Confirmed[0].ClassificationID = "cls-2"
	if LocalFingerprint(idEdit) == f1 {
		t.Fatalf("an id edit must change the fingerprint")
	}

	typeEdit := hinted
	typeEdit.Confirmed = append([]ConfirmedInterval(nil), hinted.Confirmed...)
	typeEdit.Confirmed[0].ActivityType = "client project"
	if LocalFingerprint(typeEdit) == f1 {
		t.Fatalf("an activity-type edit must change the fingerprint")
	}
}

// A zero-valued re-assert (a refresh) must preserve the retrieval metadata.
func TestMergeConfirmed_PreservesClassificationMetadata(t *testing.T) {
	r := &Record{Confirmed: []ConfirmedInterval{{
		ID: "c1", ClassificationID: "cls-1", ActivityType: "internal project",
		ClassificationCode: "CODE", ClassificationName: "Name",
	}}}
	MergeConfirmed(r, []ConfirmedInterval{{ID: "c1"}})
	ci := r.Confirmed[0]
	if ci.ClassificationCode != "CODE" || ci.ClassificationName != "Name" {
		t.Fatalf("a zero re-assert must preserve the retrieval metadata, got %+v", ci)
	}
}

// A record written before classification_code/name existed loads with empty
// metadata and round-trips byte-stably, preserving plans/approvals/outcomes/
// confirmations. SchemaVersion stays 1 (no migration).
func TestRecord_OldSchemaRoundTripPreservesAndDoesNotMutate(t *testing.T) {
	doc := `{
	  "schema_version": 1,
	  "date": "2026-10-08",
	  "revision": 3,
	  "updated_at": "2026-10-08T10:00:00Z",
	  "confirmed": [{"id":"c1","date":"2026-10-08","start_time":"09:00","end_time":"12:00","project":"Proj","description":"work","attendance":true,"classification_id":"uuid-x","activity_type":"internal project"}],
	  "plans": [{"plan_id":"p1","date":"2026-10-08","local_fingerprint":"lf","operations":[{"local_id":"c1","kind":"create","date":"2026-10-08","start_time":"09:00","end_time":"12:00","classification_id":"uuid-x","description":"work"}]}],
	  "approvals": [{"approval_id":"p1","plan_id":"p1","date":"2026-10-08"}],
	  "outcomes": [{"operation_key":"k","local_id":"c1","action":"create","status":"in_flight","date":"2026-10-08","start_time":"09:00","end_time":"12:00"}]
	}`
	var rec Record
	if err := json.Unmarshal([]byte(doc), &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.SchemaVersion != 1 {
		t.Fatalf("schema version must load as 1, got %d", rec.SchemaVersion)
	}
	if len(rec.Confirmed) != 1 || rec.Confirmed[0].ClassificationCode != "" || rec.Confirmed[0].ClassificationName != "" {
		t.Fatalf("old-schema confirmations must load without metadata: %+v", rec.Confirmed)
	}
	if len(rec.Plans) != 1 || len(rec.Approvals) != 1 || len(rec.Outcomes) != 1 {
		t.Fatalf("plans/approvals/outcomes must be preserved: %+v", rec)
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Record
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	data2, _ := json.Marshal(again)
	if string(data) != string(data2) {
		t.Fatalf("round trip must be byte-stable:\n%s\n%s", data, data2)
	}
	if len(again.Plans) != 1 || len(again.Approvals) != 1 || len(again.Outcomes) != 1 {
		t.Fatalf("round trip must preserve plans/approvals/outcomes: %+v", again)
	}
}
