package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlanHash_DeterministicAndExcludesAuditFields(t *testing.T) {
	base := SubmissionPlan{
		Date:             "2026-10-06",
		LocalFingerprint: "fp",
		RemoteObservedAt: time.Unix(1000, 0).UTC(),
		RemoteComplete:   true,
		Operations: []PlanOperation{
			{LocalID: "b", Kind: PlanOpCreate, Date: "2026-10-06"},
			{LocalID: "a", Kind: PlanOpEdit, RemoteID: "r1"},
		},
	}
	h1 := PlanHash(base)
	if h1 == "" {
		t.Fatal("empty hash")
	}

	reordered := base
	reordered.Operations = []PlanOperation{base.Operations[1], base.Operations[0]}
	if PlanHash(reordered) != h1 {
		t.Error("operation order changed the hash")
	}

	audited := base
	audited.PlanID = "xyz"
	audited.CreatedAt = time.Now().UTC()
	audited.RecordRevision = 99
	if PlanHash(audited) != h1 {
		t.Error("audit-only fields (PlanID/CreatedAt/RecordRevision) changed the hash")
	}

	changed := base
	changed.RemoteComplete = false
	if PlanHash(changed) == h1 {
		t.Error("a content change did not change the hash")
	}
}

func TestLocalFingerprint_ExcludesRevisionAndTracksEdits(t *testing.T) {
	rec := Record{
		Date:     "2026-10-06",
		Revision: 1,
		Confirmed: []ConfirmedInterval{
			{ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Project: "P"},
		},
	}
	f1 := LocalFingerprint(rec)
	rec.Revision = 5
	if LocalFingerprint(rec) != f1 {
		t.Error("a revision bump alone changed the fingerprint")
	}
	rec.Confirmed[0].Description = "edited"
	if LocalFingerprint(rec) == f1 {
		t.Error("an edited confirmed description did not change the fingerprint")
	}
}

// F5: the plan hash is a pure function of the operation multiset, even when
// operations share (LocalID, Kind, RemoteID) and differ only in other content.
func TestPlanHash_DuplicateKeyOperationsOrderIndependent(t *testing.T) {
	mk := func(desc string) PlanOperation {
		return PlanOperation{LocalID: "c1", Kind: PlanOpEdit, RemoteID: "r1", Date: "2026-10-06", Description: desc}
	}
	p1 := SubmissionPlan{Date: "2026-10-06", Operations: []PlanOperation{mk("a"), mk("b")}}
	p2 := SubmissionPlan{Date: "2026-10-06", Operations: []PlanOperation{mk("b"), mk("a")}}
	if PlanHash(p1) != PlanHash(p2) {
		t.Error("reordering operations that share (LocalID, Kind, RemoteID) changed the hash")
	}

	// Identical duplicate operations are interchangeable.
	p3 := SubmissionPlan{Date: "2026-10-06", Operations: []PlanOperation{mk("a"), mk("a")}}
	p4 := SubmissionPlan{Date: "2026-10-06", Operations: []PlanOperation{mk("a"), mk("a")}}
	if PlanHash(p3) != PlanHash(p4) {
		t.Error("identical operations must hash identically")
	}
}

// W1: a change to the chosen classification OR its derived activity type changes
// the local fingerprint, so an existing approval is invalidated.
func TestLocalFingerprint_TracksClassificationAndActivityType(t *testing.T) {
	rec := Record{
		Date: "2026-10-06",
		Confirmed: []ConfirmedInterval{{
			ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Project: "P",
			ClassificationID: "cls-1", ActivityType: "internal project",
		}},
	}
	base := LocalFingerprint(rec)

	rec.Confirmed[0].ClassificationID = "cls-2"
	if LocalFingerprint(rec) == base {
		t.Error("a classification change must change the fingerprint")
	}
	rec.Confirmed[0].ClassificationID = "cls-1"
	rec.Confirmed[0].ActivityType = "client project"
	if LocalFingerprint(rec) == base {
		t.Error("an activity-type change must change the fingerprint")
	}
}

// T6: LocalFingerprint covers a mapping change (identity fields, not LastObserved).
func TestLocalFingerprint_CoversMappingChange(t *testing.T) {
	rec := Record{
		Date: "2026-10-06",
		Confirmed: []ConfirmedInterval{
			{ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Project: "P"},
		},
		RemoteMappings: []RemoteMapping{
			{LocalID: "c1", RemoteID: "r1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Description: "work"},
		},
	}
	f1 := LocalFingerprint(rec)

	// A refresh of LastObserved must NOT change the fingerprint.
	rec.RemoteMappings[0].LastObserved = RemoteState{RemoteID: "r1", Description: "changed remotely"}
	if LocalFingerprint(rec) != f1 {
		t.Error("refreshing LastObserved must not change the fingerprint")
	}

	// A change to a mapping identity field must change the fingerprint.
	rec.RemoteMappings[0].RemoteID = "r2"
	if LocalFingerprint(rec) == f1 {
		t.Error("a mapping remote-id change must change the fingerprint")
	}
}

func TestMergeConfirmed_PreservesClassificationID(t *testing.T) {
	r := &Record{}
	r.Confirmed = []ConfirmedInterval{{ID: "c1", ClassificationID: "override"}}
	MergeConfirmed(r, []ConfirmedInterval{{ID: "c1"}})
	if r.Confirmed[0].ClassificationID != "override" {
		t.Errorf("ClassificationID wiped by re-assert: %q", r.Confirmed[0].ClassificationID)
	}
}

func TestApproval_Helpers(t *testing.T) {
	r := &Record{Date: "2026-10-06"}
	now := time.Unix(1000, 0).UTC()
	UpsertApproval(r, Approval{ApprovalID: "a1", PlanID: "p1", Date: "2026-10-06", ApprovedAt: now})
	UpsertApproval(r, Approval{ApprovalID: "a2", PlanID: "p2", Date: "2026-10-06", ApprovedAt: now.Add(time.Hour)})

	if a, ok := ActiveApproval(r, "2026-10-06"); !ok || a.ApprovalID != "a2" {
		t.Errorf("expected the latest active approval a2, got %+v (ok=%v)", a, ok)
	}

	SupersedeApproval(r, "a2", "2026-10-06")
	if !r.Approvals[0].Superseded {
		t.Error("a1 was not superseded")
	}

	InvalidateApproval(r, "a2", "remote_changed", now)
	if r.Approvals[1].InvalidatedAt == nil || r.Approvals[1].InvalidatedReason != "remote_changed" {
		t.Errorf("a2 was not invalidated: %+v", r.Approvals[1])
	}
	if _, ok := ActiveApproval(r, "2026-10-06"); ok {
		t.Error("expected no active approval after supersede+invalidate")
	}
}

func TestCapacity_PlansAndApprovals(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-06"
	cases := map[string]func(*Record){
		"plans":     func(r *Record) { r.Plans = make([]SubmissionPlan, MaxPlansCount+1) },
		"approvals": func(r *Record) { r.Approvals = make([]Approval, MaxApprovalsCount+1) },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			store, err := NewJSONFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewJSONFileStore: %v", err)
			}
			_, err = store.UpdateDay(ctx, date, func(r *Record) error { set(r); return nil })
			if !errors.Is(err, ErrCapacityExceeded) {
				t.Fatalf("expected ErrCapacityExceeded, got %v", err)
			}
		})
	}
}

func TestWorkflow_PlansAndApprovalsPersist(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	plan := SubmissionPlan{PlanID: "p1", Date: date, Operations: []PlanOperation{{LocalID: "c1", Kind: PlanOpCreate}}}
	approval := Approval{ApprovalID: "p1", PlanID: "p1", Date: date}
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		UpsertPlan(r, plan)
		UpsertApproval(r, approval)
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(got.Plans) != 1 || got.Plans[0].PlanID != "p1" {
		t.Errorf("plans did not survive reload: %+v", got.Plans)
	}
	if len(got.Approvals) != 1 {
		t.Errorf("approvals did not survive reload: %+v", got.Approvals)
	}
	if p, ok := FindPlan(got, "p1"); !ok || len(p.Operations) != 1 {
		t.Errorf("FindPlan: %+v (ok=%v)", p, ok)
	}
	if a, ok := ActiveApproval(got, date); !ok || a.ApprovalID != "p1" {
		t.Errorf("ActiveApproval: %+v (ok=%v)", a, ok)
	}
}

func TestWorkflow_OldSchemaV1FileHasNilPlansAndApprovals(t *testing.T) {
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
		t.Fatalf("old SchemaVersion 1 file must remain readable, got %v", err)
	}
	if got.Plans != nil || got.Approvals != nil {
		t.Errorf("expected nil additive fields, got plans=%+v approvals=%+v", got.Plans, got.Approvals)
	}
}
